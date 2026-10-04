// Command build-country converts the IPinfo Lite country CSV into a
// GeoLite2-Country-compatible MaxMind database.
package main

import (
	"compress/gzip"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"strings"

	"github.com/maxmind/mmdbwriter/v2"
	"github.com/maxmind/mmdbwriter/v2/mmdbtype"
)

type options struct {
	input  string
	output string
}

func parsePrefix(value string) (netip.Prefix, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Prefix{}, errors.New("empty network")
	}
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return netip.Prefix{}, err
		}
		return prefix.Masked(), nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(address, address.BitLen()), nil
}

func validCountryCode(value string) bool {
	if len(value) != 2 {
		return false
	}
	for i := range value {
		if value[i] < 'A' || value[i] > 'Z' {
			return false
		}
	}
	return true
}

func record(countryCode, countryName, continentCode, continentName string) mmdbtype.Map {
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))
	countryName = strings.TrimSpace(countryName)
	continentCode = strings.ToUpper(strings.TrimSpace(continentCode))
	continentName = strings.TrimSpace(continentName)

	country := mmdbtype.Map{
		mmdbtype.String("iso_code"): mmdbtype.String(countryCode),
	}
	if countryName != "" {
		country[mmdbtype.String("names")] = mmdbtype.Map{
			mmdbtype.String("en"): mmdbtype.String(countryName),
		}
	}

	result := mmdbtype.Map{
		mmdbtype.String("country"): country,
	}
	if continentCode != "" {
		continent := mmdbtype.Map{
			mmdbtype.String("code"): mmdbtype.String(continentCode),
		}
		if continentName != "" {
			continent[mmdbtype.String("names")] = mmdbtype.Map{
				mmdbtype.String("en"): mmdbtype.String(continentName),
			}
		}
		result[mmdbtype.String("continent")] = continent
	}
	return result
}

func run(opts options) error {
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType:            "GeoLite2-Country",
		Description:             map[string]string{"en": "IPinfo Lite converted to GeoLite2-Country"},
		Languages:               []string{"en"},
		IPVersion:               6,
		RecordSize:              28,
		IncludeReservedNetworks: true,
	})
	if err != nil {
		return fmt.Errorf("create MMDB writer: %w", err)
	}

	input, err := os.Open(opts.input)
	if err != nil {
		return fmt.Errorf("open %s: %w", opts.input, err)
	}
	defer input.Close()

	reader, err := gzip.NewReader(input)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer reader.Close()

	csvReader := csv.NewReader(reader)
	csvReader.ReuseRecord = true
	header, err := csvReader.Read()
	if err != nil {
		return fmt.Errorf("read CSV header: %w", err)
	}
	indexes := make(map[string]int, len(header))
	for i, name := range header {
		name = strings.TrimSpace(strings.TrimPrefix(name, "\uFEFF"))
		indexes[name] = i
	}
	for _, name := range []string{"network", "country", "country_code", "continent", "continent_code"} {
		if _, ok := indexes[name]; !ok {
			return fmt.Errorf("CSV is missing required field %q (header: %v)", name, header)
		}
	}
	csvReader.FieldsPerRecord = len(header)

	records := make(map[string]mmdbtype.Map, 300)
	var total, written, skipped uint64
	for {
		row, readErr := csvReader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read CSV row %d: %w", total+2, readErr)
		}
		total++

		countryCode := strings.ToUpper(strings.TrimSpace(row[indexes["country_code"]]))
		if !validCountryCode(countryCode) {
			skipped++
			continue
		}
		prefix, parseErr := parsePrefix(row[indexes["network"]])
		if parseErr != nil {
			return fmt.Errorf("CSV row %d has invalid network %q: %w", total+1, row[indexes["network"]], parseErr)
		}

		countryName := strings.TrimSpace(row[indexes["country"]])
		continentCode := strings.ToUpper(strings.TrimSpace(row[indexes["continent_code"]]))
		continentName := strings.TrimSpace(row[indexes["continent"]])
		cacheKey := strings.Join([]string{countryCode, countryName, continentCode, continentName}, "\x00")
		value, ok := records[cacheKey]
		if !ok {
			value = record(countryCode, countryName, continentCode, continentName)
			records[cacheKey] = value
		}
		if insertErr := tree.Insert(prefix, value); insertErr != nil {
			return fmt.Errorf("insert %s (%s): %w", prefix, countryCode, insertErr)
		}
		written++
	}
	if written == 0 {
		return errors.New("IPinfo CSV contained no usable records")
	}

	output, err := os.Create(opts.output)
	if err != nil {
		return fmt.Errorf("create %s: %w", opts.output, err)
	}
	size, writeErr := tree.WriteTo(output)
	closeErr := output.Close()
	if writeErr != nil {
		return fmt.Errorf("write MMDB: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close MMDB: %w", closeErr)
	}

	log.Printf("IPinfo records: total=%d written=%d skipped=%d", total, written, skipped)
	log.Printf("wrote %s (%d bytes)", opts.output, size)
	return nil
}

func main() {
	input := flag.String("input", "", "path to ipinfo_lite.csv.gz")
	output := flag.String("output", "", "path to GeoLite2-Country.mmdb")
	flag.Parse()
	if *input == "" || *output == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(options{input: *input, output: *output}); err != nil {
		log.Fatal(err)
	}
}

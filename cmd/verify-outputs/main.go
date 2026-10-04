// Command verify-outputs checks that geoip.dat contains the same country IP
// sets as the final MMDB. The DAT parser intentionally reads only the small
// public v2ray GeoIP protobuf format used by Loyalsoldier/geoip.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"

	"github.com/oschwald/maxminddb-golang/v2"
	"go4.org/netipx"
	"google.golang.org/protobuf/encoding/protowire"
)

type countrySets map[string]*netipx.IPSetBuilder

func add(set countrySets, country string, prefix netip.Prefix) {
	builder := set[country]
	if builder == nil {
		builder = new(netipx.IPSetBuilder)
		set[country] = builder
	}
	builder.AddPrefix(prefix)
}

func readMMDB(path string) (countrySets, error) {
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	sets := make(countrySets)
	for result := range db.Networks() {
		var record struct {
			Country struct {
				ISOCode string `maxminddb:"iso_code"`
			} `maxminddb:"country"`
		}
		if err := result.Decode(&record); err != nil {
			return nil, err
		}
		if record.Country.ISOCode == "" {
			continue
		}
		add(sets, record.Country.ISOCode, result.Prefix())
	}
	return sets, nil
}

func consumeMessage(data []byte, sets countrySets) error {
	var country string
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return protowire.ParseError(n)
		}
		data = data[n:]
		switch number {
		case 1:
			if wireType != protowire.BytesType {
				return fmt.Errorf("country_code has wire type %d", wireType)
			}
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			country = string(value)
			data = data[n:]
		case 2:
			if wireType != protowire.BytesType {
				return fmt.Errorf("cidr has wire type %d", wireType)
			}
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			if country == "" {
				return errors.New("CIDR appeared before country_code")
			}
			prefix, err := parseCIDR(value)
			if err != nil {
				return err
			}
			add(sets, country, prefix)
			data = data[n:]
		default:
			n := protowire.ConsumeFieldValue(number, wireType, data)
			if n < 0 {
				return protowire.ParseError(n)
			}
			data = data[n:]
		}
	}
	return nil
}

func parseCIDR(data []byte) (netip.Prefix, error) {
	var address []byte
	var bits uint64
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return netip.Prefix{}, protowire.ParseError(n)
		}
		data = data[n:]
		switch number {
		case 1:
			if wireType != protowire.BytesType {
				return netip.Prefix{}, fmt.Errorf("CIDR IP has wire type %d", wireType)
			}
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return netip.Prefix{}, protowire.ParseError(n)
			}
			address = append([]byte(nil), value...)
			data = data[n:]
		case 2:
			if wireType != protowire.VarintType {
				return netip.Prefix{}, fmt.Errorf("CIDR prefix has wire type %d", wireType)
			}
			value, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return netip.Prefix{}, protowire.ParseError(n)
			}
			bits = value
			data = data[n:]
		default:
			n := protowire.ConsumeFieldValue(number, wireType, data)
			if n < 0 {
				return netip.Prefix{}, protowire.ParseError(n)
			}
			data = data[n:]
		}
	}
	var addressValue netip.Addr
	switch len(address) {
	case net.IPv4len:
		var value [4]byte
		copy(value[:], address)
		addressValue = netip.AddrFrom4(value)
	case net.IPv6len:
		var value [16]byte
		copy(value[:], address)
		addressValue = netip.AddrFrom16(value)
	default:
		return netip.Prefix{}, fmt.Errorf("CIDR IP has %d bytes", len(address))
	}
	if bits > uint64(addressValue.BitLen()) {
		return netip.Prefix{}, fmt.Errorf("CIDR prefix length %d exceeds address length", bits)
	}
	return netip.PrefixFrom(addressValue, int(bits)).Masked(), nil
}

func readDAT(path string) (countrySets, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sets := make(countrySets)
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		data = data[n:]
		if number != 1 || wireType != protowire.BytesType {
			n = protowire.ConsumeFieldValue(number, wireType, data)
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			data = data[n:]
			continue
		}
		entry, n := protowire.ConsumeBytes(data)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		if err := consumeMessage(entry, sets); err != nil {
			return nil, err
		}
		data = data[n:]
	}
	return sets, nil
}

func freeze(input countrySets) (map[string][]string, error) {
	output := make(map[string][]string, len(input))
	for country, builder := range input {
		set, err := builder.IPSet()
		if err != nil {
			return nil, err
		}
		for _, ipRange := range set.Ranges() {
			output[country] = append(output[country], ipRange.String())
		}
		sort.Strings(output[country])
	}
	return output, nil
}

func compare(mmdbPath, datPath string) error {
	mmdbSets, err := readMMDB(mmdbPath)
	if err != nil {
		return fmt.Errorf("read MMDB: %w", err)
	}
	datSets, err := readDAT(datPath)
	if err != nil {
		return fmt.Errorf("read DAT: %w", err)
	}
	left, err := freeze(mmdbSets)
	if err != nil {
		return err
	}
	right, err := freeze(datSets)
	if err != nil {
		return err
	}
	if !bytes.Equal(mustJSON(left), mustJSON(right)) {
		return fmt.Errorf("MMDB and DAT country IP sets differ (MMDB countries=%d, DAT countries=%d)", len(left), len(right))
	}
	fmt.Printf("verified identical country IP sets: %d countries\n", len(left))
	return nil
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func main() {
	mmdbPath := flag.String("mmdb", "", "path to GeoLite2-Country.mmdb")
	datPath := flag.String("dat", "", "path to geoip.dat")
	flag.Parse()
	if *mmdbPath == "" || *datPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := compare(*mmdbPath, *datPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

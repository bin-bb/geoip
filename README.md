# IPinfo Lite GeoIP

这个仓库只使用 IPinfo Lite 的 `ipinfo_lite.csv.gz`，自动生成两个文件：

- `GeoLite2-Country.mmdb`：标准 GeoLite2-Country 类型的 MaxMind 数据库，可用于 Surge 和 `geodata-mode: false` 的 Mihomo。
- `geoip.dat`：从最终的 `GeoLite2-Country.mmdb` 使用 Loyalsoldier/geoip 转换而来，可用于 Mihomo 的 `geodata-mode: true`。

数据链路是：

```text
IPinfo Lite CSV.gz -> GeoLite2-Country.mmdb -> geoip.dat
```

不会读取或合并 `all_cn_ipv46.txt`，也不会额外覆盖 CN。构建结束时会比较 MMDB 和 DAT 中每个国家的 IP 集合，校验失败就不会发布 Release。

## GitHub 网页部署

1. 在 GitHub 网页新建一个空仓库，例如 `geoip`。不要勾选自动生成 README、`.gitignore` 或 License。
2. 打开新仓库，点击 **Add file → Upload files**。
3. 把本项目目录下的全部文件和文件夹拖进上传区域，确认能看到 `.github/workflows/build.yml`。如果网页没有显示隐藏目录，使用 GitHub 的 **Add file → Create new file** 先创建 `.github/workflows/build.yml`，再把对应文件内容粘贴进去。
4. 点击 **Commit changes**，提交到默认分支（一般是 `main`）。
5. 打开 **Settings → Secrets and variables → Actions → New repository secret**。
6. Name 填 `IPINFO_TOKEN`，Value 填你的 IPinfo Token，然后点击 **Add secret**。Token 不要写进 README、Workflow、URL 或普通仓库文件。
7. 打开 **Actions**，选择 **Build GeoIP databases**，点击 **Run workflow** 手动运行一次。
8. 第一次成功后，打开仓库的 **Releases** 页面。文件下载地址固定为：

```text
https://github.com/你的用户名/你的仓库名/releases/latest/download/GeoLite2-Country.mmdb
https://github.com/你的用户名/你的仓库名/releases/latest/download/geoip.dat
```

之后 Workflow 每 8 小时请求一次 IPinfo Lite。如果源文件 SHA-256 没有变化，就跳过构建和发布；有变化时才创建新的 Release。GitHub 定时任务可能因平台负载延迟几分钟。

## 客户端配置

Surge：

```ini
[General]
geoip-maxmind-url = https://github.com/你的用户名/你的仓库名/releases/latest/download/GeoLite2-Country.mmdb
```

Mihomo 使用 MMDB：

```yaml
geodata-mode: false
geox-url:
  mmdb: https://github.com/你的用户名/你的仓库名/releases/latest/download/GeoLite2-Country.mmdb
```

Mihomo 使用 DAT：

```yaml
geodata-mode: true
geox-url:
  geoip: https://github.com/你的用户名/你的仓库名/releases/latest/download/geoip.dat
```

## 本地检查

需要 Go 1.27、gzip，以及 PATH 中的 Loyalsoldier `geoip` 命令。构建流程与 Actions 中相同：

```bash
mkdir -p build
go run ./cmd/build-country -input ./ipinfo_lite.csv.gz -output ./build/GeoLite2-Country.mmdb
geoip convert -c .github/geoip-dat.json
mv build/dat/geoip.dat build/geoip.dat
go run ./cmd/verify-outputs -mmdb build/GeoLite2-Country.mmdb -dat build/geoip.dat
```

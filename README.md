# wireguard-exporter

A small Prometheus exporter for WireGuard. It reads device and peer state
directly from the kernel over netlink (via
[wgctrl](https://pkg.go.dev/golang.zx2c4.com/wireguard/wgctrl)), so the `wg`
binary is not needed. Userspace implementations (e.g. `wireguard-go`) are
supported too. It was originally thought for my homelab, so in case it is useful for you, feel free to use it/

State is read on every scrape. There is no background polling and no cache.

## Install

Download the tarball for your architecture from the
[releases page](https://github.com/donkeysharp/wireguard-exporter/releases)
(`uname -m`: `x86_64` → `amd64`, `aarch64` → `arm64`, `armv7l` → `armv7`):

```sh
VERSION=0.1.0 ARCH=amd64
curl -LO https://github.com/donkeysharp/wireguard-exporter/releases/download/v${VERSION}/wireguard_exporter-${VERSION}.linux-${ARCH}.tar.gz
curl -LO https://github.com/donkeysharp/wireguard-exporter/releases/download/v${VERSION}/sha256sums.txt
sha256sum --ignore-missing -c sha256sums.txt
tar -xzf wireguard_exporter-${VERSION}.linux-${ARCH}.tar.gz
cd wireguard_exporter-${VERSION}.linux-${ARCH}
```

Each tarball contains the binary, the systemd unit, `contrib/generate-peers-file.sh`
and an example peers file. Then follow the [systemd](#systemd) steps below.

## Build

Requires Go 1.22 or higher.

```sh
make            # fmt check, vet, test (-race), build -> bin/wireguard_exporter
make cross      # static binaries for linux/amd64, arm64, arm
```

## Usage

```sh
sudo ./bin/wireguard_exporter
curl -s localhost:9586/metrics | grep ^wireguard
```

| Flag | Default | Description |
|---|---|---|
| `--web.listen-address` | `:9586` | Address to listen on. |
| `--web.telemetry-path` | `/metrics` | Metrics path. |
| `--web.disable-exporter-metrics` | `false` | Exclude `go_*` and `process_*` metrics. |
| `--wireguard.interface` | all | Only collect these interfaces. Repeatable or comma-separated. |
| `--wireguard.peer-names-file` | | YAML file mapping public keys to names (see below). |
| `--log.level` | `info` | `debug`, `info`, `warn`, `error`. |
| `--log.format` | `logfmt` | `logfmt` or `json`. |
| `--version` | | Print version and exit. |

Endpoints: `/metrics`, `/-/healthy`, `/`.

### Permissions

Querying WireGuard over generic netlink requires `CAP_NET_ADMIN`. Prefer
granting just that capability over running as root:

```sh
sudo setcap cap_net_admin+ep ./bin/wireguard_exporter
```

or use the provided systemd unit, which does this with `DynamicUser=yes`.

### Peer names

Public keys are hard to read on dashboards. You can give peers names with a
YAML file:

```yaml
peers:
  "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=": alice-laptop
```

You don't need to collect the keys by hand: generate the file from the running
WireGuard config (each peer is named after its allowed IPs), then rename
entries as you like:

```sh
sudo ./contrib/generate-peers-file.sh | sudo tee /etc/wireguard_exporter/peers.yml
sudo ./contrib/generate-peers-file.sh wg0   # a single interface
```

The file is optional and can live anywhere (pass an absolute path). The name
is added as the `name` label on every peer metric, and is empty for
unknown peers. Send `SIGHUP` (`systemctl reload wireguard_exporter`) to reload
the file. If the new file is invalid, the previous mapping is kept and an error
is logged.

## Metrics

| Metric | Type | Labels |
|---|---|---|
| `wireguard_up` | gauge | |
| `wireguard_device_info` | gauge (1) | `interface`, `public_key`, `type` |
| `wireguard_device_listen_port` | gauge | `interface` |
| `wireguard_device_peers` | gauge | `interface` |
| `wireguard_peer_info` | gauge (1) | `interface`, `public_key`, `name`, `endpoint`, `allowed_ips` |
| `wireguard_peer_receive_bytes_total` | counter | `interface`, `public_key`, `name` |
| `wireguard_peer_transmit_bytes_total` | counter | `interface`, `public_key`, `name` |
| `wireguard_peer_last_handshake_timestamp_seconds` | gauge | `interface`, `public_key`, `name` |
| `wireguard_exporter_build_info` | gauge (1) | `version`, `revision`, `goversion` |

`endpoint` and `allowed_ips` can change (for example when a peer roams), so
they appear only on the `_info` metric. That keeps the counters' series stable.
Use a join to bring them into other queries when needed.

### Example queries

```promql
# Receive throughput per peer (bytes/s)
rate(wireguard_peer_receive_bytes_total[5m])

# Seconds since last handshake (peers that never connected show a very large value)
time() - wireguard_peer_last_handshake_timestamp_seconds

# Alert: peer has not completed a handshake in 5 minutes
time() - wireguard_peer_last_handshake_timestamp_seconds > 300

# Attach endpoint to throughput
rate(wireguard_peer_receive_bytes_total[5m])
  * on (interface, public_key) group_left (endpoint) wireguard_peer_info
```

## systemd

```sh
# From an extracted release tarball (use bin/wireguard_exporter if built from source)
sudo install -m 0755 wireguard_exporter /usr/local/bin/
sudo install -m 0644 contrib/systemd/wireguard_exporter.service /etc/systemd/system/
sudo install -m 0644 contrib/systemd/wireguard_exporter.default /etc/default/wireguard_exporter
sudo install -d /etc/wireguard_exporter
sudo ./contrib/generate-peers-file.sh | sudo tee /etc/wireguard_exporter/peers.yml
sudo systemctl daemon-reload
sudo systemctl enable --now wireguard_exporter
```

## Releasing

CI (`.github/workflows/ci.yml`) runs on every push to `master` and on pull
requests. It checks formatting, `go mod tidy`, vet, race tests, cross builds and
ShellCheck, and does a GoReleaser dry run so packaging problems show up before
you tag.

To publish a release, push a semver tag:

```sh
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

`.github/workflows/release.yml` then runs the tests and
[GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`), which creates a
GitHub Release with one `.tar.gz` per architecture, `sha256sums.txt` and a
changelog. Tags with a suffix such as `v0.2.0-rc.1` are marked as pre-releases.

To build the release archives locally into `dist/` without publishing:

```sh
make snapshot
```

# wireguard-exporter

A small Prometheus exporter for WireGuard. It reads device and peer state
directly from the kernel over netlink (via
[wgctrl](https://pkg.go.dev/golang.zx2c4.com/wireguard/wgctrl)), so the `wg`
binary is not needed. Userspace implementations (e.g. `wireguard-go`) are
supported too.

State is read on every scrape. There is no background polling and no cache.

## Build

Requires Go 1.22.

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

The name is added as the `name` label on every peer metric, and is empty for
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
sudo install -m 0755 bin/wireguard_exporter /usr/local/bin/
sudo install -m 0644 contrib/systemd/wireguard_exporter.service /etc/systemd/system/
sudo install -m 0644 contrib/systemd/wireguard_exporter.default /etc/default/wireguard_exporter
sudo install -d /etc/wireguard_exporter
sudo install -m 0644 examples/peers.yml /etc/wireguard_exporter/peers.yml
sudo systemctl daemon-reload
sudo systemctl enable --now wireguard_exporter
```

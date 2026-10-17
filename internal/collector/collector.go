// Package collector implements a Prometheus collector for WireGuard devices.
package collector

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const namespace = "wireguard"

// Client is the subset of *wgctrl.Client used by the collector.
type Client interface {
	Devices() ([]*wgtypes.Device, error)
}

// PeerNamer resolves a peer public key to a friendly name ("" if unknown).
type PeerNamer interface {
	Name(key wgtypes.Key) string
}

// Options configures a Collector.
type Options struct {
	// Interfaces restricts collection to these device names. Empty means all.
	Interfaces []string
	// Names optionally resolves peer public keys to friendly names.
	Names PeerNamer
	// Logger is used to report scrape errors. Defaults to slog.Default().
	Logger *slog.Logger
}

// Collector gathers WireGuard device and peer statistics on every scrape.
type Collector struct {
	client     Client
	names      PeerNamer
	interfaces map[string]struct{}
	logger     *slog.Logger

	// mu serializes access to the underlying netlink client across
	// concurrent scrapes.
	mu sync.Mutex

	up                *prometheus.Desc
	deviceInfo        *prometheus.Desc
	deviceListenPort  *prometheus.Desc
	devicePeers       *prometheus.Desc
	peerInfo          *prometheus.Desc
	peerReceiveBytes  *prometheus.Desc
	peerTransmitBytes *prometheus.Desc
	peerLastHandshake *prometheus.Desc
}

var _ prometheus.Collector = (*Collector)(nil)

// New returns a Collector that reads WireGuard state from client.
func New(client Client, opts Options) *Collector {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	var ifaces map[string]struct{}
	if len(opts.Interfaces) > 0 {
		ifaces = make(map[string]struct{}, len(opts.Interfaces))
		for _, name := range opts.Interfaces {
			ifaces[name] = struct{}{}
		}
	}

	deviceLabels := []string{"interface"}
	peerLabels := []string{"interface", "public_key", "name"}

	return &Collector{
		client:     client,
		names:      opts.Names,
		interfaces: ifaces,
		logger:     logger,

		up: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "up"),
			"Whether the last query of WireGuard devices was successful.",
			nil, nil,
		),
		deviceInfo: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "device", "info"),
			"Metadata about a WireGuard device. Value is always 1.",
			[]string{"interface", "public_key", "type"}, nil,
		),
		deviceListenPort: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "device", "listen_port"),
			"UDP port the WireGuard device listens on.",
			deviceLabels, nil,
		),
		devicePeers: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "device", "peers"),
			"Number of peers configured on the WireGuard device.",
			deviceLabels, nil,
		),
		peerInfo: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "peer", "info"),
			"Metadata about a WireGuard peer. Value is always 1.",
			append(peerLabels, "endpoint", "allowed_ips"), nil,
		),
		peerReceiveBytes: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "peer", "receive_bytes_total"),
			"Total number of bytes received from the peer.",
			peerLabels, nil,
		),
		peerTransmitBytes: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "peer", "transmit_bytes_total"),
			"Total number of bytes transmitted to the peer.",
			peerLabels, nil,
		),
		peerLastHandshake: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "peer", "last_handshake_timestamp_seconds"),
			"UNIX timestamp of the last handshake with the peer, 0 if none has occurred.",
			peerLabels, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.up
	ch <- c.deviceInfo
	ch <- c.deviceListenPort
	ch <- c.devicePeers
	ch <- c.peerInfo
	ch <- c.peerReceiveBytes
	ch <- c.peerTransmitBytes
	ch <- c.peerLastHandshake
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	devices, err := c.client.Devices()
	c.mu.Unlock()
	if err != nil {
		c.logger.Error("failed to list WireGuard devices", "err", err)
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)

	for _, d := range devices {
		if !c.wanted(d.Name) {
			continue
		}
		c.collectDevice(ch, d)
	}
}

func (c *Collector) wanted(iface string) bool {
	if c.interfaces == nil {
		return true
	}
	_, ok := c.interfaces[iface]
	return ok
}

func (c *Collector) collectDevice(ch chan<- prometheus.Metric, d *wgtypes.Device) {
	ch <- prometheus.MustNewConstMetric(c.deviceInfo, prometheus.GaugeValue, 1,
		d.Name, d.PublicKey.String(), d.Type.String())
	ch <- prometheus.MustNewConstMetric(c.deviceListenPort, prometheus.GaugeValue,
		float64(d.ListenPort), d.Name)
	ch <- prometheus.MustNewConstMetric(c.devicePeers, prometheus.GaugeValue,
		float64(len(d.Peers)), d.Name)

	for i := range d.Peers {
		p := &d.Peers[i]
		pubKey := p.PublicKey.String()
		name := c.peerName(p.PublicKey)

		endpoint := ""
		if p.Endpoint != nil {
			endpoint = p.Endpoint.String()
		}

		ch <- prometheus.MustNewConstMetric(c.peerInfo, prometheus.GaugeValue, 1,
			d.Name, pubKey, name, endpoint, joinAllowedIPs(p))
		ch <- prometheus.MustNewConstMetric(c.peerReceiveBytes, prometheus.CounterValue,
			float64(p.ReceiveBytes), d.Name, pubKey, name)
		ch <- prometheus.MustNewConstMetric(c.peerTransmitBytes, prometheus.CounterValue,
			float64(p.TransmitBytes), d.Name, pubKey, name)

		var handshake float64
		if !p.LastHandshakeTime.IsZero() {
			handshake = float64(p.LastHandshakeTime.UnixNano()) / 1e9
		}
		ch <- prometheus.MustNewConstMetric(c.peerLastHandshake, prometheus.GaugeValue,
			handshake, d.Name, pubKey, name)
	}
}

func (c *Collector) peerName(key wgtypes.Key) string {
	if c.names == nil {
		return ""
	}
	return c.names.Name(key)
}

func joinAllowedIPs(p *wgtypes.Peer) string {
	ips := make([]string, 0, len(p.AllowedIPs))
	for _, n := range p.AllowedIPs {
		ips = append(ips, n.String())
	}
	return strings.Join(ips, ",")
}

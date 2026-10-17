package collector

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type fakeClient struct {
	devices []*wgtypes.Device
	err     error
}

func (f *fakeClient) Devices() ([]*wgtypes.Device, error) { return f.devices, f.err }

type fakeNamer map[wgtypes.Key]string

func (f fakeNamer) Name(k wgtypes.Key) string { return f[k] }

func mustKey(t *testing.T, b byte) wgtypes.Key {
	t.Helper()
	var k wgtypes.Key
	for i := range k {
		k[i] = b
	}
	return k
}

func mustCIDR(t *testing.T, s string) net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return *n
}

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func testDevices(t *testing.T) []*wgtypes.Device {
	return []*wgtypes.Device{
		{
			Name:       "wg0",
			Type:       wgtypes.LinuxKernel,
			PublicKey:  mustKey(t, 1),
			ListenPort: 51820,
			Peers: []wgtypes.Peer{
				{
					PublicKey:         mustKey(t, 2),
					Endpoint:          &net.UDPAddr{IP: net.ParseIP("203.0.113.10"), Port: 51820},
					LastHandshakeTime: time.Unix(1700000000, 0),
					ReceiveBytes:      1024,
					TransmitBytes:     2048,
					AllowedIPs:        []net.IPNet{mustCIDR(t, "10.0.0.2/32"), mustCIDR(t, "fd00::2/128")},
				},
				{
					// Peer that never connected.
					PublicKey:  mustKey(t, 3),
					AllowedIPs: []net.IPNet{mustCIDR(t, "10.0.0.3/32")},
				},
			},
		},
		{
			Name:       "wg1",
			Type:       wgtypes.LinuxKernel,
			PublicKey:  mustKey(t, 4),
			ListenPort: 51821,
		},
	}
}

const wg0Expected = `
# HELP wireguard_device_info Metadata about a WireGuard device. Value is always 1.
# TYPE wireguard_device_info gauge
wireguard_device_info{interface="wg0",public_key="AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=",type="Linux kernel"} 1
# HELP wireguard_device_listen_port UDP port the WireGuard device listens on.
# TYPE wireguard_device_listen_port gauge
wireguard_device_listen_port{interface="wg0"} 51820
# HELP wireguard_device_peers Number of peers configured on the WireGuard device.
# TYPE wireguard_device_peers gauge
wireguard_device_peers{interface="wg0"} 2
# HELP wireguard_peer_info Metadata about a WireGuard peer. Value is always 1.
# TYPE wireguard_peer_info gauge
wireguard_peer_info{allowed_ips="10.0.0.2/32,fd00::2/128",endpoint="203.0.113.10:51820",interface="wg0",name="alice",public_key="AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="} 1
wireguard_peer_info{allowed_ips="10.0.0.3/32",endpoint="",interface="wg0",name="",public_key="AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM="} 1
# HELP wireguard_peer_last_handshake_timestamp_seconds UNIX timestamp of the last handshake with the peer, 0 if none has occurred.
# TYPE wireguard_peer_last_handshake_timestamp_seconds gauge
wireguard_peer_last_handshake_timestamp_seconds{interface="wg0",name="alice",public_key="AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="} 1.7e+09
wireguard_peer_last_handshake_timestamp_seconds{interface="wg0",name="",public_key="AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM="} 0
# HELP wireguard_peer_receive_bytes_total Total number of bytes received from the peer.
# TYPE wireguard_peer_receive_bytes_total counter
wireguard_peer_receive_bytes_total{interface="wg0",name="alice",public_key="AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="} 1024
wireguard_peer_receive_bytes_total{interface="wg0",name="",public_key="AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM="} 0
# HELP wireguard_peer_transmit_bytes_total Total number of bytes transmitted to the peer.
# TYPE wireguard_peer_transmit_bytes_total counter
wireguard_peer_transmit_bytes_total{interface="wg0",name="alice",public_key="AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="} 2048
wireguard_peer_transmit_bytes_total{interface="wg0",name="",public_key="AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM="} 0
# HELP wireguard_up Whether the last query of WireGuard devices was successful.
# TYPE wireguard_up gauge
wireguard_up 1
`

func TestCollectFilteredInterface(t *testing.T) {
	c := New(&fakeClient{devices: testDevices(t)}, Options{
		Interfaces: []string{"wg0"},
		Names:      fakeNamer{mustKey(t, 2): "alice"},
		Logger:     discardLogger,
	})

	if err := testutil.CollectAndCompare(c, strings.NewReader(wg0Expected)); err != nil {
		t.Fatal(err)
	}
}

func TestCollectAllInterfaces(t *testing.T) {
	c := New(&fakeClient{devices: testDevices(t)}, Options{Logger: discardLogger})

	if n := testutil.CollectAndCount(c, "wireguard_device_info"); n != 2 {
		t.Fatalf("got %d wireguard_device_info series, want 2", n)
	}
	if n := testutil.CollectAndCount(c, "wireguard_peer_receive_bytes_total"); n != 2 {
		t.Fatalf("got %d wireguard_peer_receive_bytes_total series, want 2", n)
	}
}

func TestCollectClientError(t *testing.T) {
	c := New(&fakeClient{err: errors.New("boom")}, Options{Logger: discardLogger})

	const want = `
# HELP wireguard_up Whether the last query of WireGuard devices was successful.
# TYPE wireguard_up gauge
wireguard_up 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
}

func TestLint(t *testing.T) {
	c := New(&fakeClient{devices: testDevices(t)}, Options{Logger: discardLogger})

	problems, err := testutil.CollectAndLint(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("lint: %s: %s", p.Metric, p.Text)
	}
}

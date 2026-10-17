#!/bin/sh
# Generates a peers file for --wireguard.peer-names-file from the running
# WireGuard configuration. Each peer is named after its allowed IPs; edit the
# names afterwards if you prefer something friendlier (e.g. "alice-laptop").
#
# Usage: generate-peers-file.sh [interface]
#
#   sudo ./generate-peers-file.sh | sudo tee /etc/wireguard_exporter/peers.yml
#   sudo ./generate-peers-file.sh wg0 > peers.yml
#
# Without an interface, peers of all WireGuard interfaces are included.
# The output overwrites any custom names, so review it before replacing an
# existing file.
set -eu

iface="${1:-all}"

if ! command -v wg >/dev/null 2>&1; then
	echo "error: 'wg' not found, install wireguard-tools" >&2
	exit 1
fi

dump=$(wg show "$iface" dump)

echo "peers:"
# "wg show all dump" prefixes every line with the interface name; peer lines
# have 9 fields. For a single interface the first line describes the device
# and peer lines have 8 fields.
printf '%s\n' "$dump" | awk -F '\t' -v all="$([ "$iface" = all ] && echo 1 || echo 0)" '
	all == 1 && NF == 9 { key = $2; ips = $5 }
	all == 0 && NF == 8 { key = $1; ips = $4 }
	(all == 1 && NF == 9) || (all == 0 && NF == 8) {
		if (ips == "(none)") ips = ""
		printf "  \"%s\": \"%s\"\n", key, ips
	}
'

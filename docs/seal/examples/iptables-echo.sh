#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Hard seal for a staging VM with iptables (no nftables): egress deny
# with Echo as the only way out. Same policy as nftables-echo.nft.
#
#   sudo useradd --system --no-create-home rhythm-echo
#   sudo sh iptables-echo.sh          # seal
#   sudo sh iptables-echo.sh off      # unseal
#
# Rules live in their own ECHO_SEAL chains, jumped to from OUTPUT, so
# the rest of the firewall is left alone. Persist with your distro's
# tool (iptables-save > /etc/iptables/rules.v4, netfilter-persistent).
set -eu

ECHO_USER=${ECHO_USER:-rhythm-echo}
RESOLVERS_V4=${RESOLVERS_V4:-127.0.0.53}
RESOLVERS_V6=${RESOLVERS_V6:-::1}
PRIVATE_V4="10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16"
PRIVATE_V6="fc00::/7 fe80::/10"

unseal() {
	for t in iptables ip6tables; do
		while $t -D OUTPUT -j ECHO_SEAL 2>/dev/null; do :; done
		$t -F ECHO_SEAL 2>/dev/null || true
		$t -X ECHO_SEAL 2>/dev/null || true
	done
}

unseal
[ "${1:-}" = off ] && exit 0

seal() { # $1 = iptables or ip6tables, $2 = private ranges, $3 = resolvers, $4 = reject type
	t=$1
	$t -N ECHO_SEAL
	$t -A ECHO_SEAL -o lo -j RETURN
	$t -A ECHO_SEAL -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
	$t -A ECHO_SEAL -m owner --uid-owner "$ECHO_USER" -j RETURN
	# $t -A ECHO_SEAL -m owner --uid-owner 0 -j RETURN   # root: package updates
	for net in $2; do $t -A ECHO_SEAL -d "$net" -j RETURN; done
	for r in $3; do
		$t -A ECHO_SEAL -d "$r" -p udp --dport 53 -j RETURN
		$t -A ECHO_SEAL -d "$r" -p tcp --dport 53 -j RETURN
	done
	[ "$t" = ip6tables ] && $t -A ECHO_SEAL -p icmpv6 -j RETURN
	$t -A ECHO_SEAL -m limit --limit 10/min -j LOG --log-prefix "echo-seal refused: " --log-uid
	$t -A ECHO_SEAL -p tcp -j REJECT --reject-with tcp-reset
	$t -A ECHO_SEAL -j REJECT --reject-with "$4"
	$t -I OUTPUT 1 -j ECHO_SEAL
}

seal iptables "$PRIVATE_V4" "$RESOLVERS_V4" icmp-admin-prohibited
if command -v ip6tables >/dev/null 2>&1; then
	seal ip6tables "$PRIVATE_V6" "$RESOLVERS_V6" icmp6-adm-prohibited
fi
echo "sealed: only $ECHO_USER may connect out; others use Echo's proxy and SMTP"

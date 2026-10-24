#!/bin/sh
# Checks docs/seal/examples/{nftables-echo.nft,iptables-echo.sh} in a
# throwaway network namespace (run as: unshare -rn sh firewall-test.sh
# EXAMPLES_DIR). Inside it we are uid 0; "the Echo user" is set to root
# or to nobody to see both sides (for iptables, see below). Outside traffic goes to a dummy
# interface, so nothing leaves the machine.
set -eu
dir=$1
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

ip link set lo up
ip link add dummy0 type dummy
ip addr add 198.51.100.1/24 dev dummy0
ip link set dummy0 up
ip route add default dev dummy0

# probe HOST PORT -> "out" (the SYN left: timeout on the dummy link),
# "refused" (rejected by the seal) or "eperm".
probe() {
	python3 -I -c '
import socket, sys
s = socket.socket(); s.settimeout(0.5)
try:
    s.connect((sys.argv[1], int(sys.argv[2]))); print("connected")
except socket.timeout: print("out")
except ConnectionRefusedError: print("refused")
except PermissionError: print("eperm")
except OSError as e: print("errno%d" % e.errno)
' "$1" "$2"
}

fail=0
expect() { # name got want
	if [ "$2" = "$3" ]; then echo "ok   $1: $2"; else echo "FAIL $1: got $2, want $3"; fail=1; fi
}

for fw in nft iptables; do
	for who in nobody root; do
		if [ $fw = nft ]; then
			sed -e "s/^define echo_user = .*/define echo_user = \"$who\"/" "$dir/nftables-echo.nft" >"$work/rules.nft"
			nft -f "$work/rules.nft"
		elif [ $who = root ]; then
			ECHO_USER=root sh "$dir/iptables-echo.sh" >/dev/null
		else
			# xt_owner refuses uids this namespace can't map, so the
			# "someone else" side runs without the owner rule.
			sed -e '/--uid-owner "\$ECHO_USER"/d' "$dir/iptables-echo.sh" >"$work/ipt.sh"
			ECHO_USER=root sh "$work/ipt.sh" >/dev/null
		fi
		if [ $who = nobody ]; then want=refused; else want=out; fi
		expect "$fw, echo user $who: internet" "$(probe 203.0.113.9 443)" $want
		expect "$fw, echo user $who: private network" "$(probe 10.1.2.3 5432)" out
		expect "$fw, echo user $who: loopback" "$(probe 127.0.0.1 9)" refused
		if [ $fw = nft ]; then nft delete table inet echo_seal; else sh "$dir/iptables-echo.sh" off; fi
		expect "$fw unsealed: internet" "$(probe 203.0.113.9 443)" out
	done
done
exit $fail

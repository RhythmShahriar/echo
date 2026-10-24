# Hard seal on a Linux VM (nftables or iptables)

On a VM or bare server, Echo runs on the same machine as a dedicated user.
The firewall lets only that user open connections to the internet. Every
other process can reach only:
- loopback, where Echo listens
- private networks (database, cache)
- the DNS resolvers

Everything else is refused at once, with a TCP reset or ICMP
admin-prohibited, so a missed client fails loudly instead of hanging. Refusals
are logged with the uid (`journalctl -k | grep echo-seal`), which tells you
which process tried.

| | |
|---|---|
| nftables | [examples/nftables-echo.nft](examples/nftables-echo.nft): its own `inet echo_seal` table, so the rest of your ruleset is left alone |
| iptables | [examples/iptables-echo.sh](examples/iptables-echo.sh): its own `ECHO_SEAL` chains, IPv4 and IPv6 |

## Set up

1. Create Echo's user and run Echo as that user, on loopback:

   ```sh
   sudo useradd --system --create-home --home-dir /var/lib/rhythm-echo rhythm-echo
   sudo -u rhythm-echo rhythm-echo up --seal --proxy 127.0.0.1:8026 --mitm
   ```

   A systemd unit with `User=rhythm-echo` does the same.
2. Route the app through Echo: [php-fpm.md](php-fpm.md), or `HTTPS_PROXY` and
   the CA for other stacks.
3. Edit the defines at the top of the nftables file (`echo_user`,
   `resolvers_v4`, private ranges), then load it:

   ```sh
   sudo nft -f docs/seal/examples/nftables-echo.nft
   # or, with iptables:
   sudo ECHO_USER=rhythm-echo RESOLVERS_V4=127.0.0.53 sh docs/seal/examples/iptables-echo.sh
   ```

4. Check it:

   ```sh
   sudo -u www-data curl -m 5 https://example.com        # refused
   sudo -u www-data curl -m 5 -x http://127.0.0.1:8026 \
     --cacert "$(rhythm echo ca)" https://example.com     # through Echo: recorded and faked while sealed
   ```

Unseal with `sudo nft delete table inet echo_seal`, or
`sudo sh iptables-echo.sh off`. To persist the rules:
- **nftables:** include the file from `/etc/nftables.conf`.
- **iptables:** use `netfilter-persistent save`.

## Choices you may want to change

- **Root:** root may not connect out by default, so `apt` can't update while
  the VM is sealed. Uncomment the `skuid 0` (or `--uid-owner 0`) line, or
  unseal while updating. Root egress is a hole only if the app runs as root.
- **DNS:** DNS to the configured resolvers is allowed, because some libraries
  resolve names before they use a proxy. DNS can carry data out in principle.
  For a stricter seal, drop those lines and give the app an `/etc/hosts` with
  only what it needs.
- **Direct addresses:** for the rare service the app must reach without Echo,
  add it to the `direct_v4` set:
  `nft add element inet echo_seal direct_v4 { 203.0.113.10 }`. Prefer Echo's
  `--allow`, which keeps a record.
- **Docker containers** on the VM are routed through `FORWARD`, not `OUTPUT`,
  so these rules don't cover them. Use the
  [Docker Compose recipe](docker-compose.md).

## Tested

`go test -run Firewall ./proxy` loads both files in a throwaway network
namespace (`unshare -rn`, no root needed). It sends test connections to a
dummy interface, so nothing leaves the machine and checks that:
- the internet is refused for anyone but the Echo user and allowed for the
  Echo user
- private networks are allowed
- loopback is untouched
- unsealing restores everything

The namespace can map only one uid, so the iptables "someone else" case runs
without the owner rule.

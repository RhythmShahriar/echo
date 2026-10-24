package proxy

import (
	"os/exec"
	"strings"
	"testing"
)

// TestFirewallRecipes loads docs/seal/examples' nftables and iptables
// rules in a throwaway network namespace (testdata/firewall-test.sh) and
// checks who can get out. It needs unshare, nft and iptables, and
// unprivileged user namespaces; otherwise it skips.
func TestFirewallRecipes(t *testing.T) {
	for _, bin := range []string{"unshare", "nft", "iptables", "ip", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	if out, err := exec.Command("unshare", "-rn", "nft", "list", "ruleset").CombinedOutput(); err != nil {
		t.Skipf("no unprivileged network namespaces here: %v %s", err, out)
	}
	out, err := exec.Command("unshare", "-rn", "sh", "testdata/firewall-test.sh", "../docs/seal/examples").CombinedOutput()
	t.Logf("\n%s", out)
	if err != nil || strings.Contains(string(out), "FAIL") {
		t.Fatalf("firewall recipes: %v", err)
	}
}

package cloud

import (
	"net/netip"
	"strings"
)

func PublicIPs(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range values {
		raw := strings.SplitN(s, "/", 2)[0]
		ip, e := netip.ParseAddr(raw)
		if e != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	return out
}

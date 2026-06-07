package serve

import (
	"net"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Tailscale assigns node addresses out of the CGNAT IPv4 block and a
// fixed IPv6 ULA prefix. Binding the LocalSend listener to one of these
// keeps the receiver reachable over the tailnet without exposing it on
// the public internet or the broadcast LAN.
//
//   - 100.64.0.0/10  — RFC 6598 CGNAT range Tailscale carves node IPv4s from.
//   - fd7a:115c:a1e0::/48 — Tailscale's IPv6 ULA prefix.
var (
	tailscaleV4 = mustCIDR("100.64.0.0/10")
	tailscaleV6 = mustCIDR("fd7a:115c:a1e0::/48")
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic("serve: bad tailscale CIDR " + s + ": " + err.Error())
	}
	return n
}

// tailscaleAddr returns the host's Tailscale interface address, IPv4
// preferred. It enumerates every interface address and matches against
// the Tailscale ranges; an explicit -bind host is the escape hatch when
// auto-detection is wrong or Tailscale is not in use.
func tailscaleAddr() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", errors.Wrap(err)
	}
	ip, ok := tailscaleAddrFrom(addrs)
	if !ok {
		return "", errors.ErrorWithStackf(
			"no Tailscale interface address found "+
				"(looked for %s and %s)\n"+
				"hint: is `tailscale up` running? otherwise pass -bind <host> "+
				"to choose a listen address explicitly",
			tailscaleV4, tailscaleV6,
		)
	}
	return ip, nil
}

// tailscaleAddrFrom is the pure core of tailscaleAddr, split out so the
// selection logic is testable without a live network stack. It prefers a
// CGNAT IPv4 address and falls back to the Tailscale IPv6 ULA.
func tailscaleAddrFrom(addrs []net.Addr) (string, bool) {
	var v6 string
	for _, a := range addrs {
		ip := addrIP(a)
		if ip == nil {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			if tailscaleV4.Contains(v4) {
				return v4.String(), true
			}
			continue
		}
		if v6 == "" && tailscaleV6.Contains(ip) {
			v6 = ip.String()
		}
	}
	if v6 != "" {
		return v6, true
	}
	return "", false
}

// addrIP extracts the net.IP from the two concrete net.Addr shapes
// net.InterfaceAddrs returns (*net.IPNet, *net.IPAddr).
func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		return nil
	}
}

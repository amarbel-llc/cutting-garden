package serve

import (
	"net"
	"testing"
)

// mustIPNet mirrors net.InterfaceAddrs's shape: a *net.IPNet whose IP
// keeps the host bits (not the masked network address) and whose Mask is
// the network mask.
func mustIPNet(t *testing.T, s string) *net.IPNet {
	t.Helper()
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return &net.IPNet{IP: ip, Mask: n.Mask}
}

func TestTailscaleAddrFrom_PrefersCGNATv4(t *testing.T) {
	addrs := []net.Addr{
		mustIPNet(t, "192.168.1.50/24"), // LAN, ignored
		mustIPNet(t, "fd7a:115c:a1e0::1/128"),
		mustIPNet(t, "100.101.102.103/32"), // Tailscale v4
	}
	got, ok := tailscaleAddrFrom(addrs)
	if !ok {
		t.Fatal("expected a Tailscale address, got none")
	}
	if got != "100.101.102.103" {
		t.Fatalf("got %q, want the CGNAT v4 address", got)
	}
}

func TestTailscaleAddrFrom_FallsBackToV6(t *testing.T) {
	addrs := []net.Addr{
		mustIPNet(t, "10.0.0.1/8"),
		mustIPNet(t, "fd7a:115c:a1e0:ab12::1/64"),
	}
	got, ok := tailscaleAddrFrom(addrs)
	if !ok {
		t.Fatal("expected the Tailscale ULA, got none")
	}
	if got != "fd7a:115c:a1e0:ab12::1" {
		t.Fatalf("got %q, want the Tailscale ULA", got)
	}
}

func TestTailscaleAddrFrom_NoneWhenNoTailscale(t *testing.T) {
	addrs := []net.Addr{
		mustIPNet(t, "192.168.1.50/24"),
		mustIPNet(t, "fe80::1/64"),
		&net.IPAddr{IP: net.ParseIP("8.8.8.8")},
	}
	if got, ok := tailscaleAddrFrom(addrs); ok {
		t.Fatalf("expected no match, got %q", got)
	}
}

func TestTailscaleAddrFrom_IPAddrShape(t *testing.T) {
	// net.InterfaceAddrs can yield *net.IPAddr as well as *net.IPNet.
	addrs := []net.Addr{&net.IPAddr{IP: net.ParseIP("100.64.0.7")}}
	got, ok := tailscaleAddrFrom(addrs)
	if !ok || got != "100.64.0.7" {
		t.Fatalf("got (%q,%v), want (100.64.0.7,true)", got, ok)
	}
}

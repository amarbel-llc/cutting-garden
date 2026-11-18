package bridge

import (
	"strings"
	"testing"
)

func TestHello_RoundTrip(t *testing.T) {
	line := HelloLine("abc-123")
	if !strings.HasSuffix(line, "\n") || !strings.HasPrefix(line, HelloPrefix) {
		t.Fatalf("malformed hello line %q", line)
	}
	got, ok := ParseHello([]byte(line))
	if !ok || got != "abc-123" {
		t.Fatalf("ParseHello = %q, %v; want abc-123, true", got, ok)
	}
}

func TestHelloLine_EmptyPrincipalSendsNothing(t *testing.T) {
	if got := HelloLine(""); got != "" {
		t.Fatalf("HelloLine(\"\") = %q, want empty", got)
	}
}

func TestParseHello_RejectsOrdinaryMessages(t *testing.T) {
	for _, line := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n",
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n",
		`{"jsonrpc":"2.0","method":"cutting-garden/hello-not"}` + "\n",
		"not json\n",
		"",
	} {
		if p, ok := ParseHello([]byte(line)); ok {
			t.Errorf("ParseHello(%q) accepted a non-hello as principal %q", line, p)
		}
	}
}

// A hostile peer's principal is logged, so control characters are dropped and
// the length is bounded.
func TestParseHello_CleansAndBoundsThePrincipal(t *testing.T) {
	long := strings.Repeat("a", 500)
	line := HelloLine("x\ny\x1b[31m" + long)
	got, ok := ParseHello([]byte(line))
	if !ok {
		t.Fatal("hello not parsed")
	}
	if strings.ContainsAny(got, "\n\x1b") {
		t.Errorf("control characters survived: %q", got)
	}
	if len(got) > maxPrincipal {
		t.Errorf("principal length %d exceeds %d", len(got), maxPrincipal)
	}
}

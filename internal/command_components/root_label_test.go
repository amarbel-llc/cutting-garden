package command_components

import (
	"net/url"
	"testing"
)

// One label derivation serves both `list` and the mcp roots listings
// (cutting-garden#122): the last path segment, else the host, else the URI,
// unless a plugin supplied a label.
func TestRootDisplayLabel(t *testing.T) {
	labels := map[string]string{"caldav://acct/cal-1/": "Work", "caldav://acct/cal-2/": ""}
	for raw, want := range map[string]string{
		"caldav://acct/cal-1/":   "Work",
		"caldav://acct/cal-2/":   "cal-2",
		"file:///home/me/notes/": "notes",
		"fastmail://test/":       "test",
		"opaque:thing":           "opaque:thing",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := RootDisplayLabel(u, labels); got != want {
			t.Errorf("RootDisplayLabel(%q) = %q, want %q", raw, got, want)
		}
	}
}

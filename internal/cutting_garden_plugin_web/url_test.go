package cutting_garden_plugin_web

import (
	"net/url"
	"testing"
)

func parse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

func TestCaptureTarget(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"web:https://example.com/article", "https://example.com/article", false},
		{"web:http://example.com", "http://example.com", false},
		{"web:https://host:8443/p?q=1", "https://host:8443/p?q=1", false},
		// non-web scheme: the plugin only claims web:
		{"https://example.com", "", true},
		// inner must be http(s)
		{"web:ftp://example.com", "", true},
		// inner must have a host
		{"web:https:///path", "", true},
		// not the opaque form
		{"web://example.com", "", true},
	}

	for _, c := range cases {
		got, err := captureTarget(parse(t, c.raw), c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("captureTarget(%q): want error, got %q", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("captureTarget(%q): unexpected error: %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("captureTarget(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestCaptureFormatDefaultAndOverride(t *testing.T) {
	t.Setenv(webFormatEnv, "")
	if got := captureFormat(); got != defaultFormat {
		t.Errorf("captureFormat() with unset env = %q, want %q", got, defaultFormat)
	}

	t.Setenv(webFormatEnv, "  markdown-reader \n")
	if got := captureFormat(); got != "markdown-reader" {
		t.Errorf("captureFormat() = %q, want trimmed %q", got, "markdown-reader")
	}
}

func TestValidFormat(t *testing.T) {
	if !validFormat("pdf") || !validFormat("markdown-selector") {
		t.Error("validFormat rejected a catalog format")
	}
	if validFormat("pdf-clean") || validFormat("") {
		t.Error("validFormat accepted a non-catalog format")
	}
}

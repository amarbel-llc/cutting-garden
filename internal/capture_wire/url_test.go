package capture_wire

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

// TestCaptureTarget adapts plugins/web/url_test.go's TestCaptureTarget:
// same cases, now driven through a *Plugin configured with scheme
// "web" instead of the hardcoded constant (cutting-garden#146 slice 2
// phase 2).
func TestCaptureTarget(t *testing.T) {
	p := New(Spec{Name: "web", Schemes: []string{"web"}})

	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"web:https://example.com/article", "https://example.com/article", false},
		{"web:http://example.com", "http://example.com", false},
		{"web:https://host:8443/p?q=1", "https://host:8443/p?q=1", false},
		// non-web scheme: this plugin only claims web:
		{"https://example.com", "", true},
		// inner must be http(s)
		{"web:ftp://example.com", "", true},
		// inner must have a host
		{"web:https:///path", "", true},
		// not the opaque form
		{"web://example.com", "", true},
	}

	for _, c := range cases {
		got, err := p.captureTarget(parse(t, c.raw), c.raw)
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

// TestCaptureTargetMultipleSchemes pins the generalization over
// plugins/web's single hardcoded scheme: a plugin configured with
// several Schemes accepts a target under any of them and rejects an
// unconfigured one.
func TestCaptureTargetMultipleSchemes(t *testing.T) {
	p := New(Spec{Name: "multi", Schemes: []string{"web", "webarchive"}})

	if _, err := p.captureTarget(
		parse(t, "webarchive:https://example.com"), "webarchive:https://example.com",
	); err != nil {
		t.Errorf("second configured scheme rejected: %v", err)
	}
	if _, err := p.captureTarget(
		parse(t, "other:https://example.com"), "other:https://example.com",
	); err == nil {
		t.Error("unconfigured scheme accepted")
	}
}

func TestCaptureFormatDefaultAndOverride(t *testing.T) {
	p := New(Spec{Name: "web", Schemes: []string{"web"}})

	t.Setenv(p.formatEnvVar(), "")
	if got := p.captureFormat(); got != defaultFormat {
		t.Errorf("captureFormat() with unset env = %q, want %q", got, defaultFormat)
	}

	t.Setenv(p.formatEnvVar(), "  markdown-reader \n")
	if got := p.captureFormat(); got != "markdown-reader" {
		t.Errorf("captureFormat() = %q, want trimmed %q", got, "markdown-reader")
	}
}

// TestFormatEnvVarName pins the per-plugin env var derivation
// (cutting-garden#146 slice 2 phase 2's generalization of the single
// hardcoded CUTTING_GARDEN_WEB_FORMAT): the stanza name uppercased,
// hyphens folded to underscores.
func TestFormatEnvVarName(t *testing.T) {
	p := New(Spec{Name: "my-plugin"})
	if got := p.formatEnvVar(); got != "CUTTING_GARDEN_MY_PLUGIN_FORMAT" {
		t.Errorf("formatEnvVar() = %q, want CUTTING_GARDEN_MY_PLUGIN_FORMAT", got)
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

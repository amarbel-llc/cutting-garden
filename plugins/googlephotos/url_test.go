package cutting_garden_plugin_googlephotos

import (
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func TestSourceURLFromArg(t *testing.T) {
	cases := []struct {
		name     string
		arg      string
		want     string
		wantErr  bool
		errSnips []string
	}{
		{
			name: "gphotos opaque full url",
			arg:  "gphotos:https://photos.app.goo.gl/AbCdEf123",
			want: "https://photos.app.goo.gl/AbCdEf123",
		},
		{
			name: "gphotos opaque bare host",
			arg:  "gphotos:photos.app.goo.gl/AbCdEf123",
			want: "https://photos.app.goo.gl/AbCdEf123",
		},
		{
			name: "gphotos opaque photos.google.com share",
			arg:  "gphotos:https://photos.google.com/share/AF1Qip_token",
			want: "https://photos.google.com/share/AF1Qip_token",
		},
		{
			name: "gphotos hierarchical",
			arg:  "gphotos://photos.app.goo.gl/AbCdEf123",
			want: "https://photos.app.goo.gl/AbCdEf123",
		},
		{
			name:     "gphotos hierarchical empty",
			arg:      "gphotos://",
			wantErr:  true,
			errSnips: []string{"empty source URL"},
		},
		{
			name:     "gphotos off-allowlist host",
			arg:      "gphotos:https://example.com/album",
			wantErr:  true,
			errSnips: []string{"not a Google Photos host"},
		},
		{
			name:     "gphotos off-allowlist bare host",
			arg:      "gphotos:youtu.be/dQw4w9WgXcQ",
			wantErr:  true,
			errSnips: []string{"not a Google Photos host"},
		},
		{
			name: "gphotos mixed-case host accepted",
			arg:  "gphotos:https://Photos.App.Goo.GL/AbCdEf123",
			want: "https://Photos.App.Goo.GL/AbCdEf123",
		},
		{
			name:     "https unsupported scheme",
			arg:      "https://photos.app.goo.gl/AbCdEf123",
			wantErr:  true,
			errSnips: []string{"unsupported scheme"},
		},
		{
			name:     "ftp unsupported scheme",
			arg:      "ftp://photos.app.goo.gl/x",
			wantErr:  true,
			errSnips: []string{"unsupported scheme"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.arg)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", tc.arg, err)
			}
			got, err := sourceURLFromArg(u)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("sourceURLFromArg(%q) = %q, want error", tc.arg, got)
				}
				for _, snip := range tc.errSnips {
					if !strings.Contains(err.Error(), snip) {
						t.Errorf("error %q missing snippet %q", err.Error(), snip)
					}
				}
				// Every rejection here is a malformed CALLER argument and
				// must classify as a bad request, so the wire reports
				// -32602 rather than "plugin failed" (cutting-garden#187).
				if !errors.Is400BadRequest(err) {
					t.Errorf("sourceURLFromArg(%q) error must classify as"+
						" a caller fault: %v", tc.arg, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("sourceURLFromArg(%q) error: %v", tc.arg, err)
			}
			if got != tc.want {
				t.Errorf("sourceURLFromArg(%q) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}

func TestValidateSourceAndDiffDir(t *testing.T) {
	// Both ValidateSource and ValidateDiffDir share sourceURLFromArg —
	// assert they agree on a representative accept/reject pair so the
	// symmetry is locked in.
	p := Plugin{}
	good := mustParseURL(t, "gphotos:https://photos.app.goo.gl/AbCdEf123")
	bad := mustParseURL(t, "gphotos:https://example.com/")

	if err := p.ValidateSource(good, "gphotos:..."); err != nil {
		t.Errorf("ValidateSource(good): unexpected error %v", err)
	}
	if err := p.ValidateDiffDir(good, "gphotos:..."); err != nil {
		t.Errorf("ValidateDiffDir(good): unexpected error %v", err)
	}
	if err := p.ValidateSource(bad, "gphotos:..."); err == nil {
		t.Error("ValidateSource(bad): want error, got nil")
	}
	if err := p.ValidateDiffDir(bad, "gphotos:..."); err == nil {
		t.Error("ValidateDiffDir(bad): want error, got nil")
	}
}

func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", s, err)
	}
	return u
}

package cutting_garden_plugin_ytdlp

import (
	"net/url"
	"strings"
	"testing"
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
			name: "ytdlp opaque",
			arg:  "ytdlp:https://youtu.be/dQw4w9WgXcQ",
			want: "https://youtu.be/dQw4w9WgXcQ",
		},
		{
			name: "ytdlp opaque with query",
			arg:  "ytdlp:https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42s",
			want: "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42s",
		},
		{
			name: "ytdlp opaque arbitrary host",
			arg:  "ytdlp:https://vimeo.com/123456",
			want: "https://vimeo.com/123456",
		},
		{
			name: "ytdlp hierarchical",
			arg:  "ytdlp://youtu.be/dQw4w9WgXcQ",
			want: "https://youtu.be/dQw4w9WgXcQ",
		},
		{
			name: "ytdlp hierarchical empty",
			arg:  "ytdlp://",
			// url.Parse refuses to give us an empty-host hierarchical
			// shape cleanly; the relevant rejection lives in the
			// schemeless branch the planner falls through to. Tested
			// here as a sanity check rather than a hard contract.
			wantErr:  true,
			errSnips: []string{"empty source URL"},
		},
		{
			name: "https youtu.be",
			arg:  "https://youtu.be/dQw4w9WgXcQ",
			want: "https://youtu.be/dQw4w9WgXcQ",
		},
		{
			name: "https youtube.com",
			arg:  "https://youtube.com/watch?v=dQw4w9WgXcQ",
			want: "https://youtube.com/watch?v=dQw4w9WgXcQ",
		},
		{
			name: "https www.youtube.com",
			arg:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			want: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		},
		{
			name: "https m.youtube.com",
			arg:  "https://m.youtube.com/watch?v=dQw4w9WgXcQ",
			want: "https://m.youtube.com/watch?v=dQw4w9WgXcQ",
		},
		{
			name: "https music.youtube.com",
			arg:  "https://music.youtube.com/watch?v=dQw4w9WgXcQ",
			want: "https://music.youtube.com/watch?v=dQw4w9WgXcQ",
		},
		{
			name:     "https mixed-case host",
			arg:      "https://YouTube.COM/watch?v=dQw4w9WgXcQ",
			want:     "https://YouTube.COM/watch?v=dQw4w9WgXcQ",
			errSnips: nil,
		},
		{
			name:     "https off-allowlist host",
			arg:      "https://vimeo.com/123456",
			wantErr:  true,
			errSnips: []string{"YouTube allowlist", "ytdlp:"},
		},
		{
			name:     "http unsupported",
			arg:      "http://youtu.be/dQw4w9WgXcQ",
			wantErr:  true,
			errSnips: []string{"unsupported scheme"},
		},
		{
			name:     "ftp unsupported",
			arg:      "ftp://example.com/foo",
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
	// Both ValidateSource and ValidateDiffDir share sourceURLFromArg
	// — assert they agree on a representative accept/reject pair so
	// the symmetry is locked in.
	p := Plugin{}
	good := mustParseURL(t, "ytdlp:https://youtu.be/dQw4w9WgXcQ")
	bad := mustParseURL(t, "https://example.com/")

	if err := p.ValidateSource(good, "ytdlp:..."); err != nil {
		t.Errorf("ValidateSource(good): unexpected error %v", err)
	}
	if err := p.ValidateDiffDir(good, "ytdlp:..."); err != nil {
		t.Errorf("ValidateDiffDir(good): unexpected error %v", err)
	}
	if err := p.ValidateSource(bad, "https://..."); err == nil {
		t.Error("ValidateSource(bad): want error, got nil")
	}
	if err := p.ValidateDiffDir(bad, "https://..."); err == nil {
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

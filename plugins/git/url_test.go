package cutting_garden_plugin_git

import (
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func TestRemoteAndBranchFromArg(t *testing.T) {
	cases := []struct {
		name       string
		arg        string
		wantRemote string
		wantBranch string
		wantErr    bool
		errSnips   []string
	}{
		{
			name:       "opaque https with branch",
			arg:        "git:https://github.com/amarbel-llc/cutting-garden#main",
			wantRemote: "https://github.com/amarbel-llc/cutting-garden",
			wantBranch: "main",
		},
		{
			name:       "opaque https no branch defaults empty",
			arg:        "git:https://github.com/amarbel-llc/cutting-garden",
			wantRemote: "https://github.com/amarbel-llc/cutting-garden",
			wantBranch: "",
		},
		{
			name:       "opaque ssh scp-like with branch",
			arg:        "git:git@github.com:amarbel-llc/cutting-garden.git#feature/foo",
			wantRemote: "git@github.com:amarbel-llc/cutting-garden.git",
			wantBranch: "feature/foo",
		},
		{
			name:       "opaque with query glued back",
			arg:        "git:https://example.com/repo?token=abc#dev",
			wantRemote: "https://example.com/repo?token=abc",
			wantBranch: "dev",
		},
		{
			name:       "opaque local path",
			arg:        "git:/srv/repos/thing.git#trunk",
			wantRemote: "/srv/repos/thing.git",
			wantBranch: "trunk",
		},
		{
			name:       "hierarchical native git proto",
			arg:        "git://git.example.com/repo#main",
			wantRemote: "git://git.example.com/repo",
			wantBranch: "main",
		},
		{
			name:     "empty remote",
			arg:      "git:#main",
			wantErr:  true,
			errSnips: []string{"empty remote"},
		},
		{
			name:     "remote looks like an option",
			arg:      "git:-oProxyCommand=evil#main",
			wantErr:  true,
			errSnips: []string{"begins with '-'"},
		},
		{
			name:     "branch looks like an option",
			arg:      "git:https://github.com/x/y#-evil",
			wantErr:  true,
			errSnips: []string{"branch", "begins with '-'"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := mustParseURL(t, tc.arg)
			remote, branch, err := remoteAndBranchFromArg(u)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got remote=%q branch=%q", remote, branch)
				}
				for _, snip := range tc.errSnips {
					if !strings.Contains(err.Error(), snip) {
						t.Errorf("error %q missing %q", err.Error(), snip)
					}
				}
				// Every rejection here is a malformed CALLER argument and
				// must classify as a bad request, so the wire reports
				// -32602 rather than "plugin failed" (cutting-garden#187).
				if !errors.Is400BadRequest(err) {
					t.Errorf("remoteAndBranchFromArg(%q) error must"+
						" classify as a caller fault: %v", tc.arg, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if remote != tc.wantRemote {
				t.Errorf("remote = %q, want %q", remote, tc.wantRemote)
			}
			if branch != tc.wantBranch {
				t.Errorf("branch = %q, want %q", branch, tc.wantBranch)
			}
		})
	}
}

func TestCanonicalSource(t *testing.T) {
	cases := []struct {
		remote string
		branch string
		want   string
	}{
		{"https://github.com/x/y", "main", "https://github.com/x/y#main"},
		{"https://github.com/x/y", "", "https://github.com/x/y"},
		{"git://host/repo", "dev", "git://host/repo#dev"},
	}
	for _, tc := range cases {
		if got := canonicalSource(tc.remote, tc.branch); got != tc.want {
			t.Errorf("canonicalSource(%q, %q) = %q, want %q",
				tc.remote, tc.branch, got, tc.want)
		}
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

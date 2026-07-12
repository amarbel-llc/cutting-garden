package cutting_garden_plugin_git

import (
	"testing"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// TestAuthMethod_AnonymousTransports: http (no token), git://, and local
// paths need no auth — authMethod returns (nil, nil).
func TestAuthMethod_AnonymousTransports(t *testing.T) {
	t.Setenv(tokenEnv, "")
	for _, remote := range []string{
		"https://github.com/amarbel-llc/cutting-garden",
		"http://example.com/repo.git",
		"git://example.com/repo.git",
		"/srv/repos/thing.git",
		"./rel/repo",
	} {
		auth, err := authMethod(remote)
		if err != nil {
			t.Errorf("authMethod(%q) error = %v, want nil", remote, err)
		}
		if auth != nil {
			t.Errorf("authMethod(%q) = %v, want nil (anonymous)", remote, auth)
		}
	}
}

// TestAuthMethod_HTTPToken: with the token env set, http(s) remotes get
// TokenAuth.
func TestAuthMethod_HTTPToken(t *testing.T) {
	t.Setenv(tokenEnv, "ghp_example")
	auth, err := authMethod("https://github.com/private/repo.git")
	if err != nil {
		t.Fatalf("authMethod: %v", err)
	}
	tok, ok := auth.(*githttp.TokenAuth)
	if !ok {
		t.Fatalf("auth = %T, want *http.TokenAuth", auth)
	}
	if tok.Token != "ghp_example" {
		t.Errorf("token = %q, want %q", tok.Token, "ghp_example")
	}
}

// TestAuthMethod_SSHNeverAnonymous: ssh remotes must resolve to a concrete
// auth method (the agent) or a clear error — never silent anonymous, which
// go-git's ssh transport would reject mid-fetch with a vaguer message. The
// outcome depends on whether an ssh-agent is reachable in the environment,
// so assert only that it is not (nil, nil).
func TestAuthMethod_SSHNeverAnonymous(t *testing.T) {
	for _, remote := range []string{
		"ssh://git@code.linenisgreat.com/cutting-garden.git",
		"git@github.com:amarbel-llc/cutting-garden.git",
	} {
		auth, err := authMethod(remote)
		if auth == nil && err == nil {
			t.Errorf("authMethod(%q) = (nil, nil); ssh must yield an auth method or an error", remote)
		}
	}
}

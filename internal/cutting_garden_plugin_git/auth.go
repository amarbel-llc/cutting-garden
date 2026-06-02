package cutting_garden_plugin_git

import (
	"os"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// tokenEnv names the environment variable holding a token for
// authenticating http(s) git remotes (e.g. a GitHub PAT). Anonymous when
// unset — public http remotes need no auth.
const tokenEnv = "CUTTING_GARDEN_GIT_TOKEN"

// authMethod selects the go-git transport auth for a remote, keyed on its
// protocol:
//
//   - ssh / scp-like (git@host:path): the running SSH agent
//     (SSH_AUTH_SOCK), with the user parsed from the remote (default
//     "git"). go-git's ssh transport requires an explicit auth method, so
//     a missing agent surfaces as a clear error here rather than a vaguer
//     failure mid-fetch.
//   - http / https: a token from $CUTTING_GARDEN_GIT_TOKEN when set, else
//     anonymous (nil) — public remotes need nothing.
//   - git:// and local/file: anonymous (nil).
//
// Local and file transports never reach the network, so nil is correct
// there; the in-process file transport ignores auth entirely.
func authMethod(remote string) (transport.AuthMethod, error) {
	ep, err := transport.NewEndpoint(remote)
	if err != nil {
		// Let go-git surface the malformed-endpoint error at use; no auth.
		return nil, nil
	}

	switch ep.Protocol {
	case "ssh":
		user := ep.User
		if user == "" {
			user = "git"
		}
		auth, aerr := gitssh.NewSSHAgentAuth(user)
		if aerr != nil {
			return nil, errors.Wrapf(aerr,
				"git plugin: ssh auth for %s (is ssh-agent running?)", remote)
		}
		return auth, nil
	case "http", "https":
		if tok := os.Getenv(tokenEnv); tok != "" {
			return &githttp.TokenAuth{Token: tok}, nil
		}
		return nil, nil
	default:
		return nil, nil
	}
}

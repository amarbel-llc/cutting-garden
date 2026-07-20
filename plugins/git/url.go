package cutting_garden_plugin_git

import (
	"net/url"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// remoteAndBranchFromArg extracts the git remote URL and (optional)
// branch from a parsed CLI argument. The fragment names the branch; an
// empty branch means "resolve the remote's default branch (HEAD) at
// capture time". Two accepted forms:
//
//   - git:<remote-url>[#branch]      u.Opaque carries the remote verbatim
//     (any transport: https://, ssh://, git@host:path, /abs/path, …).
//     A `?query` is glued back onto the remote; the `#fragment` is the
//     branch, NOT part of the remote.
//
//   - git://<host>/<path>[#branch]   hierarchical native git protocol.
//     Reconstructed as `git://<host>/<path>`.
//
// remote is guaranteed non-empty and not to begin with `-` (which would
// be misread as an option by the git child process). branch, when
// present, is likewise guarded against a leading `-`.
func remoteAndBranchFromArg(u *url.URL) (remote, branch string, err error) {
	branch = u.Fragment

	switch {
	case u.Opaque != "":
		// Opaque form: `git:<remote>` where the remote does not begin
		// with `/` (https://, ssh://, git@host:path, relative ./repo).
		// url.Parse split any `?query` off the opaque segment; glue it
		// back so the inner remote round-trips intact. The fragment is
		// consumed as the branch above, so it is NOT re-glued.
		remote = u.Opaque
		if u.RawQuery != "" {
			remote += "?" + u.RawQuery
		}

	case u.Host != "":
		// Hierarchical form: `git://host/path`. Reconstruct the native
		// git-protocol remote.
		rebuilt := &url.URL{
			Scheme:   "git",
			Host:     u.Host,
			Path:     u.Path,
			RawQuery: u.RawQuery,
		}
		remote = rebuilt.String()

	case u.Path != "":
		// Absolute local-path form: `git:/srv/repos/thing.git`.
		// url.Parse treats a leading `/` as a path with empty host
		// rather than an opaque segment. The remote is the bare path.
		remote = u.Path
		if u.RawQuery != "" {
			remote += "?" + u.RawQuery
		}

	default:
		return "", "", errors.ErrorWithStackf(
			"git plugin: empty remote in %q\n"+
				"hint: pass `git:<remote-url>#<branch>` or "+
				"`git://<host>/<path>#<branch>`",
			u.String(),
		)
	}

	if remote == "" {
		return "", "", errors.ErrorWithStackf(
			"git plugin: empty remote in %q", u.String(),
		)
	}
	if strings.HasPrefix(remote, "-") {
		return "", "", errors.ErrorWithStackf(
			"git plugin: remote %q begins with '-'\n"+
				"hint: a remote must not look like a command-line option",
			remote,
		)
	}
	if strings.HasPrefix(branch, "-") {
		return "", "", errors.ErrorWithStackf(
			"git plugin: branch %q begins with '-'\n"+
				"hint: a branch must not look like a command-line option",
			branch,
		)
	}

	return remote, branch, nil
}

// canonicalSource is the stable identity stamped onto every EntryV1.Root
// for a git capture. It is derived purely from the argument (no network)
// so capture and diff agree on the same Root for the same arg, which is
// what entriesForRoot keys on.
//
// Following the npm/pip `<url>#<commit-ish>` convention:
//
//   - branch given  → "<remote>#<branch>"
//   - branch omitted → "<remote>"  (the resolved default branch lives in
//     ref.txt, not in the Root identity)
func canonicalSource(remote, branch string) string {
	if branch == "" {
		return remote
	}
	return remote + "#" + branch
}

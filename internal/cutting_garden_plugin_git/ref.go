package cutting_garden_plugin_git

import (
	"context"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// refFileName is the entry recording the captured branch's tip commit
// id (one line). It is both the merkle root pointer — naming the commit
// object whose subtree was stored — and the diff freshness key: the
// probe re-derives the tip cheaply via `git ls-remote` and compares
// blob-ids before paying for a full re-clone.
const refFileName = "ref.txt"

// resolveTip queries the remote for the tip commit of branch without
// cloning. When branch is empty it resolves the remote's default branch
// via the HEAD symref and returns its short name in resolvedBranch.
//
// This is the cheap operation the diff probe leans on: one network
// round-trip, no object transfer.
func resolveTip(
	ctx context.Context,
	remote, branch string,
) (resolvedBranch, commit string, err error) {
	if branch != "" {
		out, lsErr := gitOutput(ctx, "", "ls-remote", remote, "refs/heads/"+branch)
		if lsErr != nil {
			return "", "", lsErr
		}
		line := firstNonEmptyLine(out)
		if line == "" {
			return "", "", errors.ErrorWithStackf(
				"git plugin: branch %q not found on remote %q",
				branch, remote,
			)
		}
		return branch, strings.Fields(line)[0], nil
	}

	// Default branch: ask for HEAD with --symref so the response carries
	// both the symbolic target (the branch name) and its commit id.
	out, lsErr := gitOutput(ctx, "", "ls-remote", "--symref", remote, "HEAD")
	if lsErr != nil {
		return "", "", lsErr
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "ref:") {
			// "ref: refs/heads/main\tHEAD"
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				resolvedBranch = strings.TrimPrefix(fields[1], "refs/heads/")
			}
			continue
		}
		// "<commit>\tHEAD"
		if strings.HasSuffix(line, "HEAD") {
			commit = strings.Fields(line)[0]
		}
	}
	if resolvedBranch == "" || commit == "" {
		return "", "", errors.ErrorWithStackf(
			"git plugin: could not resolve default branch (HEAD) on remote %q\n"+
				"hint: the remote may be empty or unreachable",
			remote,
		)
	}
	return resolvedBranch, commit, nil
}

// firstNonEmptyLine returns the first non-blank line of s, trimmed.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

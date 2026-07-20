package cutting_garden_plugin_git

import (
	"context"
	"fmt"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/memory"
)

// listRemoteTip resolves a branch's current tip oid over the wire without
// transferring any objects — the go-git equivalent of `git ls-remote`, and
// the cheap freshness probe behind diff (property 2: a matching tip means
// the whole reachable object set is unchanged, so nothing is fetched).
//
// When branch is empty the remote's default branch (HEAD) is resolved.
// Returns the resolved branch name (never empty) alongside the tip so the
// caller can fetch that exact branch.
func listRemoteTip(
	ctx context.Context,
	remote, branch string,
) (resolvedBranch, tip string, err error) {
	auth, err := authMethod(remote)
	if err != nil {
		return "", "", err
	}
	rem := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{remote},
	})
	refs, err := rem.ListContext(ctx, &git.ListOptions{Auth: auth})
	if err != nil {
		return "", "", errors.Wrapf(err, "git plugin: list-remote %s", remote)
	}

	if branch != "" {
		want := plumbing.NewBranchReferenceName(branch)
		for _, r := range refs {
			if r.Name() == want {
				return branch, r.Hash().String(), nil
			}
		}
		return "", "", errors.ErrorWithStackf(
			"git plugin: branch %q not found on %s", branch, remote,
		)
	}

	// Default branch: resolve HEAD. A symbolic HEAD names the branch it
	// points at; resolve that branch's tip. A detached/hash HEAD is used
	// directly, with the branch reported as the short HEAD name.
	var headTarget plumbing.ReferenceName
	for _, r := range refs {
		if r.Name() != plumbing.HEAD {
			continue
		}
		if r.Type() == plumbing.SymbolicReference {
			headTarget = r.Target()
		} else {
			return r.Name().Short(), r.Hash().String(), nil
		}
	}
	if headTarget != "" {
		for _, r := range refs {
			if r.Name() == headTarget {
				return headTarget.Short(), r.Hash().String(), nil
			}
		}
	}
	return "", "", errors.ErrorWithStackf(
		"git plugin: cannot resolve default branch (HEAD) on %s", remote,
	)
}

// fetchBranchInto fetches branch from remote into st over go-git's
// transport. go-git advertises whatever objects st already holds (via its
// references) as `have`s, so when st is seeded from a prior snapshot only
// the delta crosses the wire. An already-satisfied fetch
// (NoErrAlreadyUpToDate) is treated as success.
func fetchBranchInto(ctx context.Context, st storage.Storer, remote, branch string) error {
	auth, err := authMethod(remote)
	if err != nil {
		return err
	}
	rem := git.NewRemote(st, &config.RemoteConfig{
		Name: "origin",
		URLs: []string{remote},
	})
	refspec := config.RefSpec(fmt.Sprintf(
		"+refs/heads/%s:refs/remotes/origin/%s", branch, branch,
	))
	if err := rem.FetchContext(ctx, &git.FetchOptions{
		RefSpecs: []config.RefSpec{refspec},
		Tags:     git.NoTags,
		Auth:     auth,
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return errors.Wrapf(err, "git plugin: fetch %s from %s", branch, remote)
	}
	return nil
}

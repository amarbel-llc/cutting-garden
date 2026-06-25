package jira

import (
	"context"
	"sort"

	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cutting_garden_plugins.ProtocolDiffPlugin = (*Plugin)(nil)

// ProtocolKind routes a jira receipt to this diff handler (diff dispatch by
// receipt kind, RFC 0002 / FDR 0019). Restore is intentionally not
// registered — capture is read-only and writing issues back to a live
// tracker is a lossy, destructive mutation (see init.go).
func (Plugin) ProtocolKind() string { return captureKind }

// DiffProtocol compares a jira receipt against a live Jira source using the
// issue `updated` timestamp as the cheap freshness oracle (FDR 0019 §Diff),
// so the comparison descends only changed issues and transfers no bodies.
// It runs the same bodiless `updated` probe the incremental capture uses:
//
//   - an issue key present live but absent from the receipt → A (added),
//   - present in both with an advanced `updated` → M (modified),
//   - present in the receipt but absent live → D (deleted),
//   - present in both with an unchanged `updated` → clean, no descent.
//
// Differences are sorted A/M/D lines keyed by issue key. This is strictly
// read-only: only GET and the POST search query are issued; nothing is
// written to Jira.
func (Plugin) DiffProtocol(
	req cutting_garden_plugins.ProtocolDiffRequest,
) (cutting_garden_plugins.ProtocolDiffResult, error) {
	base, username, token, err := connectionFromArg(req.Source)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}
	origin, projectKey, issueKey, err := nodeFromBase(base)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}
	c := newClient(origin, username, token)

	// Captured per-issue freshness, from the receipt's outcome reuse index.
	prior := loadPriorIndex(req.BlobStore, req.ReceiptDigest)

	// Live freshness, via the same bodiless probe as incremental capture.
	live, err := liveIssueUpdates(req.Context, c, projectKey, issueKey)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	var added, modified []string
	seen := make(map[string]bool, len(live))
	for _, u := range live {
		seen[u.key] = true
		p, known := prior[u.key]
		switch {
		case !known:
			added = append(added, "A "+u.key)
		case p.updated != u.updated:
			modified = append(modified, "M "+u.key)
		}
	}

	var deleted []string
	for key := range prior {
		if !seen[key] {
			deleted = append(deleted, "D "+key)
		}
	}

	sort.Strings(added)
	sort.Strings(modified)
	sort.Strings(deleted)

	differences := make([]string, 0, len(added)+len(modified)+len(deleted))
	differences = append(differences, added...)
	differences = append(differences, modified...)
	differences = append(differences, deleted...)

	return cutting_garden_plugins.ProtocolDiffResult{Differences: differences}, nil
}

// liveIssueUpdates runs the bodiless `updated` probe against the diff
// source, scoped the same way capture is: a single issue, one named
// project, or every browsable project for a bare-host source.
func liveIssueUpdates(
	ctx context.Context,
	c *client,
	projectKey, issueKey string,
) ([]issueUpdate, error) {
	switch {
	case issueKey != "":
		u, err := c.issueUpdate(ctx, issueKey)
		if err != nil {
			return nil, err
		}
		return []issueUpdate{u}, nil
	case projectKey != "":
		return c.searchIssueUpdates(ctx, jqlForProject(projectKey))
	default:
		projects, err := c.listProjects(ctx)
		if err != nil {
			return nil, err
		}
		var updates []issueUpdate
		for _, p := range projects {
			us, serr := c.searchIssueUpdates(ctx, jqlForProject(p.key))
			if serr != nil {
				return nil, serr
			}
			updates = append(updates, us...)
		}
		return updates, nil
	}
}

package jira

import (
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ScanForDiff re-fetches every issue under the node and returns one EntryV1
// per issue with a freshly computed blob-id (hashed through the caller's
// discard store — no bytes persisted). Entry keys (Path) match what
// CaptureRoot produced, so the diff comparator localizes added/removed/
// modified issues. Per-issue failures aggregate into the returned error —
// diff is read-only and atomic.
func (Plugin) ScanForDiff(
	req cutting_garden_plugins.DiffScanRequest,
) ([]capture_receipt.EntryV1, error) {
	base, username, token, err := connectionFromArg(req.Dir)
	if err != nil {
		return nil, err
	}
	origin, projectKey, issueKey, err := nodeFromBase(base)
	if err != nil {
		return nil, err
	}
	c := newClient(origin, username, token)

	// Single-issue node: one fetch, no project walk.
	if issueKey != "" {
		iss, err := c.getIssue(req.Context, issueKey, allFields)
		if err != nil {
			return nil, err
		}
		entry, _, err := storeIssue(req.Context, req.BlobStore, origin, iss)
		if err != nil {
			return nil, err
		}
		return []capture_receipt.EntryV1{entry}, nil
	}

	projects := []string{projectKey}
	if projectKey == "" {
		discovered, err := c.listProjects(req.Context)
		if err != nil {
			return nil, err
		}
		projects = projects[:0]
		for _, p := range discovered {
			projects = append(projects, p.key)
		}
	}

	var (
		entries  []capture_receipt.EntryV1
		failures []string
	)
	for _, pk := range projects {
		issues, searchErr := c.searchIssues(req.Context, jqlForProject(pk), allFields)
		if searchErr != nil {
			failures = append(failures, pk+": "+searchErr.Error())
			continue
		}
		for _, iss := range issues {
			entry, path, writeErr := storeIssue(req.Context, req.BlobStore, origin, iss)
			if writeErr != nil {
				failures = append(failures, path+": "+writeErr.Error())
				continue
			}
			entries = append(entries, entry)
		}
	}

	if len(failures) > 0 {
		return nil, errors.ErrorWithStackf(
			"jira plugin: %d failures during diff scan:\n  %s",
			len(failures), strings.Join(failures, "\n  "),
		)
	}

	return entries, nil
}

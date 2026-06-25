package jira

import (
	"github.com/amarbel-llc/cutting-garden/pkgs/capture_events"
	"github.com/amarbel-llc/cutting-garden/pkgs/capture_failures"
	"github.com/amarbel-llc/cutting-garden/pkgs/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// CaptureRoot snapshots Jira issues under the source node. The node's path
// selects the scope:
//
//   - jira://host/PROJECT/KEY-1 — a single issue.
//   - jira://host/PROJECT       — every issue in one project.
//   - jira://host               — every issue in every browsable project.
//
// Each issue is fetched as its full `*all`-fields resource, canonicalized,
// and streamed into req.BlobStore as one file entry keyed by
// `PROJECT/KEY.json`. Per-project search failures and per-issue write
// failures are reported on the stream and counted; the projects that did
// succeed still contribute their entries.
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	r := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	base, username, token, err := connectionFromArg(req.Source)
	if err != nil {
		r.Failure(req.RawArg, err)
		return rootFailure(req.RawArg, err)
	}
	origin, projectKey, issueKey, err := nodeFromBase(base)
	if err != nil {
		r.Failure(req.RawArg, err)
		return rootFailure(req.RawArg, err)
	}
	c := newClient(origin, username, token)

	var (
		entries  []capture_receipt.EntryV1
		failures []capture_failures.FailureV1
	)
	// recordFailure pairs every stream Failure with a durable FailureV1 so
	// the capture's failure receipt records what went wrong; the returned
	// FailCount is derived from len(failures), keeping the two 1:1 per the
	// CaptureRootResult contract.
	recordFailure := func(failPath, op string, failErr error) {
		failures = append(failures, capture_failures.FailureV1{
			Root:  origin,
			Path:  failPath,
			Op:    op,
			Error: failErr.Error(),
		})
	}

	// Single-issue node: fetch and store exactly that issue.
	if issueKey != "" {
		r.PhaseStart("capture issue " + issueKey)
		iss, getErr := c.getIssue(req.Context, issueKey, allFields)
		if getErr != nil {
			r.PhaseEnd(capture_events.Verdict{
				OK:         false,
				Diagnostic: map[string]any{"error": getErr.Error()},
			})
			r.Failure(issueKey, getErr)
			recordFailure(issueKey, capture_failures.OpPlugin, getErr)
			return cutting_garden_plugins.CaptureRootResult{FailCount: len(failures), Failures: failures}
		}
		entry, path, writeErr := storeIssue(req.Context, req.BlobStore, origin, iss)
		if writeErr != nil {
			r.PhaseEnd(capture_events.Verdict{OK: false})
			r.Failure(path, writeErr)
			recordFailure(path, capture_failures.OpBlobWrite, writeErr)
			return cutting_garden_plugins.CaptureRootResult{FailCount: len(failures), Failures: failures}
		}
		entries = append(entries, entry)
		r.Entry(entry)
		r.PhaseEnd(capture_events.Verdict{OK: true})
		return cutting_garden_plugins.CaptureRootResult{Entries: entries}
	}

	// Resolve the set of projects to walk: the named one, or every
	// browsable project for a bare-host root.
	projects := []string{projectKey}
	if projectKey == "" {
		r.PhaseStart("list projects " + origin)
		discovered, listErr := c.listProjects(req.Context)
		if listErr != nil {
			r.PhaseEnd(capture_events.Verdict{
				OK:         false,
				Diagnostic: map[string]any{"error": listErr.Error()},
			})
			r.Failure(req.RawArg, listErr)
			return rootFailure(req.RawArg, listErr)
		}
		projects = projects[:0]
		for _, p := range discovered {
			projects = append(projects, p.key)
		}
		r.PhaseEnd(capture_events.Verdict{
			OK:         true,
			Diagnostic: map[string]any{"projects": len(projects)},
		})
	}

	for _, pk := range projects {
		r.PhaseStart("capture " + pk)
		failAtPhaseStart := len(failures)

		issues, searchErr := c.searchIssues(req.Context, jqlForProject(pk), allFields)
		if searchErr != nil {
			r.Failure(pk, searchErr)
			recordFailure(pk, capture_failures.OpPlugin, searchErr)
			r.PhaseEnd(capture_events.Verdict{
				OK:         false,
				Diagnostic: map[string]any{"error": searchErr.Error()},
			})
			continue
		}

		for _, iss := range issues {
			entry, path, writeErr := storeIssue(req.Context, req.BlobStore, origin, iss)
			if writeErr != nil {
				r.Failure(path, writeErr)
				recordFailure(path, capture_failures.OpBlobWrite, writeErr)
				continue
			}
			entries = append(entries, entry)
			r.Entry(entry)
			r.Progress(cutting_garden_plugins.ReportProgress{
				Item:  pk,
				Items: int64(len(entries)),
			})
		}

		if phaseFailed := len(failures) - failAtPhaseStart; phaseFailed == 0 {
			r.PhaseEnd(capture_events.Verdict{OK: true})
		} else {
			r.PhaseEnd(capture_events.Verdict{
				OK:         false,
				Diagnostic: map[string]any{"failed": phaseFailed},
			})
		}
	}

	return cutting_garden_plugins.CaptureRootResult{
		Entries:   entries,
		FailCount: len(failures),
		Failures:  failures,
	}
}

// rootFailure shapes a whole-arg plugin failure (connection setup or
// project discovery) as a one-element result. The failure has no per-entry
// identity below the root, so Path mirrors Root per the CaptureRootResult
// contract.
func rootFailure(rawArg string, err error) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{
		FailCount: 1,
		Failures: []capture_failures.FailureV1{{
			Root:  rawArg,
			Path:  rawArg,
			Op:    capture_failures.OpPlugin,
			Error: err.Error(),
		}},
	}
}

package caldav

import (
	"path"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_events"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_failures"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// capturedComponents is the set of iCalendar component types the plugin
// fetches from each calendar: tasks (VTODO), events (VEVENT), and journal
// notes (VJOURNAL). Free/busy (VFREEBUSY) is out of scope until a use case
// appears.
var capturedComponents = []string{"VTODO", "VEVENT", "VJOURNAL"}

// CaptureRoot discovers every calendar under the source endpoint, fetches
// each VTODO/VEVENT resource as raw text/calendar, and streams each body
// into req.BlobStore as one file entry keyed by the resource's
// server-absolute path. Per-calendar fetch failures and per-resource
// write failures are reported on the stream and counted; the calendars
// that did succeed still contribute their entries.
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	r := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	base, username, password, err := connectionFromArg(req.Source)
	if err != nil {
		r.Failure(req.RawArg, err)
		return rootFailure(req.RawArg, err)
	}
	c := newClient(base, username, password)
	origin, _ := originOf(base)

	r.PhaseStart("list calendars " + base)
	_, calendars, err := c.discoverCalendars(req.Context)
	if err != nil {
		r.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Diagnostic: map[string]any{"error": err.Error()},
		})
		r.Failure(req.RawArg, err)
		return rootFailure(req.RawArg, err)
	}
	r.PhaseEnd(capture_events.Verdict{
		OK:         true,
		Diagnostic: map[string]any{"calendars": len(calendars)},
	})

	var (
		entries  []capture_receipt.EntryV1
		failures []capture_failures.FailureV1
	)
	// recordFailure pairs every stream Failure with a durable FailureV1
	// so the capture's failure receipt records what went wrong; the
	// returned FailCount is derived from len(failures), keeping the two
	// 1:1 per the CaptureRootResult contract.
	recordFailure := func(failPath, op string, failErr error) {
		failures = append(failures, capture_failures.FailureV1{
			Root:  origin,
			Path:  failPath,
			Op:    op,
			Error: failErr.Error(),
		})
	}

	for _, cal := range calendars {
		label := calendarLabel(cal)
		r.PhaseStart("capture " + label)
		failAtPhaseStart := len(failures)

		for _, component := range capturedComponents {
			resources, listErr := c.listResources(req.Context, cal.href, component)
			if listErr != nil {
				r.Failure(cal.href, listErr)
				recordFailure(cal.href, capture_failures.OpPlugin, listErr)
				continue
			}

			for _, res := range resources {
				entry, rel, writeErr := storeResource(
					req.Context, req.BlobStore, c, origin, res,
				)
				if rel == "" {
					// Resolves to the collection root — no object to
					// capture; skip rather than emit a pathless entry.
					continue
				}
				if writeErr != nil {
					r.Failure(rel, writeErr)
					recordFailure(rel, capture_failures.OpBlobWrite, writeErr)
					continue
				}

				entries = append(entries, entry)
				r.Entry(entry)
				r.Progress(cutting_garden_plugins.ReportProgress{
					Item:  label,
					Items: int64(len(entries)),
				})
			}
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
// calendar discovery) as a one-element result. The failure has no
// per-entry identity below the root, so Path mirrors Root per the
// CaptureRootResult contract.
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

// resourceMode is the synthetic permission recorded for every captured
// CalDAV resource. Remote objects have no filesystem mode; 0644 is the
// natural mode for the `.ics` files they materialize into on a local
// restore.
const resourceMode = 0o644

// calendarLabel is a short human label for phase output: the calendar's
// display name when the server provided one, else the last path segment
// of its href.
func calendarLabel(cal calendar) string {
	if cal.displayName != "" {
		return cal.displayName
	}
	trimmed := strings.TrimRight(cal.href, "/")
	if base := path.Base(trimmed); base != "" && base != "." && base != "/" {
		return base
	}
	return cal.href
}

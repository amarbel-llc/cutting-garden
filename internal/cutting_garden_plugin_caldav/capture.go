package cutting_garden_plugin_caldav

import (
	"path"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_events"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// capturedComponents is the set of iCalendar component types the plugin
// fetches from each calendar. Tasks (VTODO) and events (VEVENT) cover
// the surface the bob caldav tool managed; journals (VJOURNAL) and
// free/busy are out of scope until a use case appears.
var capturedComponents = []string{"VTODO", "VEVENT"}

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
		return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
	}
	c := newClient(base, username, password)
	origin, _ := originOf(base)

	r.PhaseStart("list calendars " + base)
	calendars, err := c.listCalendars(req.Context)
	if err != nil {
		r.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Diagnostic: map[string]any{"error": err.Error()},
		})
		r.Failure(req.RawArg, err)
		return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
	}
	r.PhaseEnd(capture_events.Verdict{
		OK:         true,
		Diagnostic: map[string]any{"calendars": len(calendars)},
	})

	var (
		entries   []capture_receipt.EntryV1
		failCount int
	)

	for _, cal := range calendars {
		label := calendarLabel(cal)
		r.PhaseStart("capture " + label)
		failAtPhaseStart := failCount

		for _, component := range capturedComponents {
			resources, listErr := c.listResources(req.Context, cal.href, component)
			if listErr != nil {
				r.Failure(cal.href, listErr)
				failCount++
				continue
			}

			for _, res := range resources {
				entry, rel, writeErr := storeResource(
					req.Context, req.BlobStore, c, origin, res)
				if rel == "" {
					// Resolves to the collection root — no object to
					// capture; skip rather than emit a pathless entry.
					continue
				}
				if writeErr != nil {
					r.Failure(rel, writeErr)
					failCount++
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

		if phaseFailed := failCount - failAtPhaseStart; phaseFailed == 0 {
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
		FailCount: failCount,
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

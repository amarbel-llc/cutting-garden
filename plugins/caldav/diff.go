package caldav

import (
	"strings"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ScanForDiff re-fetches every VTODO/VEVENT resource under the endpoint
// and returns one EntryV1 per resource with a freshly computed blob-id
// (hashed through the caller's discard store — no bytes persisted).
// Entry keys (Path) match what CaptureRoot produced, so the diff
// comparator localizes added/removed/modified resources. Per-resource
// failures aggregate into the returned error — diff is read-only and
// atomic.
func (Plugin) ScanForDiff(
	req cutting_garden_plugins.DiffScanRequest,
) ([]capture_receipt.EntryV1, error) {
	base, username, password, err := connectionFromArg(req.Dir)
	if err != nil {
		return nil, err
	}
	c := newClient(base, username, password)
	origin, _ := originOf(base)

	_, calendars, err := c.discoverCalendars(req.Context)
	if err != nil {
		return nil, err
	}

	var (
		entries  []capture_receipt.EntryV1
		failures []string
	)

	for _, cal := range calendars {
		for _, component := range capturedComponents {
			resources, listErr := c.listResources(req.Context, cal.href, component)
			if listErr != nil {
				failures = append(failures, listErr.Error())
				continue
			}

			for _, res := range resources {
				entry, rel, writeErr := storeResource(
					req.Context, req.BlobStore, c, origin, res,
				)
				if rel == "" {
					continue
				}
				if writeErr != nil {
					failures = append(failures, rel+": "+writeErr.Error())
					continue
				}

				entries = append(entries, entry)
			}
		}
	}

	if len(failures) > 0 {
		return nil, errors.ErrorWithStackf(
			"caldav plugin: %d failures during diff scan:\n  %s",
			len(failures), strings.Join(failures, "\n  "),
		)
	}

	return entries, nil
}

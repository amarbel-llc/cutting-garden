package caldav

import (
	"context"
	"sort"
	"strings"

	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/pkgs/plugin_blob_io"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.ProtocolDiffPlugin = (*Plugin)(nil)

// ProtocolKind routes a caldav receipt to this diff handler (restore/diff
// dispatch by receipt kind, RFC 0010 / RFC 0011).
func (Plugin) ProtocolKind() string { return captureKind }

// DiffProtocol compares a caldav receipt against a live caldav source by
// native identity (RFC 0011 §Diff). It runs the cheap freshness probe — a
// getetag-only REPORT per (calendar, component) — and compares each live
// etag to the receipt's recorded {href, etag}. Resources whose etag is
// unchanged are clean and never have their bodies transferred. Only
// new/moved resources are re-fetched (to compute their native id and
// confirm a real body change by markl-id); removed resources are reported
// from the receipt alone. Differences are A/D/M lines keyed by native
// identity.
func (Plugin) DiffProtocol(
	req cutting_garden_plugins.ProtocolDiffRequest,
) (cutting_garden_plugins.ProtocolDiffResult, error) {
	base, username, password, err := connectionFromArg(req.Source)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}
	c := newClient(base, username, password)

	payload, meta, err := loadReceiptPayload(req.BlobStore, req.ReceiptDigest)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	// Index the captured records by href (the probe's match key) and the
	// captured leaf digests by native id (from the payload's object refs),
	// so a known-href etag move can be confirmed as a real body change.
	capturedByHref := make(map[string]resourceRecord, len(meta.Resources))
	for _, rec := range meta.Resources {
		capturedByHref[rec.Href] = rec
	}
	capturedDigestByID := make(map[string]string, len(payload.Refs))
	for _, ref := range payload.Refs {
		capturedDigestByID[ref.Alias] = ref.Digest
	}
	seen := make(map[string]bool, len(meta.Resources))

	_, calendars, err := c.discoverCalendars(req.Context)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	var added, modified []string
	for _, cal := range calendars {
		collection := collectionKey(cal.href)
		for _, component := range capturedComponents {
			live, err := c.listObjectEtags(req.Context, cal.href, component)
			if err != nil {
				return cutting_garden_plugins.ProtocolDiffResult{}, err
			}

			for _, lo := range live {
				href := serverPath(c.resolveHref(lo.href))
				if href == "" {
					continue
				}
				rec, known := capturedByHref[href]
				if known {
					seen[href] = true
					// Unchanged when both sides carry a non-empty, equal etag
					// — the freshness fast path, no body transfer.
					if lo.etag != "" && rec.Etag != "" && lo.etag == rec.Etag {
						continue
					}
				}

				// New, or possibly-modified: fetch the body to compute the
				// native id and the current markl-id.
				id, digest, err := c.fetchIdentity(
					req.Context, req.BlobStore, collection, component, lo.href,
				)
				if err != nil {
					return cutting_garden_plugins.ProtocolDiffResult{}, err
				}
				if !known {
					added = append(added, "A "+id)
					continue
				}
				// Known href whose etag moved (or was absent on either side):
				// a real change only if the stored body digest differs from
				// what the receipt captured for this id.
				if digest != capturedDigestByID[rec.ID] {
					modified = append(modified, "M "+id)
				}
			}
		}
	}

	var deleted []string
	for _, rec := range meta.Resources {
		if !seen[rec.Href] {
			deleted = append(deleted, "D "+rec.ID)
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

// fetchIdentity fetches one live resource's body, hashes it through the
// caller's store to get its current markl-id, and parses its UID to build
// the native identity. The body is content-addressed into the store (diff
// shares capture's addressing), so a subsequent capture dedups it.
func (c *client) fetchIdentity(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	collection, component, rawHref string,
) (id, digest string, err error) {
	body, err := c.getResource(ctx, rawHref)
	if err != nil {
		return "", "", err
	}
	uid, err := uidOf(component, body)
	if err != nil {
		return "", "", errors.Wrapf(err, "caldav plugin: identity for %s", rawHref)
	}
	d, _, err := plugin_blob_io.WriteReaderBlob(ctx, store, strings.NewReader(body))
	if err != nil {
		return "", "", errors.Wrapf(err, "caldav plugin: hash %s", rawHref)
	}
	return objectIdentity(collection, component, uid), d.String(), nil
}

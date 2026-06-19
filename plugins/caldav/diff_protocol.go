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
// **native identity** (RFC 0011 §Diff) — host-independent, so a receipt
// diffs cleanly against the source it was captured from OR a different
// server holding the same logical objects.
//
// It runs the freshness probe (listObjectEtags): a REPORT requesting
// getetag + a UID-only calendar-data projection, so each live resource's
// {etag, uid} is learned without transferring its body. The native id is
// (collection, component, uid). Resources whose native id is in the
// receipt with an unchanged etag are clean — no body transfer. New/moved
// resources are re-fetched only to confirm a real body change by markl-id;
// removed ones are reported from the receipt. A server that ignores the
// UID projection yields an empty uid; that single resource falls back to a
// full fetch to learn its native id. Differences are A/D/M lines keyed by
// native identity.
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

	// Captured state, keyed by native id: the etag (freshness) and the
	// leaf digest (the real-change confirmation).
	capturedEtag := make(map[string]string, len(meta.Resources))
	for _, rec := range meta.Resources {
		capturedEtag[rec.ID] = rec.Etag
	}
	capturedDigest := make(map[string]string, len(payload.Refs))
	for _, ref := range payload.Refs {
		capturedDigest[ref.Alias] = ref.Digest
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
				id, knownEtag, isNew, err := c.resolveLiveIdentity(
					req.Context, req.BlobStore, collection, component, lo, capturedEtag,
				)
				if err != nil {
					return cutting_garden_plugins.ProtocolDiffResult{}, err
				}
				seen[id] = true

				if isNew {
					added = append(added, "A "+id)
					continue
				}
				// Known native id with an unchanged etag: clean, no fetch.
				if lo.etag != "" && knownEtag != "" && lo.etag == knownEtag {
					continue
				}
				// Etag moved (or absent on either side): confirm a real body
				// change by digest before reporting M.
				digest, err := c.fetchDigest(req.Context, req.BlobStore, lo.href)
				if err != nil {
					return cutting_garden_plugins.ProtocolDiffResult{}, err
				}
				if digest != capturedDigest[id] {
					modified = append(modified, "M "+id)
				}
			}
		}
	}

	var deleted []string
	for _, rec := range meta.Resources {
		if !seen[rec.ID] {
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

// resolveLiveIdentity computes a live probed object's native id and reports
// whether it is new (absent from the receipt). The probe usually carries
// the UID (so the native id needs no fetch); when the server ignored the
// projection (empty uid), it falls back to a full body fetch to learn the
// UID. knownEtag is the captured etag for that native id (empty when new).
func (c *client) resolveLiveIdentity(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	collection, component string,
	lo probedObject,
	capturedEtag map[string]string,
) (id, knownEtag string, isNew bool, err error) {
	uid := lo.uid
	if uid == "" {
		// Server ignored the UID projection — fall back to a full fetch.
		body, ferr := c.getResource(ctx, lo.href)
		if ferr != nil {
			return "", "", false, ferr
		}
		uid, ferr = uidOf(component, body)
		if ferr != nil {
			return "", "", false, errors.Wrapf(ferr, "caldav plugin: identity for %s", lo.href)
		}
	}
	id = objectIdentity(collection, component, uid)
	etag, known := capturedEtag[id]
	return id, etag, !known, nil
}

// fetchDigest fetches one resource's body and hashes it through the store
// to its current markl-id (the body is content-addressed into the store,
// so a later capture dedups it). Used to confirm a real change when an
// etag moved.
func (c *client) fetchDigest(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	rawHref string,
) (string, error) {
	body, err := c.getResource(ctx, rawHref)
	if err != nil {
		return "", err
	}
	d, _, err := plugin_blob_io.WriteReaderBlob(ctx, store, strings.NewReader(body))
	if err != nil {
		return "", errors.Wrapf(err, "caldav plugin: hash %s", rawHref)
	}
	return d.String(), nil
}

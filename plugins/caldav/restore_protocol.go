package caldav

import (
	"strings"

	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.ProtocolRestorePlugin = (*Plugin)(nil)

// RestoreProtocol reconstructs a caldav receipt's resources onto a
// destination CalDAV endpoint (RFC 0011 §Restore). Routing is by receipt
// kind (ProtocolKind), not destination scheme.
//
// Each object is restored to its native identity (the payload ref alias,
// <collection>/<component>/<UID>), NOT the server path it was captured
// from — so a capture restores cleanly to a different host with a
// different layout. To turn the host-independent collection NAME into a
// concrete destination path, restore PROPFINDs the destination and matches
// the collection name against the destination's own calendars, then PUTs
// <matched-calendar-href>/<UID>.ics. This "restore the tree as natively as
// possible, querying the destination for its real layout" approach is the
// general pattern for tree-mutating plugins (see RFC 0011 / FDR guidance).
//
// A collection with no match on the destination is an error naming it:
// creating a missing collection (MKCALENDAR, plus its metadata) is its own
// follow-up phase, tracked in #77.
func (Plugin) RestoreProtocol(req cutting_garden_plugins.ProtocolRestoreRequest) error {
	base, username, password, err := connectionFromArg(req.Dest)
	if err != nil {
		return err
	}
	c := newClient(base, username, password)

	// Map the destination's collection NAMES to their real hrefs, so a
	// native-identity <collection> resolves to wherever that collection
	// actually lives on this server.
	_, destCalendars, err := c.discoverCalendars(req.Context)
	if err != nil {
		return errors.Wrapf(err, "caldav plugin: discover destination calendars")
	}
	hrefByCollection := make(map[string]string, len(destCalendars))
	for _, cal := range destCalendars {
		hrefByCollection[collectionKey(cal.href)] = cal.href
	}

	payload, _, err := loadReceiptPayload(req.BlobStore, req.ReceiptDigest)
	if err != nil {
		return err
	}

	for _, ref := range payload.Refs {
		if err := req.Context.Err(); err != nil {
			return errors.Wrap(err)
		}

		collection, _, uid, perr := parseObjectIdentity(ref.Alias)
		if perr != nil {
			return perr
		}

		calHref, ok := hrefByCollection[collection]
		if !ok {
			return errors.ErrorWithStackf(
				"caldav plugin: destination has no collection %q for %s "+
					"(MKCALENDAR on restore is tracked in #77)",
				collection, ref.Alias,
			)
		}

		body, err := readBlob(req.Context, req.BlobStore, ref.Digest)
		if err != nil {
			return errors.Wrapf(err, "read object %s", ref.Alias)
		}

		target := strings.TrimRight(calHref, "/") + "/" + uid + ".ics"
		if err := c.putResource(req.Context, target, body); err != nil {
			return errors.Wrapf(err, "caldav plugin: restore %s", ref.Alias)
		}
	}

	return nil
}

// parseObjectIdentity splits a native-identity alias
// <collection>/<component>/<UID> back into its parts. The UID may itself
// contain `/` (iCalendar UIDs are opaque), so it is everything after the
// second separator; collection and component never contain `/`.
func parseObjectIdentity(alias string) (collection, component, uid string, err error) {
	first := strings.IndexByte(alias, '/')
	if first < 0 {
		return "", "", "", errors.ErrorWithStackf(
			"caldav plugin: malformed object identity %q (want <collection>/<component>/<UID>)", alias,
		)
	}
	rest := alias[first+1:]
	second := strings.IndexByte(rest, '/')
	if second < 0 {
		return "", "", "", errors.ErrorWithStackf(
			"caldav plugin: malformed object identity %q (want <collection>/<component>/<UID>)", alias,
		)
	}
	collection = alias[:first]
	component = rest[:second]
	uid = rest[second+1:]
	if collection == "" || component == "" || uid == "" {
		return "", "", "", errors.ErrorWithStackf(
			"caldav plugin: empty segment in object identity %q", alias,
		)
	}
	return collection, component, uid, nil
}

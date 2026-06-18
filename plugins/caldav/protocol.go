package caldav

import (
	"context"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_events"
	"github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/plugins/caldav/ical"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// CaptureProtocol implements cutting_garden_plugins.ProtocolCapturePlugin:
// it stores each VTODO/VEVENT body as a content-addressed object leaf and
// wraps them in an RFC 0011 receipt merkle tree (receipt → identity →
// environment, outcome, payload). The payload node references each object
// by its native identity (<collection>/<component>/<UID>) and records its
// server etag for the diff freshness probe. Returns the root receipt's
// markl id.
func (Plugin) CaptureProtocol(
	req cutting_garden_plugins.ProtocolCaptureRequest,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	// Non-identity observability: Plan/Progress/Log MUST NOT influence any
	// stored bytes or the receipt identity. ReporterOrNop makes a nil
	// Reporter a no-op so the emission sites stay unconditional.
	r := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	base, username, password, err := connectionFromArg(req.Source)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}
	c := newClient(base, username, password)
	origin, _ := originOf(base)

	w := capture_plugin.NewBlobStoreWriter(req.BlobStore)

	r.PhaseStart("list calendars " + base)
	_, calendars, err := c.discoverCalendars(req.Context)
	if err != nil {
		r.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Diagnostic: map[string]any{"error": err.Error()},
		})
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}
	r.PhaseEnd(capture_events.Verdict{
		OK:         true,
		Diagnostic: map[string]any{"calendars": len(calendars)},
	})

	objects, err := storeObjects(req.Context, w, c, origin, calendars, r)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	res, err := writeCaldavReceipt(req.Context, w, base, req.BinaryVersion, objects)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}
	return res, nil
}

// capturedObject is one stored VTODO/VEVENT: its native-identity alias,
// the stored leaf's markl digest, and the server etag (the payload
// freshness record). Collected, sorted by id, then folded into the
// payload node.
type capturedObject struct {
	id     string // <collection>/<component>/<UID>
	digest string
	etag   string
}

// storeObjects fetches every VTODO/VEVENT under each calendar, stores its
// verbatim body as a caldav-object-v1 leaf, and returns one
// capturedObject per resource keyed by native identity. Diff and capture
// MUST derive identity the same way, so the alias construction lives in
// objectIdentity (shared).
func storeObjects(
	ctx context.Context,
	w capture_plugin.Writer,
	c *client,
	origin string,
	calendars []calendar,
	r cutting_garden_plugins.Reporter,
) ([]capturedObject, error) {
	var objects []capturedObject

	for _, cal := range calendars {
		label := calendarLabel(cal)
		collection := collectionKey(cal.href)
		r.PhaseStart("capture " + label)

		for _, component := range capturedComponents {
			resources, err := c.listResources(ctx, cal.href, component)
			if err != nil {
				r.PhaseEnd(capture_events.Verdict{
					OK:         false,
					Diagnostic: map[string]any{"error": err.Error()},
				})
				return nil, errors.Wrapf(err, "caldav plugin: list %s in %s", component, label)
			}

			for _, res := range resources {
				uid, err := uidOf(component, res.data)
				if err != nil {
					return nil, errors.Wrapf(err, "caldav plugin: identity for %s", res.href)
				}

				digest, _, err := w.WriteBlob(ctx, strings.NewReader(res.data))
				if err != nil {
					return nil, errors.Wrapf(err, "caldav plugin: store %s", res.href)
				}

				objects = append(objects, capturedObject{
					id:     objectIdentity(collection, component, uid),
					digest: digest,
					etag:   res.etag,
				})
				r.Progress(cutting_garden_plugins.ReportProgress{
					Item:  label,
					Items: int64(len(objects)),
				})
			}
		}
		r.PhaseEnd(capture_events.Verdict{OK: true})
	}

	return objects, nil
}

// writeCaldavReceipt assembles the caldav payload node (a JCS metadata
// body plus one object reference per resource) and the RFC 0011 receipt
// tree, returning the receipt's markl id. Objects are sorted by native
// identity so equivalent captures yield a byte-identical payload node
// (and identical payload digest) regardless of server enumeration order.
func writeCaldavReceipt(
	ctx context.Context,
	w capture_plugin.Writer,
	endpoint, version string,
	objects []capturedObject,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	sort.Slice(objects, func(i, j int) bool { return objects[i].id < objects[j].id })

	refs := make([]capture_plugin.Ref, 0, len(objects))
	resourceRecords := make([]map[string]any, 0, len(objects))
	for _, o := range objects {
		refs = append(refs, capture_plugin.LockedRef(o.id, o.digest, typeObject))
		resourceRecords = append(resourceRecords, map[string]any{
			"id":   o.id,
			"etag": o.etag,
		})
	}

	payloadBody, err := capture_plugin.JCS(map[string]any{
		"endpoint":     endpoint,
		"object_count": len(objects),
		"resources":    resourceRecords,
	})
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}
	payloadDigest, _, err := w.WriteBlob(ctx,
		readerOf(capture_plugin.BuildNode(payloadType, refs, payloadBody)))
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, errors.Wrap(err)
	}

	receiptDigest, err := capture_plugin.WriteReceipt(ctx, w, capture_plugin.ReceiptParams{
		Kind: captureKind,
		Invocation: capture_plugin.Invocation{
			Target:    endpoint,
			Format:    captureFormat,
			Normalize: false,
			Options:   map[string]any{"components": componentList()},
		},
		Host: capture_plugin.GatherHost(),
		Binary: capture_plugin.BinaryInfo{
			Name:    "cutting-garden",
			Version: version,
		},
		PluginEnv: capture_plugin.PluginEnv{
			TypeString: pluginEnvType,
			Body:       map[string]any{"components": componentList()},
		},
		PayloadRefs: []capture_plugin.Ref{
			capture_plugin.LockedRef("payload", payloadDigest, payloadType),
		},
	})
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	return cutting_garden_plugins.ProtocolCaptureResult{
		ReceiptDigest: receiptDigest,
		ObjectCount:   len(objects),
	}, nil
}

// objectIdentity builds the native-identity alias an object is referenced
// by: <collection>/<component>/<UID> (RFC 0011). It is the receipt's sort
// key and the restore addressing handle, and deliberately excludes the
// server's path layout so a capture restores to a different host cleanly.
func objectIdentity(collection, component, uid string) string {
	return collection + "/" + component + "/" + uid
}

// collectionKey is the stable collection label for the native identity:
// the calendar href's last non-empty path segment (e.g.
// dav/u/calendars/work/ → "work"). Host-independent, so it round-trips
// across a capture→restore to a different server.
func collectionKey(calendarHref string) string {
	trimmed := strings.TrimRight(serverPath(calendarHref), "/")
	if base := path.Base(trimmed); base != "" && base != "." && base != "/" {
		return base
	}
	return trimmed
}

// uidOf parses the resource body for its iCalendar UID, dispatching on the
// component type. The body is parsed for identity only; the stored leaf is
// the verbatim bytes (RFC 0011).
func uidOf(component, data string) (string, error) {
	switch component {
	case "VTODO":
		t, err := ical.ParseVTODO(data)
		if err != nil {
			return "", err
		}
		return t.UID, nil
	case "VEVENT":
		e, err := ical.ParseVEVENT(data)
		if err != nil {
			return "", err
		}
		return e.UID, nil
	default:
		return "", errors.ErrorWithStackf("caldav plugin: unsupported component %q", component)
	}
}

// componentList returns the captured component set as a sorted slice for
// the identity-affecting invocation/plugin-env bodies. Sorted so the body
// is byte-stable regardless of capturedComponents' declaration order.
func componentList() []any {
	cs := make([]string, len(capturedComponents))
	copy(cs, capturedComponents)
	sort.Strings(cs)
	out := make([]any, len(cs))
	for i, c := range cs {
		out[i] = c
	}
	return out
}

// readerOf wraps node bytes in an io.Reader for the Writer.
func readerOf(b []byte) io.Reader { return strings.NewReader(string(b)) }

// compile-time assertion: the caldav plugin emits an RFC 0011 protocol
// receipt tree.
var _ cutting_garden_plugins.ProtocolCapturePlugin = (*Plugin)(nil)

package caldav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// requestTimeout caps every CalDAV HTTP round-trip. The command's
// cancelable context still aborts in-flight requests earlier on
// SIGINT/SIGTERM; this is the upper bound for an unresponsive server.
const requestTimeout = 30 * time.Second

// client is a minimal CalDAV HTTP client: PROPFIND to discover calendar
// collections, REPORT to list a calendar's raw iCalendar resources, and
// PUT to materialize a resource back. It deliberately carries no
// iCalendar parser — capture/restore/diff treat each resource as an
// opaque text/calendar blob, which is exactly the content-addressed
// shape the receipt machinery wants.
type client struct {
	base     string // base collection URL, e.g. https://host/dav/
	username string
	password string
	http     *http.Client
}

func newClient(base, username, password string) *client {
	return &client{
		base:     base,
		username: username,
		password: password,
		http:     &http.Client{Timeout: requestTimeout},
	}
}

// do issues one CalDAV request. ctx is honored so a cancel unwinds the
// in-flight request promptly. depth < 0 omits the Depth header (used by
// PUT/GET); body == "" sends no request body and no XML content-type.
func (c *client) do(
	ctx context.Context,
	method, url, body, contentType string,
	depth int,
) (*http.Response, error) {
	return c.doWithHeaders(ctx, method, url, body, contentType, depth, nil)
}

// doWithHeaders is do plus a set of extra request headers, for the WebDAV
// conditional PUTs (If-None-Match / If-Match) that enforce strict
// create-vs-update semantics. do delegates here with no extras, so existing
// callers are unchanged.
func (c *client) doWithHeaders(
	ctx context.Context,
	method, url, body, contentType string,
	depth int,
	extra map[string]string,
) (*http.Response, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if depth >= 0 {
		req.Header.Set("Depth", fmt.Sprintf("%d", depth))
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return resp, nil
}

// --- XML types for CalDAV multistatus responses ---

type multistatusResponse struct {
	XMLName   xml.Name      `xml:"DAV: multistatus"`
	Responses []davResponse `xml:"DAV: response"`
}

type davResponse struct {
	Href     string        `xml:"DAV: href"`
	PropStat []davPropStat `xml:"DAV: propstat"`
}

type davPropStat struct {
	Prop   davProp `xml:"DAV: prop"`
	Status string  `xml:"DAV: status"`
}

type davProp struct {
	DisplayName  string           `xml:"DAV: displayname"`
	CalendarData string           `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
	Etag         string           `xml:"DAV: getetag"`
	Ctag         string           `xml:"http://calendarserver.org/ns/ getctag"`
	ResourceType *davResourceType `xml:"DAV: resourcetype"`
}

type davResourceType struct {
	Calendar *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
}

// calendar is a discovered CalDAV calendar collection. ctag is the
// calendarserver-namespace collection change token (empty when the server
// does not advertise one); it backs FacetVersion (RFC 0012 §11).
type calendar struct {
	href        string
	displayName string
	ctag        string
}

// resource is one raw iCalendar object (a VTODO or VEVENT resource):
// data is the verbatim text/calendar body, keyed by its server href.
// etag is the server's getetag value (empty when the server omitted it);
// the RFC 0011 protocol capture records it as the per-resource freshness
// signal, while the flat fs-v1 path ignores it.
type resource struct {
	href string
	data string
	etag string
}

// propfindCalendars also requests the calendarserver-namespace getctag —
// the collection change token FacetVersion (RFC 0012 §11) rides on. A
// server that does not support it returns the prop in a 404 propstat (or
// omits it); discovery is unaffected and the token path degrades to
// ok=false.
const propfindCalendars = `<?xml version="1.0" encoding="utf-8" ?>
<d:propfind xmlns:d="DAV:" xmlns:cs="http://calendarserver.org/ns/">
  <d:prop>
    <d:displayname />
    <d:resourcetype />
    <cs:getctag />
  </d:prop>
</d:propfind>`

// propfindResponses issues a Depth:depth PROPFIND from the base URL and
// returns the raw multistatus responses — the collection itself and its
// children — so callers can inspect resourcetypes. Shared by calendar
// discovery for capture, diff, and traversal.
func (c *client) propfindResponses(
	ctx context.Context,
	depth int,
) (responses []davResponse, err error) {
	resp, err := c.do(ctx, "PROPFIND", c.base, propfindCalendars,
		"application/xml; charset=utf-8", depth)
	if err != nil {
		return nil, errors.Wrapf(err, "PROPFIND %s", c.base)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, errors.ErrorWithStackf(
			"PROPFIND %s: status %d: %s",
			c.base, resp.StatusCode, snippet(data),
		)
	}

	var ms multistatusResponse
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, errors.Wrapf(err, "parse multistatus from %s", c.base)
	}
	return ms.Responses, nil
}

// calendarFromResponse returns the calendar a multistatus response
// describes and true when the response is a 200 calendar collection;
// false otherwise.
func calendarFromResponse(r davResponse) (calendar, bool) {
	for _, ps := range r.PropStat {
		if !strings.Contains(ps.Status, "200") {
			continue
		}
		if ps.Prop.ResourceType == nil || ps.Prop.ResourceType.Calendar == nil {
			continue
		}
		return calendar{
			href:        r.Href,
			displayName: ps.Prop.DisplayName,
			ctag:        ps.Prop.Ctag,
		}, true
	}
	return calendar{}, false
}

// discoverCalendars classifies the base URL via a Depth:1 PROPFIND. When
// the base is itself a calendar collection it returns selfIsCalendar
// true and that single calendar; otherwise it returns the child
// calendars under the base (the calendar-home case). This is the one
// traversal source shared by CaptureRoot, ScanForDiff, and ListRoots, so
// discovery and capture cannot disagree about the tree.
func (c *client) discoverCalendars(
	ctx context.Context,
) (selfIsCalendar bool, calendars []calendar, err error) {
	responses, err := c.propfindResponses(ctx, 1)
	if err != nil {
		return false, nil, err
	}

	selfKey := strings.TrimRight(serverPath(c.base), "/")
	for _, r := range responses {
		cal, ok := calendarFromResponse(r)
		if !ok {
			continue
		}
		if strings.TrimRight(serverPath(c.resolveHref(r.Href)), "/") == selfKey {
			return true, []calendar{cal}, nil
		}
		calendars = append(calendars, cal)
	}
	return false, calendars, nil
}

// calendarQuery is the REPORT body template that fetches every resource
// of one component type (VTODO or VEVENT) with its raw calendar-data.
// %s is the component name.
const calendarQuery = `<?xml version="1.0" encoding="utf-8" ?>
<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop>
    <d:getetag />
    <c:calendar-data />
  </d:prop>
  <c:filter>
    <c:comp-filter name="VCALENDAR">
      <c:comp-filter name="%s" />
    </c:comp-filter>
  </c:filter>
</c:calendar-query>`

// listResources performs a Depth:1 REPORT calendar-query against one
// calendar collection and returns every resource of the given component
// type ("VTODO" or "VEVENT") with its raw calendar-data preserved.
func (c *client) listResources(
	ctx context.Context,
	calendarHref, component string,
) (resources []resource, err error) {
	url := c.resolveHref(calendarHref)
	body := fmt.Sprintf(calendarQuery, component)
	resp, err := c.do(ctx, "REPORT", url, body,
		"application/xml; charset=utf-8", 1)
	if err != nil {
		return nil, errors.Wrapf(err, "REPORT %s (%s)", url, component)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, errors.ErrorWithStackf(
			"REPORT %s (%s): status %d: %s",
			url, component, resp.StatusCode, snippet(data),
		)
	}

	var ms multistatusResponse
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, errors.Wrapf(err, "parse multistatus from %s", url)
	}

	for _, r := range ms.Responses {
		for _, ps := range r.PropStat {
			if !strings.Contains(ps.Status, "200") || ps.Prop.CalendarData == "" {
				continue
			}
			resources = append(resources, resource{
				href: r.Href,
				data: ps.Prop.CalendarData,
				etag: ps.Prop.Etag,
			})
		}
	}
	return resources, nil
}

// calendarHrefQuery is the lightweight REPORT body template: it requests
// only getetag (no calendar-data), so enumerating a calendar's members
// does not transfer their bodies. %s is the component name.
const calendarHrefQuery = `<?xml version="1.0" encoding="utf-8" ?>
<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop>
    <d:getetag />
  </d:prop>
  <c:filter>
    <c:comp-filter name="VCALENDAR">
      <c:comp-filter name="%s" />
    </c:comp-filter>
  </c:filter>
</c:calendar-query>`

// calendarEtagUIDQuery is the diff freshness probe: getetag plus a
// calendar-data PROJECTION limited to the UID property (RFC 4791 §9.6
// allows restricting calendar-data to named comp/prop, so the body is not
// transferred — only the UID crosses the wire). The first %s is the
// component name (the comp-filter), the second %s is the same component
// (the calendar-data projection's inner <c:comp>). This yields {href,
// etag, uid} so diff can correlate by host-independent native identity.
//
// A server MAY ignore the projection and return more (or omit
// calendar-data); callers treat a missing UID as "fall back to a full
// fetch", so correctness does not depend on the projection being honored.
const calendarEtagUIDQuery = `<?xml version="1.0" encoding="utf-8" ?>
<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop>
    <d:getetag />
    <c:calendar-data>
      <c:comp name="VCALENDAR">
        <c:comp name="%s">
          <c:prop name="UID" />
        </c:comp>
      </c:comp>
    </c:calendar-data>
  </d:prop>
  <c:filter>
    <c:comp-filter name="VCALENDAR">
      <c:comp-filter name="%s" />
    </c:comp-filter>
  </c:filter>
</c:calendar-query>`

// listObjectHrefs issues a Depth:1 REPORT for one component type that
// fetches only getetag, and returns the member resource hrefs without
// their bodies. Traversal (ListRoots) uses it so enumerating a
// calendar's objects stays cheap; capture uses listResources, which
// carries the bodies it needs.
func (c *client) listObjectHrefs(
	ctx context.Context,
	calendarHref, component string,
) (hrefs []string, err error) {
	url := c.resolveHref(calendarHref)
	body := fmt.Sprintf(calendarHrefQuery, component)
	resp, err := c.do(ctx, "REPORT", url, body,
		"application/xml; charset=utf-8", 1)
	if err != nil {
		return nil, errors.Wrapf(err, "REPORT %s (%s)", url, component)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, errors.ErrorWithStackf(
			"REPORT %s (%s): status %d: %s",
			url, component, resp.StatusCode, snippet(data),
		)
	}

	var ms multistatusResponse
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, errors.Wrapf(err, "parse multistatus from %s", url)
	}

	// A calendar-query REPORT returns only member objects, never the
	// collection itself, so every 200 response is one object.
	for _, r := range ms.Responses {
		for _, ps := range r.PropStat {
			if strings.Contains(ps.Status, "200") {
				hrefs = append(hrefs, r.Href)
				break
			}
		}
	}
	return hrefs, nil
}

// probedObject is one resource as seen by the diff freshness probe: its
// server href, current etag, and — when the server honored the UID
// projection (RFC 4791 §9.6) — its iCalendar UID. uid is empty when the
// server ignored the projection or returned no calendar-data; the caller
// then falls back to a full body fetch to learn the UID. No full body is
// transferred either way.
type probedObject struct {
	href string
	etag string
	uid  string
}

// listObjectEtags is the diff freshness probe: a REPORT requesting getetag
// plus a calendar-data projection limited to the UID property, so each
// resource's {href, etag, uid} is learned without transferring its body.
// The native identity (host-independent) is derivable from uid+component,
// so diff correlates resources across hosts — not by the server-specific
// href. uid is best-effort: a server that ignores the projection yields an
// empty uid and the caller re-fetches that one body.
func (c *client) listObjectEtags(
	ctx context.Context,
	calendarHref, component string,
) (objects []probedObject, err error) {
	url := c.resolveHref(calendarHref)
	body := fmt.Sprintf(calendarEtagUIDQuery, component, component)
	resp, err := c.do(ctx, "REPORT", url, body,
		"application/xml; charset=utf-8", 1)
	if err != nil {
		return nil, errors.Wrapf(err, "REPORT %s (%s)", url, component)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, errors.ErrorWithStackf(
			"REPORT %s (%s): status %d: %s",
			url, component, resp.StatusCode, snippet(data),
		)
	}

	var ms multistatusResponse
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, errors.Wrapf(err, "parse multistatus from %s", url)
	}

	for _, r := range ms.Responses {
		for _, ps := range r.PropStat {
			if !strings.Contains(ps.Status, "200") {
				continue
			}
			// The projected calendar-data (if the server honored it) holds
			// just the UID; parse it best-effort. An empty/ignored
			// projection leaves uid empty for the caller to resolve.
			uid := uidFromProjection(component, ps.Prop.CalendarData)
			objects = append(objects, probedObject{href: r.Href, etag: ps.Prop.Etag, uid: uid})
			break
		}
	}
	return objects, nil
}

// uidFromProjection parses a UID-projected calendar-data fragment for its
// UID, returning "" on any parse failure (the caller then falls back to a
// full fetch). It tolerates a server returning more than the projection
// asked for, since the parser reads only the UID.
func uidFromProjection(component, calendarData string) string {
	if calendarData == "" {
		return ""
	}
	uid, err := uidOf(component, calendarData)
	if err != nil {
		return ""
	}
	return uid
}

// putResourceCond PUTs icalData at href. extra carries an optional WebDAV
// precondition header (If-None-Match / If-Match). On 412 Precondition Failed
// it returns precondErr (the strict create/update message); any other non-2xx
// is a generic error. The shared core of putResource/createResource/
// updateResource so the status handling lives once.
func (c *client) putResourceCond(
	ctx context.Context,
	href, icalData string,
	extra map[string]string,
	precondErr error,
) (err error) {
	url := c.resolveHref(href)
	resp, err := c.doWithHeaders(ctx, "PUT", url, icalData,
		"text/calendar; charset=utf-8", -1, extra)
	if err != nil {
		return errors.Wrapf(err, "PUT %s", url)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	if precondErr != nil && resp.StatusCode == http.StatusPreconditionFailed {
		return precondErr
	}
	if resp.StatusCode != http.StatusCreated &&
		resp.StatusCode != http.StatusNoContent &&
		resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return errors.ErrorWithStackf(
			"PUT %s: status %d: %s", url, resp.StatusCode, snippet(body),
		)
	}
	return nil
}

// putResource creates or overwrites the resource at href with icalData.
// No conditional header is sent: restore is an unconditional
// materialization of the captured bytes onto the destination.
func (c *client) putResource(ctx context.Context, href, icalData string) error {
	return c.putResourceCond(ctx, href, icalData, nil, nil)
}

// createResource strictly creates the resource at href: an If-None-Match: *
// precondition makes the server reject (412) an already-present resource, so
// create is not an upsert (NodeMutator.CreateNode semantics). A 412 surfaces
// as an "already exists" error.
func (c *client) createResource(ctx context.Context, href, icalData string) error {
	return c.putResourceCond(
		ctx, href, icalData,
		map[string]string{"If-None-Match": "*"},
		errors.BadRequestf(
			"caldav plugin: object already exists at %s "+
				"(create is strict; use update to overwrite)", href,
		),
	)
}

// updateResource strictly overwrites an existing resource at href: an
// If-Match: * precondition makes the server reject (412) a missing resource,
// so update is not a create (NodeMutator.UpdateNode semantics). A 412
// surfaces as a "does not exist" error.
func (c *client) updateResource(ctx context.Context, href, icalData string) error {
	return c.putResourceCond(
		ctx, href, icalData,
		map[string]string{"If-Match": "*"},
		errors.BadRequestf(
			"caldav plugin: no object to update at %s "+
				"(use create to make a new object)", href,
		),
	)
}

// deleteResource removes the resource at href (NodeMutator.DeleteNode). A
// 404 is reported as an error: deleting a nonexistent node is a failure the
// caller surfaces, matching the strict create/update posture.
func (c *client) deleteResource(ctx context.Context, href string) (err error) {
	url := c.resolveHref(href)
	resp, err := c.do(ctx, "DELETE", url, "", "", -1)
	if err != nil {
		return errors.Wrapf(err, "DELETE %s", url)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusNoContent &&
		resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return errors.ErrorWithStackf(
			"DELETE %s: status %d: %s", url, resp.StatusCode, snippet(body),
		)
	}
	return nil
}

// getResource fetches a single resource's verbatim text/calendar body by
// href. DiffProtocol uses it to re-fetch only the resources whose etag
// moved (or is unknown), so unchanged resources cost no body transfer.
func (c *client) getResource(ctx context.Context, href string) (body string, err error) {
	url := c.resolveHref(href)
	resp, err := c.do(ctx, "GET", url, "", "", -1)
	if err != nil {
		return "", errors.Wrapf(err, "GET %s", url)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", errors.ErrorWithStackf(
			"GET %s: status %d: %s", url, resp.StatusCode, snippet(data),
		)
	}
	return string(data), nil
}

// resolveHref turns a possibly-relative href (as returned in a
// multistatus response) into an absolute URL against the client's base.
// Absolute URLs pass through; root-relative paths ("/dav/...") bind to
// the base's origin; bare relative paths join under the base.
func (c *client) resolveHref(href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	base := strings.TrimRight(c.base, "/")
	if strings.HasPrefix(href, "/") {
		if origin, ok := originOf(base); ok {
			return origin + href
		}
		return base + href
	}
	return base + "/" + href
}

// snippet trims an error-body excerpt so diagnostics stay readable.
func snippet(b []byte) string {
	const max = 256
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

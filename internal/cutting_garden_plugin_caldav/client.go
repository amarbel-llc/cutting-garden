package cutting_garden_plugin_caldav

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
	DisplayName      string           `xml:"DAV: displayname"`
	SupportedCalComp *calCompSet      `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set"`
	CalendarData     string           `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
	GetETag          string           `xml:"DAV: getetag"`
	ResourceType     *davResourceType `xml:"DAV: resourcetype"`
}

type davResourceType struct {
	Calendar *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
}

type calCompSet struct {
	Comps []calComp `xml:"urn:ietf:params:xml:ns:caldav comp"`
}

type calComp struct {
	Name string `xml:"name,attr"`
}

// calendar is a discovered CalDAV calendar collection.
type calendar struct {
	href           string
	displayName    string
	componentTypes []string
}

// resource is one raw iCalendar object (a VTODO or VEVENT resource)
// with its href and ETag. data is the verbatim text/calendar body.
type resource struct {
	href string
	etag string
	data string
}

const propfindCalendars = `<?xml version="1.0" encoding="utf-8" ?>
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop>
    <d:displayname />
    <d:resourcetype />
    <c:supported-calendar-component-set />
  </d:prop>
</d:propfind>`

// listCalendars performs a Depth:1 PROPFIND from the base URL and
// returns every child collection whose resourcetype includes
// <C:calendar>.
func (c *client) listCalendars(ctx context.Context) ([]calendar, error) {
	resp, err := c.do(ctx, "PROPFIND", c.base, propfindCalendars,
		"application/xml; charset=utf-8", 1)
	if err != nil {
		return nil, errors.Wrapf(err, "PROPFIND %s", c.base)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, errors.ErrorWithStackf(
			"PROPFIND %s: status %d: %s",
			c.base, resp.StatusCode, snippet(data))
	}

	var ms multistatusResponse
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, errors.Wrapf(err, "parse multistatus from %s", c.base)
	}

	var calendars []calendar
	for _, r := range ms.Responses {
		for _, ps := range r.PropStat {
			if !strings.Contains(ps.Status, "200") {
				continue
			}
			if ps.Prop.ResourceType == nil || ps.Prop.ResourceType.Calendar == nil {
				continue
			}
			cal := calendar{
				href:        r.Href,
				displayName: ps.Prop.DisplayName,
			}
			if ps.Prop.SupportedCalComp != nil {
				for _, comp := range ps.Prop.SupportedCalComp.Comps {
					cal.componentTypes = append(cal.componentTypes, comp.Name)
				}
			}
			calendars = append(calendars, cal)
		}
	}
	return calendars, nil
}

// calendarQuery is the REPORT body template that fetches every resource
// of one component type (VTODO or VEVENT) with its ETag and raw
// calendar-data. %s is the component name.
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
) ([]resource, error) {
	url := c.resolveHref(calendarHref)
	body := fmt.Sprintf(calendarQuery, component)
	resp, err := c.do(ctx, "REPORT", url, body,
		"application/xml; charset=utf-8", 1)
	if err != nil {
		return nil, errors.Wrapf(err, "REPORT %s (%s)", url, component)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, errors.ErrorWithStackf(
			"REPORT %s (%s): status %d: %s",
			url, component, resp.StatusCode, snippet(data))
	}

	var ms multistatusResponse
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, errors.Wrapf(err, "parse multistatus from %s", url)
	}

	var resources []resource
	for _, r := range ms.Responses {
		for _, ps := range r.PropStat {
			if !strings.Contains(ps.Status, "200") || ps.Prop.CalendarData == "" {
				continue
			}
			resources = append(resources, resource{
				href: r.Href,
				etag: ps.Prop.GetETag,
				data: ps.Prop.CalendarData,
			})
		}
	}
	return resources, nil
}

// putResource creates or overwrites the resource at href with icalData.
// No conditional header is sent: restore is an unconditional
// materialization of the captured bytes onto the destination.
func (c *client) putResource(ctx context.Context, href, icalData string) error {
	url := c.resolveHref(href)
	resp, err := c.do(ctx, "PUT", url, icalData,
		"text/calendar; charset=utf-8", -1)
	if err != nil {
		return errors.Wrapf(err, "PUT %s", url)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated &&
		resp.StatusCode != http.StatusNoContent &&
		resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return errors.ErrorWithStackf(
			"PUT %s: status %d: %s", url, resp.StatusCode, snippet(body))
	}
	return nil
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

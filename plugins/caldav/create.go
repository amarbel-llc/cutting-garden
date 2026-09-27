package caldav

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/caldav/ical"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Object creation from an organize document (forge organize F10): caldav
// declares VTODO and VEVENT creatable under a calendar, builds the new
// object's iCalendar from the merged document fields (BuildCreateBody — the
// date+time split, the default TZID, the priority band and the STATUS case
// fold are caldav's own codecs, so the framework never sees them), and creates
// it under the calendar with a minted UID at `<calendar>/<uid>.ics`
// (CreateChild). VJOURNAL is not creatable through organize yet.
var (
	_ cutting_garden_plugins.CreationDescriber = (*Plugin)(nil)
	_ cutting_garden_plugins.CreateApplier     = (*Plugin)(nil)
	_ cutting_garden_plugins.ContainerCreator  = (*Plugin)(nil)
)

// DescribeCreation declares the creatable object types: a task needs only its
// summary (organize requires every new object's description anyway), an event
// a start date.
func (Plugin) DescribeCreation() []cutting_garden_plugins.NodeTypeCreation {
	return []cutting_garden_plugins.NodeTypeCreation{
		{Tag: typeVTODO, ContainerType: typeCalendar, Required: []string{listingFieldSummary}},
		{Tag: typeVEVENT, ContainerType: typeCalendar, Required: []string{facetDateStart}},
	}
}

// createDateField maps a date_<suffix> / time_<suffix> atom pair to the stored
// property it builds, per component.
type createDateField struct {
	suffix string
	set    func(value, tzid string)
}

// BuildCreateBody builds a new object's iCalendar from its merged document
// fields: summary (the trailer), status (case-folded up to RFC 5545), location,
// categories (the tag set), priority (a band completes to its canonical
// integer; a raw 0–9 writes verbatim — VTODO only), and date_start/time_start
// (DTSTART) plus, for a task, date_due/time_due (DUE). A date alone is an
// all-day VALUE=DATE; a date with a time is a DATE-TIME in the host's IANA
// zone (TZID) when one is known, else floating local time. A task with no
// status defaults to NEEDS-ACTION. The UID is minted here. A field caldav
// cannot set on create (a read-only facet such as due_band, an end date, a
// time without its date) is a bad request.
func (Plugin) BuildCreateBody(
	_ context.Context, typ string, fields map[string][]string,
) ([]byte, error) {
	uid, err := mintUID()
	if err != nil {
		return nil, err
	}

	switch typ {
	case typeVTODO:
		task := &ical.Task{UID: uid, Status: "NEEDS-ACTION"}
		dates := []createDateField{
			{"start", func(v, z string) { task.DtStart, task.DtStartTZID = v, z }},
			{"due", func(v, z string) { task.Due, task.DueTZID = v, z }},
		}
		plain := map[string]func([]string) error{
			listingFieldSummary:  func(v []string) error { task.Summary = v[0]; return nil },
			listingFieldStatus:   func(v []string) error { task.Status = strings.ToUpper(v[0]); return nil },
			listingFieldLocation: func(v []string) error { task.Location = v[0]; return nil },
			facetCategories:      func(v []string) error { task.Categories = v; return nil },
			listingFieldPriority: func(v []string) error {
				p, err := createPriority(v[0])
				task.Priority = p
				return err
			},
		}
		if err := applyCreateFields(typ, fields, plain, dates); err != nil {
			return nil, err
		}
		return []byte(ical.TaskToIcal(task)), nil

	case typeVEVENT:
		event := &ical.Event{UID: uid}
		dates := []createDateField{
			{"start", func(v, z string) { event.DtStart, event.DtStartTZID = v, z }},
		}
		plain := map[string]func([]string) error{
			listingFieldSummary:  func(v []string) error { event.Summary = v[0]; return nil },
			listingFieldStatus:   func(v []string) error { event.Status = strings.ToUpper(v[0]); return nil },
			listingFieldLocation: func(v []string) error { event.Location = v[0]; return nil },
			facetCategories:      func(v []string) error { event.Categories = v; return nil },
		}
		if err := applyCreateFields(typ, fields, plain, dates); err != nil {
			return nil, err
		}
		return []byte(ical.EventToIcal(event)), nil
	}

	return nil, errors.BadRequestf(
		"caldav plugin: organize cannot create a %q (creatable: %s, %s)",
		typ, typeVTODO, typeVEVENT,
	)
}

// applyCreateFields routes every document field to its setter — a plain
// field, or one half of a date/time pair — refusing any field the component
// cannot take on create.
func applyCreateFields(
	typ string,
	fields map[string][]string,
	plain map[string]func([]string) error,
	dates []createDateField,
) error {
	consumed := map[string]bool{}
	for _, d := range dates {
		dateKey, timeKey := "date_"+d.suffix, "time_"+d.suffix
		consumed[dateKey], consumed[timeKey] = true, true
		value, tzid, err := createDateTime(first(fields[dateKey]), first(fields[timeKey]), dateKey, timeKey)
		if err != nil {
			return err
		}
		if value != "" {
			d.set(value, tzid)
		}
	}

	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if consumed[name] {
			continue
		}
		set, ok := plain[name]
		if !ok {
			return errors.BadRequestf(
				"caldav plugin: a new %s cannot be given %q", typ, name,
			)
		}
		values := fields[name]
		if len(values) == 0 {
			continue
		}
		if err := set(values); err != nil {
			return err
		}
	}
	return nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// createDateTime builds a new object's DATE or DATE-TIME property value from
// its split atoms: a `YYYY-MM-DD` (or compact `YYYYMMDD`) date, and an
// optional `HH-mm` clock. A clock needs a date; a date alone is all-day.
func createDateTime(date, clock, dateKey, timeKey string) (value, tzid string, err error) {
	if date == "" {
		if clock != "" {
			return "", "", errors.BadRequestf(
				"caldav plugin: %s=%s needs a %s", timeKey, clock, dateKey,
			)
		}
		return "", "", nil
	}
	digits := strings.ReplaceAll(date, "-", "")
	if len(digits) != 8 || !allDigits(digits) {
		return "", "", errors.BadRequestf(
			"caldav plugin: %s=%s is not a YYYY-MM-DD date", dateKey, date,
		)
	}
	if _, err := time.Parse("20060102", digits); err != nil {
		return "", "", errors.BadRequestf(
			"caldav plugin: %s=%s is not a calendar date", dateKey, date,
		)
	}
	if clock == "" {
		return digits, "", nil
	}
	hm := strings.ReplaceAll(clock, "-", "")
	if len(hm) != 4 || !allDigits(hm) || hm[:2] > "23" || hm[2:] > "59" {
		return "", "", errors.BadRequestf(
			"caldav plugin: %s=%s is not an HH-mm time", timeKey, clock,
		)
	}
	return digits + "T" + hm + "00", createZone(), nil
}

// createPriority completes a priority band (or a raw RFC 5545 integer) through
// the same codec a field edit uses.
func createPriority(value string) (int, error) {
	updates, err := caldavPriorityCodec{}.Parse(
		map[string][]string{listingFieldPriority: {value}}, nil,
	)
	if err != nil {
		return 0, errors.BadRequestf("caldav plugin: %s", err)
	}
	p, _ := updates[listingFieldPriority].(int)
	return p, nil
}

// createZone is the TZID a new timed object is anchored in (#141): the host's
// IANA zone when it can be named and loaded — $TZ, else the /etc/localtime
// link target — else "" (floating local time, RFC 5545 §3.3.5). A variable so
// tests pin it.
var createZone = hostZoneName

func hostZoneName() string {
	candidates := []string{strings.TrimPrefix(os.Getenv("TZ"), ":")}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, found := strings.Cut(filepath.ToSlash(target), "zoneinfo/"); found {
			candidates = append(candidates, name)
		}
	}
	for _, name := range candidates {
		if name == "" || name == "Local" {
			continue
		}
		if _, err := time.LoadLocation(name); err == nil {
			return name
		}
	}
	return ""
}

// mintUID mints a new object's iCalendar UID: 128 random bits, hex — also the
// resource name (`<uid>.ics`), so it needs no escaping in a URL path. A
// variable so tests pin it.
var mintUID = func() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.Wrap(err)
	}
	return hex.EncodeToString(b[:]), nil
}

// CreateChild creates a new object of type typ under the calendar container
// from body (iCalendar, as BuildCreateBody builds it, or the objectView JSON),
// at `<calendar>/<UID>.ics` — the name caldav's restore uses too — returning
// the new node's URI. The PUT is If-None-Match strict, so an existing object is
// never overwritten.
func (Plugin) CreateChild(
	ctx context.Context, container *url.URL, body io.Reader, typ string,
) (*url.URL, error) {
	if container == nil {
		return nil, errors.ErrorWithStackf("caldav plugin: CreateChild requires a container URI")
	}
	switch typ {
	case typeVTODO, typeVEVENT, typeVJOURNAL:
	default:
		return nil, errors.BadRequestf(
			"caldav plugin: cannot create a %q under a calendar (want %s / %s / %s)",
			typ, typeVTODO, typeVEVENT, typeVJOURNAL,
		)
	}

	icalData, err := normalizeObjectBody(body)
	if err != nil {
		return nil, err
	}
	view, ok := parseObjectView(icalData)
	if !ok {
		return nil, errors.BadRequestf("caldav plugin: create body is not a VTODO, VEVENT or VJOURNAL")
	}
	if got := objectType(view.Component); got != typ {
		return nil, errors.BadRequestf(
			"caldav plugin: create body is a %s, but the type asked for is %q", view.Component, typ,
		)
	}
	uid := uidOfView(view)
	if uid == "" || strings.ContainsAny(uid, "/?#") {
		return nil, errors.BadRequestf("caldav plugin: create body carries no usable UID (%q)", uid)
	}

	c, base, err := clientForNode(container)
	if err != nil {
		return nil, err
	}
	href := strings.TrimSuffix(base, "/") + "/" + url.PathEscape(uid) + ".ics"
	if err := c.createResource(ctx, href, icalData); err != nil {
		return nil, err
	}
	return caldavURIForAbs(href), nil
}

// uidOfView reads the parsed object's UID, whichever component it is.
func uidOfView(view objectView) string {
	switch {
	case view.Task != nil:
		return view.Task.UID
	case view.Event != nil:
		return view.Event.UID
	case view.Journal != nil:
		return view.Journal.UID
	}
	return ""
}

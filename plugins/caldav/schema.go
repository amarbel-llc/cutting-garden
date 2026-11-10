package caldav

import (
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/caldav/ical"
)

var _ cutting_garden_plugins.BodyDescriber = (*Plugin)(nil)

// DescribeBodies describes the create/update payload of each caldav object leaf
// type — the writable caldav node types (the calendar container awaits
// MKCALENDAR, #77, so it is not listed). The accepted formats mirror
// normalizeObjectBody, and each example is the same `objectView` shape
// resources/read returns (#85) for that component, so read-as-JSON → edit →
// write-as-JSON is discoverable from the schema alone.
func (Plugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	accepts := func(kind string) []string {
		return []string{
			"application/json (the {component, event|task|journal} object read_node returns). On create its uid is " +
				"optional: a temp-id create mints one and returns the new URI, an explicit-id create takes the id " +
				"without its .ics as the uid, and a VTODO with no status gets NEEDS-ACTION",
			"patch ops take {component, event|task|journal: {field: value}} or just a bare {field: value} object " +
				"(its component is the node's; the example above is the read shape, not a patch) and change only " +
				"the fields named; null clears a field. Patchable: VTODO summary, description, status, dtstart, dtstart_tzid, " +
				"due, due_tzid, location, priority, categories, parent_uid; VEVENT summary, description, status, " +
				"dtstart, dtstart_tzid, dtend, dtend_tzid, location, categories; VJOURNAL summary, description, " +
				"status, dtstart, categories. A field outside that set is tolerated but not in the applied list",
			"text/calendar (a raw iCalendar " + kind + " with its own UID: line, never filled in for you; escape commas as \\, semicolons as \\; " +
				"and write newlines inside a value as \\n)",
			"delete ops take no body. The result names the deleted object by its summary " +
				"(one extra GET before the DELETE; if that GET fails the delete still goes ahead and " +
				"the name is the filename); an object that is not there is status not_found",
		}
	}
	return []cutting_garden_plugins.NodeTypeBody{
		{
			Tag: typeVTODO,
			Accepts: append(accepts("VTODO"),
				"status facet: a VTODO with no STATUS counts as `none` in read_facets, and a "+
					"status=needs-action filter (read_facets, list_nodes filter) includes it, RFC 5545's "+
					"default; a trellis status predicate does not see it yet (cutting-garden#413)"),
			Example: objectView{
				Component: "VTODO",
				Task: &ical.Task{
					UID:     "example-uid@cutting-garden",
					Summary: "Submit report",
					Due:     "20260815T143000",
					Status:  "NEEDS-ACTION",
				},
			},
		},
		{
			Tag:     typeVEVENT,
			Accepts: accepts("VEVENT"),
			Example: objectView{
				Component: "VEVENT",
				Event: &ical.Event{
					UID:      "example-uid@cutting-garden",
					Summary:  "Standup",
					DtStart:  "20260224T150000Z",
					DtEnd:    "20260224T151500Z",
					Location: "HQ / video link",
					Status:   "CONFIRMED",
				},
			},
		},
		{
			Tag:     typeVJOURNAL,
			Accepts: accepts("VJOURNAL"),
			Example: objectView{
				Component: "VJOURNAL",
				Journal: &ical.Journal{
					UID:     "example-uid@cutting-garden",
					Summary: "Retro notes",
					DtStart: "20260224",
					Status:  "FINAL",
				},
			},
		},
	}
}

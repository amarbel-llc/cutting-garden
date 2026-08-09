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
			"application/json (the {component, event|task|journal} object resources/read returns)",
			"text/calendar (a raw iCalendar " + kind + ")",
		}
	}
	return []cutting_garden_plugins.NodeTypeBody{
		{
			Tag:     typeVTODO,
			Accepts: accepts("VTODO"),
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

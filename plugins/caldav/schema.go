package caldav

import (
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/caldav/ical"
)

var _ cutting_garden_plugins.BodyDescriber = (*Plugin)(nil)

// DescribeBodies describes the create/update payload of the caldav object
// leaf — the only writable caldav node type (the calendar container awaits
// MKCALENDAR, #77, so it is not listed). The accepted formats mirror
// normalizeObjectBody, and the example is the same `objectView` shape
// resources/read returns (#85), so read-as-JSON → edit → write-as-JSON is
// discoverable from the schema alone.
func (Plugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	return []cutting_garden_plugins.NodeTypeBody{
		{
			Tag: typeObject,
			Accepts: []string{
				"application/json (the {component, event|task} object resources/read returns)",
				"text/calendar (a raw iCalendar VEVENT or VTODO)",
			},
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
	}
}

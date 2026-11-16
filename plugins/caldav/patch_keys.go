package caldav

import (
	"net/url"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cutting_garden_plugins.PatchKeysDescriber = (*Plugin)(nil)

// typePatchObject is the declaration tag of a calendar object patch. It is not
// a node type: VTODO, VEVENT and VJOURNAL share one URI shape, so the component
// (and with it the key set) is known only from a wrapped body or the stored
// object, never from the URI. PatchTarget returns this tag for every object URI
// and the declaration below chooses the key set from the body's `component`.
const typePatchObject = "cutting_garden-caldav-object-patch-v1"

// DescribePatchKeys declares the patch keys for the host's validation
// (cutting-garden#404), for the WRAPPED body
// {"component":"VTODO","task":{...}}: the host checks the top level against
// component and the chosen section, and the section's fields against that
// component's patchable keys, so a typo inside `task` is dropped (tolerant) or
// refused before the write (strict).
//
// The BARE form ({"summary":"x"}) takes its key set from the stored object, so
// the host cannot check it: a tolerant call sends it unchanged (the plugin
// reports what it ignored, after reading the object), a strict call refuses it
// and says to use the wrapped form. This is a documented limit of strict for
// caldav (cutting-garden#404).
func (Plugin) DescribePatchKeys() []cutting_garden_plugins.NodeTypePatchKeys {
	return []cutting_garden_plugins.NodeTypePatchKeys{{
		Tag:           typePatchObject,
		Discriminator: "component",
		Sections: []cutting_garden_plugins.PatchSection{
			{Value: "VEVENT", Section: "event", Keys: eventPatchFields},
			{Value: "VTODO", Section: "task", Keys: taskPatchFields},
			{Value: "VJOURNAL", Section: "journal", Keys: journalPatchFields},
		},
	}}
}

// PatchTarget says a patch to a `.ics` object URI targets a calendar object. A
// calendar, an account or a derived recurrence occurrence (which PatchNode
// refuses) is not patchable. It reads the URI alone, with no network call.
func (Plugin) PatchTarget(uri *url.URL) (string, bool) {
	if uri == nil {
		return "", false
	}
	if _, derived := recurrenceIDOf(uri); derived {
		return "", false
	}
	if strings.HasSuffix(uri.Path, ".ics") || strings.HasSuffix(uri.Opaque, ".ics") {
		return typePatchObject, true
	}

	return "", false
}

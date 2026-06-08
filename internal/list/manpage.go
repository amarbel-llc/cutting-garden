package list

import "github.com/amarbel-llc/cutting-garden/internal/command"

var (
	_ command.CommandWithArgs     = (*List)(nil)
	_ command.CommandWithExamples = (*List)(nil)
	_ command.CommandWithSeeAlso  = (*List)(nil)
)

func (*List) GetArgs() []command.ArgGroup {
	return []command.ArgGroup{{
		Args: []command.Arg{
			{
				Name: "uri",
				Description: "scheme-addressed URI whose immediate child nodes " +
					"to list (e.g. `caldav://host/dav/me/`). Its plugin must " +
					"support traversal; the file plugin does not.",
				Required: true,
			},
		},
	}}
}

func (*List) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "List the calendars under a CalDAV endpoint.",
			Command:     "cutting-garden list caldav://dav.host/dav/me/",
		},
		{
			Description: "List one calendar's objects.",
			Command:     "cutting-garden list caldav://dav.host/dav/me/personal/",
		},
		{
			Description: "Machine-readable listing, one object per node.",
			Command:     "cutting-garden list -format json caldav://dav.host/dav/me/ | jq .",
		},
	}
}

func (*List) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-capture(1)",
		"cutting-garden-health(1)",
	}
}

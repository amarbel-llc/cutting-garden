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
					"support traversal. With no URI, the configured and " +
					"intrinsic roots are listed instead \\(em the entry points " +
					"to descend (cutting-garden config.toml, RFC 0007).",
				Required: false,
			},
		},
	}}
}

func (*List) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "List every configured and intrinsic root (the entry " +
				"points to descend).",
			Command: "cutting-garden list",
		},
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

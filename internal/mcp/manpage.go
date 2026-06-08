package mcp

import "github.com/amarbel-llc/cutting-garden/internal/command"

var (
	_ command.CommandWithArgs     = (*MCP)(nil)
	_ command.CommandWithExamples = (*MCP)(nil)
	_ command.CommandWithSeeAlso  = (*MCP)(nil)
)

func (*MCP) GetArgs() []command.ArgGroup {
	return []command.ArgGroup{{
		Args: []command.Arg{
			{
				Name: "uri",
				Description: "one or more traversable plugin endpoint URIs to " +
					"expose as MCP resources (e.g. `caldav://host/dav/me/`). " +
					"Each scheme's plugin must support traversal; the file " +
					"plugin does not.",
				Required: true,
				Variadic: true,
			},
		},
	}}
}

func (*MCP) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "Serve a CalDAV endpoint's calendars as MCP resources.",
			Command:     "cutting-garden mcp caldav://dav.host/dav/me/",
		},
		{
			Description: "Expose several endpoints in one server.",
			Command: "cutting-garden mcp caldav://dav.host/dav/me/ " +
				"caldav://dav.host/dav/team/",
		},
	}
}

func (*MCP) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-list(1)",
		"cutting-garden-capture(1)",
	}
}

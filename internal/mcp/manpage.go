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
				Description: "optional traversable plugin endpoint URIs to " +
					"expose as MCP resources (e.g. `caldav://host/dav/me/`), " +
					"overriding the config. With no URI, every plugin's " +
					"configured and intrinsic roots are surfaced \\(em the " +
					"configured CalDAV accounts and the file plugin's working " +
					"directory (cutting-garden config.toml, RFC 0007).",
				Required: false,
				Variadic: true,
			},
		},
	}}
}

func (*MCP) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "Serve every configured and intrinsic root (the " +
				"common case; reads $XDG_CONFIG_HOME/cutting-garden/config.toml).",
			Command: "cutting-garden mcp",
		},
		{
			Description: "Override the config: serve one explicit endpoint.",
			Command:     "cutting-garden mcp caldav://dav.host/dav/me/",
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

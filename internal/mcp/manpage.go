package mcp

import "code.linenisgreat.com/cutting-garden/internal/command"

var (
	_ command.CommandWithArgs     = (*MCP)(nil)
	_ command.CommandWithEnvVars  = (*MCP)(nil)
	_ command.CommandWithExamples = (*MCP)(nil)
	_ command.CommandWithFiles    = (*MCP)(nil)
	_ command.CommandWithSeeAlso  = (*MCP)(nil)
)

func (*MCP) GetEnvVars() []command.EnvVar {
	return []command.EnvVar{
		{
			Name:        "XDG_CONFIG_HOME",
			Description: "Root of the config file read when no URI is given (see FILES).",
			Default:     "$HOME/.config",
		},
		{
			Name: "STATSD_HOST",
			Description: "When set, mutate_nodes sends best-effort statsd counters " +
				"(calls, and mis-encoded op bodies by kind) over UDP to this host. " +
				"Unset, nothing is sent.",
		},
		{
			Name:        "STATSD_PORT",
			Description: "The statsd UDP port used with STATSD_HOST.",
			Default:     "8125",
		},
		{
			Name: "(an account's password_env)",
			Description: "The variable an account's password_env names in " +
				"config.toml holds that account's credential (cutting-garden-config(5)).",
		},
	}
}

func (*MCP) GetFiles() []command.FilePath {
	return []command.FilePath{
		{
			Path: "$XDG_CONFIG_HOME/cutting-garden/config.toml",
			Description: "Plugin accounts, wire-plugin stanzas and the [mcp] background-refresh " +
				"settings (RFC 0007, cutting-garden-config(5)). A missing file is an empty config.",
		},
	}
}

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
		{
			Description: "Suppress the file plugin's PWD root and scheme " +
				"exposure entirely (e.g. a krone deployment that only wants " +
				"caldav/jira surfaced).",
			Command: "cutting-garden mcp -exclude-scheme=file",
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

package list

import "code.linenisgreat.com/cutting-garden/internal/command"

var (
	_ command.CommandWithArgs     = (*List)(nil)
	_ command.CommandWithEnvVars  = (*List)(nil)
	_ command.CommandWithExamples = (*List)(nil)
	_ command.CommandWithFiles    = (*List)(nil)
	_ command.CommandWithSeeAlso  = (*List)(nil)
)

func (*List) GetEnvVars() []command.EnvVar {
	return []command.EnvVar{
		{
			Name:        "XDG_CONFIG_HOME",
			Description: "Root of the config file read when no URI is given (see FILES).",
			Default:     "$HOME/.config",
		},
		{
			Name: "(an account's password_env)",
			Description: "The variable an account's password_env names in " +
				"config.toml holds that account's credential (cutting-garden-config(5)).",
		},
	}
}

func (*List) GetFiles() []command.FilePath {
	return []command.FilePath{
		{
			Path: "$XDG_CONFIG_HOME/cutting-garden/config.toml",
			Description: "Plugin accounts and wire-plugin stanzas whose roots a URI-less " +
				"list enumerates (RFC 0007, cutting-garden-config(5)). A missing file is an empty config.",
		},
	}
}

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
		{
			Description: "Organize-style object lines \\(em one espalier box " +
				"per node, tags leading, exactly what `organize` would emit " +
				"for the same set.",
			Command: "cutting-garden list -format espalier caldav://dav.host/dav/me/personal/",
		},
	}
}

func (*List) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-capture(1)",
		"cutting-garden-health(1)",
		"cutting-garden-organize(1)",
	}
}

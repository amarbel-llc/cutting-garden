package mutate

import "code.linenisgreat.com/cutting-garden/internal/command"

var (
	_ command.CommandWithArgs     = (*Mutate)(nil)
	_ command.CommandWithEnvVars  = (*Mutate)(nil)
	_ command.CommandWithExamples = (*Mutate)(nil)
	_ command.CommandWithFiles    = (*Mutate)(nil)
	_ command.CommandWithSeeAlso  = (*Mutate)(nil)
)

func (*Mutate) GetEnvVars() []command.EnvVar {
	return []command.EnvVar{
		{
			Name:        "XDG_CONFIG_HOME",
			Description: "Root of the config file read before the document is applied (see FILES).",
			Default:     "$HOME/.config",
		},
		{
			Name: "(an account's password_env)",
			Description: "The variable an account's password_env names in " +
				"config.toml holds that account's credential (cutting-garden-config(5)).",
		},
	}
}

func (*Mutate) GetFiles() []command.FilePath {
	return []command.FilePath{
		{
			Path: "$XDG_CONFIG_HOME/cutting-garden/config.toml",
			Description: "Plugin accounts and wire-plugin stanzas the anchors " +
				"resolve through (RFC 0007, cutting-garden-config(5)). A missing " +
				"file is an empty config.",
		},
	}
}

func (*Mutate) GetArgs() []command.ArgGroup {
	return []command.ArgGroup{{
		Args: []command.Arg{
			{
				Name: "file",
				Description: "JSON document to apply: an object with a `groups` " +
					"array (and an optional `strict` boolean), exactly the " +
					"arguments of the mutate_nodes MCP tool. With no file, or " +
					"`-`, the document is read from standard input.",
				Required: false,
			},
		},
	}}
}

func (*Mutate) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "Patch one CalDAV task from standard input; bodies are " +
				"JSON-encoded strings.",
			Command: `echo '{"groups":[{"anchor":"caldav://host/dav/me/tasks/","ops":[` +
				`{"kind":"patch","id":"call.ics","body":"{\"priority\":1}"}]}]}' | cutting-garden mutate`,
		},
		{
			Description: "Apply a document from a file, refusing any patch key the " +
				"node type does not accept.",
			Command: "cutting-garden mutate -strict changes.json",
		},
		{
			Description: "Mark every unread story read with one sweep.",
			Command: `echo '{"groups":[{"anchor":"newsblur://stories","sweep":{` +
				`"filter":"read=unread","op":{"kind":"patch","body":"{\"read\":true}"}}}]}' | cutting-garden mutate`,
		},
		{
			Description: "Branch on the exit code: 0 applied, 1 nothing applied " +
				"(the result is still printed), 2 trouble, 64 a malformed document.",
			Command: "cutting-garden mutate changes.json > result.json || echo \"exit $?\"",
		},
	}
}

func (*Mutate) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-list(1)",
		"cutting-garden-mcp(1)",
		"cutting-garden-organize(1)",
	}
}

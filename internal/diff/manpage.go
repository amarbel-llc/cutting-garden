package diff

import "code.linenisgreat.com/cutting-garden/internal/command"

var (
	_ command.CommandWithArgs     = (*Diff)(nil)
	_ command.CommandWithEnvVars  = (*Diff)(nil)
	_ command.CommandWithExamples = (*Diff)(nil)
	_ command.CommandWithFiles    = (*Diff)(nil)
	_ command.CommandWithSeeAlso  = (*Diff)(nil)
)

func (*Diff) GetArgs() []command.ArgGroup {
	return []command.ArgGroup{{
		Args: []command.Arg{
			{
				Name:        "receipt-id",
				Description: "markl-id of the capture receipt to compare against.",
				Required:    true,
			},
			{
				Name: "dir",
				Description: "directory to compare. MUST exist and be a directory; " +
					"diff refuses missing or non-directory paths up front (FDR 0002 " +
					"§Destination Preconditions).",
				Required: true,
			},
		},
	}}
}

func (*Diff) GetEnvVars() []command.EnvVar {
	return []command.EnvVar{
		{
			Name: "NO_COLOR",
			Description: "When set (any value), suppresses ANSI SGR coloring of " +
				"per-line markers under -color=auto. -color=always overrides this.",
		},
	}
}

func (*Diff) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "Round-trip diff: capture, restore, then diff should be clean (exit 0).",
			Command:     "rid=$(cutting-garden capture -format json src | jq -r '.id') && cutting-garden restore \"$rid\" out && cutting-garden diff \"$rid\" out",
		},
		{
			Description: "Detect drift after the tree is mutated (exit 1; per-entry M/A/D/T lines on stdout, `diff: N difference(s)` on stderr).",
			Command:     "cutting-garden diff \"$rid\" out",
		},
		{
			Description: "Probe for bitrot / GC'd blobs in the source store (-verify-blobs-exist).",
			Command:     "cutting-garden diff -verify-blobs-exist \"$rid\" out",
		},
		{
			Description: "Override the store-hint when the receipt's home store has rotated.",
			Command:     "cutting-garden diff -store .work \"$rid\" out",
		},
	}
}

func (*Diff) GetFiles() []command.FilePath {
	return []command.FilePath{
		{
			Path:        "$XDG_DATA_HOME/madder/<store>/",
			Description: "default blob-store-id resolution root. Receipt blob is fetched here unless -store overrides.",
		},
	}
}

func (*Diff) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-capture(1)",
		"cutting-garden-restore(1)",
		"diff(1)",
	}
}

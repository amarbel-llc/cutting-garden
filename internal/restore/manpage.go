package restore

import "github.com/amarbel-llc/cutting-garden/internal/command"

var (
	_ command.CommandWithArgs     = (*Restore)(nil)
	_ command.CommandWithExamples = (*Restore)(nil)
	_ command.CommandWithFiles    = (*Restore)(nil)
	_ command.CommandWithSeeAlso  = (*Restore)(nil)
)

func (*Restore) GetArgs() []command.ArgGroup {
	return []command.ArgGroup{{
		Args: []command.Arg{
			{
				Name:        "receipt-id",
				Description: "markl-id of the capture receipt blob to materialize.",
				Required:    true,
			},
			{
				Name: "dest",
				Description: "destination directory. MUST NOT exist; restore creates it. " +
					"Restored entries land under <dest>/<root>/ per the receipt's root " +
					"keys (see FDR 0001 §Behavior).",
				Required: true,
			},
		},
	}}
}

func (*Restore) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "Capture a tree and restore it into a fresh directory.",
			Command:     "rid=$(cutting-garden capture -format json src | jq -r '.id') && cutting-garden restore \"$rid\" out",
		},
		{
			Description: "Override the receipt's store-hint when the local store has been re-configured since capture.",
			Command:     "cutting-garden restore -store .work <receipt-id> out",
		},
	}
}

func (*Restore) GetFiles() []command.FilePath {
	return []command.FilePath{
		{
			Path:        "$XDG_DATA_HOME/madder/<store>/",
			Description: "default blob-store-id resolution root. Receipt and entry blobs are fetched from here unless -store overrides.",
		},
	}
}

func (*Restore) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-capture(1)",
		"cutting-garden-diff(1)",
	}
}

package serve

import "github.com/amarbel-llc/cutting-garden/internal/command"

var (
	_ command.CommandWithEnvVars  = (*Serve)(nil)
	_ command.CommandWithExamples = (*Serve)(nil)
	_ command.CommandWithFiles    = (*Serve)(nil)
	_ command.CommandWithSeeAlso  = (*Serve)(nil)
)

func (*Serve) GetEnvVars() []command.EnvVar {
	return []command.EnvVar{
		{
			Name: "XDG_STATE_HOME",
			Description: "Base directory under whose cutting-garden/ subdir " +
				"captures.log is appended; one NDJSON line per finalized " +
				"receipt (shared with the capture command).",
		},
	}
}

func (*Serve) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "Receive into the default store on the Tailscale address.",
			Command:     "cutting-garden serve",
		},
		{
			Description: "Receive into a named store, requiring a PIN.",
			Command:     "cutting-garden serve -store .work -pin 123456",
		},
		{
			Description: "Bind an explicit host/port instead of auto-detecting Tailscale.",
			Command:     "cutting-garden serve -bind 0.0.0.0 -port 53317",
		},
		{
			Description: "Restore the most recent received transfer (receipt id from captures.log).",
			Command:     "rid=$(tail -n1 \"${XDG_STATE_HOME:-$HOME/.local/state}/cutting-garden/captures.log\" | jq -r .receipt_id) && cutting-garden restore \"$rid\" out",
		},
	}
}

func (*Serve) GetFiles() []command.FilePath {
	return []command.FilePath{
		{
			Path:        "$XDG_STATE_HOME/cutting-garden/captures.log",
			Description: "NDJSON audit trail; one line per finalized receipt.",
		},
		{
			Path:        "$XDG_DATA_HOME/madder/<store>/",
			Description: "destination blob store received files are written into.",
		},
	}
}

func (*Serve) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-capture(1)",
		"cutting-garden-restore(1)",
		"cutting-garden-diff(1)",
	}
}

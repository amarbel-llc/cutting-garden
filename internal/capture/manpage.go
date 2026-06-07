package capture

import "github.com/amarbel-llc/cutting-garden/internal/command"

var (
	_ command.CommandWithArgs     = (*Capture)(nil)
	_ command.CommandWithEnvVars  = (*Capture)(nil)
	_ command.CommandWithExamples = (*Capture)(nil)
	_ command.CommandWithFiles    = (*Capture)(nil)
	_ command.CommandWithSeeAlso  = (*Capture)(nil)
)

func (*Capture) GetArgs() []command.ArgGroup {
	return []command.ArgGroup{{
		Args: []command.Arg{
			{
				Name: "args",
				Description: "interleaved sequence of optional store-ids (starting with " +
					"`.`) and one or more source paths. Bare paths capture into the " +
					"default store; a store-id token retargets every path that follows " +
					"until the next store-id (FDR 0001 §Producer Rules §Group Resolution). " +
					"Plugins accept URL-shaped paths too (e.g. `ytdlp:https://…`).",
				Required: true,
				Variadic: true,
			},
		},
	}}
}

func (*Capture) GetEnvVars() []command.EnvVar {
	return []command.EnvVar{
		{
			Name: "XDG_STATE_HOME",
			Description: "Root for the per-capture audit log " +
				"`$XDG_STATE_HOME/cutting-garden/captures.log` (one NDJSON entry " +
				"per receipt, best-effort — capture succeeds even if the path is unwritable).",
			Default: "$HOME/.local/state",
		},
	}
}

func (*Capture) GetFiles() []command.FilePath {
	return []command.FilePath{
		{
			Path:        "$XDG_STATE_HOME/cutting-garden/captures.log",
			Description: "NDJSON audit log of every successful capture (one entry per receipt). Best-effort write.",
		},
		{
			Path:        "$XDG_DATA_HOME/madder/<store>/",
			Description: "Default blob-store root. Captured entries and receipt blobs land here under their content-addressed paths.",
		},
	}
}

func (*Capture) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "Capture a single tree into the default store (tap-ndjson records when piped).",
			Command:     "cutting-garden capture src",
		},
		{
			Description: "Capture into a non-default store; the leading dot disambiguates store-id from path.",
			Command:     "cutting-garden capture .work src",
		},
		{
			Description: "Multi-root capture across two stores in one invocation.",
			Command:     "cutting-garden capture .default src .work docs",
		},
		{
			Description: "TAP-14 output for CI consumers (phases as test points, entries as subtests).",
			Command:     "cutting-garden capture -format tap src",
		},
		{
			Description: "The pre-unification wire (DEPRECATED; dual-format window only).",
			Command:     "cutting-garden capture -format json-legacy src",
		},
		{
			Description: "Capture a URL-shaped argument via a registered plugin (e.g. yt-dlp).",
			Command:     "cutting-garden capture 'ytdlp:https://www.youtube.com/watch?v=…'",
		},
	}
}

func (*Capture) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-restore(1)",
		"cutting-garden-diff(1)",
		"yt-dlp(1)",
	}
}

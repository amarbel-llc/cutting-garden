package command

type (
	Cmd interface {
		Run(Request)
	}

	Description struct {
		Short, Long string
	}

	CommandWithDescription interface {
		GetDescription() Description
	}

	// CommandHidden marks a subcommand as framework plumbing rather than a
	// user-facing verb. Hidden commands stay fully dispatchable — the shell
	// stubs invoke `complete`, and chrest invokes `__write-blob` — but are
	// filtered out of the usage banner, the generated manpages, and
	// tab-completion candidates. This replaces the prior by-name `complete`
	// special case so a new hidden command (e.g. `__write-blob`) opts in by
	// implementing the interface rather than being added to every filter.
	CommandHidden interface {
		CommandHidden()
	}
)

// isHidden reports whether cmd opts out of user-facing surfaces by
// implementing CommandHidden.
func isHidden(cmd Cmd) bool {
	_, ok := cmd.(CommandHidden)
	return ok
}

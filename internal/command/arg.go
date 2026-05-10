package command

import "github.com/amarbel-llc/purse-first/libs/dewey/0/interfaces"

type (
	// Arg declares metadata for a single positional argument.
	Arg struct {
		Name        string
		Description string
		Required    bool
		Variadic    bool
		EnumValues  []string
		Value       interfaces.FlagValue
	}

	// ArgGroup is a named set of args contributed by a command or component.
	ArgGroup struct {
		Name        string
		Description string
		Args        []Arg
	}

	// CommandWithArgs is the opt-in interface for declarative arg metadata.
	CommandWithArgs interface {
		GetArgs() []ArgGroup
	}

	// MCPAnnotations declares MCP tool hints. Kept for future MCP work;
	// inert in Phase 1.
	MCPAnnotations struct {
		ReadOnly    bool
		Destructive bool
	}

	CommandWithMCPAnnotations interface {
		GetMCPAnnotations() MCPAnnotations
	}
)

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
)

// Request is a stub for now — the full type lands in Task 7
// (port of command_line_input.go + request.go). cmd_test.go compiles
// against this stub, then the stub gets removed in Task 7.
type Request struct{}

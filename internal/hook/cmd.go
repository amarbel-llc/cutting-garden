// Package hook wires the hidden `hook` subcommand: the sink the
// cutting-garden clown plugin's PreToolUse handler
// (plugins/cutting-garden/hooks/handler) execs. It routes stdin/stdout
// through claude_hooks.Run, which decides whether to auto-approve a
// cutting-garden MCP tool call. Hidden plumbing — invoked by clown per
// hook event, never a user-facing verb.
package hook

import (
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/claude_hooks"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Hook is the value registered for the hidden `hook` subcommand. It carries
// no flags: the hook event arrives as JSON on stdin.
type Hook struct{}

var _ command.Cmd = (*Hook)(nil)

// New constructs a Hook command.
func New() *Hook { return &Hook{} }

func (*Hook) GetDescription() command.Description {
	return command.Description{
		Short: "internal: respond to a Claude Code PreToolUse hook event from the clown plugin",
	}
}

// CommandHidden marks `hook` as framework plumbing: the clown plugin's
// handler execs it per hook event, so it must stay dispatchable but never
// appear in the usage banner, manpages, or tab-completion candidates.
func (*Hook) CommandHidden() {}

func (cmd *Hook) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	if err := claude_hooks.Run(os.Stdin, os.Stdout); err != nil {
		errors.ContextCancelWithError(ctx, errors.Wrap(err))
	}
}

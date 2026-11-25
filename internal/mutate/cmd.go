package mutate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/interfaces"
)

// Mutate is the value registered for the `mutate` subcommand: the CLI front
// over the same write engine as the mutate_nodes MCP tool. It reads one
// {"groups":[...]} document (a file, or stdin), applies it, and prints the
// result document.
//
//	mutate [-strict] [-config FILE] [FILE | -]
//
// Exit 0 when the call applied, 1 when nothing was applied (every group
// refused or every op failed — the result is still printed, so a script can
// branch on the code and read why), 2 on trouble (an unreadable file, a
// scheme no plugin serves, a plugin failing outright), 64 on a malformed
// document or a wrong argument count.
type Mutate struct {
	// Strict is the -strict flag, OR-ed with the document's own strict
	// member: a patch naming a key its node type does not accept is refused
	// before anything is written for it (cutting-garden#404).
	Strict bool
	// ConfigPath, the -config flag, loads this file instead of
	// $XDG_CONFIG_HOME/cutting-garden/config.toml (cutting-garden#108).
	ConfigPath string

	stdin  io.Reader
	output io.Writer
	// engine, when set, replaces the config-built engine (tests).
	engine *Engine
}

var (
	_ command.Cmd                       = (*Mutate)(nil)
	_ interfaces.CommandComponentWriter = (*Mutate)(nil)
)

// New constructs a Mutate that reads os.Stdin and prints to os.Stdout.
func New() *Mutate {
	return &Mutate{stdin: os.Stdin, output: os.Stdout}
}

// newForTest is the test-only constructor: scripted stdin, captured output,
// and a registry-free engine.
func newForTest(stdin io.Reader, output io.Writer, engine *Engine) *Mutate {
	return &Mutate{stdin: stdin, output: output, engine: engine}
}

func (*Mutate) GetDescription() command.Description {
	return command.Description{
		Short: "create, replace, patch, or delete nodes through a plugin",
		Long: "Applies one JSON document of groups \\(em the same document the " +
			"mutate_nodes MCP tool takes \\(em and prints the result document. " +
			"Each group is an anchor URI plus either ops (create, put, patch, " +
			"delete, applied in order) or a sweep (one op applied to every node " +
			"under the anchor matching a filter). Groups apply in order and " +
			"independently; a malformed group refuses the whole call before " +
			"anything is written. Ops address nodes by the id `list -format " +
			"espalier` and `organize` print under the anchor. Writes live nodes: " +
			"no blob store is touched and nothing is captured.",
	}
}

func (cmd *Mutate) SetFlagDefinitions(flagSet interfaces.CLIFlagDefinitions) {
	flagSet.BoolVar(
		&cmd.Strict,
		"strict",
		false,
		"refuse a patch naming a key its node type does not accept, or one "+
			"whose plugin declares no keys, before anything is written for it "+
			"(default: drop the undeclared keys and report them as ignored)",
	)
	flagSet.StringVar(
		&cmd.ConfigPath,
		"config",
		"",
		"config file to load instead of $XDG_CONFIG_HOME/cutting-garden/config.toml "+
			"(it must exist)",
	)
}

func (cmd *Mutate) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	// The file is optional: PeekArgs rather than PopArg, which would poison
	// the context with a "missing argument" usage error on the stdin path.
	args := req.PeekArgs()
	if len(args) > 1 {
		errors.ContextCancelWithBadRequestf(ctx,
			"too many positional arguments; mutate takes at most one (<file>), "+
				"trailing: %v", args[1:])
		return
	}

	in, err := cmd.readInput(args)
	if err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}
	in.Strict = in.Strict || cmd.Strict

	engine, err := cmd.engineForRun()
	if err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}

	result, err := engine.Apply(ctx, in, "")
	if err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}

	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		errors.ContextCancelWithError(ctx, errors.Wrap(err))
		return
	}
	fmt.Fprintln(cmd.output, string(body))

	// Nothing landed in any group: exit 1, after the detail is out, so a
	// caller skimming for the code cannot read a fully refused batch as done
	// (cutting-garden#180).
	if result.NothingApplied() {
		errors.ContextCancelWithError(ctx, command.Mismatchf("nothing was applied"))
	}
}

// readInput reads and decodes the document named by args (a file, or stdin
// for no argument or `-`). A document that is not the expected JSON is the
// caller's fault (64); a file that cannot be read is trouble (2).
func (cmd *Mutate) readInput(args []string) (Input, error) {
	var in Input

	var raw []byte
	var err error
	if len(args) == 0 || args[0] == "-" {
		raw, err = io.ReadAll(cmd.stdin)
	} else {
		raw, err = os.ReadFile(args[0])
	}
	if err != nil {
		return in, errors.Wrap(err)
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		return in, errors.BadRequestf("mutate: the document is empty")
	}
	// Unknown members are refused, unlike the MCP tool's lenient decode: a
	// misspelt "strict" or "atomicity" would otherwise silently weaken a write
	// that has no schema to catch it.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return in, errors.BadRequestf("mutate: document: %s", err)
	}

	return in, nil
}

// engineForRun is the injected engine, or the production one: the config
// loaded and injected once (every plugin's section is registered as it
// decodes, and a [[traversal_plugins]] wire scheme exists only after that —
// the same ordering `list` needs), the real resolvers, the `[tags]` override,
// and the per-op log on stderr.
func (cmd *Mutate) engineForRun() (*Engine, error) {
	if cmd.engine != nil {
		return cmd.engine, nil
	}

	cfg, err := command_components.LoadAndInjectConfigAt(cmd.ConfigPath, os.Stderr)
	if err != nil {
		return nil, err
	}

	return &Engine{
		ResolveLister: command_components.ResolveRootListerPlugin,
		ResolveNode:   command_components.ResolveNodeMutatorPlugin,
		ResolveBulk:   command_components.ResolveBulkMutatorPlugin,
		TagsOverride:  cfg.Tags.Interpreter,
		Log:           os.Stderr,
	}, nil
}

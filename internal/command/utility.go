package command

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/collections_slice"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/config_cli"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/flags"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
)

// Config is the interface a Utility uses to source its CLI config —
// implementations bind their CLI flags via SetFlagDefinitions and
// expose a config_cli.Config struct via GetConfigCLI.
type Config interface {
	interfaces.CommandComponentWriter
	GetConfigCLI() config_cli.Config
}

// Utility owns the registered command set and the global Config for
// a binary. Phase 1 keeps this small: registration and lookup only.
// Run-dispatch logic lands in utility_run.go in the next commit.
//
// Aliases declare alternative binary names that ship alongside the
// canonical one. The canonical name is what PrintUsage and the
// manpage NAME line lead with; aliases surface as "(cg, …)" hints
// and produce per-alias shell-completion stubs. The utility's
// behavior under any binary name is identical — aliases are
// cosmetic + completion-routing only.
type Utility struct {
	name    string
	aliases []string
	config  Config
	cmds    map[string]Cmd
}

func MakeUtility(name string, defaultConfig Config) Utility {
	return Utility{
		name:   name,
		config: defaultConfig,
		cmds:   make(map[string]Cmd),
	}
}

func (utility Utility) GetName() string { return utility.name }

// GetAliases returns a copy of the registered alias list (alphabetical
// insertion order). Nil when no aliases are registered.
func (utility Utility) GetAliases() []string {
	if len(utility.aliases) == 0 {
		return nil
	}
	out := make([]string, len(utility.aliases))
	copy(out, utility.aliases)
	return out
}

// AddAlias declares an alternative binary name for this utility.
// Aliases surface in PrintUsage's banner, the utility manpage's NAME
// section, and produce per-alias completion stubs from
// GenerateCompletions. Pointer receiver — aliases is a slice and
// would not propagate through a value-receiver mutation.
func (utility *Utility) AddAlias(alias string) {
	utility.aliases = append(utility.aliases, alias)
}

func (utility Utility) GetConfig() config_cli.Config {
	if utility.config == nil {
		return *config_cli.Default()
	}
	return utility.config.GetConfigCLI()
}

func (utility Utility) GetConfigAny() any { return utility.config }

func (utility Utility) GetCmd(name string) (Cmd, bool) {
	cmd, ok := utility.cmds[name]
	return cmd, ok
}

func (utility Utility) LenCmds() int { return len(utility.cmds) }

func (utility Utility) AllCmds() interfaces.Seq2[string, Cmd] {
	return func(yield func(string, Cmd) bool) {
		for name, cmd := range utility.cmds {
			if !yield(name, cmd) {
				return
			}
		}
	}
}

func (utility Utility) AddCmd(name string, cmd Cmd) {
	if _, ok := utility.cmds[name]; ok {
		panic("subcommand added more than once: " + name)
	}
	utility.cmds[name] = cmd
}

func (utility Utility) MergeUtilityWithPrefix(
	otherUtility Utility,
	prefix string,
) Utility {
	for name, subcommand := range otherUtility.AllCmds() {
		if prefix != "" {
			name = fmt.Sprintf("%s-%s", prefix, name)
		}
		utility.AddCmd(name, subcommand)
	}
	return utility
}

func (utility Utility) MergeUtility(otherUtility Utility) Utility {
	return utility.MergeUtilityWithPrefix(otherUtility, "")
}

func (utility Utility) PrintUsage(ctx interfaces.ActiveContext, err error) {
	if err != nil {
		defer errors.ContextCancelWithError(ctx, err)
	}
	banner := utility.name
	if aliases := utility.GetAliases(); len(aliases) > 0 {
		banner = fmt.Sprintf("%s (%s)", utility.name, strings.Join(aliases, ", "))
	}
	fmt.Fprintf(os.Stderr, "Usage for %s:\n", banner)
	for _, sub := range utility.userFacingSubcommands() {
		fmt.Fprintf(os.Stderr, "  %-12s %s\n", sub.name, sub.short)
	}
}

// Run dispatches the command identified by args[1] and returns the
// process exit code main should propagate. Run never calls os.Exit
// itself so tests can drive it without faulting the test runner.
// Exit semantics: 0 on success, 64 (EX_USAGE) for 400/BadRequest,
// 1 otherwise.
func (utility Utility) Run(args []string) int {
	utilityNameWithExtension := extendNameIfNecessary(utility.GetName())
	ctx := errors.MakeContextDefault()
	ctx.SetCancelOnSignals(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	err := ctx.Run(func(ctx errors.Context) {
		if len(args) <= 1 {
			utility.PrintUsage(ctx,
				errors.BadRequestf("No subcommand provided."))
			return
		}

		cmd, flagSet, ok := utility.MakeCmdAndFlagSet(ctx, args)
		if !ok {
			return
		}

		req, ok := utility.MakeRequest(ctx, cmd, flagSet)
		if !ok {
			return
		}

		cmd.Run(req)
	})
	if err != nil {
		return handleMainErrors(ctx, utilityNameWithExtension, err)
	}
	return 0
}

func (utility Utility) MakeCmdAndFlagSet(
	ctx interfaces.ActiveContext,
	args []string,
) (cmd Cmd, flagSet *flags.FlagSet, ok bool) {
	name := args[1]

	if cmd, ok = utility.GetCmd(name); !ok {
		utility.PrintUsage(ctx, errors.BadRequestf("No subcommand %q", name))
		return
	}

	flagSet = flags.NewFlagSet(name, flags.ContinueOnError)

	if w, isWriter := cmd.(interfaces.CommandComponentWriter); isWriter {
		w.SetFlagDefinitions(flagSet)
	}

	rest := args[2:]

	if utility.config != nil {
		utility.config.SetFlagDefinitions(flagSet)
	}

	if err := flagSet.Parse(rest); err != nil {
		if errors.Is(err, flags.ErrHelp) {
			ok = false
			return
		}
		errors.ContextCancelWithError(ctx, err)
	}

	return cmd, flagSet, true
}

func (utility Utility) MakeRequest(
	ctx interfaces.ActiveContext,
	cmd Cmd,
	flagSet *flags.FlagSet,
) (request Request, ok bool) {
	parsed := flagSet.Args()
	input := CommandLineInput{
		FlagsOrArgs: collections_slice.String(parsed),
		Args:        collections_slice.String(parsed),
	}
	if input.Args.Len() > 0 && input.Args.First() == "--" {
		input.ContainsDoubleHyphen = true
		input.Args.ShiftInPlace(1)
	}
	return Request{
		Utility: utility,
		Context: ctx,
		FlagSet: flagSet,
		input:   &input,
	}, true
}

package command

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/0/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/charlie/flags"
)

// testStdoutHook lets tests intercept the complete subcommand's
// output. Production code falls back to os.Stdout when nil.
var testStdoutHook io.Writer

func completeOut() io.Writer {
	if testStdoutHook != nil {
		return testStdoutHook
	}
	return os.Stdout
}

// RegisterComplete attaches the visible `complete` subcommand to a
// utility. Idempotent: a second call is a no-op so callers don't
// have to coordinate.
func RegisterComplete(u *Utility) {
	if _, exists := u.GetCmd("complete"); exists {
		return
	}
	u.AddCmd("complete", &completeCmd{util: u})
}

type completeCmd struct {
	util       *Utility
	bashStyle  bool
	inProgress string
}

func (c *completeCmd) GetDescription() Description {
	return Description{Short: "complete a command-line"}
}

func (c *completeCmd) SetFlagDefinitions(
	flagSet interfaces.CLIFlagDefinitions,
) {
	flagSet.BoolVar(&c.bashStyle, "bash-style", false,
		"emit bash-style completions")
	flagSet.StringVar(&c.inProgress, "in-progress", "",
		"the partial token currently being completed")
}

func (c *completeCmd) Run(req Request) {
	commandLine := CommandLineInput{
		FlagsOrArgs: append([]string{}, req.PeekArgs()...),
		InProgress:  c.inProgress,
	}

	lastArg, hasLastArg := commandLine.LastArg()
	if !hasLastArg {
		c.completeSubcommands()
		return
	}

	name := req.PopArg("name")
	subcmd, found := c.util.GetCmd(name)
	if !found {
		c.completeSubcommands()
		return
	}

	flagSet := flags.NewFlagSet(name, flags.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if w, ok := subcmd.(interfaces.CommandComponentWriter); ok {
		w.SetFlagDefinitions(flagSet)
	}

	containsDoubleHyphen := false
	for _, a := range commandLine.FlagsOrArgs {
		if a == "--" {
			containsDoubleHyphen = true
			break
		}
	}

	if !containsDoubleHyphen &&
		c.completeSubcommandFlags(req, subcmd, flagSet, commandLine, lastArg) {
		return
	}

	c.completeSubcommandArgs(req, subcmd, commandLine)
}

func (c *completeCmd) completeSubcommands() {
	out := completeOut()
	for name, subcmd := range c.util.AllCmds() {
		if d, ok := subcmd.(CommandWithDescription); ok {
			fmt.Fprintf(out, "%s\t%s\n", name, d.GetDescription().Short)
		} else {
			fmt.Fprintln(out, name)
		}
	}
}

// completeSubcommandArgs is the *repaired* positional-completion
// dispatch. Madder's legacy implementation gutted this function (its
// TODO #48); restored here. Closes the cutting-garden side of madder
// issue #161.
func (c *completeCmd) completeSubcommandArgs(
	req Request,
	subcmd Cmd,
	commandLine CommandLineInput,
) {
	if completer, ok := subcmd.(Completer); ok {
		// Phase 1 has no env_local plumbing in the framework; pass nil
		// for the env arg. Cutting-garden commands that need env type-
		// assert at call site (per the extraction design doc).
		completer.Complete(req, nil, commandLine)
	}
}

func (c *completeCmd) completeSubcommandFlags(
	req Request,
	subcmd Cmd,
	flagSet *flags.FlagSet,
	commandLine CommandLineInput,
	lastArg string,
) (handled bool) {
	if strings.HasPrefix(lastArg, "-") && commandLine.InProgress != "" {
		handled = true
	} else if lastComplete, ok := commandLine.LastCompleteArg(); ok && commandLine.InProgress != "" {
		lastArg = lastComplete
		commandLine.InProgress = ""
		handled = strings.HasPrefix(lastArg, "-")
	}

	out := completeOut()
	if commandLine.InProgress != "" {
		flagSet.VisitAll(func(flag *flags.Flag) {
			fmt.Fprintf(out, "-%s\t%s\n", flag.Name, flag.Usage)
		})
		return handled
	}

	if err := flagSet.Parse([]string{lastArg}); err != nil {
		c.completeSubcommandFlagOnParseError(req, subcmd, flagSet, commandLine, err)
	} else {
		flagSet.VisitAll(func(flag *flags.Flag) {
			fmt.Fprintf(out, "-%s\t%s\n", flag.Name, flag.Usage)
		})
	}
	return handled
}

func (c *completeCmd) completeSubcommandFlagOnParseError(
	req Request,
	subcmd Cmd,
	flagSet *flags.FlagSet,
	commandLine CommandLineInput,
	err error,
) {
	after, found := strings.CutPrefix(err.Error(), "flag needs an argument: -")
	if !found {
		errors.ContextCancelWithError(req.Context, err)
		return
	}

	flag := flagSet.Lookup(after)
	if flag == nil {
		errors.ContextCancelWithError(req.Context,
			fmt.Errorf("flag %q not found", after))
		return
	}

	if completer, ok := flag.Value.(Completer); ok {
		completer.Complete(req, nil, commandLine)
	} else {
		errors.ContextCancelWithError(req.Context,
			fmt.Errorf("no completion for flag %q", after))
	}
}

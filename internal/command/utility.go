package command

import (
	"fmt"

	"github.com/amarbel-llc/purse-first/libs/dewey/0/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/foxtrot/config_cli"
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
type Utility struct {
	name   string
	config Config
	cmds   map[string]Cmd
}

func MakeUtility(name string, defaultConfig Config) Utility {
	return Utility{
		name:   name,
		config: defaultConfig,
		cmds:   make(map[string]Cmd),
	}
}

func (utility Utility) GetName() string { return utility.name }

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

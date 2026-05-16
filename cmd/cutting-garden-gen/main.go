// Command cutting-garden-gen writes the cutting-garden binary's
// manpages and shell-completion stubs under a caller-supplied
// prefix.
//
// Usage:
//
//	cutting-garden-gen <prefix>
//
// Writes:
//
//	<prefix>/share/man/man1/cutting-garden.1
//	<prefix>/share/man/man1/cutting-garden-<sub>.1   (one per subcommand)
//	<prefix>/share/bash-completion/completions/cutting-garden
//	<prefix>/share/fish/vendor_completions.d/cutting-garden.fish
//	<prefix>/share/zsh/site-functions/_cutting-garden
//
// The flake's postInstall calls `$out/bin/cutting-garden-gen $out`
// then removes the gen binary so release artifacts don't ship it.
//
// The Utility built here MUST match the one in cmd/cutting-garden so
// the generated artifacts describe the binary users actually install.
// Adding a subcommand to cmd/cutting-garden requires adding it here
// too — there is no compile-time link between the two main.go files.
package main

import (
	"fmt"
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/capture"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/diff"
	"github.com/amarbel-llc/cutting-garden/internal/restore"
	// Blank-imports mirror cmd/cutting-garden/main.go so the
	// constructed Utility is byte-equivalent for generator purposes.
	// Manpage/completion gen doesn't actually need the plugins to be
	// registered, but keeping the import set identical avoids
	// drift-bait when someone copy-pastes one main.go to the other.
	_ "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_file"
	_ "github.com/amarbel-llc/madder/go/pkgs/markl_registrations"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr,
			"usage: cutting-garden-gen <prefix>")
		os.Exit(64) // EX_USAGE
	}
	prefix := os.Args[1]

	utility := command.MakeUtility("cutting-garden", nil)
	command.RegisterComplete(&utility)
	utility.AddCmd("capture", capture.New())
	utility.AddCmd("restore", restore.New())
	utility.AddCmd("diff", diff.New())

	if err := utility.GenerateUtilityManpage(prefix); err != nil {
		fmt.Fprintf(os.Stderr,
			"cutting-garden-gen: GenerateUtilityManpage: %v\n", err)
		os.Exit(1)
	}
	if err := utility.GenerateManpages(prefix); err != nil {
		fmt.Fprintf(os.Stderr,
			"cutting-garden-gen: GenerateManpages: %v\n", err)
		os.Exit(1)
	}
	if err := utility.GenerateCompletions(prefix); err != nil {
		fmt.Fprintf(os.Stderr,
			"cutting-garden-gen: GenerateCompletions: %v\n", err)
		os.Exit(1)
	}
}

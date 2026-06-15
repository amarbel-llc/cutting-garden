// Command cutting-garden-gen writes the cutting-garden binary's
// manpages and shell-completion stubs under a caller-supplied
// prefix.
//
// Usage:
//
//	cutting-garden-gen <prefix>
//
// Writes (for the canonical name AND each registered alias):
//
//	<prefix>/share/man/man1/cutting-garden.1
//	<prefix>/share/man/man1/cutting-garden-<sub>.1   (one per subcommand)
//	<prefix>/share/man/man1/cg.1                    (symlink → cutting-garden.1)
//	<prefix>/share/bash-completion/completions/{cutting-garden,cg}
//	<prefix>/share/fish/vendor_completions.d/{cutting-garden,cg}.fish
//	<prefix>/share/zsh/site-functions/{_cutting-garden,_cg}
//
// The flake's postInstall calls `$out/bin/cutting-garden-gen $out`
// then removes the gen binary so release artifacts don't ship it.
//
// The Utility is built by cgapp.Build, the same factory the
// cutting-garden + cg binaries use — drift between the generator and
// the real binaries is impossible by construction.
package main

import (
	"fmt"
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/cgapp"

	// Register the standard in-repo plugin set so generated manpages and
	// completions reflect every plugin's schemes. cgapp.Build() is
	// plugin-bare (RFC 0009 §5 step 3); the in-repo binaries opt in here.
	_ "github.com/amarbel-llc/cutting-garden/plugins/all"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr,
			"usage: cutting-garden-gen <prefix>")
		os.Exit(64) // EX_USAGE
	}
	prefix := os.Args[1]

	utility := cgapp.Build()

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

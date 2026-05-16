package command

import (
	"fmt"
	"os"
	"path/filepath"
)

// GenerateCompletions writes per-shell stub completion scripts under
// <outDir>/share/{bash-completion,fish,zsh}/. Each stub is a thin
// delegator that asks the running binary for completion candidates
// via its `complete --bash-style` subcommand. The binary owns the
// completion grammar; the stubs hold no per-command knowledge.
//
// One stub per registered name (canonical + aliases) per shell.
// Each stub references its own binary name in the `complete -F`
// registration and the inner CLI call, so `cg <TAB>` shells out to
// the `cg` binary (not `cutting-garden`). This means the stubs
// CANNOT be symlinks to one another — every name needs its own
// content with the right binary baked in.
func (utility Utility) GenerateCompletions(outDir string) error {
	for _, name := range utility.allInvocationNames() {
		if err := generateBashStub(outDir, name); err != nil {
			return err
		}
		if err := generateFishStub(outDir, name); err != nil {
			return err
		}
		if err := generateZshStub(outDir, name); err != nil {
			return err
		}
	}
	return nil
}

// allInvocationNames returns the canonical name followed by any
// registered aliases. The order matches what users would type;
// callers that iterate to produce per-name artifacts get the
// canonical artifact first (handy for "the canonical file already
// exists when an alias write fires" reasoning).
func (utility Utility) allInvocationNames() []string {
	aliases := utility.GetAliases()
	out := make([]string, 0, 1+len(aliases))
	out = append(out, utility.GetName())
	out = append(out, aliases...)
	return out
}

func generateBashStub(outDir, name string) error {
	dir := filepath.Join(outDir, "share", "bash-completion", "completions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf(`# bash completion for %[1]s
_%[1]s() {
    local cur words cword
    _get_comp_words_by_ref -n =: cur words cword
    local in_progress="${cur}"
    COMPREPLY=( $(compgen -W "$(%[1]s complete --bash-style --in-progress="${in_progress}" -- "${words[@]:1}")" -- "${cur}") )
}
complete -F _%[1]s %[1]s
`, name)
	return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
}

func generateFishStub(outDir, name string) error {
	dir := filepath.Join(outDir, "share", "fish", "vendor_completions.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf(`# fish completion for %[1]s
function __%[1]s_complete
    set -l args (commandline -opc)
    set -l cur (commandline -ct)
    %[1]s complete --bash-style --in-progress="$cur" -- $args[2..]
end
complete -c %[1]s -f -a '(__%[1]s_complete)'
`, name)
	return os.WriteFile(filepath.Join(dir, name+".fish"), []byte(body), 0o644)
}

func generateZshStub(outDir, name string) error {
	dir := filepath.Join(outDir, "share", "zsh", "site-functions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf(`#compdef %[1]s
_%[1]s() {
    local -a candidates
    candidates=( ${(f)"$(%[1]s complete --bash-style --in-progress="${words[CURRENT]}" -- "${words[@]:1}")"} )
    _describe 'completions' candidates
}
_%[1]s "$@"
`, name)
	return os.WriteFile(filepath.Join(dir, "_"+name), []byte(body), 0o644)
}

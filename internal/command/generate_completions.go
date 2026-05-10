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
func (utility Utility) GenerateCompletions(outDir string) error {
	if err := utility.generateBashStub(outDir); err != nil {
		return err
	}
	if err := utility.generateFishStub(outDir); err != nil {
		return err
	}
	return utility.generateZshStub(outDir)
}

func (utility Utility) generateBashStub(outDir string) error {
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
`, utility.GetName())
	return os.WriteFile(filepath.Join(dir, utility.GetName()),
		[]byte(body), 0o644)
}

func (utility Utility) generateFishStub(outDir string) error {
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
`, utility.GetName())
	return os.WriteFile(filepath.Join(dir, utility.GetName()+".fish"),
		[]byte(body), 0o644)
}

func (utility Utility) generateZshStub(outDir string) error {
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
`, utility.GetName())
	return os.WriteFile(filepath.Join(dir, "_"+utility.GetName()),
		[]byte(body), 0o644)
}

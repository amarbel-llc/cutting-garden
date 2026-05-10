package command

import "io/fs"

// EnvVar declares an environment variable that a command reads, for
// inclusion in the manpage ENVIRONMENT section.
type EnvVar struct {
	Name        string // variable name, e.g. "CUTTING_GARDEN_VERBOSE"
	Description string // one-paragraph description (plain text)
	Default     string // optional; rendered as "Default: ..." when non-empty
}

// FilePath declares a file or directory path that a command reads or
// writes, for inclusion in the manpage FILES section.
type FilePath struct {
	Path        string // filesystem path, e.g. "$XDG_CONFIG_HOME/cutting-garden"
	Description string // one-paragraph description (plain text)
}

// ManpageFile declares a hand-written manpage source file (any roff
// dialect) to be installed alongside auto-generated pages. Source may
// be any fs.FS — typically an embed.FS for binary-bundled docs.
type ManpageFile struct {
	Source  fs.FS  // filesystem to read from
	Path    string // path within Source
	Section int    // man section number, e.g. 1, 5, 7
	Name    string // installed filename, e.g. "cutting-garden-config.5"
}

// Example represents a single usage example for a command.
type Example struct {
	Description string // what this example demonstrates
	Command     string // shell invocation (may be multi-line)
	Output      string // optional expected output snippet
}

// Opt-in interfaces. A Cmd implements one or more of these to surface
// metadata to the manpage generator.

type CommandWithEnvVars interface {
	GetEnvVars() []EnvVar
}

type CommandWithFiles interface {
	GetFiles() []FilePath
}

type CommandWithSeeAlso interface {
	GetSeeAlso() []string
}

type CommandWithExamples interface {
	GetExamples() []Example
}

type CommandWithManpageFiles interface {
	GetManpageFiles() []ManpageFile
}

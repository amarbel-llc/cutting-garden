// Package output_format provides a shared -format flag for cutting-garden
// (and madder) commands that stream per-record results.
//
// The Format type is a flag.Value with three values: auto, tap, and json
// (NDJSON, one object per line). auto is the default; Resolve collapses it
// to tap when stdout is a TTY and json when stdout is piped.
//
// Each consuming command defines its own sink interface (TAP vs NDJSON)
// because per-command event shapes differ. This package only supplies the
// flag type and auto-detect helper; it does not prescribe a sink shape.
//
// VENDORED FROM madder@eb8dc31 (tag go/v0.3.15),
// go/internal/charlie/output_format/. Madder@6ff15af exposes this via
// dagnabit as `pkgs/output_format`, but we can't bump past v0.3.15 yet
// (the post-v0.3.15 markl wire-format change breaks pre-flip on-disk
// stores). Delete this copy and rewrite imports once we bump the
// madder dep past 6ff15af.
package output_format

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// Format selects the encoding of a command's per-record result stream.
//
// auto (default): NDJSON when stdout is not a TTY, TAP otherwise.
// tap:            TAP format regardless of stdout.
// json:           NDJSON (one JSON object per record) regardless of stdout.
type Format string

const (
	FormatAuto = Format("auto")
	FormatTAP  = Format("tap")
	FormatJSON = Format("json")
)

// Default is the value to initialize a flag field with.
const Default = FormatAuto

// FlagDescription is a suggested description for use with flag.Var so all
// commands present the same help text.
const FlagDescription = "output format: auto (default), tap, or json (NDJSON)"

func (f Format) String() string { return string(f) }

func (f *Format) Set(value string) error {
	clean := Format(strings.TrimSpace(strings.ToLower(value)))

	switch clean {
	case FormatAuto, FormatTAP, FormatJSON:
		*f = clean
		return nil
	}

	return fmt.Errorf("unsupported output format: %q", value)
}

func (Format) GetCLICompletion() map[string]string {
	return map[string]string{
		FormatAuto.String(): "TAP on a TTY, NDJSON when stdout is piped (default)",
		FormatTAP.String():  "TAP format (human-readable)",
		FormatJSON.String(): "NDJSON: one JSON object per record",
	}
}

// Resolve collapses auto into tap or json based on whether stdout is a
// terminal. Non-auto values are returned unchanged.
func (f Format) Resolve(stdout *os.File) Format {
	if f != FormatAuto {
		return f
	}

	fd := stdout.Fd()
	if isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd) {
		return FormatTAP
	}

	return FormatJSON
}

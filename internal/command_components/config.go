package command_components

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"code.linenisgreat.com/cutting-garden/internal/cgconfig"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ConfigFileName is the config leaf under $XDG_CONFIG_HOME/cutting-garden/.
const ConfigFileName = "config.toml"

// DefaultConfigPath returns $XDG_CONFIG_HOME/cutting-garden/config.toml,
// using os.UserConfigDir — which honors $XDG_CONFIG_HOME and falls back to
// $HOME/.config on Linux (RFC 0007 § File Location).
//
// The loader lives here rather than in cgconfig because cgconfig holds the
// `//go:generate tommy generate` schema struct: tommy ignores its own
// generated file during regeneration, so a sibling that consumed
// DecodeConfigV0 would break `tommy generate` (codegen bootstrap). Keeping
// the consumer out of the schema package avoids that.
func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", errors.Wrap(err)
	}
	return filepath.Join(dir, "cutting-garden", ConfigFileName), nil
}

// LoadConfig reads and decodes the config at path. A missing file yields a
// zero ConfigV0 and a nil error — cutting-garden runs normally with no
// config (RFC 0007 § File Location). Decode or Validate failures are
// returned as bad-request errors (EX_USAGE). Keys present in the file but
// consumed by no field are reported to warnw (typically os.Stderr) and do
// not fail the load.
func LoadConfig(path string, warnw io.Writer) (*cgconfig.ConfigV0, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &cgconfig.ConfigV0{}, nil
	}
	if err != nil {
		return nil, errors.Wrapf(err, "read config %s", path)
	}

	doc, err := cgconfig.DecodeConfigV0(raw)
	if err != nil {
		return nil, errors.BadRequestf("%s: %s", path, err)
	}

	if warnw != nil {
		if unused := doc.Undecoded(); len(unused) > 0 {
			fmt.Fprintf(warnw,
				"warning: %s: unknown config keys: %v\n", path, unused)
		}
	}

	return doc.Data(), nil
}

// LoadDefaultConfig loads the config from DefaultConfigPath, the common
// entry point for the composition root. A missing file yields an empty
// config (see LoadConfig).
func LoadDefaultConfig(warnw io.Writer) (*cgconfig.ConfigV0, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadConfig(path, warnw)
}

package command_components

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/cgconfig"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
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
	_, cfg, err := loadConfigWithRaw(path, warnw)
	return cfg, err
}

// loadConfigWithRaw is LoadConfig plus the raw file bytes, which the
// traversal-plugin registration needs for SectionTOML (RFC 0013 §Host
// integration — the sections stanzas name are passed through raw, never
// decoded here). raw is nil when the file is absent.
func loadConfigWithRaw(
	path string, warnw io.Writer,
) ([]byte, *cgconfig.ConfigV0, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &cgconfig.ConfigV0{}, nil
	}
	if err != nil {
		return nil, nil, errors.Wrapf(err, "read config %s", path)
	}

	doc, err := cgconfig.DecodeConfigV0(raw)
	if err != nil {
		return nil, nil, errors.BadRequestf("%s: %s", path, err)
	}
	cfg := doc.Data()

	if warnw != nil {
		unused := withoutStanzaClaimedKeys(
			doc.Undecoded(), cfg.TraversalPlugins,
		)
		if len(unused) > 0 {
			fmt.Fprintf(warnw,
				"warning: %s: unknown config keys: %v\n", path, unused)
		}
	}

	return raw, cfg, nil
}

// withoutStanzaClaimedKeys drops undecoded-key warnings for sections a
// [[traversal_plugins]] stanza claims via config_section: those keys are
// consumed — raw, by the wire plugin itself — not unknown.
func withoutStanzaClaimedKeys(
	keys []string, stanzas []traversal_serve.PluginStanza,
) []string {
	if len(stanzas) == 0 || len(keys) == 0 {
		return keys
	}

	kept := keys[:0:0]
	for _, key := range keys {
		claimed := false
		for _, stanza := range stanzas {
			section := stanza.Section()
			if key == section || strings.HasPrefix(key, section+".") {
				claimed = true
				break
			}
		}
		if !claimed {
			kept = append(kept, key)
		}
	}
	return kept
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

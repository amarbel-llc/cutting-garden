package command_components

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"code.linenisgreat.com/cutting-garden/internal/cgconfig"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
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
// config (RFC 0007 § File Location). A TOML syntax error, and a failing
// framework section, is a bad-request error (EX_USAGE); a failing plugin
// section (a type error inside it included) is skipped with a warning unless
// CG_STRICT_CONFIG=1 (cutting-garden#325), and SkippedConfigSections reports
// it, for this load only. Keys present in the file but
// consumed by no field are reported to warnw (typically os.Stderr) and do
// not fail the load.
//
// LOADING INJECTS. Decoding covers the framework sections (ConfigV0) AND
// every plugin section registered with the SDK (cutting_garden_plugins.
// MustRegisterConfigSection), and a registered decoder ends by injecting
// its section into the plugin's package state — the injection
// LoadAndInjectConfig's name promises is this same dispatch. So this is
// NOT a cheap read: each call re-parses the file and reassigns every
// linked plugin's accounts. Call it ONCE per command invocation and thread
// the returned value to whatever needs a config lever, rather than
// re-reading (internal/list and internal/organize do exactly that; there
// is deliberately no cache here).
//
// A top-level table no linked plugin registered is an unknown key (warned,
// not an error), so a config naming a plugin this binary does not link
// loads fine.
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
	// The skipped sections describe this load only, whatever path it takes.
	setSkippedConfigSections(nil)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &cgconfig.ConfigV0{}, nil
	}
	if err != nil {
		return nil, nil, errors.Wrapf(err, "read config %s", path)
	}

	// One parse, one CST value model: the framework sections decode through
	// cgconfig's generated delegation entrypoint and every registered plugin
	// section through its own decoder over the SAME model, so both sets of
	// consumption marks feed the single unknown-key report below. (The
	// generated document type's model is unexported and cgconfig cannot
	// hand-write an accessor beside its codec — tommy blanks its own output
	// while type-checking — so the loader decomposes the bytes itself.)
	model, err := cst.DecomposeBytes(raw)
	if err != nil {
		return nil, nil, errors.BadRequestf("%s: %s", path, err)
	}
	cfg := &cgconfig.ConfigV0{}
	if err := cgconfig.DecodeConfigV0Into(cfg, model); err != nil {
		return nil, nil, errors.BadRequestf("%s: %s", path, err)
	}

	// A plugin section whose decoder fails is skipped with a warning, so one
	// bad account cannot take every plugin down (cutting-garden#325); the
	// plugin then simply has no configured accounts. CG_STRICT_CONFIG=1
	// restores fail-fast for CI: the dispatch error already names the
	// section and the entry, and prefixing the path yields
	// "<path>: <section>: <entry>", EX_USAGE naming all three (RFC 0007
	// § Loading and Validation).
	failures := cutting_garden_plugins.DecodeRegisteredConfigSectionsEach(model)
	if len(failures) > 0 && strictConfig() {
		return nil, nil, errors.BadRequestf(
			"%s: %s: %s", path, failures[0].Section, failures[0].Err)
	}
	skipped := make([]SkippedConfigSection, 0, len(failures))
	skippedNames := make([]traversal_serve.PluginStanza, 0, len(failures))
	for _, f := range failures {
		skipped = append(skipped, SkippedConfigSection{Section: f.Section, Error: f.Err.Error()})
		skippedNames = append(skippedNames, traversal_serve.PluginStanza{ConfigSection: f.Section})
		if warnw != nil {
			fmt.Fprintf(warnw, "warning: %s: skipping [%s]: %s\n", path, f.Section, f.Err)
		}
	}
	setSkippedConfigSections(skipped)

	if warnw != nil {
		// Both stanza tables claim sections: the generalized [[plugins]] and
		// the [[traversal_plugins]] alias (cutting-garden#347). A skipped
		// section's leftover keys were reported above, not as unknown.
		unused := withoutStanzaClaimedKeys(
			model.Undecoded(), slices.Concat(cfg.Plugins, cfg.TraversalPlugins, skippedNames),
		)
		if len(unused) > 0 {
			fmt.Fprintf(warnw,
				"warning: %s: unknown config keys: %v\n", path, unused)
		}
	}

	return raw, cfg, nil
}

// strictConfigEnv makes a failing plugin config section fatal again
// (cutting-garden#325), for CI and nix checks.
const strictConfigEnv = "CG_STRICT_CONFIG"

func strictConfig() bool {
	v := os.Getenv(strictConfigEnv)
	return v != "" && v != "0"
}

// SkippedConfigSection is a plugin config section the last load skipped
// because its decoder failed; Error is the decoder's message, which names
// the entry.
type SkippedConfigSection struct {
	Section string `json:"section"`
	Error   string `json:"error"`
}

var (
	skippedMu     sync.Mutex
	skippedConfig []SkippedConfigSection
)

func setSkippedConfigSections(s []SkippedConfigSection) {
	skippedMu.Lock()
	defer skippedMu.Unlock()
	skippedConfig = s
}

// SkippedConfigSections reports the sections the most recent config load
// skipped, for health and the mcp server to surface; empty when none.
func SkippedConfigSections() []SkippedConfigSection {
	skippedMu.Lock()
	defer skippedMu.Unlock()
	return slices.Clone(skippedConfig)
}

// withoutStanzaClaimedKeys drops undecoded-key warnings for sections a
// [[plugins]] or [[traversal_plugins]] stanza claims via config_section: those keys are
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

// LoadDefaultConfig loads the config from DefaultConfigPath. A missing
// file yields an empty config, and — like LoadConfig, which this wraps —
// loading INJECTS every registered plugin section, so it is once-per-
// invocation work, not a cheap re-read. A command that has already called
// LoadAndInjectConfig holds the value and must thread it rather than
// calling this again.
func LoadDefaultConfig(warnw io.Writer) (*cgconfig.ConfigV0, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadConfig(path, warnw)
}

package cutting_garden_plugins

import (
	"sort"
	"sync"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
)

// ConfigSectionDecoder decodes one top-level table of the cutting-garden
// config (RFC 0007 § Plugin-Owned Sections). sub is the table's tommy CST
// value — the same model the framework's own DecodeConfigV0 walks — so the
// decoder MUST mark what it consumes (a tommy-generated Decode<X>Into does)
// for the loader's unknown-key report to stay accurate, and MUST validate
// (the generated decoder invokes the section type's Validate). It runs once
// per process from the composition step, after every plugin init() has
// registered, and typically ends by injecting the decoded section into the
// plugin's package state (SetConfiguredAccounts or the like).
type ConfigSectionDecoder func(sub *cst.Value) error

// configSectionRegistry is the name-keyed index of plugin config-section
// decoders. It replaces RFC 0007's original static delegation, where the
// framework's ConfigV0 named each plugin's section TYPE and so imported every
// account-bearing plugin: here the plugin registers its section by NAME at
// init() and the framework imports no plugin (the invalidation-cone
// inversion, docs/plans/2026-09-21-godyn-invalidation-cone-research.md §1.5).
type configSectionRegistry struct {
	mu       sync.RWMutex
	decoders map[string]ConfigSectionDecoder
}

func newConfigSectionRegistry() *configSectionRegistry {
	return &configSectionRegistry{decoders: map[string]ConfigSectionDecoder{}}
}

func (r *configSectionRegistry) register(name string, d ConfigSectionDecoder) error {
	if name == "" {
		return errors.Errorf("config section: empty name")
	}
	if d == nil {
		return errors.Errorf("config section %q: nil decoder", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.decoders[name]; ok {
		return errors.Errorf("%w: config section %q", ErrAlreadyRegistered, name)
	}
	r.decoders[name] = d
	return nil
}

// names returns the registered section names, sorted so dispatch (and any
// listing) is deterministic regardless of init() order.
func (r *configSectionRegistry) names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.decoders))
	for name := range r.decoders {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// decode dispatches every registered section present in model as a table.
// A registered name absent from the document is simply skipped (the section
// is optional, exactly as the delegated field was); a top-level key that is
// present but no registered decoder claims is left unmarked, so the
// document's Undecoded report surfaces it as an unknown key (RFC 0007
// § Loading and Validation). A decoder's error is returned wrapped with the
// section name, so the loader can prefix the file path and report EX_USAGE
// naming file, section, and entry.
func (r *configSectionRegistry) decode(model *cst.Value) error {
	for _, name := range r.names() {
		sub, ok := model.Get(name)
		if !ok || sub.Kind != cst.VTable {
			continue
		}
		r.mu.RLock()
		d := r.decoders[name]
		r.mu.RUnlock()
		sub.MarkSeen()
		if err := d(sub); err != nil {
			return errors.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

var defaultConfigSectionRegistry = newConfigSectionRegistry()

// MustRegisterConfigSection installs d as the decoder of the top-level
// config table `[name]` (RFC 0007 § Plugin-Owned Sections). Panics on
// duplicate registration; intended for plugin init() functions where a clash
// is a programming error, exactly like MustRegisterScheme. An account-bearing
// plugin registers its scheme name and wraps its tommy-generated
// Decode<X>Into followed by its inject step.
func MustRegisterConfigSection(name string, d ConfigSectionDecoder) {
	if err := defaultConfigSectionRegistry.register(name, d); err != nil {
		panic(err)
	}
}

// RegisteredConfigSections returns every registered section name, sorted.
func RegisteredConfigSections() []string {
	return defaultConfigSectionRegistry.names()
}

// DecodeRegisteredConfigSections runs each registered section decoder over
// the top-level table of the same name in model — the parsed config
// document's CST value model (the one DecodeConfigV0 walked, so consumption
// marks land on one model and Undecoded stays accurate). The composition
// step calls it once, right after decoding the framework sections and
// before any plugin's roots are aggregated. Exported because the loader
// lives in the framework (command_components), outside this package.
func DecodeRegisteredConfigSections(model *cst.Value) error {
	return defaultConfigSectionRegistry.decode(model)
}

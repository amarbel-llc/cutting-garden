package command_components

import (
	"context"
	"io"
	"net/url"
	"sync"

	"code.linenisgreat.com/cutting-garden/internal/cgconfig"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// LoadAndInjectConfig loads the default config (a missing file is empty)
// and injects each plugin's section into its package state, so
// RootProvider.Roots and credential resolution reflect the user's config
// (RFC 0007). Idempotent — the step every root-consuming command runs
// before aggregating. A malformed config returns a bad-request error
// (EX_USAGE); unknown keys are warned to warnw.
//
// It also registers each [[traversal_plugins]] wire plugin (RFC 0013
// §Host integration). Scheme registration is not idempotent, so that
// step runs once per process — one process loads one config — and its
// result is replayed on later calls.
func LoadAndInjectConfig(warnw io.Writer) error {
	path, err := DefaultConfigPath()
	if err != nil {
		return err
	}
	raw, cfg, err := loadConfigWithRaw(path, warnw)
	if err != nil {
		return err
	}
	cgconfig.Inject(cfg)

	traversalRegisterOnce.Do(func() {
		traversalRegisterErr = registerTraversalPlugins(cfg, raw)
	})
	return traversalRegisterErr
}

var (
	traversalRegisterOnce sync.Once
	traversalRegisterErr  error
)

// registerTraversalPlugins builds and registers one WirePlugin per
// [[traversal_plugins]] stanza: the RFC 0013 §Host integration
// switch-on. Registration is lazy on the plugin side (NewWirePlugin
// spawns nothing), so a configured-but-unused plugin costs nothing. A
// scheme clash — against a linked plugin or an earlier stanza — is user
// misconfiguration: EX_USAGE, not a panic.
func registerTraversalPlugins(
	cfg *cgconfig.ConfigV0, raw []byte,
) error {
	for _, stanza := range cfg.TraversalPlugins {
		configTOML, err := traversal_serve.SectionTOML(raw, stanza.Section())
		if err != nil {
			return errors.BadRequestf(
				"traversal plugin %q: %s", stanza.Name, err,
			)
		}

		plugin := traversal_serve.NewWirePlugin(traversal_serve.PluginSpec{
			Name:       stanza.Name,
			Command:    stanza.Command,
			Schemes:    stanza.Schemes,
			ConfigTOML: configTOML,
		})
		if err := cutting_garden_plugins.RegisterScheme(plugin); err != nil {
			return errors.BadRequestf(
				"traversal plugin %q: %s", stanza.Name, err,
			)
		}
	}
	return nil
}

// AggregateRoots returns every top-level root across all registered
// RootProvider plugins (RFC 0007 § The Root-Provider Capability), in
// registry order. Fail-fast: any plugin's error aborts rather than
// yielding a silently partial set. Call LoadAndInjectConfig first so
// config-driven plugins see their accounts.
func AggregateRoots(ctx context.Context) ([]*url.URL, error) {
	var out []*url.URL
	for _, plugin := range cutting_garden_plugins.RegisteredPlugins() {
		provider, ok := plugin.(cutting_garden_plugins.RootProvider)
		if !ok {
			continue
		}
		roots, err := provider.Roots(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, roots...)
	}
	return out, nil
}

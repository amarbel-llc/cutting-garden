package command_components

import (
	"context"
	"io"
	"net/url"
	"slices"
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
// It also registers each [[plugins]] / [[traversal_plugins]] wire
// plugin stanza (RFC 0013 §Host integration, generalized by
// cutting-garden#146 slice 2). Scheme registration is not idempotent,
// so that step runs once per process — one process loads one config —
// and its result is replayed on later calls.
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

	pluginRegisterOnce.Do(func() {
		pluginRegisterErr = registerPlugins(cfg, raw)
	})
	return pluginRegisterErr
}

var (
	pluginRegisterOnce sync.Once
	pluginRegisterErr  error
)

// registerPlugins builds and registers one wire plugin per configured
// stanza: the RFC 0013 §Host integration switch-on, generalized by
// cutting-garden#146 slice 2 to cover both the legacy
// `[[traversal_plugins]]` compatibility alias (registered with Command
// used verbatim, unchanged from RFC 0013's original behavior) and the
// general `[[plugins]]` table (registered with Command treated as the
// base binary invocation — see traversal_serve.PluginStanza's doc
// comment). Registration is lazy on the plugin side (NewWirePlugin
// spawns nothing), so a configured-but-unused plugin costs nothing. A
// scheme clash — against a linked plugin or an earlier stanza — is user
// misconfiguration: EX_USAGE, not a panic.
func registerPlugins(cfg *cgconfig.ConfigV0, raw []byte) error {
	for _, stanza := range cfg.TraversalPlugins {
		if err := registerStanza(stanza, raw, true); err != nil {
			return err
		}
	}
	for _, stanza := range cfg.Plugins {
		if err := registerStanza(stanza, raw, false); err != nil {
			return err
		}
	}
	return nil
}

// registerStanza registers one stanza's plugin(s) for whichever
// protocol(s) it declares (traversal_serve.PluginStanza.
// EffectiveProtocols). legacyVerbatimCommand is true for a
// `[[traversal_plugins]]` compatibility-alias stanza, where Command is
// the full argv verbatim per RFC 0013's original convention; false for
// a `[[plugins]]` general-table stanza, where Command is the base
// binary invocation and the protocol-specific subcommand is appended.
func registerStanza(
	stanza traversal_serve.PluginStanza, raw []byte, legacyVerbatimCommand bool,
) error {
	configTOML, err := traversal_serve.SectionTOML(raw, stanza.Section())
	if err != nil {
		return errors.BadRequestf("plugin %q: %s", stanza.Name, err)
	}

	protocols := stanza.EffectiveProtocols()
	wantsTraversal := slices.Contains(protocols, traversal_serve.ProtocolTraversal)
	wantsCapture := slices.Contains(protocols, traversal_serve.ProtocolCapture)

	var plugin cutting_garden_plugins.Plugin
	switch {
	case wantsTraversal && !wantsCapture:
		cmd := stanza.Command
		if !legacyVerbatimCommand {
			cmd = append(append([]string{}, stanza.Command...), "traversal-serve")
		}
		plugin = traversal_serve.NewWirePlugin(traversal_serve.PluginSpec{
			Name:       stanza.Name,
			Command:    cmd,
			Schemes:    stanza.Schemes,
			ConfigTOML: configTOML,
		})
	case wantsCapture:
		// cutting-garden#146 slice 2 phase 2 wires the capture-side
		// launcher here (and the wantsTraversal+wantsCapture combined
		// case); until then a capture-protocol stanza is a clear
		// configuration error rather than a silent no-op.
		return errors.BadRequestf(
			"plugin %q: protocol %q is not yet implemented",
			stanza.Name, traversal_serve.ProtocolCapture,
		)
	default:
		return errors.BadRequestf(
			"plugin %q: no recognized protocols in %v", stanza.Name, protocols,
		)
	}

	if err := cutting_garden_plugins.RegisterScheme(plugin); err != nil {
		return errors.BadRequestf("plugin %q: %s", stanza.Name, err)
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

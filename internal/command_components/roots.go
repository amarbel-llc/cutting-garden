package command_components

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"
	"sync"

	"code.linenisgreat.com/cutting-garden/internal/capture_wire"
	"code.linenisgreat.com/cutting-garden/internal/cgconfig"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
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
//
// The loaded config is returned so a caller that also needs a config
// VALUE (e.g. the mcp server's [tags] override) reuses this load instead
// of a second LoadDefaultConfig read; a caller wanting only the injection
// discards it.
func LoadAndInjectConfig(warnw io.Writer) (*cgconfig.ConfigV0, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	raw, cfg, err := loadConfigWithRaw(path, warnw)
	if err != nil {
		return nil, err
	}
	cgconfig.Inject(cfg)

	pluginRegisterOnce.Do(func() {
		pluginRegisterErr = registerPlugins(cfg, raw)
	})
	if pluginRegisterErr != nil {
		return nil, pluginRegisterErr
	}
	return cfg, nil
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

	var (
		plugin      cutting_garden_plugins.Plugin
		captureWire *capture_wire.Plugin
	)
	switch {
	case wantsTraversal && wantsCapture:
		// A single stanza speaking BOTH protocols needs one registered
		// value satisfying both capability surfaces; no configured
		// plugin needs this yet (chrest is capture-only, fj-cg-style
		// peers are traversal-only), so it is a clear configuration
		// error rather than silently registering just one side.
		return errors.BadRequestf(
			"plugin %q: declaring both %q and %q protocols on one stanza"+
				" is not yet supported",
			stanza.Name, traversal_serve.ProtocolTraversal,
			traversal_serve.ProtocolCapture,
		)
	case wantsTraversal:
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
		// Command is always treated as the base binary invocation for
		// capture, regardless of which table decoded the stanza — see
		// traversal_serve.PluginStanza's doc comment: unlike traversal,
		// RFC 0008's v2/v1 fallback needs to append either
		// "capture-serve" or "capture-batch", so there is no verbatim
		// form to preserve for a legacy-alias capture stanza (none
		// exist today; [[traversal_plugins]] predates ProtocolCapture).
		cw := capture_wire.New(capture_wire.Spec{
			Name:    stanza.Name,
			Command: stanza.Command,
			Schemes: stanza.Schemes,
		})
		captureWire = cw
		plugin = cw
	default:
		return errors.BadRequestf(
			"plugin %q: no recognized protocols in %v", stanza.Name, protocols,
		)
	}

	if err := cutting_garden_plugins.RegisterScheme(plugin); err != nil {
		return errors.BadRequestf("plugin %q: %s", stanza.Name, err)
	}

	// A capture-side wire plugin ALSO dispatches diff by receipt kind
	// (internal/diff's runProtocolDiff resolves ResolveProtocolDiff(kind),
	// a registry independent of the scheme registry RegisterScheme just
	// populated) — register it there too, under the same stanza name.
	if captureWire != nil {
		if err := cutting_garden_plugins.RegisterProtocolDiff(captureWire); err != nil {
			return errors.BadRequestf("plugin %q: %s", stanza.Name, err)
		}
	}

	return nil
}

// AggregateRoots returns every top-level root across all registered
// RootProvider plugins (RFC 0007 § The Root-Provider Capability), in
// registry order. Call LoadAndInjectConfig first so config-driven
// plugins see their accounts.
//
// Per-plugin fault isolation (cutting-garden#165): a plugin's Roots
// error — most commonly a wire plugin (RFC 0013) that failed to spawn,
// crashed before announcing, or failed its initialize handshake — is
// contained to that plugin. A warning naming the plugin's schemes and
// the error is written to warnw (nil discards it; callers pass
// os.Stderr, matching LoadAndInjectConfig's convention — NEVER stdout,
// which on `mcp` is the JSON-RPC transport), and the plugin's
// contribution is simply omitted from the result, exactly as if it had
// returned no roots. Before this, ANY plugin's error aborted the whole
// aggregation — including every OTHER already-healthy plugin's roots —
// which on `mcp` meant one misconfigured wire plugin failed
// cutting-garden's own MCP `initialize` handshake with its host and
// took down every scheme (caldav, file, ...), not just its own.
func AggregateRoots(ctx context.Context, warnw io.Writer) ([]*url.URL, error) {
	var out []*url.URL
	for _, plugin := range cutting_garden_plugins.RegisteredPlugins() {
		provider, ok := plugin.(cutting_garden_plugins.RootProvider)
		if !ok {
			continue
		}
		roots, err := provider.Roots(ctx)
		if err != nil {
			if warnw != nil {
				fmt.Fprintf(warnw,
					"warning: plugin %v: roots unavailable: %s\n",
					provider.Schemes(), err)
			}
			continue
		}
		out = append(out, roots...)
	}
	return out, nil
}

// AggregateRootLabels probes every registered plugin implementing
// RootLabeler (cutting-garden#120) and merges their root->label maps into
// one, keyed by each root URL's String() form — the SAME key AggregateRoots'
// URLs stringify to, so a caller pairs them by a plain map lookup. Call
// LoadAndInjectConfig first, exactly as AggregateRoots.
//
// A plugin's label resolution failure is a non-fatal warning, mirroring
// AggregateRoots' own per-plugin fault isolation (cutting-garden#165): that
// plugin's roots simply keep the framework's default label derivation
// rather than aborting the whole aggregation. A returned empty-string label
// for a key is dropped (treated the same as the key being absent), so a
// RootLabeler need not filter its own "no label for this one" entries.
func AggregateRootLabels(ctx context.Context, warnw io.Writer) map[string]string {
	labels := map[string]string{}
	for _, plugin := range cutting_garden_plugins.RegisteredPlugins() {
		labeler, ok := plugin.(cutting_garden_plugins.RootLabeler)
		if !ok {
			continue
		}
		got, err := labeler.RootLabels(ctx)
		if err != nil && warnw != nil {
			fmt.Fprintf(warnw,
				"warning: plugin %v: root labels unavailable: %s\n",
				labeler.Schemes(), err)
		}
		for k, v := range got {
			if v != "" {
				labels[k] = v
			}
		}
	}
	return labels
}

package command_components

import (
	"context"
	"io"
	"net/url"

	"github.com/amarbel-llc/cutting-garden/internal/cgconfig"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// LoadAndInjectConfig loads the default config (a missing file is empty)
// and injects each plugin's section into its package state, so
// RootProvider.Roots and credential resolution reflect the user's config
// (RFC 0007). Idempotent — the step every root-consuming command runs
// before aggregating. A malformed config returns a bad-request error
// (EX_USAGE); unknown keys are warned to warnw.
func LoadAndInjectConfig(warnw io.Writer) error {
	cfg, err := LoadDefaultConfig(warnw)
	if err != nil {
		return err
	}
	cgconfig.Inject(cfg)
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

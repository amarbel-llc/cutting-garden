package cutting_garden_plugins

import (
	"sync"

	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// captureRegistry / restoreRegistry are the in-process indexes,
// populated at init() by each plugin peer-leaf package.
type captureRegistry struct {
	mu      sync.RWMutex
	plugins map[string]CapturePlugin
}

type restoreRegistry struct {
	mu      sync.RWMutex
	plugins map[string]RestorePlugin
}

type diffRegistry struct {
	mu      sync.RWMutex
	plugins map[string]DiffPlugin
}

func newCaptureRegistry() *captureRegistry {
	return &captureRegistry{plugins: map[string]CapturePlugin{}}
}

func newRestoreRegistry() *restoreRegistry {
	return &restoreRegistry{plugins: map[string]RestorePlugin{}}
}

func newDiffRegistry() *diffRegistry {
	return &diffRegistry{plugins: map[string]DiffPlugin{}}
}

func (r *captureRegistry) register(scheme string, p CapturePlugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plugins[scheme]; ok {
		return errors.Errorf("%w: capture %q", ErrAlreadyRegistered, scheme)
	}
	r.plugins[scheme] = p
	return nil
}

func (r *captureRegistry) resolve(scheme string) (CapturePlugin, error) {
	r.mu.RLock()
	p, ok := r.plugins[scheme]
	r.mu.RUnlock()
	if !ok {
		return nil, errors.Errorf("%w: capture %q", ErrUnknownScheme, scheme)
	}
	return p, nil
}

func (r *restoreRegistry) register(scheme string, p RestorePlugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plugins[scheme]; ok {
		return errors.Errorf("%w: restore %q", ErrAlreadyRegistered, scheme)
	}
	r.plugins[scheme] = p
	return nil
}

func (r *restoreRegistry) resolve(scheme string) (RestorePlugin, error) {
	r.mu.RLock()
	p, ok := r.plugins[scheme]
	r.mu.RUnlock()
	if !ok {
		return nil, errors.Errorf("%w: restore %q", ErrUnknownScheme, scheme)
	}
	return p, nil
}

func (r *diffRegistry) register(scheme string, p DiffPlugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plugins[scheme]; ok {
		return errors.Errorf("%w: diff %q", ErrAlreadyRegistered, scheme)
	}
	r.plugins[scheme] = p
	return nil
}

func (r *diffRegistry) resolve(scheme string) (DiffPlugin, error) {
	r.mu.RLock()
	p, ok := r.plugins[scheme]
	r.mu.RUnlock()
	if !ok {
		return nil, errors.Errorf("%w: diff %q", ErrUnknownScheme, scheme)
	}
	return p, nil
}

var (
	defaultCaptureRegistry = newCaptureRegistry()
	defaultRestoreRegistry = newRestoreRegistry()
	defaultDiffRegistry    = newDiffRegistry()
)

// MustRegisterCapture installs p in the default capture registry
// under every scheme p.Schemes() returns. Panics on duplicate
// registration; intended for plugin init() functions where a clash
// is a programming error.
func MustRegisterCapture(p CapturePlugin) {
	for _, s := range p.Schemes() {
		if err := defaultCaptureRegistry.register(s, p); err != nil {
			panic(err)
		}
	}
}

// MustRegisterRestore is the restore-direction analogue of
// MustRegisterCapture.
func MustRegisterRestore(p RestorePlugin) {
	for _, s := range p.Schemes() {
		if err := defaultRestoreRegistry.register(s, p); err != nil {
			panic(err)
		}
	}
}

// ResolveCapture looks up the capture plugin registered under
// scheme. Returns an error wrapping ErrUnknownScheme on miss.
func ResolveCapture(scheme string) (CapturePlugin, error) {
	return defaultCaptureRegistry.resolve(scheme)
}

// ResolveRestore is the restore-direction analogue of
// ResolveCapture.
func ResolveRestore(scheme string) (RestorePlugin, error) {
	return defaultRestoreRegistry.resolve(scheme)
}

// MustRegisterDiff is the diff-direction analogue of
// MustRegisterCapture.
func MustRegisterDiff(p DiffPlugin) {
	for _, s := range p.Schemes() {
		if err := defaultDiffRegistry.register(s, p); err != nil {
			panic(err)
		}
	}
}

// ResolveDiff is the diff-direction analogue of ResolveCapture.
func ResolveDiff(scheme string) (DiffPlugin, error) {
	return defaultDiffRegistry.resolve(scheme)
}

package capture_viewport

import vp "github.com/amarbel-llc/crap/go-crap/viewport"

// Model and its options are the shared CRAP-2 viewport. Aliased here so
// existing call sites (capture.go, the demo) keep using
// capture_viewport.Model / New / WithTitle / WithTailLines unchanged.
type (
	Model  = vp.Model
	Option = vp.Option
)

// New builds a viewport Model ready for tea.NewProgram.
func New(opts ...Option) Model { return vp.New(opts...) }

// WithTitle sets the header label.
func WithTitle(s string) Option { return vp.WithTitle(s) }

// WithTailLines sets the rolling-tail height (default 5).
func WithTailLines(n int) Option { return vp.WithTailLines(n) }

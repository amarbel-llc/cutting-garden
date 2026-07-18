package capture_wire

const (
	// defaultFormat is captured when no per-plugin format override is
	// set. pdf is functional on the Firefox/BiDi backend and
	// normalizable — chrest's default, relocated verbatim.
	defaultFormat = "pdf"

	// defaultBrowser is the plugin-namespaced browser default handed
	// to the capture-serve/capture-batch session via
	// defaults.plugin.browser.
	defaultBrowser = "firefox"

	// captureName labels the single batch capture. RFC 0002 forbids
	// emitting the name into any blob; it only correlates
	// input/output.
	captureName = "cg"

	// batchSchema is the RFC 0002 subprocess input/output schema
	// token (the v1 fallback envelope).
	batchSchema = "capture-plugin/v1"

	// subcommandServe/subcommandBatch are appended to Spec.Command:
	// the RFC 0008 persistent session, always attempted first, and
	// the RFC 0002/0008 §Migration v1 fallback.
	subcommandServe = "capture-serve"
	subcommandBatch = "capture-batch"
)

// knownFormats is the RFC 0003 web-archive batch format vocabulary —
// what chrest's capture-batch accepts. Validated structurally before
// invoking the plugin binary so a typo fails fast rather than after a
// browser launch. Relocated from plugins/web verbatim; the plugin
// binary remains the authority on which it can actually produce.
var knownFormats = map[string]bool{
	"text":              true,
	"pdf":               true,
	"screenshot":        true,
	"mhtml":             true,
	"a11y":              true,
	"html-monolith":     true,
	"html-outer":        true,
	"markdown-full":     true,
	"markdown-reader":   true,
	"markdown-selector": true,
}

func validFormat(f string) bool { return knownFormats[f] }

// --- RFC 0002 subprocess form: capture-plugin/v1 JSON shapes (the v1
// fallback batch envelope) — relocated from plugins/web verbatim. ---

type batchInput struct {
	Schema   string         `json:"schema"`
	Writer   writerSpec     `json:"writer"`
	Target   string         `json:"target"`
	Defaults *batchDefaults `json:"defaults,omitempty"`
	Captures []captureSpec  `json:"captures"`
}

type writerSpec struct {
	Cmd []string `json:"cmd"`
}

type batchDefaults struct {
	Normalize *bool          `json:"normalize,omitempty"`
	Plugin    map[string]any `json:"plugin,omitempty"`
}

type captureSpec struct {
	Name    string         `json:"name"`
	Format  string         `json:"format"`
	Options map[string]any `json:"options,omitempty"`
}

type batchOutput struct {
	Schema   string          `json:"schema"`
	Plugin   pluginInfo      `json:"plugin"`
	Errors   []protocolError `json:"errors"`
	Captures []captureResult `json:"captures"`
}

type pluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type captureResult struct {
	Name    string         `json:"name"`
	Receipt *receiptRef    `json:"receipt,omitempty"`
	Error   *protocolError `json:"error,omitempty"`
}

type receiptRef struct {
	Id   string `json:"id"`
	Size int64  `json:"size"`
}

type protocolError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

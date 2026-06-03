package cutting_garden_plugin_web

const (
	// captureKind is the receipt kind tag: the receipt type-string is
	// cutting_garden-capture-receipt-web-v1, dispatched on for restore
	// and diff (RFC 0002 / RFC 0003).
	captureKind = "web"

	// capturerBinary is the external RFC 0003 web capturer, resolved on
	// PATH. chrest emits the merkle receipt tree itself (subprocess form).
	capturerBinary = "chrest"

	// batchSchema is the RFC 0002 subprocess input/output schema token.
	batchSchema = "capture-plugin/v1"

	// defaultFormat is captured when CUTTING_GARDEN_WEB_FORMAT is unset.
	// pdf is functional on the Firefox/BiDi backend and normalizable.
	defaultFormat = "pdf"

	// defaultBrowser is the plugin-namespaced browser default handed to
	// chrest via defaults.plugin.browser.
	defaultBrowser = "firefox"

	// captureName labels the single batch capture. RFC 0002 forbids
	// emitting the name into any blob; it only correlates input/output.
	captureName = "cg"

	// webFormatEnv overrides defaultFormat. The cutting-garden capture
	// command has no per-source options surface yet (FDR follow-up), so
	// format selection rides an environment variable for now.
	webFormatEnv = "CUTTING_GARDEN_WEB_FORMAT"
)

// knownFormats is the RFC 0003 web-archive batch format vocabulary —
// what chrest's capture-batch accepts. Validated structurally before
// invoking chrest so a typo fails fast rather than after a browser
// launch. chrest is the authority on which it can actually produce.
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

// --- RFC 0002 subprocess form: capture-plugin/v1 JSON shapes ---

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

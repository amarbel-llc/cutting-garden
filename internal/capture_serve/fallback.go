package capture_serve

import (
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/go-mcp/jsonrpc"
)

// BringUpError marks a launch that never reached a usable session:
// spawn failure, a child that exited or polluted stdout before
// announcing, a cookie/version/network mismatch, the announce deadline,
// or a failed dial. Run wraps every Launch failure in one so callers can
// distinguish "v2 is not available here" from a real capture failure.
type BringUpError struct {
	Err error
}

func (e *BringUpError) Error() string {
	return "capture-serve bring-up: " + e.Err.Error()
}

func (e *BringUpError) Unwrap() error { return e.Err }

// IsFallbackSignal reports whether err is one of the RFC 0008 §Migration
// v2→v1 fallback conditions: a bring-up failure (the plugin binary has
// no working capture-serve) or an initialize refusal carrying
// CodeUnsupportedVersion (it has one, but not this protocol version).
// Anything after a successful initialize — a batch error, a blob
// failure, a dropped session — is a REAL capture failure and MUST NOT
// silently retry on v1.
func IsFallbackSignal(err error) bool {
	var bringUp *BringUpError
	if errors.As(err, &bringUp) {
		return true
	}
	var jerr *jsonrpc.Error
	return errors.As(err, &jerr) && jerr.Code == CodeUnsupportedVersion
}

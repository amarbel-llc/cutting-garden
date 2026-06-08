package cutting_garden_plugin_git

import (
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// CaptureRoot is a vestigial EntryV1 entry point. Git capture always uses
// the RFC 0002 protocol path (CaptureProtocol, protocol.go); the capture
// orchestrator resolves the git plugin through the EntryV1 CapturePlugin
// registry and then type-asserts ProtocolCapturePlugin, so this method
// exists only to satisfy that registration and is never reached for the
// `git` scheme.
//
// Teaching the orchestrator to resolve protocol-only plugins (so this stub
// and the EntryV1 registration can be dropped) is tracked as a follow-up.
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	cutting_garden_plugins.ReporterOrNop(req.Reporter).Failure(req.RawArg,
		errors.ErrorWithStackf(
			"git plugin: capture uses the RFC 0002 protocol path, not the EntryV1 path",
		))
	return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
}

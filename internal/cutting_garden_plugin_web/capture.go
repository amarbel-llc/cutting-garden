package cutting_garden_plugin_web

import (
	"context"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// CaptureProtocol drives one RFC 0003 web capture: it resolves the target
// URL and format, then runs chrest (subprocess form) to assemble the
// receipt merkle tree, returning the root receipt's markl id.
func (Plugin) CaptureProtocol(
	req cutting_garden_plugins.ProtocolCaptureRequest,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	target, err := captureTarget(req.Source, req.RawArg)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	format := captureFormat()
	if !validFormat(format) {
		return cutting_garden_plugins.ProtocolCaptureResult{}, errors.BadRequestf(
			"web plugin: unknown capture format %q (set %s to a supported format)",
			format, webFormatEnv,
		)
	}

	receiptID, err := capture(req.Context, req.StoreName, target, format)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	return cutting_garden_plugins.ProtocolCaptureResult{
		ReceiptDigest: receiptID,
		ObjectCount:   1,
	}, nil
}

// capture runs a single-format chrest capture-batch and returns the
// resulting receipt's markl id. Shared by CaptureProtocol and the diff
// re-capture path.
func capture(
	ctx context.Context,
	storeName, target, format string,
) (string, error) {
	writerCmd, err := writerArgv(storeName)
	if err != nil {
		return "", err
	}

	normalize := true
	input := batchInput{
		Schema: batchSchema,
		Writer: writerSpec{Cmd: writerCmd},
		Target: target,
		Defaults: &batchDefaults{
			Normalize: &normalize,
			Plugin:    map[string]any{"browser": defaultBrowser},
		},
		Captures: []captureSpec{{Name: captureName, Format: format}},
	}

	out, err := runCaptureBatch(ctx, input)
	if err != nil {
		return "", err
	}

	if len(out.Errors) > 0 {
		return "", errors.ErrorWithStackf(
			"web plugin: chrest batch error (%s): %s",
			out.Errors[0].Kind, out.Errors[0].Message,
		)
	}
	if len(out.Captures) != 1 {
		return "", errors.ErrorWithStackf(
			"web plugin: chrest returned %d capture results, want 1", len(out.Captures),
		)
	}

	c := out.Captures[0]
	if c.Error != nil {
		return "", errors.ErrorWithStackf(
			"web plugin: capture failed (%s): %s", c.Error.Kind, c.Error.Message,
		)
	}
	if c.Receipt == nil || c.Receipt.Id == "" {
		return "", errors.ErrorWithStackf(
			"web plugin: chrest returned no receipt for capture %q", c.Name,
		)
	}

	return c.Receipt.Id, nil
}

// CaptureRoot is a vestigial EntryV1 entry point. Web capture always uses
// the RFC 0002 protocol path (CaptureProtocol); the orchestrator resolves
// the web plugin through the EntryV1 CapturePlugin registry and then
// type-asserts ProtocolCapturePlugin, so this exists only to satisfy that
// registration and is never reached for the `web` scheme. Its removal
// tracks the same protocol-only-resolution follow-up as the git binding.
func (Plugin) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	cutting_garden_plugins.ReporterOrNop(req.Reporter).Failure(req.RawArg,
		errors.ErrorWithStackf(
			"web plugin: capture uses the RFC 0002 protocol path, not the EntryV1 path",
		))
	return cutting_garden_plugins.CaptureRootResult{FailCount: 1}
}

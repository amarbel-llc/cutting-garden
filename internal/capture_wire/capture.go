package capture_wire

import (
	"context"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// CaptureProtocol drives one config-declared capture: it resolves the
// target URL and format, then runs the plugin binary (RFC 0008
// capture-serve first, capture-batch v1 fallback) to assemble the
// receipt merkle tree, returning the root receipt's markl id.
// Relocated from plugins/web's CaptureProtocol (cutting-garden#146
// slice 2 phase 2).
func (p *Plugin) CaptureProtocol(
	req cutting_garden_plugins.ProtocolCaptureRequest,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	target, err := p.captureTarget(req.Source, req.RawArg)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	format := p.captureFormat()
	if !validFormat(format) {
		return cutting_garden_plugins.ProtocolCaptureResult{}, errors.BadRequestf(
			"plugin %q: unknown capture format %q (set %s to a supported format)",
			p.spec.Name, format, p.formatEnvVar(),
		)
	}

	receiptID, err := p.capture(req.Context, req.BlobStore, req.StoreName, target, format)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	return cutting_garden_plugins.ProtocolCaptureResult{
		ReceiptDigest: receiptID,
		ObjectCount:   1,
	}, nil
}

// capture runs a single-format capture and returns the resulting
// receipt's markl id. Shared by CaptureProtocol and the diff
// re-capture path. The RFC 0008 persistent session ("capture-serve",
// v2) is ALWAYS attempted first; a bring-up failure or
// unsupported-version refusal — a plugin binary without a working
// capture-serve — falls back to the one-shot "capture-batch" (v1)
// below. Any failure after a successful v2 handshake is a real
// capture failure and does NOT retry on v1. Relocated from
// plugins/web's capture.
func (p *Plugin) capture(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	storeName, target, format string,
) (string, error) {
	receiptID, usedV2, err := p.captureServeV2(ctx, store, target, format)
	if err != nil {
		return "", err
	}
	if usedV2 {
		return receiptID, nil
	}

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

	out, err := p.runCaptureBatch(ctx, input)
	if err != nil {
		return "", err
	}

	if len(out.Errors) > 0 {
		return "", errors.ErrorWithStackf(
			"plugin %q: batch error (%s): %s",
			p.spec.Name, out.Errors[0].Kind, out.Errors[0].Message,
		)
	}
	if len(out.Captures) != 1 {
		return "", errors.ErrorWithStackf(
			"plugin %q: returned %d capture results, want 1",
			p.spec.Name, len(out.Captures),
		)
	}

	c := out.Captures[0]
	if c.Error != nil {
		return "", errors.ErrorWithStackf(
			"plugin %q: capture failed (%s): %s",
			p.spec.Name, c.Error.Kind, c.Error.Message,
		)
	}
	if c.Receipt == nil || c.Receipt.Id == "" {
		return "", errors.ErrorWithStackf(
			"plugin %q: returned no receipt for capture %q", p.spec.Name, c.Name,
		)
	}

	return c.Receipt.Id, nil
}

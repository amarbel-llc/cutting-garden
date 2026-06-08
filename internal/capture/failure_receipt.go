// Failure-receipt assembly and durability for the capture
// orchestrator: build one capture_failures.V1 per store group that had
// failures (or was aborted with the group active), write it into the
// group's blob store, and spill the wire bytes locally when the store
// write fails. Normative design:
// docs/plans/2026-06-07-capture-failure-receipt-design.md
// (§Write path, §Error handling).

package capture

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_failures"
	"github.com/amarbel-llc/cutting-garden/internal/capture_log"
	"github.com/amarbel-llc/madder/go/pkgs/domain_interfaces"
	"github.com/amarbel-llc/madder/go/pkgs/env_dir"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// buildFailureReceipt assembles the in-memory failure receipt for one
// store group. Outcome rules per the design doc: aborted wins over
// failures, and the signal name is recorded only when aborted.
// receiptID is the group's success-receipt id ("" when no EntryV1
// receipt was written or its write failed). Ts comes from
// capture_log.Timestamp so the receipt and its captures.log entry
// share a clock.
func buildFailureReceipt(
	groupRoots []string,
	captured int,
	failures []capture_failures.FailureV1,
	receiptID string,
	aborted bool,
	signalName string,
) *capture_failures.V1 {
	outcome := capture_failures.OutcomeFailures
	if aborted {
		outcome = capture_failures.OutcomeAborted
	} else {
		signalName = ""
	}

	return &capture_failures.V1{
		Meta: capture_failures.Meta{
			Ts:       capture_log.Timestamp(),
			Outcome:  outcome,
			Signal:   signalName,
			Receipt:  receiptID,
			Roots:    groupRoots,
			Captured: int64(captured),
			Failed:   int64(len(failures)),
		},
		Failures: failures,
	}
}

// signalCauseName extracts the signal name from the context's cancel
// cause when dewey's signal handler aborted the run — it cancels with
// errors.Signal{Signal: sig} (dewey internal/bravo/errors/context.go,
// exported as pkgs/errors.Signal). Returns "" when the context is
// live or was cancelled by something other than a signal.
func signalCauseName(ctx context.Context) string {
	var sig errors.Signal
	if errors.As(context.Cause(ctx), &sig) {
		return sig.Signal.String()
	}
	return ""
}

// writeFailureReceipt writes v into blobStore and returns its markl id.
// When the store write fails (a flaky store is often why entries
// failed in the first place), the same wire bytes spill to
// $XDG_STATE_HOME/cutting-garden/failures/<ts>.ndjson and spillPath is
// returned instead. err is non-nil only when BOTH the store write and
// the spill fail; callers degrade it to a notice — failure-receipt
// durability never alters the run's exit code (design §Error
// handling).
//
// blobStore is the minimal write surface (MakeBlobWriter) rather than
// the concrete store so tests can fail it with a small double.
func writeFailureReceipt(
	blobStore domain_interfaces.BlobWriterFactory,
	cgEnvDir env_dir.Env,
	v *capture_failures.V1,
) (id, spillPath string, err error) {
	var buf bytes.Buffer
	if _, err = capture_failures.WriteV1(&buf, v); err != nil {
		return "", "", errors.Wrapf(err, "encode failure receipt")
	}

	id, storeErr := writeFailureBlobBytes(blobStore, buf.Bytes())
	if storeErr == nil {
		return id, "", nil
	}

	spillPath, spillErr := spillFailureReceipt(cgEnvDir, v.Meta.Ts, buf.Bytes())
	if spillErr != nil {
		return "", "", errors.Errorf(
			"failure receipt store write failed (%v); local spill also failed (%v)",
			storeErr, spillErr,
		)
	}

	return "", spillPath, nil
}

// writeFailureBlobBytes streams data through a store blob writer and
// returns the content-addressed id. Open-codes
// capture_failures.WriteV1ToStore's write half so the store dependency
// stays the minimal BlobWriterFactory surface.
func writeFailureBlobBytes(
	blobStore domain_interfaces.BlobWriterFactory,
	data []byte,
) (id string, err error) {
	wc, err := blobStore.MakeBlobWriter(nil)
	if err != nil {
		return "", errors.Wrap(err)
	}
	defer errors.DeferredCloser(&err, wc)

	if _, err = wc.Write(data); err != nil {
		return "", errors.Wrap(err)
	}

	return wc.GetMarklId().String(), nil
}

// spillFailureReceipt writes the encoded receipt bytes to
// $XDG_STATE_HOME/cutting-garden/failures/<ts>.ndjson so triage
// information survives the store outage that caused it. The filename
// is Meta.Ts with ':' replaced by '-' (filesystem-safe RFC3339).
// Timestamps have one-second resolution, so two store groups spilling
// in the same second would collide on the name — O_EXCL plus a
// `-N` suffix retry keeps every group's spill intact instead of
// letting the second truncate the first.
func spillFailureReceipt(
	cgEnvDir env_dir.Env,
	ts string,
	data []byte,
) (string, error) {
	stem := strings.ReplaceAll(ts, ":", "-")
	dir := cgEnvDir.GetXDG().State.MakePath("failures").String()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", errors.Wrap(err)
	}

	// Bounded retry: same-second collisions come from store groups in
	// one invocation, so a handful of suffixes is plenty; bail rather
	// than loop forever on a pathological directory.
	const maxSpillSuffix = 100
	for i := 0; i <= maxSpillSuffix; i++ {
		name := stem
		if i > 0 {
			name += "-" + strconv.Itoa(i)
		}
		path := filepath.Join(dir, name+".ndjson")

		file, err := os.OpenFile(
			path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644,
		)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", errors.Wrap(err)
		}

		if _, werr := file.Write(data); werr != nil {
			_ = file.Close() //defer:err-checked — write error wins
			return "", errors.Wrap(werr)
		}
		if cerr := file.Close(); cerr != nil {
			return "", errors.Wrap(cerr)
		}
		return path, nil
	}

	return "", errors.ErrorWithStackf(
		"spill name collision not resolved after %d suffixes under %q",
		maxSpillSuffix, dir,
	)
}

package traversal_conformance

// This file is the driver's session harness: launch the peer over the
// transport's own bring-up half (LaunchWithoutInitialize — spawn,
// cookie, announce, dial, but NO initialize, because initialize is
// itself the first case under test), run the slice-1 case list in
// order, and emit TAP 14 through the bridged tap writer. Assertions
// live in cases.go; every one of them reads the RAW json.RawMessage
// response — never a value the WirePlugin adapter has normalized — so
// the driver observes exactly the bytes a peer put on the wire
// (cutting-garden#186; the by_container case is the one that forced
// this architecture, since the host repairs a non-conformant breakdown
// at the boundary per #173).

import (
	"context"
	"io"
	"slices"
	"time"

	tap "code.linenisgreat.com/tap/go/pkgs/writer"

	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

// runDeadline bounds the WHOLE conformance run. Every session.Call the
// cases make derives from ctx, so a peer that HANGS mid-method (rather
// than erroring — which the cases already surface) would otherwise hang
// the run forever; the reviewer flagged this for a tool an external CI
// invokes. The bound is generous: a real peer answers a fixed-tree
// method in milliseconds, and a caller wanting a shorter leash passes
// its own already-bounded ctx (this only ever tightens it). A blown
// deadline surfaces as a Call error on the in-flight case — a NotOk, so
// the failure is attributed to a point rather than a silent hang.
const runDeadline = 2 * time.Minute

// perCaseDeadline bounds a SINGLE case, derived from the run context at the
// top of each case method. It gives a hang PRECISE attribution
// (cutting-garden#189): a peer that stalls in one method blows this
// deadline and the failure lands on THAT case's point, rather than on
// whichever case happened to be in flight when the whole-run deadline
// (runDeadline, the outer backstop) blew. Generous — a real peer answers a
// fixed-tree method in milliseconds — so it never trips a conformant peer;
// it only converts a silent hang into an attributed NotOk.
const perCaseDeadline = 20 * time.Second

// Run launches manifest.Command as a traversal peer, drives the slice-1
// conformance cases against it, and writes TAP 14 to out. passed
// reports the conformance verdict — true iff every non-SKIP test point
// was ok; err is reserved for driver-side trouble (the peer could not
// even be launched), never for a peer failing a case.
func Run(
	ctx context.Context, manifest *Manifest, out io.Writer,
) (passed bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, runDeadline)
	defer cancel()

	tw := tap.NewWriter(out)

	session, err := traversal_serve.LaunchWithoutInitialize(
		ctx, manifest.Command,
	)
	if err != nil {
		tw.BailOut("launch %v: %s", manifest.Command, err)
		tw.Plan()

		return false, err
	}
	defer func() { _ = session.Close() }()

	r := &runner{tap: tw, session: session, manifest: manifest}

	if initErr := r.caseInitialize(ctx); initErr != nil {
		// No initialized session, no further cases: everything after
		// initialize would fail for the same reason, which is noise, not
		// signal. The failed initialize point is already on the stream;
		// bail out (TAP 14) instead of cascading. This is a CONFORMANCE
		// verdict (the peer refused the handshake), not driver trouble.
		tw.BailOut("initialize failed: %s", initErr)
		tw.Plan()

		return false, nil
	}

	r.caseErrorCodes(ctx)
	r.casePatchTriState(ctx)

	entries, filter, descendSkip := r.caseByContainer(ctx)
	r.caseDescendTargets(ctx, entries, filter, descendSkip)

	r.caseContainerBody(ctx)
	r.caseFilteredList(ctx)

	tw.Plan()

	return !tw.HasFailures(), nil
}

// runner is one conformance session's shared state: the TAP stream, the
// raw wire session, the manifest, and the DECODED initialize result
// kept solely for capability gating (the assertions read the raw
// bytes; the decoded form only answers "may the driver call this
// method at all" — RFC 0013 §Method set forbids calling unadvertised
// methods, so a read-only peer passes with the mutate cases SKIPped).
type runner struct {
	tap      *tap.Writer
	session  *traversal_serve.Session
	manifest *Manifest

	init traversal_serve.InitializeResult
}

// hasCapability reports whether the peer advertised the capability
// token in its initialize result.
func (r *runner) hasCapability(capability string) bool {
	return slices.Contains(r.init.Capabilities, capability)
}

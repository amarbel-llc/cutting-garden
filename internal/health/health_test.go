package health

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// ---------------------------------------------------------------------
// In-package fake plugins
//
// These tests probe the capability-detection logic, not any particular
// backend, so they register fakes rather than blank-importing plugins/ — an
// internal/ test importing plugins/ hands every framework package the plugin
// invalidation cone under godyn
// (docs/plans/2026-09-21-invalidation-cone-moves.md D6/D7, pinned by
// internal/sdklayering). That the REAL plugins are linked into the shipped
// binary and report the capabilities they claim is asserted end-to-end in
// zz-tests_bats/health.bats.
//
// The fakes cover every capability COLUMN probe() emits — capture, all three
// restore verdicts, diff, protocol kind, traversal types — and both scheme
// labels a row can carry. Two branches are deliberately NOT covered, neither
// of them a regression (the real-plugin test they replaced reached neither):
//
//   - health.go:157, protocol kind read from ProtocolDiffPlugin. It is the
//     else-arm of the ProtocolRestorePlugin check at :155, and fakeProtocol
//     satisfies ProtocolRestorePlugin, so it takes :155. Reaching :157 needs
//     a protocol-diff-only plugin; none exists in-tree either.
//   - health.go:184, displayName's "(schemeless)" fallback, which needs a
//     plugin whose every scheme is "". fakeFull claims "" alongside a named
//     scheme, so it takes the named-scheme path at :181.
// ---------------------------------------------------------------------

// fakeFull claims the schemeless default plus a named scheme, and
// implements capture + direct restore + diff + RootLister: the "yes"
// branch of every column, the "(default)" scheme label, and a non-empty
// traversal type list.
type fakeFull struct{}

func (fakeFull) Schemes() []string { return []string{"", "fakefull"} }
func (fakeFull) TypeTag() string   { return "cutting_garden-capture_receipt-fakefull-v1" }

func (fakeFull) ValidateSource(*url.URL, string) error { return nil }

func (fakeFull) CaptureRoot(
	cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

func (fakeFull) ValidateDest(*url.URL, string) error { return nil }

func (fakeFull) Restore(cutting_garden_plugins.RestoreRequest) error { return nil }

func (fakeFull) ValidateDiffDir(*url.URL, string) error { return nil }

func (fakeFull) ScanForDiff(
	cutting_garden_plugins.DiffScanRequest,
) ([]capture_receipt.EntryV1, error) {
	return nil, nil
}

func (fakeFull) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: "fakefull-container-v1", Container: true},
		{Tag: "fakefull-object-v1"},
	}
}

func (fakeFull) ListRoots(
	context.Context, *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	return nil, nil
}

// fakeProtocol implements capture + diff and restores through the RFC 0002
// capture protocol rather than directly: the "protocol" restore branch and
// the protocol-kind column.
type fakeProtocol struct{}

func (fakeProtocol) Schemes() []string { return []string{"fakeproto"} }
func (fakeProtocol) TypeTag() string   { return "cutting_garden-capture_receipt-fakeproto-v1" }

func (fakeProtocol) ValidateSource(*url.URL, string) error { return nil }

func (fakeProtocol) CaptureRoot(
	cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

func (fakeProtocol) ValidateDiffDir(*url.URL, string) error { return nil }

func (fakeProtocol) ScanForDiff(
	cutting_garden_plugins.DiffScanRequest,
) ([]capture_receipt.EntryV1, error) {
	return nil, nil
}

func (fakeProtocol) ProtocolKind() string { return "fakeproto" }

func (fakeProtocol) RestoreProtocol(
	cutting_garden_plugins.ProtocolRestoreRequest,
) error {
	return nil
}

// fakeCaptureOnly implements capture and nothing else: the "no" restore
// branch, no diff, no protocol, no traversal.
type fakeCaptureOnly struct{}

func (fakeCaptureOnly) Schemes() []string { return []string{"fakecaptureonly"} }
func (fakeCaptureOnly) TypeTag() string {
	return "cutting_garden-capture_receipt-fakecaptureonly-v1"
}

func (fakeCaptureOnly) ValidateSource(*url.URL, string) error { return nil }

func (fakeCaptureOnly) CaptureRoot(
	cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

// fakeSchemeOnly is registered via MustRegisterScheme alone — the
// traversal-only out-of-tree shape (RFC 0009 §3) health must still
// enumerate even though it implements no capture/restore/diff direction.
type fakeSchemeOnly struct{}

func (fakeSchemeOnly) Schemes() []string { return []string{"fakeschemeonly"} }
func (fakeSchemeOnly) TypeTag() string {
	return "cutting_garden-capture_receipt-fakeschemeonly-v1"
}

func init() {
	cutting_garden_plugins.MustRegisterCapture(fakeFull{})
	cutting_garden_plugins.MustRegisterRestore(fakeFull{})
	cutting_garden_plugins.MustRegisterDiff(fakeFull{})

	cutting_garden_plugins.MustRegisterCapture(fakeProtocol{})
	cutting_garden_plugins.MustRegisterDiff(fakeProtocol{})

	cutting_garden_plugins.MustRegisterCapture(fakeCaptureOnly{})

	cutting_garden_plugins.MustRegisterScheme(fakeSchemeOnly{})
}

// driveHealth dispatches the health subcommand through a fresh Utility
// (flag parsing included) with output routed to out, returning the exit
// code. Mirrors failures_test.driveFailures.
func driveHealth(t *testing.T, out io.Writer, args ...string) int {
	t.Helper()
	u := command.MakeUtility("cg-test", nil)
	u.AddCmd("health", newWithOutput(out))
	return u.Run(append([]string{"cg-test", "health"}, args...))
}

func rowsByName(rows []pluginRow) map[string]pluginRow {
	m := make(map[string]pluginRow, len(rows))
	for _, r := range rows {
		m[r.Plugin] = r
	}
	return m
}

func TestProbe_CapabilitiesPerPlugin(t *testing.T) {
	rows := rowsByName(collectRows())

	for _, name := range []string{
		"fakefull", "fakeproto", "fakecaptureonly", "fakeschemeonly",
	} {
		if _, ok := rows[name]; !ok {
			t.Fatalf("plugin %q not enumerated; got %v", name, keys(rows))
		}
	}

	// fakefull: full capture/restore/diff, no protocol, RootLister
	// traversal reporting its declared node types in order.
	if r := rows["fakefull"]; !r.Capture || r.Restore != "yes" || !r.Diff ||
		r.Protocol != "" ||
		strings.Join(r.Traversal, ",") != "fakefull-container-v1,fakefull-object-v1" {
		t.Errorf("fakefull row = %+v", r)
	}
	// displayName skips the empty schemeless claim and names the plugin
	// after its first non-empty scheme.
	if r := rows["fakefull"]; len(r.Schemes) != 2 || r.Schemes[0] != "" {
		t.Errorf("fakefull schemes = %v, want the schemeless claim first", r.Schemes)
	}
	// fakeproto: capture/diff, restore via the capture protocol, and the
	// protocol kind surfaced.
	if r := rows["fakeproto"]; !r.Capture || r.Restore != "protocol" || !r.Diff ||
		r.Protocol != "fakeproto" || len(r.Traversal) != 0 {
		t.Errorf("fakeproto row = %+v", r)
	}
	// fakecaptureonly: capture only — no restore, no diff, no protocol.
	if r := rows["fakecaptureonly"]; !r.Capture || r.Restore != "no" || r.Diff ||
		r.Protocol != "" || len(r.Traversal) != 0 {
		t.Errorf("fakecaptureonly row = %+v", r)
	}
	// fakeschemeonly: reachable only through the scheme registry, so every
	// capability column is negative but the plugin is still enumerated.
	if r := rows["fakeschemeonly"]; r.Capture || r.Restore != "no" || r.Diff ||
		r.Protocol != "" || len(r.Traversal) != 0 {
		t.Errorf("fakeschemeonly row = %+v", r)
	}
}

func TestRun_TextTable(t *testing.T) {
	var buf bytes.Buffer
	if code := driveHealth(t, &buf); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}
	out := buf.String()
	// "(default)" is fakefull's empty schemeless claim, rendered readably
	// instead of a bare leading comma.
	for _, want := range []string{
		"PLUGIN", "SCHEMES", "TRAVERSAL",
		"fakefull", "fakeproto", "(default)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestRun_JSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if code := driveHealth(t, &buf, "-format", "json"); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}

	var got []pluginRow
	dec := json.NewDecoder(&buf)
	for dec.More() {
		var r pluginRow
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode NDJSON: %v", err)
		}
		got = append(got, r)
	}
	rows := rowsByName(got)
	if r, ok := rows["fakefull"]; !ok || len(r.Traversal) != 2 {
		t.Errorf("fakefull json row = %+v (ok=%v)", r, ok)
	}
	if r, ok := rows["fakecaptureonly"]; !ok || r.Restore != "no" {
		t.Errorf("fakecaptureonly json row = %+v (ok=%v)", r, ok)
	}
	// omitempty keeps a capability-less plugin's optional columns absent
	// rather than emitting null arrays.
	if r, ok := rows["fakeschemeonly"]; !ok || r.Protocol != "" ||
		len(r.Traversal) != 0 {
		t.Errorf("fakeschemeonly json row = %+v (ok=%v)", r, ok)
	}
}

func TestRun_BadFormatIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	if code := driveHealth(t, &buf, "-format", "yaml"); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE)", code)
	}
	if buf.Len() != 0 {
		t.Errorf("bad-format run wrote output: %q", buf.String())
	}
}

func TestRun_TrailingArgIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	if code := driveHealth(t, &buf, "extra"); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE)", code)
	}
}

func keys(m map[string]pluginRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

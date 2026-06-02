package capture_plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
	"time"
)

// memWriter is a content-addressed in-memory Writer for tests: it
// computes a sha256 digest over each node, dedups identical content, and
// records every distinct node in write order.
type memWriter struct {
	order    []string
	byDigest map[string][]byte
}

func newMemWriter() *memWriter {
	return &memWriter{byDigest: map[string][]byte{}}
}

func (m *memWriter) WriteBlob(_ context.Context, r io.Reader) (string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(b)
	d := "sha256-" + hex.EncodeToString(sum[:])
	if _, ok := m.byDigest[d]; !ok {
		m.byDigest[d] = b
		m.order = append(m.order, d)
	}
	return d, int64(len(b)), nil
}

func (m *memWriter) node(digest string) (string, bool) {
	b, ok := m.byDigest[digest]
	return string(b), ok
}

// typeOf extracts the `! type` line value from a node's bytes.
func typeOf(node string) string {
	for _, line := range strings.Split(node, "\n") {
		if strings.HasPrefix(line, "! ") {
			return strings.TrimPrefix(line, "! ")
		}
	}
	return ""
}

// refsOf parses `- <alias> < @<digest> !<type>` lines into a map of
// alias→digest.
func refsOf(node string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(node, "\n") {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		rest := strings.TrimPrefix(line, "- ")
		alias, after, ok := strings.Cut(rest, " < @")
		if !ok {
			continue
		}
		digest, _, _ := strings.Cut(after, " !")
		out[alias] = digest
	}
	return out
}

func sampleParams(now time.Time) ReceiptParams {
	return ReceiptParams{
		Kind: "git",
		Invocation: Invocation{
			Target:    "https://example.com/r#main",
			Format:    "object-graph",
			Normalize: false,
			Options:   map[string]any{},
		},
		Host:   HostInfo{OS: "linux", Kernel: "6.0", Arch: "x86_64", Libc: "unknown"},
		Binary: BinaryInfo{Name: "cutting-garden", Version: "dev"},
		PluginEnv: PluginEnv{
			TypeString: "jcs-git-capture-environment-v1",
			Body:       map[string]any{"git_version": "git version 2.43.0"},
		},
		PayloadRefs: []Ref{
			{Alias: "payload", Digest: "sha256-payload", TypeString: "jcs-git-capture-payload-v1"},
		},
		Now: func() time.Time { return now },
	}
}

func TestWriteReceipt_TreeShape(t *testing.T) {
	w := newMemWriter()
	fixed := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

	receiptDigest, err := WriteReceipt(context.Background(), w, sampleParams(fixed))
	if err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}

	// 8 protocol nodes: invocation, host, binary, plugin-env,
	// environment, outcome, identity, receipt.
	if len(w.order) != 8 {
		t.Fatalf("wrote %d nodes, want 8: %v", len(w.order), nodeTypes(w))
	}

	receipt, ok := w.node(receiptDigest)
	if !ok {
		t.Fatalf("receipt digest %q not written", receiptDigest)
	}
	if typeOf(receipt) != "cutting_garden-capture-receipt-git-v1" {
		t.Errorf("receipt type = %q", typeOf(receipt))
	}

	rrefs := refsOf(receipt)
	for _, alias := range []string{"identity", "outcome", "payload"} {
		if rrefs[alias] == "" {
			t.Errorf("receipt missing %q ref", alias)
		}
	}
	if rrefs["payload"] != "sha256-payload" {
		t.Errorf("receipt payload ref = %q, want sha256-payload", rrefs["payload"])
	}

	// identity → invocation + environment.
	identity, _ := w.node(rrefs["identity"])
	if typeOf(identity) != TypeIdentity {
		t.Errorf("identity type = %q", typeOf(identity))
	}
	irefs := refsOf(identity)
	for _, alias := range []string{"invocation", "environment"} {
		if irefs[alias] == "" {
			t.Errorf("identity missing %q ref", alias)
		}
	}

	// environment → host + binary + plugin.
	env, _ := w.node(irefs["environment"])
	erefs := refsOf(env)
	for _, alias := range []string{"host", "binary", "plugin"} {
		if erefs[alias] == "" {
			t.Errorf("environment missing %q ref", alias)
		}
	}
}

func TestWriteReceipt_IdentityStableAcrossRuns(t *testing.T) {
	// Same params, different outcome datetimes → identity digest stable,
	// receipt digest per-run (changes with the datetime).
	w1 := newMemWriter()
	r1, err := WriteReceipt(context.Background(), w1, sampleParams(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	w2 := newMemWriter()
	r2, err := WriteReceipt(context.Background(), w2, sampleParams(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}

	id1 := refsOf(mustNode(t, w1, r1))["identity"]
	id2 := refsOf(mustNode(t, w2, r2))["identity"]
	if id1 != id2 {
		t.Errorf("identity digest changed across runs: %q vs %q", id1, id2)
	}
	if r1 == r2 {
		t.Errorf("receipt digest should differ across runs (per-run datetime), both %q", r1)
	}
}

func TestWriteReceipt_DedupSharedNodes(t *testing.T) {
	// Two captures with identical environment into the same writer share
	// host/binary/plugin/invocation/environment/identity nodes; only the
	// outcome and receipt differ per run.
	w := newMemWriter()
	if _, err := WriteReceipt(context.Background(), w, sampleParams(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteReceipt(context.Background(), w, sampleParams(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}

	// 8 nodes for run 1; run 2 adds only outcome + receipt (2 new) since
	// the other 6 dedup. 8 + 2 = 10.
	if len(w.order) != 10 {
		t.Errorf("deduped node count = %d, want 10: %v", len(w.order), nodeTypes(w))
	}
}

func mustNode(t *testing.T, w *memWriter, digest string) string {
	t.Helper()
	n, ok := w.node(digest)
	if !ok {
		t.Fatalf("node %q not found", digest)
	}
	return n
}

func nodeTypes(w *memWriter) []string {
	out := make([]string, 0, len(w.order))
	for _, d := range w.order {
		out = append(out, typeOf(string(w.byDigest[d])))
	}
	return out
}

package cutting_garden_plugin_git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
)

// memWriter is a content-addressed in-memory capture_plugin.Writer for
// tests: sha256 over each blob, dedups identical content, records write
// order.
type memWriter struct {
	order    []string
	byDigest map[string][]byte
}

var _ capture_plugin.Writer = (*memWriter)(nil)

func newMemWriter() *memWriter { return &memWriter{byDigest: map[string][]byte{}} }

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

func nodeTypeOf(node string) string {
	for _, line := range strings.Split(node, "\n") {
		if strings.HasPrefix(line, "! ") {
			return strings.TrimPrefix(line, "! ")
		}
	}
	return ""
}

func nodeRefs(node string) map[string]string {
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
		digest, typ, _ := strings.Cut(after, " !")
		// Drop the optional `@<sig>` type lock so assertions compare the
		// bare type-string.
		if t, _, ok := strings.Cut(typ, "@"); ok {
			typ = t
		}
		out[alias] = digest + "|" + typ
	}
	return out
}

func TestCaptureProtocol_EmitsReceiptTreeOverObjectGraph(t *testing.T) {
	withFakeGit(t)

	w := newMemWriter()
	res, err := captureProtocol(
		context.Background(), w,
		"https://github.com/amarbel-llc/cutting-garden", "main",
	)
	if err != nil {
		t.Fatalf("captureProtocol: %v", err)
	}
	if res.ReceiptDigest == "" {
		t.Fatal("empty receipt digest")
	}
	if res.ObjectCount != 3 {
		t.Fatalf("ObjectCount = %d, want 3", res.ObjectCount)
	}

	receipt := string(w.byDigest[res.ReceiptDigest])
	if got := nodeTypeOf(receipt); got != "cutting_garden-capture-receipt-git-v1" {
		t.Errorf("receipt type = %q", got)
	}

	// Receipt → payload node.
	payloadRef := nodeRefs(receipt)["payload"]
	payloadDigest, payloadType, _ := strings.Cut(payloadRef, "|")
	if payloadType != "jcs-git-capture-payload-v1" {
		t.Errorf("payload ref type = %q", payloadType)
	}
	payload := string(w.byDigest[payloadDigest])
	if got := nodeTypeOf(payload); got != "jcs-git-capture-payload-v1" {
		t.Fatalf("payload node type = %q", got)
	}

	// Payload node references the three git objects by oid, typed by git
	// object kind, and its body records the tip + object_count.
	prefs := nodeRefs(payload)
	wantObjs := map[string]string{
		"commit_oid_1": "git-capture-object-commit-v1",
		"tree_oid_1":   "git-capture-object-tree-v1",
		"blob_oid_1":   "git-capture-object-blob-v1",
	}
	for oid, wantType := range wantObjs {
		ref, ok := prefs[oid]
		if !ok {
			t.Errorf("payload missing object ref %q", oid)
			continue
		}
		objDigest, objType, _ := strings.Cut(ref, "|")
		if objType != wantType {
			t.Errorf("object %q ref type = %q, want %q", oid, objType, wantType)
		}
		if _, ok := w.byDigest[objDigest]; !ok {
			t.Errorf("object %q blob %q not stored", oid, objDigest)
		}
	}

	if !strings.Contains(payload, `"object_count":3`) {
		t.Errorf("payload body missing object_count:3:\n%s", payload)
	}
	if !strings.Contains(payload, `"tip":"commit_oid_1"`) {
		t.Errorf("payload body missing tip:\n%s", payload)
	}

	// The three raw git object blobs are stored verbatim (no hyphence
	// framing).
	wantBytes := map[string]string{
		"commit_oid_1": "commit-byte!",
		"tree_oid_1":   "tree-byte!",
		"blob_oid_1":   "hello!",
	}
	for oid, want := range wantBytes {
		ref := prefs[oid]
		objDigest, _, _ := strings.Cut(ref, "|")
		if got := string(w.byDigest[objDigest]); got != want {
			t.Errorf("object %q bytes = %q, want %q", oid, got, want)
		}
	}
}

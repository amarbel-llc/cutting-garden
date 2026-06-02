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
	// A single commit with one file: exactly three reachable objects —
	// one commit, one tree, one blob.
	dir, branch, tips := buildRepo(t, map[string]string{"hello.txt": "hello!"})
	tip := tips[0]

	w := newMemWriter()
	res, err := captureProtocol(context.Background(), w, dir, branch)
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

	// Payload node references one object of each git kind by oid; the body
	// records the real tip + object_count. Object oids are real git sha1s,
	// so assert by the distribution of leaf types and that every referenced
	// blob was stored.
	prefs := nodeRefs(payload)
	typeCounts := map[string]int{}
	for oid, ref := range prefs {
		objDigest, objType, _ := strings.Cut(ref, "|")
		typeCounts[objType]++
		if _, ok := w.byDigest[objDigest]; !ok {
			t.Errorf("object %q blob %q not stored", oid, objDigest)
		}
	}
	for _, want := range []string{
		"git-capture-object-commit-v1",
		"git-capture-object-tree-v1",
		"git-capture-object-blob-v1",
	} {
		if typeCounts[want] != 1 {
			t.Errorf("payload has %d refs of type %q, want 1", typeCounts[want], want)
		}
	}

	if !strings.Contains(payload, `"object_count":3`) {
		t.Errorf("payload body missing object_count:3:\n%s", payload)
	}
	if !strings.Contains(payload, `"tip":"`+tip+`"`) {
		t.Errorf("payload body missing tip %q:\n%s", tip, payload)
	}

	// The blob object's stored bytes are the raw file content verbatim (no
	// loose-object header, no hyphence framing).
	var blobBytesSeen bool
	for _, ref := range prefs {
		objDigest, objType, _ := strings.Cut(ref, "|")
		if objType != "git-capture-object-blob-v1" {
			continue
		}
		blobBytesSeen = true
		if got := string(w.byDigest[objDigest]); got != "hello!" {
			t.Errorf("blob bytes = %q, want %q", got, "hello!")
		}
	}
	if !blobBytesSeen {
		t.Error("no blob object referenced in payload")
	}
}

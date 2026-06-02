package cutting_garden_plugin_git

import (
	"bytes"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
)

func TestPlugin_Schemes_TypeTag(t *testing.T) {
	p := Plugin{}
	gotSchemes := p.Schemes()
	if len(gotSchemes) != 1 || gotSchemes[0] != "git" {
		t.Errorf("Schemes() = %v, want [git]", gotSchemes)
	}
	if p.TypeTag() != capture_receipt.TypeTagV1 {
		t.Errorf("TypeTag() = %q, want %q", p.TypeTag(), capture_receipt.TypeTagV1)
	}
}

func TestTailWriter_KeepsOnlyTail(t *testing.T) {
	var buf bytes.Buffer
	w := newTailWriter(&buf, 8)
	for _, chunk := range [][]byte{[]byte("aaaa"), []byte("bbbb"), []byte("cccc")} {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if got := buf.String(); got != "bbbbcccc" {
		t.Errorf("tail = %q, want %q", got, "bbbbcccc")
	}
}

package restore

import (
	"net/url"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/ids"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
)

// stubRestorePlugin returns a configurable TypeTag. Used to exercise
// the cross-scheme guard without registering a second real plugin.
type stubRestorePlugin struct {
	tag string
}

func (s stubRestorePlugin) Schemes() []string { return []string{"stub"} }
func (s stubRestorePlugin) TypeTag() string   { return s.tag }
func (stubRestorePlugin) ValidateDest(*url.URL, string) error {
	return nil
}
func (stubRestorePlugin) Restore(cutting_garden_plugins.RestoreRequest) error {
	return nil
}

func TestCheckReceiptTypeTag_AcceptsMatchingTag(t *testing.T) {
	var rid markl.Id
	tt := capture_receipt.TypeStructV1
	plugin := stubRestorePlugin{tag: tt.StringSansOp()}
	destURL, _ := url.Parse("stub://target")

	if err := checkReceiptTypeTag(&rid, tt, plugin, destURL); err != nil {
		t.Errorf("expected accept on matching tag, got error: %v", err)
	}
}

func TestCheckReceiptTypeTag_RefusesCrossScheme(t *testing.T) {
	var rid markl.Id
	receiptTT := capture_receipt.TypeStructV1
	plugin := stubRestorePlugin{tag: "cutting_garden-capture_receipt-s3-v1"}
	destURL, _ := url.Parse("stub://bucket/key")

	err := checkReceiptTypeTag(&rid, receiptTT, plugin, destURL)
	if err == nil {
		t.Fatal("expected refusal on cross-scheme, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"type-tag",
		"cross-scheme restore is not supported",
		"cutting-garden#18",
		receiptTT.StringSansOp(),
		plugin.tag,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q; got: %s", want, msg)
		}
	}
}

func TestCheckReceiptTypeTag_RefusesUnknownReceiptTag(t *testing.T) {
	// A forged receipt could carry an arbitrary type-tag; the guard
	// refuses anything the resolved plugin does not declare.
	var rid markl.Id
	forged := ids.MustTypeStruct("bogus-type-v1")
	plugin := stubRestorePlugin{tag: capture_receipt.TypeTagV1}
	destURL, _ := url.Parse("out")

	if err := checkReceiptTypeTag(&rid, forged, plugin, destURL); err == nil {
		t.Fatal("expected refusal on unknown receipt tag, got nil")
	}
}

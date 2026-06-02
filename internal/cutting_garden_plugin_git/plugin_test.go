package cutting_garden_plugin_git

import (
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

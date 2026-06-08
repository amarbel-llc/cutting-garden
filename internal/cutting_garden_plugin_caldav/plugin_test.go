package cutting_garden_plugin_caldav

import (
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
)

func TestPlugin_Schemes_TypeTag(t *testing.T) {
	p := Plugin{}
	schemes := p.Schemes()
	if len(schemes) != 1 || schemes[0] != schemeCalDAV {
		t.Errorf("Schemes() = %v, want [%s]", schemes, schemeCalDAV)
	}
	if p.TypeTag() != capture_receipt.TypeTagV1 {
		t.Errorf("TypeTag() = %q, want %q", p.TypeTag(), capture_receipt.TypeTagV1)
	}
}

func TestPlugin_Validate(t *testing.T) {
	p := Plugin{}

	good := mustParseURL(t, "caldav://host/dav/")
	if err := p.ValidateSource(good, "caldav://host/dav/"); err != nil {
		t.Errorf("ValidateSource(good): %v", err)
	}
	if err := p.ValidateDest(good, "caldav://host/dav/"); err != nil {
		t.Errorf("ValidateDest(good): %v", err)
	}
	if err := p.ValidateDiffDir(good, "caldav://host/dav/"); err != nil {
		t.Errorf("ValidateDiffDir(good): %v", err)
	}

	bad := mustParseURL(t, "caldav:ftp://host/x")
	if err := p.ValidateSource(bad, "caldav:ftp://host/x"); err == nil {
		t.Errorf("ValidateSource(bad) = nil, want error")
	}
}

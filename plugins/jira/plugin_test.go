package jira

import (
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_receipt"
)

func TestPlugin_Schemes_TypeTag(t *testing.T) {
	p := Plugin{}
	schemes := p.Schemes()
	if len(schemes) != 1 || schemes[0] != schemeJira {
		t.Errorf("Schemes() = %v, want [%s]", schemes, schemeJira)
	}
	if p.TypeTag() != capture_receipt.TypeTagV1 {
		t.Errorf("TypeTag() = %q, want %q", p.TypeTag(), capture_receipt.TypeTagV1)
	}
}

func TestPlugin_Validate(t *testing.T) {
	p := Plugin{}

	good := mustParseURL(t, "jira://acme.atlassian.net/PROJ")
	if err := p.ValidateSource(good, "jira://acme.atlassian.net/PROJ"); err != nil {
		t.Errorf("ValidateSource(good): %v", err)
	}
	if err := p.ValidateDiffDir(good, "jira://acme.atlassian.net/PROJ"); err != nil {
		t.Errorf("ValidateDiffDir(good): %v", err)
	}

	bad := mustParseURL(t, "jira:ftp://host/x")
	if err := p.ValidateSource(bad, "jira:ftp://host/x"); err == nil {
		t.Errorf("ValidateSource(bad) = nil, want error")
	}
}

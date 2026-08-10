package fastmail

import "testing"

func TestPlugin_Schemes_TypeTag(t *testing.T) {
	p := Plugin{}
	schemes := p.Schemes()
	if len(schemes) != 1 || schemes[0] != schemeFastmail {
		t.Errorf("Schemes() = %v, want [%s]", schemes, schemeFastmail)
	}
	if p.TypeTag() != typeTagCaptureReceipt {
		t.Errorf("TypeTag() = %q, want %q", p.TypeTag(), typeTagCaptureReceipt)
	}
}

func TestPlugin_Types(t *testing.T) {
	byTag := map[string]bool{}
	container := map[string]bool{}
	mime := map[string]string{}
	for _, nt := range (Plugin{}).Types() {
		byTag[nt.Tag] = true
		container[nt.Tag] = nt.Container
		mime[nt.Tag] = nt.MimeType
	}
	for _, tag := range []string{typeMailbox, typeThread, typeEmail, typeEmailRaw} {
		if !byTag[tag] {
			t.Errorf("Types() missing %q", tag)
		}
	}
	if !container[typeMailbox] || !container[typeThread] || !container[typeEmail] {
		t.Error("mailbox/thread/email must be containers")
	}
	if container[typeEmailRaw] {
		t.Error("email-raw must be a leaf")
	}
	if mime[typeEmailRaw] != mimeRFC822 {
		t.Errorf("email-raw mime = %q, want %q", mime[typeEmailRaw], mimeRFC822)
	}
}

func TestPlugin_Validate(t *testing.T) {
	setAccounts(t, acct("personal", "FASTMAIL_X"))
	p := Plugin{}

	good := mustParseURL(t, "fastmail://personal/area/finance/")
	if err := p.Validate(good, good.String()); err != nil {
		t.Errorf("Validate(good): %v", err)
	}

	badScheme := mustParseURL(t, "caldav://personal/")
	if err := p.Validate(badScheme, badScheme.String()); err == nil {
		t.Error("Validate(bad scheme) = nil, want error")
	}

	unknownAccount := mustParseURL(t, "fastmail://nope/")
	if err := p.Validate(unknownAccount, unknownAccount.String()); err == nil {
		t.Error("Validate(unknown account) = nil, want error")
	}
}

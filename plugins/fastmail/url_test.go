package fastmail

import (
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func TestClassifyURI_Kinds(t *testing.T) {
	cases := []struct {
		raw      string
		kind     nodeKind
		mailbox  []string
		threadID string
		emailID  string
	}{
		{"fastmail://personal/", kindAccountRoot, nil, "", ""},
		{"fastmail://personal", kindAccountRoot, nil, "", ""},
		{"fastmail://personal/area/finance/", kindMailbox, []string{"area", "finance"}, "", ""},
		{"fastmail://personal/area/finance/receipts/?thread=T1", kindThread, []string{"area", "finance", "receipts"}, "T1", ""},
		{"fastmail://personal/area/?thread=T1&email=e9", kindEmail, []string{"area"}, "T1", "e9"},
		{"fastmail://personal/area/?thread=T1&email=e9&raw=1", kindRaw, []string{"area"}, "T1", "e9"},
	}
	for _, tc := range cases {
		ref, err := classifyURI(mustParseURL(t, tc.raw))
		if err != nil {
			t.Errorf("classify %q: %v", tc.raw, err)
			continue
		}
		if ref.kind != tc.kind {
			t.Errorf("classify %q: kind = %d, want %d", tc.raw, ref.kind, tc.kind)
		}
		if ref.account != "personal" {
			t.Errorf("classify %q: account = %q", tc.raw, ref.account)
		}
		if !equalStrings(ref.mailboxPath, tc.mailbox) {
			t.Errorf("classify %q: mailbox = %v, want %v", tc.raw, ref.mailboxPath, tc.mailbox)
		}
		if ref.threadID != tc.threadID || ref.emailID != tc.emailID {
			t.Errorf("classify %q: thread/email = %q/%q, want %q/%q",
				tc.raw, ref.threadID, ref.emailID, tc.threadID, tc.emailID)
		}
	}
}

func TestClassifyURI_Rejections(t *testing.T) {
	for _, raw := range []string{
		"caldav://personal/",       // wrong scheme
		"fastmail:///area/",        // empty account
		"fastmail://p/a/?raw=1",    // raw without thread+email
		"fastmail://p/a/?email=e9", // email without thread
	} {
		_, err := classifyURI(mustParseURL(t, raw))
		if err == nil {
			t.Errorf("classify %q = nil error, want rejection", raw)
			continue
		}
		if !errors.Is400BadRequest(err) {
			t.Errorf("classify %q: not a bad request: %v", raw, err)
		}
	}
}

func TestURIMinting_RoundTrips(t *testing.T) {
	account := "personal"
	mailbox := []string{"area", "finance", "receipts"}

	// mailbox
	ref, err := classifyURI(mailboxURI(account, mailbox))
	if err != nil || ref.kind != kindMailbox || !equalStrings(ref.mailboxPath, mailbox) {
		t.Fatalf("mailbox round-trip: kind=%d path=%v err=%v", ref.kind, ref.mailboxPath, err)
	}

	// thread
	ref, err = classifyURI(threadURI(account, mailbox, "T5"))
	if err != nil || ref.kind != kindThread || ref.threadID != "T5" {
		t.Fatalf("thread round-trip: kind=%d thread=%q err=%v", ref.kind, ref.threadID, err)
	}

	// email
	ref, err = classifyURI(emailURI(account, mailbox, "T5", "e9"))
	if err != nil || ref.kind != kindEmail || ref.threadID != "T5" || ref.emailID != "e9" {
		t.Fatalf("email round-trip: %+v err=%v", ref, err)
	}

	// raw
	ref, err = classifyURI(rawURI(account, mailbox, "T5", "e9"))
	if err != nil || ref.kind != kindRaw || ref.emailID != "e9" {
		t.Fatalf("raw round-trip: %+v err=%v", ref, err)
	}
}

func TestURIMinting_EscapesSegments(t *testing.T) {
	// A mailbox name with a space must round-trip as a single segment.
	mailbox := []string{"area", "big money"}
	ref, err := classifyURI(mailboxURI("personal", mailbox))
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(ref.mailboxPath, mailbox) {
		t.Errorf("escaped round-trip: got %v, want %v", ref.mailboxPath, mailbox)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

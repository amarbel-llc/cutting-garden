package fastmail

import (
	"context"
	"strings"
	"testing"
)

func TestReadLeaf_Email_StructuredAndRaw(t *testing.T) {
	account := newFixture(t)
	mailbox := []string{"area", "finance", "receipts"}

	content, ok, err := (Plugin{}).ReadLeaf(
		context.Background(), emailURI(account, mailbox, "T1", "e1"),
	)
	if err != nil {
		t.Fatalf("ReadLeaf(email): %v", err)
	}
	if !ok {
		t.Fatal("ReadLeaf(email) ok=false, want true")
	}
	email, isEmail := content.Structured.(Email)
	if !isEmail {
		t.Fatalf("Structured is %T, want Email", content.Structured)
	}
	if email.ID != "e1" || email.ThreadID != "T1" {
		t.Errorf("structured email = %q/%q, want e1/T1", email.ID, email.ThreadID)
	}
	if content.RawMimeType != mimeRFC822 {
		t.Errorf("RawMimeType = %q, want %q", content.RawMimeType, mimeRFC822)
	}
	if !strings.Contains(string(content.Raw), "July total") {
		t.Errorf("raw body missing expected content: %q", content.Raw)
	}
}

func TestReadLeaf_Raw_BytesOnly(t *testing.T) {
	account := newFixture(t)
	mailbox := []string{"area", "finance", "receipts"}

	content, ok, err := (Plugin{}).ReadLeaf(
		context.Background(), rawURI(account, mailbox, "T1", "e1"),
	)
	if err != nil {
		t.Fatalf("ReadLeaf(raw): %v", err)
	}
	if !ok {
		t.Fatal("ReadLeaf(raw) ok=false, want true")
	}
	if content.Structured != nil {
		t.Errorf("raw node Structured = %v, want nil", content.Structured)
	}
	if !strings.Contains(string(content.Raw), "July total") {
		t.Errorf("raw body missing expected content: %q", content.Raw)
	}
}

func TestReadLeaf_NonLeaf(t *testing.T) {
	account := newFixture(t)
	// A mailbox is not a readable leaf.
	_, ok, err := (Plugin{}).ReadLeaf(context.Background(), receiptsURI(account))
	if err != nil {
		t.Fatalf("ReadLeaf(mailbox): %v", err)
	}
	if ok {
		t.Error("ReadLeaf(mailbox) ok=true, want false")
	}
}

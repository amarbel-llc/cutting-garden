package fastmail

import (
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/plugins/fastmail/fastmailtestserver"
)

// testAccount is the synthetic account name used across the tests; it
// doubles as the URI host slot (fastmail://personal/).
const testAccount = "personal"

// testTokenEnv is the env var the synthetic account resolves its bearer
// token from.
const testTokenEnv = "FASTMAIL_TEST_TOKEN"

// mustParseURL parses raw or fails the test.
func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// setAccounts installs the given accounts as package state for the test and
// restores none afterward.
func setAccounts(t *testing.T, accts ...config_common.Account) {
	t.Helper()
	SetConfiguredAccounts(accts)
	t.Cleanup(func() { SetConfiguredAccounts(nil) })
}

// acct builds a synthetic fastmail account.
func acct(name, pwEnv string) config_common.Account {
	return config_common.Account{
		Root:        config_common.Root{Name: name, URL: "fastmail://" + name + "/"},
		PasswordEnv: pwEnv,
	}
}

// newFixture starts an in-memory JMAP server, wires the plugin at it via a
// single configured account, seeds a small synthetic tree, and returns the
// account name. The tree:
//
//	Inbox   (role inbox)     Archive (role archive)   Drafts (role drafts, EXCLUDED)
//	area/                    payee/
//	  finance/                 acme/
//	    receipts/            <- two threads seeded here
//
// receipts holds thread T1 ("Your July receipt", read, inbox, attachment)
// and thread T2 ("June receipt", unread+flagged, archive, no attachment,
// senders acme+bob).
// startWired starts an in-memory JMAP server and wires the plugin at it via
// a single configured account named testAccount — but seeds nothing. It
// returns the server so a test can seed its own tree.
func startWired(t *testing.T) *fastmailtestserver.Server {
	t.Helper()
	srv := fastmailtestserver.Start("acct-1")
	t.Cleanup(srv.Close)

	prev := resolveSessionURL
	resolveSessionURL = func(string) string { return srv.SessionURL() }
	t.Cleanup(func() { resolveSessionURL = prev })

	t.Setenv(testTokenEnv, "secret-token")
	setAccounts(t, acct(testAccount, testTokenEnv))
	return srv
}

func newFixture(t *testing.T) string {
	t.Helper()
	srv := startWired(t)

	// Mailboxes.
	srv.AddMailbox("mb-inbox", "Inbox", "", "inbox")
	srv.AddMailbox("mb-archive", "Archive", "", "archive")
	srv.AddMailbox("mb-drafts", "Drafts", "", "drafts") // excluded from the tree
	srv.AddMailbox("mb-area", "area", "", "")
	srv.AddMailbox("mb-finance", "finance", "mb-area", "")
	srv.AddMailbox("mb-receipts", "receipts", "mb-finance", "")
	srv.AddMailbox("mb-payee", "payee", "", "")
	srv.AddMailbox("mb-acme", "acme", "mb-payee", "")

	acmeFrom := []fastmailtestserver.Address{{Name: "Acme Billing", Email: "billing@acme.example"}}
	bobFrom := []fastmailtestserver.Address{{Name: "Bob", Email: "bob@example.test"}}

	// Thread T1: one read message with an attachment, in inbox + receipts.
	srv.AddEmail(fastmailtestserver.Email{
		ID: "e1", ThreadID: "T1",
		MailboxIDs: []string{"mb-receipts", "mb-inbox"},
		Keywords:   []string{"$seen"},
		From:       acmeFrom,
		Subject:    "Your July receipt", ReceivedAt: "2026-07-14T09:12:03Z",
		HasAttachment: true, BlobID: "blob-e1",
		Raw: "From: billing@acme.example\r\nSubject: Your July receipt\r\n\r\nJuly total: 42.00\r\n",
	})

	// Thread T2: two messages — one unread (in archive), one flagged.
	srv.AddEmail(fastmailtestserver.Email{
		ID: "e2", ThreadID: "T2",
		MailboxIDs: []string{"mb-receipts", "mb-archive"},
		Keywords:   nil, // unread
		From:       acmeFrom,
		Subject:    "June receipt", ReceivedAt: "2026-06-02T08:00:00Z",
		BlobID: "blob-e2",
		Raw:    "From: billing@acme.example\r\nSubject: June receipt\r\n\r\nJune total: 19.00\r\n",
	})
	srv.AddEmail(fastmailtestserver.Email{
		ID: "e3", ThreadID: "T2",
		MailboxIDs: []string{"mb-receipts"},
		Keywords:   []string{"$seen", "$flagged"},
		From:       bobFrom,
		Subject:    "June receipt", ReceivedAt: "2026-06-03T09:00:00Z",
		BlobID: "blob-e3",
		Raw:    "From: bob@example.test\r\nSubject: June receipt\r\n\r\nthanks\r\n",
	})

	return testAccount
}

// receiptsURI is the mailbox URI for area/finance/receipts.
func receiptsURI(account string) *url.URL {
	return mailboxURI(account, []string{"area", "finance", "receipts"})
}

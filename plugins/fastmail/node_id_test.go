package fastmail

import "testing"

func TestRelativeNodeID(t *testing.T) {
	inboxT1 := threadURI("test", []string{"Inbox"}, "T1").String()
	cases := []struct {
		name   string
		node   string
		anchor string
		want   string
		wantOK bool
	}{
		{"thread directly under the anchor mailbox", inboxT1, "fastmail://test/Inbox/", "T1", true},
		{"slash-less anchor spelling", inboxT1, "fastmail://test/Inbox", "T1", true},
		{"slash-less node spelling", "fastmail://test/Inbox?thread=T1", "fastmail://test/Inbox/", "T1", true},
		{"account-root anchor keeps the mailbox path", inboxT1, "fastmail://test/", "Inbox/T1", true},
		{
			"nested mailbox under an ancestor anchor",
			threadURI("test", []string{"area", "finance", "receipts"}, "T5").String(),
			"fastmail://test/area/", "finance/receipts/T5", true,
		},
		{
			"a mailbox name with a space stays readable",
			threadURI("test", []string{"big money"}, "T1").String(),
			"fastmail://test/", "big money/T1", true,
		},
		{
			"a `/` inside one mailbox name is escaped so segments stay unambiguous",
			threadURI("test", []string{"a/b"}, "T1").String(),
			"fastmail://test/", "a%2Fb/T1", true,
		},
		{"other account", inboxT1, "fastmail://personal/Inbox/", "", false},
		{"sibling mailbox is not a prefix", inboxT1, "fastmail://test/Archive/", "", false},
		{"segment-wise, not string-wise, prefix", inboxT1, "fastmail://test/In/", "", false},
		{"deeper anchor than the node", inboxT1, "fastmail://test/Inbox/sub/", "", false},
		{"a thread anchor is not a mailbox", inboxT1, inboxT1, "", false},
		{"mailbox node", mailboxURI("test", []string{"Inbox", "sub"}).String(), "fastmail://test/Inbox/", "", false},
		{"email node", emailURI("test", []string{"Inbox"}, "T1", "e1").String(), "fastmail://test/Inbox/", "", false},
		{"raw node", rawURI("test", []string{"Inbox"}, "T1", "e1").String(), "fastmail://test/Inbox/", "", false},
		{"foreign scheme node", "caldav://test/Inbox/x.ics", "fastmail://test/Inbox/", "", false},
		{"unparseable anchor", inboxT1, "%zz", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Plugin{}.RelativeNodeID(tc.node, tc.anchor)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("RelativeNodeID(%q, %q) = (%q, %v), want (%q, %v)",
					tc.node, tc.anchor, got, ok, tc.want, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			// ResolveNodeID inverts it (cutting-garden#315).
			resolved, ok := Plugin{}.ResolveNodeID(tc.want, tc.anchor)
			if !ok {
				t.Fatalf("ResolveNodeID(%q, %q) declined", tc.want, tc.anchor)
			}
			if back, _ := (Plugin{}).RelativeNodeID(resolved, tc.anchor); back != tc.want {
				t.Errorf("ResolveNodeID(%q) = %q, which ids as %q", tc.want, resolved, back)
			}
		})
	}
}

func TestResolveNodeID_Declines(t *testing.T) {
	for name, tc := range map[string]struct{ id, anchor string }{
		"empty thread":        {"Inbox/", "fastmail://test/"},
		"thread anchor":       {"T1", threadURI("test", []string{"Inbox"}, "T1").String()},
		"bad escape":          {"a%zz/T1", "fastmail://test/"},
		"not a fastmail root": {"T1", "caldav://test/Inbox/"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := (Plugin{}).ResolveNodeID(tc.id, tc.anchor); ok {
				t.Errorf("resolved to %q", got)
			}
		})
	}
}

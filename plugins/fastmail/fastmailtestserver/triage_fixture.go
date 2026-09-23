package fastmailtestserver

// SeedTriageFixture seeds the deterministic, PII-free inbox-triage fixture
// the fastmail organize bats lane (zz-tests_bats/organize_fastmail.bats,
// fastmail tags slice 1) renders and edits. Every id is stable and readable
// so the lane's whole-document vectors can name them.
//
// Mailboxes (id  name  parent  role) — the four role mailboxes plus the
// dodder-hyphen label tree (FDR 0024, D1: a label's tag joins its `-`
// continuations from the NEAREST bare ancestor; the `_` root never renders):
//
//	mb-inbox        Inbox                  -               inbox
//	mb-archive      Archive                -               archive
//	mb-sent         Sent                   -               sent
//	mb-trash        Trash                  -               trash
//	mb-underscore   _                      -               -   (tag: none)
//	mb-req-others   req-others             mb-underscore   -   (tag: req-others)
//	mb-area         area                   -               -
//	mb-career       -career                mb-area         -
//	mb-resume       -resume                mb-career       -   (tag: area-career-resume)
//	mb-proj-x       proj-x                 mb-career       -
//	mb-msft         -msft                  mb-proj-x       -   (tag: proj-x-msft)
//	mb-travel       -travel                mb-area         -
//	mb-yoga         proj-trips-26-09-yoga  mb-travel       -   (tag: proj-trips-26-09-yoga)
//	mb-payee        payee                  -               -
//	mb-one_medical  -one_medical           mb-payee        -   (tag: payee-one_medical)
//	mb-zz-archive   zz-archive             -               -
//	mb-zz-proj      proj                   mb-zz-archive   -
//	mb-24-t         -24-t                  mb-zz-proj      -
//	mb-10x          -10x                   mb-24-t         -   (tag: proj-24-t-10x)
//
// Threads (newest receivedAt first within the Inbox):
//
//	T1  M1   2026-09-20T09:00:00Z  inbox + one_medical        unseen
//	T2  M2a  2026-09-15T10:00:00Z  inbox + msft               seen
//	    M2b  2026-09-16T11:00:00Z  sent                       seen
//	    M2c  2026-09-17T12:00:00Z  req-others + msft          seen   (union case)
//	T3  M3   2026-09-10T08:00:00Z  inbox                      seen + flagged (no label)
//	T4  M4   2026-08-28T14:00:00Z  inbox + 10x                seen
//	T5  M5   2026-09-05T16:00:00Z  archive + resume           seen   (NOT in the inbox)
func SeedTriageFixture(s *Server) {
	for _, m := range []Mailbox{
		{ID: "mb-inbox", Name: "Inbox", Role: "inbox"},
		{ID: "mb-archive", Name: "Archive", Role: "archive"},
		{ID: "mb-sent", Name: "Sent", Role: "sent"},
		{ID: "mb-trash", Name: "Trash", Role: "trash"},
		{ID: "mb-underscore", Name: "_"},
		{ID: "mb-req-others", Name: "req-others", ParentID: "mb-underscore"},
		{ID: "mb-area", Name: "area"},
		{ID: "mb-career", Name: "-career", ParentID: "mb-area"},
		{ID: "mb-resume", Name: "-resume", ParentID: "mb-career"},
		{ID: "mb-proj-x", Name: "proj-x", ParentID: "mb-career"},
		{ID: "mb-msft", Name: "-msft", ParentID: "mb-proj-x"},
		{ID: "mb-travel", Name: "-travel", ParentID: "mb-area"},
		{ID: "mb-yoga", Name: "proj-trips-26-09-yoga", ParentID: "mb-travel"},
		{ID: "mb-payee", Name: "payee"},
		{ID: "mb-one_medical", Name: "-one_medical", ParentID: "mb-payee"},
		{ID: "mb-zz-archive", Name: "zz-archive"},
		{ID: "mb-zz-proj", Name: "proj", ParentID: "mb-zz-archive"},
		{ID: "mb-24-t", Name: "-24-t", ParentID: "mb-zz-proj"},
		{ID: "mb-10x", Name: "-10x", ParentID: "mb-24-t"},
	} {
		s.AddMailbox(m.ID, m.Name, m.ParentID, m.Role)
	}

	for _, e := range []Email{
		triageEmail("M1", "T1", "billing@example.com", "Your statement is ready",
			"2026-09-20T09:00:00Z", []string{"mb-inbox", "mb-one_medical"}),
		triageEmail("M2a", "T2", "recruiter@example.com", "Interview loop",
			"2026-09-15T10:00:00Z", []string{"mb-inbox", "mb-msft"}, "$seen"),
		triageEmail("M2b", "T2", "me@example.com", "Re: Interview loop",
			"2026-09-16T11:00:00Z", []string{"mb-sent"}, "$seen"),
		triageEmail("M2c", "T2", "recruiter@example.com", "Re: Interview loop",
			"2026-09-17T12:00:00Z", []string{"mb-req-others", "mb-msft"}, "$seen"),
		triageEmail("M3", "T3", "friend@example.com", "Lunch next week?",
			"2026-09-10T08:00:00Z", []string{"mb-inbox"}, "$seen", "$flagged"),
		triageEmail("M4", "T4", "team@example.com", "Project wrap-up",
			"2026-08-28T14:00:00Z", []string{"mb-inbox", "mb-10x"}, "$seen"),
		triageEmail("M5", "T5", "careers@example.com", "Resume received",
			"2026-09-05T16:00:00Z", []string{"mb-archive", "mb-resume"}, "$seen"),
	} {
		s.AddEmail(e)
	}
}

// triageEmail builds one fixture message with a matching minimal RFC 5322
// body served under blob-<id>.
func triageEmail(
	id, threadID, from, subject, receivedAt string,
	mailboxIDs []string, keywords ...string,
) Email {
	return Email{
		ID:         id,
		ThreadID:   threadID,
		MailboxIDs: mailboxIDs,
		Keywords:   keywords,
		From:       []Address{{Email: from}},
		Subject:    subject,
		ReceivedAt: receivedAt,
		BlobID:     "blob-" + id,
		Raw: "From: " + from + "\r\nSubject: " + subject + "\r\nDate: " +
			receivedAt + "\r\n\r\nfixture body\r\n",
	}
}

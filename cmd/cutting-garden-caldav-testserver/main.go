// Command cutting-garden-caldav-testserver is a test-only in-memory CalDAV
// server for the bats caldav lane. It is the standalone, coproc-spawned
// form of plugins/caldav/caldavtestserver: a localhost HTTP server speaking
// just enough CalDAV (PROPFIND/REPORT/GET/PUT) for the plugin's
// capture/restore/diff round-trip.
//
// It exists because Radicale cannot start under the nix bats sandbox (it
// calls socket.socketpair(AF_UNIX); see amarbel-llc/dodder#117). This
// server is a pure net/http TCP listener, so it runs in-sandbox. It is NOT
// shipped in the cutting-garden release.
//
// Protocol (mirrors cutting-garden-test-git-sshd's coproc contract): on
// startup it seeds a fixed set of VTODO/VEVENT resources, prints one
// handshake line to stdout —
//
//	<caldav-source-arg> <calendar-path>
//
// — where <caldav-source-arg> is the opaque `caldav:<http-url>` argument
// pointing at the seeded calendar, then serves until its stdin is closed
// (the shutdown signal the bats helper sends).
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"code.linenisgreat.com/cutting-garden/plugins/caldav/caldavtestserver"
)

func main() {
	// CG_TEST_CALDAV_PORT pins the listen port (else an ephemeral one). The
	// organize bats lanes set it so the server URL — which reaches the organize
	// document's `_anchor`/provenance lines and thus its `_base` digest — is
	// stable enough for whole-document vectors.
	var addr string
	if port := os.Getenv("CG_TEST_CALDAV_PORT"); port != "" {
		addr = "127.0.0.1:" + port
	}
	srv := caldavtestserver.StartAt("/dav/cal/", addr)

	// Seed a deterministic fixture set: two VTODOs and one VEVENT under the
	// advertised calendar. The bats lane asserts capture/restore/diff
	// against exactly these.
	srv.Seed("/dav/cal/task1.ics", "VTODO", vtodo("task1", "Buy milk"))
	srv.Seed("/dav/cal/task2.ics", "VTODO", vtodo("task2", "Walk dog"))
	srv.Seed("/dav/cal/event1.ics", "VEVENT", vevent("event1", "Standup"))

	// A second calendar under the SAME calendar-home ("/dav/"), so a
	// PROPFIND at $CALDAV_SOURCE (the home, not a specific calendar)
	// discovers TWO distinctly-named children — the cutting-garden#162
	// principal/calendar-home discovery scenario, exercised end-to-end by
	// zz-tests_bats/caldav.bats against the real binary rather than only
	// the in-process Go fakes.
	srv.AddCalendar("/dav/work/", "Work")
	srv.Seed("/dav/work/task3.ics", "VTODO", vtodo("task3", "Submit report"))

	// A third calendar dedicated to the organize date reschedule-by-move lanes
	// (zz-tests_bats/organize_date.bats, FDR 0023 Slice 2b): VTODOs with
	// clock-bearing, TZID-stamped DUE dates in DISTINCT months, so grouping by
	// `--group-by date_due=(month)` yields separate buckets and moving one between
	// them exercises the FacetWriteApplier date splice (day + clock + zone
	// preserved). OPT-IN via CG_TEST_CALDAV_SCHED so it never inflates the
	// home-capture object count the caldav.bats / discovery fixtures assert
	// against — only the date lanes, which set the env var before spawning the
	// server, see this calendar.
	if os.Getenv("CG_TEST_CALDAV_SCHED") != "" {
		srv.AddCalendar("/dav/sched/", "Schedule")
		srv.Seed("/dav/sched/sched1.ics", "VTODO",
			vtodoDue("sched1", "Book flights", "20260815T143000", "America/Los_Angeles"))
		srv.Seed("/dav/sched/sched2.ics", "VTODO",
			vtodoDue("sched2", "Renew passport", "20260910T163000", "America/Los_Angeles"))
	}

	// A fourth calendar dedicated to the organize field-write lanes (FDR 0025
	// Slice 1 Phase 0 conformance net): VTODOs carrying banded PRIORITY, LOCATION,
	// and STATUS so grouping by `priority` yields all four bands and each object's
	// box surfaces editable location/status/priority atoms plus the SUMMARY
	// trailer. Two of them additionally carry CATEGORIES so the tag
	// dimension (RFC 0019) has multi-membership signal: field2 is
	// a two-tag task (work + errand) and field3 a one-tag task (work), so grouping
	// by `categories` files field2 under BOTH `## =work` and `## =errand` while
	// field3 lands under `## =work` only, and the categories histogram reads
	// work=2, errand=1. (Since native tags slice 2 CATEGORIES also renders as
	// key-free box tag atoms, so the priority/field-edit lanes' whole-document
	// vectors pin `errand work` in field2's box.) OPT-IN
	// via CG_TEST_CALDAV_FIELDS so it never inflates the home-capture object count
	// caldav.bats asserts against — only the priority, field-edit, and categories
	// lanes, which set the env var, see this calendar.
	//
	// field5 is the designated MISSING-STATUS object (native tags slice 1.5 C):
	// no STATUS, no PRIORITY, no LOCATION, no CATEGORIES — under a
	// `--group-by status=` document it sits ungrouped above the first heading
	// with no status atom in its box, and the RFC 0015 write:one rule keeps an
	// un-bucketed placement a no-op while a move into a bucket ASSIGNS the
	// property.
	//
	//   field1  Pay rent      PRIORITY 1 (0_must)        LOCATION Bank  STATUS NEEDS-ACTION
	//   field2  Read book     PRIORITY 5 (1_should)      CATEGORIES work,errand
	//   field3  Water plants  PRIORITY 9 (2_nice)        CATEGORIES work
	//   field4  Someday idea  no PRIORITY (3_unspecified)
	//   field5  Waiting idea  no PRIORITY (3_unspecified)  no STATUS
	if os.Getenv("CG_TEST_CALDAV_FIELDS") != "" {
		srv.AddCalendar("/dav/fields/", "Fields")
		srv.Seed("/dav/fields/field1.ics", "VTODO",
			vtodoRich("field1", "Pay rent", 1, "Bank", "NEEDS-ACTION", ""))
		srv.Seed("/dav/fields/field2.ics", "VTODO",
			vtodoRich("field2", "Read book", 5, "", "", "work,errand"))
		srv.Seed("/dav/fields/field3.ics", "VTODO",
			vtodoRich("field3", "Water plants", 9, "", "", "work"))
		srv.Seed("/dav/fields/field4.ics", "VTODO",
			vtodoRich("field4", "Someday idea", 0, "", "", ""))
		srv.Seed("/dav/fields/field5.ics", "VTODO",
			vtodoRich("field5", "Waiting idea", 0, "", "", ""))
	}

	// A fifth calendar dedicated to the organize NAMESPACE-ROLLUP grouping lane
	// (RFC 0019 tags slice 3 B5, cutting-garden#231): VTODOs whose CATEGORIES form a
	// `project-*` hyphen hierarchy, so `--group-by project` under the dodder-hyphen
	// interpreter rolls each task up to its immediate segment — `# -client`
	// (nsA + nsB) and `# -cutting_garden` (nsC) — while a task tagged outside the
	// namespace (nsD, `other`) lands ungrouped, above the first bucket heading.
	// Moving a task between rollup buckets rewrites the reconstructed namespace tag
	// through the CATEGORIES full-set write (the naive interpreter rejects a
	// namespace grouping, so the lane sets [tags] interpreter = dodder-hyphen).
	// OPT-IN via CG_TEST_CALDAV_NS so it never inflates the home-capture object
	// count caldav.bats asserts against — only the namespace lane, which sets the
	// env var, sees this calendar.
	//
	//   nsA  Acme retainer   CATEGORIES project-client-acme     → -client
	//   nsB  Baxter audit     CATEGORIES project-client-baxter   → -client
	//   nsC  CG roadmap       CATEGORIES project-cutting_garden  → -cutting_garden
	//   nsD  Loose idea       CATEGORIES other                    → ungrouped
	if os.Getenv("CG_TEST_CALDAV_NS") != "" {
		srv.AddCalendar("/dav/ns/", "Namespaces")
		srv.Seed("/dav/ns/nsA.ics", "VTODO",
			vtodoRich("nsA", "Acme retainer", 0, "", "", "project-client-acme"))
		srv.Seed("/dav/ns/nsB.ics", "VTODO",
			vtodoRich("nsB", "Baxter audit", 0, "", "", "project-client-baxter"))
		srv.Seed("/dav/ns/nsC.ics", "VTODO",
			vtodoRich("nsC", "CG roadmap", 0, "", "", "project-cutting_garden"))
		srv.Seed("/dav/ns/nsD.ics", "VTODO",
			vtodoRich("nsD", "Loose idea", 0, "", "", "other"))
	}

	// A sixth calendar dedicated to the organize BOX-LITERAL lane
	// (zz-tests_bats/organize_literal.bats, native tags slice 1 G9/G13): a task
	// whose CATEGORIES value carries whitespace, so its `# "_ inbox"` bucket
	// heading MUST quote (trellis String) and the quoted spelling round-trips
	// through a bucket move; and a task with a LOCATION atom for the hand-edited
	// bare-token / non-ground box refusals. OPT-IN via CG_TEST_CALDAV_LIT so it
	// never inflates the shared fixtures' object counts.
	//
	// lit3 carries RFC 5545 §3.3.11 TEXT escaping ON THE WIRE (native tags
	// slice 1.5 F, seeded raw — vtodoRich passes the value through verbatim):
	// `SUMMARY:Plan\, then do` and `CATEGORIES:planning\, misc` (ONE category
	// containing a literal comma). The ical layer unescapes on parse, so
	// organize renders the trailer `Plan, then do` (no backslash) and the ONE
	// quoted tag heading `# "planning, misc"`; a write escapes back.
	//
	//   lit1  Triage inbox    CATEGORIES _ inbox
	//   lit2  Read book       LOCATION Bank
	//   lit3  Plan\, then do  CATEGORIES planning\, misc
	if os.Getenv("CG_TEST_CALDAV_LIT") != "" {
		srv.AddCalendar("/dav/lit/", "Literal")
		srv.Seed("/dav/lit/lit1.ics", "VTODO",
			vtodoRich("lit1", "Triage inbox", 0, "", "", "_ inbox"))
		srv.Seed("/dav/lit/lit2.ics", "VTODO",
			vtodoRich("lit2", "Read book", 0, "Bank", "", ""))
		srv.Seed("/dav/lit/lit3.ics", "VTODO",
			vtodoRich("lit3", `Plan\, then do`, 0, "", "", `planning\, misc`))
	}

	// Handshake: the caldav: source arg (opaque form reaches the plain-HTTP
	// test server) and the calendar path.
	fmt.Printf("caldav:%s/dav/ %s\n", srv.URL(), srv.CalendarPath)

	// Block until stdin closes — the coproc shutdown signal.
	_, _ = io.Copy(io.Discard, os.Stdin)

	srv.Close()
}

func vtodo(uid, summary string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:" + uid +
		"\nSUMMARY:" + summary + "\nEND:VTODO\nEND:VCALENDAR\n"
}

func vevent(uid, summary string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:" + uid +
		"\nSUMMARY:" + summary + "\nEND:VEVENT\nEND:VCALENDAR\n"
}

// vtodoRich seeds a VTODO carrying the optional detail properties the organize
// field-edit and priority lanes exercise: a banded PRIORITY (priority <= 0 omits
// the property, leaving the task unprioritized → the 3_unspecified band), a
// LOCATION, a STATUS, and a raw comma-separated CATEGORIES (empty omits the
// property → the untagged case). It is the richest VTODO shape the fixtures need
// — organize surfaces priority/location/status as editable box atoms with SUMMARY
// as the trailer, while CATEGORIES feeds the tag dimension (RFC 0019)
// and never renders as an atom.
func vtodoRich(uid, summary string, priority int, location, status, categories string) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:" + uid +
		"\nSUMMARY:" + summary + "\n")
	if priority > 0 {
		fmt.Fprintf(&b, "PRIORITY:%d\n", priority)
	}
	if location != "" {
		b.WriteString("LOCATION:" + location + "\n")
	}
	if status != "" {
		b.WriteString("STATUS:" + status + "\n")
	}
	if categories != "" {
		b.WriteString("CATEGORIES:" + categories + "\n")
	}
	b.WriteString("END:VTODO\nEND:VCALENDAR\n")
	return b.String()
}

// vtodoDue seeds a VTODO whose DUE carries a clock time and (when tzid is
// non-empty) a TZID parameter — the shape the month reschedule lane needs to
// prove day+clock+zone are preserved across a bucket move.
func vtodoDue(uid, summary, due, tzid string) string {
	dueProp := "DUE"
	if tzid != "" {
		dueProp += ";TZID=" + tzid
	}
	dueProp += ":" + due
	return "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:" + uid +
		"\nSUMMARY:" + summary + "\n" + dueProp + "\nEND:VTODO\nEND:VCALENDAR\n"
}

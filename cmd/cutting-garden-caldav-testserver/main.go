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

	"code.linenisgreat.com/cutting-garden/plugins/caldav/caldavtestserver"
)

func main() {
	srv := caldavtestserver.Start("/dav/cal/")

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

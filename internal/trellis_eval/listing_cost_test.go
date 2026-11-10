package trellis_eval

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
)

// countingCalendar is a caldav-shaped stand-in that counts the listing calls
// a query makes (cutting-garden#398). One calendar holds n task leaves; like
// caldav, ListEnriched serves the calendar's objects with their summary
// inline and declines (ok == false) anywhere else, and ListRoots of a task
// returns nothing. Against the real plugin every one of these calls is a
// PROPFIND plus a credential lookup, so the count is the request bill.
type countingCalendar struct {
	n             int
	enrichedCalls int
	rootCalls     int
}

var (
	_ cgp.RootLister     = (*countingCalendar)(nil)
	_ cgp.EnrichedLister = (*countingCalendar)(nil)
)

const countingCalendarURI = "cnt://home/cal/"

func (*countingCalendar) Schemes() []string { return []string{"cnt"} }
func (*countingCalendar) TypeTag() string   { return "cnt-v1" }

func (*countingCalendar) Types() []cgp.NodeType {
	return []cgp.NodeType{
		{Tag: "calendar-v1", Container: true},
		{Tag: "todo-v1", Container: false, MimeType: "text/calendar"},
	}
}

func (c *countingCalendar) tasks() []cgp.Node {
	nodes := make([]cgp.Node, 0, c.n)
	for i := range c.n {
		nodes = append(nodes, cgp.Node{
			URI:    urlp(fmt.Sprintf("%st%03d.ics", countingCalendarURI, i)),
			Type:   "todo-v1",
			Fields: map[string]any{"summary": fmt.Sprintf("task %03d", i)},
		})
	}
	return nodes
}

func (c *countingCalendar) ListRoots(_ context.Context, u *url.URL) ([]cgp.Node, error) {
	c.rootCalls++
	if u.String() == countingCalendarURI {
		return c.tasks(), nil
	}
	return nil, nil
}

func (c *countingCalendar) ListEnriched(
	_ context.Context, u *url.URL, _ cgp.FacetFilter,
) ([]cgp.Node, bool, error) {
	c.enrichedCalls++
	if u.String() != countingCalendarURI {
		return nil, false, nil
	}
	return c.tasks(), true, nil
}

// TestEvaluate_ClosureDoesNotListLeaves pins cutting-garden#398: a forward
// closure over a calendar lists the calendar once and never asks a declared
// leaf (Container: false, NodeType's contract: no children) for children.
// Before the fix the 300-task query below made 301 ListEnriched and 300
// ListRoots calls, so against caldav it cost ~600 PROPFINDs and as many
// credential lookups.
func TestEvaluate_ClosureDoesNotListLeaves(t *testing.T) {
	cal := &countingCalendar{n: 300}
	q, err := trellis.Parse(`->> summary~="task 04."`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	nodes, err := Evaluate(context.Background(), q, urlp(countingCalendarURI), cal)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(nodes) != 10 {
		t.Errorf("matched %d nodes, want 10 (task 040..049)", len(nodes))
	}
	if cal.enrichedCalls != 1 || cal.rootCalls != 0 {
		t.Errorf("listing calls: ListEnriched=%d ListRoots=%d, want 1 and 0",
			cal.enrichedCalls, cal.rootCalls)
	}
}

// TestEvaluate_DescendDoesNotListLeaves is the one-hop twin: `-> ->` from the
// calendar's tasks must not list each task either.
func TestEvaluate_DescendDoesNotListLeaves(t *testing.T) {
	cal := &countingCalendar{n: 300}
	q, err := trellis.Parse(`!todo-v1 -> :`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	nodes, err := Evaluate(context.Background(), q, urlp(countingCalendarURI), cal)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("matched %d nodes, want 0 (tasks have no children)", len(nodes))
	}
	if cal.enrichedCalls != 1 || cal.rootCalls != 0 {
		t.Errorf("listing calls: ListEnriched=%d ListRoots=%d, want 1 and 0",
			cal.enrichedCalls, cal.rootCalls)
	}
}

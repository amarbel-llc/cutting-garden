package caldav

import (
	"context"
	"strings"
	"testing"
)

const detailTodo = "BEGIN:VCALENDAR\r\nBEGIN:VTODO\r\nUID:t1\r\nSUMMARY:Buy milk\r\nSTATUS:NEEDS-ACTION\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"

func seedDetailTodo(f *fakeCalDAV) {
	f.resources["/dav/cal/t1.ics"] = detailTodo
	f.component["/dav/cal/t1.ics"] = "VTODO"
}

// A delete names what it removed: the object's summary, from one GET before
// the DELETE.
func TestDeleteNodeDetail_NamesTheObjectBySummary(t *testing.T) {
	f, home := startFakeEmpty(t)
	seedDetailTodo(f)

	d, err := Plugin{}.DeleteNodeDetail(context.Background(),
		mustParseURL(t, objectArg(home, "/dav/cal/t1.ics")), nil)
	if err != nil || d.NotFound || d.Name != "Buy milk" {
		t.Fatalf("detail=%+v err=%v", d, err)
	}
	if _, ok := f.resources["/dav/cal/t1.ics"]; ok {
		t.Error("the object was not deleted")
	}
}

// The GET is best effort: when it fails the delete still happens and the name
// falls back to the object's filename.
func TestDeleteNodeDetail_AFailedGetDoesNotBlockTheDelete(t *testing.T) {
	f, home := startFakeEmpty(t)
	seedDetailTodo(f)
	f.failGet = true

	d, err := Plugin{}.DeleteNodeDetail(context.Background(),
		mustParseURL(t, objectArg(home, "/dav/cal/t1.ics")), nil)
	if err != nil || d.NotFound || d.Name != "t1.ics" {
		t.Fatalf("detail=%+v err=%v", d, err)
	}
	if _, ok := f.resources["/dav/cal/t1.ics"]; ok {
		t.Error("a failed GET blocked the delete")
	}
}

// A delete of an object that is not there is not_found, not a bare failure.
func TestDeleteNodeDetail_AMissingObjectIsNotFound(t *testing.T) {
	_, home := startFakeEmpty(t)

	d, err := Plugin{}.DeleteNodeDetail(context.Background(),
		mustParseURL(t, objectArg(home, "/dav/cal/nope.ics")), nil)
	if err == nil || !d.NotFound || !strings.Contains(err.Error(), "404") {
		t.Errorf("detail=%+v err=%v", d, err)
	}
	if d.Name != "nope.ics" {
		t.Errorf("name = %q, want the filename", d.Name)
	}
}

// DeleteNode is unchanged for its existing callers: a missing object is still
// an error, an existing one still goes.
func TestDeleteNode_StillStrict(t *testing.T) {
	f, home := startFakeEmpty(t)
	seedDetailTodo(f)
	node := mustParseURL(t, objectArg(home, "/dav/cal/t1.ics"))
	if err := (Plugin{}).DeleteNode(context.Background(), node); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := (Plugin{}).DeleteNode(context.Background(), node); err == nil {
		t.Error("deleting it again must fail")
	}
}

// A delete op that carries a body is still refused by caldav's delete (it
// takes no options): the structural detail interface must not let one slip
// through.
func TestDeleteNodeDetail_RefusesABody(t *testing.T) {
	f, home := startFakeEmpty(t)
	seedDetailTodo(f)

	_, err := Plugin{}.DeleteNodeDetail(context.Background(),
		mustParseURL(t, objectArg(home, "/dav/cal/t1.ics")), strings.NewReader(`{"subtree": true}`))
	if err == nil || !strings.Contains(err.Error(), "takes no body") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := f.resources["/dav/cal/t1.ics"]; !ok {
		t.Error("a refused delete removed the object")
	}
}

package caldav

import (
	"context"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// A bare field object is a patch: the component is the stored object's, so the
// wrapper is redundant. This is the reported `{"categories":[...]}` body.
func TestPatchNode_BareFieldObjectTakesTheNodesComponent(t *testing.T) {
	f, home := startFake(t) // task1.ics is a VTODO
	arg := objectArg(home, "/dav/cal/task1.ics")

	applied, err := Plugin{}.PatchNode(
		context.Background(), mustParseURL(t, arg),
		strings.NewReader(`{"categories":["a","b"],"summary":"Bare patched"}`),
	)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if !slices.Equal(applied, []string{"categories", "summary"}) {
		t.Errorf("applied = %#v, want [categories summary]", applied)
	}
	got := f.resources["/dav/cal/task1.ics"]
	if !strings.Contains(got, "CATEGORIES:a,b") || !strings.Contains(got, "SUMMARY:Bare patched") {
		t.Errorf("bare patch not applied:\n%s", got)
	}
	if !strings.Contains(got, "UID:task1") {
		t.Errorf("an untouched field was lost:\n%s", got)
	}
}

// A bare patch behaves like the wrapped one in every other respect: an unknown
// field is tolerated and left out of applied, null clears, a body naming
// nothing recognized stores nothing, and the component comes from the stored
// object (a VEVENT here takes event fields).
func TestPatchNode_BareFieldObjectSharesTheWrappedSemantics(t *testing.T) {
	f, home := startFake(t)
	f.seed("/dav/cal/ev.ics", "VEVENT", vevent("ev", "Standup"))
	ev := mustParseURL(t, objectArg(home, "/dav/cal/ev.ics"))
	task := mustParseURL(t, objectArg(home, "/dav/cal/task1.ics"))
	ctx := context.Background()

	applied, err := Plugin{}.PatchNode(ctx, task, strings.NewReader(`{"summary":"ok","color":"red"}`))
	if err != nil || !slices.Equal(applied, []string{"summary"}) {
		t.Errorf("unknown field: applied = %#v, err %v; want [summary]", applied, err)
	}

	before := f.resources["/dav/cal/task1.ics"]
	applied, err = Plugin{}.PatchNode(ctx, task, strings.NewReader(`{"color":"red"}`))
	if err != nil || applied == nil || len(applied) != 0 {
		t.Errorf("nothing recognized: applied = %#v, err %v; want a non-nil empty list", applied, err)
	}
	if f.resources["/dav/cal/task1.ics"] != before {
		t.Errorf("nothing recognized must store nothing")
	}

	applied, err = Plugin{}.PatchNode(ctx, ev, strings.NewReader(`{"location":"HQ","description":null}`))
	if err != nil || !slices.Equal(applied, []string{"description", "location"}) {
		t.Errorf("event bare patch: applied = %#v, err %v", applied, err)
	}
	if got := f.resources["/dav/cal/ev.ics"]; !strings.Contains(got, "LOCATION:HQ") {
		t.Errorf("event not patched:\n%s", got)
	}

	applied, err = Plugin{}.PatchNode(ctx, task, strings.NewReader(`{}`))
	if err != nil || applied == nil || len(applied) != 0 {
		t.Errorf("empty bare object: applied = %#v, err %v; want a non-nil empty list", applied, err)
	}
}

// A top-level `component` keeps the wrapped form, with its no-GET shortcut: a
// wrapped body naming nothing recognized never touches the server, so it
// succeeds even against a node that does not exist.
func TestPatchNode_WrappedFormKeepsItsNoFetchShortcut(t *testing.T) {
	_, home := startFakeEmpty(t)
	applied, err := Plugin{}.PatchNode(
		context.Background(), mustParseURL(t, objectArg(home, "/dav/cal/ghost.ics")),
		strings.NewReader(`{"component":"VTODO","task":{"color":"red"}}`),
	)
	if err != nil || applied == nil || len(applied) != 0 {
		t.Errorf("wrapped, nothing recognized: applied = %#v, err %v; want no error and no fetch", applied, err)
	}
}

// A bare body does need the node, so a missing node is the usual error.
func TestPatchNode_BareFieldObjectOnAMissingNodeErrors(t *testing.T) {
	_, home := startFakeEmpty(t)
	if _, err := (Plugin{}).PatchNode(
		context.Background(), mustParseURL(t, objectArg(home, "/dav/cal/ghost.ics")),
		strings.NewReader(`{"summary":"x"}`),
	); err == nil {
		t.Fatal("a bare patch of a missing node must error")
	}
}

// The wrapped shape is named in the error when a wrapper is half there: a
// section object without `component`, and a component that is not one of the
// three, both say what the wrapped form is.
func TestPatchNode_HalfWrappedBodiesNameTheWrappedShape(t *testing.T) {
	f, home := startFake(t)
	arg := mustParseURL(t, objectArg(home, "/dav/cal/task1.ics"))
	before := f.resources["/dav/cal/task1.ics"]

	for name, tc := range map[string]struct{ body, want string }{
		"section without component": {`{"task":{"summary":"x"}}`, `"component"`},
		"unknown component":         {`{"component":"VFOO","task":{"summary":"x"}}`, `"component"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Plugin{}.PatchNode(context.Background(), arg, strings.NewReader(tc.body))
			if err == nil || !errors.Is400BadRequest(err) {
				t.Fatalf("PatchNode = %v; want a bad request", err)
			}
			for _, frag := range []string{tc.want, `{"component":"VTODO","task":{`, "bare field object"} {
				if !strings.Contains(err.Error(), frag) {
					t.Errorf("error %q lacks %q", err, frag)
				}
			}
		})
	}
	if f.resources["/dav/cal/task1.ics"] != before {
		t.Errorf("a refused patch must store nothing")
	}
}

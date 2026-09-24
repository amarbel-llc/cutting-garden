package organize

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const refusalAnchor = "caldav://h/c/"

func refusalMove(t *testing.T, id, typ, from, to string) move {
	t.Helper()
	uri := refusalAnchor + id
	return move{URI: uri, From: from, To: to, Node: cgp.Node{URI: mustURL(t, uri), Type: typ}}
}

// refusalWrites: status is a non-clearable one write, milestone a clearable
// one, component read-only.
func refusalWrites() map[string]cgp.FacetWrite {
	return map[string]cgp.FacetWrite{
		"task":  {DimensionKey: "status", Mode: cgp.FacetWriteOne, Field: "status"},
		"issue": {DimensionKey: "milestone", Mode: cgp.FacetWriteOne, Field: "milestone", Clearable: true},
		"event": {DimensionKey: "component", Mode: cgp.FacetWriteNone},
	}
}

// TestPartitionRefusedMoves pins the partition: ONLY a non-clearable one
// dimension moved into the no-value section is a separable refusal; a normal
// move, a clearable clear, and a read-only move (a NON-separable refusal that
// still aborts through checkMoveWritable) are kept.
func TestPartitionRefusedMoves(t *testing.T) {
	moves := []move{
		refusalMove(t, "t1.ics", "task", "open", "done"),
		refusalMove(t, "t2.ics", "task", "open", ""),
		refusalMove(t, "i3", "issue", "v0.1", ""),
		refusalMove(t, "e4.ics", "event", "VEVENT", "VTODO"),
	}
	kept, refused := partitionRefusedMoves(moves, refusalWrites(), boxIDsFor(nil, refusalAnchor))

	var keptIDs []string
	for _, mv := range kept {
		keptIDs = append(keptIDs, mv.URI)
	}
	if want := []string{moves[0].URI, moves[2].URI, moves[3].URI}; !reflect.DeepEqual(keptIDs, want) {
		t.Errorf("kept = %v, want %v", keptIDs, want)
	}
	if len(refused) != 1 {
		t.Fatalf("refused = %+v, want exactly t2.ics", refused)
	}
	r := refused[0]
	if r.ObjectID != "t2.ics" || r.Field != "status" || r.Kind != "move" ||
		r.Reason != `moved into the no-value section, but "status" can't be cleared` {
		t.Errorf("refusal = %+v", r)
	}
	if r.Err == nil || !errors.Is400BadRequest(r.Err) ||
		!strings.Contains(r.Err.Error(), `dimension "status" cannot be cleared`) {
		t.Errorf("refusal Err = %v, want the headless cannot-be-cleared bad request", r.Err)
	}
}

func oneRefusal(t *testing.T) []refusal {
	t.Helper()
	_, refused := partitionRefusedMoves(
		[]move{refusalMove(t, "task2.ics", "task", "open", "")},
		refusalWrites(), boxIDsFor(nil, refusalAnchor),
	)
	return refused
}

// TestResolveRefusals pins the gate: headless refuses without prompting
// (exit 64, nothing written); at a terminal the refusals are listed and the
// strip prompt asked — declining aborts with the same error, accepting
// continues.
func TestResolveRefusals(t *testing.T) {
	t.Run("headless refuses without prompting", func(t *testing.T) {
		var out bytes.Buffer
		cmd := newWithOutput(&out)
		cmd.confirm = func(string) (bool, error) {
			t.Fatal("headless must never prompt")
			return false, nil
		}
		err := cmd.resolveRefusals(oneRefusal(t), 2, false)
		if err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("err = %v, want the bad request", err)
		}
		if out.Len() != 0 {
			t.Errorf("headless output = %q, want none", out.String())
		}
	})

	for _, tc := range []struct {
		name    string
		answer  bool
		wantErr bool
	}{
		{"decline aborts", false, true},
		{"accept strips and continues", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			var asked []string
			cmd := newWithOutput(&out)
			cmd.confirm = func(title string) (bool, error) {
				asked = append(asked, title)
				return tc.answer, nil
			}
			err := cmd.resolveRefusals(oneRefusal(t), 2, true)
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %t", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is400BadRequest(err) {
				t.Errorf("decline err = %v, want the bad request (exit 64)", err)
			}
			wantOut := "organize: 1 edit can't be applied:\n" +
				"  task2.ics  status: moved into the no-value section, but \"status\" can't be cleared\n"
			if out.String() != wantOut {
				t.Errorf("output =\n%s\nwant\n%s", out.String(), wantOut)
			}
			if want := []string{"Drop it and continue with the remaining 2 change(s)?"}; !reflect.DeepEqual(asked, want) {
				t.Errorf("asked = %q, want %q", asked, want)
			}
		})
	}
}

// TestStrippedRefusalKeepsTheObjectsOtherEdits pins strip-one-keep-others:
// with the refused move stripped, the SAME object's field edit and another
// object's move still preview; with every edit stripped nothing is left, and
// the skipped summary still names the dropped edit.
func TestStrippedRefusalKeepsTheObjectsOtherEdits(t *testing.T) {
	idOf := boxIDsFor(nil, refusalAnchor)
	refusedMove := refusalMove(t, "task2.ics", "task", "open", "")
	otherMove := refusalMove(t, "task1.ics", "task", "open", "done")
	doc := document{Ungrouped: []objectLine{
		{ID: "task1.ics", Desc: "one"},
		{ID: "task2.ics", Fields: []cgp.BoxAtom{{Name: "location", Value: "Annex"}}, Desc: "two"},
	}}
	base := document{Ungrouped: []objectLine{
		{ID: "task1.ics", Desc: "one"},
		{ID: "task2.ics", Fields: []cgp.BoxAtom{{Name: "location", Value: "HQ"}}, Desc: "two"},
	}}
	fieldEdit := objectFieldEdit{
		URI: refusedMove.URI, Node: refusedMove.Node,
		Edits: []cgp.FieldEdit{{Name: "location", Value: "Annex"}},
	}

	kept, refused := partitionRefusedMoves(
		[]move{otherMove, refusedMove}, refusalWrites(), idOf,
	)
	changes := buildChanges(doc, base, kept, []objectFieldEdit{fieldEdit}, nil, "status=", "", nil, nil, idOf)
	if len(changes) != 2 {
		t.Fatalf("changes = %+v, want task1 (move) and task2 (field edit)", changes)
	}
	task2 := changes[1]
	if task2.ID != "task2.ics" || len(task2.Moves) != 0 || len(task2.Atoms) != 1 {
		t.Errorf("task2 change = %+v, want its location edit and NO move", task2)
	}

	onlyKept, onlyRefused := partitionRefusedMoves([]move{refusedMove}, refusalWrites(), idOf)
	if left := buildChanges(doc, base, onlyKept, nil, nil, "status=", "", nil, nil, idOf); len(left) != 0 {
		t.Errorf("changes after stripping everything = %+v, want none", left)
	}

	var out bytes.Buffer
	newWithOutput(&out).reportSkipped(append(refused, onlyRefused...))
	if got, want := out.String(), "organize: skipped: task2.ics status move, task2.ics status move\n"; got != want {
		t.Errorf("skipped summary = %q, want %q", got, want)
	}
}

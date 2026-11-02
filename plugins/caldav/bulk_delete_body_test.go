package caldav

import (
	"context"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// A delete op that carries a body used to run with the body dropped; a source
// whose delete takes no options now refuses it, and deletes nothing.
func TestBulkMutate_DeleteWithBodyIsRefused(t *testing.T) {
	f, home := startFakeEmpty(t)
	obj := objectArg(home, "/dav/cal/keep.ics")
	if _, err := (Plugin{}).BulkMutate(context.Background(), cutting_garden_plugins.BulkRequest{
		Atomicity: cutting_garden_plugins.BulkBestEffort,
		Ops: []cutting_garden_plugins.BulkOp{{
			Kind: cutting_garden_plugins.BulkCreate, URI: mustParseURL(t, obj),
			Type: typeVEVENT, Body: []byte(vevent("u-keep", "Keep")),
		}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	result, err := Plugin{}.BulkMutate(context.Background(), cutting_garden_plugins.BulkRequest{
		Atomicity: cutting_garden_plugins.BulkBestEffort,
		Ops: []cutting_garden_plugins.BulkOp{{
			Kind: cutting_garden_plugins.BulkDelete, URI: mustParseURL(t, obj),
			Body: []byte(`{"subtree": true}`),
		}},
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.AppliedNodes) != 0 || len(result.Failed) != 1 ||
		!strings.Contains(result.Failed[0].Err, "takes no body") {
		t.Fatalf("applied = %v, failed = %+v; want one failure saying the delete takes no body",
			result.AppliedNodes, result.Failed)
	}
	if _, ok := f.resources["/dav/cal/keep.ics"]; !ok {
		t.Error("a refused delete removed the object")
	}
}

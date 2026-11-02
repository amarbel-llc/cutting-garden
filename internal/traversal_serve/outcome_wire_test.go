package traversal_serve

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// detailPlugin is the full fake plus the per-op detail interfaces: a patch
// that moved the node, a delete that names it, a delete of a missing node.
type detailPlugin struct {
	*fakeFullPlugin
	newURI  string
	name    string
	missing bool
}

func (d detailPlugin) PatchNodeDetail(
	ctx context.Context, uri *url.URL, body io.Reader,
) (cutting_garden_plugins.PatchDetail, error) {
	applied, err := d.fakeFullPlugin.PatchNode(ctx, uri, body)
	detail := cutting_garden_plugins.PatchDetail{Applied: applied}
	if d.newURI != "" {
		detail.NewURI, _ = url.Parse(d.newURI)
	}
	return detail, err
}

func (d detailPlugin) DeleteNodeDetail(
	_ context.Context, _ *url.URL, _ io.Reader,
) (cutting_garden_plugins.DeleteDetail, error) {
	if d.missing {
		return cutting_garden_plugins.DeleteDetail{NotFound: true}, errors.BadRequestf("no such node")
	}
	return cutting_garden_plugins.DeleteDetail{Name: d.name}, nil
}

func detailConfig(d detailPlugin) ServeConfig {
	return ServeConfig{Plugin: d, Info: PluginInfo{Name: "fake-mem", Version: "0.0.1"}}
}

func rawCall(t *testing.T, client *Peer, method string, params any) (json.RawMessage, error) {
	t.Helper()
	var raw json.RawMessage
	err := client.Call(context.Background(), method, params, &raw)
	return raw, err
}

// node.patch carries the node's URI afterwards when the plugin reports one,
// and stays exactly as before when it does not.
func TestServeNodePatchResultCarriesTheNewURI(t *testing.T) {
	patch := NodePatchParams{URI: fakeLeafA, BodyBase64: "e30="} // {}

	t.Run("reported", func(t *testing.T) {
		d := detailPlugin{
			fakeFullPlugin: &fakeFullPlugin{patchApplied: []string{"state"}},
			newURI:         "mem://host/root/a?id=1",
		}
		client, _ := startServe(t, detailConfig(d))
		mustInitialize(t, client)
		raw, err := rawCall(t, client, MethodNodePatch, patch)
		if err != nil {
			t.Fatalf("node.patch: %v", err)
		}
		if got, want := string(raw), `{"applied":["state"],"uri":"mem://host/root/a?id=1"}`; got != want {
			t.Errorf("result = %s, want %s", got, want)
		}
	})

	t.Run("not reported", func(t *testing.T) {
		d := detailPlugin{fakeFullPlugin: &fakeFullPlugin{patchApplied: []string{"state"}}}
		client, _ := startServe(t, detailConfig(d))
		mustInitialize(t, client)
		raw, err := rawCall(t, client, MethodNodePatch, patch)
		if err != nil || string(raw) != `{"applied":["state"]}` {
			t.Errorf("result = %s err=%v", raw, err)
		}
	})
}

// node.delete names the deleted node; a missing node answers CodeNotFound,
// an error an old host reads as a plain failure; a plugin without the detail
// interface answers the empty result, as before.
func TestServeNodeDeleteNamesTheNodeAndTellsNotFound(t *testing.T) {
	del := NodeDeleteParams{URI: fakeLeafA}

	t.Run("named", func(t *testing.T) {
		client, _ := startServe(t, detailConfig(detailPlugin{fakeFullPlugin: &fakeFullPlugin{}, name: "Dee"}))
		mustInitialize(t, client)
		raw, err := rawCall(t, client, MethodNodeDelete, del)
		if err != nil || string(raw) != `{"name":"Dee"}` {
			t.Errorf("result = %s err=%v", raw, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		client, _ := startServe(t, detailConfig(detailPlugin{fakeFullPlugin: &fakeFullPlugin{}, missing: true}))
		mustInitialize(t, client)
		_, err := rawCall(t, client, MethodNodeDelete, del)
		if code, ok := CodeOf(err); !ok || code != CodeNotFound {
			t.Errorf("CodeOf = %d, %t, want %d (err %v)", code, ok, CodeNotFound, err)
		}
	})
	t.Run("a plugin without detail", func(t *testing.T) {
		client, _ := startServe(t, fullPluginConfig(&fakeFullPlugin{}))
		mustInitialize(t, client)
		raw, err := rawCall(t, client, MethodNodeDelete, del)
		if err != nil || string(raw) != `{}` {
			t.Errorf("result = %s err=%v", raw, err)
		}
	})
}

// The adapter hands a linked consumer the same detail a linked plugin gives.
func TestWirePluginDetailSurvivesTheWire(t *testing.T) {
	ctx := context.Background()
	d := detailPlugin{
		fakeFullPlugin: &fakeFullPlugin{patchApplied: []string{"title"}},
		newURI:         "mem://host/root/a?id=1",
		name:           "Dee",
	}
	adapter, _ := newTestWirePlugin(t, memSpec(), detailConfig(d))

	patch, err := adapter.PatchNodeDetail(ctx, mustParseURL(t, fakeLeafA), strings.NewReader(`{"title":"x"}`))
	if err != nil || !reflect.DeepEqual(patch.Applied, []string{"title"}) ||
		patch.NewURI == nil || patch.NewURI.String() != "mem://host/root/a?id=1" {
		t.Fatalf("patch detail = %+v err=%v", patch, err)
	}
	applied, err := adapter.PatchNode(ctx, mustParseURL(t, fakeLeafA), strings.NewReader(`{"title":"x"}`))
	if err != nil || !reflect.DeepEqual(applied, []string{"title"}) {
		t.Errorf("PatchNode = %v err=%v", applied, err)
	}

	del, err := adapter.DeleteNodeDetail(ctx, mustParseURL(t, fakeLeafA), nil)
	if err != nil || del.Name != "Dee" || del.NotFound {
		t.Errorf("delete detail = %+v err=%v", del, err)
	}

	// A delete body does not cross node.delete: refused, nothing sent.
	if _, err := adapter.DeleteNodeDetail(ctx, mustParseURL(t, fakeLeafA), strings.NewReader(`{"subtree":true}`)); err == nil ||
		!strings.Contains(err.Error(), "takes no body") {
		t.Errorf("delete with a body: %v", err)
	}
}

func TestWirePluginDeleteOfAMissingNodeIsNotFound(t *testing.T) {
	d := detailPlugin{fakeFullPlugin: &fakeFullPlugin{}, missing: true}
	adapter, _ := newTestWirePlugin(t, memSpec(), detailConfig(d))

	got, err := adapter.DeleteNodeDetail(context.Background(), mustParseURL(t, fakeLeafA), nil)
	if err == nil || !got.NotFound {
		t.Errorf("detail = %+v err=%v, want NotFound with an error", got, err)
	}
	// The plain delete is still a failure, as before.
	if err := adapter.DeleteNode(context.Background(), mustParseURL(t, fakeLeafA)); err == nil {
		t.Error("DeleteNode of a missing node must fail")
	}
}

// node.bulk_mutate's result carries the per-op outcomes, omitted when the
// plugin reported none.
func TestBulkMutateResultOutcomesRoundTrip(t *testing.T) {
	u := func(s string) *url.URL { v, _ := url.Parse(s); return v }
	in := cutting_garden_plugins.BulkResult{
		AppliedNodes: []*url.URL{u("mem://h/a"), u("mem://h/b")},
		Failed:       []cutting_garden_plugins.BulkFailure{{URI: u("mem://h/c"), Err: "gone"}},
		Outcomes: []cutting_garden_plugins.BulkOutcome{
			{
				URI: u("mem://h/a"), Status: cutting_garden_plugins.OutcomeApplied,
				Fields: []string{"title"}, NewURI: u("mem://h/a?id=1"),
			},
			{URI: u("mem://h/b"), Status: cutting_garden_plugins.OutcomeDeleted, Name: "Bee"},
			{URI: u("mem://h/c"), Status: cutting_garden_plugins.OutcomeNotFound, Err: "gone"},
			{Ref: "+t", Status: cutting_garden_plugins.OutcomeFailed, Err: "depends on the create of temp id +t, which failed"},
		},
	}
	data, err := json.Marshal(BulkMutateResultFrom(in))
	if err != nil {
		t.Fatal(err)
	}
	var wire BulkMutateResult
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	out, err := wire.ToBulkResult()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Outcomes, in.Outcomes) {
		t.Errorf("outcomes\n got %+v\nwant %+v", out.Outcomes, in.Outcomes)
	}

	none, _ := json.Marshal(BulkMutateResultFrom(cutting_garden_plugins.BulkResult{
		AppliedNodes: []*url.URL{u("mem://h/a")},
	}))
	if strings.Contains(string(none), "outcomes") {
		t.Errorf("a result with no outcomes carries the key: %s", none)
	}
}

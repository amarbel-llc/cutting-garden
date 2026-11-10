package cutting_garden_plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"reflect"
	"testing"
)

func TestIgnoredPatchFields(t *testing.T) {
	fields := map[string]json.RawMessage{
		"status": json.RawMessage(`"x"`), "color": json.RawMessage(`1`), "alpha": json.RawMessage(`1`),
	}
	if got := IgnoredPatchFields(fields, []string{"status", "summary"}); !reflect.DeepEqual(got, []string{"alpha", "color"}) {
		t.Errorf("IgnoredPatchFields = %v, want [alpha color] (sorted)", got)
	}
	if got := IgnoredPatchFields(fields, []string{"alpha", "color", "status"}); got != nil {
		t.Errorf("IgnoredPatchFields = %v, want nil when every key is supported", got)
	}
}

// ignoringPatcher is a NodeMutator whose detailed patch reports ignored keys.
type ignoringPatcher struct {
	NodeMutator
	applied []string
}

func (p ignoringPatcher) PatchNodeDetail(
	context.Context, *url.URL, io.Reader,
) (PatchDetail, error) {
	return PatchDetail{Applied: p.applied, Ignored: []string{"color"}}, nil
}

// TestApplyBulkOp_CarriesIgnored pins cutting-garden#376: the per-op outcome
// names the ignored keys, both when other fields applied and when the patch
// landed nothing (which stays patched_nothing).
func TestApplyBulkOp_CarriesIgnored(t *testing.T) {
	u, _ := url.Parse("x://n")
	for _, c := range []struct {
		applied []string
		status  BulkOutcomeStatus
	}{
		{[]string{"status"}, OutcomeApplied},
		{[]string{}, OutcomePatchedNothing},
	} {
		var result BulkResult
		ApplyBulkOp(context.Background(), ignoringPatcher{applied: c.applied},
			BulkOp{Kind: BulkPatch, URI: u, Body: []byte(`{}`)}, &result)
		if len(result.Outcomes) != 1 {
			t.Fatalf("outcomes = %v, want one", result.Outcomes)
		}
		got := result.Outcomes[0]
		if got.Status != c.status || !reflect.DeepEqual(got.Ignored, []string{"color"}) {
			t.Errorf("outcome = %+v, want status %s with ignored [color]", got, c.status)
		}
	}
}

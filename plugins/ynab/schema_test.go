package ynab

import (
	"encoding/json"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestDescribeBodies_NamesEveryPatchField pins describe_node_types' word on a
// transaction and a payee: both are writable (a type is writable iff it has a
// body entry), and the patch description names every key PatchNode recognizes,
// so a capability profile can name a field the plugin advertises. memo has no
// facet dimension, so this is the only place it is advertised (cutting-garden#410).
func TestDescribeBodies_NamesEveryPatchField(t *testing.T) {
	bodies := map[string][]string{}
	examples := map[string]any{}
	for _, b := range (Plugin{}).DescribeBodies() {
		bodies[b.Tag] = b.Accepts
		examples[b.Tag] = b.Example
	}
	for tag, fields := range map[string][]string{
		typeTransaction: transactionPatchFields,
		typePayee:       payeePatchFields,
	} {
		accepts, ok := bodies[tag]
		if !ok {
			t.Errorf("%s has no body entry, so describe_node_types reports it not writable", tag)
			continue
		}
		joined := strings.Join(accepts, "\n")
		for _, field := range fields {
			if !strings.Contains(joined, `"`+field+`"`) {
				t.Errorf("%s body description does not name the patch field %q:\n%s", tag, field, joined)
			}
		}
		raw, err := json.Marshal(examples[tag])
		if err != nil {
			t.Fatalf("%s example does not marshal: %v", tag, err)
		}
		patch, err := parseExamplePatch(tag, raw)
		if err != nil {
			t.Errorf("%s example is not a body PatchNode accepts: %v", tag, err)
		}
		if len(patch) == 0 {
			t.Errorf("%s example names no recognized patch field: %s", tag, raw)
		}
	}
	if len(bodies) != 2 {
		t.Errorf("DescribeBodies lists %d types, want only the transaction and the payee", len(bodies))
	}
}

// parseExamplePatch returns the fields an example body names that PatchNode
// recognizes; a transaction example also runs the pre-network type checks.
func parseExamplePatch(tag string, raw []byte) ([]string, error) {
	if tag == typePayee {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		return cutting_garden_plugins.RecognizedPatchFields(fields, payeePatchFields), nil
	}
	patch, err := parseTransactionPatch(strings.NewReader(string(raw)))
	return patch.recognized, err
}

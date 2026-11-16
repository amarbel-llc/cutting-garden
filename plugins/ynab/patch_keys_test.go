package ynab

import (
	"net/url"
	"reflect"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestPatchTarget_TransactionAndPayeeOnly(t *testing.T) {
	for _, tc := range []struct {
		uri  string
		want string
	}{
		{"ynab://acct/Budget/transactions/abc", typeTransaction},
		{"ynab://acct/Budget/payees/p1", typePayee},
		{"ynab://acct/Budget/transactions/", ""},
		{"ynab://acct/Budget/payees/", ""},
		{"ynab://acct/Budget/accounts/Checking", ""},
		{"ynab://acct/Budget/categories/Group/Cat", ""},
		{"ynab://acct/", ""},
		{"fastmail://acct/Budget/transactions/abc", ""},
	} {
		u, err := url.Parse(tc.uri)
		if err != nil {
			t.Fatal(err)
		}
		tag, ok := Plugin{}.PatchTarget(u)
		if tag != tc.want || ok != (tc.want != "") {
			t.Errorf("PatchTarget(%q) = %q, %v; want %q", tc.uri, tag, ok, tc.want)
		}
	}
}

// The declared keys are exactly the recognized sets, so the host's strip
// leaves the plugin's behaviour unchanged.
func TestDescribePatchKeys_MatchesTheRecognizedSets(t *testing.T) {
	want := map[string][]string{
		typeTransaction: transactionPatchFields,
		typePayee:       payeePatchFields,
	}
	for _, declared := range (Plugin{}).DescribePatchKeys() {
		if !reflect.DeepEqual(declared.Keys, want[declared.Tag]) {
			t.Errorf("%s keys = %v, want %v", declared.Tag, declared.Keys, want[declared.Tag])
		}
		delete(want, declared.Tag)
	}
	if len(want) != 0 {
		t.Errorf("types not declared: %v", want)
	}

	u, _ := url.Parse("ynab://acct/Budget/transactions/abc")
	got, ok := cutting_garden_plugins.ResolvePatchKeys(Plugin{}, u)
	if !ok || got.Tag != typeTransaction || !reflect.DeepEqual(got.Keys, transactionPatchFields) {
		t.Errorf("ResolvePatchKeys = %+v %v", got, ok)
	}
}

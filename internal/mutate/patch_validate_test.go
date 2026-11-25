package mutate

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// flat is a declaration of top-level keys.
func flat(keys ...string) cutting_garden_plugins.NodeTypePatchKeys {
	return cutting_garden_plugins.NodeTypePatchKeys{Tag: "test-v1", Keys: keys}
}

func TestValidatePatchBody_AllDeclaredKeepsTheOriginalBytes(t *testing.T) {
	body := []byte(`{ "summary": "x",  "priority": 1 }`)
	got := validatePatchBody(body, flat("summary", "priority"))
	if string(got.Body) != string(body) || got.Ignored != nil || got.Empty {
		t.Errorf("verdict = %+v, want the original bytes, no ignored, not empty", got)
	}
}

func TestValidatePatchBody_StripsUndeclaredKeysAndSortsThem(t *testing.T) {
	got := validatePatchBody(
		[]byte(`{"zeta":1,"summary":"x","alpha":2}`), flat("summary"),
	)
	if !reflect.DeepEqual(got.Ignored, []string{"alpha", "zeta"}) {
		t.Errorf("ignored = %v, want [alpha zeta]", got.Ignored)
	}
	if got.Empty {
		t.Error("a body keeping a declared key is not empty")
	}
	var kept map[string]json.RawMessage
	if err := json.Unmarshal(got.Body, &kept); err != nil {
		t.Fatalf("stripped body is not JSON: %v", err)
	}
	if len(kept) != 1 || string(kept["summary"]) != `"x"` {
		t.Errorf("stripped body = %s, want only summary", got.Body)
	}
}

func TestValidatePatchBody_NothingDeclaredIsEmpty(t *testing.T) {
	got := validatePatchBody([]byte(`{"a":1,"b":2}`), flat("summary"))
	if !got.Empty || !reflect.DeepEqual(got.Ignored, []string{"a", "b"}) {
		t.Errorf("verdict = %+v, want empty with ignored [a b]", got)
	}
}

// A body the host cannot read as an object is the plugin's to refuse.
func TestValidatePatchBody_NonObjectBodiesPassThrough(t *testing.T) {
	for _, body := range []string{``, `not json`, `[1,2]`, `{}`, `null`} {
		got := validatePatchBody([]byte(body), flat("summary"))
		if string(got.Body) != body || got.Ignored != nil || got.Empty {
			t.Errorf("body %q: verdict = %+v, want pass-through", body, got)
		}
	}
}

// sectioned declares a wrapped body: {"component":"VTODO","task":{...}}.
func sectioned() cutting_garden_plugins.NodeTypePatchKeys {
	return cutting_garden_plugins.NodeTypePatchKeys{
		Tag:           "obj-v1",
		Discriminator: "component",
		Sections: []cutting_garden_plugins.PatchSection{
			{Value: "VTODO", Section: "task", Keys: []string{"priority", "summary"}},
			{Value: "VEVENT", Section: "event", Keys: []string{"location", "summary"}},
		},
	}
}

func TestValidatePatchBody_WrappedCatchesATypoOneLevelDown(t *testing.T) {
	got := validatePatchBody(
		[]byte(`{"component":"VTODO","task":{"summry":"x","summary":"y"}}`), sectioned(),
	)
	if !reflect.DeepEqual(got.Ignored, []string{"summry"}) || got.Empty || got.Unverifiable {
		t.Fatalf("verdict = %+v", got)
	}
	if strings.Contains(string(got.Body), "summry") || !strings.Contains(string(got.Body), `"summary":"y"`) {
		t.Errorf("stripped body = %s", got.Body)
	}
}

// A key valid for another component's section is not valid for this one.
func TestValidatePatchBody_WrappedUsesTheChosenSectionsKeys(t *testing.T) {
	got := validatePatchBody(
		[]byte(`{"component":"VTODO","task":{"location":"x","summary":"y"}}`), sectioned(),
	)
	if !reflect.DeepEqual(got.Ignored, []string{"location"}) {
		t.Errorf("ignored = %v, want [location]", got.Ignored)
	}
}

func TestValidatePatchBody_WrappedNamesTopLevelStrays(t *testing.T) {
	got := validatePatchBody(
		[]byte(`{"component":"VTODO","task":{"summary":"y"},"event":{"summary":"z"},"oops":1}`),
		sectioned(),
	)
	if !reflect.DeepEqual(got.Ignored, []string{"event", "oops"}) {
		t.Errorf("ignored = %v, want [event oops]", got.Ignored)
	}
	if strings.Contains(string(got.Body), "event") || strings.Contains(string(got.Body), "oops") {
		t.Errorf("stripped body = %s", got.Body)
	}
}

func TestValidatePatchBody_WrappedNothingDeclaredIsEmpty(t *testing.T) {
	got := validatePatchBody([]byte(`{"component":"VTODO","task":{"nope":1}}`), sectioned())
	if !got.Empty || !reflect.DeepEqual(got.Ignored, []string{"nope"}) {
		t.Errorf("verdict = %+v, want empty with ignored [nope]", got)
	}
}

// A bare body's keys depend on the stored node: the host cannot check it.
func TestValidatePatchBody_BareBodyUnderASectionedDeclarationIsUnverifiable(t *testing.T) {
	body := `{"summary":"x"}`
	got := validatePatchBody([]byte(body), sectioned())
	if !got.Unverifiable || string(got.Body) != body || got.Ignored != nil {
		t.Errorf("verdict = %+v", got)
	}
}

// A component no section names is the plugin's to refuse, before any write.
func TestValidatePatchBody_UnknownDiscriminatorValuePassesThrough(t *testing.T) {
	body := `{"component":"VFREEBUSY","task":{"x":1}}`
	got := validatePatchBody([]byte(body), sectioned())
	if string(got.Body) != body || got.Ignored != nil || got.Unverifiable || got.Empty {
		t.Errorf("verdict = %+v", got)
	}
}

// keyedLister is a lister that declares writable keys per URI path.
type keyedLister struct{ keys map[string][]string }

func (k keyedLister) PatchKeys(uri *url.URL) (cutting_garden_plugins.NodeTypePatchKeys, bool) {
	keys, ok := k.keys[uri.Path]
	return flat(keys...), ok
}

// sectionedLister declares the wrapped form for every URI.
type sectionedLister struct{}

func (sectionedLister) PatchKeys(*url.URL) (cutting_garden_plugins.NodeTypePatchKeys, bool) {
	return sectioned(), true
}

func patchOp(t *testing.T, uri, body string) cutting_garden_plugins.BulkOp {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	return cutting_garden_plugins.BulkOp{
		Kind: cutting_garden_plugins.BulkPatch, URI: u, Body: []byte(body),
	}
}

func TestScreenPatches_TolerantStripsAndReports(t *testing.T) {
	ops := []cutting_garden_plugins.BulkOp{patchOp(t, "faketest://h/a", `{"summary":"x","oops":1}`)}
	screen, err := screenPatches(ops, keyedLister{map[string][]string{"/a": {"summary"}}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(screen.dispatched, []int{0}) ||
		!reflect.DeepEqual(screen.stripped[0], []string{"oops"}) {
		t.Errorf("screen = %+v", screen)
	}
	if strings.Contains(string(ops[0].Body), "oops") {
		t.Errorf("op body still carries the undeclared key: %s", ops[0].Body)
	}
}

func TestScreenPatches_TolerantWithoutADeclarationPassesThrough(t *testing.T) {
	ops := []cutting_garden_plugins.BulkOp{patchOp(t, "faketest://h/b", `{"anything":1}`)}
	screen, err := screenPatches(ops, keyedLister{}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(screen.dispatched, []int{0}) || len(screen.resolved) != 0 ||
		string(ops[0].Body) != `{"anything":1}` {
		t.Errorf("screen = %+v, body = %s", screen, ops[0].Body)
	}

	// No resolver at all behaves the same.
	if _, err := screenPatches(ops, nil, false, false); err != nil {
		t.Fatal(err)
	}
}

func TestScreenPatches_TolerantEmptyAfterStripIsPatchedNothing(t *testing.T) {
	ops := []cutting_garden_plugins.BulkOp{patchOp(t, "faketest://h/a", `{"oops":1}`)}
	screen, err := screenPatches(ops, keyedLister{map[string][]string{"/a": {"summary"}}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	got := screen.resolved[0]
	if len(screen.dispatched) != 0 ||
		got.status != cutting_garden_plugins.OutcomePatchedNothing ||
		!reflect.DeepEqual(got.ignored, []string{"oops"}) {
		t.Errorf("screen = %+v", screen)
	}
}

// Inside a transaction the plugin decides what an all-ignored body means.
func TestScreenPatches_AtomicKeepsAnEmptiedBodyForThePlugin(t *testing.T) {
	ops := []cutting_garden_plugins.BulkOp{patchOp(t, "faketest://h/a", `{"oops":1}`)}
	screen, err := screenPatches(ops, keyedLister{map[string][]string{"/a": {"summary"}}}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(screen.dispatched, []int{0}) || string(ops[0].Body) != `{"oops":1}` {
		t.Errorf("screen = %+v, body = %s", screen, ops[0].Body)
	}
}

func TestScreenPatches_StrictRefusesUndeclaredKeysAndUndeclaredTypes(t *testing.T) {
	lister := keyedLister{map[string][]string{"/a": {"summary"}}}
	ops := []cutting_garden_plugins.BulkOp{
		patchOp(t, "faketest://h/a", `{"summary":"x","oops":1}`),
		patchOp(t, "faketest://h/undeclared", `{"summary":"x"}`),
		patchOp(t, "faketest://h/a", `{"summary":"y"}`),
	}
	screen, err := screenPatches(ops, lister, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(screen.dispatched, []int{2}) {
		t.Errorf("dispatched = %v, want only the clean op", screen.dispatched)
	}
	if got := screen.resolved[0].err; !strings.Contains(got, "oops") ||
		!strings.Contains(got, "nothing was written") {
		t.Errorf("refusal for the typo'd key = %q", got)
	}
	if got := screen.resolved[1].err; !strings.Contains(got, "does not declare") {
		t.Errorf("refusal for the undeclared type = %q", got)
	}
}

// Strict concerns patches only: creates, puts and deletes go through.
func TestScreenPatches_StrictLeavesOtherVerbsAlone(t *testing.T) {
	u, _ := url.Parse("faketest://h/a")
	ops := []cutting_garden_plugins.BulkOp{
		{Kind: cutting_garden_plugins.BulkDelete, URI: u},
		{Kind: cutting_garden_plugins.BulkPut, URI: u, Body: []byte(`{"x":1}`)},
	}
	screen, err := screenPatches(ops, keyedLister{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(screen.dispatched, []int{0, 1}) {
		t.Errorf("dispatched = %v", screen.dispatched)
	}
}

func TestScreenPatches_StrictAtomicFailsTheCallWithNothingDispatched(t *testing.T) {
	lister := keyedLister{map[string][]string{"/a": {"summary"}}}
	ops := []cutting_garden_plugins.BulkOp{
		patchOp(t, "faketest://h/a", `{"summary":"x"}`),
		patchOp(t, "faketest://h/a", `{"oops":1}`),
	}
	if _, err := screenPatches(ops, lister, true, true); err == nil ||
		!strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("err = %v, want a refusal before any write", err)
	}
}

// Strict, wrapped-form declaration: a typo nested one level down is refused
// before the write, and a bare body is refused with a message that says to use
// the wrapped form. Tolerant dispatches the bare body unchanged.
func TestScreenPatches_StrictWrappedForm(t *testing.T) {
	ops := []cutting_garden_plugins.BulkOp{
		patchOp(t, "faketest://h/o1", `{"component":"VTODO","task":{"summry":"x"}}`),
		patchOp(t, "faketest://h/o2", `{"summary":"x"}`),
		patchOp(t, "faketest://h/o3", `{"component":"VTODO","task":{"summary":"x"}}`),
	}
	screen, err := screenPatches(ops, sectionedLister{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(screen.dispatched, []int{2}) {
		t.Errorf("dispatched = %v, want only the clean wrapped op", screen.dispatched)
	}
	if got := screen.resolved[0].err; !strings.Contains(got, "summry") {
		t.Errorf("nested typo refusal = %q", got)
	}
	if got := screen.resolved[1].err; !strings.Contains(got, "wrapped form") ||
		!strings.Contains(got, `"component"`) {
		t.Errorf("bare-form refusal = %q", got)
	}

	tolerant := []cutting_garden_plugins.BulkOp{patchOp(t, "faketest://h/o2", `{"summary":"x"}`)}
	screen, err = screenPatches(tolerant, sectionedLister{}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(screen.dispatched, []int{0}) || string(tolerant[0].Body) != `{"summary":"x"}` {
		t.Errorf("tolerant bare body: %+v / %s", screen, tolerant[0].Body)
	}
}

func TestMergeScreen_InterleavesHostSettledOpsInRequestOrder(t *testing.T) {
	a, _ := url.Parse("faketest://h/a")
	b, _ := url.Parse("faketest://h/b")
	c, _ := url.Parse("faketest://h/c")
	ops := []cutting_garden_plugins.BulkOp{{URI: a}, {URI: b}, {URI: c}}
	screen := patchScreen{
		dispatched: []int{0, 2},
		resolved: map[int]hostResolvedOp{
			1: {status: cutting_garden_plugins.OutcomeFailed, err: "refused", ignored: []string{"k"}},
		},
		stripped: map[int][]string{2: {"oops"}},
	}
	result := cutting_garden_plugins.BulkResult{
		AppliedNodes: []*url.URL{a, c},
		Outcomes: []cutting_garden_plugins.BulkOutcome{
			{URI: a, Status: cutting_garden_plugins.OutcomeApplied},
			{URI: c, Status: cutting_garden_plugins.OutcomeApplied, Ignored: []string{"plugin"}},
		},
	}

	mergeScreen(&result, ops, screen)

	if len(result.Outcomes) != 3 ||
		result.Outcomes[0].URI != a || result.Outcomes[1].URI != b || result.Outcomes[2].URI != c {
		t.Fatalf("outcomes out of request order: %+v", result.Outcomes)
	}
	if result.Outcomes[1].Status != cutting_garden_plugins.OutcomeFailed ||
		result.Outcomes[1].Err != "refused" {
		t.Errorf("settled outcome = %+v", result.Outcomes[1])
	}
	if !reflect.DeepEqual(result.Outcomes[2].Ignored, []string{"oops", "plugin"}) {
		t.Errorf("stripped keys not merged with the plugin's: %v", result.Outcomes[2].Ignored)
	}
	if len(result.Failed) != 1 || result.Failed[0].Err != "refused" {
		t.Errorf("failed = %+v", result.Failed)
	}
}

// A plugin that reports no outcomes is not given invented ones.
func TestMergeScreen_UnreportedOutcomesStayUnreported(t *testing.T) {
	a, _ := url.Parse("faketest://h/a")
	b, _ := url.Parse("faketest://h/b")
	ops := []cutting_garden_plugins.BulkOp{{URI: a}, {URI: b}}
	screen := patchScreen{
		dispatched: []int{0},
		resolved: map[int]hostResolvedOp{
			1: {status: cutting_garden_plugins.OutcomePatchedNothing, ignored: []string{"k"}},
		},
	}
	result := cutting_garden_plugins.BulkResult{AppliedNodes: []*url.URL{a}}

	mergeScreen(&result, ops, screen)

	if len(result.Outcomes) != 0 {
		t.Errorf("outcomes = %+v, want none (the plugin reported none)", result.Outcomes)
	}
	if len(result.PatchedNothing) != 1 || result.PatchedNothing[0] != b {
		t.Errorf("patched_nothing = %v", result.PatchedNothing)
	}
}

package command_components

import (
	"context"
	"net/url"
	"reflect"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// interpBase carries the RootLister boilerplate shared by the tag-view test
// fakes: ListRoots is unused here (the helpers only read the schema
// capability), so it returns nothing.
type interpBase struct{}

func (interpBase) Schemes() []string     { return []string{"fake"} }
func (interpBase) TypeTag() string       { return "fake-v1" }
func (interpBase) Types() []cgp.NodeType { return nil }
func (interpBase) ListRoots(context.Context, *url.URL) ([]cgp.Node, error) {
	return nil, nil
}

// interpUnifiedLister is a RootLister that ALSO implements UnifiedDescriber,
// declaring the given node-type unified fields — the field-default source
// InterpreterForDimension reads.
type interpUnifiedLister struct {
	interpBase
	fields []cgp.NodeTypeUnifiedFields
}

func (l interpUnifiedLister) DescribeUnified() []cgp.NodeTypeUnifiedFields {
	return l.fields
}

// interpPlainLister is a RootLister with NO unified-field capability — the
// "capability absent" case that must default to naive.
type interpPlainLister struct {
	interpBase
}

// categoriesUnified declares a single categories tag field carrying interp as its
// plugin-declared default interpreter.
func categoriesUnified(interp string) []cgp.NodeTypeUnifiedFields {
	return []cgp.NodeTypeUnifiedFields{{
		Tag: "task-v1",
		Codecs: []cgp.Codec{cgp.IdentityCodec{Field: cgp.UnifiedField{
			Key:         "categories",
			Kind:        cgp.FieldTag,
			MultiValued: true,
			Interpreter: interp,
		}}},
	}}
}

// isTransitive distinguishes the resolved interpreter by real behavior, exactly as
// TestResolveTagInterpreter does: dodder-hyphen's Matches is transitive along
// the segment path (project matches project-client), naive's is exact.
func isTransitive(interp cgp.TagInterpreter) bool {
	return interp.Matches([]string{"project-client"}, "project")
}

// TestInterpreterForDimension_FieldDefaultAndOverride pins RFC 0019 §4 selection
// (#231 slice 3; moved here from internal/organize with the helper, native tags
// slice 2 T4): the field's declared default resolves the interpreter, the
// global [tags] override wins over it, an absent capability / missing field /
// empty declaration defaults to naive (no error), and an unknown override name is
// a loud bad request.
func TestInterpreterForDimension_FieldDefaultAndOverride(t *testing.T) {
	declaresNaive := interpUnifiedLister{fields: categoriesUnified("naive")}

	// Field declares naive, no override → naive (exact match), name reported.
	interp, name, err := InterpreterForDimension(declaresNaive, "categories", "")
	if err != nil {
		t.Fatalf("field default naive, no override: %v", err)
	}
	if isTransitive(interp) {
		t.Error("field default naive, no override: got transitive (dodder-hyphen) semantics")
	}
	if name != "naive" {
		t.Errorf("resolved name = %q, want naive", name)
	}

	// Override wins over the declared field default, and is the reported name.
	interp, name, err = InterpreterForDimension(declaresNaive, "categories", "dodder-hyphen")
	if err != nil {
		t.Fatalf("override dodder-hyphen: %v", err)
	}
	if !isTransitive(interp) {
		t.Error("override=dodder-hyphen did not win: got exact-match (naive) semantics")
	}
	if name != "dodder-hyphen" {
		t.Errorf("resolved name = %q, want dodder-hyphen", name)
	}

	// A lister with no UnifiedDescriber capability defaults to naive.
	interp, _, err = InterpreterForDimension(interpPlainLister{}, "categories", "")
	if err != nil {
		t.Fatalf("no DescribeUnified: %v", err)
	}
	if isTransitive(interp) {
		t.Error("no DescribeUnified: default should be naive (exact match)")
	}

	// A dimension with no declared field defaults to naive.
	interp, _, err = InterpreterForDimension(declaresNaive, "nonexistent", "")
	if err != nil {
		t.Fatalf("unknown dimension: %v", err)
	}
	if isTransitive(interp) {
		t.Error("unknown dimension: default should be naive (exact match)")
	}

	// A field declaring an EMPTY interpreter defaults to naive.
	declaresEmpty := interpUnifiedLister{fields: categoriesUnified("")}
	interp, _, err = InterpreterForDimension(declaresEmpty, "categories", "")
	if err != nil {
		t.Fatalf("empty declared interpreter: %v", err)
	}
	if isTransitive(interp) {
		t.Error("empty declared interpreter: default should be naive (exact match)")
	}

	// An unknown override NAME is a loud bad request.
	if _, _, err := InterpreterForDimension(declaresNaive, "categories", "bogus"); err == nil {
		t.Error("unknown override name must reject")
	}
}

// tagListCodec presents a stored []string field verbatim as the type's
// designated tag set — the multi-valued shape NodeTagsPresenter renders.
type tagListCodec struct{ interpreter string }

func (c tagListCodec) Fields() []cgp.UnifiedField {
	return []cgp.UnifiedField{{
		Key: "categories", Kind: cgp.FieldTag,
		Groupable: true, MultiValued: true, Interpreter: c.interpreter,
	}}
}

func (tagListCodec) Format(stored map[string]any) (map[string][]string, error) {
	tags, _ := stored["categories"].([]string)
	if len(tags) == 0 {
		return map[string][]string{}, nil
	}
	return map[string][]string{"categories": tags}, nil
}

func (tagListCodec) Parse(map[string][]string, map[string]any) (map[string]any, error) {
	return nil, nil
}

// tagListLister declares one task type over tagListCodec.
type tagListLister struct {
	interpBase
	interpreter string
}

func (l tagListLister) DescribeUnified() []cgp.NodeTypeUnifiedFields {
	return []cgp.NodeTypeUnifiedFields{{
		Tag:    "task-v1",
		Codecs: []cgp.Codec{tagListCodec{interpreter: l.interpreter}},
	}}
}

// TestNodeTagsPresenter pins the node-view composition (design G12): the
// presenter reads the designated FieldTag field's values and orders them by
// the resolved interpreter's SortKey; a plugin without the capability
// presents no tags (nil presenter, nil error); an unknown [tags] override
// name rejects loudly.
func TestNodeTagsPresenter(t *testing.T) {
	lister := tagListLister{interpreter: "naive"}
	present, err := NodeTagsPresenter(lister, "")
	if err != nil {
		t.Fatalf("NodeTagsPresenter: %v", err)
	}
	if present == nil {
		t.Fatal("presenter must resolve for a tag-declaring lister")
	}
	n := cgp.Node{
		Type:   "task-v1",
		Fields: map[string]any{"categories": []string{"work", "errand", "_ inbox"}},
	}
	got := present(n)
	if want := []string{"_ inbox", "errand", "work"}; !reflect.DeepEqual(got, want) {
		t.Errorf("presented = %v, want SortKey order %v", got, want)
	}
	// The codec's stored slice is never reordered in place.
	if stored := n.Fields["categories"].([]string); !reflect.DeepEqual(
		stored, []string{"work", "errand", "_ inbox"},
	) {
		t.Errorf("stored slice mutated by the presenter: %v", stored)
	}
	// An untagged node presents an empty set (omitted by the views).
	if got := present(cgp.Node{Type: "task-v1", Fields: map[string]any{}}); len(got) != 0 {
		t.Errorf("untagged node presented %v, want none", got)
	}

	// No tag dimension declared → no presenter, no error.
	if p, err := NodeTagsPresenter(interpPlainLister{}, ""); err != nil || p != nil {
		t.Errorf("no-capability lister: presenter nil=%v err=%v, want nil/nil",
			p == nil, err)
	}

	// An unknown override NAME is a loud bad request.
	if _, err := NodeTagsPresenter(lister, "bogus"); err == nil {
		t.Error("unknown [tags] override name must reject")
	}
}

// TestTypeTagSets pins the describe_node_types discovery shape (design G12):
// a tag-declaring type maps to {field, interpreter} with the field default
// resolved, the [tags] override winning, and a no-capability plugin mapping
// to nothing.
func TestTypeTagSets(t *testing.T) {
	lister := tagListLister{interpreter: "naive"}

	got := TypeTagSets(lister, "")
	want := map[string]TagSet{"task-v1": {Field: "categories", Interpreter: "naive"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TypeTagSets = %v, want %v", got, want)
	}

	// The [tags] override wins over the declared default.
	got = TypeTagSets(lister, "dodder-hyphen")
	want = map[string]TagSet{"task-v1": {Field: "categories", Interpreter: "dodder-hyphen"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TypeTagSets(override) = %v, want %v", got, want)
	}

	// An empty declaration resolves to the naive default.
	got = TypeTagSets(tagListLister{interpreter: ""}, "")
	want = map[string]TagSet{"task-v1": {Field: "categories", Interpreter: "naive"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TypeTagSets(empty declared) = %v, want %v", got, want)
	}

	// No capability → nothing to report.
	if got := TypeTagSets(interpPlainLister{}, ""); len(got) != 0 {
		t.Errorf("no-capability TypeTagSets = %v, want empty", got)
	}
}

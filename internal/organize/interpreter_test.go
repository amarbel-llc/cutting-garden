package organize

import (
	"context"
	"net/url"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// interpBase carries the RootLister boilerplate shared by the interpreter-test
// fakes: ListRoots is unused here (interpreterForDimension only reads the schema
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
// interpreterForDimension reads.
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
// A2's TestResolveTagInterpreter does: dodder-hyphen's Matches is transitive along
// the segment path (project matches project-client), naive's is exact.
func isTransitive(interp cgp.TagInterpreter) bool {
	return interp.Matches([]string{"project-client"}, "project")
}

// TestInterpreterForDimension_FieldDefaultAndOverride pins RFC 0019 §4 selection
// (#231 slice 3): the field's declared default resolves the interpreter, the
// global [tags] override wins over it, an absent capability / missing field /
// empty declaration defaults to naive (no error), and an unknown override name is
// a loud bad request.
func TestInterpreterForDimension_FieldDefaultAndOverride(t *testing.T) {
	declaresNaive := interpUnifiedLister{fields: categoriesUnified("naive")}

	// Field declares naive, no override → naive (exact match).
	interp, err := interpreterForDimension(declaresNaive, "categories", "")
	if err != nil {
		t.Fatalf("field default naive, no override: %v", err)
	}
	if isTransitive(interp) {
		t.Error("field default naive, no override: got transitive (dodder-hyphen) semantics")
	}

	// Override wins over the declared field default.
	interp, err = interpreterForDimension(declaresNaive, "categories", "dodder-hyphen")
	if err != nil {
		t.Fatalf("override dodder-hyphen: %v", err)
	}
	if !isTransitive(interp) {
		t.Error("override=dodder-hyphen did not win: got exact-match (naive) semantics")
	}

	// A lister with no UnifiedDescriber capability defaults to naive.
	interp, err = interpreterForDimension(interpPlainLister{}, "categories", "")
	if err != nil {
		t.Fatalf("no DescribeUnified: %v", err)
	}
	if isTransitive(interp) {
		t.Error("no DescribeUnified: default should be naive (exact match)")
	}

	// A dimension with no declared field defaults to naive.
	interp, err = interpreterForDimension(declaresNaive, "nonexistent", "")
	if err != nil {
		t.Fatalf("unknown dimension: %v", err)
	}
	if isTransitive(interp) {
		t.Error("unknown dimension: default should be naive (exact match)")
	}

	// A field declaring an EMPTY interpreter defaults to naive.
	declaresEmpty := interpUnifiedLister{fields: categoriesUnified("")}
	interp, err = interpreterForDimension(declaresEmpty, "categories", "")
	if err != nil {
		t.Fatalf("empty declared interpreter: %v", err)
	}
	if isTransitive(interp) {
		t.Error("empty declared interpreter: default should be naive (exact match)")
	}

	// An unknown override NAME is a loud bad request.
	if _, err := interpreterForDimension(declaresNaive, "categories", "bogus"); err == nil {
		t.Error("unknown override name must reject")
	}
}

package mcp

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// fakeFacetWriteLister adds a FacetWriteDescriber to fakeFacetLister — the
// write-mapping capability describe_node_types folds onto the facet schema.
type fakeFacetWriteLister struct{ fakeFacetLister }

func (fakeFacetWriteLister) DescribeFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	return []cutting_garden_plugins.NodeTypeFacetWrites{{
		Tag: "test-object-v1",
		Writes: []cutting_garden_plugins.FacetWrite{
			{
				DimensionKey:   "status",
				Mode:           cutting_garden_plugins.FacetWriteOne,
				Field:          "STATUS",
				CompletionHint: "sets the status property",
			},
			{DimensionKey: "read", Mode: cutting_garden_plugins.FacetWriteNone},
		},
	}}
}

func findFacetDim(t *testing.T, schemes []schemeSchema, tag, key string) facetDimSchema {
	t.Helper()
	for _, s := range schemes {
		for _, ty := range s.Types {
			if ty.Tag != tag {
				continue
			}
			for _, d := range ty.Facets {
				if d.Key == key {
					return d
				}
			}
		}
	}
	t.Fatalf("dimension %q of type %q not found", key, tag)
	return facetDimSchema{}
}

// TestCollectSchema_FoldsFacetWrites pins that a FacetWriteDescriber's mapping is
// folded onto the facet-dimension schema by key (the organize write-mapping
// capability, RFC 0012 §Write mapping / FDR 0023): a writable dimension carries
// writeMode / field / completionHint, a read-only one carries writeMode "none",
// and a plugin WITHOUT the capability leaves the write fields absent (back-compat).
func TestCollectSchema_FoldsFacetWrites(t *testing.T) {
	schemes := collectSchema([]cutting_garden_plugins.Plugin{fakeFacetWriteLister{}}, "")

	status := findFacetDim(t, schemes, "test-object-v1", "status")
	if status.WriteMode != "one" {
		t.Errorf("status writeMode = %q, want one", status.WriteMode)
	}
	if status.Field != "STATUS" {
		t.Errorf("status field = %q, want STATUS", status.Field)
	}
	if status.CompletionHint == "" {
		t.Error("status completionHint empty, want the declared hint")
	}

	read := findFacetDim(t, schemes, "test-object-v1", "read")
	if read.WriteMode != "none" {
		t.Errorf("read writeMode = %q, want none", read.WriteMode)
	}

	// A plugin without the write capability carries no write metadata.
	plain := collectSchema([]cutting_garden_plugins.Plugin{fakeFacetLister{}}, "")
	if got := findFacetDim(t, plain, "test-object-v1", "status").WriteMode; got != "" {
		t.Errorf("no-write-capability writeMode = %q, want empty", got)
	}
}

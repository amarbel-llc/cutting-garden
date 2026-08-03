package cutting_garden_plugins

import "testing"

func TestValidateFacetWrites(t *testing.T) {
	reads := []NodeTypeFacets{{
		Tag: "obj-v1",
		Dimensions: []FacetDimension{
			{Key: "date", Kind: FacetNumericBucket},
			{Key: "status", Kind: FacetCategorical},
			{Key: "labels", Kind: FacetCategorical, Multi: true},
		},
	}}

	cases := []struct {
		name    string
		writes  []NodeTypeFacetWrites
		wantErr bool
	}{
		{
			name: "consistent mappings pass",
			writes: []NodeTypeFacetWrites{{
				Tag: "obj-v1",
				Writes: []FacetWrite{
					{DimensionKey: "date", Mode: FacetWriteOne, Field: "DTSTART", CompletionHint: "preserves clock time"},
					{DimensionKey: "status", Mode: FacetWriteOne, Field: "STATUS"},
					{DimensionKey: "labels", Mode: FacetWriteMany, Field: "CATEGORIES"},
				},
			}},
		},
		{
			name: "a read-only dimension needs no field",
			writes: []NodeTypeFacetWrites{{
				Tag:    "obj-v1",
				Writes: []FacetWrite{{DimensionKey: "date", Mode: FacetWriteNone}},
			}},
		},
		{
			name: "undeclared tag is rejected",
			writes: []NodeTypeFacetWrites{{
				Tag:    "ghost-v1",
				Writes: []FacetWrite{{DimensionKey: "date", Mode: FacetWriteOne, Field: "DTSTART"}},
			}},
			wantErr: true,
		},
		{
			name: "undeclared dimension key is rejected",
			writes: []NodeTypeFacetWrites{{
				Tag:    "obj-v1",
				Writes: []FacetWrite{{DimensionKey: "nope", Mode: FacetWriteOne, Field: "X"}},
			}},
			wantErr: true,
		},
		{
			name: "a writable dimension without a field is rejected",
			writes: []NodeTypeFacetWrites{{
				Tag:    "obj-v1",
				Writes: []FacetWrite{{DimensionKey: "date", Mode: FacetWriteOne}},
			}},
			wantErr: true,
		},
		{
			name: "an unknown mode is rejected",
			writes: []NodeTypeFacetWrites{{
				Tag:    "obj-v1",
				Writes: []FacetWrite{{DimensionKey: "date", Mode: "sometimes", Field: "DTSTART"}},
			}},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFacetWrites(reads, tc.writes)
			if tc.wantErr && err == nil {
				t.Errorf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

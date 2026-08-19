package cutting_garden_plugins

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// bucketTestCodec is a groupable-only declaration codec: one volatile closed-
// domain band field plus one writable open bucket field whose Parse completes a
// bucket move onto the stored field "raw" (uppercasing, a stand-in for a real
// completion like a date splice).
type bucketTestCodec struct{}

func (bucketTestCodec) Fields() []UnifiedField {
	return []UnifiedField{
		{
			Key: "band", Label: "Band", Kind: FieldNumericBucket, Groupable: true,
			Values:          []FieldValue{{Value: "hot", Order: 2}, {Value: "cold", Order: 1}},
			RevalidateAfter: time.Minute,
		},
		{
			Key: "bucket", Label: "Bucket", Kind: FieldCategorical, Groupable: true,
			Writable: true, Source: "raw", CompletionHint: "uppercases",
		},
	}
}

func (bucketTestCodec) Format(map[string]any) (map[string][]string, error) {
	return map[string][]string{}, nil
}

func (bucketTestCodec) Parse(
	edited map[string][]string, _ map[string]any,
) (map[string]any, error) {
	v, ok := edited["bucket"]
	if !ok || len(v) == 0 {
		return map[string]any{}, nil
	}
	return map[string]any{"raw": strings.ToUpper(v[0])}, nil
}

// DeriveFacetDimensions projects only the GROUPABLE fields, in codec-then-field
// order, carrying the closed domain, terminal values, and volatility through to
// the legacy shape — and maps a date-kind field onto numeric-bucket.
func TestDeriveFacetDimensions(t *testing.T) {
	codecs := []Codec{
		IdentityCodec{Field: UnifiedField{
			Key: "status", Label: "Status", Kind: FieldCategorical,
			Inline: true, Groupable: true, Writable: true,
			TerminalValues: []string{"DONE"},
		}},
		IdentityCodec{Field: UnifiedField{Key: "location", Inline: true}},
		bucketTestCodec{},
	}
	got := DeriveFacetDimensions(codecs)
	want := []FacetDimension{
		{
			Key: "status", Label: "Status", Kind: FacetCategorical,
			TerminalValues: []string{"DONE"},
		},
		{
			Key: "band", Label: "Band", Kind: FacetNumericBucket,
			Values:          []FacetValue{{Key: "hot", Order: 2}, {Key: "cold", Order: 1}},
			RevalidateAfter: time.Minute,
		},
		{Key: "bucket", Label: "Bucket", Kind: FacetCategorical},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveFacetDimensions = %#v, want %#v", got, want)
	}

	if k := facetKindOf(FieldDate); k != FacetNumericBucket {
		t.Errorf("facetKindOf(date) = %q, want numeric-bucket", k)
	}
}

// DeriveFacetWrites declares a read-only groupable field as Mode none (loud
// rejection, never silent absence), a writable one as one/many with the Source
// fallback to Key, and the pre-rendered bucket list from WriteValues with the
// closed-Values-keys fallback.
func TestDeriveFacetWrites(t *testing.T) {
	codecs := []Codec{
		IdentityCodec{Field: UnifiedField{
			Key: "status", Kind: FieldCategorical, Groupable: true, Writable: true,
			WriteValues: []string{"OPEN", "DONE"},
		}},
		IdentityCodec{Field: UnifiedField{
			Key: "tags", Kind: FieldTag, Groupable: true, Writable: true,
			MultiValued: true,
		}},
		IdentityCodec{Field: UnifiedField{
			Key: "prio", Kind: FieldCategorical, Groupable: true, Writable: true,
			Values: []FieldValue{{Value: "hi", Order: 2}, {Value: "lo", Order: 1}},
		}},
		bucketTestCodec{},
	}
	got := DeriveFacetWrites(codecs)
	want := []FacetWrite{
		{DimensionKey: "status", Mode: FacetWriteOne, Field: "status", Values: []string{"OPEN", "DONE"}},
		{DimensionKey: "tags", Mode: FacetWriteMany, Field: "tags"},
		{DimensionKey: "prio", Mode: FacetWriteOne, Field: "prio", Values: []string{"hi", "lo"}},
		{DimensionKey: "band", Mode: FacetWriteNone},
		{DimensionKey: "bucket", Mode: FacetWriteOne, Field: "raw", CompletionHint: "uppercases"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveFacetWrites = %#v, want %#v", got, want)
	}
}

// The derived read + write declarations are mutually consistent by construction —
// the ValidateFacetWrites cross-check a hand-written pair must pass holds for a
// derived pair too.
func TestDeriveFacetWrites_ValidatesAgainstDerivedDimensions(t *testing.T) {
	codecs := []Codec{bucketTestCodec{}}
	reads := []NodeTypeFacets{{Tag: "t", Dimensions: DeriveFacetDimensions(codecs)}}
	writes := []NodeTypeFacetWrites{{Tag: "t", Writes: DeriveFacetWrites(codecs)}}
	if err := ValidateFacetWrites(reads, writes); err != nil {
		t.Fatalf("derived read/write declarations inconsistent: %v", err)
	}
}

// A bucket move routes to the owning codec's Parse with the completion applied;
// a read-only or undeclared dimension is a bad request.
func TestParseUnifiedBucketMove(t *testing.T) {
	codecs := []Codec{
		IdentityCodec{Field: UnifiedField{
			Key: "status", Groupable: true, Inline: true, Writable: true,
		}},
		bucketTestCodec{},
	}

	updates, err := ParseUnifiedBucketMove(codecs, "status", "DONE", nil)
	if err != nil {
		t.Fatalf("status move: %v", err)
	}
	if !reflect.DeepEqual(updates, map[string]any{"status": "DONE"}) {
		t.Fatalf("status move updates = %v, want {status: DONE}", updates)
	}

	updates, err = ParseUnifiedBucketMove(codecs, "bucket", "abc", nil)
	if err != nil {
		t.Fatalf("bucket move: %v", err)
	}
	if !reflect.DeepEqual(updates, map[string]any{"raw": "ABC"}) {
		t.Fatalf("bucket move updates = %v, want {raw: ABC} (completed)", updates)
	}

	if _, err := ParseUnifiedBucketMove(codecs, "band", "hot", nil); err == nil {
		t.Fatal("read-only dimension move: want error, got nil")
	}
	if _, err := ParseUnifiedBucketMove(codecs, "nope", "x", nil); err == nil {
		t.Fatal("undeclared dimension move: want error, got nil")
	}
}

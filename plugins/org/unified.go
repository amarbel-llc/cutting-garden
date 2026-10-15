package org

import (
	"fmt"
	"slices"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The headline type's fields, declared once (FDR 0025); the facet schema,
// the organize write mappings and the box atoms derive from it. Since slice
// 3b the metadata fields are writable through organize, each routed to the
// patch_node key of the same name (FDR 0026 D7-B). Not memoized: the state
// dimension's terminal values come from the configured accounts.
func unifiedFieldSets() []cutting_garden_plugins.NodeTypeUnifiedFields {
	identity := func(f cutting_garden_plugins.UnifiedField) cutting_garden_plugins.Codec {
		return cutting_garden_plugins.IdentityCodec{Field: f}
	}
	band := func(key, label string) cutting_garden_plugins.Codec {
		values := make([]cutting_garden_plugins.FieldValue, len(bandValues))
		for i, v := range bandValues {
			values[i] = cutting_garden_plugins.FieldValue{Value: v}
		}
		return facetOnlyCodec{field: cutting_garden_plugins.UnifiedField{
			Key: key, Label: label,
			Kind:            cutting_garden_plugins.FieldNumericBucket,
			Groupable:       true,
			Values:          values,
			RevalidateAfter: bandRevalidate,
		}}
	}
	date := func(key, label string) cutting_garden_plugins.Codec {
		return headlineFieldCodec{field: cutting_garden_plugins.UnifiedField{
			Key: key, Label: label, Kind: cutting_garden_plugins.FieldDate,
			Groupable: true, Inline: true, Writable: true,
			CompletionHint: "a month or year bucket keeps the day of month (clamped) and the time",
		}}
	}
	return []cutting_garden_plugins.NodeTypeUnifiedFields{{
		Tag: typeHeadline,
		Codecs: []cutting_garden_plugins.Codec{
			headlineFieldCodec{field: cutting_garden_plugins.UnifiedField{
				Key: fieldState, Label: "State",
				Kind:           cutting_garden_plugins.FieldCategorical,
				Groupable:      true,
				Writable:       true,
				TerminalValues: allDoneKeywords(),
				WriteValues:    allKeywords(),
				CompletionHint: "entering or leaving a done keyword adds or removes CLOSED; none clears",
			}},
			tagsCodec{},
			headlineFieldCodec{field: cutting_garden_plugins.UnifiedField{
				Key: fieldPriority, Label: "Priority",
				Kind:        cutting_garden_plugins.FieldCategorical,
				Groupable:   true,
				Inline:      true,
				Writable:    true,
				WriteValues: []string{"A", "B", "C"},
			}},
			date(fieldScheduled, "Scheduled"),
			date(fieldDeadline, "Deadline"),
			identity(cutting_garden_plugins.UnifiedField{
				Key: fieldClosed, Label: "Closed", Kind: cutting_garden_plugins.FieldDate, Groupable: true,
			}),
			band(fieldScheduledBand, "Scheduled"),
			band(fieldDeadlineBand, "Deadline"),
			identity(cutting_garden_plugins.UnifiedField{
				Key: fieldFile, Label: "File",
				Kind:      cutting_garden_plugins.FieldCategorical,
				Groupable: true,
			}),
			headlineFieldCodec{field: cutting_garden_plugins.UnifiedField{
				Key: fieldTitle, Label: "Title",
				Kind:     cutting_garden_plugins.FieldText,
				Trailer:  true,
				Writable: true,
			}},
		},
	}}
}

// headlineCodecs is the headline type's codec set.
func headlineCodecs() []cutting_garden_plugins.Codec { return unifiedFieldSets()[0].Codecs }

// DescribeUnified declares the org plugin's unified field model.
func (Plugin) DescribeUnified() []cutting_garden_plugins.NodeTypeUnifiedFields {
	return unifiedFieldSets()
}

// DescribeFacets derives the facet schema from the unified declaration.
func (Plugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return cutting_garden_plugins.DeriveNodeTypeFacets(unifiedFieldSets())
}

// DescribeFacetWrites derives the organize write mappings. A date a
// headline lacks has no bucket, so moving a headline into the no-value
// section clears that date: the date writes are clearable. State and
// priority carry an explicit "none" bucket instead.
func (Plugin) DescribeFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	writes := cutting_garden_plugins.DeriveNodeTypeFacetWrites(unifiedFieldSets())
	for i := range writes {
		for j, w := range writes[i].Writes {
			if w.DimensionKey == fieldScheduled || w.DimensionKey == fieldDeadline {
				writes[i].Writes[j].Clearable = true
			}
		}
	}
	return writes
}

// DescribeListingFields declares the Node.Fields keys each type carries;
// writability matches the unified declaration.
func (Plugin) DescribeListingFields() []cutting_garden_plugins.NodeTypeListingFields {
	field := func(key, label string) cutting_garden_plugins.ListingField {
		return cutting_garden_plugins.ListingField{Key: key, Label: label}
	}
	writable := func(key, label string) cutting_garden_plugins.ListingField {
		return cutting_garden_plugins.ListingField{Key: key, Label: label, Writable: true}
	}
	return []cutting_garden_plugins.NodeTypeListingFields{
		{Tag: typeFile, Fields: []cutting_garden_plugins.ListingField{field(fieldModified, "Modified")}},
		{Tag: typeHeadline, Fields: []cutting_garden_plugins.ListingField{
			writable(fieldState, "State"),
			writable(fieldPriority, "Priority"),
			{Key: fieldTitle, Label: "Title", Trailer: true, Writable: true},
			writable(fieldScheduled, "Scheduled"),
			writable(fieldDeadline, "Deadline"),
			field(fieldClosed, "Closed"),
			writable(fieldTags, "Tags"),
			field(fieldOwnTags, "Own tags"),
			field(fieldFile, "File"),
			field(fieldLevel, "Level"),
			field(fieldID, "ID"),
			field(fieldIDSynthetic, "Synthetic id"),
		}},
	}
}

// headlineFieldCodec presents one stored headline field and writes it back
// as the patch_node key of the same name: "none" or an empty value clears
// it (null), and a date accepts a month or year bucket, completed with the
// headline's current day of month.
type headlineFieldCodec struct {
	field cutting_garden_plugins.UnifiedField
}

func (c headlineFieldCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{c.field}
}

func (c headlineFieldCodec) Format(stored map[string]any) (map[string][]string, error) {
	s, _ := stored[c.field.Key].(string)
	if s == "" || s == valueNone {
		return map[string][]string{}, nil
	}
	return map[string][]string{c.field.Key: {s}}, nil
}

func (c headlineFieldCodec) Parse(edited map[string][]string, current map[string]any) (map[string]any, error) {
	vals, ok := edited[c.field.Key]
	if !ok {
		return map[string]any{}, nil
	}
	v := ""
	if len(vals) > 0 {
		v = vals[0]
	}
	if v == "" || v == valueNone {
		if c.field.Key == fieldTitle {
			return nil, errors.BadRequestf("org plugin: a headline's title cannot be empty")
		}
		return map[string]any{c.field.Key: nil}, nil
	}
	if c.field.Kind == cutting_garden_plugins.FieldDate {
		cur, _ := current[c.field.Key].(string)
		day, err := completeDateBucket(v, cur)
		if err != nil {
			return nil, err
		}
		v = day
	}
	return map[string]any{c.field.Key: v}, nil
}

// completeDateBucket turns a day, month or year bucket into a day, keeping
// the current date's day of month (and month, for a year), clamped to the
// target month's length; a headline without a date lands on the 1st.
func completeDateBucket(bucket, current string) (string, error) {
	g, ok := cutting_garden_plugins.ParseDateBucket(bucket)
	if !ok {
		return "", errors.BadRequestf("org plugin: %q is not a date (YYYY, YYYY-MM or YYYY-MM-DD)", bucket)
	}
	month, dom := 1, 1
	if t, err := time.Parse("2006-01-02", current); err == nil {
		month, dom = int(t.Month()), t.Day()
	}
	var year int
	switch g {
	case cutting_garden_plugins.GranularityDay:
		return bucket, nil
	case cutting_garden_plugins.GranularityMonth:
		t, _ := time.Parse("2006-01", bucket)
		year, month = t.Year(), int(t.Month())
	default:
		t, _ := time.Parse("2006", bucket)
		year = t.Year()
	}
	last := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return fmt.Sprintf("%04d-%02d-%02d", year, month, min(dom, last)), nil
}

// tagsCodec declares the effective tag set (D4) as the type's designated
// FieldTag under dodder-hyphen (D11). It is writable through the own tags:
// organize hands over the complete new effective set, and the headline's
// own tags become that set minus the inherited ones. Dropping an inherited
// tag cannot be done here — it lives on a parent headline or in
// #+FILETAGS — so it is refused, naming the tag.
type tagsCodec struct{}

func (tagsCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{{
		Key: fieldTags, Label: "Tags",
		Kind:        cutting_garden_plugins.FieldTag,
		Groupable:   true,
		MultiValued: true,
		Writable:    true,
		Interpreter: "dodder-hyphen",
	}}
}

func (tagsCodec) Format(stored map[string]any) (map[string][]string, error) {
	tags := cutting_garden_plugins.StringsOf(stored, fieldTags)
	if len(tags) == 0 {
		return map[string][]string{}, nil
	}
	return map[string][]string{fieldTags: tags}, nil
}

func (tagsCodec) Parse(edited map[string][]string, current map[string]any) (map[string]any, error) {
	want, ok := edited[fieldTags]
	if !ok {
		return map[string]any{}, nil
	}
	own := cutting_garden_plugins.StringsOf(current, fieldOwnTags)
	var inherited []string
	for _, tag := range cutting_garden_plugins.StringsOf(current, fieldTags) {
		if !slices.Contains(own, tag) {
			inherited = append(inherited, tag)
		}
	}
	for _, tag := range inherited {
		if !slices.Contains(want, tag) {
			return nil, errors.BadRequestf(
				"org plugin: tag %q is inherited from a parent headline or #+FILETAGS; remove it there", tag)
		}
	}
	newOwn := []string{}
	for _, tag := range own {
		if slices.Contains(want, tag) {
			newOwn = append(newOwn, tag)
		}
	}
	for _, tag := range want {
		if !slices.Contains(inherited, tag) && !slices.Contains(newOwn, tag) {
			newOwn = append(newOwn, tag)
		}
	}
	return map[string]any{fieldPatchOwnTags: newOwn}, nil
}

// facetOnlyCodec is a groupable dimension computed by the counting path
// with no stored counterpart (the date bands).
type facetOnlyCodec struct {
	field cutting_garden_plugins.UnifiedField
}

func (c facetOnlyCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{c.field}
}

func (facetOnlyCodec) Format(map[string]any) (map[string][]string, error) {
	return map[string][]string{}, nil
}

func (c facetOnlyCodec) Parse(map[string][]string, map[string]any) (map[string]any, error) {
	return nil, errors.BadRequestf("org plugin: %s is not writable", c.field.Key)
}

package cutting_garden_plugin_ytdlp

import (
	"context"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestPlugin_DescribeFacets_DeclaresVideoDimensions(t *testing.T) {
	facets := Plugin{}.DescribeFacets()
	if len(facets) != 1 || facets[0].Tag != typeVideo {
		t.Fatalf("DescribeFacets() = %+v, want one entry tagged %q", facets, typeVideo)
	}
	keys := map[string]cutting_garden_plugins.FacetDimension{}
	for _, d := range facets[0].Dimensions {
		keys[d.Key] = d
	}
	for _, want := range []string{facetUploader, facetYear, facetMonth, facetDurationBand} {
		if _, ok := keys[want]; !ok {
			t.Errorf("DescribeFacets() missing dimension %q", want)
		}
	}
	band := keys[facetDurationBand]
	if len(band.Values) != 3 {
		t.Errorf("duration_band Values = %+v, want a closed 3-value domain", band.Values)
	}
	if keys[facetYear].Values != nil {
		t.Errorf("year dimension declares a closed Values set %+v, want open (nil)", keys[facetYear].Values)
	}
}

func TestPlugin_FacetCounts_FoldsChannelEntries(t *testing.T) {
	installFlatPlaylistFake(t)

	result, ok, err := Plugin{}.FacetCounts(
		context.Background(),
		mustParseURL(t, "https://www.youtube.com/@channel"),
		nil,
	)
	if err != nil {
		t.Fatalf("FacetCounts: %v", err)
	}
	if !ok {
		t.Fatal("FacetCounts ok = false, want true")
	}
	if !result.Complete {
		t.Error("result.Complete = false, want true (one-shot probe has every entry)")
	}

	// v1 (100s, 2026-01), v2 (1500s, 2026-07), v3 (no duration, no date).
	if got := result.Summary[facetUploader]["Chan"]; got != 3 {
		t.Errorf("uploader[Chan] = %d, want 3", got)
	}
	if got := result.Summary[facetYear]["2026"]; got != 2 {
		t.Errorf("year[2026] = %d, want 2 (v3 has no upload_date)", got)
	}
	if got := result.Summary[facetMonth]["2026-01"]; got != 1 {
		t.Errorf("month[2026-01] = %d, want 1", got)
	}
	if got := result.Summary[facetMonth]["2026-07"]; got != 1 {
		t.Errorf("month[2026-07] = %d, want 1", got)
	}
	if got := result.Summary[facetDurationBand][durationBandShort]; got != 1 {
		t.Errorf("duration_band[short] = %d, want 1 (v1, 100s)", got)
	}
	if got := result.Summary[facetDurationBand][durationBandLong]; got != 1 {
		t.Errorf("duration_band[long] = %d, want 1 (v2, 1500s)", got)
	}
	// v3 has no duration at all: it must not appear in ANY duration_band
	// bucket, since flatPlaylistEntry.Duration is nil (not a synthetic
	// zero-second entry).
	total := int64(0)
	for _, n := range result.Summary[facetDurationBand] {
		total += n
	}
	if total != 2 {
		t.Errorf("duration_band total = %d, want 2 (v3 contributes nothing)", total)
	}
}

func TestPlugin_FacetCounts_AppliesFilter(t *testing.T) {
	installFlatPlaylistFake(t)

	result, ok, err := Plugin{}.FacetCounts(
		context.Background(),
		mustParseURL(t, "https://www.youtube.com/@channel"),
		cutting_garden_plugins.FacetFilter{{Dimension: facetDurationBand, Value: durationBandShort}},
	)
	if err != nil {
		t.Fatalf("FacetCounts: %v", err)
	}
	if !ok {
		t.Fatal("FacetCounts ok = false, want true")
	}
	if got := result.Summary[facetUploader]["Chan"]; got != 1 {
		t.Errorf("filtered uploader[Chan] = %d, want 1 (only v1 matches duration_band=short)", got)
	}
}

func TestPlugin_FacetCounts_NilNodeErrors(t *testing.T) {
	if _, _, err := (Plugin{}).FacetCounts(context.Background(), nil, nil); err == nil {
		t.Error("FacetCounts(nil) returned nil error")
	}
}

func TestDurationBandOf(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{0, durationBandShort},
		{239, durationBandShort},
		{240, durationBandMedium},
		{1199, durationBandMedium},
		{1200, durationBandLong},
		{9999, durationBandLong},
		{-1, ""},
	}
	for _, tc := range cases {
		if key, _ := durationBandOf(tc.seconds); key != tc.want {
			t.Errorf("durationBandOf(%v) = %q, want %q", tc.seconds, key, tc.want)
		}
	}
}

func TestYearOfMonthOf(t *testing.T) {
	if got := yearOf("20260718"); got != "2026" {
		t.Errorf("yearOf(20260718) = %q, want 2026", got)
	}
	if got := yearOf(""); got != "" {
		t.Errorf("yearOf(\"\") = %q, want empty", got)
	}
	if key, order := monthOf("20260718"); key != "2026-07" || order != 202607 {
		t.Errorf("monthOf(20260718) = (%q, %d), want (2026-07, 202607)", key, order)
	}
	if key, _ := monthOf(""); key != "" {
		t.Errorf("monthOf(\"\") = %q, want empty", key)
	}
	if key, _ := monthOf("20261301"); key != "" {
		t.Errorf("monthOf with month=13 = %q, want empty (rejected)", key)
	}
}

func TestEntryFacets_AbsentFieldsContributeNothing(t *testing.T) {
	e := flatPlaylistEntry{ID: "x", Title: "no metadata"}
	facets := entryFacets(e)
	if facets != nil {
		t.Errorf("entryFacets(no uploader/date/duration) = %+v, want nil", facets)
	}
}

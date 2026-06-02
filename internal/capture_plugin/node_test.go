package capture_plugin

import "testing"

func TestBuildNode_MetadataOnly(t *testing.T) {
	got := string(BuildNode("cutting_garden-capture-identity-v1", []Ref{
		{Alias: "invocation", Digest: "sha256-inv", TypeString: "jcs-cutting_garden-capture-invocation-v1"},
		{Alias: "environment", Digest: "sha256-env", TypeString: "cutting_garden-capture-environment-v1"},
	}, nil))

	want := "---\n" +
		"- invocation < @sha256-inv !jcs-cutting_garden-capture-invocation-v1\n" +
		"- environment < @sha256-env !cutting_garden-capture-environment-v1\n" +
		"! cutting_garden-capture-identity-v1\n" +
		"---\n"

	if got != want {
		t.Errorf("metadata-only node mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestBuildNode_WithBody(t *testing.T) {
	got := string(BuildNode("jcs-cutting_garden-capture-outcome-v1", nil,
		[]byte(`{"datetime":"2026-06-02T00:00:00.000Z"}`)))

	want := "---\n" +
		"! jcs-cutting_garden-capture-outcome-v1\n" +
		"---\n" +
		"\n" +
		`{"datetime":"2026-06-02T00:00:00.000Z"}` + "\n"

	if got != want {
		t.Errorf("bodied node mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestJCS_SortsKeysNoHTMLEscape(t *testing.T) {
	got, err := JCS(map[string]any{
		"target":    "https://example.com/a?x=1&y=2",
		"format":    "object-graph",
		"normalize": false,
		"options":   map[string]any{},
	})
	if err != nil {
		t.Fatalf("JCS: %v", err)
	}
	// Keys sorted (format < normalize < options < target); `&` not
	// escaped; no insignificant whitespace; empty object as {}.
	want := `{"format":"object-graph","normalize":false,"options":{},"target":"https://example.com/a?x=1&y=2"}`
	if string(got) != want {
		t.Errorf("JCS mismatch:\n got=%s\nwant=%s", got, want)
	}
}

package organize

import (
	"strings"
	"testing"
)

// TestProvenanceWrapsCommandInBackticks pins cutting-garden#243: the generated
// `% generated:` note wraps the echoed command in backticks so it renders as
// code and copy-pastes unambiguously, for both the query and no-query spellings.
func TestProvenanceWrapsCommandInBackticks(t *testing.T) {
	noQuery := provenance("status", "", "caldav:task")
	if noQuery != "generated: `cg organize -group-by status caldav:task`" {
		t.Errorf("no-query provenance = %q", noQuery)
	}

	withQuery := provenance("categories", "_terminal=no", "caldav:task")
	if !strings.HasPrefix(withQuery, "generated: `cg organize") {
		t.Errorf("query provenance must open the backtick before the command: %q", withQuery)
	}
	if !strings.HasSuffix(withQuery, "`") {
		t.Errorf("query provenance must close the backtick after the uri: %q", withQuery)
	}
}

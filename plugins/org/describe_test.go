package org

import (
	"strings"
	"testing"
)

// The create ops' side effects are in the schema an agent reads before it
// creates: the provenance tag, the :CREATED: stamp, and how to get rid of
// the tag.
func TestDescribeBodies_CreateNamesItsSideEffects(t *testing.T) {
	var accepts []string
	for _, b := range (Plugin{}).DescribeBodies() {
		if b.Tag == typeHeadline {
			accepts = b.Accepts
		}
	}
	var create string
	for _, a := range accepts {
		if strings.Contains(a, "create ops") {
			create = a
		}
	}
	if create == "" {
		t.Fatalf("no create entry among %q", accepts)
	}
	for _, want := range []string{
		agentCapturedTag, ":CREATED:", "provenance", "own_tags", "no opt-out", "parent",
	} {
		if !strings.Contains(create, want) {
			t.Errorf("create text lacks %q:\n%s", want, create)
		}
	}
}

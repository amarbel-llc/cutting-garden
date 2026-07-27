package jira

import (
	"context"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// TestCreateChild creates an issue under a project container and asserts the
// server-assigned key round-trips as the returned URI and the issue was
// stored.
func TestCreateChild(t *testing.T) {
	f, baseURI := startFake(t)

	created, err := (Plugin{}).CreateChild(
		context.Background(),
		mustParseURL(t, baseURI+"/PROJ"),
		strings.NewReader(`{"issuetype":"Task","summary":"New issue"}`),
		typeIssue,
	)
	if err != nil {
		t.Fatalf("CreateChild: %v", err)
	}
	if !strings.HasSuffix(created.String(), "/PROJ/PROJ-1") {
		t.Errorf("created URI = %q, want a /PROJ/PROJ-1 suffix", created)
	}
	if _, ok := f.issues["PROJ-1"]; !ok {
		t.Error("PROJ-1 not stored after CreateChild")
	}
}

// TestCreateChild_RequiresProjectContainer rejects a create whose target is
// the bare root or an issue rather than a project.
func TestCreateChild_RequiresProjectContainer(t *testing.T) {
	_, baseURI := startFake(t)

	for _, target := range []string{baseURI, baseURI + "/PROJ/PROJ-1"} {
		_, err := (Plugin{}).CreateChild(
			context.Background(),
			mustParseURL(t, target),
			strings.NewReader(`{"issuetype":"Task","summary":"x"}`),
			typeIssue,
		)
		if !errors.Is400BadRequest(err) {
			t.Errorf("CreateChild(%q) err = %v, want a 400 bad request", target, err)
		}
	}
}

// TestCreateChild_RequiresIssueTypeAndSummary rejects a create body missing
// the required fields.
func TestCreateChild_RequiresIssueTypeAndSummary(t *testing.T) {
	_, baseURI := startFake(t)

	_, err := (Plugin{}).CreateChild(
		context.Background(),
		mustParseURL(t, baseURI+"/PROJ"),
		strings.NewReader(`{"summary":"no type"}`),
		typeIssue,
	)
	if !errors.Is400BadRequest(err) {
		t.Errorf("CreateChild without issuetype err = %v, want a 400 bad request", err)
	}
}

// TestCreateNodeAndPutNodeRejected pins that the two verbs that do not map to
// Jira issues refuse with a caller-fault error rather than silently no-op.
func TestCreateNodeAndPutNodeRejected(t *testing.T) {
	_, baseURI := startFake(t)
	node := mustParseURL(t, baseURI+"/PROJ/PROJ-1")

	if err := (Plugin{}).CreateNode(
		context.Background(), node, strings.NewReader(`{}`), typeIssue,
	); !errors.Is400BadRequest(err) {
		t.Errorf("CreateNode err = %v, want a 400 bad request", err)
	}
	if err := (Plugin{}).PutNode(
		context.Background(), node, strings.NewReader(`{}`),
	); !errors.Is400BadRequest(err) {
		t.Errorf("PutNode err = %v, want a 400 bad request", err)
	}
}

// TestPatchNode_FieldsAndStatus patches every recognized field: the
// non-status fields land in one PUT, status resolves to a workflow
// transition, and applied reports exactly the recognized keys (sorted).
func TestPatchNode_FieldsAndStatus(t *testing.T) {
	f, baseURI := startFake(t)
	f.seed("PROJ-1", "Old summary")

	applied, err := (Plugin{}).PatchNode(
		context.Background(),
		mustParseURL(t, baseURI+"/PROJ/PROJ-1"),
		strings.NewReader(`{"summary":"New","priority":"High","labels":["triage"],"status":"Done"}`),
	)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	want := []string{"labels", "priority", "status", "summary"}
	if !slices.Equal(applied, want) {
		t.Errorf("applied = %v, want %v", applied, want)
	}

	put := f.patched["PROJ-1"]
	if put["summary"] != "New" {
		t.Errorf("put summary = %v, want New", put["summary"])
	}
	if _, ok := put["priority"]; !ok {
		t.Error("put missing priority")
	}
	if _, ok := put["labels"]; !ok {
		t.Error("put missing labels")
	}
	// status must NOT ride the field PUT — it is applied via a transition.
	if _, ok := put["status"]; ok {
		t.Error("status leaked into the field PUT; it must go through a transition")
	}
	if got := f.transitioned["PROJ-1"]; !slices.Equal(got, []string{"31"}) {
		t.Errorf("transitioned = %v, want [31] (the Done transition)", got)
	}
}

// TestPatchNode_UnrecognizedOnly: a body naming no recognized field applies
// nothing, issues no network call, and reports a non-nil empty applied
// (cutting-garden#182).
func TestPatchNode_UnrecognizedOnly(t *testing.T) {
	f, baseURI := startFake(t)
	f.seed("PROJ-1", "Untouched")

	applied, err := (Plugin{}).PatchNode(
		context.Background(),
		mustParseURL(t, baseURI+"/PROJ/PROJ-1"),
		strings.NewReader(`{"nonsense":1,"also":"ignored"}`),
	)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if applied == nil || len(applied) != 0 {
		t.Errorf("applied = %v, want a non-nil empty slice", applied)
	}
	if _, patched := f.patched["PROJ-1"]; patched {
		t.Error("an unrecognized-only patch issued a field PUT")
	}
	if _, transitioned := f.transitioned["PROJ-1"]; transitioned {
		t.Error("an unrecognized-only patch issued a transition")
	}
}

// TestPatchNode_StatusWithoutTransition: a status target with no available
// workflow transition is a caller-fault bad request, not a silent no-op.
func TestPatchNode_StatusWithoutTransition(t *testing.T) {
	f, baseURI := startFake(t)
	f.seed("PROJ-1", "x")

	_, err := (Plugin{}).PatchNode(
		context.Background(),
		mustParseURL(t, baseURI+"/PROJ/PROJ-1"),
		strings.NewReader(`{"status":"Nonexistent"}`),
	)
	if !errors.Is400BadRequest(err) {
		t.Errorf("patch to an unreachable status err = %v, want a 400 bad request", err)
	}
}

// TestPatchNode_NotAnIssue: the mutation verbs address issues only, so a
// project (or root) node is a caller mistake.
func TestPatchNode_NotAnIssue(t *testing.T) {
	_, baseURI := startFake(t)

	_, err := (Plugin{}).PatchNode(
		context.Background(),
		mustParseURL(t, baseURI+"/PROJ"),
		strings.NewReader(`{"summary":"x"}`),
	)
	if !errors.Is400BadRequest(err) {
		t.Errorf("patch of a project node err = %v, want a 400 bad request", err)
	}
}

// TestDeleteNode removes an issue.
func TestDeleteNode(t *testing.T) {
	f, baseURI := startFake(t)
	f.seed("PROJ-1", "doomed")

	if err := (Plugin{}).DeleteNode(
		context.Background(), mustParseURL(t, baseURI+"/PROJ/PROJ-1"),
	); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if _, ok := f.issues["PROJ-1"]; ok {
		t.Error("PROJ-1 still present after DeleteNode")
	}
}

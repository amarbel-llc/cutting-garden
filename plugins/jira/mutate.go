package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var (
	_ cutting_garden_plugins.NodeMutator      = (*Plugin)(nil)
	_ cutting_garden_plugins.ContainerCreator = (*Plugin)(nil)
	_ cutting_garden_plugins.BodyDescriber    = (*Plugin)(nil)
)

// DescribeBodies is the schema-discovery half of jira's write capability
// (the describe_node_types tool). It declares the issue leaf as writable and
// ServerAssignedIdentity — Jira assigns the key, so the create_node tool
// routes creation to CreateChild under a project rather than CreateNode. The
// Accepts line documents both payloads: the create body (issuetype/summary/
// priority?/labels?) and the flat patch body (any of summary/priority/
// labels/status).
func (Plugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	return []cutting_garden_plugins.NodeTypeBody{
		{
			Tag: typeIssue,
			Accepts: []string{
				"application/json — create: {\"issuetype\":…, \"summary\":…," +
					" \"priority\"?:…, \"labels\"?:[…]}; patch: any of" +
					" {\"summary\", \"priority\", \"labels\", \"status\"}" +
					" (status is applied via the workflow transition API)",
			},
			ServerAssignedIdentity: true,
			Example: map[string]any{
				"issuetype": "Task",
				"summary":   "Investigate flaky test",
				"priority":  "High",
				"labels":    []string{"triage"},
			},
		},
	}
}

// CreateChild creates a new Jira issue under a project container. Jira
// assigns the issue key (PROJ-N), so creation is the ContainerCreator
// capability (FDR 0019 #143): the caller cannot choose the URI, and the
// server-assigned key is returned as the created node's URI. The container
// MUST be a project (jira://host/PROJECT) — not the bare host root or an
// issue. The body is issue JSON ({issuetype, summary, priority?, labels?});
// issuetype and summary are required (Jira rejects a create without them).
func (Plugin) CreateChild(
	ctx context.Context,
	container *url.URL,
	body io.Reader,
	typ string,
) (*url.URL, error) {
	if container == nil {
		return nil, errors.ErrorWithStackf(
			"jira plugin: CreateChild requires a container URI",
		)
	}
	switch typ {
	case typeIssue, "":
		// An issue leaf — the supported case.
	default:
		return nil, errors.BadRequestf(
			"jira plugin: cannot create node of type %q (want %s)",
			typ, typeIssue,
		)
	}

	base, username, token, err := connectionFromArg(container)
	if err != nil {
		return nil, err
	}
	origin, project, issue, err := nodeFromBase(base)
	if err != nil {
		return nil, err
	}
	if project == "" || issue != "" {
		return nil, errors.BadRequestf(
			"jira plugin: CreateChild target %q is not a project container "+
				"(want jira://host/PROJECT)", container,
		)
	}

	fields, err := createFieldsFromBody(body, project)
	if err != nil {
		return nil, err
	}

	c := newClient(origin, username, token)
	key, err := c.createIssue(ctx, fields)
	if err != nil {
		return nil, err
	}
	return jiraURIForNode(origin, project, key), nil
}

// CreateNode is unsupported for jira: Jira assigns issue keys, so an issue
// cannot be created at a caller-chosen URI. Creation goes through
// CreateChild (the create tool routes a server-assigned type there), which
// returns the assigned key. Rejecting is a caller-fault -32602, not a
// silent no-op.
func (Plugin) CreateNode(
	_ context.Context, _ *url.URL, _ io.Reader, _ string,
) error {
	return errors.BadRequestf(
		"jira plugin: CreateNode is not supported — Jira assigns issue keys;" +
			" create an issue with create_child under a project" +
			" (jira://host/PROJECT)",
	)
}

// PutNode is unsupported for jira: an issue is not a wholesale-replaceable
// document (no full-replace endpoint, and a blind overwrite would clobber
// workflow state, comments, and server-managed fields). Field changes go
// through PatchNode.
func (Plugin) PutNode(_ context.Context, _ *url.URL, _ io.Reader) error {
	return errors.BadRequestf(
		"jira plugin: PutNode (full replace) is not supported — an issue is" +
			" not a wholesale-replaceable document; use patch to change fields",
	)
}

// issuePatchFields is the recognized patch key set (cutting-garden#182),
// SORTED so the applied report is deterministic. A patch body may name other
// keys — they are TOLERATED but never reported in applied, so the caller can
// always tell what actually landed. status is recognized here but applied
// via the workflow transition API, not a field write (see PatchNode).
var issuePatchFields = []string{"labels", "priority", "status", "summary"}

// PatchNode partially updates an existing Jira issue. The body is a flat
// JSON object of the fields to change: summary (string), priority (a
// priority name, e.g. "High"), labels (string array), status (a target
// status name, applied via the workflow transition API since status is not
// a settable field). Only recognized fields present in the body are
// applied; unknown fields are tolerated but excluded from applied
// (cutting-garden#182). An empty body is a bad request; a body naming zero
// recognized fields applies nothing and issues no network call, reporting a
// non-nil empty applied rather than a bare success.
func (Plugin) PatchNode(
	ctx context.Context, node *url.URL, body io.Reader,
) ([]string, error) {
	c, key, err := clientAndIssueForNode(node)
	if err != nil {
		return nil, err
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.BadRequestf(
			"jira plugin: PatchNode body must be issue-field JSON; got empty body",
		)
	}

	var patch map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &patch); err != nil {
		return nil, errors.BadRequestf("jira plugin: invalid patch JSON: %s", err)
	}

	applied := recognizedFields(patch, issuePatchFields)
	if len(applied) == 0 {
		// Nothing we understand — no network round-trip, honest empty applied.
		return applied, nil
	}

	// Split the recognized fields into the PUT payload (summary/priority/
	// labels) and the status transition, decoding each with its own type so
	// a wrong-typed value is a caller-fault error (cutting-garden#185).
	putFields := map[string]any{}
	var statusTarget string
	for _, field := range applied {
		switch field {
		case "summary":
			var s string
			if err := json.Unmarshal(patch["summary"], &s); err != nil {
				return nil, errors.BadRequestf("jira plugin: patch summary: %s", err)
			}
			putFields["summary"] = s
		case "priority":
			var name string
			if err := json.Unmarshal(patch["priority"], &name); err != nil {
				return nil, errors.BadRequestf("jira plugin: patch priority: %s", err)
			}
			putFields["priority"] = map[string]any{"name": name}
		case "labels":
			var labels []string
			if err := json.Unmarshal(patch["labels"], &labels); err != nil {
				return nil, errors.BadRequestf("jira plugin: patch labels: %s", err)
			}
			putFields["labels"] = labels
		case "status":
			if err := json.Unmarshal(patch["status"], &statusTarget); err != nil {
				return nil, errors.BadRequestf("jira plugin: patch status: %s", err)
			}
		}
	}

	if len(putFields) > 0 {
		if err := c.updateIssueFields(ctx, key, putFields); err != nil {
			return nil, err
		}
	}
	if statusTarget != "" {
		if err := applyStatusTransition(ctx, c, key, statusTarget); err != nil {
			return nil, err
		}
	}

	return applied, nil
}

// DeleteNode removes the Jira issue addressed by the node URI.
func (Plugin) DeleteNode(ctx context.Context, node *url.URL) error {
	c, key, err := clientAndIssueForNode(node)
	if err != nil {
		return err
	}
	return c.deleteIssue(ctx, key)
}

// applyStatusTransition moves an issue to the target status by resolving the
// status NAME to an available workflow transition and applying it. Jira
// exposes status changes only through these transitions (not a field write),
// and the available set depends on the issue's current status, so a target
// with no available transition is a caller-fault bad request naming what WAS
// available — not a silent no-op.
func applyStatusTransition(
	ctx context.Context, c *client, key, target string,
) error {
	transitions, err := c.transitions(ctx, key)
	if err != nil {
		return err
	}
	for _, t := range transitions {
		if strings.EqualFold(t.To.Name, target) {
			return c.doTransition(ctx, key, t.ID)
		}
	}

	available := make([]string, 0, len(transitions))
	for _, t := range transitions {
		available = append(available, t.To.Name)
	}
	return errors.BadRequestf(
		"jira plugin: no workflow transition to status %q is available for %s"+
			" (available: %s)", target, key, strings.Join(available, ", "),
	)
}

// clientAndIssueForNode resolves a jira node URI to a credentialed client
// and the issue key it targets. A node that is not an issue (the bare root
// or a project) is a caller mistake: the mutation verbs address issues only.
func clientAndIssueForNode(node *url.URL) (*client, string, error) {
	if node == nil {
		return nil, "", errors.ErrorWithStackf(
			"jira plugin: mutation requires a node URI",
		)
	}
	base, username, token, err := connectionFromArg(node)
	if err != nil {
		return nil, "", err
	}
	origin, _, issue, err := nodeFromBase(base)
	if err != nil {
		return nil, "", err
	}
	if issue == "" {
		return nil, "", errors.BadRequestf(
			"jira plugin: %q is not an issue node (want"+
				" jira://host/PROJECT/ISSUE-KEY)", node,
		)
	}
	return newClient(origin, username, token), issue, nil
}

// createBody is the CreateChild issue JSON: the caller-supplied fields of a
// new issue. issuetype and summary are required; priority and labels are
// optional.
type createBody struct {
	IssueType string   `json:"issuetype"`
	Summary   string   `json:"summary"`
	Priority  string   `json:"priority"`
	Labels    []string `json:"labels"`
}

// createFieldsFromBody parses a CreateChild body into the Jira create-issue
// fields map, injecting the project from the container URI.
func createFieldsFromBody(r io.Reader, project string) (map[string]any, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.BadRequestf(
			"jira plugin: CreateChild body must be issue JSON; got empty body",
		)
	}
	var cb createBody
	if err := json.Unmarshal(trimmed, &cb); err != nil {
		return nil, errors.BadRequestf("jira plugin: invalid issue JSON: %s", err)
	}
	if cb.IssueType == "" || cb.Summary == "" {
		return nil, errors.BadRequestf(
			"jira plugin: CreateChild requires issuetype and summary",
		)
	}
	fields := map[string]any{
		"project":   map[string]any{"key": project},
		"issuetype": map[string]any{"name": cb.IssueType},
		"summary":   cb.Summary,
	}
	if cb.Priority != "" {
		fields["priority"] = map[string]any{"name": cb.Priority}
	}
	if cb.Labels != nil {
		fields["labels"] = cb.Labels
	}
	return fields, nil
}

// recognizedFields returns the subset of supported keys present in the patch
// body, preserving supported's (sorted) order so the applied report is
// deterministic. Always non-nil: jira DOES report applied fields, so an
// empty result is the authoritative "nothing applied" rather than "did not
// report" (cutting-garden#182).
func recognizedFields(
	fields map[string]json.RawMessage, supported []string,
) []string {
	recognized := make([]string, 0, min(len(fields), len(supported)))
	for _, key := range supported {
		if _, ok := fields[key]; ok {
			recognized = append(recognized, key)
		}
	}
	return recognized
}

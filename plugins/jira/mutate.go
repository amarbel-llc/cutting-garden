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
					" \"priority\"?:…, \"labels\"?:[…], \"assignee\"?:…," +
					" \"description\"?:…}; patch: any of {\"summary\"," +
					" \"priority\", \"labels\", \"assignee\", \"description\"," +
					" \"status\"}. assignee is a name/email (resolved to an" +
					" accountId); description is plain text or a raw ADF object;" +
					" status is applied via the workflow transition API",
			},
			ServerAssignedIdentity: true,
			Example: map[string]any{
				"issuetype":   "Task",
				"summary":     "Investigate flaky test",
				"priority":    "High",
				"labels":      []string{"triage"},
				"assignee":    "alice@example.com",
				"description": "Repro steps in the linked CI run.",
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

	c := newClient(origin, username, token)
	fields, err := createFieldsFromBody(ctx, c, body, project)
	if err != nil {
		return nil, err
	}

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
// always tell what actually landed. Two fields are not plain field writes:
// status is applied via the workflow transition API, and assignee resolves a
// name/email to an accountId via user search (both pre-resolved before the
// PUT; see PatchNode).
var issuePatchFields = []string{
	"assignee", "description", "labels", "priority", "status", "summary",
}

// PatchNode partially updates an existing Jira issue. The body is a flat
// JSON object of the fields to change:
//
//   - summary — a string;
//   - priority — a priority name, e.g. "High";
//   - labels — a string array;
//   - assignee — a user name or email; resolved to a Jira accountId via user
//     search (Jira Cloud addresses users by accountId, not name);
//   - description — a plain-text string (wrapped in a minimal ADF document)
//     or a raw ADF object (used verbatim); Jira descriptions are ADF;
//   - status — a target status name, applied via the workflow transition API
//     since status is not a settable field.
//
// Only recognized fields present in the body are applied; unknown fields are
// tolerated but excluded from applied (cutting-garden#182). An empty body is
// a bad request; a body naming zero recognized fields applies nothing and
// issues no network call, reporting a non-nil empty applied rather than a
// bare success. status and assignee are pre-resolved (pure GETs) BEFORE the
// field PUT, so a bad status/assignee fails without a partial mutation.
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

	applied := cutting_garden_plugins.RecognizedPatchFields(patch, issuePatchFields)
	if len(applied) == 0 {
		// Nothing we understand — no network round-trip, honest empty applied.
		return applied, nil
	}

	// Split the recognized fields into the PUT payload (summary/priority/
	// labels) and the status transition, decoding each with its own type so
	// a wrong-typed value is a caller-fault error (cutting-garden#185).
	putFields := map[string]any{}
	var (
		statusTarget  string
		hasStatus     bool
		assigneeQuery string
		hasAssignee   bool
	)
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
		case "description":
			adf, err := descriptionToADF(patch["description"])
			if err != nil {
				return nil, err
			}
			putFields["description"] = adf
		case "assignee":
			// The value is a name/email QUERY; resolving it to an accountId is
			// a pure GET pre-resolved before the PUT (see below).
			hasAssignee = true
			if err := json.Unmarshal(patch["assignee"], &assigneeQuery); err != nil {
				return nil, errors.BadRequestf("jira plugin: patch assignee: %s", err)
			}
		case "status":
			hasStatus = true
			if err := json.Unmarshal(patch["status"], &statusTarget); err != nil {
				return nil, errors.BadRequestf("jira plugin: patch status: %s", err)
			}
		}
	}

	// Resolve the status transition BEFORE any field write. It is a pure GET,
	// so validating it first means a bad or empty status name fails without a
	// partial mutation (Jira has no cross-call transaction). It also fixes the
	// asymmetry where an empty status — recognized and reported in applied —
	// would otherwise silently do nothing: an empty target matches no
	// transition and is refused here, so applied never claims a write that did
	// not happen. Gated on status PRESENCE (hasStatus), not on a non-empty
	// value.
	var transitionID string
	if hasStatus {
		transitionID, err = resolveStatusTransition(ctx, c, key, statusTarget)
		if err != nil {
			return nil, err
		}
	}
	if hasAssignee {
		accountID, err := resolveAssignee(ctx, c, assigneeQuery)
		if err != nil {
			return nil, err
		}
		putFields["assignee"] = map[string]any{"accountId": accountID}
	}

	if len(putFields) > 0 {
		if err := c.updateIssueFields(ctx, key, putFields); err != nil {
			return nil, err
		}
	}
	if hasStatus {
		if err := c.doTransition(ctx, key, transitionID); err != nil {
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

// resolveStatusTransition resolves a target status NAME to an available
// workflow transition id WITHOUT applying it — the pure GET PatchNode runs
// before any field write so a bad status name fails without a partial
// mutation. Jira exposes status changes only through these transitions (not
// a field write), and the available set depends on the issue's current
// status, so a target with no available transition — an empty or misspelled
// name — is a caller-fault bad request naming what WAS available, never a
// silent no-op.
func resolveStatusTransition(
	ctx context.Context, c *client, key, target string,
) (string, error) {
	transitions, err := c.transitions(ctx, key)
	if err != nil {
		return "", err
	}
	for _, t := range transitions {
		if strings.EqualFold(t.To.Name, target) {
			return t.ID, nil
		}
	}

	available := make([]string, 0, len(transitions))
	for _, t := range transitions {
		available = append(available, t.To.Name)
	}
	return "", errors.BadRequestf(
		"jira plugin: no workflow transition to status %q is available for %s"+
			" (available: %s)", target, key, strings.Join(available, ", "),
	)
}

// resolveAssignee resolves a user name/email query to a single Jira accountId
// via user search — Jira Cloud addresses users by accountId, not name, so a
// human-friendly assignee must be looked up. Exactly one match is required:
// zero is a caller-fault "no such user", and multiple is refused unless the
// query is an exact email match for one of them (the unambiguous
// disambiguator). A pure GET, so callers run it before any write.
func resolveAssignee(ctx context.Context, c *client, query string) (string, error) {
	users, err := c.searchUsers(ctx, query)
	if err != nil {
		return "", err
	}
	switch len(users) {
	case 0:
		return "", errors.BadRequestf(
			"jira plugin: no user matches assignee %q", query,
		)
	case 1:
		return users[0].AccountID, nil
	default:
		for _, u := range users {
			if strings.EqualFold(u.EmailAddress, query) {
				return u.AccountID, nil
			}
		}
		return "", errors.BadRequestf(
			"jira plugin: assignee %q is ambiguous (%d users match) — use an"+
				" exact email address to disambiguate", query, len(users),
		)
	}
}

// descriptionToADF converts a description patch/create value to the Atlassian
// Document Format Jira Cloud requires. A JSON STRING is plain text, wrapped
// in a minimal single-paragraph ADF document (an empty string produces an
// empty doc, which clears the description); a JSON OBJECT is a raw ADF
// document, used verbatim. Anything else is a caller-fault bad request. A
// richer plain-text mapping (paragraphs on blank lines, hard breaks on
// newlines) is a future refinement.
func descriptionToADF(raw json.RawMessage) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.BadRequestf("jira plugin: description is empty")
	}
	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, errors.BadRequestf("jira plugin: description text: %s", err)
		}
		content := []any{}
		if text != "" {
			content = []any{
				map[string]any{
					"type": "paragraph",
					"content": []any{
						map[string]any{"type": "text", "text": text},
					},
				},
			}
		}
		return map[string]any{
			"type": "doc", "version": 1, "content": content,
		}, nil
	case '{':
		var adf map[string]any
		if err := json.Unmarshal(trimmed, &adf); err != nil {
			return nil, errors.BadRequestf("jira plugin: description ADF: %s", err)
		}
		return adf, nil
	default:
		return nil, errors.BadRequestf(
			"jira plugin: description must be a plain-text string or an ADF" +
				" object",
		)
	}
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
// new issue. issuetype and summary are required; priority, labels, assignee
// (a name/email), and description (plain text or ADF) are optional.
type createBody struct {
	IssueType   string          `json:"issuetype"`
	Summary     string          `json:"summary"`
	Priority    string          `json:"priority"`
	Labels      []string        `json:"labels"`
	Assignee    string          `json:"assignee"`
	Description json.RawMessage `json:"description"`
}

// createFieldsFromBody parses a CreateChild body into the Jira create-issue
// fields map, injecting the project from the container URI. assignee is
// resolved to an accountId and description is converted to ADF, exactly as
// PatchNode does — the create and patch surfaces accept the same field forms.
func createFieldsFromBody(
	ctx context.Context, c *client, r io.Reader, project string,
) (map[string]any, error) {
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
	if cb.Assignee != "" {
		accountID, err := resolveAssignee(ctx, c, cb.Assignee)
		if err != nil {
			return nil, err
		}
		fields["assignee"] = map[string]any{"accountId": accountID}
	}
	if len(bytes.TrimSpace(cb.Description)) > 0 {
		adf, err := descriptionToADF(cb.Description)
		if err != nil {
			return nil, err
		}
		fields["description"] = adf
	}
	return fields, nil
}

package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// putJSON performs a PUT with a JSON body, erroring on a non-2xx status.
// Jira's issue-edit endpoint replies 204 No Content on success, so the
// returned body is typically empty.
func (c *client) putJSON(
	ctx context.Context, endpoint string, body []byte,
) (out []byte, err error) {
	resp, err := c.do(ctx, http.MethodPut, endpoint, body)
	if err != nil {
		return nil, errors.Wrapf(err, "PUT %s", endpoint)
	}
	defer errors.DeferredCloser(&err, resp.Body)
	return readOK(resp, "PUT", endpoint)
}

// deleteRequest performs a DELETE, erroring on a non-2xx status (Jira
// replies 204 No Content on a successful issue delete).
func (c *client) deleteRequest(ctx context.Context, endpoint string) (err error) {
	resp, err := c.do(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return errors.Wrapf(err, "DELETE %s", endpoint)
	}
	defer errors.DeferredCloser(&err, resp.Body)
	_, err = readOK(resp, "DELETE", endpoint)
	return err
}

// issueEndpoint is the REST URL for one issue by key.
func (c *client) issueEndpoint(key string) string {
	return c.origin + "/rest/api/3/issue/" + url.PathEscape(key)
}

// createIssue POSTs a new issue with the given fields (which MUST include
// project, issuetype, and summary) and returns the server-assigned issue
// key — the identity Jira, not the caller, chooses (why creation is the
// ContainerCreator capability, RFC 0017 §Selection's sibling in FDR 0020).
func (c *client) createIssue(
	ctx context.Context, fields map[string]any,
) (string, error) {
	body, err := json.Marshal(map[string]any{"fields": fields})
	if err != nil {
		return "", errors.Wrap(err)
	}
	data, err := c.postJSON(ctx, c.origin+"/rest/api/3/issue", body)
	if err != nil {
		return "", err
	}
	var created struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		return "", errors.Wrapf(err, "parse create-issue response")
	}
	if created.Key == "" {
		return "", errors.ErrorWithStackf(
			"jira plugin: create-issue response has no key",
		)
	}
	return created.Key, nil
}

// updateIssueFields PUTs a partial field update to an existing issue — the
// non-status half of PatchNode (summary/priority/labels). Absent fields are
// left untouched (Jira's edit-issue semantics).
func (c *client) updateIssueFields(
	ctx context.Context, key string, fields map[string]any,
) error {
	body, err := json.Marshal(map[string]any{"fields": fields})
	if err != nil {
		return errors.Wrap(err)
	}
	_, err = c.putJSON(ctx, c.issueEndpoint(key), body)
	return err
}

// deleteIssue DELETEs an issue by key.
func (c *client) deleteIssue(ctx context.Context, key string) error {
	return c.deleteRequest(ctx, c.issueEndpoint(key))
}

// transition is one available workflow transition for an issue: its id, its
// name, and the status it moves the issue TO. Status is not a settable Jira
// field — it moves only through these workflow transitions, which is why
// PatchNode resolves a target status name to a transition here.
type transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   struct {
		Name string `json:"name"`
	} `json:"to"`
}

// transitions lists the workflow transitions currently available on an
// issue. The set depends on the issue's current status and the caller's
// permissions, so it is fetched per-issue at patch time, never cached.
func (c *client) transitions(
	ctx context.Context, key string,
) ([]transition, error) {
	data, err := c.getJSON(ctx, c.issueEndpoint(key)+"/transitions")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Transitions []transition `json:"transitions"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, errors.Wrapf(err, "parse transitions response")
	}
	return resp.Transitions, nil
}

// doTransition applies a workflow transition by id to an issue.
func (c *client) doTransition(ctx context.Context, key, transitionID string) error {
	body, err := json.Marshal(map[string]any{
		"transition": map[string]any{"id": transitionID},
	})
	if err != nil {
		return errors.Wrap(err)
	}
	_, err = c.postJSON(ctx, c.issueEndpoint(key)+"/transitions", body)
	return err
}

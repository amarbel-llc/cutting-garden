package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// requestTimeout caps every Jira HTTP round-trip. The command's cancelable
// context still aborts in-flight requests earlier on SIGINT/SIGTERM; this
// is the upper bound for an unresponsive server.
const requestTimeout = 60 * time.Second

// pageSize is the per-request page cap for the paginated search and
// project-listing endpoints. Jira clamps it server-side; we request a
// generous page so large projects need few round-trips.
const pageSize = 100

// client is a minimal Jira Cloud REST v3 client: enhanced JQL search to
// enumerate issues, single-issue GET for full fidelity, and project search
// for traversal. It carries no Jira object model — each issue is treated
// as an opaque JSON document, canonicalized (key-sorted) so capture and
// diff hash identical bytes for an unchanged issue.
type client struct {
	origin   string // Jira REST origin, e.g. https://acme.atlassian.net
	username string
	token    string
	http     *http.Client
}

func newClient(origin, username, token string) *client {
	return &client{
		origin:   strings.TrimRight(origin, "/"),
		username: username,
		token:    token,
		http:     &http.Client{Timeout: requestTimeout},
	}
}

// do issues one Jira request with basic auth (email + API token) and JSON
// accept. ctx is honored so a cancel unwinds the in-flight request
// promptly. body == nil sends no request body.
func (c *client) do(
	ctx context.Context,
	method, endpoint string,
	body []byte,
) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if c.username != "" || c.token != "" {
		req.SetBasicAuth(c.username, c.token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return resp, nil
}

// getJSON performs a GET and returns the response body, erroring on a
// non-2xx status with a trimmed body snippet for diagnostics.
func (c *client) getJSON(ctx context.Context, endpoint string) (body []byte, err error) {
	resp, err := c.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "GET %s", endpoint)
	}
	defer errors.DeferredCloser(&err, resp.Body)
	return readOK(resp, "GET", endpoint)
}

// postJSON performs a POST with a JSON body and returns the response body,
// erroring on a non-2xx status.
func (c *client) postJSON(ctx context.Context, endpoint string, body []byte) (out []byte, err error) {
	resp, err := c.do(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, errors.Wrapf(err, "POST %s", endpoint)
	}
	defer errors.DeferredCloser(&err, resp.Body)
	return readOK(resp, "POST", endpoint)
}

// readOK reads resp.Body and returns it on a 2xx status, or an error
// carrying the status and a trimmed body snippet otherwise.
func readOK(resp *http.Response, method, endpoint string) ([]byte, error) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.ErrorWithStackf(
			"%s %s: status %d: %s",
			method, endpoint, resp.StatusCode, snippet(data),
		)
	}
	return data, nil
}

// issue is one captured Jira issue: key is its issue key (e.g. PROJ-42)
// and data is the canonical (key-sorted) JSON body of the full issue
// resource.
type issue struct {
	key  string
	data []byte
}

// searchPage is one page of the enhanced JQL search response
// (/rest/api/3/search/jql). nextPageToken is empty on the final page.
type searchPage struct {
	Issues        []json.RawMessage `json:"issues"`
	NextPageToken string            `json:"nextPageToken"`
}

// searchRaw runs jql against the enhanced search endpoint, paginating via
// nextPageToken, and returns the raw JSON of every matching issue with the
// requested fields. It owns the one pagination + page-parse loop both
// typed searches (searchIssues, searchIssueUpdates) share, so a pagination
// fix lands in one place. fields is the Jira field selector list (["*all"]
// for a full capture, ["summary"]/["updated"] for cheap probes — the issue
// key is always present regardless).
func (c *client) searchRaw(
	ctx context.Context,
	jql string,
	fields []string,
) ([]json.RawMessage, error) {
	endpoint := c.origin + "/rest/api/3/search/jql"
	var (
		raws  []json.RawMessage
		token string
	)
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Wrap(err)
		}
		reqBody, err := json.Marshal(map[string]any{
			"jql":           jql,
			"fields":        fields,
			"maxResults":    pageSize,
			"nextPageToken": token,
		})
		if err != nil {
			return nil, errors.Wrap(err)
		}
		data, err := c.postJSON(ctx, endpoint, reqBody)
		if err != nil {
			return nil, err
		}
		var page searchPage
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, errors.Wrapf(err, "parse search response from %s", endpoint)
		}
		raws = append(raws, page.Issues...)
		if page.NextPageToken == "" {
			return raws, nil
		}
		token = page.NextPageToken
	}
}

// searchIssues runs jql and returns every matching issue with the requested
// fields, each canonicalized (key-sorted) so an unchanged issue hashes
// identically across fetches.
func (c *client) searchIssues(
	ctx context.Context,
	jql string,
	fields []string,
) ([]issue, error) {
	raws, err := c.searchRaw(ctx, jql, fields)
	if err != nil {
		return nil, err
	}
	issues := make([]issue, 0, len(raws))
	for _, raw := range raws {
		iss, err := issueFromRaw(raw)
		if err != nil {
			return nil, err
		}
		issues = append(issues, iss)
	}
	return issues, nil
}

// issueUpdate is the cheap freshness record for one issue: its key and its
// `updated` timestamp. The protocol capture's severing oracle (FDR 0019):
// an issue whose `updated` is unchanged since the prior receipt has its
// whole subtree grafted by markl-id, never re-fetched.
type issueUpdate struct {
	key     string
	updated string
}

// updatedFields is the bodiless field selector for the freshness probe:
// just `updated` (the key is always returned regardless). It deliberately
// transfers no issue body — that is the whole point of the severing
// shortcut.
var updatedFields = []string{"updated"}

// searchIssueUpdates runs jql requesting only the `updated` field and
// returns one issueUpdate per matching issue. This is the cheap probe that
// drives subtree reuse: it learns every issue's freshness without
// transferring bodies.
func (c *client) searchIssueUpdates(ctx context.Context, jql string) ([]issueUpdate, error) {
	raws, err := c.searchRaw(ctx, jql, updatedFields)
	if err != nil {
		return nil, err
	}
	updates := make([]issueUpdate, 0, len(raws))
	for _, raw := range raws {
		u, err := updateFromRaw(raw)
		if err != nil {
			return nil, err
		}
		updates = append(updates, u)
	}
	return updates, nil
}

// issueUpdate fetches one issue's freshness record (key + `updated`) without
// transferring its body — the single-issue analogue of searchIssueUpdates.
func (c *client) issueUpdate(ctx context.Context, key string) (issueUpdate, error) {
	endpoint := c.origin + "/rest/api/3/issue/" + url.PathEscape(key) +
		"?fields=" + url.QueryEscape("updated")
	data, err := c.getJSON(ctx, endpoint)
	if err != nil {
		return issueUpdate{}, err
	}
	return updateFromRaw(data)
}

// updateFromRaw extracts the key and `updated` timestamp from an issue
// resource fetched with the bodiless `updated` selector.
func updateFromRaw(raw []byte) (issueUpdate, error) {
	var meta struct {
		Key    string `json:"key"`
		Fields struct {
			Updated string `json:"updated"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return issueUpdate{}, errors.Wrapf(err, "parse issue update record")
	}
	if meta.Key == "" {
		return issueUpdate{}, errors.ErrorWithStackf("jira plugin: issue update record has no key")
	}
	return issueUpdate{key: meta.Key, updated: meta.Fields.Updated}, nil
}

// getIssue fetches one issue by key with the given field selector and
// returns it as a canonical-JSON issue.
func (c *client) getIssue(ctx context.Context, key string, fields []string) (issue, error) {
	endpoint := c.origin + "/rest/api/3/issue/" + url.PathEscape(key)
	if len(fields) > 0 {
		endpoint += "?fields=" + url.QueryEscape(strings.Join(fields, ","))
	}
	data, err := c.getJSON(ctx, endpoint)
	if err != nil {
		return issue{}, err
	}
	return issueFromRaw(data)
}

// issueFromRaw extracts the issue key and canonicalizes the JSON body of
// one issue resource. The canonical form (key-sorted, indented) makes
// capture and diff hash identical bytes for an unchanged issue.
func issueFromRaw(raw []byte) (issue, error) {
	var meta struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return issue{}, errors.Wrapf(err, "parse issue key")
	}
	if meta.Key == "" {
		return issue{}, errors.ErrorWithStackf("jira plugin: issue response has no key")
	}
	canon, err := canonicalJSON(raw)
	if err != nil {
		return issue{}, errors.Wrapf(err, "canonicalize issue %s", meta.Key)
	}
	return issue{key: meta.Key, data: canon}, nil
}

// project is one discovered Jira project: key (e.g. PROJ) and a human
// display name.
type project struct {
	key  string
	name string
}

// projectPage is one page of the project-search response
// (/rest/api/3/project/search). IsLast marks the final page.
type projectPage struct {
	Values []struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"values"`
	IsLast bool `json:"isLast"`
}

// listProjects enumerates every project the authenticated user can browse,
// paginating the project-search endpoint via startAt until isLast.
func (c *client) listProjects(ctx context.Context) ([]project, error) {
	var (
		projects []project
		startAt  int
	)
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Wrap(err)
		}
		endpoint := c.origin + "/rest/api/3/project/search?maxResults=" +
			strconv.Itoa(pageSize) + "&startAt=" + strconv.Itoa(startAt)
		data, err := c.getJSON(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		var page projectPage
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, errors.Wrapf(err, "parse project search from %s", endpoint)
		}
		for _, v := range page.Values {
			projects = append(projects, project{key: v.Key, name: v.Name})
		}
		if page.IsLast || len(page.Values) == 0 {
			return projects, nil
		}
		startAt += len(page.Values)
	}
}

// canonicalJSON re-encodes arbitrary JSON with map keys sorted and stable
// indentation, so two fetches of an unchanged issue produce byte-identical
// blobs (Go's encoding/json sorts map keys on marshal).
func canonicalJSON(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errors.Wrap(err)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return append(out, '\n'), nil
}

// jqlForProject builds the JQL that selects every issue in one project,
// ordered by key so capture output is deterministic. The project key is
// quoted to tolerate keys that collide with JQL reserved words.
func jqlForProject(projectKey string) string {
	return fmt.Sprintf("project = %q ORDER BY key ASC", projectKey)
}

// snippet trims an error-body excerpt so diagnostics stay readable.
func snippet(b []byte) string {
	const max = 256
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

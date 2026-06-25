package jira

import (
	"context"
	"net/url"

	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

const (
	// typeProject is a Jira project — a container whose children are its
	// issues.
	typeProject = "cutting_garden-jira-project-v1"
	// typeIssue is a single Jira issue — a leaf captured as one .json file
	// entry.
	typeIssue = "cutting_garden-jira-issue-v1"
)

// listFields is the cheap field selector traversal uses to enumerate a
// project's issues: just the summary (the key is always returned). It
// avoids transferring every issue's full body the way capture's allFields
// does.
var listFields = []string{"summary"}

var _ cutting_garden_plugins.RootLister = (*Plugin)(nil)

// Types declares the two node types the jira tree is built from. The tags
// are hyphenated and horizontally versioned (issue #79) so a future shape
// change adds a -v2 tag beside the -v1 rather than breaking it.
func (Plugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: typeProject, Container: true},
		// A leaf is a single issue, captured as its canonical JSON body.
		{Tag: typeIssue, Container: false, MimeType: "application/json"},
	}
}

// ListRoots enumerates the immediate children of node. When node is a
// bare-host root it returns the browsable projects (containers); when node
// is a project it returns that project's issues (leaves); an issue node is
// a leaf with no children. It shares the search/project endpoints with
// CaptureRoot and ScanForDiff, so discovery and capture cannot disagree
// about the tree.
func (Plugin) ListRoots(
	ctx context.Context,
	node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf(
			"jira plugin: ListRoots requires a node URI",
		)
	}

	base, username, token, err := connectionFromArg(node)
	if err != nil {
		return nil, err
	}
	origin, projectKey, issueKey, err := nodeFromBase(base)
	if err != nil {
		return nil, err
	}
	c := newClient(origin, username, token)

	switch {
	case issueKey != "":
		// A single issue is a leaf — it has no descendable children.
		return nil, nil
	case projectKey != "":
		return c.issueNodes(ctx, origin, projectKey)
	default:
		return c.projectNodes(ctx, origin)
	}
}

// projectNodes lists the browsable projects and maps each to a container
// Node addressable as its own jira: capture root.
func (c *client) projectNodes(
	ctx context.Context,
	origin string,
) ([]cutting_garden_plugins.Node, error) {
	projects, err := c.listProjects(ctx)
	if err != nil {
		return nil, err
	}
	nodes := make([]cutting_garden_plugins.Node, 0, len(projects))
	for _, p := range projects {
		name := p.name
		if name == "" {
			name = p.key
		}
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  jiraURIForNode(origin, p.key, ""),
			Name: name,
			Type: typeProject,
		})
	}
	return nodes, nil
}

// issueNodes lists a project's issues by key (summary only, no full
// bodies) and maps each to a leaf Node.
func (c *client) issueNodes(
	ctx context.Context,
	origin, projectKey string,
) ([]cutting_garden_plugins.Node, error) {
	issues, err := c.searchIssues(ctx, jqlForProject(projectKey), listFields)
	if err != nil {
		return nil, err
	}
	nodes := make([]cutting_garden_plugins.Node, 0, len(issues))
	for _, iss := range issues {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  jiraURIForNode(origin, projectKey, iss.key),
			Name: iss.key,
			Type: typeIssue,
		})
	}
	return nodes, nil
}

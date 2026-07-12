package jira

import (
	"context"
	"io"
	"sort"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_events"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// CaptureProtocol implements cutting_garden_plugins.ProtocolCapturePlugin:
// it emits an RFC 0002 receipt merkle tree (receipt → identity →
// environment, outcome, payload → site → projects → project → issues →
// issue) instead of the flat EntryV1 stream of CaptureRoot. The
// orchestrator type-asserts this interface on the resolved jira
// CapturePlugin and prefers it, recording the returned receipt id directly.
//
// The issue node is the severable unit (FDR 0019): when req.PriorReceiptDigest
// names a prior jira receipt, an issue whose `updated` timestamp is unchanged
// has its whole issue-node subtree grafted verbatim by markl-id — no body
// fetch — and only changed issues are re-fetched and rebuilt. A container's
// markl-id is the hash of its child-ref listing, so an unchanged subtree
// keeps its id and is a physically shared blob across captures (RFC 0002
// automatic dedup).
func (Plugin) CaptureProtocol(
	req cutting_garden_plugins.ProtocolCaptureRequest,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	r := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	base, username, token, err := connectionFromArg(req.Source)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}
	origin, projectKey, issueKey, err := nodeFromBase(base)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}
	c := newClient(origin, username, token)
	w := capture_plugin.NewBlobStoreWriter(req.BlobStore)

	// Resolve the set of projects to capture, mirroring CaptureRoot's
	// scoping: a single issue, one named project, or every browsable
	// project for a bare-host root.
	projects, err := resolveProjects(req.Context, c, projectKey, issueKey, r)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	// Prior reuse index, keyed by issue key → {updated, issue-node digest}.
	// Empty when no prior receipt, unreadable, or a non-jira receipt — every
	// such miss falls back to a full re-fetch of that issue.
	prior := loadPriorIndex(req.BlobStore, req.PriorReceiptDigest)

	cap := &protocolCapture{
		ctx:       req.Context,
		client:    c,
		writer:    w,
		origin:    origin,
		reporter:  r,
		prior:     prior,
		singleKey: issueKey,
	}

	projectRefs := make([]capture_plugin.Ref, 0, len(projects))
	for _, pk := range projects {
		ref, perr := cap.captureProject(pk)
		if perr != nil {
			return cutting_garden_plugins.ProtocolCaptureResult{}, perr
		}
		projectRefs = append(projectRefs, ref)
	}

	projectsRef, err := cap.writeContainer(aliasProjects, typeProjects, projectRefs)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}
	siteRef, err := cap.writeContainer(aliasSite, typeSite, []capture_plugin.Ref{projectsRef})
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	receiptDigest, err := capture_plugin.WriteReceipt(req.Context, w, capture_plugin.ReceiptParams{
		Kind: captureKind,
		Invocation: capture_plugin.Invocation{
			Target:    captureTarget(origin, projectKey, issueKey),
			Format:    captureFormat,
			Normalize: false,
			Options:   map[string]any{"fields": fieldOption()},
		},
		Host: capture_plugin.GatherHost(),
		Binary: capture_plugin.BinaryInfo{
			Name:    "cutting-garden",
			Version: req.BinaryVersion,
		},
		PluginEnv: capture_plugin.PluginEnv{
			TypeString: pluginEnvType,
			Body:       map[string]any{"fields": fieldOption()},
		},
		OutcomePlugin: cap.outcomePlugin(),
		PayloadRefs: []capture_plugin.Ref{
			capture_plugin.LockedRef("payload", siteRef.Digest, typeSite),
		},
	})
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	return cutting_garden_plugins.ProtocolCaptureResult{
		ReceiptDigest: receiptDigest,
		// One entry per issue captured (grafted or freshly built) — the
		// per-receipt "objects" count, matching caldav/git's "payload
		// objects, not merkle scaffolding" semantics.
		ObjectCount: len(cap.reuseIndex),
	}, nil
}

// protocolCapture threads the per-capture state through the project/issue
// node builders so each can append to the reuse outcome index without a long
// parameter list.
type protocolCapture struct {
	ctx      context.Context
	client   *client
	writer   capture_plugin.Writer
	origin   string
	reporter cutting_garden_plugins.Reporter

	// prior maps issue key → its prior {updated, issue-node digest} for
	// subtree reuse. nil/empty disables reuse (full capture).
	prior map[string]priorIssue
	// singleKey, when non-empty, restricts a project capture to exactly
	// that issue (a single-issue source node).
	singleKey string

	// reuseIndex records every captured issue's {updated, digest} for the
	// receipt outcome node, so the NEXT capture can reuse this one. Its
	// length is the per-receipt issue count (ProtocolCaptureResult.ObjectCount).
	reuseIndex []issueIndexEntry
	// reused counts issues grafted from the prior receipt (a diagnostic
	// surfaced per project in the capture phase verdict).
	reused int
}

// captureProject builds one project-node subtree: its issues collection
// referencing one issue node per issue in the project. Returns the
// project-node ref for the projects container.
func (p *protocolCapture) captureProject(projectKey string) (capture_plugin.Ref, error) {
	p.reporter.PhaseStart("capture " + projectKey)

	updates, err := p.listIssueUpdates(projectKey)
	if err != nil {
		p.reporter.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Diagnostic: map[string]any{"error": err.Error()},
		})
		return capture_plugin.Ref{}, err
	}

	issueRefs := make([]capture_plugin.Ref, 0, len(updates))
	for _, u := range updates {
		ref, rerr := p.captureIssue(u)
		if rerr != nil {
			p.reporter.PhaseEnd(capture_events.Verdict{
				OK:         false,
				Diagnostic: map[string]any{"error": rerr.Error()},
			})
			return capture_plugin.Ref{}, rerr
		}
		issueRefs = append(issueRefs, ref)
		p.reporter.Progress(cutting_garden_plugins.ReportProgress{
			Item:  projectKey,
			Items: int64(len(issueRefs)),
		})
	}

	issuesRef, err := p.writeContainer(aliasIssues, typeIssues, issueRefs)
	if err != nil {
		return capture_plugin.Ref{}, err
	}
	// The project is keyed by its native id (the project key), not a fixed
	// slot, so its parent (the projects collection) references it by key.
	projectRef, err := p.writeContainer(projectKey, typeProjectNode, []capture_plugin.Ref{issuesRef})
	if err != nil {
		return capture_plugin.Ref{}, err
	}

	p.reporter.PhaseEnd(capture_events.Verdict{
		OK: true,
		Diagnostic: map[string]any{
			"issues": len(issueRefs),
			"reused": p.reused,
		},
	})
	return projectRef, nil
}

// captureIssue builds (or grafts) one issue-node subtree and returns its
// ref aliased by issue key. When the prior index holds this key with an
// unchanged `updated`, the prior issue-node digest is reused verbatim — no
// body is fetched (the sever). Otherwise the full `*all` issue is fetched
// and decomposed into fields / description / comment leaves.
func (p *protocolCapture) captureIssue(u issueUpdate) (capture_plugin.Ref, error) {
	if prior, ok := p.prior[u.key]; ok && prior.updated != "" && prior.updated == u.updated {
		p.reused++
		p.reuseIndex = append(p.reuseIndex, issueIndexEntry{
			Key: u.key, Updated: u.updated, Digest: prior.digest,
		})
		return capture_plugin.LockedRef(u.key, prior.digest, typeIssueNode), nil
	}

	full, err := p.client.getIssue(p.ctx, u.key, allFields)
	if err != nil {
		return capture_plugin.Ref{}, err
	}
	decomposed, err := decomposeIssue(full)
	if err != nil {
		return capture_plugin.Ref{}, err
	}

	childRefs, err := p.writeIssueChildren(decomposed)
	if err != nil {
		return capture_plugin.Ref{}, err
	}

	// The issue is keyed by its native id (the issue key) under the issues
	// collection — the severable unit's reuse alias.
	issueRef, err := p.writeContainer(u.key, typeIssueNode, childRefs)
	if err != nil {
		return capture_plugin.Ref{}, err
	}
	p.reuseIndex = append(p.reuseIndex, issueIndexEntry{
		Key: u.key, Updated: u.updated, Digest: issueRef.Digest,
	})
	return issueRef, nil
}

// writeIssueChildren stores the issue's leaf blobs (fields, description, and
// one node per comment) and returns the ordered child refs for the issue
// node. Description and comments are omitted when absent so an issue without
// either yields a stable, minimal subtree.
func (p *protocolCapture) writeIssueChildren(d decomposedIssue) ([]capture_plugin.Ref, error) {
	refs := make([]capture_plugin.Ref, 0, 2+len(d.comments))

	fieldsRef, err := p.writeLeaf("fields", typeIssueFields, d.fields)
	if err != nil {
		return nil, err
	}
	refs = append(refs, fieldsRef)

	if len(d.description) > 0 {
		descRef, derr := p.writeLeaf("description", typeDescription, d.description)
		if derr != nil {
			return nil, derr
		}
		refs = append(refs, descRef)
	}

	for _, com := range d.comments {
		comRef, cerr := p.writeLeaf("comment/"+com.id, typeComment, com.body)
		if cerr != nil {
			return nil, cerr
		}
		refs = append(refs, comRef)
	}
	return refs, nil
}

// writeLeaf stores a canonical-JSON leaf body as a node and returns its
// type-locked ref under alias.
func (p *protocolCapture) writeLeaf(alias, typeString string, body []byte) (capture_plugin.Ref, error) {
	node := capture_plugin.BuildNode(typeString, nil, body)
	digest, _, err := p.writer.WriteBlob(p.ctx, readerOf(node))
	if err != nil {
		return capture_plugin.Ref{}, errors.Wrapf(err, "jira plugin: store %s", alias)
	}
	return capture_plugin.LockedRef(alias, digest, typeString), nil
}

// writeContainer stores a container node whose body is its sorted child-ref
// listing (no JCS body) and returns its ref under alias — a fixed slot name
// for a singleton container (aliasSite/aliasProjects/aliasIssues) or a
// native id for a keyed one (a project key, an issue key). Children are
// sorted by alias so the container is byte-stable regardless of enumeration
// order — the property that makes an unchanged subtree dedup.
func (p *protocolCapture) writeContainer(alias, typeString string, refs []capture_plugin.Ref) (capture_plugin.Ref, error) {
	sorted := make([]capture_plugin.Ref, len(refs))
	copy(sorted, refs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Alias < sorted[j].Alias })

	node := capture_plugin.BuildNode(typeString, sorted, nil)
	digest, _, err := p.writer.WriteBlob(p.ctx, readerOf(node))
	if err != nil {
		return capture_plugin.Ref{}, errors.Wrapf(err, "jira plugin: store %s container", typeString)
	}
	return capture_plugin.LockedRef(alias, digest, typeString), nil
}

// outcomePlugin builds the optional plugin-outcome node carrying the
// per-issue reuse index (key → {updated, issue-node digest}). The NEXT
// capture's loadPriorIndex reads it to graft unchanged issue subtrees. nil
// when no issues were captured.
func (p *protocolCapture) outcomePlugin() *capture_plugin.PluginEnv {
	if len(p.reuseIndex) == 0 {
		return nil
	}
	sort.Slice(p.reuseIndex, func(i, j int) bool { return p.reuseIndex[i].Key < p.reuseIndex[j].Key })
	issues := make([]map[string]any, 0, len(p.reuseIndex))
	for _, e := range p.reuseIndex {
		issues = append(issues, map[string]any{
			"key":     e.Key,
			"updated": e.Updated,
			"digest":  e.Digest,
		})
	}
	return &capture_plugin.PluginEnv{
		TypeString: outcomeIndexType,
		Body:       map[string]any{"issues": issues},
	}
}

// listIssueUpdates returns the issue keys to capture under a project paired
// with their `updated` timestamps — the freshness oracle. It is the cheap,
// bodiless probe (fields=[updated]) that drives subtree reuse. A
// single-issue source restricts the result to that one key.
func (p *protocolCapture) listIssueUpdates(projectKey string) ([]issueUpdate, error) {
	if p.singleKey != "" {
		u, err := p.client.issueUpdate(p.ctx, p.singleKey)
		if err != nil {
			return nil, err
		}
		return []issueUpdate{u}, nil
	}
	return p.client.searchIssueUpdates(p.ctx, jqlForProject(projectKey))
}

// resolveProjects mirrors CaptureRoot's scoping: a single-issue node and a
// named-project node both resolve to one project (derived from the issue
// key when only an issue is named); a bare-host node lists every browsable
// project.
func resolveProjects(
	ctx context.Context,
	c *client,
	projectKey, issueKey string,
	r cutting_garden_plugins.Reporter,
) ([]string, error) {
	switch {
	case issueKey != "":
		return []string{projectOfKey(issueKey)}, nil
	case projectKey != "":
		return []string{projectKey}, nil
	default:
		r.PhaseStart("list projects")
		discovered, err := c.listProjects(ctx)
		if err != nil {
			r.PhaseEnd(capture_events.Verdict{
				OK:         false,
				Diagnostic: map[string]any{"error": err.Error()},
			})
			return nil, err
		}
		projects := make([]string, 0, len(discovered))
		for _, p := range discovered {
			projects = append(projects, p.key)
		}
		r.PhaseEnd(capture_events.Verdict{
			OK:         true,
			Diagnostic: map[string]any{"projects": len(projects)},
		})
		return projects, nil
	}
}

// captureTarget is the human-readable invocation target recorded on the
// receipt: the most specific addressed node.
func captureTarget(origin, projectKey, issueKey string) string {
	switch {
	case issueKey != "":
		return origin + "/" + projectOfKey(issueKey) + "/" + issueKey
	case projectKey != "":
		return origin + "/" + projectKey
	default:
		return origin
	}
}

// fieldOption is the field selector recorded in the identity-affecting
// invocation/plugin-env bodies, as a JSON-stable slice.
func fieldOption() []any {
	out := make([]any, len(allFields))
	for i, f := range allFields {
		out[i] = f
	}
	return out
}

// readerOf wraps node bytes in an io.Reader for the Writer.
func readerOf(b []byte) io.Reader { return strings.NewReader(string(b)) }

// compile-time assertion: the jira plugin emits an RFC 0002 protocol
// receipt tree.
var _ cutting_garden_plugins.ProtocolCapturePlugin = (*Plugin)(nil)

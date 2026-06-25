package jira

import "github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"

// Protocol-defined node type-strings for the jira RFC 0002 binding. These
// are the merkle-tree node types CaptureProtocol emits and DiffProtocol
// consumes — distinct from the flat EntryV1 capture (capture.go), which the
// orchestrator uses only when a plugin does NOT type-assert to
// ProtocolCapturePlugin. The tags are hyphenated and horizontally versioned
// (FDR 0018, issue #79): a future shape change adds a -v2 beside the -v1.
//
// This increment realizes the FDR 0019 "promote to experimental" subset:
// the full merkle machinery (receipt → site → projects → project → issues →
// issue) with the issue subtree as the severable unit, decomposed into the
// high-value child nodes carried by the single `*all` issue resource
// (fields, description ADF, comments). The remaining FDR taxonomy (catalog,
// actors, attachments+binaries, worklogs, changelog, links, agile) is a
// follow-up — see docs/features/0019-jira-plugin.md §Relationship to the
// landed increment.
const (
	// captureKind tags the receipt:
	// cutting_garden-capture_receipt-jira-v1 (the converged underscore
	// prefix, #112 — jira is a new family debuting at v1).
	captureKind = "jira"

	// captureFormat is the invocation `format` value for jira captures.
	captureFormat = "jira-issues"

	// pluginEnvType is the jira plugin's identity-affecting environment
	// node (the field selector the capture requested).
	pluginEnvType = "jcs-jira-environment-v1"

	// typeSite is the payload root: the captured Jira site. Container
	// referencing the projects collection. (Catalog/actors are deferred —
	// the FDR places them under site too.)
	typeSite = "cutting_garden-jira-site-v1"
	// typeProjects is the projects collection container.
	typeProjects = "cutting_garden-jira-projects-v1"
	// typeProjectNode is one captured project: a container referencing its
	// issues collection. Distinct from the traversal-layer typeProject tag
	// (traversal.go), which the lazy lister advertises; the capture tree
	// nests an issues collection beneath it.
	typeProjectNode = "cutting_garden-jira-project-node-v1"
	// typeIssues is a project's issues collection — THE reuse boundary. Its
	// markl-id changes iff some issue subtree beneath it changed.
	typeIssues = "cutting_garden-jira-issues-v1"
	// typeIssueNode is one issue: a container referencing its fields,
	// description, and comment subtree. The severable unit (FDR 0019): its
	// markl-id is the incremental-capture reuse key, grafted verbatim when
	// the issue's `updated` timestamp is unchanged.
	typeIssueNode = "cutting_garden-jira-issue-node-v1"

	// typeIssueFields is the issue's system + custom field VALUES, with the
	// large independently-edited blobs (description, comments) lifted out
	// into their own nodes so a field-only edit doesn't rewrite them.
	typeIssueFields = "jcs-jira-issue-fields-v1"
	// typeDescription is the issue's ADF description body, its own blob so a
	// summary edit doesn't rewrite the (large) description.
	typeDescription = "jcs-jira-description-v1"
	// typeComment is one comment (author, ADF body, created/updated,
	// visibility), its own blob.
	typeComment = "jcs-jira-comment-v1"
)

// Singleton-container ref aliases: the fixed slot name each singleton
// container is referenced by in its parent (the site under the receipt
// payload, projects under site, issues under a project). Keyed containers
// (a project under projects, an issue under issues) are instead aliased by
// their native id — the project key / issue key — at the call site.
const (
	aliasSite     = "site"
	aliasProjects = "projects"
	aliasIssues   = "issues"
)

// init registers the jira binding's node types into the build-time
// type-signature registry (RFC 0002 §Type Signatures), so every reference
// into a jira receipt tree carries an `@<sig>` type lock that consumers
// verify. Media types follow application/vnd.cutting-garden.<thing>+<format>:
// container nodes are +hyphence (their body is the ref listing), leaf nodes
// are +jcs (canonical JSON bodies).
func init() {
	register := func(typeString, mediaType string) {
		capture_plugin.RegisterType(capture_plugin.TypeDef{
			TypeString:    typeString,
			IANAMediaType: mediaType,
		})
	}

	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:    capture_plugin.ReceiptType(captureKind),
		IANAMediaType: "application/vnd.cutting-garden.capture-receipt-jira+hyphence",
	})
	register(pluginEnvType, "application/vnd.cutting-garden.jira-environment+jcs")
	register(outcomeIndexType, "application/vnd.cutting-garden.jira-outcome-index+jcs")

	// Container nodes: bodies are their typed-ref listings (hyphence).
	register(typeSite, "application/vnd.cutting-garden.jira-site+hyphence")
	register(typeProjects, "application/vnd.cutting-garden.jira-projects+hyphence")
	register(typeProjectNode, "application/vnd.cutting-garden.jira-project+hyphence")
	register(typeIssues, "application/vnd.cutting-garden.jira-issues+hyphence")
	register(typeIssueNode, "application/vnd.cutting-garden.jira-issue+hyphence")

	// Leaf nodes: canonical JSON bodies (jcs).
	register(typeIssueFields, "application/vnd.cutting-garden.jira-issue-fields+jcs")
	register(typeDescription, "application/vnd.cutting-garden.jira-description+jcs")
	register(typeComment, "application/vnd.cutting-garden.jira-comment+jcs")
}

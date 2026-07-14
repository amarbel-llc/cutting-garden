# Split UpdateNode into PutNode + PatchNode Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task.

**Goal:** Split `NodeMutator.UpdateNode` into `PutNode` (full-replace rename) and `PatchNode` (partial-field patch), wire both into the MCP tool layer, and provide a working caldav reference implementation.

**Architecture:** Four layers in strict dependency order: (1) `mcp_tool_perms` constants — the single classifier for tool names/permissions; (2) the `NodeMutator` interface in `internal/cutting_garden_plugins/mutate.go` plus its `pkgs/` dagnabit facade (regenerated after the interface change); (3) `internal/mcp/tools.go` — schema constants, tool catalogue, dispatch; (4) `plugins/caldav/mutate.go` — the only concrete `NodeMutator` implementation. `PatchNode`'s caldav implementation follows a GET→parse→`json.Unmarshal`→serialize→PUT flow: Go's `json.Unmarshal` onto a non-zero struct updates only fields present in the JSON, giving RFC 7396 JSON Merge Patch semantics without any custom merge logic.

**Tech Stack:** Go, CalDAV (WebDAV/iCalendar), go-mcp protocol, dagnabit-generated `pkgs/` facades, in-memory `caldavtestserver` for tests.

**Rollback:** All changes live on the `bright-cherry` worktree branch. Revert the commits; after reverting re-run `just codemod-generate-dagnabit` to restore the old `pkgs/` facade.

---

### Task 1: Update `mcp_tool_perms/perms.go` — rename and add tool name constants

**Promotion criteria:** N/A (additive change).

**Files:**
- Modify: `internal/mcp_tool_perms/perms.go:29-52`

**Step 1: Write the change**

Replace the existing `const` block and `Classify` function:

```go
const (
	ToolCreateNode        = "create_node"
	ToolPutNode           = "put_node"
	ToolPatchNode         = "patch_node"
	ToolDeleteNode        = "delete_node"
	ToolDescribeNodeTypes = "describe_node_types"
	ToolReadNode          = "read_node"
	ToolListNodes         = "list_nodes"
)

func Classify(toolName string) (Class, bool) {
	switch toolName {
	case ToolCreateNode, ToolPutNode, ToolPatchNode, ToolDeleteNode:
		return ClassDestructive, true
	case ToolDescribeNodeTypes, ToolReadNode, ToolListNodes:
		return ClassRead, true
	default:
		return "", false
	}
}
```

Also remove `ToolUpdateNode` — it is gone; downstream callers must be updated.

**Step 2: Verify it compiles**

```bash
go build ./internal/mcp_tool_perms/...
```

Expected: error(s) about `ToolUpdateNode` in dependent files — that is the correct signal that the rename must propagate.

**Step 3: Commit**

```bash
git add internal/mcp_tool_perms/perms.go
git commit -m "feat(mcp_tool_perms): rename update_node → put_node, add patch_node"
```

---

### Task 2: Update `mcp_tool_perms/perms_test.go` — fix tests for new constants

**Promotion criteria:** N/A.

**Files:**
- Modify: `internal/mcp_tool_perms/perms_test.go`

**Step 1: Read the test file**

Read `internal/mcp_tool_perms/perms_test.go` to see exact assertions before editing.

**Step 2: Write the test change**

Replace all references to `ToolUpdateNode` / `"update_node"` with `ToolPutNode` / `"put_node"`. Add a test assertion for `ToolPatchNode` being classified as `ClassDestructive`. Example shape:

```go
{ToolCreateNode, ClassDestructive, true},
{ToolPutNode, ClassDestructive, true},
{ToolPatchNode, ClassDestructive, true},
{ToolDeleteNode, ClassDestructive, true},
```

**Step 3: Run the tests**

```bash
go test ./internal/mcp_tool_perms/...
```

Expected: PASS.

**Step 4: Commit**

```bash
git add internal/mcp_tool_perms/perms_test.go
git commit -m "test(mcp_tool_perms): update tests for put_node / patch_node"
```

---

### Task 3: Update `NodeMutator` interface

**Promotion criteria:** N/A (the old `UpdateNode` is replaced fleet-wide — caldav is the only implementer; the pkg facade is regenerated next).

**Files:**
- Modify: `internal/cutting_garden_plugins/mutate.go`

**Step 1: Write the interface change**

Replace the current interface (lines 20–37) with:

```go
// NodeMutator is the OPTIONAL write capability: create, replace, patch, or
// delete a single addressable node in a plugin's tree — the write-side
// sibling of RootLister (FDR 0014/0020). It is probed by type assertion on an
// already-resolved plugin, exactly as RootLister / LeafReader / RootProvider
// are; a plugin whose scheme has no meaningful write surface simply omits it.
//
// Node addressing reuses the RootLister URI space verbatim: a mutation
// targets the same *url.URL a ListRoots / resources/read walk surfaces, so
// the read and write axes share one address space. CUD is NOT receipt-based —
// it mutates one live node, with no blob store and no capture receipt
// (capturing the post-mutation state is a separate `capture` invocation).
type NodeMutator interface {
	Plugin

	// CreateNode creates a new node at uri from body. typ is a NodeType.Tag
	// from the plugin's declared Types() (FDR 0014) — the plugin validates it
	// can create a node of that type. Create is STRICT, not upsert: it is an
	// error if uri already exists (use PutNode to overwrite). For a leaf,
	// body is the object bytes; for a container type body MAY be empty.
	CreateNode(ctx context.Context, uri *url.URL, body io.Reader, typ string) error

	// PutNode replaces the body of an existing leaf at uri. It is an error if
	// uri does NOT exist (use CreateNode to create). Containers are not updated
	// as a unit — their children are mutated individually. This is full-replace
	// semantics: the body must represent the complete desired state.
	PutNode(ctx context.Context, uri *url.URL, body io.Reader) error

	// PatchNode applies a partial-field update to an existing node at uri.
	// body contains only the fields the caller wants to change; absent fields
	// MUST be left untouched. Implementations MUST NOT error on an absent or
	// unrecognized field — the entire point of PatchNode is "only touch what
	// is explicitly named in the body." An empty body is a bad-request error;
	// a body with no recognized fields is a no-op (or near-no-op). The body
	// format is plugin-defined.
	PatchNode(ctx context.Context, uri *url.URL, body io.Reader) error

	// DeleteNode removes the node at uri. node MUST be non-nil.
	DeleteNode(ctx context.Context, uri *url.URL) error
}
```

**Step 2: Verify it compiles (will fail downstream — expected)**

```bash
go build ./internal/cutting_garden_plugins/...
```

Expected: PASS for this package. Dependent packages (`internal/mcp`, `plugins/caldav`) will fail — that's the signal.

**Step 3: Regenerate the pkgs/ dagnabit facade**

The caldav plugin imports `pkgs/cutting_garden_plugins` (not `internal/`), so the facade must reflect the new interface:

```bash
just codemod-generate-dagnabit
```

Expected: `pkgs/cutting_garden_plugins/main.go` regenerated with `PutNode` and `PatchNode` in the re-exported `NodeMutator`.

**Step 4: Commit both the interface and the facade together**

```bash
git add internal/cutting_garden_plugins/mutate.go pkgs/cutting_garden_plugins/main.go
git commit -m "feat(cutting_garden_plugins): split UpdateNode → PutNode + PatchNode"
```

---

### Task 4: Update `internal/mcp/tools.go` — schema, catalogue, dispatch

**Promotion criteria:** N/A.

**Files:**
- Modify: `internal/mcp/tools.go:38-42,70-87,137-161,226-279`

**Step 1: Update schema constants (lines ~70–87)**

Rename `updateNodeSchema` → `putNodeSchema`, update its description, and add `patchNodeSchema`:

```go
putNodeSchema = `{"type":"object","required":["uri","body"],` +
    `"properties":{` +
    `"uri":{"type":"string","description":"the existing node URI to overwrite (full replace)"},` +
    `"body":{"type":"string","description":"the new object as raw iCalendar or {component,event|task} JSON"}}}`
patchNodeSchema = `{"type":"object","required":["uri","body"],` +
    `"properties":{` +
    `"uri":{"type":"string","description":"the existing node URI to patch"},` +
    `"body":{"type":"string","description":"a JSON object with only the fields to change; absent fields are left untouched"}}}`
```

**Step 2: Update `Tools` struct doc comment (line ~38)**

Change "create_node / update_node / delete_node" to "create_node / put_node / patch_node / delete_node".

**Step 3: Update `cudToolDefs()` (lines ~138–161)**

Replace the `ToolUpdateNode` entry with a `ToolPutNode` entry (same shape, updated description) and add a `ToolPatchNode` entry:

```go
{
    Name:        mcp_tool_perms.ToolPutNode,
    Description: "Overwrite an existing node's body at a node URI (full replace). " +
        "Strict: errors if the node does not exist (use create_node).",
    InputSchema: json.RawMessage(putNodeSchema),
    Annotations: annotationFor(mcp_tool_perms.ToolPutNode),
},
{
    Name: mcp_tool_perms.ToolPatchNode,
    Description: "Partially update an existing node: body is a JSON object " +
        "containing only the fields to change; absent fields are left untouched. " +
        "Use this instead of put_node when you only want to flip one field " +
        "without reading and re-sending the entire object.",
    InputSchema: json.RawMessage(patchNodeSchema),
    Annotations: annotationFor(mcp_tool_perms.ToolPatchNode),
},
```

Also update `cudToolDefs`'s doc comment to say "create/put/patch/delete".

**Step 4: Update `call()` dispatch (lines ~248–279)**

Rename the `case mcp_tool_perms.ToolUpdateNode` block to `case mcp_tool_perms.ToolPutNode` (change `UpdateNode` → `PutNode` in the method call and success message). Add a new `case mcp_tool_perms.ToolPatchNode` block immediately after:

```go
case mcp_tool_perms.ToolPutNode:
    var in struct {
        URI  string `json:"uri"`
        Body string `json:"body"`
    }
    if err := json.Unmarshal(args, &in); err != nil {
        return "", errors.Wrap(err)
    }
    u, m, err := t.resolve(in.URI)
    if err != nil {
        return "", err
    }
    if err := m.PutNode(ctx, u, strings.NewReader(in.Body)); err != nil {
        return "", err
    }
    return "put " + in.URI, nil

case mcp_tool_perms.ToolPatchNode:
    var in struct {
        URI  string `json:"uri"`
        Body string `json:"body"`
    }
    if err := json.Unmarshal(args, &in); err != nil {
        return "", errors.Wrap(err)
    }
    u, m, err := t.resolve(in.URI)
    if err != nil {
        return "", err
    }
    if err := m.PatchNode(ctx, u, strings.NewReader(in.Body)); err != nil {
        return "", err
    }
    return "patched " + in.URI, nil
```

**Step 5: Update `ToolDescribeNodeTypes` description in `readToolDefs()` (line ~112)**

Change "create_node/update_node" to "create_node/put_node" in the description string.

**Step 6: Verify**

```bash
go build ./internal/mcp/...
```

Expected: error(s) about `ToolUpdateNode` and `UpdateNode` in `tools_test.go` — fix in the next task.

**Step 7: Commit**

```bash
git add internal/mcp/tools.go
git commit -m "feat(mcp): rename update_node → put_node, add patch_node tool"
```

---

### Task 5: Update `internal/mcp/tools_test.go` — rename fakeMutator method

**Promotion criteria:** N/A.

**Files:**
- Modify: `internal/mcp/tools_test.go`

**Step 1: Read the file**

Read `internal/mcp/tools_test.go` to see the exact `fakeMutator` struct definition and which test cases reference `"update_node"`.

**Step 2: Update `fakeMutator`**

Add `PutNode` (copy of `UpdateNode`'s body), add `PatchNode` stub, remove `UpdateNode`:

```go
type fakeMutator struct {
    created []string
    put     []string
    patched []string
    deleted []string
}

func (f *fakeMutator) CreateNode(_ context.Context, u *url.URL, _ io.Reader, _ string) error {
    f.created = append(f.created, u.String()); return nil
}
func (f *fakeMutator) PutNode(_ context.Context, u *url.URL, _ io.Reader) error {
    f.put = append(f.put, u.String()); return nil
}
func (f *fakeMutator) PatchNode(_ context.Context, u *url.URL, _ io.Reader) error {
    f.patched = append(f.patched, u.String()); return nil
}
func (f *fakeMutator) DeleteNode(_ context.Context, u *url.URL) error {
    f.deleted = append(f.deleted, u.String()); return nil
}
```

(The exact field names on `fakeMutator` may differ — match what's already there.)

**Step 3: Update test cases**

Replace all `"update_node"` strings with `"put_node"`. Add a `"patch_node"` test case mirroring the `"put_node"` one (same uri+body shape, dispatches to `PatchNode`).

**Step 4: Verify**

```bash
go test ./internal/mcp/...
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/mcp/tools_test.go
git commit -m "test(mcp): update tools_test for put_node / patch_node"
```

---

### Task 6: Rename `UpdateNode` → `PutNode` in `plugins/caldav/mutate.go`

**Promotion criteria:** N/A (just a rename; logic unchanged).

**Files:**
- Modify: `plugins/caldav/mutate.go:58-79`

**Step 1: Write the rename**

Change the method signature from `UpdateNode` to `PutNode` and update its doc comment:

```go
// PutNode strictly overwrites an existing CalDAV object at the node URI
// (full-replace semantics). The body is normalized to iCalendar (raw .ics or
// objectView JSON) and PUT with an If-Match precondition, so a missing object
// is reported rather than silently created.
func (Plugin) PutNode(
    ctx context.Context,
    node *url.URL,
    body io.Reader,
) error {
    if node == nil {
        return errors.ErrorWithStackf("caldav plugin: PutNode requires a node URI")
    }
    c, href, err := clientForNode(node)
    if err != nil {
        return err
    }
    icalData, err := normalizeObjectBody(body)
    if err != nil {
        return err
    }
    return c.updateResource(ctx, href, icalData)
}
```

Also update the compile-time assertion comment if it says `UpdateNode`.

**Step 2: Verify**

```bash
go build ./plugins/caldav/...
```

Expected: compile-time assertion error because `PatchNode` is not yet implemented — that's expected.

**Step 3: Commit** (after PatchNode is added — do not commit the partial interface satisfaction):

Hold this commit until after Task 7.

---

### Task 7: Implement `PatchNode` in `plugins/caldav/mutate.go`

**Promotion criteria:** N/A.

**Files:**
- Modify: `plugins/caldav/mutate.go` (add new method after `PutNode`)

**Step 1: Write the implementation**

```go
// PatchNode applies a partial-field update to an existing CalDAV object at
// the node URI. body MUST be a JSON object whose keys are the fields to
// change; absent keys are left untouched (JSON Merge Patch semantics,
// RFC 7396). Raw iCalendar is NOT accepted — use PutNode for a full replace.
//
// An empty body is a bad-request error. A JSON object with no recognized
// fields results in the object being read and re-written without modification
// (a no-op round-trip). The component type (VEVENT/VTODO/VJOURNAL) is
// inferred from the current object — the caller does not supply it.
//
// Supported patch keys match the field names the objectView JSON (returned by
// resources/read) exposes, e.g. {"summary":"new title"} or
// {"status":"COMPLETED"}.
func (Plugin) PatchNode(ctx context.Context, node *url.URL, body io.Reader) error {
    if node == nil {
        return errors.ErrorWithStackf("caldav plugin: PatchNode requires a node URI")
    }
    raw, err := io.ReadAll(body)
    if err != nil {
        return errors.Wrap(err)
    }
    trimmed := bytes.TrimSpace(raw)
    if len(trimmed) == 0 {
        return errors.BadRequestf(
            "caldav plugin: PatchNode requires a JSON body; " +
                "for a full replace use put_node",
        )
    }
    if trimmed[0] != '{' {
        return errors.BadRequestf(
            "caldav plugin: PatchNode body must be a JSON object; " +
                "raw iCalendar is not accepted (use put_node for a full replace)",
        )
    }

    c, href, err := clientForNode(node)
    if err != nil {
        return err
    }

    // Read the current object to learn its component type and field values.
    current, err := c.getResource(ctx, href)
    if err != nil {
        return err
    }
    ov, ok := parseObjectView(current)
    if !ok {
        return errors.ErrorWithStackf(
            "caldav plugin: PatchNode: could not parse existing object at %s", href,
        )
    }

    // Unmarshal the patch JSON onto the current component struct.
    // Go's json.Unmarshal only sets fields present in the JSON and leaves
    // absent fields at their current values — RFC 7396 merge-patch semantics.
    var updated string
    switch ov.Component {
    case "VEVENT":
        if err := json.Unmarshal(trimmed, ov.Event); err != nil {
            return errors.BadRequestf("caldav plugin: invalid patch JSON for VEVENT: %s", err)
        }
        updated = ical.EventToIcal(ov.Event)
    case "VTODO":
        if err := json.Unmarshal(trimmed, ov.Task); err != nil {
            return errors.BadRequestf("caldav plugin: invalid patch JSON for VTODO: %s", err)
        }
        updated = ical.TaskToIcal(ov.Task)
    case "VJOURNAL":
        if err := json.Unmarshal(trimmed, ov.Journal); err != nil {
            return errors.BadRequestf("caldav plugin: invalid patch JSON for VJOURNAL: %s", err)
        }
        updated = ical.JournalToIcal(ov.Journal)
    default:
        return errors.ErrorWithStackf(
            "caldav plugin: PatchNode: existing object at %s has unrecognized component %q",
            href, ov.Component,
        )
    }
    return c.updateResource(ctx, href, updated)
}
```

Note: `bytes` and `json` are already imported in `mutate.go`; `ical` is already imported. Verify the import block covers them.

**Step 2: Verify the interface is satisfied**

```bash
go build ./plugins/caldav/...
```

Expected: PASS (the compile-time assertion `var _ cutting_garden_plugins.NodeMutator = (*Plugin)(nil)` satisfies with all four methods).

**Step 3: Commit PutNode rename + PatchNode together**

```bash
git add plugins/caldav/mutate.go
git commit -m "feat(caldav): rename UpdateNode → PutNode, implement PatchNode"
```

---

### Task 8: Add PatchNode tests; rename UpdateNode test cases

**Promotion criteria:** N/A.

**Files:**
- Modify: `plugins/caldav/mutate_test.go`
- Test helpers: `caldav_test.go` (has `startFake`, `startFakeEmpty`, `vevent`, `vtodo`), `leaf_test.go` (has `objectArg`)

**Step 1: Rename existing `TestUpdateNode_*` tests**

- `TestUpdateNode_OverwritesExisting` → `TestPutNode_OverwritesExisting` (change method name in body: `UpdateNode` → `PutNode`)
- `TestUpdateNode_MissingErrors` → `TestPutNode_MissingErrors` (same)
- In `TestMutate_RoundTrip`: change `UpdateNode` → `PutNode` in the call.

**Step 2: Add PatchNode tests**

Add the following test functions after the existing PutNode tests:

```go
func TestPatchNode_UpdatesSummaryLeavesOtherFieldsUntouched(t *testing.T) {
    f, home := startFakeEmpty(t)
    arg := objectArg(home, "/dav/cal/item.ics")
    node := mustParseURL(t, arg)
    ctx := context.Background()

    // Create a VTODO with a known UID and summary.
    if err := (Plugin{}).CreateNode(
        ctx, node, strings.NewReader(vtodo("uid-preserve", "Original Summary")), typeObject,
    ); err != nil {
        t.Fatalf("CreateNode: %v", err)
    }

    // Patch only the summary.
    patch := `{"summary":"Patched Summary"}`
    if err := (Plugin{}).PatchNode(ctx, node, strings.NewReader(patch)); err != nil {
        t.Fatalf("PatchNode: %v", err)
    }

    got := f.resources["/dav/cal/item.ics"]
    if !strings.Contains(got, "SUMMARY:Patched Summary") {
        t.Errorf("patch did not update summary: %q", got)
    }
    // UID was NOT in the patch body — must be untouched.
    if !strings.Contains(got, "UID:uid-preserve") {
        t.Errorf("patch clobbered UID (should be untouched): %q", got)
    }
}

func TestPatchNode_EmptyBodyErrors(t *testing.T) {
    _, home := startFakeEmpty(t)
    arg := objectArg(home, "/dav/cal/item.ics")

    if err := (Plugin{}).PatchNode(
        context.Background(), mustParseURL(t, arg), strings.NewReader(""),
    ); err == nil {
        t.Fatal("PatchNode with empty body must error")
    }
}

func TestPatchNode_RawIcalRejected(t *testing.T) {
    _, home := startFakeEmpty(t)
    arg := objectArg(home, "/dav/cal/item.ics")

    // raw iCalendar is a full-replace — PatchNode must reject it.
    err := (Plugin{}).PatchNode(
        context.Background(), mustParseURL(t, arg), strings.NewReader(vtodo("x", "y")),
    )
    if err == nil {
        t.Fatal("PatchNode with raw iCalendar body must error")
    }
}

func TestPatchNode_MissingObjectErrors(t *testing.T) {
    _, home := startFakeEmpty(t)
    arg := objectArg(home, "/dav/cal/ghost.ics") // does not exist

    if err := (Plugin{}).PatchNode(
        context.Background(), mustParseURL(t, arg), strings.NewReader(`{"summary":"new"}`),
    ); err == nil {
        t.Fatal("PatchNode on a missing object must error")
    }
}
```

**Step 3: Run the tests**

```bash
go test ./plugins/caldav/... -run 'TestPatch|TestPut'
```

Expected: all PASS.

**Step 4: Run the full caldav test suite**

```bash
go test ./plugins/caldav/...
```

Expected: all PASS (no regressions).

**Step 5: Commit**

```bash
git add plugins/caldav/mutate_test.go
git commit -m "test(caldav): rename TestUpdateNode→TestPutNode, add TestPatchNode"
```

---

### Task 9: Amend FDR 0020

**Promotion criteria:** N/A.

**Files:**
- Modify: `docs/features/0020-cud-tree-modifications.md`

**Step 1: Read the file**

Read `docs/features/0020-cud-tree-modifications.md` to locate the Interface Layer 1 section (~lines 72–104) and the Interface Layer 2 / MCP tool binding section (~lines 117–169).

**Step 2: Amend Interface Layer 1**

In the section describing the `NodeMutator` interface signature, replace the `UpdateNode` method entry with `PutNode` (same semantics, renamed) and add `PatchNode` with its semantics. Keep all existing prose — insert a new paragraph explaining the split:

> **2026-07-14 amendment — `UpdateNode` split into `PutNode` + `PatchNode`:**
>
> The original three-method CUD surface (`CreateNode` / `UpdateNode` / `DeleteNode`) has been extended to four methods by splitting `UpdateNode`:
>
> - `PutNode` — the direct rename of `UpdateNode`, semantics unchanged: full-replace, errors if absent.
> - `PatchNode` — new: partial-field update. The body contains only the fields to change; absent fields MUST be left untouched. Implementations MUST NOT error on absent or unrecognized fields (contrast `PutNode`, which requires the body to represent the complete desired state).
>
> **Motivation:** a consumer (`nebulous`) needs to flip single booleans (mark a story read) or rename/move individual fields without reading and re-sending the entire object. A read-modify-write dance on every single-field mutation is wasteful and error-prone (stale data risk). `PatchNode` closes this gap without removing the strict-replace guarantee that `PutNode` callers depend on.

**Step 3: Amend Interface Layer 2 / MCP tool binding**

Rename `update_node` → `put_node` everywhere in this section. Add `patch_node` with the same shape (uri + body), classified as destructive (same permission class as the other CUD tools). Update the example list.

**Step 4: Commit**

```bash
git add docs/features/0020-cud-tree-modifications.md
git commit -m "docs(fdr-0020): amend for UpdateNode→PutNode split + PatchNode"
```

---

### Task 10: Full build and test verification

**Promotion criteria:** all of `go build ./...`, `go test ./...`, and the dagnabit regeneration gate pass clean.

**Files:** none (read-only verification).

**Step 1: Build everything**

```bash
go build ./...
```

Expected: PASS.

**Step 2: Run the full test suite**

```bash
go test ./...
```

Expected: PASS.

**Step 3: Verify dagnabit facade is current**

```bash
just validate-generate-dagnabit
```

Expected: PASS (no drift — facade was regenerated in Task 3).

**Step 4: Lint**

```bash
just lint-go
```

Expected: PASS.

---

### Task 11: Merge via spinclass

**Promotion criteria:** pre-merge hook (`just`) passes.

**Step 1: Confirm nothing-but-the-truth attestation**

Run the spinclass attestation:

```
mcp__plugin_spinclass_spinclass__nothing-but-the-truth
```

**Step 2: Merge asynchronously** (pre-merge hook runs `just` which can take >90s)

```
mcp__plugin_spinclass_spinclass__merge-this-session-async
```

**Step 3: Notify nebulous session**

After merge succeeds, send a chat message to `9625e660-42f1-49d6-8ead-28f133cb28de` (nebulous/deft-larch) with:
- Merge outcome
- Final interface shape (PutNode + PatchNode signatures)
- PatchNode body format for caldav (JSON object of fields to change; component type inferred from current object)
- Any deviations from the original brief

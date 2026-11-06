# What depends on the enriched listing's shape (#367 inventory)

Date: 2026-11-06. Session: cutting-garden/rapid-pine. The first step #367
asks for: before designing a slimmer default for list_nodes / read_node /
resources/read, find everything that pins today's per-node `facets`,
`fields` and top-level `tags`. Line numbers are as of master 36b38d2.

## Findings in brief

- **No in-repo consumer reads the MCP listing JSON.** organize and
  `list -format espalier` work on `Node` structs through
  `internal/node_view`. `list -format json` has its own `{uri,name,type,tags}`
  view (`internal/list/list.go:442-478`) that shares nothing with the MCP
  view, so its roughly 10 bats vectors are unaffected by an MCP-only change.
  No external consumer of the MCP JSON is recorded (fj-cg consumes the
  traversal wire, not this view).
- **resources/read and default list_nodes must stay byte-identical**
  (cutting-garden#86, RFC 0012 §12.6). The Go pin is
  `internal/mcp/listing_compact_test.go:40-48`, and both go through
  `renderNodeViews` (`internal/mcp/resources.go:399-421`). No bats lane pins
  it.
- **Bats pins are spot checks, not vectors:**
  - `mcp.bats:213, 215, 236, 239, 324`: `facets.component`, `fields.summary`,
    `tags`;
  - `ynab.bats:110-115, 174-447`: `facets.category|cleared|payee` and
    `fields.payee|amount|memo`;
  - `traversal_serve.bats:1039-1043`: a `{uri,tags}` projection.
  - Nothing pins `facets.categories`.

## Touch estimate per candidate change

| Change | Code | Go tests | Bats | Docs |
|---|---|---|---|---|
| (a) drop facet entries duplicated by a field or tag (`facets.categories`) | ~4 (`projection.go:138-153`, `resources.go:752-771, 836, 741`) | ~3 (`projection_test.go`) | 0 | ~3 (`tools.go:151`, RFC 0012 :971, FDR 0025 :299) |
| (b) drop per-node constant facets (`component`) | ~4, plus a per-dimension "constant" rule that does not exist yet | few | 2 (`mcp.bats:213, 324`) | ~4 (`tools.go`, `mcp.go:88`, RFC 0012 :959, FDR 0015 :214) |
| (c) drop top-level `tags` from the MCP view | ~8 | ~4 (`projection_test.go:131-240`, `listing_test.go:546-579`) | 2 (`mcp.bats:219-241`, `traversal_serve.bats:1021-1043`) | ~4 (RFC 0012 :965, RFC 0013 :861, FDR 0025 :291, AGENTS.md) |

(a) is the cheapest and was already planned: RFC 0012 :971 and FDR 0025
:299 say `facets.categories` rides under the tag key "until #251".

## Where the shape is documented

- Code: `internal/mcp/mcp.go:28-31, 86-93` (server instructions);
  `internal/mcp/tools.go:148-151, 184-191, 199-226` (tool and schema text);
  `internal/mcp/resources.go:399-403, 710-743`; `internal/mcp/projection.go:13-20`.
- RFC 0012 §12.5 (:955-983) and §12.6 (:987-997); RFC 0013 :856-866.
- FDR 0015 :209-217; FDR 0014 :328; FDR 0025 :291-301; FDR 0024 :334;
  FDR 0021 :200-241.
- AGENTS.md :182.

## Go assertions (for the change itself)

- `internal/mcp/projection_test.go:33-40, 131-221, 240`
- `internal/mcp/listing_test.go:112, 200, 291, 330, 483-488, 535-579`
- `internal/mcp/listing_cap_test.go:73, 107`
- `internal/mcp/listing_compact_test.go:13-48`
- `internal/list/list_test.go:350-411` (the separate `list -format json` view)

## Next step

The cheapest win is (a), and it was promised by #251: one view rule, "a
dimension that a declared field or the tag set already carries is not
repeated under facets", behind no parameter. That's 4 code sites and 3 Go
tests, no bats. (b) and (c) need a `view` parameter or a breaking default,
which is #367's design question proper and needs a ruling.

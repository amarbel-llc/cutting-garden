# cutting_garden_plugins

URI-scheme-keyed registry for cutting-garden capture, restore, and
diff backends. Defines the `Plugin`, `CapturePlugin`,
`RestorePlugin`, and `DiffPlugin` interfaces plus the traversal
capabilities (`RootLister` / `RootProvider`, FDR 0014), and four
package-level registries: one per direction (capture/restore/diff) and
a direction-agnostic **scheme** registry (`scheme_registry.go`).

Each plugin lives in its own peer-leaf package (e.g.
`cutting_garden_plugin_file`) and registers itself in `init()` via
`MustRegisterCapture` / `MustRegisterRestore` / `MustRegisterDiff` — or,
for a plugin that implements none of those (e.g. a `RootProvider`-only
traversal plugin), via `MustRegisterScheme` (RFC 0005). A plugin MAY
support any subset of the directions; the file plugin happens to
implement all three. `RegisteredPlugins()` unions all four registries
(dedup by scheme set), so `list`/`mcp` discover scheme-only plugins too.
The CLI command blank-imports each plugin so registration fires at
binary startup.

## Public SDK facade

The public, out-of-tree-consumable surface of this package is the
dagnabit-generated facade at `pkgs/cutting_garden_plugins` (RFC 0009).
External and (eventually) relocated in-repo plugins import the facade,
never this `internal/` package directly; see `export.go` and
`docs/rfcs/0009-cutting-garden-plugin-sdk.md`.

## Layering

Imports `internal/capture_receipt`, `internal/capture_events` (via
the `Reporter` alias in `reporter.go`), and
`madder/go/pkgs/blob_stores`. The `capture` cmd imports this
package; nothing here imports back. Per-entry results flow over the
unified `capture_events.Stream` (Stage B); the legacy
`capture_sink.Sink` is consumed only by the orchestrator and
`capture_render_legacy`'s bridge.

## More information

Vendored from `madder@7d295b9` (tag `go/v0.3.16`),
`go/internal/hotel/cutting_garden_plugins/`. Madder design context:
`docs/features/0007-cutting-garden-uri-plugins.md` (madder repo).

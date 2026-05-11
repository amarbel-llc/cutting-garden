# cutting_garden_plugins

URI-scheme-keyed registry for cutting-garden capture, restore, and
diff backends. Defines the `Plugin`, `CapturePlugin`,
`RestorePlugin`, and `DiffPlugin` interfaces and three independent
package-level registries (one per direction).

Each plugin lives in its own peer-leaf package (e.g.
`cutting_garden_plugin_file`) and registers itself in `init()` via
`MustRegisterCapture` / `MustRegisterRestore` / `MustRegisterDiff`.
A plugin MAY support any subset of the three directions; the file
plugin happens to implement all three. The CLI command will
blank-import each plugin so registration fires at binary startup.

## Layering

Imports `internal/capture_receipt`, `internal/capture_sink`, and
`madder/go/pkgs/blob_stores`. Future `capture` cmd will import this
package; nothing here imports back.

## More information

Vendored from `madder@7d295b9` (tag `go/v0.3.16`),
`go/internal/hotel/cutting_garden_plugins/`. Madder design context:
`docs/features/0007-cutting-garden-uri-plugins.md` (madder repo).

// Package command_components is cutting-garden's CLI-flavored
// composition layer: cross-command glue that wires the framework into
// madder's pkgs/ substrate (env_dir, env_local, env_ui,
// blob_store_env) and resolves capture receipts to the stores their
// blobs live in.
//
// Mirrors madder's `internal/golf/command_components/` and dodder's
// `internal/echo/command_components/` patterns: one flat package, one
// file per composition concern. Per-command logic (capture's planner,
// restore's runRestore, diff's compareEntries) lives in each
// command's own package; command_components is the seam they share.
//
// Two XDG scopes apply throughout:
//
//   - "madder"  — where cutting-garden reads/writes blob_store config
//     and blobs. cutting-garden is a sibling of madder that operates
//     on madder's stores.
//   - "cutting-garden" — where cutting-garden writes its own per-
//     utility state (captures.log etc.). Distinct from the madder
//     scope by construction (see env_dir.TestMakeDefault_
//     DistinctScopesAreIndependent upstream).
package command_components

import (
	"io"

	"code.linenisgreat.com/madder/go/pkgs/blob_store_env"
	"code.linenisgreat.com/madder/go/pkgs/env_dir"
	"code.linenisgreat.com/madder/go/pkgs/env_local"
	"code.linenisgreat.com/madder/go/pkgs/env_ui"
	"code.linenisgreat.com/madder/go/pkgs/madder_env"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/debug"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// MakeEnvDir builds a madder-family env_dir at the given xdgScope.
// Local reimplementation of madder's
// command_components.MakeEnvDirForScope; honors madder's env-var
// contract via madder_env.DefaultEnvVarNames so MADDER_* overrides
// reach every scope uniformly.
func MakeEnvDir(ctx errors.Context, xdgScope string) env_dir.Env {
	return env_dir.MakeDefault(
		ctx,
		env_dir.Config{EnvVarNames: madder_env.DefaultEnvVarNames},
		xdgScope,
	)
}

// MakeBlobStoreEnv is the local reimplementation of madder's
// command_components.EnvBlobStore.MakeEnvBlobStore mixin: build a
// dewey-context-backed env_local from env_dir + env_ui, then hand it
// to pkgs/blob_store_env.MakeBlobStoreEnv. The xdgScope is hardcoded
// to "madder" — cutting-garden is a sibling of madder that operates
// on madder's stores. The audit-log wiring (SetBlobWriteObserver) on
// the blob-write path is intentionally omitted (madder's inventory
// log is a different observability mechanism from cg's captures.log).
func MakeBlobStoreEnv(ctx errors.Context) blob_store_env.BlobStoreEnv {
	return makeBlobStoreEnvWithOptions(ctx, env_ui.Options{})
}

// MakeBlobStoreEnvWithErr is MakeBlobStoreEnv with the env's err sink
// redirected to errWriter via env_ui's Options.CustomErr. Blob-store
// chatter (lazy SFTP dial / host-key / remote-config / dir-check
// lines) follows the env's err sink as of madder#228, so a caller
// that owns stderr with a TUI (capture's -progress viewport) passes a
// Reporter-backed writer here to keep that chatter from fracturing
// the render. Stdout, stdin, and the UI sink are untouched.
func MakeBlobStoreEnvWithErr(
	ctx errors.Context,
	errWriter io.Writer,
) blob_store_env.BlobStoreEnv {
	return makeBlobStoreEnvWithOptions(ctx, env_ui.Options{CustomErr: errWriter})
}

// makeBlobStoreEnvWithOptions is the shared body: env_ui.Make with
// zero-value cliConfig/debug matches env_ui.MakeDefault exactly, so
// MakeBlobStoreEnv's behavior is unchanged when options is the zero
// Options.
func makeBlobStoreEnvWithOptions(
	ctx errors.Context,
	options env_ui.Options,
) blob_store_env.BlobStoreEnv {
	dir := MakeEnvDir(ctx, "madder")
	ui := env_ui.Make(ctx, nil, debug.Options{}, options)
	return blob_store_env.MakeBlobStoreEnv(env_local.Make(ui, dir))
}

// MakeCgEnvDir builds the cutting-garden-scoped env_dir for cg's own
// per-utility state. Distinct from MakeBlobStoreEnv's madder-scoped
// env_dir — the two address disjoint XDG paths by construction.
func MakeCgEnvDir(ctx errors.Context) env_dir.Env {
	return MakeEnvDir(ctx, "cutting-garden")
}

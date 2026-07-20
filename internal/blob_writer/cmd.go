// Package blob_writer wires the hidden `__write-blob` subcommand: the
// RFC 0002 §Writer Protocol sink. An external capturer subprocess (the
// web binding's chrest reference implementation) is handed a
// `writer.cmd` argv pointing at this command; it pipes each node blob's
// raw bytes to stdin and reads back a single JSON line
// `{"id":"<markl-id>","size":<n>}`.
//
// The subprocess re-resolves the destination store by name from the same
// cutting-garden environment the orchestrating `capture` command did, so
// blobs land in exactly the store the capture is targeting. The `__`
// prefix marks it as plumbing, not a user-facing verb.
package blob_writer

import (
	"fmt"
	"os"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/cutting-garden/internal/plugin_blob_io"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/interfaces"
)

// WriteBlob is the value registered for the `__write-blob` subcommand.
// Store is bound to `--store` by SetFlagDefinitions; empty selects the
// default store.
type WriteBlob struct {
	Store string
}

var (
	_ command.Cmd                       = (*WriteBlob)(nil)
	_ interfaces.CommandComponentWriter = (*WriteBlob)(nil)
)

// New constructs a WriteBlob targeting the default store.
func New() *WriteBlob { return &WriteBlob{} }

func (*WriteBlob) GetDescription() command.Description {
	return command.Description{
		Short: "internal: stream stdin into a blob store (RFC 0002 writer protocol)",
	}
}

// CommandHidden marks `__write-blob` as framework plumbing: chrest invokes
// it as its writer.cmd, so it must stay dispatchable but never appear in
// the usage banner, the manpages, or tab-completion candidates.
func (*WriteBlob) CommandHidden() {}

func (cmd *WriteBlob) SetFlagDefinitions(flagSet interfaces.CLIFlagDefinitions) {
	flagSet.StringVar(&cmd.Store, "store", "",
		"destination blob store name (empty selects the default store)")
}

func (cmd *WriteBlob) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	env := command_components.MakeBlobStoreEnv(ctx)

	var store blob_stores.BlobStoreInitialized
	if cmd.Store == "" {
		store = env.GetDefaultBlobStore()
	} else {
		s, ok := env.GetBlobStores()[cmd.Store]
		if !ok {
			errors.ContextCancelWithBadRequestf(ctx,
				"unknown blob store %q", cmd.Store)
			return
		}
		store = s
	}

	id, size, err := plugin_blob_io.WriteReaderBlob(ctx, store, os.Stdin)
	if err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}

	// An RFC 0002 node blob is always framed (type header + body), so it
	// is never zero bytes. A zero-byte read means the writer closed stdin
	// without sending anything — a truncated/empty transfer — so refuse it
	// rather than emit a receipt for an empty blob the caller would treat
	// as a successfully written node.
	if size == 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"__write-blob: refusing empty stdin (no node bytes received)")
		return
	}

	// RFC 0002 §Writer Protocol: exactly one JSON line on success.
	if _, err := fmt.Fprintf(os.Stdout, "{\"id\":%q,\"size\":%d}\n", id.String(), size); err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}
}

// Package restore wires the `restore` subcommand: parse a receipt
// blob, validate destination preconditions and per-entry path
// sanitization, then materialize the captured tree onto disk.
//
// Positional surface:
//
//	restore [-store STORE_ID] RECEIPT_ID DEST
//
// Semantics are normative per FDR 0001 (docs/features/0001-restore.md
// upstream) and RFC 0003 §Consumer Rules.
//
// Phase 3 step 2: skeleton only. Flag parsing and positional arg
// extraction are wired; the actual receipt fetch / sanitization /
// materialization pipeline lands in steps 3–6.
package restore

import (
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/0/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// Restore is the value registered for the `restore` subcommand.
//
// Store is bound to the `-store` flag by SetFlagDefinitions; when
// non-empty it overrides the receipt's store-hint resolution per FDR
// §Store-Hint Resolution branch 1.
type Restore struct {
	Store string
}

var (
	_ command.Cmd                       = (*Restore)(nil)
	_ interfaces.CommandComponentWriter = (*Restore)(nil)
)

// New constructs a Restore with default flag values.
func New() *Restore {
	return &Restore{}
}

func (*Restore) GetDescription() command.Description {
	return command.Description{
		Short: "restore a captured tree from a receipt blob",
	}
}

func (cmd *Restore) SetFlagDefinitions(
	flagSet interfaces.CLIFlagDefinitions,
) {
	flagSet.StringVar(
		&cmd.Store,
		"store",
		"",
		"explicit blob-store-id to resolve the receipt and entry blobs "+
			"against (overrides the receipt's store-hint resolution)",
	)
}

func (cmd *Restore) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	receiptID := req.PopArg("receipt-id")
	dest := req.PopArg("dest")

	if req.RemainingArgCount() > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"too many positional arguments; restore takes exactly two "+
				"(<receipt-id> <dest>), trailing: %v", req.PeekArgs())
		return
	}

	// Steps 3–6 land here. The args are consumed so the framework
	// records them in the request's audit trail, but no work is
	// dispatched yet.
	_ = receiptID
	_ = dest

	errors.ContextCancelWithBadRequestf(ctx,
		"restore: not yet implemented (Phase 3 step 2 skeleton; "+
			"fetch/sanitize/materialize land in steps 3-6)")
}

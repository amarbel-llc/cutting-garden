// Package failures wires the `failures` subcommand: fetch a
// cutting_garden-capture_failures-v1 blob (written by capture whenever
// entries failed or the run was aborted) and pretty-print it for
// triage.
//
// Positional surface:
//
//	failures [-store STORE_ID] [-format text|json] FAILURE_RECEIPT_ID
//
// Semantics follow the failure-receipt design
// (docs/plans/2026-06-07-capture-failure-receipt-design.md §CLI):
// store resolution mirrors restore's (probe all stores, `-store`
// overrides); text mode prints the metadata header then one tabbed
// line per failure; json mode re-emits the failures as NDJSON. Exit 0
// on success, 2 when the receipt cannot be read, 64 on bad flags/args.
package failures

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_failures"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/piggy/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
)

// Format flag values.
const (
	formatText = "text"
	formatJSON = "json"
)

// Failures is the value registered for the `failures` subcommand.
//
// Store mirrors Restore.Store: when non-empty, the receipt is resolved
// against that store directly instead of probing all configured
// stores.
//
// Format selects the rendering: text (default, metadata header + one
// line per failure) or json (body-only NDJSON, one FailureV1 object
// per line). Validated in Run.
//
// output is the writer the rendered receipt goes to. Defaults to
// os.Stdout in New(); tests use newWithOutput to inject a
// bytes.Buffer.
type Failures struct {
	Store  string
	Format string
	output io.Writer
}

var (
	_ command.Cmd                       = (*Failures)(nil)
	_ interfaces.CommandComponentWriter = (*Failures)(nil)
)

// New constructs a Failures with default flag values; output routes
// to os.Stdout.
func New() *Failures {
	return &Failures{Format: formatText, output: os.Stdout}
}

// newWithOutput is the test-only constructor that routes the rendered
// receipt to the supplied writer.
func newWithOutput(output io.Writer) *Failures {
	return &Failures{Format: formatText, output: output}
}

func (*Failures) GetDescription() command.Description {
	return command.Description{
		Short: "inspect a capture failure receipt",
		Long: "Reads a cutting_garden-capture_failures-v1 blob \\(em the " +
			"durable record `capture` writes alongside its success receipt " +
			"whenever entries failed or the run was aborted by a signal " +
			"\\(em and pretty-prints it for triage: the outcome (failures " +
			"or aborted), the paired success-receipt id, the capture's " +
			"root arguments, the captured/failed counts, then one line " +
			"per failed entry (operation, root, path, error).",
	}
}

func (cmd *Failures) SetFlagDefinitions(
	flagSet interfaces.CLIFlagDefinitions,
) {
	flagSet.StringVar(
		&cmd.Store,
		"store",
		"",
		"explicit blob-store-id to resolve the failure receipt against "+
			"(overrides probing all configured stores)",
	)
	flagSet.StringVar(
		&cmd.Format,
		"format",
		formatText,
		"output format: text (metadata header + one tabbed line per "+
			"failure) or json (body-only NDJSON, one failure object "+
			"per line)",
	)
}

func (cmd *Failures) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	if err := validateFormat(cmd.Format); err != nil {
		errors.ContextCancelWithBadRequestf(ctx, "%s", err.Error())
		return
	}

	idStr := req.PopArg("failure-receipt-id")

	if req.RemainingArgCount() > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"too many positional arguments; failures takes exactly one "+
				"(<failure-receipt-id>), trailing: %v", req.PeekArgs())
		return
	}

	if err := cmd.runFailures(ctx, idStr); err != nil {
		errors.ContextCancelWithError(ctx, err)
	}
}

// runFailures implements the cmd in three phases: parse the id,
// resolve the store holding the blob (probe order matches restore's),
// then fetch + render per -format.
func (cmd *Failures) runFailures(ctx errors.Context, idStr string) error {
	var id markl.Id
	if err := id.Set(idStr); err != nil {
		return errors.Wrapf(err, "parse failure-receipt-id %q", idStr)
	}

	envBlobStore := command_components.MakeBlobStoreEnv(ctx)

	store, err := command_components.LocateReceiptStore(
		envBlobStore, &id, cmd.Store,
	)
	if err != nil {
		return err
	}

	v, err := capture_failures.Read(store, &id)
	if err != nil {
		return errors.Wrapf(err, "read failure receipt %s", &id)
	}

	if cmd.Format == formatJSON {
		return writeNDJSON(cmd.output, v.Failures)
	}
	return writeText(cmd.output, v)
}

// writeText renders the metadata header then one
// `<op>\t<root>\t<path>\t<error>` line per failure.
func writeText(w io.Writer, v *capture_failures.V1) error {
	outcome := v.Meta.Outcome
	if v.Meta.Signal != "" {
		outcome += " (" + v.Meta.Signal + ")"
	}
	receipt := v.Meta.Receipt
	if receipt == "" {
		receipt = "(none)"
	}

	if _, err := fmt.Fprintf(
		w,
		"outcome: %s\nreceipt: %s\nroots: %s\ncaptured: %d  failed: %d\n",
		outcome, receipt, strings.Join(v.Meta.Roots, " "),
		v.Meta.Captured, v.Meta.Failed,
	); err != nil {
		return errors.Wrap(err)
	}

	for _, f := range v.Failures {
		if _, err := fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\n", f.Op, f.Root, f.Path, f.Error,
		); err != nil {
			return errors.Wrap(err)
		}
	}

	return nil
}

// writeNDJSON re-emits the failure body as NDJSON — one FailureV1
// object per line, no metadata — for piping into jq or a future
// `capture --retry`.
func writeNDJSON(w io.Writer, failures []capture_failures.FailureV1) error {
	enc := json.NewEncoder(w)
	for _, f := range failures {
		if err := enc.Encode(f); err != nil {
			return errors.Wrap(err)
		}
	}
	return nil
}

// validateFormat enforces the -format value constraint. Returns nil
// for the two allowed values, an error otherwise. Mirrors
// diff.validateColor.
func validateFormat(value string) error {
	switch value {
	case formatText, formatJSON:
		return nil
	}
	return errors.ErrorWithStackf(
		"invalid -format value %q; expected text or json", value,
	)
}

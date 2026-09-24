package organize

import (
	"fmt"
	"io"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// refusal is one edit planning refuses but that is SEPARABLE from the
// document's other edits: dropping it invalidates nothing else the document
// asks for (the object's tag atoms, field edits and trailer, and every other
// object's edits, still stand). A headless apply refuses the whole document on
// any refusal (Err, exit 64, nothing written); at a terminal the user may strip
// the refused edits and continue with the rest (resolveRefusals).
//
// The one class today is a move of a non-clearable write:one dimension into
// the no-value section (forge organize F12). Planned candidates for the same
// record: an unknown non-`+` box id (F8c) and a move + same-property atom edit
// (#271, rejectMoveFieldCollisions). The document parser tracks no line
// numbers, so a refusal names the object by its box id.
type refusal struct {
	ObjectID string
	// Field is the dimension / field the refused edit writes.
	Field string
	// Reason is the one-line human cause, shown in the strip prompt.
	Reason string
	// Kind names the refused edit in the final "skipped:" summary (e.g. "move").
	Kind string
	// Err is the refusal as the headless path reports it.
	Err error
}

// partitionRefusedMoves splits moves into the ones to plan on and the
// separable refusals: a move into the no-value section (To "") of a write:one
// dimension not declared clearable. Every other move — including ones
// checkMoveWritable refuses for a NON-separable reason (read-only, unmapped) —
// is kept, so those refusals still abort as before.
func partitionRefusedMoves(
	moves []move, writes map[string]cgp.FacetWrite, idOf boxIDer,
) (kept []move, refused []refusal) {
	for _, mv := range moves {
		w, mapped := writes[mv.Node.Type]
		if mv.To != "" || !mapped || w.Mode != cgp.FacetWriteOne || w.Clearable {
			kept = append(kept, mv)
			continue
		}
		refused = append(refused, refusal{
			ObjectID: idOf(mv.URI),
			Field:    w.DimensionKey,
			Reason: fmt.Sprintf(
				"moved into the no-value section, but %q can't be cleared", w.DimensionKey,
			),
			Kind: "move",
			Err:  checkMoveWritable(writes, mv),
		})
	}
	return kept, refused
}

// resolveRefusals is the refusal gate, run BEFORE the diff and the commit
// confirm. Headless (interactive false) it refuses exactly as before: the
// first refusal's error, nothing written. At a terminal it lists the refusals
// and asks whether to drop them and continue with the remaining changes;
// declining returns the same error (the edited buffer is kept by the caller),
// accepting returns nil and the caller plans on without them.
func (cmd *Organize) resolveRefusals(
	refused []refusal, remaining int, interactive bool,
) error {
	if len(refused) == 0 {
		return nil
	}
	if !interactive {
		return refused[0].Err
	}

	writeRefusals(cmd.output, refused)
	pronoun := "it"
	if len(refused) > 1 {
		pronoun = "them"
	}
	ok, err := cmd.ask(fmt.Sprintf(
		"Drop %s and continue with the remaining %d change(s)?", pronoun, remaining,
	))
	if err != nil {
		return err
	}
	if !ok {
		return refused[0].Err
	}
	return nil
}

// writeRefusals prints the refused edits, one per line.
func writeRefusals(w io.Writer, refused []refusal) {
	noun := "edit"
	if len(refused) > 1 {
		noun = "edits"
	}
	fmt.Fprintf(w, "organize: %d %s can't be applied:\n", len(refused), noun)
	for _, r := range refused {
		fmt.Fprintf(w, "  %s  %s: %s\n", r.ObjectID, r.Field, r.Reason)
	}
}

// reportSkipped names the dropped edits once more at the end, so a stripped
// edit is never forgotten. A no-op when nothing was dropped.
func (cmd *Organize) reportSkipped(refused []refusal) {
	if len(refused) == 0 {
		return
	}
	names := make([]string, len(refused))
	for i, r := range refused {
		names[i] = fmt.Sprintf("%s %s %s", r.ObjectID, r.Field, r.Kind)
	}
	fmt.Fprintf(cmd.output, "organize: skipped: %s\n", strings.Join(names, ", "))
}

package ynab

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Renaming a payee (FDR 0027, cutting-garden#333). A payee is only ever
// RENAMED here: YNAB's API creates, reads and renames payees but has no merge,
// and it is not documented what a rename onto an existing payee's name does. So
// a rename that would take another live payee's name is refused rather than
// risked, and the refusal says merging is done in YNAB itself.

// payeePatchFields are the PatchNode keys recognized on a payee.
var payeePatchFields = []string{"name"}

// patchPayee renames the payee ref names:
//
//	{"name": "Service: Anthropic"}
//
// It reads the budget's payees fresh (the guards must see the names as they are
// now, not as a listing cached up to a minute ago), refuses a transfer payee
// (YNAB owns those), an empty name and a name another live payee already holds,
// sends one rename, and checks the payee YNAB returns carries the new name. A
// body naming nothing recognized, or a name the payee already has, sends no
// write and reports a non-nil EMPTY applied (cutting-garden#182).
func patchPayee(ctx context.Context, ref nodeRef, body io.Reader) ([]string, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.BadRequestf("ynab plugin: PatchNode body must be JSON; got empty body")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, errors.BadRequestf("ynab plugin: invalid patch JSON: %s", err)
	}
	recognized := cutting_garden_plugins.RecognizedPatchFields(fields, payeePatchFields)
	if len(recognized) == 0 {
		return recognized, nil // nothing recognized: no fetch, honest report
	}
	name, err := stringField("name", fields["name"])
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.BadRequestf("ynab plugin: patch field \"name\" must not be empty")
	}

	c, err := resolveClient(ref)
	if err != nil {
		return nil, err
	}
	budgetID, err := resolveBudgetID(ctx, c, ref)
	if err != nil {
		return nil, err
	}
	payees, err := c.freshPayees(ctx, budgetID)
	if err != nil {
		return nil, err
	}
	current, err := payeeForRename(payees, ref.payeeID, name)
	if err != nil {
		return nil, err
	}
	if current.Name == name {
		return []string{}, nil // already named so: no write
	}

	saved, err := c.renamePayee(ctx, budgetID, current.ID, name)
	if err != nil {
		return nil, err
	}
	if saved.ID != current.ID || saved.Name != name {
		return nil, errors.ErrorWithStackf(
			"ynab plugin: the rename of payee %q was not reflected in YNAB's response", current.ID,
		)
	}
	return recognized, nil
}

// payeeForRename finds the payee to rename in the budget's payee list and
// checks the rename is one this plugin will make.
func payeeForRename(payees []Payee, id, name string) (Payee, error) {
	var current *Payee
	for i := range payees {
		if payees[i].ID == id && !payees[i].Deleted {
			current = &payees[i]
			break
		}
	}
	if current == nil {
		return Payee{}, errors.BadRequestf("ynab plugin: no payee with id %q", id)
	}
	if current.isTransfer() {
		return Payee{}, errors.BadRequestf(
			"ynab plugin: payee %q is a transfer payee; YNAB names it after its account "+
				"and keeps it in step, so it is not renamed here", current.Name,
		)
	}
	for _, other := range livePayees(payees) {
		if other.ID != id && strings.EqualFold(strings.TrimSpace(other.Name), name) {
			return Payee{}, errors.BadRequestf(
				"ynab plugin: renaming %q to %q would collide with the existing payee %q (%s); "+
					"merging payees is done in YNAB itself, and it is not known what a rename "+
					"onto an existing name does",
				current.Name, name, other.Name, other.ID,
			)
		}
	}
	return *current, nil
}

// Package tap_diagnostics builds the YAML-shaped diagnostics block
// attached to a TAP `not ok` line. Generic helper — error in, map out;
// special-cases the markl error types that carry their own structured
// fields.
//
// VENDORED FROM madder@7d295b9 (tag go/v0.3.16),
// go/internal/charlie/tap_diagnostics/. Once madder exposes this via
// dagnabit as pkgs/tap_diagnostics (see madder#165), delete this copy
// and rewrite imports.
package tap_diagnostics

import (
	"errors"
	"fmt"

	"github.com/amarbel-llc/madder/go/pkgs/markl"
)

func FromError(err error) map[string]string {
	diag := map[string]string{
		"severity": "fail",
		"message":  err.Error(),
	}

	var errNotEqual markl.ErrNotEqual
	if errors.As(err, &errNotEqual) {
		diag["expected"] = fmt.Sprintf("%s", errNotEqual.Expected)
		diag["actual"] = fmt.Sprintf("%s", errNotEqual.Actual)
		return diag
	}

	var errIsNull markl.ErrIsNull
	if errors.As(err, &errIsNull) {
		diag["field"] = errIsNull.Purpose
		return diag
	}

	return diag
}

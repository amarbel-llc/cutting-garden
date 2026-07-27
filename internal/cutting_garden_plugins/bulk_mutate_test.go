package cutting_garden_plugins

import (
	"net/url"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}

	return u
}

// TestBulkRequestValidate covers RFC 0017's request-shape rules: the one
// valid changeset and sweep, plus every bad-request case the plugin must
// reject before applying anything.
func TestBulkRequestValidate(t *testing.T) {
	uri := mustURL(t, "x://h/a")
	root := mustURL(t, "x://h/box")

	for _, tc := range []struct {
		name    string
		req     BulkRequest
		wantBad bool
	}{
		{
			name: "valid changeset",
			req: BulkRequest{
				Atomicity: BulkBestEffort,
				Ops:       []BulkOp{{Kind: BulkPatch, URI: uri}},
			},
		},
		{
			name: "valid sweep",
			req: BulkRequest{
				Atomicity: BulkAtomic,
				Sweep:     &BulkSweep{Root: root, Op: BulkOp{Kind: BulkDelete}},
			},
		},
		{
			name:    "missing atomicity",
			req:     BulkRequest{Ops: []BulkOp{{Kind: BulkPatch, URI: uri}}},
			wantBad: true,
		},
		{
			name: "both ops and sweep",
			req: BulkRequest{
				Atomicity: BulkBestEffort,
				Ops:       []BulkOp{{Kind: BulkPatch, URI: uri}},
				Sweep:     &BulkSweep{Root: root, Op: BulkOp{Kind: BulkPatch}},
			},
			wantBad: true,
		},
		{
			name:    "neither ops nor sweep",
			req:     BulkRequest{Atomicity: BulkBestEffort},
			wantBad: true,
		},
		{
			name: "empty ops",
			req: BulkRequest{
				Atomicity: BulkBestEffort,
				Ops:       []BulkOp{},
			},
			wantBad: true,
		},
		{
			name: "op with no uri",
			req: BulkRequest{
				Atomicity: BulkBestEffort,
				Ops:       []BulkOp{{Kind: BulkPatch}},
			},
			wantBad: true,
		},
		{
			name: "op with unknown kind",
			req: BulkRequest{
				Atomicity: BulkBestEffort,
				Ops:       []BulkOp{{Kind: "frobnicate", URI: uri}},
			},
			wantBad: true,
		},
		{
			name: "sweep with no root",
			req: BulkRequest{
				Atomicity: BulkBestEffort,
				Sweep:     &BulkSweep{Op: BulkOp{Kind: BulkPatch}},
			},
			wantBad: true,
		},
		{
			name: "create inside a sweep",
			req: BulkRequest{
				Atomicity: BulkBestEffort,
				Sweep:     &BulkSweep{Root: root, Op: BulkOp{Kind: BulkCreate}},
			},
			wantBad: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			switch {
			case tc.wantBad && err == nil:
				t.Fatal("Validate = nil, want a bad-request error")
			case tc.wantBad && !errors.Is400BadRequest(err):
				t.Fatalf("Validate = %v, want Is400BadRequest", err)
			case !tc.wantBad && err != nil:
				t.Fatalf("Validate = %v, want nil", err)
			}
		})
	}
}

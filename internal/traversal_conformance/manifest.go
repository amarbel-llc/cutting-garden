// Package traversal_conformance is the RFC 0013 session-level
// conformance driver (cutting-garden#186): it launches a traversal peer
// via the transport's own bring-up grammar, drives the slice-1 case list
// against the peer's RAW wire responses, and reports TAP 14. The raw
// posture is the point — the driver deliberately sits BELOW the
// WirePlugin adapter, whose boundary normalization (the #173 breakdown
// re-sort/cap/filter, and any future repair) would correct a peer's
// non-conformance before an adapter-mediated driver could observe it.
//
// What the protocol cannot know generically — patch bodies are
// plugin-defined, probe containers are per-tree — arrives via a per-peer
// Manifest (the parameterization fj-cg predicted; see the plan in
// docs/plans/2026-07-23-traversal-conformance-driver.md).
package traversal_conformance

import (
	"os"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
	"code.linenisgreat.com/tommy/pkg/document"
)

// Manifest is one peer's conformance parameterization. The format is
// TOML, decoded through tommy's cst — the TOML surface already in the
// dependency graph (the config subsystem's codec, RFC 0007) — rather
// than a new decoder dependency. The decode is hand-rolled instead of
// tommy-codegen'd because the manifest is read-only driver input: it
// needs none of the Encode round-trip, comment preservation, or
// config-section machinery the generated codecs exist for, and a
// hand-rolled decode keeps this package free of the codegen lane.
type Manifest struct {
	// Command is the argv that launches the peer (RFC 0013 §Launch and
	// rendezvous). REQUIRED.
	Command []string
	// ConfigTOML is the peer's own config section, passed verbatim
	// through initialize (RFC 0007 §Plugin-Owned Sections). Optional.
	ConfigTOML string
	// Schemes is the expected schemes echo in the initialize result.
	// REQUIRED.
	Schemes []string
	// WritableContainer is the container URI under which mutation
	// probes may be created and deleted (the probe-hygiene discipline
	// from the #180 arc). Required only for a mutate-capable peer.
	WritableContainer string
	// Create describes the probe node the patch tri-state case creates
	// under WritableContainer.
	Create CreateSpec
	// PatchRecognized is a patch body every field of which the peer
	// recognizes, with the exact applied set it must report
	// (cutting-garden#182).
	PatchRecognized PatchRecognizedSpec
	// PatchUnrecognizedOnly is a patch body NO field of which the peer
	// recognizes — the present-empty applied case. An empty Body means
	// the peer tolerates every key (no unrecognized field is
	// constructible, e.g. the cgtest testpeer's merge-anything patch
	// format) and the case is SKIPped rather than failed.
	PatchUnrecognizedOnly PatchSpec
	// PatchWrongTyped is a patch body the peer must reject as a caller
	// mistake: JSON-RPC -32602 (cutting-garden#185).
	PatchWrongTyped PatchSpec
	// FacetContainer, when non-nil, names a container whose
	// facets.counts result should carry the RFC 0012 §13 by_container
	// breakdown, plus the filter the breakdown cases apply. Optional —
	// omitting it skips the breakdown cases (a conformant peer may
	// simply not emit one).
	FacetContainer *FacetContainerSpec
	// ContainerBody, when non-nil, names a CONTAINER that also carries its
	// own body (RFC 0018 §7 / cutting-garden#168): a node with children
	// AND a leaf.read body. The driver asserts nodes.list returns children,
	// leaf.read returns a body despite them (§7.1), and the node's URI
	// resolves through its declared uri_template to a body-declaring type
	// (§5). Optional — omitting it SKIPs the container-body cases.
	ContainerBody *ContainerBodySpec
	// BulkMutate, when non-nil, parameterizes the node.bulk_mutate case
	// (RFC 0017 / cutting-garden#196): a writable container the case
	// creates a probe under. Optional — omitting it SKIPs the bulk case
	// even when the peer advertises bulk-mutate (the manifest author opts
	// in, as with facet_container / container_body).
	BulkMutate *BulkMutateSpec
}

// CreateSpec is the node.create parameterization for the probe node:
// a declared node type tag and the (plugin-defined) create body.
type CreateSpec struct {
	Type string
	Body string
}

// PatchRecognizedSpec pairs a recognized-fields patch body with the
// exact applied set the peer must report (order-insensitive).
type PatchRecognizedSpec struct {
	Body          string
	ExpectApplied []string
}

// PatchSpec is a bare plugin-defined patch body.
type PatchSpec struct {
	Body string
}

// FacetContainerSpec names the breakdown-case container and filter.
type FacetContainerSpec struct {
	URI string
	// Filter is a comma-separated list of dimension=value equality
	// predicates (the RFC 0012 §6 filter, in the same surface grammar
	// `list --filter` speaks); empty matches everything.
	Filter string
}

// ContainerBodySpec names a container that also carries its own body
// (RFC 0018 §7 / cutting-garden#168).
type ContainerBodySpec struct {
	URI string
}

// BulkMutateSpec parameterizes the node.bulk_mutate conformance case
// (RFC 0017 / cutting-garden#196). The case runs a best-effort changeset of
// [create Container/<probe>, delete Container/<probe>-missing]: the create
// must land in applied and the deliberately-missing delete in failed,
// without the call itself erroring. So only the writable Container plus the
// probe's CreateType/CreateBody are needed; the missing sibling is derived.
type BulkMutateSpec struct {
	// Container is the writable container the probe create op targets.
	Container string
	// CreateType is the probe node's type (the bulk create op's type).
	CreateType string
	// CreateBody is the probe node's body.
	CreateBody string
}

// LoadManifest reads and decodes a TOML manifest. Every failure is a
// caller mistake (errors.BadRequestf), so the conformance binary can
// map it to EX_USAGE. Unknown keys are rejected rather than ignored: a
// typo'd manifest key silently narrowing the case list is exactly the
// false ratification a conformance tool must not hand out.
func LoadManifest(path string) (*Manifest, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.BadRequestf("manifest %s: %s", path, err)
	}

	doc, err := document.Parse(input)
	if err != nil {
		return nil, errors.BadRequestf("manifest %s: %s", path, err)
	}

	model, err := cst.Decompose(doc.Root())
	if err != nil {
		return nil, errors.BadRequestf("manifest %s: %s", path, err)
	}

	manifest := &Manifest{}

	if err := decodeStringSlice(model, "command", &manifest.Command); err != nil {
		return nil, err
	}
	if err := decodeString(model, "config_toml", &manifest.ConfigTOML); err != nil {
		return nil, err
	}
	if err := decodeStringSlice(model, "schemes", &manifest.Schemes); err != nil {
		return nil, err
	}
	if err := decodeString(
		model, "writable_container", &manifest.WritableContainer,
	); err != nil {
		return nil, err
	}

	if sub, ok, err := decodeTable(model, "create"); err != nil {
		return nil, err
	} else if ok {
		if err := decodeString(sub, "type", &manifest.Create.Type); err != nil {
			return nil, err
		}
		if err := decodeString(sub, "body", &manifest.Create.Body); err != nil {
			return nil, err
		}
	}

	if sub, ok, err := decodeTable(model, "patch_recognized"); err != nil {
		return nil, err
	} else if ok {
		if err := decodeString(
			sub, "body", &manifest.PatchRecognized.Body,
		); err != nil {
			return nil, err
		}
		if err := decodeStringSlice(
			sub, "expect_applied", &manifest.PatchRecognized.ExpectApplied,
		); err != nil {
			return nil, err
		}
	}

	if sub, ok, err := decodeTable(model, "patch_unrecognized_only"); err != nil {
		return nil, err
	} else if ok {
		if err := decodeString(
			sub, "body", &manifest.PatchUnrecognizedOnly.Body,
		); err != nil {
			return nil, err
		}
	}

	if sub, ok, err := decodeTable(model, "patch_wrong_typed"); err != nil {
		return nil, err
	} else if ok {
		if err := decodeString(
			sub, "body", &manifest.PatchWrongTyped.Body,
		); err != nil {
			return nil, err
		}
	}

	if sub, ok, err := decodeTable(model, "facet_container"); err != nil {
		return nil, err
	} else if ok {
		spec := &FacetContainerSpec{}
		if err := decodeString(sub, "uri", &spec.URI); err != nil {
			return nil, err
		}
		if err := decodeString(sub, "filter", &spec.Filter); err != nil {
			return nil, err
		}
		manifest.FacetContainer = spec
	}

	if sub, ok, err := decodeTable(model, "container_body"); err != nil {
		return nil, err
	} else if ok {
		spec := &ContainerBodySpec{}
		if err := decodeString(sub, "uri", &spec.URI); err != nil {
			return nil, err
		}
		manifest.ContainerBody = spec
	}

	if sub, ok, err := decodeTable(model, "bulk_mutate"); err != nil {
		return nil, err
	} else if ok {
		spec := &BulkMutateSpec{}
		if err := decodeString(sub, "container", &spec.Container); err != nil {
			return nil, err
		}
		if err := decodeString(sub, "create_type", &spec.CreateType); err != nil {
			return nil, err
		}
		if err := decodeString(sub, "create_body", &spec.CreateBody); err != nil {
			return nil, err
		}
		manifest.BulkMutate = spec
	}

	if leftover := model.Undecoded(); len(leftover) > 0 {
		return nil, errors.BadRequestf(
			"manifest %s: unknown keys %v", path, leftover,
		)
	}

	if len(manifest.Command) == 0 {
		return nil, errors.BadRequestf(
			"manifest %s: command is required", path,
		)
	}
	if len(manifest.Schemes) == 0 {
		return nil, errors.BadRequestf(
			"manifest %s: schemes is required", path,
		)
	}

	return manifest, nil
}

// decodeString extracts an optional string key; absent leaves into
// untouched, present-but-not-a-string is a type error.
func decodeString(model *cst.Value, key string, into *string) error {
	value, ok := model.Get(key)
	if !ok {
		return nil
	}

	if value.Kind != cst.VLeaf {
		return errors.BadRequestf("manifest: %s must be a string", key)
	}

	text, ok := cst.ExtractString(value.Leaf)
	if !ok {
		return errors.BadRequestf("manifest: %s must be a string", key)
	}

	*into = text
	value.MarkConsumed()

	return nil
}

// decodeStringSlice extracts an optional string-array key; a
// heterogeneous array is a type error (cst.ExtractStringSlice refuses
// partial slices).
func decodeStringSlice(model *cst.Value, key string, into *[]string) error {
	value, ok := model.Get(key)
	if !ok {
		return nil
	}

	if value.Kind != cst.VLeaf {
		return errors.BadRequestf(
			"manifest: %s must be an array of strings", key,
		)
	}

	items, ok := cst.ExtractStringSlice(value.Leaf)
	if !ok {
		return errors.BadRequestf(
			"manifest: %s must be an array of strings", key,
		)
	}

	*into = items
	value.MarkConsumed()

	return nil
}

// decodeTable resolves an optional sub-table key, marking it seen so
// Undecoded descends into it (leaves inside are consumed field by
// field, surfacing typos WITHIN a table too).
func decodeTable(
	model *cst.Value, key string,
) (sub *cst.Value, ok bool, err error) {
	value, found := model.Get(key)
	if !found {
		return nil, false, nil
	}

	if value.Kind != cst.VTable {
		return nil, false, errors.BadRequestf(
			"manifest: %s must be a table", key,
		)
	}

	value.MarkSeen()

	return value, true, nil
}

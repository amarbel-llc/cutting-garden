package traversal_conformance

import (
	"context"
	"encoding/json"
	"fmt"

	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

const namePatchIgnored = "node.patch: ignored names the body keys tolerated but not applied"

// casePatchIgnored pins the node.patch result's optional ignored member
// (cutting-garden#376) on a probe node: a body naming the manifest's
// supported and unsupported keys reports exactly the expected applied and
// ignored sets, and the two are disjoint. A peer whose manifest omits
// patch_ignored (one that tolerates every key, or does not report ignored)
// SKIPs, as does a read-only peer.
func (r *runner) casePatchIgnored(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, perCaseDeadline)
	defer cancel()

	spec := r.manifest.PatchIgnored
	switch {
	case !r.hasCapability(traversal_serve.CapMutate):
		r.tap.Skip(namePatchIgnored, "mutate not advertised")
		return
	case spec.Body == "":
		r.tap.Skip(namePatchIgnored, "manifest omits patch_ignored")
		return
	}

	probe, ok := r.createProbe(ctx)
	if !ok {
		r.tap.NotOk(namePatchIgnored, map[string]string{"error": "probe create failed"})
		return
	}
	defer func() {
		_ = r.session.Call(context.WithoutCancel(ctx), traversal_serve.MethodNodeDelete,
			traversal_serve.NodeDeleteParams{URI: probe}, nil)
	}()

	var raw json.RawMessage
	if err := r.session.Call(ctx, traversal_serve.MethodNodePatch,
		traversal_serve.NodePatchParams{URI: probe, BodyBase64: encodeBody(spec.Body)}, &raw,
	); err != nil {
		r.tap.NotOk(namePatchIgnored, map[string]string{"error": err.Error()})
		return
	}
	var result struct {
		Applied []string `json:"applied"`
		Ignored []string `json:"ignored"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		r.tap.NotOk(namePatchIgnored, map[string]string{
			"result": fmt.Sprintf("%s: %s", raw, err),
		})
		return
	}
	problems := map[string]string{}
	if !sameStringSet(result.Applied, spec.ExpectApplied) {
		problems["applied"] = fmt.Sprintf("%v, manifest expects %v", result.Applied, spec.ExpectApplied)
	}
	if !sameStringSet(result.Ignored, spec.ExpectIgnored) {
		problems["ignored"] = fmt.Sprintf("%v, manifest expects %v", result.Ignored, spec.ExpectIgnored)
	}
	for _, key := range result.Ignored {
		for _, applied := range result.Applied {
			if key == applied {
				problems["disjoint"] = fmt.Sprintf("%q is both applied and ignored", key)
			}
		}
	}
	if len(problems) > 0 {
		r.tap.NotOk(namePatchIgnored, problems)
		return
	}
	r.tap.Ok(namePatchIgnored)
}

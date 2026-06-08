package capture_plugin

import (
	"context"
	"time"
)

// Invocation is the resolved request the capture answered. It feeds the
// `jcs-cutting_garden-capture-invocation-v1` node and is part of
// identity: two captures with identical invocations share an invocation
// markl-id.
type Invocation struct {
	Target    string
	Format    string
	Normalize bool
	Options   map[string]any
}

func (iv Invocation) body() map[string]any {
	opts := iv.Options
	if opts == nil {
		opts = map[string]any{}
	}
	return map[string]any{
		"target":    iv.Target,
		"format":    iv.Format,
		"normalize": iv.Normalize,
		"options":   opts,
	}
}

// PluginEnv is the plugin-defined, identity-affecting environment node
// the environment subtree REQUIRES (RFC 0002 §Environment). TypeString
// is the plugin's `jcs-<plugin>-capture-environment-v1`; Body is its
// JCS-serializable value (an ASCII-keyed map per jcsMarshal's contract).
type PluginEnv struct {
	TypeString string
	Body       any
}

// ReceiptParams is everything WriteReceipt needs to assemble one
// capture's merkle tree. The payload subtree is the caller's
// responsibility: it writes its payload node(s) through the same Writer
// and passes the resulting reference(s) in PayloadRefs.
type ReceiptParams struct {
	// Kind is the capture kind tag on the receipt type (fs, web, git).
	Kind string

	Invocation Invocation
	Host       HostInfo
	Binary     BinaryInfo
	PluginEnv  PluginEnv

	// OutcomeStripped is the optional per-format normalization residue
	// recorded in the outcome body. Nil omits the field.
	OutcomeStripped map[string]any

	// PayloadRefs link the receipt to the already-written payload
	// subtree. Typically one ref; tree-shaped payloads MAY pass several.
	PayloadRefs []Ref

	// Now, when non-nil, supplies the outcome datetime — injected by
	// tests for determinism. Defaults to time.Now when nil.
	Now func() time.Time
}

// WriteReceipt assembles and writes the protocol merkle tree in
// post-order — every child before its parent, so each reference line
// carries a real digest — and returns the root receipt's markl id.
//
// Order: invocation, host, binary, plugin-env, environment, outcome,
// identity, receipt. (Payload nodes were written by the caller before
// this call.)
func WriteReceipt(
	ctx context.Context,
	w Writer,
	p ReceiptParams,
) (receiptDigest string, err error) {
	invBody, err := jcsMarshal(p.Invocation.body())
	if err != nil {
		return "", err
	}
	invDigest, _, err := WriteNode(ctx, w, encodeNode(TypeInvocation, nil, invBody))
	if err != nil {
		return "", err
	}

	hostBody, err := jcsMarshal(p.Host.body())
	if err != nil {
		return "", err
	}
	hostDigest, _, err := WriteNode(ctx, w, encodeNode(TypeHost, nil, hostBody))
	if err != nil {
		return "", err
	}

	binBody, err := jcsMarshal(p.Binary.bodyMap())
	if err != nil {
		return "", err
	}
	binDigest, _, err := WriteNode(ctx, w, encodeNode(TypeBinary, nil, binBody))
	if err != nil {
		return "", err
	}

	pluginBody, err := jcsMarshal(p.PluginEnv.Body)
	if err != nil {
		return "", err
	}
	pluginDigest, _, err := WriteNode(ctx, w, encodeNode(p.PluginEnv.TypeString, nil, pluginBody))
	if err != nil {
		return "", err
	}

	envRefs := []Ref{
		LockedRef("host", hostDigest, TypeHost),
		LockedRef("binary", binDigest, TypeBinary),
		LockedRef("plugin", pluginDigest, p.PluginEnv.TypeString),
	}
	envDigest, _, err := WriteNode(ctx, w, encodeNode(TypeEnvironment, envRefs, nil))
	if err != nil {
		return "", err
	}

	now := p.Now
	if now == nil {
		now = time.Now
	}
	outcomeBody := map[string]any{
		"datetime": now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if p.OutcomeStripped != nil {
		outcomeBody["stripped"] = p.OutcomeStripped
	}
	outBody, err := jcsMarshal(outcomeBody)
	if err != nil {
		return "", err
	}
	outDigest, _, err := WriteNode(ctx, w, encodeNode(TypeOutcome, nil, outBody))
	if err != nil {
		return "", err
	}

	idRefs := []Ref{
		LockedRef("invocation", invDigest, TypeInvocation),
		LockedRef("environment", envDigest, TypeEnvironment),
	}
	idDigest, _, err := WriteNode(ctx, w, encodeNode(TypeIdentity, idRefs, nil))
	if err != nil {
		return "", err
	}

	receiptRefs := make([]Ref, 0, 2+len(p.PayloadRefs))
	receiptRefs = append(
		receiptRefs,
		LockedRef("identity", idDigest, TypeIdentity),
		LockedRef("outcome", outDigest, TypeOutcome),
	)
	receiptRefs = append(receiptRefs, p.PayloadRefs...)

	receiptDigest, _, err = WriteNode(ctx, w, encodeNode(ReceiptType(p.Kind), receiptRefs, nil))
	if err != nil {
		return "", err
	}

	return receiptDigest, nil
}

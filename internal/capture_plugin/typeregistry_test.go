package capture_plugin

import (
	"strings"
	"testing"
)

func TestSignatures_PresentDistinctStable(t *testing.T) {
	types := []string{
		TypeIdentity, TypeInvocation, TypeEnvironment,
		TypeHost, TypeBinary, TypeOutcome,
	}

	seen := map[string]string{}
	for _, ts := range types {
		sig, ok := SignatureFor(ts)
		if !ok {
			t.Errorf("type %q not registered", ts)
			continue
		}
		if !strings.HasPrefix(sig, "sha256-") {
			t.Errorf("type %q signature %q not a sha256 markl id", ts, sig)
		}
		if prev, dup := seen[sig]; dup {
			t.Errorf("types %q and %q share signature %q", prev, ts, sig)
		}
		seen[sig] = ts

		// Stable across calls.
		if sig2, _ := SignatureFor(ts); sig2 != sig {
			t.Errorf("type %q signature unstable: %q vs %q", ts, sig, sig2)
		}
	}
}

func TestLockedRef_AppliesAndEmitsSignature(t *testing.T) {
	r := LockedRef("identity", "sha256-id", TypeIdentity)
	sig, _ := SignatureFor(TypeIdentity)
	if r.Sig != sig || r.Sig == "" {
		t.Fatalf("LockedRef sig = %q, want %q", r.Sig, sig)
	}

	node := string(BuildNode("cutting_garden-capture-receipt-git-v1", []Ref{r}, nil))
	wantLine := "- identity < @sha256-id !cutting_garden-capture-identity-v1@" + sig
	if !strings.Contains(node, wantLine) {
		t.Errorf("node missing locked ref line %q:\n%s", wantLine, node)
	}

	// Unregistered types stay unlocked.
	u := LockedRef("x", "sha256-x", "totally-unregistered-v1")
	if u.Sig != "" {
		t.Errorf("unregistered type should be sig-less, got %q", u.Sig)
	}
}

func TestVerifyRef(t *testing.T) {
	sig, _ := SignatureFor(TypeIdentity)

	cases := []struct {
		name    string
		ref     Ref
		wantErr bool
	}{
		{"matching lock", Ref{TypeString: TypeIdentity, Sig: sig}, false},
		{"sig-less unlocked", Ref{TypeString: TypeIdentity}, false},
		{"unknown type", Ref{TypeString: "unregistered-v1", Sig: "sha256-whatever"}, false},
		{"mismatched lock", Ref{TypeString: TypeIdentity, Sig: "sha256-bogus"}, true},
	}
	for _, tc := range cases {
		err := VerifyRef(tc.ref)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: VerifyRef err = %v, wantErr = %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestParseNode_CapturesSignature(t *testing.T) {
	sig, _ := SignatureFor(TypeOutcome)
	in := BuildNode("cutting_garden-capture-receipt-git-v1",
		[]Ref{LockedRef("outcome", "sha256-out", TypeOutcome)}, nil)

	node, err := ParseNode(strings.NewReader(string(in)))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := node.RefByAlias("outcome")
	if !ok {
		t.Fatal("outcome ref not parsed")
	}
	if r.TypeString != TypeOutcome {
		t.Errorf("type-string = %q, want %q", r.TypeString, TypeOutcome)
	}
	if r.Sig != sig {
		t.Errorf("parsed sig = %q, want %q", r.Sig, sig)
	}
}

func TestMediaTypeFor(t *testing.T) {
	mt, ok := MediaTypeFor(TypeIdentity)
	if !ok || mt != "application/vnd.cutting-garden.capture-identity+hyphence" {
		t.Errorf("MediaTypeFor(identity) = (%q, %v)", mt, ok)
	}
}

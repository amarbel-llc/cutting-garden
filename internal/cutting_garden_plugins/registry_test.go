package cutting_garden_plugins

import (
	"errors"
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
)

type stubPlugin struct{ schemes []string }

func (s stubPlugin) Schemes() []string                   { return s.schemes }
func (stubPlugin) TypeTag() string                       { return "stub-tag-v1" }
func (stubPlugin) ValidateSource(*url.URL, string) error { return nil }
func (stubPlugin) CaptureRoot(CaptureRootRequest) CaptureRootResult {
	return CaptureRootResult{}
}
func (stubPlugin) ValidateDest(*url.URL, string) error    { return nil }
func (stubPlugin) Restore(RestoreRequest) error           { return nil }
func (stubPlugin) ValidateDiffDir(*url.URL, string) error { return nil }
func (stubPlugin) ScanForDiff(DiffScanRequest) ([]capture_receipt.EntryV1, error) {
	return nil, nil
}

// Use a private registry for tests so we don't pollute the package
// default (which the file plugin's init() populates).
func TestCaptureRegistry_DuplicateRegistration(t *testing.T) {
	r := newCaptureRegistry()
	p := stubPlugin{schemes: []string{"x"}}

	if err := r.register("x", p); err != nil {
		t.Fatalf("first register: %v", err)
	}

	err := r.register("x", p)
	if err == nil {
		t.Fatalf("expected duplicate-registration error, got nil")
	}
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("got %v, want wrapping ErrAlreadyRegistered", err)
	}
}

func TestCaptureRegistry_UnknownScheme(t *testing.T) {
	r := newCaptureRegistry()

	_, err := r.resolve("nonexistent")
	if err == nil {
		t.Fatalf("expected unknown-scheme error, got nil")
	}
	if !errors.Is(err, ErrUnknownScheme) {
		t.Errorf("got %v, want wrapping ErrUnknownScheme", err)
	}
}

func TestCaptureRegistry_ResolveSucceeds(t *testing.T) {
	r := newCaptureRegistry()
	p := stubPlugin{schemes: []string{"x"}}

	if err := r.register("x", p); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, err := r.resolve("x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.TypeTag() != "stub-tag-v1" {
		t.Errorf("resolved plugin has wrong TypeTag: %q", got.TypeTag())
	}
}

func TestRestoreRegistry_DuplicateRegistration(t *testing.T) {
	r := newRestoreRegistry()
	p := stubPlugin{schemes: []string{"x"}}

	if err := r.register("x", p); err != nil {
		t.Fatalf("first register: %v", err)
	}

	err := r.register("x", p)
	if err == nil {
		t.Fatalf("expected duplicate-registration error, got nil")
	}
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("got %v, want wrapping ErrAlreadyRegistered", err)
	}
}

func TestRestoreRegistry_UnknownScheme(t *testing.T) {
	r := newRestoreRegistry()

	_, err := r.resolve("nonexistent")
	if err == nil {
		t.Fatalf("expected unknown-scheme error, got nil")
	}
	if !errors.Is(err, ErrUnknownScheme) {
		t.Errorf("got %v, want wrapping ErrUnknownScheme", err)
	}
}

func TestDiffRegistry_DuplicateRegistration(t *testing.T) {
	r := newDiffRegistry()
	p := stubPlugin{schemes: []string{"x"}}

	if err := r.register("x", p); err != nil {
		t.Fatalf("first register: %v", err)
	}

	err := r.register("x", p)
	if err == nil {
		t.Fatalf("expected duplicate-registration error, got nil")
	}
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("got %v, want wrapping ErrAlreadyRegistered", err)
	}
}

func TestDiffRegistry_UnknownScheme(t *testing.T) {
	r := newDiffRegistry()

	_, err := r.resolve("nonexistent")
	if err == nil {
		t.Fatalf("expected unknown-scheme error, got nil")
	}
	if !errors.Is(err, ErrUnknownScheme) {
		t.Errorf("got %v, want wrapping ErrUnknownScheme", err)
	}
}

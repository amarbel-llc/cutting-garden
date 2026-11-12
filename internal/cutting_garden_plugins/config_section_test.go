package cutting_garden_plugins

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/tommy/pkg/cst"
)

func decomposeTOML(t *testing.T, src string) *cst.Value {
	t.Helper()
	model, err := cst.DecomposeBytes([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return model
}

// A private registry keeps these tests off the package default, which real
// plugin init()s populate.
func TestConfigSectionRegistry_RegisterAndList(t *testing.T) {
	r := newConfigSectionRegistry()
	noop := func(*cst.Value) error { return nil }
	for _, name := range []string{"zeta", "alpha"} {
		if err := r.register(name, noop); err != nil {
			t.Fatalf("register(%s): %v", name, err)
		}
	}
	if got := r.names(); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Errorf("names() = %v, want sorted [alpha zeta]", got)
	}
	if err := r.register("alpha", noop); !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("duplicate register error = %v, want wrap of ErrAlreadyRegistered", err)
	}
	if err := r.register("", noop); err == nil {
		t.Error("empty name must be rejected")
	}
	if err := r.register("nildecoder", nil); err == nil {
		t.Error("nil decoder must be rejected")
	}
}

func TestMustRegisterConfigSection_DuplicatePanics(t *testing.T) {
	noop := func(*cst.Value) error { return nil }
	MustRegisterConfigSection("cgp-dup-section-test", noop)
	if !slices.Contains(RegisteredConfigSections(), "cgp-dup-section-test") {
		t.Fatal("RegisteredConfigSections omits a just-registered name")
	}
	defer func() {
		if r := recover(); r == nil {
			t.Error("duplicate MustRegisterConfigSection did not panic")
		}
	}()
	MustRegisterConfigSection("cgp-dup-section-test", noop)
}

// decode dispatches only the registered names whose table is present,
// hands each decoder its own sub-table, and leaves unregistered tables and
// unclaimed keys for the Undecoded report.
func TestConfigSectionRegistry_DecodeDispatchesPresentSectionsOnly(t *testing.T) {
	r := newConfigSectionRegistry()
	var seen []string
	decoder := func(name string) ConfigSectionDecoder {
		return func(sub *cst.Value) error {
			seen = append(seen, name)
			v, ok := sub.Get("token_env")
			if !ok || v.Kind != cst.VLeaf {
				t.Errorf("%s: sub-table lacks token_env leaf", name)
				return nil
			}
			v.MarkConsumed()
			return nil
		}
	}
	for _, name := range []string{"present", "absent", "notatable"} {
		if err := r.register(name, decoder(name)); err != nil {
			t.Fatal(err)
		}
	}

	model := decomposeTOML(t, `
notatable = 1

[present]
token_env = "X"
extra = true

[unregistered]
key = "v"
`)
	if err := r.decode(model); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Equal(seen, []string{"present"}) {
		t.Errorf("dispatched %v, want [present] only", seen)
	}

	// The registered table is entered (seen) so only its unconsumed child
	// surfaces; the unregistered table and the non-table key stay unknown.
	got := model.Undecoded()
	want := []string{"notatable", "present.extra", "unregistered"}
	if !slices.Equal(got, want) {
		t.Errorf("Undecoded() = %v, want %v", got, want)
	}
}

// A section whose decoder fails is skipped, and its plugin then has NO
// configured accounts, even in a process that loaded a valid section earlier
// (cutting-garden#412): decoding injects, so the skip must reset what an
// earlier load injected.
func TestConfigSectionRegistry_SkippedSectionResetsInjectedState(t *testing.T) {
	r := newConfigSectionRegistry()
	injected := "unset"
	err := r.register("plug", func(sub *cst.Value) error {
		if bad, ok := sub.Get("bad"); ok {
			bad.MarkConsumed()
			return errors.New("accounts[0]: invalid")
		}
		injected = ""
		if v, ok := sub.Get("value"); ok && v.Kind == cst.VLeaf {
			if s, sok := cst.ExtractString(v.Leaf); sok {
				v.MarkConsumed()
				injected = s
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if failures := r.decodeEach(decomposeTOML(t, "[plug]\nvalue = \"first\"\n")); len(failures) != 0 {
		t.Fatalf("first load failed: %v", failures)
	}
	if injected != "first" {
		t.Fatalf("first load injected %q, want first", injected)
	}

	failures := r.decodeEach(decomposeTOML(t, "[plug]\nbad = true\n"))
	if len(failures) != 1 || failures[0].Section != "plug" {
		t.Fatalf("second load failures = %v, want [plug]", failures)
	}
	if injected != "" {
		t.Errorf("after a skipped section the plugin still holds %q, want no injected state", injected)
	}
}

func TestConfigSectionRegistry_DecodeErrorNamesSection(t *testing.T) {
	r := newConfigSectionRegistry()
	boom := errors.New("accounts[0]: empty name")
	if err := r.register("broken", func(*cst.Value) error { return boom }); err != nil {
		t.Fatal(err)
	}
	if err := r.register("fine", func(*cst.Value) error { return nil }); err != nil {
		t.Fatal(err)
	}

	err := r.decode(decomposeTOML(t, "[fine]\n[broken]\n"))
	if err == nil {
		t.Fatal("decode must surface the decoder error")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %v does not wrap the decoder's error", err)
	}
	if !strings.HasPrefix(err.Error(), "broken: ") {
		t.Errorf("error %q must be prefixed with the section name", err)
	}
}

package mutate

import (
	"context"
	"io"
	"net/url"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// mutFake is a registry-free plugin for the engine and command tests: a
// NodeMutator that records what it was asked to write, and a RootLister so
// a group's anchor and node ids resolve through it. It claims the
// "faketest" scheme and declares no patch keys, so a strict patch against it
// is refused as unverifiable.
type mutFake struct {
	created, put, patched, deleted []string
	failCreate                     bool
}

func (*mutFake) Schemes() []string                     { return []string{"faketest"} }
func (*mutFake) TypeTag() string                       { return "cutting_garden-test-v1" }
func (*mutFake) ValidateSource(*url.URL, string) error { return nil }
func (*mutFake) CaptureRoot(cgp.CaptureRootRequest) cgp.CaptureRootResult {
	return cgp.CaptureRootResult{}
}

func (f *mutFake) CreateNode(_ context.Context, u *url.URL, _ io.Reader, _ string) error {
	if f.failCreate {
		return errors.ErrorWithStackf("boom")
	}
	f.created = append(f.created, u.String())
	return nil
}

func (f *mutFake) PutNode(_ context.Context, u *url.URL, _ io.Reader) error {
	f.put = append(f.put, u.String())
	return nil
}

func (f *mutFake) PatchNode(_ context.Context, u *url.URL, _ io.Reader) ([]string, error) {
	f.patched = append(f.patched, u.String())
	return nil, nil
}

func (f *mutFake) DeleteNode(_ context.Context, u *url.URL) error {
	f.deleted = append(f.deleted, u.String())
	return nil
}

func (*mutFake) Types() []cgp.NodeType { return nil }

func (*mutFake) ListRoots(context.Context, *url.URL) ([]cgp.Node, error) {
	return nil, nil
}

// parseFake parses uri and requires the fake's scheme, the way the real
// resolvers refuse an unknown one.
func parseFake(uri string) (*url.URL, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if u.Scheme != "faketest" {
		return nil, errors.ErrorWithStackf("unknown scheme %q", u.Scheme)
	}
	return u, nil
}

// fakeEngine wires an Engine whose every resolver lands on m.
func fakeEngine(m *mutFake) *Engine {
	return &Engine{
		ResolveLister: func(uri string) (*url.URL, cgp.RootLister, error) {
			u, err := parseFake(uri)
			if err != nil {
				return nil, nil, err
			}
			return u, m, nil
		},
		ResolveNode: func(uri string) (*url.URL, cgp.NodeMutator, error) {
			u, err := parseFake(uri)
			if err != nil {
				return nil, nil, err
			}
			return u, m, nil
		},
	}
}

func equalStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s = %v, want %v", what, got, want)
			return
		}
	}
}

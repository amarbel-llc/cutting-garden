package command_components

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// namerFake is a RootProvider that also reports configured root names, under
// its own scheme so the two registered instances are distinct plugins.
type namerFake struct {
	scheme string
	names  map[string]string // root URL -> name
	fail   bool
}

func (f namerFake) Schemes() []string                   { return []string{f.scheme} }
func (namerFake) TypeTag() string                       { return "cutting_garden-test-v1" }
func (namerFake) ValidateSource(*url.URL, string) error { return nil }
func (namerFake) CaptureRoot(cutting_garden_plugins.CaptureRootRequest) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

func (namerFake) Types() []cutting_garden_plugins.NodeType { return nil }

func (namerFake) ListRoots(context.Context, *url.URL) ([]cutting_garden_plugins.Node, error) {
	return nil, nil
}

var _ cutting_garden_plugins.RootNamer = namerFake{}

func (f namerFake) Roots(context.Context) ([]*url.URL, error) {
	var out []*url.URL
	for raw := range f.names {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func (f namerFake) RootNames(context.Context) (map[string]string, error) {
	if f.fail {
		return nil, errors.ErrorWithStackf("names unavailable")
	}
	return f.names, nil
}

func init() {
	cutting_garden_plugins.MustRegisterCapture(namerFake{
		scheme: "rootnamesa",
		names: map[string]string{
			"rootnamesa://h/one/": "rn-one",
			"rootnamesa://h/two/": "rn-shared",
			"rootnamesa://h/x/":   "", // an unnamed root is not in the table
		},
	})
	cutting_garden_plugins.MustRegisterCapture(namerFake{
		scheme: "rootnamesb",
		names:  map[string]string{"rootnamesb://h/two/": "rn-shared"},
	})
	cutting_garden_plugins.MustRegisterCapture(namerFake{
		scheme: "rootnamesc", fail: true,
	})
}

// TestAggregateRootNames pins the name table bound types resolve through (RFC
// 0020 §3.4): one entry per configured name, several roots under a name two
// plugins both use, unnamed roots omitted, and a failing plugin degraded to a
// warning rather than emptying the table.
func TestAggregateRootNames(t *testing.T) {
	var warn bytes.Buffer
	names := AggregateRootNames(context.Background(), &warn)

	one := names["rn-one"]
	if len(one) != 1 || one[0].URL != "rootnamesa://h/one/" ||
		len(one[0].Schemes) != 1 || one[0].Schemes[0] != "rootnamesa" {
		t.Errorf("rn-one = %+v, want the single rootnamesa root", one)
	}

	shared := names["rn-shared"]
	if len(shared) != 2 {
		t.Fatalf("rn-shared = %+v, want one root from each of two plugins", shared)
	}
	got := []string{shared[0].URL, shared[1].URL}
	if !(got[0] == "rootnamesa://h/two/" && got[1] == "rootnamesb://h/two/") {
		t.Errorf("rn-shared roots = %v, want registry order a then b", got)
	}

	if _, present := names[""]; present {
		t.Error("an unnamed root must not appear in the table")
	}
	if !strings.Contains(warn.String(), "root names unavailable") {
		t.Errorf("a failing RootNamer must warn; got %q", warn.String())
	}
}

package list

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// listFake is a capture plugin that also implements RootLister, so the
// list command can be exercised without a live CalDAV server. It claims
// the "listtest" scheme and returns two canned container nodes.
type listFake struct{}

func (listFake) Schemes() []string                       { return []string{"listtest"} }
func (listFake) TypeTag() string                         { return "cutting_garden-test-v1" }
func (listFake) ValidateSource(*url.URL, string) error   { return nil }
func (listFake) ValidateDiffDir(*url.URL, string) error  { return nil }
func (listFake) CaptureRoot(cutting_garden_plugins.CaptureRootRequest) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

func (listFake) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: "test-container-v1", Container: true},
		{Tag: "test-object-v1", Container: false},
	}
}

func (listFake) ListRoots(
	_ context.Context,
	node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf("listtest: nil node")
	}
	mk := func(path, name string) cutting_garden_plugins.Node {
		return cutting_garden_plugins.Node{
			URI:  &url.URL{Scheme: "listtest", Host: node.Host, Path: path},
			Name: name,
			Type: "test-container-v1",
		}
	}
	return []cutting_garden_plugins.Node{
		mk("/work", "Work"),
		mk("/personal", "Personal"),
	}, nil
}

// captureOnlyFake claims a scheme but is NOT a RootLister, to exercise
// the "does not support listing" path.
type captureOnlyFake struct{}

func (captureOnlyFake) Schemes() []string                     { return []string{"noroots"} }
func (captureOnlyFake) TypeTag() string                       { return "cutting_garden-test-v1" }
func (captureOnlyFake) ValidateSource(*url.URL, string) error { return nil }
func (captureOnlyFake) CaptureRoot(cutting_garden_plugins.CaptureRootRequest) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

func init() {
	cutting_garden_plugins.MustRegisterCapture(listFake{})
	cutting_garden_plugins.MustRegisterCapture(captureOnlyFake{})
}

// driveList dispatches the list subcommand through a fresh Utility (flag
// parsing included) with output routed to out, returning the exit code.
func driveList(t *testing.T, out io.Writer, args ...string) int {
	t.Helper()
	u := command.MakeUtility("cg-test", nil)
	u.AddCmd("list", newWithOutput(out))
	return u.Run(append([]string{"cg-test", "list"}, args...))
}

func TestRun_TextTable(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "listtest://h/dav/"); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"URI", "NAME", "TYPE", "Work", "Personal", "test-container-v1", "listtest://h/work"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestRun_JSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "-format", "json", "listtest://h/dav/"); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}

	var got []nodeView
	dec := json.NewDecoder(&buf)
	for dec.More() {
		var n nodeView
		if err := dec.Decode(&n); err != nil {
			t.Fatalf("decode NDJSON: %v", err)
		}
		got = append(got, n)
	}
	if len(got) != 2 {
		t.Fatalf("got %d nodes, want 2: %+v", len(got), got)
	}
	for _, n := range got {
		if n.Type != "test-container-v1" || n.URI == "" || n.Name == "" {
			t.Errorf("node = %+v", n)
		}
	}
}

func TestRun_UnknownSchemeIsTrouble(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "bogus://x"); code != 2 {
		t.Fatalf("exit = %d, want 2 (unknown scheme)", code)
	}
}

func TestRun_SchemeNotListableIsTrouble(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "noroots://x"); code != 2 {
		t.Fatalf("exit = %d, want 2 (scheme has no RootLister)", code)
	}
	if buf.Len() != 0 {
		t.Errorf("non-listable run wrote output: %q", buf.String())
	}
}

func TestRun_MissingArgIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE)", code)
	}
}

func TestRun_TrailingArgIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "listtest://h/", "extra"); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE)", code)
	}
}

func TestRun_BadFormatIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "-format", "yaml", "listtest://h/"); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE)", code)
	}
}

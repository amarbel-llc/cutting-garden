package mutate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/command"
)

const putDoc = `{"groups":[{"anchor":"faketest://h/a/","ops":[{"kind":"put","id":"x.ics","body":"1"}]}]}`

const patchDoc = `{"groups":[{"anchor":"faketest://h/a/","ops":[{"kind":"patch","id":"x.ics","body":"{\"summary\":\"s\"}"}]}]}`

const failingCreateDoc = `{"groups":[{"anchor":"faketest://h/a/","ops":[{"kind":"create","id":"n.ics","body":"{}"}]}]}`

// runMutate drives the command through a Utility, the way the binary does,
// and returns the exit code and what the command printed to stdout.
func runMutate(t *testing.T, m *mutFake, stdin string, args ...string) (int, string) {
	t.Helper()

	var out bytes.Buffer
	u := command.MakeUtility("cg-test", nil)
	u.AddCmd("mutate", newForTest(strings.NewReader(stdin), &out, fakeEngine(m)))

	code := u.Run(append([]string{"cg-test", "mutate"}, args...))

	return code, out.String()
}

func writeDoc(t *testing.T, doc string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "doc.json")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

func appliedOf(t *testing.T, out string) []string {
	t.Helper()

	var got struct {
		Groups []struct {
			Applied []string `json:"applied"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not the result document: %v\n%s", err, out)
	}
	if len(got.Groups) != 1 {
		t.Fatalf("groups = %d, want 1:\n%s", len(got.Groups), out)
	}

	return got.Groups[0].Applied
}

// A document named on the command line applies: exit 0, the result JSON on
// stdout in the caller's own ids.
func TestRun_DocumentFromFileApplies(t *testing.T) {
	m := &mutFake{}
	code, out := runMutate(t, m, "", writeDoc(t, putDoc))

	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	equalStrings(t, "put", m.put, []string{"faketest://h/a/x.ics"})
	equalStrings(t, "applied", appliedOf(t, out), []string{"x.ics"})
}

// With no file argument, and with `-`, the document is read from stdin.
func TestRun_DocumentFromStdinApplies(t *testing.T) {
	for _, args := range [][]string{nil, {"-"}} {
		m := &mutFake{}
		code, out := runMutate(t, m, putDoc, args...)

		if code != 0 {
			t.Fatalf("args %v: exit = %d, want 0\n%s", args, code, out)
		}
		equalStrings(t, "put", m.put, []string{"faketest://h/a/x.ics"})
	}
}

// Nothing applied exits 1 — a clean refusal, not trouble — and still prints
// the per-id detail, so a script can both branch on the code and read why.
func TestRun_NothingAppliedExitsOneAndStillPrintsTheResult(t *testing.T) {
	m := &mutFake{failCreate: true}
	code, out := runMutate(t, m, failingCreateDoc)

	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, `"failed"`) || !strings.Contains(out, "n.ics") {
		t.Errorf("stdout lacks the per-id failure:\n%s", out)
	}
}

// -strict is the call-wide flag, same as the document's own strict member:
// the fake declares no patch keys, so a strict patch is refused (exit 1,
// nothing dispatched) and the same patch without it goes through.
func TestRun_StrictFlag(t *testing.T) {
	m := &mutFake{}
	code, out := runMutate(t, m, patchDoc, "-strict")
	if code != 1 {
		t.Fatalf("-strict: exit = %d, want 1\n%s", code, out)
	}
	if len(m.patched) != 0 {
		t.Errorf("-strict still dispatched: %v", m.patched)
	}

	m = &mutFake{}
	code, out = runMutate(t, m, patchDoc)
	if code != 0 {
		t.Fatalf("tolerant: exit = %d, want 0\n%s", code, out)
	}
	equalStrings(t, "patched", m.patched, []string{"faketest://h/a/x.ics"})
}

func TestRun_StrictMemberOfTheDocument(t *testing.T) {
	m := &mutFake{}
	doc := `{"strict":true,` + strings.TrimPrefix(patchDoc, "{")

	code, out := runMutate(t, m, doc)
	if code != 1 || len(m.patched) != 0 {
		t.Fatalf("exit = %d, patched = %v, want 1 and none\n%s", code, m.patched, out)
	}
}

// Bad input is EX_USAGE (64): nothing was written, and the caller can fix it.
func TestRun_BadInputExitsSixtyFour(t *testing.T) {
	for name, tc := range map[string]struct {
		stdin string
		args  []string
	}{
		"not json":             {stdin: "this is not json"},
		"empty input":          {stdin: ""},
		"unknown member":       {stdin: `{"strcit":true,"groups":[{"anchor":"faketest://h/a/","ops":[{"kind":"delete","id":"y.ics"}]}]}`},
		"no groups":            {stdin: `{"groups":[]}`},
		"group without anchor": {stdin: `{"groups":[{"ops":[{"kind":"delete","id":"y.ics"}]}]}`},
		"too many arguments":   {args: []string{"a.json", "b.json"}},
	} {
		t.Run(name, func(t *testing.T) {
			m := &mutFake{}
			code, out := runMutate(t, m, tc.stdin, tc.args...)

			if code != 64 {
				t.Errorf("exit = %d, want 64\n%s", code, out)
			}
			if out != "" {
				t.Errorf("a refused call printed a result:\n%s", out)
			}
			if len(m.put)+len(m.deleted)+len(m.created)+len(m.patched) != 0 {
				t.Errorf("a refused call wrote: %+v", m)
			}
		})
	}
}

// Trouble is 2: the command could not do what was asked, through no fault of
// the document's shape — an unreadable file, a scheme no plugin serves.
func TestRun_TroubleExitsTwo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	code, _ := runMutate(t, &mutFake{}, "", missing)
	if code != 2 {
		t.Errorf("missing file: exit = %d, want 2", code)
	}

	unknown := `{"groups":[{"anchor":"nope://h/a/","ops":[{"kind":"delete","id":"y.ics"}]}]}`
	code, _ = runMutate(t, &mutFake{}, unknown)
	if code != 2 {
		t.Errorf("unknown scheme: exit = %d, want 2", code)
	}
}

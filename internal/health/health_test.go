package health

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/command"

	// Blank-import the plugins so their init() populates the registries
	// RegisteredPlugins() enumerates. The production binaries get these
	// via cgapp's blank-imports.
	_ "github.com/amarbel-llc/cutting-garden/plugins/caldav"
	_ "github.com/amarbel-llc/cutting-garden/plugins/file"
	_ "github.com/amarbel-llc/cutting-garden/plugins/git"
	_ "github.com/amarbel-llc/cutting-garden/plugins/googlephotos"
	_ "github.com/amarbel-llc/cutting-garden/plugins/optical"
	_ "github.com/amarbel-llc/cutting-garden/plugins/ytdlp"
)

// driveHealth dispatches the health subcommand through a fresh Utility
// (flag parsing included) with output routed to out, returning the exit
// code. Mirrors failures_test.driveFailures.
func driveHealth(t *testing.T, out io.Writer, args ...string) int {
	t.Helper()
	u := command.MakeUtility("cg-test", nil)
	u.AddCmd("health", newWithOutput(out))
	return u.Run(append([]string{"cg-test", "health"}, args...))
}

func rowsByName(rows []pluginRow) map[string]pluginRow {
	m := make(map[string]pluginRow, len(rows))
	for _, r := range rows {
		m[r.Plugin] = r
	}
	return m
}

func TestProbe_CapabilitiesPerPlugin(t *testing.T) {
	rows := rowsByName(collectRows())

	for _, name := range []string{"file", "git", "ytdlp", "caldav", "optical", "gphotos"} {
		if _, ok := rows[name]; !ok {
			t.Fatalf("plugin %q not enumerated; got %v", name, keys(rows))
		}
	}

	// file: full capture/restore/diff, no protocol, and now RootLister
	// traversal — intrinsic-PWD roots (RFC 0007) declaring directory +
	// file node types.
	if r := rows["file"]; !r.Capture || r.Restore != "yes" || !r.Diff ||
		r.Protocol != "" ||
		strings.Join(r.Traversal, ",") != "cutting_garden-file-directory-v1,cutting_garden-file-object-v1" {
		t.Errorf("file row = %+v", r)
	}
	// git: capture/diff, restore via protocol, protocol kind "git".
	if r := rows["git"]; !r.Capture || r.Restore != "protocol" || !r.Diff ||
		r.Protocol != "git" {
		t.Errorf("git row = %+v", r)
	}
	// ytdlp: capture/diff, no restore, no protocol.
	if r := rows["ytdlp"]; !r.Capture || r.Restore != "no" || !r.Diff ||
		r.Protocol != "" {
		t.Errorf("ytdlp row = %+v", r)
	}
	// optical: capture only — no restore, no diff, no protocol.
	if r := rows["optical"]; !r.Capture || r.Restore != "no" || r.Diff ||
		r.Protocol != "" || len(r.Traversal) != 0 {
		t.Errorf("optical row = %+v", r)
	}
	// gphotos: capture/diff, no restore, no protocol, no traversal.
	if r := rows["gphotos"]; !r.Capture || r.Restore != "no" || !r.Diff ||
		r.Protocol != "" || len(r.Traversal) != 0 {
		t.Errorf("gphotos row = %+v", r)
	}
	// caldav: full capture/restore/diff + RootLister traversal types.
	r := rows["caldav"]
	if !r.Capture || r.Restore != "yes" || !r.Diff {
		t.Errorf("caldav caps = %+v", r)
	}
	if strings.Join(r.Traversal, ",") != "caldav-calendar-v1,caldav-object-v1" {
		t.Errorf("caldav traversal = %v, want the two declared node types", r.Traversal)
	}
}

func TestRun_TextTable(t *testing.T) {
	var buf bytes.Buffer
	if code := driveHealth(t, &buf); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}
	out := buf.String()
	// "(default)" is the file plugin's empty schemeless claim, rendered
	// readably instead of a bare leading comma.
	for _, want := range []string{"PLUGIN", "SCHEMES", "TRAVERSAL", "caldav", "ytdlp", "(default)"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestRun_JSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if code := driveHealth(t, &buf, "-format", "json"); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}

	var got []pluginRow
	dec := json.NewDecoder(&buf)
	for dec.More() {
		var r pluginRow
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode NDJSON: %v", err)
		}
		got = append(got, r)
	}
	rows := rowsByName(got)
	if r, ok := rows["caldav"]; !ok || len(r.Traversal) != 2 {
		t.Errorf("caldav json row = %+v (ok=%v)", r, ok)
	}
	if r, ok := rows["ytdlp"]; !ok || r.Restore != "no" {
		t.Errorf("ytdlp json row = %+v (ok=%v)", r, ok)
	}
}

func TestRun_BadFormatIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	if code := driveHealth(t, &buf, "-format", "yaml"); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE)", code)
	}
	if buf.Len() != 0 {
		t.Errorf("bad-format run wrote output: %q", buf.String())
	}
}

func TestRun_TrailingArgIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	if code := driveHealth(t, &buf, "extra"); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE)", code)
	}
}

func keys(m map[string]pluginRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

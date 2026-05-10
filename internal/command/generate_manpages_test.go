package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type goldenCmd struct{}

func (goldenCmd) Run(req Request) {}

func (goldenCmd) GetDescription() Description {
	return Description{
		Short: "do the thing",
		Long:  "Does the thing in a configurable, well-described way.",
	}
}

func (goldenCmd) GetEnvVars() []EnvVar {
	return []EnvVar{
		{Name: "DEMO_VERBOSE", Description: "enable verbose mode", Default: "0"},
	}
}

func TestGenerateManpages_BasicCommand(t *testing.T) {
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	u.AddCmd("thing", goldenCmd{})

	if err := u.GenerateManpages(dir); err != nil {
		t.Fatalf("GenerateManpages: %v", err)
	}

	wantPath := filepath.Join(dir, "share", "man", "man1", "demo-thing.1")
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("missing manpage at %s: %v", wantPath, err)
	}
	got := string(body)

	for _, want := range []string{
		"demo-thing",
		"do the thing",
		"DEMO_VERBOSE",
		"enable verbose mode",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manpage missing %q\n--- got ---\n%s", want, got)
		}
	}
}

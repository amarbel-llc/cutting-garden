package command

import "testing"

type manpageFixture struct{}

func (manpageFixture) Run(req Request) {}

func (manpageFixture) GetEnvVars() []EnvVar {
	return []EnvVar{{Name: "FOO", Description: "foo var"}}
}

func (manpageFixture) GetFiles() []FilePath {
	return []FilePath{{Path: "$HOME/.foo", Description: "config"}}
}

func (manpageFixture) GetSeeAlso() []string {
	return []string{"bar(1)"}
}

func (manpageFixture) GetExamples() []Example {
	return []Example{{Description: "do thing", Command: "foo bar"}}
}

func TestEnvVar_Fields(t *testing.T) {
	e := EnvVar{Name: "X", Description: "y", Default: "z"}
	if e.Name != "X" || e.Description != "y" || e.Default != "z" {
		t.Errorf("EnvVar roundtrip failed: %+v", e)
	}
}

func TestFilePath_Fields(t *testing.T) {
	f := FilePath{Path: "/etc/foo", Description: "config"}
	if f.Path == "" || f.Description == "" {
		t.Error("FilePath roundtrip failed")
	}
}

func TestExample_Fields(t *testing.T) {
	e := Example{Description: "d", Command: "c", Output: "o"}
	if e.Description != "d" || e.Command != "c" || e.Output != "o" {
		t.Errorf("Example roundtrip failed: %+v", e)
	}
}

func TestCommandWithEnvVars_Implementable(t *testing.T) {
	var c CommandWithEnvVars = manpageFixture{}
	if got := len(c.GetEnvVars()); got != 1 {
		t.Errorf("GetEnvVars len = %d, want 1", got)
	}
}

func TestCommandWithFiles_Implementable(t *testing.T) {
	var _ CommandWithFiles = manpageFixture{}
}

func TestCommandWithSeeAlso_Implementable(t *testing.T) {
	var _ CommandWithSeeAlso = manpageFixture{}
}

func TestCommandWithExamples_Implementable(t *testing.T) {
	var _ CommandWithExamples = manpageFixture{}
}

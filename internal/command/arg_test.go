package command

import (
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/values"
)

func TestArg_FieldsPreserved(t *testing.T) {
	a := Arg{
		Name:        "blob-store-id",
		Description: "store id",
		Required:    true,
		Variadic:    true,
		EnumValues:  []string{"a", "b"},
		Value:       &values.String{},
	}
	if a.Name != "blob-store-id" {
		t.Errorf("Name = %q, want blob-store-id", a.Name)
	}
	if !a.Required || !a.Variadic {
		t.Error("Required/Variadic not preserved")
	}
}

type fakeArgsCmd struct{}

func (fakeArgsCmd) Run(req Request) {}

func (fakeArgsCmd) GetArgs() []ArgGroup {
	return []ArgGroup{{
		Name: "primary",
		Args: []Arg{{Name: "id", Required: true}},
	}}
}

func TestCommandWithArgs_Implementable(t *testing.T) {
	var c CommandWithArgs = fakeArgsCmd{}
	if got := len(c.GetArgs()); got != 1 {
		t.Errorf("GetArgs() len = %d, want 1", got)
	}
}

func TestMCPAnnotations_Fields(t *testing.T) {
	m := MCPAnnotations{ReadOnly: true, Destructive: false}
	if !m.ReadOnly || m.Destructive {
		t.Errorf("MCPAnnotations field roundtrip failed: %+v", m)
	}
}

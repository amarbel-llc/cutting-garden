package command

import "testing"

type registeredCmd struct{ name string }

func (registeredCmd) Run(req Request) {}

func TestUtility_AddAndGet(t *testing.T) {
	u := MakeUtility("test", nil)
	u.AddCmd("foo", registeredCmd{name: "foo"})
	got, ok := u.GetCmd("foo")
	if !ok {
		t.Fatal("GetCmd(foo) not found")
	}
	if got.(registeredCmd).name != "foo" {
		t.Errorf("registered cmd identity not preserved")
	}
}

func TestUtility_DuplicateAddPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("duplicate AddCmd did not panic")
		}
	}()
	u := MakeUtility("test", nil)
	u.AddCmd("dup", registeredCmd{})
	u.AddCmd("dup", registeredCmd{})
}

func TestUtility_AllCmdsIterates(t *testing.T) {
	u := MakeUtility("test", nil)
	u.AddCmd("a", registeredCmd{})
	u.AddCmd("b", registeredCmd{})
	count := 0
	for range u.AllCmds() {
		count++
	}
	if count != 2 {
		t.Errorf("AllCmds count = %d, want 2", count)
	}
}

func TestUtility_GetName(t *testing.T) {
	u := MakeUtility("cutting-garden", nil)
	if got := u.GetName(); got != "cutting-garden" {
		t.Errorf("GetName = %q, want cutting-garden", got)
	}
}

func TestUtility_LenCmds(t *testing.T) {
	u := MakeUtility("test", nil)
	if got := u.LenCmds(); got != 0 {
		t.Errorf("LenCmds empty = %d, want 0", got)
	}
	u.AddCmd("a", registeredCmd{})
	if got := u.LenCmds(); got != 1 {
		t.Errorf("LenCmds after Add = %d, want 1", got)
	}
}

func TestUtility_MergeWithPrefix(t *testing.T) {
	parent := MakeUtility("parent", nil)
	child := MakeUtility("child", nil)
	child.AddCmd("op", registeredCmd{})
	parent = parent.MergeUtilityWithPrefix(child, "child")
	if _, ok := parent.GetCmd("child-op"); !ok {
		t.Error("MergeUtilityWithPrefix did not prefix the cmd name")
	}
}

package command

import "testing"

type fakeCmd struct {
	ran bool
}

func (c *fakeCmd) Run(req Request) {
	c.ran = true
}

func (fakeCmd) GetDescription() Description {
	return Description{Short: "fake command for tests"}
}

func TestCmd_RunIsCallable(t *testing.T) {
	var c fakeCmd
	c.Run(Request{})
	if !c.ran {
		t.Error("Run was not invoked")
	}
}

func TestDescription_Fields(t *testing.T) {
	d := Description{Short: "short", Long: "long"}
	if d.Short != "short" || d.Long != "long" {
		t.Errorf("Description fields not preserved: %+v", d)
	}
}

func TestCommandWithDescription_Implementable(t *testing.T) {
	var c CommandWithDescription = fakeCmd{}
	if c.GetDescription().Short != "fake command for tests" {
		t.Error("CommandWithDescription not implemented as expected")
	}
}

package health

import "code.linenisgreat.com/cutting-garden/internal/command"

var (
	_ command.CommandWithExamples = (*Health)(nil)
	_ command.CommandWithSeeAlso  = (*Health)(nil)
)

func (*Health) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "Show every registered plugin and what it supports.",
			Command:     "cutting-garden health",
		},
		{
			Description: "Machine-readable capability report, one object per plugin.",
			Command:     "cutting-garden health -format json | jq .",
		},
		{
			Description: "List each plugin's configured roots.",
			Command:     "cutting-garden health -format json | jq -r 'select(.roots) | .plugin + \" \" + (.roots | join(\" \"))'",
		},
	}
}

func (*Health) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-capture(1)",
		"cutting-garden-serve(1)",
	}
}

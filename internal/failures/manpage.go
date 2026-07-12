package failures

import "code.linenisgreat.com/cutting-garden/internal/command"

var (
	_ command.CommandWithArgs     = (*Failures)(nil)
	_ command.CommandWithExamples = (*Failures)(nil)
	_ command.CommandWithSeeAlso  = (*Failures)(nil)
)

func (*Failures) GetArgs() []command.ArgGroup {
	return []command.ArgGroup{{
		Args: []command.Arg{
			{
				Name: "failure-receipt-id",
				Description: "markl-id of the failure receipt blob to inspect " +
					"(printed by capture as `failures store=... id=...` and " +
					"journaled in captures.log as failure_receipt_id).",
				Required: true,
			},
		},
	}}
}

func (*Failures) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "Inspect the failure receipt a capture just printed.",
			Command:     "cutting-garden failures <failure-receipt-id>",
		},
		{
			Description: "List only the failed paths, machine-readably.",
			Command:     "cutting-garden failures -format json <failure-receipt-id> | jq -r .path",
		},
		{
			Description: "Read the most recent failure receipt out of captures.log.",
			Command:     "fid=$(jq -rs 'map(select(.failure_receipt_id)) | last | .failure_receipt_id' \"${XDG_STATE_HOME:-$HOME/.local/state}/cutting-garden/captures.log\") && cutting-garden failures \"$fid\"",
		},
	}
}

func (*Failures) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-capture(1)",
		"cutting-garden-restore(1)",
	}
}

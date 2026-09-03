package command_components

import (
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// ResolveTagInterpreter selects the tag interpreter a dimension uses,
// applying RFC 0019 §4 / design D6 precedence: the config override wins over
// the field's plugin-declared default (UnifiedField.Interpreter). An empty
// override falls back to fieldDefault. The resolved name MUST name a
// registered interpreter (a cutting_garden_plugins builtin — "naive" or
// "dodder-hyphen" — or a future registered name); a miss is a loud bad
// request naming the value, never a silent default (the no-fallback rule of
// LookupTagInterpreter).
//
// This lives in command_components rather than cutting_garden_plugins so the
// framework callers wiring resolved interpreters into apply/list/trellis
// (tags slice 3 Tasks A3/A4) reach it without a pkgs/ facade regen — this
// package is internal-only and already imports cutting_garden_plugins.
//
// A per-account interpreter override (design D6's optional per-account key)
// is a deferred follow-up; this helper resolves only the global override.
func ResolveTagInterpreter(
	fieldDefault, override string,
) (cutting_garden_plugins.TagInterpreter, error) {
	name := resolveTagInterpreterName(fieldDefault, override)
	interp, ok := cutting_garden_plugins.LookupTagInterpreter(name)
	if !ok {
		return nil, errors.BadRequestf(
			"tag interpreter %q is not registered "+
				"(builtins: naive, dodder-hyphen)",
			name,
		)
	}
	return interp, nil
}

// resolveTagInterpreterName is the name half of the §4 precedence, shared by
// ResolveTagInterpreter (which validates against the registry) and the
// discovery-only TypeTagSets (which reports the resolution as-is): the
// override wins; an empty override falls back to fieldDefault.
func resolveTagInterpreterName(fieldDefault, override string) string {
	if override != "" {
		return override
	}
	return fieldDefault
}

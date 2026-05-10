package command

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GenerateManpages walks the registered command set and emits a roff
// man(1) page per subcommand under <outDir>/share/man/man1/. Each
// page surfaces whatever opt-in interfaces the command implements
// (Description, Args, EnvVars, Files, Examples, SeeAlso).
func (utility Utility) GenerateManpages(outDir string) error {
	manDir := filepath.Join(outDir, "share", "man", "man1")
	if err := os.MkdirAll(manDir, 0o755); err != nil {
		return err
	}
	for name, cmd := range utility.AllCmds() {
		body, err := utility.renderManpage(name, cmd)
		if err != nil {
			return err
		}
		path := filepath.Join(manDir,
			fmt.Sprintf("%s-%s.1", utility.GetName(), name))
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (utility Utility) renderManpage(name string, cmd Cmd) (string, error) {
	var b strings.Builder
	page := fmt.Sprintf("%s-%s", utility.GetName(), name)
	fmt.Fprintf(&b, ".TH %s 1\n", strings.ToUpper(page))
	fmt.Fprintf(&b, ".SH NAME\n%s", page)

	if d, ok := cmd.(CommandWithDescription); ok {
		desc := d.GetDescription()
		if desc.Short != "" {
			fmt.Fprintf(&b, " - %s", desc.Short)
		}
		fmt.Fprintln(&b)
		if desc.Long != "" {
			fmt.Fprintf(&b, ".SH DESCRIPTION\n%s\n", desc.Long)
		}
	} else {
		fmt.Fprintln(&b)
	}

	if a, ok := cmd.(CommandWithArgs); ok {
		groups := a.GetArgs()
		if len(groups) > 0 {
			fmt.Fprintln(&b, ".SH ARGUMENTS")
			for _, g := range groups {
				for _, arg := range g.Args {
					fmt.Fprintf(&b, ".TP\n.B %s\n%s\n",
						arg.Name, arg.Description)
				}
			}
		}
	}

	if e, ok := cmd.(CommandWithEnvVars); ok {
		envs := e.GetEnvVars()
		if len(envs) > 0 {
			fmt.Fprintln(&b, ".SH ENVIRONMENT")
			for _, env := range envs {
				fmt.Fprintf(&b, ".TP\n.B %s\n%s\n",
					env.Name, env.Description)
				if env.Default != "" {
					fmt.Fprintf(&b, "Default: %s\n", env.Default)
				}
			}
		}
	}

	if f, ok := cmd.(CommandWithFiles); ok {
		files := f.GetFiles()
		if len(files) > 0 {
			fmt.Fprintln(&b, ".SH FILES")
			for _, file := range files {
				fmt.Fprintf(&b, ".TP\n.I %s\n%s\n",
					file.Path, file.Description)
			}
		}
	}

	if e, ok := cmd.(CommandWithExamples); ok {
		examples := e.GetExamples()
		if len(examples) > 0 {
			fmt.Fprintln(&b, ".SH EXAMPLES")
			for _, ex := range examples {
				fmt.Fprintf(&b, ".TP\n%s\n.B %s\n",
					ex.Description, ex.Command)
				if ex.Output != "" {
					fmt.Fprintf(&b, "Output: %s\n", ex.Output)
				}
			}
		}
	}

	if s, ok := cmd.(CommandWithSeeAlso); ok {
		others := s.GetSeeAlso()
		if len(others) > 0 {
			fmt.Fprintln(&b, ".SH SEE ALSO")
			fmt.Fprintln(&b, strings.Join(others, ", "))
		}
	}

	return b.String(), nil
}

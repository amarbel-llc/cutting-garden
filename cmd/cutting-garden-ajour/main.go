// Command cutting-garden-ajour explains a stencil-stack verdict: it reads a
// stack of layers and one call and prints pkgs/ajour's verdict, one line per
// refusal (FDR 0028 open item 4, cutting-garden#403; cutting-garden-ajour(1)).
//
// It is a separate binary rather than a `cg ajour` subcommand because every
// cg subcommand lives under internal/, which the layering guard
// (internal/sdklayering) forbids from importing pkgs/; a `cg ajour` waits on
// an SDK command registry (cutting-garden#405). It imports pkgs/ajour and
// nothing under internal/, so it is also the first out-of-tree-shaped
// consumer of the package: what it needs, an outside enforcer has.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/ajour"
)

// Exit codes, diff(1)-style as cutting-garden(1) documents them.
const (
	exitAdmitted = 0
	exitRefused  = 1
	exitUsage    = 64 // EX_USAGE: a parse or shape error in the input
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// A layerArg is one --stencil or --patch flag, kept in command-line order.
type layerArg struct {
	patch bool
	path  string
}

// stackFlag appends to the one ordered stack, so --stencil and --patch
// interleave in the order given (bottom of the stack first).
type stackFlag struct {
	stack *[]layerArg
	patch bool
}

func (f stackFlag) String() string { return "" }

func (f stackFlag) Set(path string) error {
	*f.stack = append(*f.stack, layerArg{patch: f.patch, path: path})
	return nil
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cutting-garden-ajour", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var stack []layerArg
	var callPath string
	flags.Var(stackFlag{stack: &stack}, "stencil", "a capability profile's body, as a stencil (repeatable; stack order)")
	flags.Var(stackFlag{stack: &stack, patch: true}, "patch", "an act's body, as a deny-only patch (repeatable; stack order)")
	flags.StringVar(&callPath, "call", "", "the call as JSON (ajour.Call), or - for stdin")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	usage := func(err error) int {
		fmt.Fprintf(stderr, "cutting-garden-ajour: %v\n", err)
		return exitUsage
	}
	if flags.NArg() > 0 {
		return usage(fmt.Errorf("unexpected argument %q (layers are --stencil/--patch, the call is --call)", flags.Arg(0)))
	}
	if callPath == "" {
		return usage(errors.New("--call is required"))
	}

	layers, err := readStack(stack)
	if err != nil {
		return usage(err)
	}
	call, err := readCall(callPath, stdin)
	if err != nil {
		return usage(err)
	}

	verdict := ajour.Evaluate(layers, call)
	if verdict.Admitted {
		fmt.Fprintln(stdout, "admitted")
		return exitAdmitted
	}
	fmt.Fprintln(stdout, "refused")
	if len(verdict.Refusals) == 0 {
		// Evaluate refuses without a refusal only when the stack holds no
		// stencil: no stencil, no hole.
		fmt.Fprintln(stdout, "no stencil in the stack: nothing is admitted")
	}
	fmt.Fprint(stdout, verdict.String())
	return exitRefused
}

func readStack(stack []layerArg) ([]ajour.Layer, error) {
	layers := make([]ajour.Layer, 0, len(stack))
	for _, l := range stack {
		raw, err := os.ReadFile(l.path)
		if err != nil {
			return nil, err
		}
		body := string(raw)
		if strings.HasPrefix(strings.TrimLeft(body, " \t\r\n"), "---") {
			return nil, fmt.Errorf("%s: a layer file is a body; this one opens with a hyphence head (`---`)", l.path)
		}
		var layer ajour.Layer
		if l.patch {
			layer, err = ajour.ParsePatch(body)
		} else {
			layer, err = ajour.ParseStencil(body)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", l.path, err)
		}
		layers = append(layers, layer)
	}
	return layers, nil
}

func readCall(path string, stdin io.Reader) (ajour.Call, error) {
	in := stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return ajour.Call{}, err
		}
		defer f.Close()
		in = f
	}
	dec := json.NewDecoder(in)
	dec.DisallowUnknownFields()
	var call ajour.Call
	if err := dec.Decode(&call); err != nil {
		return ajour.Call{}, fmt.Errorf("--call %s: %w", path, err)
	}
	return call, nil
}

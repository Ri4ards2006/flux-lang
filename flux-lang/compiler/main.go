// Command flux-compiler is the CLI entrypoint for the flux language.
//
// Usage:
//
//	# Tokenise (default mode).
//	flux-compiler <file>
//	echo 'ALLOC R1, 32' | flux-compiler
//
//	# Parse and dump the AST.
//	flux-compiler -ast <file>
//
//	# Compile and write the .flx bytecode to a file.
//	flux-compiler -o program.flx <file>
//
// Modes are mutually exclusive-ish: -o takes precedence over -ast,
// which takes precedence over the default tokenise mode.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"flux/compiler/codegen"
	"flux/compiler/lexer"
	"flux/compiler/parser"
)

const usage = `flux-compiler — compile flux source.

Usage:
  flux-compiler [flags] [<file>|-]

Flags:
  -ast       parse the source and print the AST instead of the token stream
  -o <file>  compile the source and write the .flx bytecode to <file>

If no <file> argument is given, flux-compiler reads from STDIN.
`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run is the testable wrapper around the CLI behaviour. Pulling the
// flag-handling and dispatch out of main() keeps main itself trivial
// and makes the binary easy to exercise from Go tests with mock I/O.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("flux-compiler", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dumpAST := fs.Bool("ast", false, "parse source and print the AST")
	outFile := fs.String("o", "", "compile and write the .flx binary to this file")
	fs.Usage = func() { fmt.Fprint(stderr, usage) }

	if err := fs.Parse(args); err != nil {
		// flag already printed the error to stderr.
		return err
	}

	src, err := readSource(fs.Args(), stdin)
	if err != nil {
		fmt.Fprint(stderr, usage)
		return err
	}

	// Mode precedence: -o > -ast > tokenise.
	if *outFile != "" {
		return runEmitBinary(string(src), *outFile, stderr)
	}
	if *dumpAST {
		return runDumpAST(string(src), stdout, stderr)
	}
	return runDumpTokens(string(src), stdout)
}

// readSource returns the bytes to operate on: the contents of args[0]
// if a path was supplied, the contents of stdin if args[0] is "-", or
// everything available on stdin when no arguments were given.
func readSource(args []string, stdin io.Reader) ([]byte, error) {
	if len(args) == 0 {
		return io.ReadAll(stdin)
	}
	arg := args[0]
	if arg == "-" {
		return io.ReadAll(stdin)
	}
	data, err := os.ReadFile(arg)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", arg, err)
	}
	return data, nil
}

// runDumpTokens prints the eager token stream produced by the lexer and
// stops at the first TOKEN_EOF.
func runDumpTokens(src string, stdout io.Writer) error {
	l := lexer.New(src)
	for {
		tok := l.NextToken()
		fmt.Fprintln(stdout, tok)
		if tok.Type == lexer.TOKEN_EOF {
			return nil
		}
	}
}

// runDumpAST runs the parser and prints the rendered AST. The partial
// AST is written to stdout even when errors occurred — surfacing the
// shape of whatever the parser did manage to build is usually more
// useful than a bare error code. Errors are reported line-by-line on
// stderr and the function returns a non-nil error so the CLI exits
// non-zero when the parse was incomplete.
func runDumpAST(src string, stdout, stderr io.Writer) error {
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()

	// Always print the partial AST first — it makes the error messages
	// much easier to interpret because the reader can see what could
	// be parsed before the parser gave up.
	fmt.Fprintln(stdout, program.Dump())

	if errs := p.Errors(); len(errs) > 0 {
		for _, msg := range errs {
			fmt.Fprintln(stderr, "parse error:", msg)
		}
		return fmt.Errorf("%d parse error(s)", len(errs))
	}

	return nil
}

// runEmitBinary runs the parser + codegen and writes the assembled .flx
// binary to outFile. Both parser and codegen errors are surfaced; the
// file is only written when both stages succeed so we never leave a
// half-written .flx on disk.
func runEmitBinary(src, outFile string, stderr io.Writer) error {
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()

	if errs := p.Errors(); len(errs) > 0 {
		for _, msg := range errs {
			fmt.Fprintln(stderr, "parse error:", msg)
		}
		return fmt.Errorf("%d parse error(s); refusing to emit", len(errs))
	}

	c := codegen.New()
	if err := c.Compile(program); err != nil {
		return fmt.Errorf("compile: %w", err)
	}
	if errs := c.Errors(); len(errs) > 0 {
		for _, msg := range errs {
			fmt.Fprintln(stderr, "codegen error:", msg)
		}
		return fmt.Errorf("%d codegen error(s); refusing to emit", len(errs))
	}

	if err := os.WriteFile(outFile, c.Binary(), 0o644); err != nil {
		return fmt.Errorf("write %q: %w", outFile, err)
	}

	fmt.Fprintf(stderr, "wrote %q (%d bytes, %d constants)\n",
		outFile, len(c.Binary()), len(c.Constants()))
	return nil
}

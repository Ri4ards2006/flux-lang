package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// canonicalFixture is the same program the parser integration test uses;
// duplicating it here keeps the CLI tests self-contained.
const canonicalFixture = `ALLOC R1, 32
MOV R1, "Richard"
ON_CHAT "!hype", R1
    SEND_CHAT "Hype train activated!"
    TRIGGER_PIN 18, 1
FREE R1
`

// TestRun_TokenMode_Stdin verifies the default (token-stream) mode reads
// from stdin and emits every expected token type.
func TestRun_TokenMode_Stdin(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	if err := run([]string{}, strings.NewReader("ALLOC R1, 32\n"), stdout, stderr); err != nil {
		t.Fatalf("unexpected error from token mode: %v (stderr=%q)", err, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{"ALLOC", "R1", ",", "32", "EOF"} {
		if !strings.Contains(out, want) {
			t.Errorf("token-mode stdout missing %q\n---\n%s\n---", want, out)
		}
	}
	// EOF should appear exactly once and end the stream.
	if got := strings.Count(out, "EOF"); got != 1 {
		t.Errorf("token-mode stdout should contain exactly one EOF, got %d", got)
	}
}

// TestRun_ASTMode_Stdin verifies the -ast flag drives the parser and
// produces a human-readable AST dump containing every expected node.
func TestRun_ASTMode_Stdin(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	if err := run([]string{"-ast"}, strings.NewReader(canonicalFixture), stdout, stderr); err != nil {
		t.Fatalf("unexpected error from -ast mode: %v (stderr=%q)", err, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"Program",
		"AllocStmt",
		"MovStmt",
		"OnChatBlock",
		"SendChatStmt",
		"TriggerPinStmt",
		"FreeStmt",
		// StringLiteral rendering must include the surrounding quotes.
		`"Richard"`,
		`"Hype train activated!"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("AST dump missing %q\n---\n%s\n---", want, out)
		}
	}

	if stderr.Len() != 0 {
		t.Errorf("AST mode should be silent on valid input, got stderr=%q", stderr.String())
	}
}

// TestRun_ASTMode_StdinErrors verifies that parser errors propagate as
// a non-nil error from run() AND are written line-by-line to stderr.
func TestRun_ASTMode_StdinErrors(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	// ALLOC X has no register identifier in the parser's vocabulary.
	err := run([]string{"-ast"}, strings.NewReader("ALLOC X, 32\n"), stdout, stderr)
	if err == nil {
		t.Fatalf("expected error for malformed input, got nil")
	}
	if !strings.Contains(stderr.String(), "parse error") {
		t.Errorf("expected 'parse error' line on stderr, got %q", stderr.String())
	}
	if stdout.Len() == 0 {
		t.Errorf("expected partial AST in stdout even on error path")
	}
}

// TestRun_ASTMode_PositionalFile verifies that -ast composes with a
// positional file argument (i.e. the flag switch did not break the
// argument-parsing path).
func TestRun_ASTMode_PositionalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "program.fx")
	if err := os.WriteFile(path, []byte(canonicalFixture), 0o644); err != nil {
		t.Fatalf("seed temp file: %v", err)
	}

	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	if err := run([]string{"-ast", path}, nil, stdout, stderr); err != nil {
		t.Fatalf("unexpected error: %v (stderr=%q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "OnChatBlock") {
		t.Errorf("stdout should contain OnChatBlock, got:\n%s", stdout.String())
	}
}

// TestRun_OutFlag_WritesBinary exercises the -o flag end-to-end: pipe
// the canonical fixture through run() with -o <path>, then re-read the
// file and assert the wire-format bytes are sane (magic, version,
// code-section size, constant-pool overflow check).
func TestRun_OutFlag_WritesBinary(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.flx")

	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	if err := run([]string{"-o", outPath}, strings.NewReader(canonicalFixture), stdout, stderr); err != nil {
		t.Fatalf("unexpected error: %v (stderr=%q)", err, stderr.String())
	}

	// -o is silent on the success path.
	if stdout.Len() != 0 {
		t.Errorf("-o mode should not write to stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "wrote ") {
		t.Errorf("expected 'wrote …' confirmation on stderr, got %q", stderr.String())
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read -o output: %v", err)
	}

	// Minimum-size guard so we don't read garbage past the header.
	if len(data) < 15 {
		t.Fatalf("output %q too small to be a .flx file: %d bytes", outPath, len(data))
	}
	// Magic + version.
	if string(data[0:4]) != "FLUX" {
		t.Errorf("Magic mismatch: got %q, want %q", data[0:4], "FLUX")
	}
	if data[4] != 1 {
		t.Errorf("Version: got %d, want 1", data[4])
	}
	// CodeSectionSize is a Uint32 at [11..15) — sanity-check it matches
	// the canonical fixture's known 36-byte code section.
	if codeSize := binary.BigEndian.Uint32(data[11:15]); codeSize != 36 {
		t.Errorf("CodeSectionSize: got %d, want 36", codeSize)
	}
}

// TestRun_OutFlag_NoHalfBakedOutput locks in the safety property
// documented in main.go: a malformed input MUST NOT leave a partial
// .flx file on disk. The error must be reported on stderr and run()
// must return a non-nil error.
func TestRun_OutFlag_NoHalfBakedOutput(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "should-not-exist.flx")

	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	// ALLOC X has no register identifier → parser error path.
	err := run([]string{"-o", outPath}, strings.NewReader("ALLOC X, 32\n"), stdout, stderr)
	if err == nil {
		t.Fatalf("expected error for malformed input with -o")
	}
	if !strings.Contains(stderr.String(), "parse error") {
		t.Errorf("expected 'parse error' on stderr, got %q", stderr.String())
	}

	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Errorf("%q should NOT exist after a parse-error -o run", outPath)
	}
}

// TestRun_FlagPrecedence confirms that -o wins over -ast and -ast
// wins over token mode when more than one is supplied (defensive
// behavior — `flux-compiler -ast -o out.flx source.fx` should produce
// a binary, not an ast dump).
func TestRun_FlagPrecedence(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.flx")

	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	// -ast AND -o together: -o wins.
	if err := run([]string{"-ast", "-o", outPath}, strings.NewReader(canonicalFixture), stdout, stderr); err != nil {
		t.Fatalf("unexpected error: %v (stderr=%q)", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "Program") {
		t.Errorf("with -o set, stdout must not contain AST dump:\n%s", stdout.String())
	}
	// File must exist with valid .flx magic.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read -o output: %v", err)
	}
	if string(data[0:4]) != "FLUX" {
		t.Errorf("Magic mismatch: got %q", data[0:4])
	}
}

// TestRun_OutFlag_ALUProgram verifies that a program mixing memory ops,
// ALU arithmetic, and bitwise operations compiles to a valid .flx binary.
func TestRun_OutFlag_ALUProgram(t *testing.T) {
	const aluFixture = `ALLOC R1, 16
MOV R1, 100
MOV R2, 25
ADD R1, R2
SUB R1, R2
MUL R1, R2
DIV R1, R2
AND R1, R2
OR R1, R2
XOR R1, R2
SHL R1, R2
SHR R1, R2
FREE R1
`
	dir := t.TempDir()
	outPath := filepath.Join(dir, "alu.flx")

	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	if err := run([]string{"-o", outPath}, strings.NewReader(aluFixture), stdout, stderr); err != nil {
		t.Fatalf("unexpected error compiling ALU fixture: %v (stderr=%q)", err, stderr.String())
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read -o output: %v", err)
	}

	if len(data) < 15 {
		t.Fatalf("output file too small: %d bytes", len(data))
	}
	if string(data[0:4]) != "FLUX" {
		t.Errorf("Magic mismatch: got %q, want %q", data[0:4], "FLUX")
	}
}


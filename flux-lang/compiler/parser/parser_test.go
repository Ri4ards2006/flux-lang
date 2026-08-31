package parser

import (
	"strings"
	"testing"

	"flux/compiler/ast"
	"flux/compiler/lexer"
)

// mockInput is the canonical flux fixture from the design doc. It is
// used by the integration test as well as by ad-hoc debugging helpers.
const mockInput = `ALLOC R1, 32
MOV R1, "Richard"
ON_CHAT "!hype", R1
    SEND_CHAT "Hype train activated!"
    TRIGGER_PIN 18, 1
FREE R1
`

// ---------------------------------------------------------------------------
// Integration: the full mock fixture
// ---------------------------------------------------------------------------

// TestParseProgram_FullFixture walks the entire canonical fixture end
// to end: every top-level statement, its operands, the ON_CHAT block's
// nesting, and the trailing FREE. A regression in any branch of
// ParseProgram or any leaf sub-parser will surface here.
func TestParseProgram_FullFixture(t *testing.T) {
	p := New(lexer.New(mockInput))
	program := p.ParseProgram()

	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parser reported errors on canonical fixture: %v", errs)
	}

	if got, want := len(program.Statements), 4; got != want {
		t.Fatalf("expected %d top-level statements, got %d (%s)",
			want, got, program.Dump())
	}

	// TokenLiteral must point at the first statement for diagnostic
	// tools downstream of the parser.
	if got, want := program.TokenLiteral(), "ALLOC"; got != want {
		t.Errorf("program.TokenLiteral() = %q, want %q", got, want)
	}

	// ---- stmt[0]: ALLOC R1, 32 -----------------------------------------
	alloc, ok := program.Statements[0].(*ast.AllocStmt)
	if !ok {
		t.Fatalf("stmt[0] should be *AllocStmt, got %T", program.Statements[0])
	}
	if alloc.TokenLiteral() != "ALLOC" {
		t.Errorf("ALLOC token literal: got %q, want %q", alloc.TokenLiteral(), "ALLOC")
	}
	if alloc.Register == nil || alloc.Register.Value != "R1" {
		t.Errorf("ALLOC register mismatch: %+v", alloc.Register)
	}
	if alloc.Size == nil || alloc.Size.Value != 32 {
		t.Errorf("ALLOC size mismatch: %+v", alloc.Size)
	}

	// ---- stmt[1]: MOV R1, "Richard" ------------------------------------
	mov, ok := program.Statements[1].(*ast.MovStmt)
	if !ok {
		t.Fatalf("stmt[1] should be *MovStmt, got %T", program.Statements[1])
	}
	if mov.Register == nil || mov.Register.Value != "R1" {
		t.Errorf("MOV register mismatch: %+v", mov.Register)
	}
	strLit, ok := mov.Value.(*ast.StringLiteral)
	if !ok {
		t.Fatalf("MOV value should be *StringLiteral, got %T", mov.Value)
	}
	if strLit.Value != "Richard" {
		t.Errorf("MOV string literal mismatch: got %q, want %q",
			strLit.Value, "Richard")
	}

	// ---- stmt[2]: ON_CHAT "!hype", R1 { SEND_CHAT; TRIGGER_PIN } -------
	oc, ok := program.Statements[2].(*ast.OnChatBlock)
	if !ok {
		t.Fatalf("stmt[2] should be *OnChatBlock, got %T", program.Statements[2])
	}
	if oc.Trigger == nil || oc.Trigger.Value != "!hype" {
		t.Errorf("ON_CHAT trigger mismatch: %+v", oc.Trigger)
	}
	if oc.UserVar == nil || oc.UserVar.Value != "R1" {
		t.Errorf("ON_CHAT userVar mismatch: %+v", oc.UserVar)
	}
	if got := len(oc.Body); got != 2 {
		t.Fatalf("ON_CHAT body length: got %d, want 2 (%s)", got, oc.Body)
	}

	sc, ok := oc.Body[0].(*ast.SendChatStmt)
	if !ok {
		t.Fatalf("ON_CHAT.Body[0] should be *SendChatStmt, got %T", oc.Body[0])
	}
	scStr, ok := sc.Value.(*ast.StringLiteral)
	if !ok {
		t.Fatalf("SEND_CHAT value should be *StringLiteral, got %T", sc.Value)
	}
	if scStr.Value != "Hype train activated!" {
		t.Errorf("SEND_CHAT message: got %q", scStr.Value)
	}

	tp, ok := oc.Body[1].(*ast.TriggerPinStmt)
	if !ok {
		t.Fatalf("ON_CHAT.Body[1] should be *TriggerPinStmt, got %T", oc.Body[1])
	}
	if tp.Pin == nil || tp.Pin.Value != 18 {
		t.Errorf("TRIGGER_PIN pin mismatch: %+v", tp.Pin)
	}
	if tp.State == nil || tp.State.Value != 1 {
		t.Errorf("TRIGGER_PIN state mismatch: %+v", tp.State)
	}

	// ---- stmt[3]: FREE R1 ----------------------------------------------
	fr, ok := program.Statements[3].(*ast.FreeStmt)
	if !ok {
		t.Fatalf("stmt[3] should be *FreeStmt, got %T", program.Statements[3])
	}
	if fr.Register == nil || fr.Register.Value != "R1" {
		t.Errorf("FREE register mismatch: %+v", fr.Register)
	}
}

// TestParseProgram_DumpContainsAllNodes is a smoke test for the
// human-readable renderer in ast.Program.Dump — it pins the format so
// downstream snapshot-style tests have something to compare against.
func TestParseProgram_DumpContainsAllNodes(t *testing.T) {
	program := New(lexer.New(mockInput)).ParseProgram()
	out := program.Dump()

	for _, want := range []string{
		"Program",
		"AllocStmt",
		"MovStmt",
		"OnChatBlock",
		"SendChatStmt",
		"TriggerPinStmt",
		"FreeStmt",
		"R1",
		"32",
		`"Richard"`, // sentinel — quotes are preserved in the dump
		"!hype",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("AST dump missing %q\n---\n%s\n---", want, out)
		}
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

// TestParseProgram_EmptyInput verifies that an empty program yields zero
// statements and zero errors — the simplest possible pass.
func TestParseProgram_EmptyInput(t *testing.T) {
	p := New(lexer.New(""))
	program := p.ParseProgram()

	if len(program.Statements) != 0 {
		t.Fatalf("empty input should yield 0 statements, got %d", len(program.Statements))
	}
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("empty input should yield 0 errors, got %v", errs)
	}
}

// TestParseProgram_AllStatementKinds verifies that every statement and
// every legitimate expression variant parses successfully when
// presented on its own. Catches regressions in any branch of the
// parseStatement dispatch table.
func TestParseProgram_AllStatementKinds(t *testing.T) {
	input := `ALLOC R5, 16
MOV R6, 123
MOV R7, R8
TRIGGER_PIN 7, 1
SEND_CHAT "hi"
SEND_CHAT R1
`
	p := New(lexer.New(input))
	program := p.ParseProgram()

	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parser errors: %v", errs)
	}
	if got := len(program.Statements); got != 6 {
		t.Fatalf("expected 6 statements, got %d", got)
	}

	wantTypes := []struct {
		name  string
		check func(ast.Statement) bool
	}{
		{"AllocStmt", func(s ast.Statement) bool { _, ok := s.(*ast.AllocStmt); return ok }},
		{"MovStmt", func(s ast.Statement) bool { _, ok := s.(*ast.MovStmt); return ok }},
		{"MovStmt", func(s ast.Statement) bool { _, ok := s.(*ast.MovStmt); return ok }},
		{"TriggerPinStmt", func(s ast.Statement) bool { _, ok := s.(*ast.TriggerPinStmt); return ok }},
		{"SendChatStmt", func(s ast.Statement) bool { _, ok := s.(*ast.SendChatStmt); return ok }},
		{"SendChatStmt", func(s ast.Statement) bool { _, ok := s.(*ast.SendChatStmt); return ok }},
	}
	for i, want := range wantTypes {
		if !want.check(program.Statements[i]) {
			t.Errorf("stmt[%d] wrong type: %T (want %s)",
				i, program.Statements[i], want.name)
		}
	}

	// Spot-check the operand types for the non-trivial cases.
	if v, ok := program.Statements[1].(*ast.MovStmt); ok {
		if x, ok := v.Value.(*ast.IntegerLiteral); !ok || x.Value != 123 {
			t.Errorf("MOV R6, 123: value should be IntegerLiteral(123), got %+v", v.Value)
		}
	}
	if v, ok := program.Statements[2].(*ast.MovStmt); ok {
		if x, ok := v.Value.(*ast.RegisterLiteral); !ok || x.Value != "R8" {
			t.Errorf("MOV R7, R8: value should be RegisterLiteral(R8), got %+v", v.Value)
		}
	}
	if v, ok := program.Statements[5].(*ast.SendChatStmt); ok {
		if x, ok := v.Value.(*ast.RegisterLiteral); !ok || x.Value != "R1" {
			t.Errorf("SEND_CHAT R1: value should be RegisterLiteral(R1), got %+v", v.Value)
		}
	}
}

// TestParseProgram_MultipleOnChatBlocks exercises the block-termination
// heuristic: a second ON_CHAT (which is a block-starter) must close the
// first block and start a new one cleanly.
func TestParseProgram_MultipleOnChatBlocks(t *testing.T) {
	input := `ON_CHAT "!first", R1
    SEND_CHAT "first body"
ON_CHAT "!second", R2
    SEND_CHAT "second body"
    TRIGGER_PIN 4, 1
FREE R1
`

	p := New(lexer.New(input))
	program := p.ParseProgram()

	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parser errors: %v", errs)
	}
	if got := len(program.Statements); got != 3 {
		t.Fatalf("expected 3 top-level statements, got %d (%s)",
			got, program.Dump())
	}

	first, ok := program.Statements[0].(*ast.OnChatBlock)
	if !ok {
		t.Fatalf("stmt[0] should be *OnChatBlock, got %T", program.Statements[0])
	}
	if first.Trigger.Value != "!first" {
		t.Errorf("first block trigger: got %q", first.Trigger.Value)
	}
	if got := len(first.Body); got != 1 {
		t.Fatalf("first block body length: got %d, want 1", got)
	}

	second, ok := program.Statements[1].(*ast.OnChatBlock)
	if !ok {
		t.Fatalf("stmt[1] should be *OnChatBlock, got %T", program.Statements[1])
	}
	if second.Trigger.Value != "!second" {
		t.Errorf("second block trigger: got %q", second.Trigger.Value)
	}
	if got := len(second.Body); got != 2 {
		t.Fatalf("second block body length: got %d, want 2", got)
	}
}

// TestParseProgram_OnChatWithEmptyBody makes sure a body is initialised
// to an empty (but non-nil) slice even when no nested statements
// appear — downstream code can iterate it without a nil guard.
func TestParseProgram_OnChatWithEmptyBody(t *testing.T) {
	input := `ON_CHAT "!silent", R1
FREE R1
`
	p := New(lexer.New(input))
	program := p.ParseProgram()

	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parser errors: %v", errs)
	}
	if got := len(program.Statements); got != 2 {
		t.Fatalf("expected 2 top-level statements, got %d", got)
	}
	oc, ok := program.Statements[0].(*ast.OnChatBlock)
	if !ok {
		t.Fatalf("stmt[0] should be *OnChatBlock, got %T", program.Statements[0])
	}
	if oc.Body == nil {
		t.Fatalf("OnChatBlock.Body must be non-nil even when empty")
	}
	if got := len(oc.Body); got != 0 {
		t.Errorf("empty ON_CHAT body: got %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Error-collection discipline
// ---------------------------------------------------------------------------

// TestParseProgram_MultipleSyntaxErrors confirms that the parser keeps
// running after the first failure instead of bailing — Error() should
// surface more than one diagnostic so users don't have to fix-and-retry
// one defect at a time.
func TestParseProgram_MultipleSyntaxErrors(t *testing.T) {
	// ALLOC is missing its operand register; MOV has an unknown
	// identifier as the destination; the rest is valid.
	input := `ALLOC X, 32
MOV foo, 1
FREE R1
`
	p := New(lexer.New(input))
	_ = p.ParseProgram()

	errs := p.Errors()
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 errors, got %d: %v", len(errs), errs)
	}
}

// TestParseProgram_SendChatIntRejected verifies the SEND_CHAT integer
// rejection rule: sending an integer literal as chat output is a
// semantic error and must show up in p.Errors().
func TestParseProgram_SendChatIntRejected(t *testing.T) {
	p := New(lexer.New("SEND_CHAT 42"))
	_ = p.ParseProgram()

	errs := p.Errors()
	if len(errs) == 0 {
		t.Fatalf("expected SEND_CHAT 42 to produce an error, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "SEND_CHAT") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a SEND_CHAT-related error, got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Phase 1 (ALU, Bitwise & Control Flow) Parser Tests
// ---------------------------------------------------------------------------

// TestParseProgram_ALUStatements verifies parsing of all 9 ALU instructions.
func TestParseProgram_ALUStatements(t *testing.T) {
	input := `ADD R1, R2
SUB R3, R4
MUL R5, R6
DIV R7, R8
AND R9, R10
OR R11, R12
XOR R13, R14
SHL R15, R16
SHR R1, R2
`
	p := New(lexer.New(input))
	prog := p.ParseProgram()

	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parser reported errors: %v", errs)
	}
	if len(prog.Statements) != 9 {
		t.Fatalf("expected 9 statements, got %d (%s)", len(prog.Statements), prog.Dump())
	}

	expected := []struct {
		op  lexer.TokenType
		dst string
		src string
	}{
		{lexer.TOKEN_ADD, "R1", "R2"},
		{lexer.TOKEN_SUB, "R3", "R4"},
		{lexer.TOKEN_MUL, "R5", "R6"},
		{lexer.TOKEN_DIV, "R7", "R8"},
		{lexer.TOKEN_AND, "R9", "R10"},
		{lexer.TOKEN_OR, "R11", "R12"},
		{lexer.TOKEN_XOR, "R13", "R14"},
		{lexer.TOKEN_SHL, "R15", "R16"},
		{lexer.TOKEN_SHR, "R1", "R2"},
	}

	for i, want := range expected {
		alu, ok := prog.Statements[i].(*ast.ALUStmt)
		if !ok {
			t.Fatalf("stmt[%d] is not *ast.ALUStmt: %T", i, prog.Statements[i])
		}
		if alu.Op != want.op {
			t.Errorf("stmt[%d] op = %s, want %s", i, alu.Op, want.op)
		}
		if alu.DstReg == nil || alu.DstReg.Value != want.dst {
			t.Errorf("stmt[%d] dst = %v, want %s", i, alu.DstReg, want.dst)
		}
		if alu.SrcReg == nil || alu.SrcReg.Value != want.src {
			t.Errorf("stmt[%d] src = %v, want %s", i, alu.SrcReg, want.src)
		}
	}
}

// TestParseProgram_ControlFlowStatements verifies parsing of CMP, JMP, JZ, JNZ, and labels.
func TestParseProgram_ControlFlowStatements(t *testing.T) {
	input := `start:
  CMP R1, R2
  JZ is_equal
  JNZ not_equal
  JMP start
is_equal:
  MOV R3, 1
not_equal:
  MOV R3, 2
`
	p := New(lexer.New(input))
	prog := p.ParseProgram()

	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parser reported errors: %v", errs)
	}
	if len(prog.Statements) != 8 {
		t.Fatalf("expected 8 statements, got %d (%s)", len(prog.Statements), prog.Dump())
	}

	// stmt 0: start:
	lbl1, ok := prog.Statements[0].(*ast.LabelStmt)
	if !ok || lbl1.Name != "start" {
		t.Errorf("stmt[0] should be LabelStmt 'start', got %T (%+v)", prog.Statements[0], lbl1)
	}

	// stmt 1: CMP R1, R2
	cmp, ok := prog.Statements[1].(*ast.CmpStmt)
	if !ok || cmp.Reg1.Value != "R1" || cmp.Reg2.Value != "R2" {
		t.Errorf("stmt[1] should be CmpStmt R1, R2, got %T (%+v)", prog.Statements[1], cmp)
	}

	// stmt 2: JZ is_equal
	jz, ok := prog.Statements[2].(*ast.JumpStmt)
	if !ok || jz.Op != lexer.TOKEN_JZ || jz.Label != "is_equal" {
		t.Errorf("stmt[2] should be JZ 'is_equal', got %T (%+v)", prog.Statements[2], jz)
	}

	// stmt 3: JNZ not_equal
	jnz, ok := prog.Statements[3].(*ast.JumpStmt)
	if !ok || jnz.Op != lexer.TOKEN_JNZ || jnz.Label != "not_equal" {
		t.Errorf("stmt[3] should be JNZ 'not_equal', got %T (%+v)", prog.Statements[3], jnz)
	}

	// stmt 4: JMP start
	jmp, ok := prog.Statements[4].(*ast.JumpStmt)
	if !ok || jmp.Op != lexer.TOKEN_JMP || jmp.Label != "start" {
		t.Errorf("stmt[4] should be JMP 'start', got %T (%+v)", prog.Statements[4], jmp)
	}
}

// TestParseProgram_LoopWithLabels tests parsing of loops within ON_CHAT blocks.
func TestParseProgram_LoopWithLabels(t *testing.T) {
	input := `ON_CHAT "!loop", R1
  MOV R2, 10
  loop_head:
    SUB R2, R3
    CMP R2, R4
    JNZ loop_head
FREE R1
`
	p := New(lexer.New(input))
	prog := p.ParseProgram()

	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parser reported errors: %v", errs)
	}
	if len(prog.Statements) != 2 {
		t.Fatalf("expected 2 top-level statements, got %d", len(prog.Statements))
	}
	oc, ok := prog.Statements[0].(*ast.OnChatBlock)
	if !ok {
		t.Fatalf("stmt[0] should be *ast.OnChatBlock, got %T", prog.Statements[0])
	}
	if len(oc.Body) != 5 {
		t.Fatalf("ON_CHAT body should have 5 statements, got %d", len(oc.Body))
	}
}

// TestParseProgram_SubroutineStatements tests parsing of CALL and RET statements.
func TestParseProgram_SubroutineStatements(t *testing.T) {
	input := `main:
  CALL my_subroutine
  JMP exit
my_subroutine:
  ADD R1, R2
  RET
exit:
`
	p := New(lexer.New(input))
	prog := p.ParseProgram()

	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parser reported errors: %v", errs)
	}
	if len(prog.Statements) != 6 {
		t.Fatalf("expected 6 statements, got %d (%s)", len(prog.Statements), prog.Dump())
	}

	callStmt, ok := prog.Statements[1].(*ast.CallStmt)
	if !ok || callStmt.Label != "my_subroutine" {
		t.Errorf("stmt[1] should be CallStmt 'my_subroutine', got %T (%+v)", prog.Statements[1], callStmt)
	}

	retStmt, ok := prog.Statements[4].(*ast.RetStmt)
	if !ok {
		t.Errorf("stmt[4] should be RetStmt, got %T (%+v)", prog.Statements[4], retStmt)
	}
}

// TestParseProgram_ControlFlowSyntaxErrors tests diagnostic generation on malformed statements.
func TestParseProgram_ControlFlowSyntaxErrors(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"CMP R1"},
		{"CMP 10, R2"},
		{"CMP R1, 10"},
		{"JMP"},
		{"JZ 42"},
		{"JNZ R1"},
		{"CALL"},
		{"CALL 123"},
		{"unknown_ident"},
	}

	for _, tt := range tests {
		p := New(lexer.New(tt.input))
		_ = p.ParseProgram()
		if len(p.Errors()) == 0 {
			t.Errorf("input %q should produce parser errors, got none", tt.input)
		}
	}
}



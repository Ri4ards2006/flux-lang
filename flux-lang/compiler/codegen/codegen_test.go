package codegen

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"testing"

	"flux/compiler/ast"
	"flux/compiler/lexer"
	"flux/compiler/parser"
)

// canonicalFixture matches the flux program used in earlier test phases
// (full assembly: ALLOC + MOV + ON_CHAT with two body statements + FREE).
const canonicalFixture = `ALLOC R1, 32
MOV R1, "Richard"
ON_CHAT "!hype", R1
    SEND_CHAT "Hype train activated!"
    TRIGGER_PIN 18, 1
FREE R1
`

// parseCanonical is a tiny test helper that runs the lexer + parser on
// the canonical fixture and returns the resulting *ast.Program. It
// fails the test if any parser error is reported, since the fixtures
// below assume the input is well-formed.
func parseCanonical(t *testing.T) *ast.Program {
	t.Helper()
	p := parser.New(lexer.New(canonicalFixture))
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parser reported errors on canonical fixture: %v", errs)
	}
	return prog
}

// parseSrc is a more general helper: parse arbitrary source text and
// forbid any parser errors. Used by the dedup / zero-body tests.
func parseSrc(t *testing.T, src string) *ast.Program {
	t.Helper()
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parser errors on %q: %v\n%s", src, errs, prog.Dump())
	}
	return prog
}

// ---------------------------------------------------------------------------
// Direct emitter tests (without the .flx header / constant pool wrap)
// ---------------------------------------------------------------------------

// TestEmit_CanonicalNoErrors is the smoke test: parsing + compiling
// the canonical fixture must produce zero compiler errors and a
// non-trivial code buffer.
func TestEmit_CanonicalNoErrors(t *testing.T) {
	c := New()
	if err := c.Compile(parseCanonical(t)); err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}
	if errs := c.Errors(); len(errs) > 0 {
		t.Fatalf("Compiler reported errors: %v", errs)
	}
	if got := len(c.Code()); got <= 14 {
		t.Errorf("expected non-trivial code section (>14 bytes), got %d", got)
	}
}

// TestEmit_StringDeduplication ensures the constant pool collapses
// repeated literals instead of allocating a fresh index per use.
func TestEmit_StringDeduplication(t *testing.T) {
	prog := parseSrc(t, `MOV R1, "hello"
MOV R2, "hello"
MOV R3, "world"
`)

	c := New()
	if err := c.Compile(prog); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	constants := c.Constants()
	if len(constants) != 2 {
		t.Fatalf("expected 2 unique constants after dedup, got %d (%v)",
			len(constants), constants)
	}
	if constants[0] != "hello" || constants[1] != "world" {
		t.Errorf("constant order mismatch: got %v", constants)
	}
}

// TestEmit_OnChatZeroBody verifies the body-length patch correctly
// handles an ON_CHAT block with no nested statements (the backpatch
// must produce a zero, not panic or leave a stale value).
func TestEmit_OnChatZeroBody(t *testing.T) {
	prog := parseSrc(t, `ON_CHAT "!silent", R1
FREE R1
`)

	c := New()
	if err := c.Compile(prog); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	code := c.Code()
	// BodyLength lives at offset [10..14) of the ON_CHAT header.
	bodyLen := binary.BigEndian.Uint32(code[10:14])
	if bodyLen != 0 {
		t.Errorf("zero-body ON_CHAT: bodyLength=%d, want 0", bodyLen)
	}
	if len(code) != 14+2 {
		t.Errorf("code section expected 16 bytes, got %d (hex: %x)", len(code), code)
	}
}

// ---------------------------------------------------------------------------
// Full .flx wire-format test
// ---------------------------------------------------------------------------

// TestEmit_FullFlxBinary_Layout is a byte-level contract test for the
// .flx file format. Every offset, length prefix, magic byte, and
// embedded constant index is asserted individually — a regression in
// any emit function will surface here as a precise failure.
//
// Layout of the canonical fixture:
//   Header        : 15 bytes
//     [Magic:4] [Version:1]
//     [ConstantsCount:Uint16 = 3]
//     [CodeSectionOffset:Uint32 = 60]
//     [CodeSectionSize:Uint32   = 36]
//   Constant pool : 11 + 9 + 25 = 45 bytes
//     [0] "Richard" (Len=7)
//     [1] "!hype" (Len=5)
//     [2] "Hype train activated!" (Len=21)
//   Code section  : 36 bytes
//     OP_ALLOC(6) + OP_MOV_REG_STR(6) + OP_ON_CHAT(14)
//     + OP_SEND_CHAT(5) + OP_TRIGGER_PIN(3) + OP_FREE(2)
func TestEmit_FullFlxBinary_Layout(t *testing.T) {
	c := New()
	if err := c.Compile(parseCanonical(t)); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	bin := c.Binary()

	// Assert constants up front so the offsets below stay readable.
	//
	// "Hype train activated!" is exactly 21 chars
	// (H-y-p-e-sp-t-r-a-i-n-sp-a-c-t-i-v-a-t-e-d-!). The constant pool
	// entry size is len-prefix(4) + string-bytes, so each line of the
	// const block below is independently auditable.
	const (
		wantHeader         = FlxHeaderSize            // 15
		wantLenRichard     = 7
		wantLenHypeTrigger = 5
		wantLenChatMessage = 21                       // "Hype train activated!" is 21 chars
		wantConstPoolBytes = (4 + wantLenRichard) + (4 + wantLenHypeTrigger) + (4 + wantLenChatMessage)
		wantCodeBytes      = 36                       // ALLOC + MOV + ON_CHAT + SEND_CHAT + TRIGGER_PIN + FREE
		wantCodeOffset     = wantHeader + wantConstPoolBytes
		wantFileSize       = wantCodeOffset + wantCodeBytes
	)

	if len(bin) != wantFileSize {
		t.Fatalf("file size: got %d, want %d\nhex: %x", len(bin), wantFileSize, bin)
	}

	// ---------- Header ----------
	if !bytes.Equal(bin[0:4], []byte(FlxMagic)) {
		t.Errorf("Magic mismatch: got %q, want %q", bin[0:4], FlxMagic)
	}
	if bin[4] != FlxVersion {
		t.Errorf("Version: got %d, want %d", bin[4], FlxVersion)
	}
	if got := binary.BigEndian.Uint16(bin[5:7]); got != 3 {
		t.Errorf("ConstantsCount: got %d, want 3", got)
	}
	if got := binary.BigEndian.Uint32(bin[7:11]); got != wantCodeOffset {
		t.Errorf("CodeSectionOffset: got %d, want %d", got, wantCodeOffset)
	}
	if got := binary.BigEndian.Uint32(bin[11:15]); got != wantCodeBytes {
		t.Errorf("CodeSectionSize: got %d, want %d", got, wantCodeBytes)
	}

	// ---------- Constant pool ----------
	expectString := func(offset, length int, want string) {
		t.Helper()
		if got := binary.BigEndian.Uint32(bin[offset : offset+4]); got != uint32(length) {
			t.Errorf("pool entry @%d: length prefix got %d, want %d", offset, got, length)
		}
		if got := string(bin[offset+4 : offset+4+length]); got != want {
			t.Errorf("pool entry @%d: got %q, want %q", offset, got, want)
		}
	}
	expectString(wantHeader, wantLenRichard, "Richard")
	expectString(wantHeader+11, wantLenHypeTrigger, "!hype")
	expectString(wantHeader+11+9, wantLenChatMessage, "Hype train activated!")

	// ---------- Code section (byte-for-byte) ----------
	wantCode := []byte{
		// ALLOC R1, 32
		OP_ALLOC, 0x01, 0x00, 0x00, 0x00, 0x20,

		// MOV R1, "Richard"  — references pool[0]
		OP_MOV_REG_STR, 0x01, 0x00, 0x00, 0x00, 0x00,

		// ON_CHAT "!hype", R1 — references pool[1]; body starts at
		// offset 26 (within the code section), body_length = 8 bytes.
		OP_ON_CHAT,
		0x00, 0x00, 0x00, 0x01, // pool[1]
		0x01,                   // R1
		0x00, 0x00, 0x00, 0x1A, // body_start = 26
		0x00, 0x00, 0x00, 0x08, // body_length = 8

		// ---- body begins here (offset 26) ----
		// SEND_CHAT "Hype train activated!" — constant-tagged index 2.
		OP_SEND_CHAT,
		0x80, 0x00, 0x00, 0x02,

		// TRIGGER_PIN 18, 1
		OP_TRIGGER_PIN,
		0x12, 0x01,
		// ---- body ends here (offset 34) ----

		// FREE R1
		OP_FREE, 0x01,
	}

	got := bin[wantCodeOffset : wantCodeOffset+wantCodeBytes]
	if !bytes.Equal(got, wantCode) {
		t.Fatalf("code section mismatch:\n got:  %x\n want: %x", got, wantCode)
	}
}

// TestEmit_OperandTagging sanity-checks the SEND_CHAT op's tagged
// operand encoding — the high bit MUST be set for constants and clear
// for registers.
func TestEmit_OperandTagging(t *testing.T) {
	regOperand, err := encodeOperandAsRegister("R3")
	if err != nil {
		t.Fatalf("encodeOperandAsRegister: %v", err)
	}
	if regOperand&operandTagMask != 0 {
		t.Errorf("register operand has constant tag set: %x", regOperand)
	}
	if regOperand != 3 {
		t.Errorf("register operand should encode 3 (R3), got %d", regOperand)
	}

	constOp := encodeOperandAsConstant(42)
	if constOp&operandTagMask == 0 {
		t.Errorf("constant operand missing tag bit: %x", constOp)
	}
	if constOp&^operandTagMask != 42 {
		t.Errorf("constant operand should preserve index 42, got %d", constOp&^operandTagMask)
	}
}

// TestEmit_RegisterEncodingRoundTrip pins the textual register name ↔
// byte code mapping used by every emit path.
func TestEmit_RegisterEncodingRoundTrip(t *testing.T) {
	for n := 1; n <= 16; n++ {
		name := "R" + strconv.Itoa(n)
		code, err := encodeRegister(name)
		if err != nil {
			t.Errorf("encodeRegister(%q) returned error: %v", name, err)
			continue
		}
		if int(code) != n {
			t.Errorf("encodeRegister(%q) = %d, want %d", name, code, n)
		}
		back, err := decodeRegister(code)
		if err != nil {
			t.Errorf("decodeRegister(%d) returned error: %v", code, err)
			continue
		}
		if back != name {
			t.Errorf("decodeRegister(%d) = %q, want %q", code, back, name)
		}
	}
	if _, err := encodeRegister("R0"); err == nil {
		t.Errorf("encodeRegister(R0) should have failed")
	}
	if _, err := encodeRegister("R17"); err == nil {
		t.Errorf("encodeRegister(R17) should have failed")
	}
}

// TestEmit_ALUInstructions verifies that all 9 ALU instructions emit
// the exact 3-byte bytecode [Opcode][DstReg][SrcReg].
func TestEmit_ALUInstructions(t *testing.T) {
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
	prog := parseSrc(t, input)
	c := New()
	if err := c.Compile(prog); err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if errs := c.Errors(); len(errs) > 0 {
		t.Fatalf("codegen reported errors: %v", errs)
	}

	code := c.Code()
	wantCode := []byte{
		OP_ADD, 1, 2,
		OP_SUB, 3, 4,
		OP_MUL, 5, 6,
		OP_DIV, 7, 8,
		OP_AND, 9, 10,
		OP_OR, 11, 12,
		OP_XOR, 13, 14,
		OP_SHL, 15, 16,
		OP_SHR, 1, 2,
	}

	if !bytes.Equal(code, wantCode) {
		t.Fatalf("ALU bytecode mismatch:\n got:  %x\n want: %x", code, wantCode)
	}
}


package cpu

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"flux/vm/memory"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// encodeFLX serialises a header + constant pool + code section into
// a single .flx blob. Mirrors the wire format the Phase-3 compiler
// emits: magic, version, constants count, code offset, code size,
// then length-prefixed strings, then code bytes.
func encodeFLX(t *testing.T, constants []string, code []byte) []byte {
	t.Helper()

	var pool bytes.Buffer
	for _, s := range constants {
		binary.Write(&pool, binary.BigEndian, uint32(len(s)))
		pool.WriteString(s)
	}

	var buf bytes.Buffer
	buf.WriteString(FlxMagic)
	buf.WriteByte(FlxVersion)
	binary.Write(&buf, binary.BigEndian, uint16(len(constants)))
	codeOffset := uint32(FlxHeaderSize + pool.Len())
	binary.Write(&buf, binary.BigEndian, codeOffset)
	binary.Write(&buf, binary.BigEndian, uint32(len(code)))
	buf.Write(pool.Bytes())
	buf.Write(code)
	return buf.Bytes()
}

// buildCanonicalProgram returns the minimal flux program whose body
// is dispatchable end-to-end through DeliverChatMessage. Layout:
//
//	0..6    ALLOC R1, 32
//	6..12   MOV R1, "Richard"        (constant idx 0)
//	12..26  ON_CHAT "!hype", R1      (constant idx 1, body_offset=26, body_length=8)
//	26..31  ; body:  SEND_CHAT "Hype train activated!"   (constant idx 2, high-bit-tagged)
//	31..34  ;         TRIGGER_PIN 18, 1
//	34..36  FREE R1
func buildCanonicalProgram(t *testing.T) (code []byte, constants []string) {
	t.Helper()
	constants = []string{"Richard", "!hype", "Hype train activated!"}
	code = []byte{
		OP_ALLOC, 0x01, 0x00, 0x00, 0x00, 0x20,
		OP_MOV_REG_STR, 0x01, 0x00, 0x00, 0x00, 0x00,
		OP_ON_CHAT,
		0x00, 0x00, 0x00, 0x01,
		0x01,
		0x00, 0x00, 0x00, 0x1A,
		0x00, 0x00, 0x00, 0x08,
		OP_SEND_CHAT, 0x80, 0x00, 0x00, 0x02,
		OP_TRIGGER_PIN, 0x12, 0x01,
		OP_FREE, 0x01,
	}
	return code, constants
}

// ---------------------------------------------------------------------------
// Phase 4 tests (preserved)
// ---------------------------------------------------------------------------

// TestLoadBinary_HappyPath exercises the loader with a minimal
// header, an empty constant pool, and a single-byte code section
// (the OP_NOP padding byte ensures Run() exits cleanly).
func TestLoadBinary_HappyPath(t *testing.T) {
	memory.Reset()

	var buf bytes.Buffer
	buf.WriteString(FlxMagic)
	buf.WriteByte(FlxVersion)
	binary.Write(&buf, binary.BigEndian, uint16(0))
	binary.Write(&buf, binary.BigEndian, uint32(FlxHeaderSize))
	binary.Write(&buf, binary.BigEndian, uint32(1))
	buf.WriteByte(0x00)

	c := New()
	if err := c.LoadBinary(buf.Bytes()); err != nil {
		t.Fatalf("LoadBinary: unexpected error: %v", err)
	}
	if len(c.Constants) != 0 {
		t.Errorf("Constants: got %d, want 0", len(c.Constants))
	}
	if len(c.Bytecode) != 1 {
		t.Errorf("Bytecode length: got %d, want 1", len(c.Bytecode))
	}
}

// TestRun_AllocFreeCycle drives the dispatch loop through an
// ALLOC + FREE sequence without panicking.
func TestRun_AllocFreeCycle(t *testing.T) {
	memory.Reset()

	code := []byte{
		OP_ALLOC, 0x01, 0x00, 0x00, 0x00, 0x20, // ALLOC R1, 32
		OP_FREE, 0x01,                          // FREE R1
	}
	c := New()
	if err := c.LoadBinary(encodeFLX(t, nil, code)); err != nil {
		t.Fatalf("LoadBinary: %v", err)
	}
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.Registers[0] == 0 {
		t.Errorf("R1: got 0, want the Alloc-returned address")
	}
	if memory.IsAllocated(c.Registers[0]) {
		t.Errorf("R1 block at %d should be free after FREE", c.Registers[0])
	}
}

// ---------------------------------------------------------------------------
// Phase 5 tests
// ---------------------------------------------------------------------------

// TestDeliverChatMessage_MatchesAndRunsBlock drives the canonical
// program, then delivers a chat message whose pattern matches the
// ON_CHAT subscription, and asserts that the body executed.
func TestDeliverChatMessage_MatchesAndRunsBlock(t *testing.T) {
	memory.Reset()

	code, constants := buildCanonicalProgram(t)
	c := New()
	if err := c.LoadBinary(encodeFLX(t, constants, code)); err != nil {
		t.Fatalf("LoadBinary: %v", err)
	}
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(c.ActiveSubscriptions) != 1 {
		t.Fatalf("subscriptions post-Run: got %d, want 1", len(c.ActiveSubscriptions))
	}
	if c.ActiveSubscriptions[0].Pattern != "!hype" {
		t.Errorf("subscription pattern: got %q, want %q",
			c.ActiveSubscriptions[0].Pattern, "!hype")
	}
	if c.ActiveSubscriptions[0].BodyOffset != 26 {
		t.Errorf("subscription body_offset: got %d, want 26",
			c.ActiveSubscriptions[0].BodyOffset)
	}
	if c.ActiveSubscriptions[0].BodyLength != 8 {
		t.Errorf("subscription body_length: got %d, want 8",
			c.ActiveSubscriptions[0].BodyLength)
	}

	logCountBefore := len(c.Logs)
	c.DeliverChatMessage("Richard", "!hype")

	var sawSend, sawTrigger, sawEntry, sawExit bool
	for _, line := range c.Logs[logCountBefore:] {
		switch {
		case strings.Contains(line, "Hype train activated!"):
			sawSend = true
		case strings.Contains(line, "TRIGGER_PIN pin=18 state=1"):
			sawTrigger = true
		case strings.Contains(line, ">>>"):
			sawEntry = true
		case strings.Contains(line, "<<<"):
			sawExit = true
		}
	}
	if !sawSend {
		t.Errorf("body SEND_CHAT didn't fire:\n%v", c.Logs[logCountBefore:])
	}
	if !sawTrigger {
		t.Errorf("body TRIGGER_PIN didn't fire:\n%v", c.Logs[logCountBefore:])
	}
	if !sawEntry {
		t.Errorf("missing >>> block-start marker:\n%v", c.Logs[logCountBefore:])
	}
	if !sawExit {
		t.Errorf("missing <<< block-end marker:\n%v", c.Logs[logCountBefore:])
	}
}

// TestDeliverChatMessage_FreesUsernameHeapBlock asserts that the
// username heap block allocated inside DeliverChatMessage is
// returned to the heap once the body finishes — R1 still contains
// the address, but IsAllocated must be false.
func TestDeliverChatMessage_FreesUsernameHeapBlock(t *testing.T) {
	memory.Reset()

	code, constants := buildCanonicalProgram(t)
	c := New()
	if err := c.LoadBinary(encodeFLX(t, constants, code)); err != nil {
		t.Fatalf("LoadBinary: %v", err)
	}
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	c.DeliverChatMessage("Richard", "!hype")

	r1 := c.Registers[0]
	if r1 == 0 {
		t.Fatalf("R1 (user_var) wasn't set to a heap address")
	}
	if memory.IsAllocated(r1) {
		t.Errorf("username heap block at R1=%d should be freed after dispatch", r1)
	}
}

// TestDeliverChatMessage_NoMatchDoesNothing ensures that messages
// which no subscription matches pass through silently — no body
// fired, no logs added, no heap activity.
func TestDeliverChatMessage_NoMatchDoesNothing(t *testing.T) {
	memory.Reset()

	code, constants := buildCanonicalProgram(t)
	c := New()
	if err := c.LoadBinary(encodeFLX(t, constants, code)); err != nil {
		t.Fatalf("LoadBinary: %v", err)
	}
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	logsBefore := append([]string(nil), c.Logs...)
	c.DeliverChatMessage("Alice", "hello") // "hello" != "!hype"

	// No new log entries.
	if len(c.Logs) != len(logsBefore) {
		t.Errorf("non-matching deliver changed log count: got %d, want %d\n%v",
			len(c.Logs), len(logsBefore), c.Logs[len(logsBefore):])
	}
}

// ---------------------------------------------------------------------------
// Phase 1 (ALU & Bitwise) tests
// ---------------------------------------------------------------------------

func TestRun_ALUArithmetic(t *testing.T) {
	memory.Reset()

	code := []byte{
		// R1 = 10, R2 = 25; ADD R1, R2 -> R1 = 35
		OP_MOV_REG_INT, 0x01, 0x00, 0x00, 0x00, 0x0A,
		OP_MOV_REG_INT, 0x02, 0x00, 0x00, 0x00, 0x19,
		OP_ADD, 0x01, 0x02,

		// R3 = 50, R4 = 20; SUB R3, R4 -> R3 = 30
		OP_MOV_REG_INT, 0x03, 0x00, 0x00, 0x00, 0x32,
		OP_MOV_REG_INT, 0x04, 0x00, 0x00, 0x00, 0x14,
		OP_SUB, 0x03, 0x04,

		// R5 = 6, R6 = 7; MUL R5, R6 -> R5 = 42
		OP_MOV_REG_INT, 0x05, 0x00, 0x00, 0x00, 0x06,
		OP_MOV_REG_INT, 0x06, 0x00, 0x00, 0x00, 0x07,
		OP_MUL, 0x05, 0x06,

		// R7 = 100, R8 = 4; DIV R7, R8 -> R7 = 25
		OP_MOV_REG_INT, 0x07, 0x00, 0x00, 0x00, 0x64,
		OP_MOV_REG_INT, 0x08, 0x00, 0x00, 0x00, 0x04,
		OP_DIV, 0x07, 0x08,

		// R9 = 0xFFFFFFFF, R10 = 1; ADD R9, R10 -> R9 = 0 (wrapping)
		OP_MOV_REG_INT, 0x09, 0xFF, 0xFF, 0xFF, 0xFF,
		OP_MOV_REG_INT, 0x0A, 0x00, 0x00, 0x00, 0x01,
		OP_ADD, 0x09, 0x0A,

		// R11 = 5, R12 = 10; SUB R11, R12 -> R11 = 0xFFFFFFFB (underflow wrapping)
		OP_MOV_REG_INT, 0x0B, 0x00, 0x00, 0x00, 0x05,
		OP_MOV_REG_INT, 0x0C, 0x00, 0x00, 0x00, 0x0A,
		OP_SUB, 0x0B, 0x0C,
	}

	c := New()
	if err := c.LoadBinary(encodeFLX(t, nil, code)); err != nil {
		t.Fatalf("LoadBinary: %v", err)
	}
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if c.Registers[0] != 35 {
		t.Errorf("ADD R1, R2: got %d, want 35", c.Registers[0])
	}
	if c.Registers[2] != 30 {
		t.Errorf("SUB R3, R4: got %d, want 30", c.Registers[2])
	}
	if c.Registers[4] != 42 {
		t.Errorf("MUL R5, R6: got %d, want 42", c.Registers[4])
	}
	if c.Registers[6] != 25 {
		t.Errorf("DIV R7, R8: got %d, want 25", c.Registers[6])
	}
	if c.Registers[8] != 0 {
		t.Errorf("ADD overflow R9, R10: got %d, want 0", c.Registers[8])
	}
	if c.Registers[10] != 0xFFFFFFFB {
		t.Errorf("SUB underflow R11, R12: got 0x%X, want 0xFFFFFFFB", c.Registers[10])
	}
}

func TestRun_ALUDivideByZero(t *testing.T) {
	memory.Reset()

	code := []byte{
		OP_MOV_REG_INT, 0x01, 0x00, 0x00, 0x00, 0x64, // R1 = 100
		OP_MOV_REG_INT, 0x02, 0x00, 0x00, 0x00, 0x00, // R2 = 0
		OP_DIV, 0x01, 0x02,                          // DIV R1, R2
	}

	c := New()
	if err := c.LoadBinary(encodeFLX(t, nil, code)); err != nil {
		t.Fatalf("LoadBinary: %v", err)
	}
	err := c.Run()
	if err == nil {
		t.Fatalf("Run: expected divide by zero error, got nil")
	}
	if !strings.Contains(err.Error(), "division by zero") {
		t.Errorf("Run error message: got %q, want 'division by zero'", err.Error())
	}
}

func TestRun_ALUBitwiseAndShifts(t *testing.T) {
	memory.Reset()

	code := []byte{
		// R1 = 0b1100 (12), R2 = 0b1010 (10); AND R1, R2 -> R1 = 0b1000 (8)
		OP_MOV_REG_INT, 0x01, 0x00, 0x00, 0x00, 0x0C,
		OP_MOV_REG_INT, 0x02, 0x00, 0x00, 0x00, 0x0A,
		OP_AND, 0x01, 0x02,

		// R3 = 0b1100 (12), R4 = 0b1010 (10); OR R3, R4 -> R3 = 0b1110 (14)
		OP_MOV_REG_INT, 0x03, 0x00, 0x00, 0x00, 0x0C,
		OP_MOV_REG_INT, 0x04, 0x00, 0x00, 0x00, 0x0A,
		OP_OR, 0x03, 0x04,

		// R5 = 0b1100 (12), R6 = 0b1010 (10); XOR R5, R6 -> R5 = 0b0110 (6)
		OP_MOV_REG_INT, 0x05, 0x00, 0x00, 0x00, 0x0C,
		OP_MOV_REG_INT, 0x06, 0x00, 0x00, 0x00, 0x0A,
		OP_XOR, 0x05, 0x06,

		// R7 = 1, R8 = 4; SHL R7, R8 -> R7 = 16
		OP_MOV_REG_INT, 0x07, 0x00, 0x00, 0x00, 0x01,
		OP_MOV_REG_INT, 0x08, 0x00, 0x00, 0x00, 0x04,
		OP_SHL, 0x07, 0x08,

		// R9 = 64, R10 = 3; SHR R9, R10 -> R9 = 8
		OP_MOV_REG_INT, 0x09, 0x00, 0x00, 0x00, 0x40,
		OP_MOV_REG_INT, 0x0A, 0x00, 0x00, 0x00, 0x03,
		OP_SHR, 0x09, 0x0A,

		// R11 = 1, R12 = 32; SHL R11, R12 -> R11 = 0
		OP_MOV_REG_INT, 0x0B, 0x00, 0x00, 0x00, 0x01,
		OP_MOV_REG_INT, 0x0C, 0x00, 0x00, 0x00, 0x20,
		OP_SHL, 0x0B, 0x0C,

		// R13 = 100, R14 = 35; SHR R13, R14 -> R13 = 0
		OP_MOV_REG_INT, 0x0D, 0x00, 0x00, 0x00, 0x64,
		OP_MOV_REG_INT, 0x0E, 0x00, 0x00, 0x00, 0x23,
		OP_SHR, 0x0D, 0x0E,
	}

	c := New()
	if err := c.LoadBinary(encodeFLX(t, nil, code)); err != nil {
		t.Fatalf("LoadBinary: %v", err)
	}
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if c.Registers[0] != 8 {
		t.Errorf("AND: got %d, want 8", c.Registers[0])
	}
	if c.Registers[2] != 14 {
		t.Errorf("OR: got %d, want 14", c.Registers[2])
	}
	if c.Registers[4] != 6 {
		t.Errorf("XOR: got %d, want 6", c.Registers[4])
	}
	if c.Registers[6] != 16 {
		t.Errorf("SHL: got %d, want 16", c.Registers[6])
	}
	if c.Registers[8] != 8 {
		t.Errorf("SHR: got %d, want 8", c.Registers[8])
	}
	if c.Registers[10] != 0 {
		t.Errorf("SHL >= 32: got %d, want 0", c.Registers[10])
	}
	if c.Registers[12] != 0 {
		t.Errorf("SHR >= 32: got %d, want 0", c.Registers[12])
	}
}


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

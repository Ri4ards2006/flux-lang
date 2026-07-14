package cpu

import (
	"bytes"
	"encoding/binary"
	"testing"

	"flux/vm/memory"
)

// TestLoadBinary_HappyPath exercises the loader with a minimal
// header, an empty constant pool, and a single-byte code section
// (the OP_NOP padding byte ensures Run() exits cleanly).
func TestLoadBinary_HappyPath(t *testing.T) {
	memory.Reset()

	var buf bytes.Buffer
	buf.WriteString(FlxMagic)
	buf.WriteByte(FlxVersion)
	// ConstantsCount = 0
	binary.Write(&buf, binary.BigEndian, uint16(0))
	// CodeSectionOffset and CodeSectionSize — a single 0x00 padding byte
	binary.Write(&buf, binary.BigEndian, uint32(FlxHeaderSize))
	binary.Write(&buf, binary.BigEndian, uint32(1))
	buf.WriteByte(0x00) // not a real opcode; Run() exits before consuming it

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
// ALLOC + FREE sequence. The Fresh pointer in R1 should round-trip
// through memory.Alloc + memory.Free without panicking.
func TestRun_AllocFreeCycle(t *testing.T) {
	memory.Reset()

	code := []byte{
		OP_ALLOC, 0x01, 0x00, 0x00, 0x00, 0x20, // ALLOC R1, 32
		OP_FREE, 0x01,                          // FREE R1
	}

	var buf bytes.Buffer
	buf.WriteString(FlxMagic)
	buf.WriteByte(FlxVersion)
	binary.Write(&buf, binary.BigEndian, uint16(0))
	binary.Write(&buf, binary.BigEndian, uint32(FlxHeaderSize))
	binary.Write(&buf, binary.BigEndian, uint32(len(code)))
	buf.Write(code)

	c := New()
	if err := c.LoadBinary(buf.Bytes()); err != nil {
		t.Fatalf("LoadBinary: unexpected error: %v", err)
	}

	if err := c.Run(); err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	// R1 should be the heap address returned by Alloc, then freed.
	if c.Registers[0] == 0 {
		t.Errorf("R1 after run: got 0, want the Alloc-returned address")
	}
	if memory.IsAllocated(c.Registers[0]) {
		t.Errorf("R1 block at %d should be free after FREE", c.Registers[0])
	}
}

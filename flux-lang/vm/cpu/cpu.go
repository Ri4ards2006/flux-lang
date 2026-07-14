// Package cpu implements the flux virtual machine.
//
// The CPU is a tiny single-threaded interpreter: a 16-register
// register file, a program counter, a flat copy of the loaded
// bytecode, and a flat constant pool. Run() walks the bytecode one
// instruction at a time, dispatching each opcode to a small handler.
//
// OP_TRIGGER_PIN and OP_SEND_CHAT print to stdout — they are
// simulations of GPIO toggles and Twitch chat messages respectively.
//
// OP_ON_CHAT just skips its associated body bytes; the VM does not
// have a chat subsystem yet, so the block can never fire during
// top-down execution. That wiring is the next phase.
package cpu

import (
	"encoding/binary"
	"errors"
	"fmt"

	"flux/vm/memory"
)

// Opcodes understood by this VM. Numeric values must match the
// compiler's emitter byte-for-byte.
const (
	OP_ALLOC       byte = 0x01
	OP_FREE        byte = 0x02
	OP_MOV_REG_REG byte = 0x03
	OP_MOV_REG_INT byte = 0x04
	OP_MOV_REG_STR byte = 0x05
	OP_TRIGGER_PIN byte = 0x06
	OP_SEND_CHAT   byte = 0x07
	OP_ON_CHAT     byte = 0x08
)

// .flx wire-format constants.
const (
	FlxMagic     = "FLUX"
	FlxVersion   byte = 1
	FlxHeaderSize     = 4 + 1 + 2 + 4 + 4 // magic|ver|count|codeOff|codeSize = 15
)

// CPU is the in-memory state of one running flux program.
type CPU struct {
	Registers [16]uint32
	PC        uint32
	Bytecode  []byte
	Constants []string
}

// New returns a CPU ready to LoadBinary.
func New() *CPU {
	return &CPU{}
}

// LoadBinary validates the FLUX header, parses the constant pool, and
// copies the code section into the CPU's bytecode slice.
func (c *CPU) LoadBinary(data []byte) error {
	if len(data) < FlxHeaderSize {
		return errors.New(".flx file too short for header")
	}
	if string(data[0:4]) != FlxMagic {
		return errors.New(".flx bad magic")
	}
	if data[4] != FlxVersion {
		return fmt.Errorf(".flx bad version: got %d want %d", data[4], FlxVersion)
	}

	constCount := binary.BigEndian.Uint16(data[5:7])
	codeOffset := binary.BigEndian.Uint32(data[7:11])
	codeSize := binary.BigEndian.Uint32(data[11:15])
	if codeOffset+codeSize > uint32(len(data)) {
		return errors.New(".flx code section extends past file end")
	}

	consts, err := parseConstants(data[FlxHeaderSize:codeOffset], int(constCount))
	if err != nil {
		return fmt.Errorf(".flx constant pool: %w", err)
	}

	c.Constants = consts
	c.Bytecode = make([]byte, codeSize)
	copy(c.Bytecode, data[codeOffset:codeOffset+codeSize])
	c.PC = 0
	return nil
}

func parseConstants(pool []byte, n int) ([]string, error) {
	out := make([]string, n)
	pos := 0
	for i := 0; i < n; i++ {
		if pos+4 > len(pool) {
			return nil, fmt.Errorf("constant %d: length truncated", i)
		}
		l := binary.BigEndian.Uint32(pool[pos : pos+4])
		pos += 4
		if int(l) > len(pool)-pos {
			return nil, fmt.Errorf("constant %d: body truncated (need %d, have %d)", i, l, len(pool)-pos)
		}
		out[i] = string(pool[pos : pos+int(l)])
		pos += int(l)
	}
	return out, nil
}

// Run executes the loaded bytecode until PC walks off the end or an
// instruction returns an error.
func (c *CPU) Run() error {
	for c.PC < uint32(len(c.Bytecode)) {
		op := c.Bytecode[c.PC]
		switch op {
		case OP_ALLOC:
			if err := c.opAlloc(); err != nil {
				return err
			}
		case OP_FREE:
			if err := c.opFree(); err != nil {
				return err
			}
		case OP_MOV_REG_REG:
			c.opMovRegReg()
		case OP_MOV_REG_INT:
			c.opMovRegInt()
		case OP_MOV_REG_STR:
			c.opMovRegStr()
		case OP_TRIGGER_PIN:
			c.opTriggerPin()
		case OP_SEND_CHAT:
			c.opSendChat()
		case OP_ON_CHAT:
			if err := c.opOnChat(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("pc=%d: unknown opcode 0x%02x", c.PC, op)
		}
	}
	return nil
}

// registerCode converts the wire-format register code (1..16) into
// the Registers[] slice index (0..15).
func registerCode(reg byte) uint8 {
	if reg < 1 || reg > 16 {
		return 0
	}
	return reg - 1
}

func (c *CPU) opAlloc() error {
	if c.PC+6 > uint32(len(c.Bytecode)) {
		return errors.New("ALLOC truncated")
	}
	reg := c.Bytecode[c.PC+1]
	size := binary.BigEndian.Uint32(c.Bytecode[c.PC+2 : c.PC+6])
	addr, err := memory.Alloc(size)
	if err != nil {
		return fmt.Errorf("ALLOC: %w", err)
	}
	c.Registers[registerCode(reg)] = addr
	c.PC += 6
	return nil
}

func (c *CPU) opFree() error {
	if c.PC+2 > uint32(len(c.Bytecode)) {
		return errors.New("FREE truncated")
	}
	reg := c.Bytecode[c.PC+1]
	addr := c.Registers[registerCode(reg)]
	if err := memory.Free(addr); err != nil {
		return fmt.Errorf("FREE: %w", err)
	}
	c.PC += 2
	return nil
}

func (c *CPU) opMovRegReg() {
	dst := c.Bytecode[c.PC+1]
	src := c.Bytecode[c.PC+2]
	c.Registers[registerCode(dst)] = c.Registers[registerCode(src)]
	c.PC += 3
}

func (c *CPU) opMovRegInt() {
	dst := c.Bytecode[c.PC+1]
	v := binary.BigEndian.Uint32(c.Bytecode[c.PC+2 : c.PC+6])
	c.Registers[registerCode(dst)] = v
	c.PC += 6
}

// opMovRegStr stores the constant-pool index into the destination
// register. We do not copy the string into the heap yet — the bare
// VM only needs to remember which constant a SEND_CHAT will read.
func (c *CPU) opMovRegStr() {
	dst := c.Bytecode[c.PC+1]
	idx := binary.BigEndian.Uint32(c.Bytecode[c.PC+2 : c.PC+6])
	c.Registers[registerCode(dst)] = idx
	c.PC += 6
}

func (c *CPU) opTriggerPin() {
	pin := c.Bytecode[c.PC+1]
	state := c.Bytecode[c.PC+2]
	fmt.Printf("TRIGGER_PIN pin=%d state=%d\n", pin, state)
	c.PC += 3
}

// opSendChat honours the high-bit-tagged operand the wire format
// uses: bits other than the top one are either a register code or a
// constant-pool index.
func (c *CPU) opSendChat() {
	const highBit uint32 = 0x80000000
	operand := binary.BigEndian.Uint32(c.Bytecode[c.PC+1 : c.PC+5])

	if operand&highBit != 0 {
		idx := operand &^ highBit
		if int(idx) < len(c.Constants) {
			fmt.Printf("SEND_CHAT %q\n", c.Constants[idx])
		}
	} else {
		reg := byte(operand)
		idx := c.Registers[registerCode(reg)]
		if int(idx) < len(c.Constants) {
			fmt.Printf("SEND_CHAT %q\n", c.Constants[idx])
		}
	}
	c.PC += 5
}

// opOnChat just skips past the body. There is no chat subsystem yet
// so the body never fires during top-down execution.
func (c *CPU) opOnChat() error {
	if c.PC+14 > uint32(len(c.Bytecode)) {
		return errors.New("ON_CHAT header truncated")
	}
	bodyLen := binary.BigEndian.Uint32(c.Bytecode[c.PC+10 : c.PC+14])
	c.PC += 14 + bodyLen
	return nil
}

// Package cpu implements the flux virtual machine.
//
// The VM is single-threaded and small: a 16-register register file,
// a program counter, a copy of the loaded bytecode, and a parsed
// constant pool. Run() walks the bytecode one instruction at a time
// through a single dispatchOne() helper.
//
// OP_MOV_REG_STR heap-copies a constant-pool string (plus a NUL
// terminator) into RAM and stores the returned address in the
// destination register.
//
// OP_ON_CHAT appends an EventSubscription to ActiveSubscriptions
// and skips the body's bytes during top-down execution. The body
// only fires when DeliverChatMessage matches a subscription's
// pattern via DeliverChatMessage(username, message) — at which
// point the user_var register is set to a heap-resident copy of
// the sender's username, the body is dispatched instruction by
// instruction until exactly BodyLength bytes have been consumed,
// and the username block is freed before returning.
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
	OP_ADD         byte = 0x09
	OP_SUB         byte = 0x0A
	OP_MUL         byte = 0x0B
	OP_DIV         byte = 0x0C
	OP_AND         byte = 0x0D
	OP_OR          byte = 0x0E
	OP_XOR         byte = 0x0F
	OP_SHL         byte = 0x10
	OP_SHR         byte = 0x11
	OP_CMP         byte = 0x12
	OP_JMP         byte = 0x13
	OP_JZ          byte = 0x14
	OP_JNZ         byte = 0x15
	OP_CALL        byte = 0x16
	OP_RET         byte = 0x17
)

// .flx wire-format constants.
const (
	FlxMagic          = "FLUX"
	FlxVersion   byte = 1
	FlxHeaderSize     = 4 + 1 + 2 + 4 + 4 // 15
)

// MaxCallStackDepth is the maximum nesting depth for subroutines before stack overflow.
const MaxCallStackDepth = 1024

// EventSubscription holds one ON_CHAT registration. BodyOffset and
// BodyLength are absolute offsets into c.Bytecode.
type EventSubscription struct {
	Pattern    string
	UserVarReg uint8   // 1..16
	BodyOffset uint32  // byte offset within the code section
	BodyLength uint32  // size of the body in bytes
}

// CPU is the volatile state of one running flux program.
type CPU struct {
	Registers           [16]uint32
	PC                  uint32
	Bytecode            []byte
	Constants           []string
	ActiveSubscriptions []EventSubscription
	Logs                []string
	ZeroFlag            bool
	SignFlag            bool
	CallStack           []uint32
}

// New returns a CPU ready to LoadBinary.
func New() *CPU {
	return &CPU{}
}

// log appends a formatted line to c.Logs. UI code (e.g. main.go)
// can flush c.Logs to stdout after Run() or after each chat
// delivery.
func (c *CPU) log(format string, args ...interface{}) {
	c.Logs = append(c.Logs, fmt.Sprintf(format, args...))
}

// LoadBinary validates the FLUX header, parses the constant pool,
// and copies the code section into c.Bytecode.
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

// Run executes the loaded bytecode until PC walks off the end of
// the code section or an instruction returns an error.
func (c *CPU) Run() error {
	for c.PC < uint32(len(c.Bytecode)) {
		if err := c.dispatchOne(); err != nil {
			return err
		}
	}
	return nil
}

// dispatchOne decodes and executes the single instruction at c.PC.
// Both Run() and DeliverChatMessage() drive themselves through this
// helper so opcode behaviour stays consistent on every path.
func (c *CPU) dispatchOne() error {
	if c.PC >= uint32(len(c.Bytecode)) {
		return nil
	}
	op := c.Bytecode[c.PC]
	switch op {
	case OP_ALLOC:
		return c.opAlloc()
	case OP_FREE:
		return c.opFree()
	case OP_MOV_REG_REG:
		c.opMovRegReg()
		return nil
	case OP_MOV_REG_INT:
		c.opMovRegInt()
		return nil
	case OP_MOV_REG_STR:
		return c.opMovRegStr()
	case OP_TRIGGER_PIN:
		c.opTriggerPin()
		return nil
	case OP_SEND_CHAT:
		c.opSendChat()
		return nil
	case OP_ON_CHAT:
		return c.opOnChat()
	case OP_ADD:
		return c.opAdd()
	case OP_SUB:
		return c.opSub()
	case OP_MUL:
		return c.opMul()
	case OP_DIV:
		return c.opDiv()
	case OP_AND:
		return c.opAnd()
	case OP_OR:
		return c.opOr()
	case OP_XOR:
		return c.opXor()
	case OP_SHL:
		return c.opShl()
	case OP_SHR:
		return c.opShr()
	case OP_CMP:
		return c.opCmp()
	case OP_JMP:
		return c.opJmp()
	case OP_JZ:
		return c.opJz()
	case OP_JNZ:
		return c.opJnz()
	case OP_CALL:
		return c.opCall()
	case OP_RET:
		return c.opRet()
	default:
		return fmt.Errorf("pc=%d: unknown opcode 0x%02x", c.PC, op)
	}
}

// registerCode converts the wire-format register code (1..16) to the
// Registers[] slice index (0..15). Out-of-range codes default to 0.
func registerCode(reg byte) uint8 {
	if reg < 1 || reg > 16 {
		return 0
	}
	return reg - 1
}

// ---------------------------------------------------------------------------
// Per-opcode handlers
// ---------------------------------------------------------------------------

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

// opMovRegStr copies the constant-pool string (plus a trailing NUL
// terminator) into a freshly-allocated RAM block and stores the
// returned address in the destination register. This replaces the
// heap address the previous ALLOC put there — programs that want
// to keep both blocks must use distinct registers.
func (c *CPU) opMovRegStr() error {
	if c.PC+6 > uint32(len(c.Bytecode)) {
		return errors.New("MOV_REG_STR truncated")
	}
	dst := c.Bytecode[c.PC+1]
	idx := binary.BigEndian.Uint32(c.Bytecode[c.PC+2 : c.PC+6])
	if int(idx) >= len(c.Constants) {
		return fmt.Errorf("MOV_REG_STR: constant index %d out of range (pool=%d)", idx, len(c.Constants))
	}
	s := c.Constants[idx]

	addr, err := memory.Alloc(uint32(len(s) + 1))
	if err != nil {
		return fmt.Errorf("MOV_REG_STR: %w", err)
	}
	for i := 0; i < len(s); i++ {
		memory.RAM[addr+uint32(i)] = s[i]
	}
	memory.RAM[addr+uint32(len(s))] = 0

	c.Registers[registerCode(dst)] = addr
	c.PC += 6
	return nil
}

func (c *CPU) opTriggerPin() {
	pin := c.Bytecode[c.PC+1]
	state := c.Bytecode[c.PC+2]
	c.log("TRIGGER_PIN pin=%d state=%d", pin, state)
	c.PC += 3
}

// opSendChat honours the high-bit-tagged operand convention: setting
// bit 31 denotes a constant-pool index, clearing it denotes a
// register code whose value should be interpreted as a heap address
// holding a NUL-terminated C-string.
func (c *CPU) opSendChat() {
	const highBit uint32 = 0x80000000
	operand := binary.BigEndian.Uint32(c.Bytecode[c.PC+1 : c.PC+5])

	if operand&highBit != 0 {
		idx := operand &^ highBit
		if int(idx) < len(c.Constants) {
			c.log("SEND_CHAT %q", c.Constants[idx])
		}
	} else {
		reg := byte(operand)
		addr := c.Registers[registerCode(reg)]
		if addr >= memory.HeaderSize && addr < memory.RAMSize {
			end := addr
			for end < memory.RAMSize && memory.RAM[end] != 0 {
				end++
			}
			if end < memory.RAMSize {
				c.log("SEND_CHAT %q", string(memory.RAM[addr:end]))
			}
		}
	}
	c.PC += 5
}

// opOnChat registers the event and skips the body during top-down
// execution. The body only fires when DeliverChatMessage
// explicitly dispatches it.
func (c *CPU) opOnChat() error {
	if c.PC+14 > uint32(len(c.Bytecode)) {
		return errors.New("ON_CHAT header truncated")
	}
	triggerIdx := binary.BigEndian.Uint32(c.Bytecode[c.PC+1 : c.PC+5])
	userVar := c.Bytecode[c.PC+5]
	bodyOffset := binary.BigEndian.Uint32(c.Bytecode[c.PC+6 : c.PC+10])
	bodyLength := binary.BigEndian.Uint32(c.Bytecode[c.PC+10 : c.PC+14])

	if int(triggerIdx) >= len(c.Constants) {
		return fmt.Errorf("ON_CHAT: trigger index %d out of range", triggerIdx)
	}

	c.ActiveSubscriptions = append(c.ActiveSubscriptions, EventSubscription{
		Pattern:    c.Constants[triggerIdx],
		UserVarReg: userVar,
		BodyOffset: bodyOffset,
		BodyLength: bodyLength,
	})
	c.log("ON_CHAT subscribed pattern=%q user_var=R%d body=[%d..%d)",
		c.Constants[triggerIdx], userVar, bodyOffset, bodyOffset+bodyLength)

	c.PC += 14 + bodyLength
	return nil
}

func (c *CPU) opAdd() error {
	if c.PC+3 > uint32(len(c.Bytecode)) {
		return errors.New("ADD truncated")
	}
	dst := registerCode(c.Bytecode[c.PC+1])
	src := registerCode(c.Bytecode[c.PC+2])
	c.Registers[dst] = c.Registers[dst] + c.Registers[src]
	c.PC += 3
	return nil
}

func (c *CPU) opSub() error {
	if c.PC+3 > uint32(len(c.Bytecode)) {
		return errors.New("SUB truncated")
	}
	dst := registerCode(c.Bytecode[c.PC+1])
	src := registerCode(c.Bytecode[c.PC+2])
	c.Registers[dst] = c.Registers[dst] - c.Registers[src]
	c.PC += 3
	return nil
}

func (c *CPU) opMul() error {
	if c.PC+3 > uint32(len(c.Bytecode)) {
		return errors.New("MUL truncated")
	}
	dst := registerCode(c.Bytecode[c.PC+1])
	src := registerCode(c.Bytecode[c.PC+2])
	c.Registers[dst] = c.Registers[dst] * c.Registers[src]
	c.PC += 3
	return nil
}

func (c *CPU) opDiv() error {
	if c.PC+3 > uint32(len(c.Bytecode)) {
		return errors.New("DIV truncated")
	}
	dst := registerCode(c.Bytecode[c.PC+1])
	src := registerCode(c.Bytecode[c.PC+2])
	if c.Registers[src] == 0 {
		return errors.New("DIV: division by zero")
	}
	c.Registers[dst] = c.Registers[dst] / c.Registers[src]
	c.PC += 3
	return nil
}

func (c *CPU) opAnd() error {
	if c.PC+3 > uint32(len(c.Bytecode)) {
		return errors.New("AND truncated")
	}
	dst := registerCode(c.Bytecode[c.PC+1])
	src := registerCode(c.Bytecode[c.PC+2])
	c.Registers[dst] = c.Registers[dst] & c.Registers[src]
	c.PC += 3
	return nil
}

func (c *CPU) opOr() error {
	if c.PC+3 > uint32(len(c.Bytecode)) {
		return errors.New("OR truncated")
	}
	dst := registerCode(c.Bytecode[c.PC+1])
	src := registerCode(c.Bytecode[c.PC+2])
	c.Registers[dst] = c.Registers[dst] | c.Registers[src]
	c.PC += 3
	return nil
}

func (c *CPU) opXor() error {
	if c.PC+3 > uint32(len(c.Bytecode)) {
		return errors.New("XOR truncated")
	}
	dst := registerCode(c.Bytecode[c.PC+1])
	src := registerCode(c.Bytecode[c.PC+2])
	c.Registers[dst] = c.Registers[dst] ^ c.Registers[src]
	c.PC += 3
	return nil
}

func (c *CPU) opShl() error {
	if c.PC+3 > uint32(len(c.Bytecode)) {
		return errors.New("SHL truncated")
	}
	dst := registerCode(c.Bytecode[c.PC+1])
	src := registerCode(c.Bytecode[c.PC+2])
	shift := c.Registers[src]
	if shift >= 32 {
		c.Registers[dst] = 0
	} else {
		c.Registers[dst] = c.Registers[dst] << shift
	}
	c.PC += 3
	return nil
}

func (c *CPU) opShr() error {
	if c.PC+3 > uint32(len(c.Bytecode)) {
		return errors.New("SHR truncated")
	}
	dst := registerCode(c.Bytecode[c.PC+1])
	src := registerCode(c.Bytecode[c.PC+2])
	shift := c.Registers[src]
	if shift >= 32 {
		c.Registers[dst] = 0
	} else {
		c.Registers[dst] = c.Registers[dst] >> shift
	}
	c.PC += 3
	return nil
}

func (c *CPU) opCmp() error {
	if c.PC+3 > uint32(len(c.Bytecode)) {
		return errors.New("CMP truncated")
	}
	reg1 := registerCode(c.Bytecode[c.PC+1])
	reg2 := registerCode(c.Bytecode[c.PC+2])
	v1 := c.Registers[reg1]
	v2 := c.Registers[reg2]
	c.ZeroFlag = (v1 == v2)
	c.SignFlag = (v1 < v2)
	c.PC += 3
	return nil
}

func (c *CPU) opJmp() error {
	if c.PC+5 > uint32(len(c.Bytecode)) {
		return errors.New("JMP truncated")
	}
	target := binary.BigEndian.Uint32(c.Bytecode[c.PC+1 : c.PC+5])
	if target > uint32(len(c.Bytecode)) {
		return fmt.Errorf("JMP: target %d out of bounds (code size %d)", target, len(c.Bytecode))
	}
	c.PC = target
	return nil
}

func (c *CPU) opJz() error {
	if c.PC+5 > uint32(len(c.Bytecode)) {
		return errors.New("JZ truncated")
	}
	if c.ZeroFlag {
		target := binary.BigEndian.Uint32(c.Bytecode[c.PC+1 : c.PC+5])
		if target > uint32(len(c.Bytecode)) {
			return fmt.Errorf("JZ: target %d out of bounds (code size %d)", target, len(c.Bytecode))
		}
		c.PC = target
	} else {
		c.PC += 5
	}
	return nil
}

func (c *CPU) opJnz() error {
	if c.PC+5 > uint32(len(c.Bytecode)) {
		return errors.New("JNZ truncated")
	}
	if !c.ZeroFlag {
		target := binary.BigEndian.Uint32(c.Bytecode[c.PC+1 : c.PC+5])
		if target > uint32(len(c.Bytecode)) {
			return fmt.Errorf("JNZ: target %d out of bounds (code size %d)", target, len(c.Bytecode))
		}
		c.PC = target
	} else {
		c.PC += 5
	}
	return nil
}

func (c *CPU) opCall() error {
	if c.PC+5 > uint32(len(c.Bytecode)) {
		return errors.New("CALL truncated")
	}
	target := binary.BigEndian.Uint32(c.Bytecode[c.PC+1 : c.PC+5])
	if target > uint32(len(c.Bytecode)) {
		return fmt.Errorf("CALL: target %d out of bounds (code size %d)", target, len(c.Bytecode))
	}
	if len(c.CallStack) >= MaxCallStackDepth {
		return errors.New("CALL: stack overflow")
	}
	returnPC := c.PC + 5
	c.CallStack = append(c.CallStack, returnPC)
	c.PC = target
	return nil
}

func (c *CPU) opRet() error {
	if len(c.CallStack) == 0 {
		return errors.New("RET: stack underflow")
	}
	retPC := c.CallStack[len(c.CallStack)-1]
	c.CallStack = c.CallStack[:len(c.CallStack)-1]
	if retPC > uint32(len(c.Bytecode)) {
		return fmt.Errorf("RET: return PC %d out of bounds (code size %d)", retPC, len(c.Bytecode))
	}
	c.PC = retPC
	return nil
}

// DeliverChatMessage fires every subscription whose Pattern exactly
// equals message. For each match it:
//
//  1. Allocates a heap block for username + NUL,
//  2. Writes the username into that block and stores the address
//     in the subscription's UserVarReg,
//  3. Saves the current PC, jumps the PC to the body's offset, and
//     dispatches instructions until exactly BodyLength bytes have
//     been consumed,
//  4. Frees the username block,
//  5. Restores the saved PC.
//
// Dispatch errors inside a body are logged but do not abort the
// function — the machine keeps going to the next subscription.
func (c *CPU) DeliverChatMessage(username, message string) {
	for _, sub := range c.ActiveSubscriptions {
		if sub.Pattern != message {
			continue
		}
		if sub.UserVarReg < 1 || sub.UserVarReg > 16 {
			c.log("DeliverChatMessage: subscription %q has invalid user_var=%d",
				sub.Pattern, sub.UserVarReg)
			continue
		}

		addr, err := memory.Alloc(uint32(len(username) + 1))
		if err != nil {
			c.log("DeliverChatMessage: alloc username: %v", err)
			continue
		}
		for i := 0; i < len(username); i++ {
			memory.RAM[addr+uint32(i)] = username[i]
		}
		memory.RAM[addr+uint32(len(username))] = 0

		c.Registers[sub.UserVarReg-1] = addr
		c.log(">>> ON_CHAT match pattern=%q username=%q user_var=R%d username_addr=%d",
			sub.Pattern, username, sub.UserVarReg, addr)

		savedPC := c.PC
		c.PC = sub.BodyOffset
		bodyEnd := sub.BodyOffset + sub.BodyLength
		// Loop while we're still inside the declared body AND still
		// inside the loaded bytecode. The second guard is defensive:
		// if a hand-crafted .flx declares body_length larger than the
		// remaining code, we must not spin when dispatchOne returns
		// nil for a PC that already walked off the end.
		codeEnd := uint32(len(c.Bytecode))
		for c.PC < bodyEnd && c.PC < codeEnd {
			if err := c.dispatchOne(); err != nil {
				c.log("chat dispatch: %v", err)
				break
			}
		}
		c.log("<<< ON_CHAT block end")

		if err := memory.Free(addr); err != nil {
			c.log("DeliverChatMessage: free username: %v", err)
		}

		c.PC = savedPC
	}
}

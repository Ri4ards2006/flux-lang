// Package codegen implements the bytecode emitter that turns the parsed
// AST into the .flx binary format consumed by the flux virtual machine.
//
// This file defines the ISA: opcode constants, operand encoding helpers,
// and the .flx header/file-format constants. The actual emitting logic
// lives in codegen.go.
package codegen

import (
	"fmt"
	"strconv"
)

// ---------------------------------------------------------------------------
// Opcodes
// ---------------------------------------------------------------------------

// Opcodes for the flux virtual machine. Each opcode is a single byte that
// starts an instruction; the bytes that follow are interpretation-specific
// to the opcode (see the layout comments).
//
// The numeric values are part of the .flx binary format and MUST NOT be
// renumbered once they have been emitted into a real program.
const (
	// OP_ALLOC: allocate `Size` cells for the register named `Reg`.
	//   Layout: [OP_ALLOC] [Reg:1] [Size:Uint32]
	OP_ALLOC byte = 0x01

	// OP_FREE: release the register back to the runtime.
	//   Layout: [OP_FREE] [Reg:1]
	OP_FREE byte = 0x02

	// OP_MOV_REG_REG: register-to-register copy.
	//   Layout: [OP_MOV_REG_REG] [DestReg:1] [SrcReg:1]
	OP_MOV_REG_REG byte = 0x03

	// OP_MOV_REG_INT: register <- 32-bit integer literal.
	//   Layout: [OP_MOV_REG_INT] [DestReg:1] [Value:Int32]
	OP_MOV_REG_INT byte = 0x04

	// OP_MOV_REG_STR: register <- string from the constant pool.
	//   Layout: [OP_MOV_REG_STR] [DestReg:1] [StringConstantIndex:Uint32]
	OP_MOV_REG_STR byte = 0x05

	// OP_TRIGGER_PIN: simulate toggling a GPIO pin.
	//   Layout: [OP_TRIGGER_PIN] [Pin:Uint8] [State:Uint8]
	OP_TRIGGER_PIN byte = 0x06

	// OP_SEND_CHAT: emit a chat message sourced from either a register
	// (1..16) or a string from the constant pool (encoded Uint32, see
	// encodeOperand below).
	//   Layout: [OP_SEND_CHAT] [Operand:Uint32]
	OP_SEND_CHAT byte = 0x07

	// OP_ON_CHAT: register an event handler.
	//   Layout:
	//     [OP_ON_CHAT] [StringConstantIndex:Uint32] [UserVarReg:1]
	//     [OffsetToBodyStart:Uint32] [BodyLength:Uint32]
	//
	// The body of the ON_CHAT block follows IMMEDIATELY AFTER this
	// 14-byte header. The VM, after registering the listener, advances
	// its instruction pointer past the body by [BodyLength] bytes so
	// linear top-down execution skips over the block. When the event
	// fires the VM jumps to [OffsetToBodyStart].
	OP_ON_CHAT byte = 0x08

	// OP_ADD: DestReg <- DestReg + SrcReg (uint32 wrapping addition)
	//   Layout: [OP_ADD] [DestReg:1] [SrcReg:1]
	OP_ADD byte = 0x09

	// OP_SUB: DestReg <- DestReg - SrcReg (uint32 wrapping subtraction)
	//   Layout: [OP_SUB] [DestReg:1] [SrcReg:1]
	OP_SUB byte = 0x0A

	// OP_MUL: DestReg <- DestReg * SrcReg (uint32 wrapping multiplication)
	//   Layout: [OP_MUL] [DestReg:1] [SrcReg:1]
	OP_MUL byte = 0x0B

	// OP_DIV: DestReg <- DestReg / SrcReg (uint32 division; error if SrcReg == 0)
	//   Layout: [OP_DIV] [DestReg:1] [SrcReg:1]
	OP_DIV byte = 0x0C

	// OP_AND: DestReg <- DestReg & SrcReg (bitwise AND)
	//   Layout: [OP_AND] [DestReg:1] [SrcReg:1]
	OP_AND byte = 0x0D

	// OP_OR: DestReg <- DestReg | SrcReg (bitwise OR)
	//   Layout: [OP_OR] [DestReg:1] [SrcReg:1]
	OP_OR byte = 0x0E

	// OP_XOR: DestReg <- DestReg ^ SrcReg (bitwise XOR)
	//   Layout: [OP_XOR] [DestReg:1] [SrcReg:1]
	OP_XOR byte = 0x0F

	// OP_SHL: DestReg <- DestReg << SrcReg (logical shift left; yields 0 if SrcReg >= 32)
	//   Layout: [OP_SHL] [DestReg:1] [SrcReg:1]
	OP_SHL byte = 0x10

	// OP_SHR: DestReg <- DestReg >> SrcReg (logical shift right; yields 0 if SrcReg >= 32)
	//   Layout: [OP_SHR] [DestReg:1] [SrcReg:1]
	OP_SHR byte = 0x11

	// OP_CMP: compare Reg1 with Reg2, updating ZeroFlag and SignFlag
	//   Layout: [OP_CMP] [Reg1:1] [Reg2:1]
	OP_CMP byte = 0x12

	// OP_JMP: unconditional jump to TargetPC
	//   Layout: [OP_JMP] [TargetPC:Uint32]
	OP_JMP byte = 0x13

	// OP_JZ: jump to TargetPC if ZeroFlag is true
	//   Layout: [OP_JZ] [TargetPC:Uint32]
	OP_JZ byte = 0x14

	// OP_JNZ: jump to TargetPC if ZeroFlag is false
	//   Layout: [OP_JNZ] [TargetPC:Uint32]
	OP_JNZ byte = 0x15
)

// ---------------------------------------------------------------------------
// .flx file format constants
// ---------------------------------------------------------------------------

// FlxMagic is the 4-byte magic identifier prepended to every .flx file.
// The VM MUST refuse to decode any file that does not start with these
// bytes — that's the only "checksum" the format carries.
const FlxMagic = "FLUX"

// FlxVersion is the supported .flx major version. Wire-format
// compatibility breaks whenever this number changes.
const FlxVersion byte = 1

// FlxHeaderSize is the fixed size in bytes of the .flx file header.
//
// Layout (15 bytes total):
//
//	Bytes  [0..4)    Magic "FLUX"
//	Byte   [4]       Version (=1)
//	Bytes  [5..7)    ConstantsCount (big-endian Uint16)
//	Bytes  [7..11)   CodeSectionOffset (big-endian Uint32, from file start)
//	Bytes  [11..15)  CodeSectionSize (big-endian Uint32)
//
// We keep both CodeSectionOffset and CodeSectionSize as dedicated
// Uint32s rather than packing them into a single 4-byte field — the
// 4 extra bytes of overhead buy us guaranteed invariants in the VM
// loader instead of constant-pool-driven offsets that have to be
// recomputed on every load.
const FlxHeaderSize = 15

// ---------------------------------------------------------------------------
// Register encoding (R1..R16 → Uint8)
// ---------------------------------------------------------------------------

// FirstRegisterCode is the encoded value for `R1`. Subsequent registers
// are encoded sequentially: R2 = 2, …, R16 = 16.
const FirstRegisterCode byte = 1

// LastRegisterCode is the encoded value for `R16`.
const LastRegisterCode byte = 16

// encodeRegister converts the textual register name (e.g. "R12") into
// its 1-byte code (e.g. 12). It returns a non-nil error for anything
// outside R1..R16 so the emitter never silently emits a bogus byte.
func encodeRegister(name string) (byte, error) {
	if len(name) < 2 || name[0] != 'R' {
		return 0, fmt.Errorf("invalid register name %q", name)
	}
	n, err := strconv.Atoi(name[1:])
	if err != nil {
		return 0, fmt.Errorf("invalid register name %q: %w", name, err)
	}
	if n < int(FirstRegisterCode) || n > int(LastRegisterCode) {
		return 0, fmt.Errorf("register number %d out of range R1..R16", n)
	}
	return byte(n), nil
}

// decodeRegister flips encodeRegister — used in tests and (eventually)
// by the VM.
func decodeRegister(code byte) (string, error) {
	if code < FirstRegisterCode || code > LastRegisterCode {
		return "", fmt.Errorf("register code %d out of range R1..R16", code)
	}
	return "R" + strconv.Itoa(int(code)), nil
}

// ---------------------------------------------------------------------------
// SEND_CHAT operand encoding (tagged Uint32)
// ---------------------------------------------------------------------------

// Operand tag sits in the high bit of the Uint32 operand of OP_SEND_CHAT.
// High bit cleared = register; high bit set = constant-pool index.
//
// This is the same scheme Lua uses for its top-bit tag for "numbers vs
// objects": single-instruction type discrimination, no extra byte.
const (
	operandTagRegister uint32 = 0x00000000
	operandTagConstant uint32 = 0x80000000
	operandTagMask     uint32 = operandTagConstant
)

// encodeOperand packs an operand (either a register name or a string
// constant) into the tagged Uint32 format expected by OP_SEND_CHAT.
// Mutually-exclusive nil-ness is enforced by the caller — if both
// arguments are zero-value the result is the register-zero encoding,
// which the VM must reject.
func encodeOperandAsRegister(regName string) (uint32, error) {
	regCode, err := encodeRegister(regName)
	if err != nil {
		return 0, err
	}
	return operandTagRegister | uint32(regCode), nil
}

func encodeOperandAsConstant(constantIndex uint32) uint32 {
	return operandTagConstant | constantIndex
}

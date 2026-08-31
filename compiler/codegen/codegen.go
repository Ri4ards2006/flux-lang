// Package codegen — bytecode emitter for flux-lang.
//
// The Compiler walks an AST and writes a flat bytecode buffer plus a
// deduplicated string constant pool. After every statement has been
// emitted, Binary() assembles the .flx file layout:
//
//	[ Header ] [ Constant Pool ] [ Code Section ]
//
// The emitter uses 0-indexed byte offsets inside the code section for
// ON_CHAT body metadata; the header's CodeSectionOffset is added to
// those offsets at VM-load time. This keeps every offset the emitter
// writes small (fits in Uint32) and lets us reason about the layout
// without knowing the constant-pool size ahead of time.
package codegen

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"flux/compiler/ast"
	"flux/compiler/lexer"
)

type jumpFixup struct {
	offset    int
	label     string
	tokenType lexer.TokenType
}

// Compiler holds the in-progress bytecode buffer and constant pool.
// A single Compiler instance is NOT safe for concurrent use; instantiate
// one per goroutine.
type Compiler struct {
	bytecode   []byte            // flat instruction buffer (code section)
	constants  []string          // constant pool in insertion order
	constMap   map[string]uint32 // dedup map: string -> pool index
	labels     map[string]uint32 // label name -> byte offset in bytecode
	jumpFixups []jumpFixup       // pending label backpatches
	errors     []error           // collected compile-time errors
}

// New returns an empty Compiler ready to walk an AST.
func New() *Compiler {
	return &Compiler{
		bytecode:   []byte{},
		constants:  []string{},
		constMap:   make(map[string]uint32),
		labels:     make(map[string]uint32),
		jumpFixups: []jumpFixup{},
	}
}

// Errors returns a snapshot of the compiler's error slice.
func (c *Compiler) Errors() []error {
	out := make([]error, len(c.errors))
	copy(out, c.errors)
	return out
}

// ---------------------------------------------------------------------------
// Public entry: Binary assembly
// ---------------------------------------------------------------------------

// Code returns the raw code-section bytes. Exposed mostly so tests can
// assert against the bytecode in isolation, without the full .flx
// header; Binary() is the production-grade output.
func (c *Compiler) Code() []byte {
	out := make([]byte, len(c.bytecode))
	copy(out, c.bytecode)
	return out
}

// Constants returns a copy of the constant pool in insertion order.
func (c *Compiler) Constants() []string {
	out := make([]string, len(c.constants))
	copy(out, c.constants)
	return out
}

// Binary assembles the .flx file with the documented header, the
// constant pool, and the code section, and returns it as a single byte
// slice. The assembler owns the layout below:
//
//	Bytes  [0..4)    Magic "FLUX"
//	Byte   [4]       Version (=1)
//	Bytes  [5..7)    ConstantsCount (big-endian Uint16)
//	Bytes  [7..11)   CodeSectionOffset (big-endian Uint32, from file start)
//	Bytes  [11..15)  CodeSectionSize (big-endian Uint32)
//
//	Then the constant pool (sequential [Length:Uint32][Bytes]).
//	Then the code section.
func (c *Compiler) Binary() []byte {
	constPool := c.encodeConstantPool()
	code := c.Code()

	// Header reserves FlxHeaderSize bytes.
	codeOffset := uint32(FlxHeaderSize + len(constPool))
	codeSize := uint32(len(code))

	buf := make([]byte, 0, codeOffset+codeSize)
	// Magic
	buf = append(buf, []byte(FlxMagic)...)
	// Version
	buf = append(buf, FlxVersion)
	// ConstantsCount (Uint16)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(c.constants)))
	// CodeSectionOffset + CodeSectionSize (Uint32 each)
	buf = binary.BigEndian.AppendUint32(buf, codeOffset)
	buf = binary.BigEndian.AppendUint32(buf, codeSize)
	// Constant pool
	buf = append(buf, constPool...)
	// Code section
	buf = append(buf, code...)

	return buf
}

// encodeConstantPool converts each entry to [Length:Uint32][RawBytes]
// and concatenates them in pool order.
func (c *Compiler) encodeConstantPool() []byte {
	var buf bytes.Buffer
	for _, s := range c.constants {
		binary.Write(&buf, binary.BigEndian, uint32(len(s)))
		buf.WriteString(s)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// AST walker
// ---------------------------------------------------------------------------

// Compile walks the AST rooted at `node` and appends emitted bytes
// to c.bytecode. Errors are accumulated rather than returned early so
// that one bad source construct doesn't hide other problems; the caller
// can still check c.Errors() once Compile has returned.
func (c *Compiler) Compile(node ast.Node) error {
	switch n := node.(type) {
	case *ast.Program:
		for _, s := range n.Statements {
			if err := c.Compile(s); err != nil {
				return err
			}
		}
		c.resolveJumps()
		return nil

	case *ast.AllocStmt:
		return c.emitAlloc(n)
	case *ast.FreeStmt:
		return c.emitFree(n)
	case *ast.MovStmt:
		return c.emitMov(n)
	case *ast.TriggerPinStmt:
		return c.emitTriggerPin(n)
	case *ast.SendChatStmt:
		return c.emitSendChat(n)
	case *ast.OnChatBlock:
		return c.emitOnChat(n)
	case *ast.ALUStmt:
		return c.emitALU(n)
	case *ast.LabelStmt:
		return c.emitLabel(n)
	case *ast.CmpStmt:
		return c.emitCmp(n)
	case *ast.JumpStmt:
		return c.emitJump(n)
	case *ast.CallStmt:
		return c.emitCall(n)
	case *ast.RetStmt:
		return c.emitRet(n)

	default:
		return fmt.Errorf("codegen: unsupported AST node %T", node)
	}
}

// ---------------------------------------------------------------------------
// Per-statement emitters
// ---------------------------------------------------------------------------

// emitAlloc: ALLOC <register>, <size> → OP_ALLOC [Reg][Size]
func (c *Compiler) emitAlloc(s *ast.AllocStmt) error {
	reg, err := encodeRegister(s.Register.Value)
	if err != nil {
		c.errors = append(c.errors, fmt.Errorf("ALLOC: %w", err))
		return nil
	}
	c.bytecode = append(c.bytecode, OP_ALLOC, reg)
	c.bytecode = binary.BigEndian.AppendUint32(c.bytecode, uint32(s.Size.Value))
	return nil
}

// emitFree: FREE <register> → OP_FREE [Reg]
func (c *Compiler) emitFree(s *ast.FreeStmt) error {
	reg, err := encodeRegister(s.Register.Value)
	if err != nil {
		c.errors = append(c.errors, fmt.Errorf("FREE: %w", err))
		return nil
	}
	c.bytecode = append(c.bytecode, OP_FREE, reg)
	return nil
}

// emitMov: MOV <register>, <value> → OP_MOV_REG_* with operand chosen
// by the runtime type of the AST expression.
func (c *Compiler) emitMov(s *ast.MovStmt) error {
	destReg, err := encodeRegister(s.Register.Value)
	if err != nil {
		c.errors = append(c.errors, fmt.Errorf("MOV: %w", err))
		return nil
	}
	switch v := s.Value.(type) {
	case *ast.RegisterLiteral:
		srcReg, err := encodeRegister(v.Value)
		if err != nil {
			c.errors = append(c.errors, fmt.Errorf("MOV src: %w", err))
			return nil
		}
		c.bytecode = append(c.bytecode, OP_MOV_REG_REG, destReg, srcReg)

	case *ast.IntegerLiteral:
		c.bytecode = append(c.bytecode, OP_MOV_REG_INT, destReg)
		c.bytecode = binary.BigEndian.AppendUint32(c.bytecode, uint32(int32(v.Value)))

	case *ast.StringLiteral:
		idx := c.addConstant(v.Value)
		c.bytecode = append(c.bytecode, OP_MOV_REG_STR, destReg)
		c.bytecode = binary.BigEndian.AppendUint32(c.bytecode, idx)

	default:
		c.errors = append(c.errors,
			fmt.Errorf("MOV: unsupported source expression %T", v))
		return nil
	}
	return nil
}

// emitTriggerPin: TRIGGER_PIN <pin>, <state> → OP_TRIGGER_PIN [Pin][State]
func (c *Compiler) emitTriggerPin(s *ast.TriggerPinStmt) error {
	c.bytecode = append(c.bytecode,
		OP_TRIGGER_PIN,
		byte(uint8(s.Pin.Value)),
		byte(uint8(s.State.Value)),
	)
	return nil
}

// emitSendChat: SEND_CHAT <string_or_register> → OP_SEND_CHAT [Operand]
//
// Operand is tagged: high bit cleared = register, high bit set = constant.
func (c *Compiler) emitSendChat(s *ast.SendChatStmt) error {
	var operand uint32
	switch v := s.Value.(type) {
	case *ast.StringLiteral:
		operand = encodeOperandAsConstant(c.addConstant(v.Value))
	case *ast.RegisterLiteral:
		reg, err := encodeOperandAsRegister(v.Value)
		if err != nil {
			c.errors = append(c.errors, fmt.Errorf("SEND_CHAT: %w", err))
			return nil
		}
		operand = reg
	default:
		c.errors = append(c.errors,
			fmt.Errorf("SEND_CHAT: unsupported value type %T", v))
		return nil
	}
	c.bytecode = append(c.bytecode, OP_SEND_CHAT)
	c.bytecode = binary.BigEndian.AppendUint32(c.bytecode, operand)
	return nil
}

// emitOnChat: ON_CHAT <trigger>, <user_var> <body...>
//
// Layout (14 bytes header + body bytes):
//   [OP_ON_CHAT] [triggerIdx:Uint32] [userReg:1]
//   [bodyStart:Uint32] [bodyLength:Uint32]
//
// `bodyStart` is the byte offset (within the code section, 0-indexed)
// of the first body instruction. `bodyLength` is the body size in bytes
// — used by the VM to skip the body during linear top-down execution.
//
// The header is emitted first with placeholder offsets, then the body,
// and finally the two offsets are patched in place. This avoids needing
// a separate relocation pass.
func (c *Compiler) emitOnChat(s *ast.OnChatBlock) error {
	triggerIdx := c.addConstant(s.Trigger.Value)

	userReg, err := encodeRegister(s.UserVar.Value)
	if err != nil {
		c.errors = append(c.errors, fmt.Errorf("ON_CHAT: %w", err))
		return nil
	}

	// Emit the 14-byte ON_CHAT header with placeholder offsets.
	c.bytecode = append(c.bytecode, OP_ON_CHAT)
	c.bytecode = binary.BigEndian.AppendUint32(c.bytecode, triggerIdx)
	c.bytecode = append(c.bytecode, userReg)
	bodyStartOffsetPos := len(c.bytecode)
	c.bytecode = binary.BigEndian.AppendUint32(c.bytecode, 0) // placeholder
	bodyLengthOffsetPos := len(c.bytecode)
	c.bytecode = binary.BigEndian.AppendUint32(c.bytecode, 0) // placeholder

	// Body starts here. Snapshot the offset so we can patch later.
	bodyStart := len(c.bytecode)

	for _, stmt := range s.Body {
		if err := c.Compile(stmt); err != nil {
			return err
		}
	}

	bodyLength := uint32(len(c.bytecode) - bodyStart)

	// Patch the two placeholder slots in place. We use big-endian to
	// stay consistent with the writer; offsets are absolute within the
	// code section, so they will be added to the header's
	// CodeSectionOffset by the VM loader.
	binary.BigEndian.PutUint32(c.bytecode[bodyStartOffsetPos:bodyStartOffsetPos+4], uint32(bodyStart))
	binary.BigEndian.PutUint32(c.bytecode[bodyLengthOffsetPos:bodyLengthOffsetPos+4], bodyLength)

	return nil
}

// emitALU: <OP> <DstReg>, <SrcReg> → [OP] [DstReg:1] [SrcReg:1]
func (c *Compiler) emitALU(s *ast.ALUStmt) error {
	var op byte
	switch s.Op {
	case lexer.TOKEN_ADD:
		op = OP_ADD
	case lexer.TOKEN_SUB:
		op = OP_SUB
	case lexer.TOKEN_MUL:
		op = OP_MUL
	case lexer.TOKEN_DIV:
		op = OP_DIV
	case lexer.TOKEN_AND:
		op = OP_AND
	case lexer.TOKEN_OR:
		op = OP_OR
	case lexer.TOKEN_XOR:
		op = OP_XOR
	case lexer.TOKEN_SHL:
		op = OP_SHL
	case lexer.TOKEN_SHR:
		op = OP_SHR
	default:
		c.errors = append(c.errors, fmt.Errorf("codegen: unsupported ALU op %s", s.Op))
		return nil
	}

	dst, err := encodeRegister(s.DstReg.Value)
	if err != nil {
		c.errors = append(c.errors, fmt.Errorf("%s dst: %w", s.Op, err))
		return nil
	}

	src, err := encodeRegister(s.SrcReg.Value)
	if err != nil {
		c.errors = append(c.errors, fmt.Errorf("%s src: %w", s.Op, err))
		return nil
	}

	c.bytecode = append(c.bytecode, op, dst, src)
	return nil
}

// emitLabel registers the current bytecode offset for the label name.
func (c *Compiler) emitLabel(s *ast.LabelStmt) error {
	if _, exists := c.labels[s.Name]; exists {
		c.errors = append(c.errors, fmt.Errorf("duplicate label %q", s.Name))
		return nil
	}
	c.labels[s.Name] = uint32(len(c.bytecode))
	return nil
}

// emitCmp: CMP <Reg1>, <Reg2> → [OP_CMP] [Reg1:1] [Reg2:1]
func (c *Compiler) emitCmp(s *ast.CmpStmt) error {
	reg1, err := encodeRegister(s.Reg1.Value)
	if err != nil {
		c.errors = append(c.errors, fmt.Errorf("CMP reg1: %w", err))
		return nil
	}
	reg2, err := encodeRegister(s.Reg2.Value)
	if err != nil {
		c.errors = append(c.errors, fmt.Errorf("CMP reg2: %w", err))
		return nil
	}
	c.bytecode = append(c.bytecode, OP_CMP, reg1, reg2)
	return nil
}

// emitJump: <JMP|JZ|JNZ> <label> → [OP] [TargetPC:Uint32 BE (placeholder)]
func (c *Compiler) emitJump(s *ast.JumpStmt) error {
	var op byte
	switch s.Op {
	case lexer.TOKEN_JMP:
		op = OP_JMP
	case lexer.TOKEN_JZ:
		op = OP_JZ
	case lexer.TOKEN_JNZ:
		op = OP_JNZ
	default:
		c.errors = append(c.errors, fmt.Errorf("codegen: unsupported jump op %s", s.Op))
		return nil
	}

	c.bytecode = append(c.bytecode, op)
	fixupOffset := len(c.bytecode)
	c.bytecode = binary.BigEndian.AppendUint32(c.bytecode, 0) // placeholder

	c.jumpFixups = append(c.jumpFixups, jumpFixup{
		offset:    fixupOffset,
		label:     s.Label,
		tokenType: s.Op,
	})
	return nil
}

// emitCall: CALL <label> → [OP_CALL] [TargetPC:Uint32 BE (placeholder)]
func (c *Compiler) emitCall(s *ast.CallStmt) error {
	c.bytecode = append(c.bytecode, OP_CALL)
	fixupOffset := len(c.bytecode)
	c.bytecode = binary.BigEndian.AppendUint32(c.bytecode, 0) // placeholder

	c.jumpFixups = append(c.jumpFixups, jumpFixup{
		offset:    fixupOffset,
		label:     s.Label,
		tokenType: lexer.TOKEN_CALL,
	})
	return nil
}

// emitRet: RET → [OP_RET]
func (c *Compiler) emitRet(s *ast.RetStmt) error {
	c.bytecode = append(c.bytecode, OP_RET)
	return nil
}

// resolveJumps patches the TargetPC for all emitted jumps.
func (c *Compiler) resolveJumps() {
	for _, fixup := range c.jumpFixups {
		targetOffset, ok := c.labels[fixup.label]
		if !ok {
			c.errors = append(c.errors, fmt.Errorf("%s: undefined label %q", fixup.tokenType, fixup.label))
			continue
		}
		binary.BigEndian.PutUint32(c.bytecode[fixup.offset:fixup.offset+4], targetOffset)
	}
}

// ---------------------------------------------------------------------------
// Constant pool
// ---------------------------------------------------------------------------

// addConstant returns the canonical pool index for `s`, registering it
// on first sight. Subsequent uses return the existing index, so the
// pool is deduplicated automatically.
func (c *Compiler) addConstant(s string) uint32 {
	if idx, ok := c.constMap[s]; ok {
		return idx
	}
	idx := uint32(len(c.constants))
	c.constants = append(c.constants, s)
	c.constMap[s] = idx
	return idx
}

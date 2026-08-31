// Package parser implements a hand-written recursive descent parser for
// flux-lang.
//
// The parser wraps a lexer.Lexer and tracks a one-token lookahead
// (`curToken` / `peekToken`). Errors are collected into a slice rather
// than returned early so callers can display every syntax issue in a
// single pass.
//
// The parser is intentionally small and explicit: every construct has a
// named parse function with no shared generic parsing machinery. This
// matches the language's surface area (six statement kinds, three
// expression kinds) and makes it easy to attach diagnostic context
// later.
package parser

import (
	"fmt"
	"strconv"

	"flux/compiler/ast"
	"flux/compiler/lexer"
)

// Parser owns the lexer and the lookahead cursor. The lexer is the only
// external dependency; the parser performs no other I/O.
type Parser struct {
	l         *lexer.Lexer
	curToken  lexer.Token
	peekToken lexer.Token
	errors    []string
}

// New constructs a Parser primed with two tokens of lookahead so the
// first statement dispatch can use expectPeek without special casing.
func New(l *lexer.Lexer) *Parser {
	p := &Parser{
		l:      l,
		errors: []string{},
	}
	// Two reads set curToken to the first token and peekToken to the
	// second token — exactly the state needed to dispatch on curToken
	// while validating peekToken.
	p.nextToken()
	p.nextToken()
	return p
}

// ---------------------------------------------------------------------------
// Cursor & error helpers
// ---------------------------------------------------------------------------

// nextToken advances the lookahead by one token.
func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.l.NextToken()
}

// Errors returns a copy of the parser's error slice so callers cannot
// mutate parser internals by accident.
func (p *Parser) Errors() []string {
	out := make([]string, len(p.errors))
	copy(out, p.errors)
	return out
}

// peekError appends a "expected X, got Y" diagnostic to the errors slice.
func (p *Parser) peekError(expected lexer.TokenType) {
	p.errors = append(p.errors, fmt.Sprintf(
		"expected next token to be %s, got %s instead (literal=%q)",
		expected, p.peekToken.Type, p.peekToken.Literal,
	))
}

// expectPeek validates the lookahead token and advances on success.
func (p *Parser) expectPeek(t lexer.TokenType) bool {
	if p.peekToken.Type == t {
		p.nextToken()
		return true
	}
	p.peekError(t)
	return false
}

// registerSet is an O(1) predicate table built at package init from the
// lexer's exported register list, so the parser and lexer cannot drift
// when new registers are added.
var registerSet = func() map[lexer.TokenType]bool {
	m := make(map[lexer.TokenType]bool, len(lexer.RegisterTokenTypes()))
	for _, t := range lexer.RegisterTokenTypes() {
		m[t] = true
	}
	return m
}()

// isRegisterTokenType is the canonical "is this a register?" predicate.
// It delegates to the lexer's exported register list via registerSet,
// so adding a new register to the lexer automatically becomes valid
// here as well.
func isRegisterTokenType(t lexer.TokenType) bool {
	return registerSet[t]
}

// expectRegister uses isRegisterTokenType to validate the lookahead and
// advance. It records a custom diagnostic message that names the
// expected register range rather than a single token type.
func (p *Parser) expectRegister() bool {
	if isRegisterTokenType(p.peekToken.Type) {
		p.nextToken()
		return true
	}
	msg := fmt.Sprintf(
		"expected next token to be a register (R1..R16), got %s instead (literal=%q)",
		p.peekToken.Type, p.peekToken.Literal,
	)
	p.errors = append(p.errors, msg)
	return false
}

// isBlockStarter reports whether the token opens a new top-level
// statement. An ON_CHAT body terminates as soon as a block-starter or
// EOF appears in the lookahead, which gives the brace-less language a
// predictable scope boundary.
func isBlockStarter(t lexer.TokenType) bool {
	switch t {
	case lexer.TOKEN_ALLOC, lexer.TOKEN_FREE, lexer.TOKEN_MOV, lexer.TOKEN_ON_CHAT:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Top-level entry point
// ---------------------------------------------------------------------------

// ParseProgram consumes tokens until EOF and assembles a Program AST.
// Errors are accumulated rather than fatal — callers can still inspect a
// partially-built program after breaking on a syntax error.
func (p *Parser) ParseProgram() *ast.Program {
	program := &ast.Program{Statements: []ast.Statement{}}

	for p.curToken.Type != lexer.TOKEN_EOF {
		stmt := p.parseStatement()
		if stmt != nil {
			program.Statements = append(program.Statements, stmt)
		}
		p.nextToken()
	}
	return program
}

// parseStatement dispatches on the current token's type.
//
// Each case returns either a fully-populated *ast.Statement or nil if it
// recorded one or more errors. Returning nil (instead of panicking) lets
// the outer ParseProgram loop keep collecting additional top-level
// statements even after the first failure.
//
// Each branch uses a typed local so a child returning a nil concrete
// pointer is caught at the concrete-type level — Go's "typed-nil
// interface" gotcha would otherwise let a nil *ast.AllocStmt sneak past
// `if stmt != nil` in the outer loop and panic later when Program.Dump
// tries to dereference its fields.
func (p *Parser) parseStatement() ast.Statement {
	switch p.curToken.Type {
	case lexer.TOKEN_ALLOC:
		if s := p.parseAllocStmt(); s != nil {
			return s
		}
	case lexer.TOKEN_FREE:
		if s := p.parseFreeStmt(); s != nil {
			return s
		}
	case lexer.TOKEN_MOV:
		if s := p.parseMovStmt(); s != nil {
			return s
		}
	case lexer.TOKEN_TRIGGER_PIN:
		if s := p.parseTriggerPinStmt(); s != nil {
			return s
		}
	case lexer.TOKEN_SEND_CHAT:
		if s := p.parseSendChatStmt(); s != nil {
			return s
		}
	case lexer.TOKEN_ON_CHAT:
		if s := p.parseOnChatBlock(); s != nil {
			return s
		}
	case lexer.TOKEN_ADD, lexer.TOKEN_SUB, lexer.TOKEN_MUL, lexer.TOKEN_DIV,
		lexer.TOKEN_AND, lexer.TOKEN_OR, lexer.TOKEN_XOR, lexer.TOKEN_SHL, lexer.TOKEN_SHR:
		if s := p.parseALUStmt(); s != nil {
			return s
		}
	case lexer.TOKEN_CMP:
		if s := p.parseCmpStmt(); s != nil {
			return s
		}
	case lexer.TOKEN_JMP, lexer.TOKEN_JZ, lexer.TOKEN_JNZ:
		if s := p.parseJumpStmt(); s != nil {
			return s
		}
	case lexer.TOKEN_CALL:
		if s := p.parseCallStmt(); s != nil {
			return s
		}
	case lexer.TOKEN_RET:
		if s := p.parseRetStmt(); s != nil {
			return s
		}
	case lexer.TOKEN_IDENT:
		if p.peekToken.Type == lexer.TOKEN_COLON {
			if s := p.parseLabelStmt(); s != nil {
				return s
			}
		} else {
			p.errors = append(p.errors, fmt.Sprintf(
				"unexpected identifier %q at statement position (did you mean to define a label with ':'?)",
				p.curToken.Literal,
			))
		}
	default:
		p.errors = append(p.errors, fmt.Sprintf(
			"unexpected token %s (literal=%q) at statement position",
			p.curToken.Type, p.curToken.Literal,
		))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Per-statement parsers
// ---------------------------------------------------------------------------

// parseAllocStmt: ALLOC <register>, <integer>
func (p *Parser) parseAllocStmt() *ast.AllocStmt {
	stmt := &ast.AllocStmt{Token: p.curToken}

	if !p.expectRegister() {
		return nil
	}
	stmt.Register = p.parseRegisterLiteral()

	if !p.expectPeek(lexer.TOKEN_COMMA) {
		return nil
	}
	if !p.expectPeek(lexer.TOKEN_INT) {
		return nil
	}
	size := p.parseIntegerLiteral()
	if size == nil {
		return nil
	}
	stmt.Size = size
	return stmt
}

// parseFreeStmt: FREE <register>
func (p *Parser) parseFreeStmt() *ast.FreeStmt {
	stmt := &ast.FreeStmt{Token: p.curToken}

	if !p.expectRegister() {
		return nil
	}
	stmt.Register = p.parseRegisterLiteral()
	return stmt
}

// parseMovStmt: MOV <register>, <expression>
//
// The expression can be any of the three flux expression kinds — register,
// integer literal, or string literal. Semantic validation of MOV (e.g.
// "cannot MOV a string into an integer register") is delegated to the VM.
func (p *Parser) parseMovStmt() *ast.MovStmt {
	stmt := &ast.MovStmt{Token: p.curToken}

	if !p.expectRegister() {
		return nil
	}
	stmt.Register = p.parseRegisterLiteral()

	if !p.expectPeek(lexer.TOKEN_COMMA) {
		return nil
	}

	// Move past the comma onto the value expression.
	p.nextToken()
	expr := p.parseExpression()
	if expr == nil {
		return nil
	}
	stmt.Value = expr
	return stmt
}

// parseTriggerPinStmt: TRIGGER_PIN <integer>, <integer>
func (p *Parser) parseTriggerPinStmt() *ast.TriggerPinStmt {
	stmt := &ast.TriggerPinStmt{Token: p.curToken}

	if !p.expectPeek(lexer.TOKEN_INT) {
		return nil
	}
	pin := p.parseIntegerLiteral()
	if pin == nil {
		return nil
	}
	stmt.Pin = pin

	if !p.expectPeek(lexer.TOKEN_COMMA) {
		return nil
	}
	if !p.expectPeek(lexer.TOKEN_INT) {
		return nil
	}
	state := p.parseIntegerLiteral()
	if state == nil {
		return nil
	}
	stmt.State = state
	return stmt
}

// parseSendChatStmt: SEND_CHAT <string_or_register>
//
// Integer operands are rejected here. This mirrors the IRC protocol: only
// printable strings (or a register that resolves to a printable string)
// make sense as chat output.
func (p *Parser) parseSendChatStmt() *ast.SendChatStmt {
	stmt := &ast.SendChatStmt{Token: p.curToken}

	// Advance past SEND_CHAT to inspect the operand directly.
	p.nextToken()

	if p.curToken.Type == lexer.TOKEN_STRING {
		stmt.Value = p.parseStringLiteral()
		return stmt
	}
	if isRegisterTokenType(p.curToken.Type) {
		stmt.Value = p.parseRegisterLiteral()
		return stmt
	}

	p.errors = append(p.errors, fmt.Sprintf(
		"SEND_CHAT expects a string literal or a register, got %s (literal=%q)",
		p.curToken.Type, p.curToken.Literal,
	))
	return nil
}

// parseOnChatBlock: ON_CHAT <string>, <register> { <body> }
//
// Block termination: the body is a sequence of statements that ends
// when the lookahead is either EOF or one of the block-starter tokens
// (ALLOC, FREE, MOV, ON_CHAT). This convention gives the brace-less
// language deterministic scoping without needing a NEWLINE or INDENT
// token in the lexer.
func (p *Parser) parseOnChatBlock() *ast.OnChatBlock {
	stmt := &ast.OnChatBlock{
		Token: p.curToken,
		Body:  []ast.Statement{},
	}

	if !p.expectPeek(lexer.TOKEN_STRING) {
		return nil
	}
	stmt.Trigger = p.parseStringLiteral()

	if !p.expectPeek(lexer.TOKEN_COMMA) {
		return nil
	}
	if !p.expectRegister() {
		return nil
	}
	stmt.UserVar = p.parseRegisterLiteral()

	// Consume body statements until terminator. Each iteration consumes
	// exactly one statement; curToken ends on the statement's last
	// token and peekToken holds the terminator or EOF.
	for p.peekToken.Type != lexer.TOKEN_EOF && !isBlockStarter(p.peekToken.Type) {
		p.nextToken()
		bodyStmt := p.parseStatement()
		if bodyStmt != nil {
			stmt.Body = append(stmt.Body, bodyStmt)
		}
	}

	return stmt
}

// parseALUStmt: <OP> <DstReg>, <SrcReg>
func (p *Parser) parseALUStmt() *ast.ALUStmt {
	stmt := &ast.ALUStmt{
		Token: p.curToken,
		Op:    p.curToken.Type,
	}

	if !p.expectRegister() {
		return nil
	}
	stmt.DstReg = p.parseRegisterLiteral()

	if !p.expectPeek(lexer.TOKEN_COMMA) {
		return nil
	}

	if !p.expectRegister() {
		return nil
	}
	stmt.SrcReg = p.parseRegisterLiteral()

	return stmt
}

// parseCmpStmt: CMP <Reg1>, <Reg2>
func (p *Parser) parseCmpStmt() *ast.CmpStmt {
	stmt := &ast.CmpStmt{Token: p.curToken}

	if !p.expectRegister() {
		return nil
	}
	stmt.Reg1 = p.parseRegisterLiteral()

	if !p.expectPeek(lexer.TOKEN_COMMA) {
		return nil
	}

	if !p.expectRegister() {
		return nil
	}
	stmt.Reg2 = p.parseRegisterLiteral()

	return stmt
}

// parseJumpStmt: <JMP|JZ|JNZ> <label>
func (p *Parser) parseJumpStmt() *ast.JumpStmt {
	stmt := &ast.JumpStmt{
		Token: p.curToken,
		Op:    p.curToken.Type,
	}

	if !p.expectPeek(lexer.TOKEN_IDENT) {
		return nil
	}
	stmt.Label = p.curToken.Literal

	return stmt
}

// parseLabelStmt: <ident>:
func (p *Parser) parseLabelStmt() *ast.LabelStmt {
	stmt := &ast.LabelStmt{
		Token: p.curToken,
		Name:  p.curToken.Literal,
	}
	// curToken is the IDENT; advance past the COLON
	p.nextToken()
	return stmt
}

// parseCallStmt: CALL <label>
func (p *Parser) parseCallStmt() *ast.CallStmt {
	stmt := &ast.CallStmt{Token: p.curToken}
	if !p.expectPeek(lexer.TOKEN_IDENT) {
		return nil
	}
	stmt.Label = p.curToken.Literal
	return stmt
}

// parseRetStmt: RET
func (p *Parser) parseRetStmt() *ast.RetStmt {
	return &ast.RetStmt{Token: p.curToken}
}

// ---------------------------------------------------------------------------
// Expression / leaf parsers
// ---------------------------------------------------------------------------

// parseExpression dispatches on the current token. It supports the three
// flux expression kinds; anything else records an error.
//
// parseIntegerLiteral can return a nil pointer when the literal overflows
// int64, so the integer branch uses a typed local to avoid the
// typed-nil-interface gotcha on the way out.
func (p *Parser) parseExpression() ast.Expression {
	switch p.curToken.Type {
	case lexer.TOKEN_STRING:
		return p.parseStringLiteral()
	case lexer.TOKEN_INT:
		if i := p.parseIntegerLiteral(); i != nil {
			return i
		}
	default:
		if isRegisterTokenType(p.curToken.Type) {
			return p.parseRegisterLiteral()
		}
	}

	p.errors = append(p.errors, fmt.Sprintf(
		"no prefix parse function for expression starting with %s (literal=%q)",
		p.curToken.Type, p.curToken.Literal,
	))
	return nil
}

func (p *Parser) parseRegisterLiteral() *ast.RegisterLiteral {
	return &ast.RegisterLiteral{Token: p.curToken, Value: p.curToken.Literal}
}

func (p *Parser) parseStringLiteral() *ast.StringLiteral {
	return &ast.StringLiteral{Token: p.curToken, Value: p.curToken.Literal}
}

// parseIntegerLiteral parses the integer literal represented by curToken
// and stores it as an int64. Overflow or other ParseInt failures are
// recorded as parser errors so callers see one diagnostic per problem
// rather than a single panic.
func (p *Parser) parseIntegerLiteral() *ast.IntegerLiteral {
	lit := &ast.IntegerLiteral{Token: p.curToken}

	v, err := strconv.ParseInt(p.curToken.Literal, 10, 64)
	if err != nil {
		p.errors = append(p.errors, fmt.Sprintf(
			"could not parse %q as int64: %v",
			p.curToken.Literal, err,
		))
		return nil
	}

	lit.Value = v
	return lit
}

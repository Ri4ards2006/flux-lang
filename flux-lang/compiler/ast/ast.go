// Package ast defines the Abstract Syntax Tree for the flux language.
//
// Every node in the tree implements the Node interface, which exposes the
// literal of the token that introduced the node. Statements and
// expressions are tagged with private marker methods so the parser can
// distinguish them at compile time — accidentally passing a Statement
// where an Expression is expected becomes a type error instead of a
// silent runtime mistake.
//
// The package's only stdlib dependency is `strings` (for the renderer)
// and `fmt`; everything else is pure data structures built around the
// lexer token types.
package ast

import (
	"fmt"
	"strings"

	"flux/compiler/lexer"
)

// ----------------------------------------------------------------------------
// Interface hierarchy
// ----------------------------------------------------------------------------

// Node is the universal AST-node interface. TokenLiteral returns the raw
// source literal of the token that opened the construct, so round-tripping
// a position is cheap.
type Node interface {
	TokenLiteral() string
}

// Statement mixes Node with a private marker. Concrete statements satisfy
// this interface; concrete expressions do not.
type Statement interface {
	Node
	statementNode()
}

// Expression mixes Node with a private marker. Concrete expressions
// satisfy this interface; concrete statements do not.
type Expression interface {
	Node
	expressionNode()
}

// ----------------------------------------------------------------------------
// Root
// ----------------------------------------------------------------------------

// Program is the root of every flux AST. Statements are stored in source
// order; OnChatBlock carries its own nested statement slice for the
// event-handler body.
type Program struct {
	Statements []Statement
}

// TokenLiteral reports the literal of the first statement, or "" for an
// empty program — useful for error messages that point at the first bad
// token.
func (p *Program) TokenLiteral() string {
	if len(p.Statements) > 0 {
		return p.Statements[0].TokenLiteral()
	}
	return ""
}

// Dump renders the program as an indented, human-readable tree. The
// format includes node-type names so the tree is unambiguous when read
// out of context (e.g. in test failure messages).
func (p *Program) Dump() string {
	var b strings.Builder
	b.WriteString("Program\n")
	for _, s := range p.Statements {
		writeStatement(&b, s, "  ")
	}
	return b.String()
}

// writeStatement is the recursion helper used by Program.Dump. It uses a
// type switch so adding a new statement kind requires a single new case
// and nothing else.
func writeStatement(b *strings.Builder, s Statement, indent string) {
	switch v := s.(type) {
	case *AllocStmt:
		fmt.Fprintf(b, "%sAllocStmt { register=%s, size=%s }\n",
			indent, v.Register.Value, v.Size.TokenLiteral())

	case *FreeStmt:
		fmt.Fprintf(b, "%sFreeStmt { register=%s }\n",
			indent, v.Register.Value)

	case *MovStmt:
		fmt.Fprintf(b, "%sMovStmt { register=%s, value=%s }\n",
			indent, v.Register.Value, fmtExpression(v.Value))

	case *TriggerPinStmt:
		fmt.Fprintf(b, "%sTriggerPinStmt { pin=%s, state=%s }\n",
			indent, v.Pin.TokenLiteral(), v.State.TokenLiteral())

	case *SendChatStmt:
		fmt.Fprintf(b, "%sSendChatStmt { value=%s }\n",
			indent, fmtExpression(v.Value))

	case *OnChatBlock:
		fmt.Fprintf(b, "%sOnChatBlock { trigger=%s, userVar=%s }\n",
			indent, fmtExpression(v.Trigger), v.UserVar.Value)
		for _, child := range v.Body {
			writeStatement(b, child, indent+"  ")
		}
	}
}

// fmtExpression renders an Expression for human-facing output. String
// literals are wrapped in their source quotes so the rendered tree is
// unambiguous; everything else uses the bare token literal.
func fmtExpression(e Expression) string {
	if s, ok := e.(*StringLiteral); ok {
		return `"` + s.Value + `"`
	}
	return e.TokenLiteral()
}

// ----------------------------------------------------------------------------
// Statement nodes
// ----------------------------------------------------------------------------

// AllocStmt models `ALLOC <register>, <integer>`.
//
// Example: ALLOC R1, 32 — allocate 32 cells in register R1.
type AllocStmt struct {
	Token    lexer.Token
	Register *RegisterLiteral
	Size     *IntegerLiteral
}

func (s *AllocStmt) statementNode()   {}
func (s *AllocStmt) TokenLiteral() string { return s.Token.Literal }

// FreeStmt models `FREE <register>`. After a FREE the contents of the
// register are undefined.
type FreeStmt struct {
	Token    lexer.Token
	Register *RegisterLiteral
}

func (s *FreeStmt) statementNode()   {}
func (s *FreeStmt) TokenLiteral() string { return s.Token.Literal }

// MovStmt models `MOV <register>, <expression>`.
//
// The right-hand side may be a register, an integer literal, or a string
// literal; the parser does not enforce a particular kind here because
// the VM is the layer that decides whether the move is type-legal at
// execution time.
type MovStmt struct {
	Token    lexer.Token
	Register *RegisterLiteral
	Value    Expression
}

func (s *MovStmt) statementNode()   {}
func (s *MovStmt) TokenLiteral() string { return s.Token.Literal }

// TriggerPinStmt models `TRIGGER_PIN <pin>, <state>`.
// Both operands are integer literals; pin is non-negative and state is
// either 0 or 1.
type TriggerPinStmt struct {
	Token lexer.Token
	Pin   *IntegerLiteral
	State *IntegerLiteral
}

func (s *TriggerPinStmt) statementNode()   {}
func (s *TriggerPinStmt) TokenLiteral() string { return s.Token.Literal }

// SendChatStmt models `SEND_CHAT <expression>`. The expression is either
// a string literal or a register that holds a string. Integer operands
// are rejected at the parser level (see parser.parseSendChatStmt).
type SendChatStmt struct {
	Token lexer.Token
	Value Expression
}

func (s *SendChatStmt) statementNode()   {}
func (s *SendChatStmt) TokenLiteral() string { return s.Token.Literal }

// OnChatBlock models `ON_CHAT <trigger>, <user_var> ...body...`. It is
// the only event-driven construct in flux.
//
// The body holds zero or more nested Statements that the VM executes
// whenever a chat message matches the trigger string.
//
// Block termination is decided by the parser using a "block-starter"
// heuristic (see parser.go): the body extends until the parser sees
// another ALLOC/FREE/MOV/ON_CHAT or end-of-input.
type OnChatBlock struct {
	Token   lexer.Token
	Trigger *StringLiteral
	UserVar *RegisterLiteral
	Body    []Statement
}

func (s *OnChatBlock) statementNode()   {}
func (s *OnChatBlock) TokenLiteral() string { return s.Token.Literal }

// ----------------------------------------------------------------------------
// Operand / Expression nodes
// ----------------------------------------------------------------------------

// RegisterLiteral names one of the 16 flux registers (R1..R16).
type RegisterLiteral struct {
	Token lexer.Token
	Value string
}

func (r *RegisterLiteral) expressionNode()  {}
func (r *RegisterLiteral) TokenLiteral() string { return r.Token.Literal }

// IntegerLiteral is a parsed integer constant.
type IntegerLiteral struct {
	Token lexer.Token
	Value int64
}

func (i *IntegerLiteral) expressionNode()   {}
func (i *IntegerLiteral) TokenLiteral() string { return i.Token.Literal }

// StringLiteral is a parsed string constant. Its Value is the interior of
// the source literal — surrounding quotes are not retained.
type StringLiteral struct {
	Token lexer.Token
	Value string
}

func (s *StringLiteral) expressionNode()    {}
func (s *StringLiteral) TokenLiteral() string { return s.Token.Literal }

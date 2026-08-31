// Package lexer implements the lexical analyser for the flux language.
//
// The lexer is intentionally byte-oriented and zero-allocation on the hot
// path: identifiers, integers and strings are returned as sub-slices of the
// original input buffer rather than as freshly-allocated strings. Reserved
// words (keywords and register names) are disambiguated by O(1) map
// lookups, so `NextToken` performs no string compares for keywords.
//
// Source text is assumed to be ASCII — every reserved word and digit literal
// in flux is ASCII — so operating on bytes instead of runes keeps the hot
// path tight. Non-ASCII bytes encountered inside string literals are
// preserved verbatim as part of the slice.
package lexer

import (
	"fmt"
)

// -----------------------------------------------------------------------------
// Token types
// -----------------------------------------------------------------------------

// TokenType enumerates every kind of lexical token recognised by flux.
// Each reserved word has its own TokenType so downstream stages can switch on
// the type without ever inspecting the literal string for a keyword match.
type TokenType string

const (
	// Memory management keywords.
	TOKEN_ALLOC TokenType = "ALLOC"
	TOKEN_FREE  TokenType = "FREE"
	TOKEN_MOV   TokenType = "MOV"

	// Twitch socket keywords.
	TOKEN_ON_CHAT   TokenType = "ON_CHAT"
	TOKEN_SEND_CHAT TokenType = "SEND_CHAT"

	// Hardware / IoT socket keywords.
	TOKEN_TRIGGER_PIN TokenType = "TRIGGER_PIN"

	// Arithmetic & bitwise ALU keywords.
	TOKEN_ADD TokenType = "ADD"
	TOKEN_SUB TokenType = "SUB"
	TOKEN_MUL TokenType = "MUL"
	TOKEN_DIV TokenType = "DIV"
	TOKEN_AND TokenType = "AND"
	TOKEN_OR  TokenType = "OR"
	TOKEN_XOR TokenType = "XOR"
	TOKEN_SHL TokenType = "SHL"
	TOKEN_SHR TokenType = "SHR"

	// Control flow & branching keywords.
	TOKEN_CMP  TokenType = "CMP"
	TOKEN_JMP  TokenType = "JMP"
	TOKEN_JZ   TokenType = "JZ"
	TOKEN_JNZ  TokenType = "JNZ"
	TOKEN_CALL TokenType = "CALL"
	TOKEN_RET  TokenType = "RET"

	// Registers R1..R16. Declared explicitly so the test fixture can name
	// each one without resorting to string-typed assertions.
	TOKEN_R1  TokenType = "R1"
	TOKEN_R2  TokenType = "R2"
	TOKEN_R3  TokenType = "R3"
	TOKEN_R4  TokenType = "R4"
	TOKEN_R5  TokenType = "R5"
	TOKEN_R6  TokenType = "R6"
	TOKEN_R7  TokenType = "R7"
	TOKEN_R8  TokenType = "R8"
	TOKEN_R9  TokenType = "R9"
	TOKEN_R10 TokenType = "R10"
	TOKEN_R11 TokenType = "R11"
	TOKEN_R12 TokenType = "R12"
	TOKEN_R13 TokenType = "R13"
	TOKEN_R14 TokenType = "R14"
	TOKEN_R15 TokenType = "R15"
	TOKEN_R16 TokenType = "R16"

	// Generic literals & punctuation.
	TOKEN_IDENT  TokenType = "IDENT"
	TOKEN_INT    TokenType = "INT"
	TOKEN_STRING TokenType = "STRING"
	TOKEN_COMMA  TokenType = ","
	TOKEN_COLON  TokenType = ":"
	TOKEN_EOF    TokenType = "EOF"

	// Anything flux does not understand.
	TOKEN_ILLEGAL TokenType = "ILLEGAL"
)

// Token is the strongly-typed result of one call to Lexer.NextToken.
//
// Literal is a sub-slice of the source buffer — the caller MUST treat it as
// borrowed memory and not modify or retain it past the next call to
// NextToken. The Type field is always populated.
type Token struct {
	Type    TokenType
	Literal string
}

// String renders a Token for debug printing. It is not used on the lexer hot
// path.
func (t Token) String() string {
	if t.Literal == "" {
		return string(t.Type)
	}
	return fmt.Sprintf("%s(%q)", t.Type, t.Literal)
}

// -----------------------------------------------------------------------------
// Reserved-word tables (built once at package init; never mutated).
// -----------------------------------------------------------------------------

// keywords maps each reserved word to its strongly-typed TokenType.
// Lookup is O(1) and performs no allocation per call.
var keywords = map[string]TokenType{
	"ALLOC":       TOKEN_ALLOC,
	"FREE":        TOKEN_FREE,
	"MOV":         TOKEN_MOV,
	"ON_CHAT":     TOKEN_ON_CHAT,
	"SEND_CHAT":   TOKEN_SEND_CHAT,
	"TRIGGER_PIN": TOKEN_TRIGGER_PIN,
	"ADD":         TOKEN_ADD,
	"SUB":         TOKEN_SUB,
	"MUL":         TOKEN_MUL,
	"DIV":         TOKEN_DIV,
	"AND":         TOKEN_AND,
	"OR":          TOKEN_OR,
	"XOR":         TOKEN_XOR,
	"SHL":         TOKEN_SHL,
	"SHR":         TOKEN_SHR,
	"CMP":         TOKEN_CMP,
	"JMP":         TOKEN_JMP,
	"JZ":          TOKEN_JZ,
	"JNZ":         TOKEN_JNZ,
	"CALL":        TOKEN_CALL,
	"RET":         TOKEN_RET,
}

// registerTokens is the single source of truth for every register name the
// lexer understands. The const block above and the registers map below are
// both derived from this slice, so adding R17 only requires declaring one
// new TOKEN_R17 constant and appending it here — the map keys cannot drift
// from the TokenType names.
var registerTokens = []TokenType{
	TOKEN_R1, TOKEN_R2, TOKEN_R3, TOKEN_R4,
	TOKEN_R5, TOKEN_R6, TOKEN_R7, TOKEN_R8,
	TOKEN_R9, TOKEN_R10, TOKEN_R11, TOKEN_R12,
	TOKEN_R13, TOKEN_R14, TOKEN_R15, TOKEN_R16,
}

// registers maps each register's textual name (e.g. "R10") to its
// strongly-typed TokenType. Keys are derived from string(TOKEN_Rn), so
// they are guaranteed to stay in lock-step with the constants above.
var registers = func() map[string]TokenType {
	m := make(map[string]TokenType, len(registerTokens))
	for _, tok := range registerTokens {
		m[string(tok)] = tok
	}
	return m
}()

// RegisterTokenTypes returns the canonical list of register TokenTypes
// (R1..R16, in order). Downstream packages — notably the parser — use
// this so the lexer and parser agree on what counts as a register
// without duplicating the list in two places.
//
// Returning the underlying slice directly is safe because every consumer
// iterates without mutating; if that assumption ever changes we should
// return a copy here.
func RegisterTokenTypes() []TokenType {
	return registerTokens
}

// LookupIdent classifies a raw identifier into a strongly-typed TokenType.
// Order of checks matters: registers (most frequent in real code) go first,
// then keywords, otherwise the identifier is reported as TOKEN_IDENT.
func LookupIdent(ident string) TokenType {
	if tok, ok := registers[ident]; ok {
		return tok
	}
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return TOKEN_IDENT
}

// -----------------------------------------------------------------------------
// Lexer
// -----------------------------------------------------------------------------

// Lexer converts a flux source string into a stream of Tokens.
//
// All four fields are private to keep cursor state encapsulated. The lexer
// is *not* safe for concurrent use; instantiate one per goroutine.
type Lexer struct {
	input        string
	position     int // byte index of the current character
	readPosition int // byte index of the next character (one past current)
	ch           byte
}

// New constructs a Lexer bound to the given source and primes the cursor.
func New(input string) *Lexer {
	l := &Lexer{input: input}
	l.readChar()
	return l
}

// readChar advances the cursor by exactly one byte.
func (l *Lexer) readChar() {
	if l.readPosition >= len(l.input) {
		l.ch = 0
	} else {
		l.ch = l.input[l.readPosition]
	}
	l.position = l.readPosition
	l.readPosition++
}

// skipWhitespace consumes ASCII spaces, tabs, carriage returns and newlines.
// Keeping the check as a tight, branchless-ish byte compare is faster than a
// unicode.IsSpace call and avoids any rune decoding.
func (l *Lexer) skipWhitespace() {
	for l.ch == ' ' || l.ch == '\t' || l.ch == '\n' || l.ch == '\r' {
		l.readChar()
	}
}

// NextToken is the public entrypoint. Consumers loop:
//
//	for tok := l.NextToken(); tok.Type != lexer.TOKEN_EOF; tok = l.NextToken() {
//	    ...
//	}
//
// Every Literal returned is a sub-slice of the original source buffer and
// MUST NOT be mutated.
func (l *Lexer) NextToken() Token {
	l.skipWhitespace()

	switch l.ch {
	case '"':
		tok := Token{
			Type:    TOKEN_STRING,
			Literal: l.readString(),
		}
		return tok

	case ',':
		tok := Token{
			Type:    TOKEN_COMMA,
			Literal: l.input[l.position : l.position+1],
		}
		l.readChar()
		return tok

	case ':':
		tok := Token{
			Type:    TOKEN_COLON,
			Literal: l.input[l.position : l.position+1],
		}
		l.readChar()
		return tok

	case 0:
		// Sentinel value produced by readChar when the cursor has walked
		// past the end of the source.
		return Token{Type: TOKEN_EOF}
	}

	switch {
	case isDigit(l.ch):
		return Token{
			Type:    TOKEN_INT,
			Literal: l.readNumber(),
		}

	case isLetter(l.ch):
		// readIdentifier returns a sub-slice; LookupIdent is the only
		// allocation-free classification step.
		lit := l.readIdentifier()
		return Token{
			Type:    LookupIdent(lit),
			Literal: lit,
		}
	}

	// Anything else is reported as TOKEN_ILLEGAL. The illegal byte is
	// captured by slicing the source so callers can show the offending
	// character without forcing the lexer to allocate.
	tok := Token{
		Type:    TOKEN_ILLEGAL,
		Literal: l.input[l.position : l.position+1],
	}
	l.readChar()
	return tok
}

// readIdentifier consumes [A-Za-z_][A-Za-z0-9_]* starting at the current
// cursor position and returns a sub-slice of the source buffer (no copy).
func (l *Lexer) readIdentifier() string {
	start := l.position
	for isLetter(l.ch) || isDigit(l.ch) {
		l.readChar()
	}
	return l.input[start:l.position]
}

// readNumber consumes [0-9]+ starting at the current cursor position. The
// returned slice borrows from the source buffer; no allocation occurs.
func (l *Lexer) readNumber() string {
	start := l.position
	for isDigit(l.ch) {
		l.readChar()
	}
	return l.input[start:l.position]
}

// readString consumes bytes up to (but not including) the closing ASCII
// double-quote, then advances past it. The returned slice borrows the
// interior of the source buffer; surrounding quote characters are stripped.
func (l *Lexer) readString() string {
	// `position` currently points at the opening quote.
	pos := l.position + 1

	for {
		l.readChar()
		if l.ch == '"' || l.ch == 0 {
			break
		}
	}

	literal := l.input[pos:l.position]
	if l.ch == '"' {
		// Consume the closing quote so the next NextToken call starts
		// past it.
		l.readChar()
	}
	return literal
}

// -----------------------------------------------------------------------------
// Byte predicates
// -----------------------------------------------------------------------------

func isLetter(ch byte) bool {
	return ('a' <= ch && ch <= 'z') || ('A' <= ch && ch <= 'Z') || ch == '_'
}

func isDigit(ch byte) bool {
	return '0' <= ch && ch <= '9'
}

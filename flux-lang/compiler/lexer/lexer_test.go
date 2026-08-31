package lexer

import (
	"testing"
)

// TestNextToken feeds the canonical flux assembly fixture through the lexer
// and asserts that every emitted token matches the expected (Type, Literal)
// pair byte-for-byte. The fixture intentionally mixes every token category
// (memory ops, Twitch sockets, GPIO, registers, integers, strings, commas,
// and EOF) so a regression in any branch of NextToken will surface here.
func TestNextToken(t *testing.T) {
	input := `ALLOC R1, 32
MOV R1, "Richard"
ON_CHAT "!hype", R1
    SEND_CHAT "Hype train activated!"
    TRIGGER_PIN 18, 1
FREE R1
`

	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		// ALLOC R1, 32
		{TOKEN_ALLOC, "ALLOC"},
		{TOKEN_R1, "R1"},
		{TOKEN_COMMA, ","},
		{TOKEN_INT, "32"},

		// MOV R1, "Richard"
		{TOKEN_MOV, "MOV"},
		{TOKEN_R1, "R1"},
		{TOKEN_COMMA, ","},
		{TOKEN_STRING, "Richard"},

		// ON_CHAT "!hype", R1
		{TOKEN_ON_CHAT, "ON_CHAT"},
		{TOKEN_STRING, "!hype"},
		{TOKEN_COMMA, ","},
		{TOKEN_R1, "R1"},

		// SEND_CHAT "Hype train activated!"
		{TOKEN_SEND_CHAT, "SEND_CHAT"},
		{TOKEN_STRING, "Hype train activated!"},

		// TRIGGER_PIN 18, 1
		{TOKEN_TRIGGER_PIN, "TRIGGER_PIN"},
		{TOKEN_INT, "18"},
		{TOKEN_COMMA, ","},
		{TOKEN_INT, "1"},

		// FREE R1
		{TOKEN_FREE, "FREE"},
		{TOKEN_R1, "R1"},

		// End of input.
		{TOKEN_EOF, ""},
	}

	l := New(input)

	for i, tt := range tests {
		tok := l.NextToken()

		if tok.Type != tt.expectedType {
			t.Fatalf(
				"tests[%d] - wrong token type. expected=%q, got=%q (literal=%q)",
				i, tt.expectedType, tok.Type, tok.Literal,
			)
		}

		if tok.Literal != tt.expectedLiteral {
			t.Fatalf(
				"tests[%d] - wrong literal. expected=%q, got=%q (type=%q)",
				i, tt.expectedLiteral, tok.Literal, tok.Type,
			)
		}
	}
}

// TestEOFAfterInput verifies the lexer reports EXACTLY ONE TOKEN_EOF per
// invocation of NextToken after the source is exhausted. A common bug in
// hand-rolled lexers is to keep emitting EOF tokens forever; this test
// guards against that.
func TestEOFAfterInput(t *testing.T) {
	l := New("")
	got := l.NextToken()
	if got.Type != TOKEN_EOF {
		t.Fatalf("first NextToken on empty input should be EOF, got %q", got.Type)
	}
	for i := 0; i < 3; i++ {
		got = l.NextToken()
		if got.Type != TOKEN_EOF {
			t.Fatalf("subsequent NextToken #%d should still be EOF, got %q", i, got.Type)
		}
	}
}

// TestWhitespaceIsSkipped doubles as a no-op regression: a source that is
// only whitespace must lex to exactly one EOF token.
func TestWhitespaceIsSkipped(t *testing.T) {
	l := New("   \n\t  \r\n   ")
	got := l.NextToken()
	if got.Type != TOKEN_EOF {
		t.Fatalf("whitespace-only input should lex to EOF, got %q", got.Literal)
	}
}

// TestUnterminatedString makes sure an opening quote without a closing
// quote does not loop forever or panic — it must terminate at EOF with the
// accumulated literal.
func TestUnterminatedString(t *testing.T) {
	l := New(`"oops`)
	tok := l.NextToken()
	if tok.Type != TOKEN_STRING {
		t.Fatalf("unterminated string should still emit STRING, got %q", tok.Type)
	}
	if tok.Literal != "oops" {
		t.Fatalf("unterminated string literal mismatch: got %q", tok.Literal)
	}
	if (l.NextToken()).Type != TOKEN_EOF {
		t.Fatalf("expected EOF after unterminated string")
	}
}

// TestAllRegistersCovered drives one tokenization pass over every register
// from R1 to R16 to guarantee the registers table is complete, in order,
// and that each token's literal matches its declared name exactly.
func TestAllRegistersCovered(t *testing.T) {
	input := "R1 R2 R3 R4 R5 R6 R7 R8 R9 R10 R11 R12 R13 R14 R15 R16"
	expected := []struct {
		typ TokenType
		lit string
	}{
		{TOKEN_R1, "R1"}, {TOKEN_R2, "R2"}, {TOKEN_R3, "R3"}, {TOKEN_R4, "R4"},
		{TOKEN_R5, "R5"}, {TOKEN_R6, "R6"}, {TOKEN_R7, "R7"}, {TOKEN_R8, "R8"},
		{TOKEN_R9, "R9"}, {TOKEN_R10, "R10"}, {TOKEN_R11, "R11"}, {TOKEN_R12, "R12"},
		{TOKEN_R13, "R13"}, {TOKEN_R14, "R14"}, {TOKEN_R15, "R15"}, {TOKEN_R16, "R16"},
	}

	l := New(input)
	for i, want := range expected {
		tok := l.NextToken()
		if tok.Type != want.typ {
			t.Fatalf("registers[%d] type mismatch: expected=%q got=%q (literal=%q)",
				i, want.typ, tok.Type, tok.Literal)
		}
		if tok.Literal != want.lit {
			t.Fatalf("registers[%d] literal mismatch: expected=%q got=%q (type=%q)",
				i, want.lit, tok.Literal, tok.Type)
		}
	}
	if (l.NextToken()).Type != TOKEN_EOF {
		t.Fatalf("expected EOF after R16")
	}
}

// TestNegativeNumberRejected pins current behaviour: flux integers are
// non-negative; a leading '-' is reported as an ILLEGAL token. If we ever
// add signed ints this test should be updated alongside the lexer.
func TestNegativeNumberRejected(t *testing.T) {
	l := New("-1")
	tok := l.NextToken()
	if tok.Type != TOKEN_ILLEGAL {
		t.Fatalf("expected '-' to be ILLEGAL, got %q", tok.Type)
	}
	if tok.Literal != "-" {
		t.Fatalf("expected ILLEGAL literal to be \"-\", got %q", tok.Literal)
	}
}

// TestAllALUKeywordsCovered checks that all 9 ALU keywords are tokenized
// with the correct TokenType and literal values.
func TestAllALUKeywordsCovered(t *testing.T) {
	input := "ADD SUB MUL DIV AND OR XOR SHL SHR"
	expected := []struct {
		typ TokenType
		lit string
	}{
		{TOKEN_ADD, "ADD"},
		{TOKEN_SUB, "SUB"},
		{TOKEN_MUL, "MUL"},
		{TOKEN_DIV, "DIV"},
		{TOKEN_AND, "AND"},
		{TOKEN_OR, "OR"},
		{TOKEN_XOR, "XOR"},
		{TOKEN_SHL, "SHL"},
		{TOKEN_SHR, "SHR"},
	}

	l := New(input)
	for i, want := range expected {
		tok := l.NextToken()
		if tok.Type != want.typ {
			t.Fatalf("ALU[%d] type mismatch: expected=%q got=%q (literal=%q)",
				i, want.typ, tok.Type, tok.Literal)
		}
		if tok.Literal != want.lit {
			t.Fatalf("ALU[%d] literal mismatch: expected=%q got=%q (type=%q)",
				i, want.lit, tok.Literal, tok.Type)
		}
	}
	if (l.NextToken()).Type != TOKEN_EOF {
		t.Fatalf("expected EOF after SHR")
	}
}

// TestControlFlowKeywordsCovered checks CMP, JMP, JZ, JNZ tokenization.
func TestControlFlowKeywordsCovered(t *testing.T) {
	input := "CMP JMP JZ JNZ"
	expected := []struct {
		typ TokenType
		lit string
	}{
		{TOKEN_CMP, "CMP"},
		{TOKEN_JMP, "JMP"},
		{TOKEN_JZ, "JZ"},
		{TOKEN_JNZ, "JNZ"},
	}

	l := New(input)
	for i, want := range expected {
		tok := l.NextToken()
		if tok.Type != want.typ {
			t.Fatalf("ControlFlow[%d] type mismatch: expected=%q got=%q (literal=%q)",
				i, want.typ, tok.Type, tok.Literal)
		}
		if tok.Literal != want.lit {
			t.Fatalf("ControlFlow[%d] literal mismatch: expected=%q got=%q (type=%q)",
				i, want.lit, tok.Literal, tok.Type)
		}
	}
	if (l.NextToken()).Type != TOKEN_EOF {
		t.Fatalf("expected EOF after JNZ")
	}
}

// TestLabelTokenization verifies that label definitions emit IDENT + COLON.
func TestLabelTokenization(t *testing.T) {
	input := "loop_start:\n  CMP R1, R2\n  JNZ loop_start\n"
	expected := []struct {
		typ TokenType
		lit string
	}{
		{TOKEN_IDENT, "loop_start"},
		{TOKEN_COLON, ":"},
		{TOKEN_CMP, "CMP"},
		{TOKEN_R1, "R1"},
		{TOKEN_COMMA, ","},
		{TOKEN_R2, "R2"},
		{TOKEN_JNZ, "JNZ"},
		{TOKEN_IDENT, "loop_start"},
		{TOKEN_EOF, ""},
	}

	l := New(input)
	for i, want := range expected {
		tok := l.NextToken()
		if tok.Type != want.typ {
			t.Fatalf("step %d: type mismatch: expected=%q got=%q (literal=%q)",
				i, want.typ, tok.Type, tok.Literal)
		}
		if want.lit != "" && tok.Literal != want.lit {
			t.Fatalf("step %d: literal mismatch: expected=%q got=%q",
				i, want.lit, tok.Literal)
		}
	}
}


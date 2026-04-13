package lexer

import (
	"math"
	"testing"
)

func TestScannerTokenGolden(t *testing.T) {
	source := []byte("-- hello\nlocal x = 42\nreturn x + 'a\\nb', ...\n")
	scanner := NewScanner("@golden.lua", source)
	tokens, err := scanner.ScanAll()
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	type tokenShape struct {
		kind        TokenKind
		lexeme      string
		numberValue float64
		stringValue string
		line        int
		column      int
	}
	want := []tokenShape{
		{kind: TokenLocal, lexeme: "local", line: 2, column: 1},
		{kind: TokenName, lexeme: "x", line: 2, column: 7},
		{kind: TokenAssign, lexeme: "=", line: 2, column: 9},
		{kind: TokenNumber, lexeme: "42", numberValue: 42, line: 2, column: 11},
		{kind: TokenReturn, lexeme: "return", line: 3, column: 1},
		{kind: TokenName, lexeme: "x", line: 3, column: 8},
		{kind: TokenPlus, lexeme: "+", line: 3, column: 10},
		{kind: TokenString, lexeme: "'a\\nb'", stringValue: "a\nb", line: 3, column: 12},
		{kind: TokenComma, lexeme: ",", line: 3, column: 18},
		{kind: TokenDots, lexeme: "...", line: 3, column: 20},
		{kind: TokenEOF, lexeme: "", line: 4, column: 1},
	}
	if len(tokens) != len(want) {
		t.Fatalf("token count = %d, want %d", len(tokens), len(want))
	}
	for index, expected := range want {
		got := tokens[index]
		if got.Kind != expected.kind || got.Lexeme != expected.lexeme || got.NumberValue != expected.numberValue || got.StringValue != expected.stringValue {
			t.Fatalf("token %d = %+v, want kind=%s lexeme=%q number=%v string=%q", index, got, expected.kind, expected.lexeme, expected.numberValue, expected.stringValue)
		}
		if got.Span.Start.Line != expected.line || got.Span.Start.Column != expected.column {
			t.Fatalf("token %d start = %d:%d, want %d:%d", index, got.Span.Start.Line, got.Span.Start.Column, expected.line, expected.column)
		}
	}
}

func TestScannerLongStringAndCommentBoundaries(t *testing.T) {
	source := []byte("-- short\n--[=[long\r\ncomment]=]\n[==[\r\nline1\rline2\n]==]")
	scanner := NewScanner("@long.lua", source)
	token, err := scanner.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if token.Kind != TokenString {
		t.Fatalf("long string token kind = %s, want string", token.Kind)
	}
	if token.StringValue != "line1\nline2\n" {
		t.Fatalf("long string value = %q, want %q", token.StringValue, "line1\nline2\n")
	}
	if token.Span.Start.Line != 4 || token.Span.Start.Column != 1 {
		t.Fatalf("long string start = %d:%d, want 4:1", token.Span.Start.Line, token.Span.Start.Column)
	}
	eof, err := scanner.Next()
	if err != nil {
		t.Fatalf("Next EOF: %v", err)
	}
	if eof.Kind != TokenEOF {
		t.Fatalf("EOF token kind = %s, want eof", eof.Kind)
	}
}

func TestScannerLookaheadDoesNotAdvance(t *testing.T) {
	scanner := NewScanner("@peek.lua", []byte("return x"))
	first, err := scanner.Lookahead()
	if err != nil {
		t.Fatalf("first lookahead: %v", err)
	}
	second, err := scanner.Lookahead()
	if err != nil {
		t.Fatalf("second lookahead: %v", err)
	}
	if first != second {
		t.Fatalf("lookahead tokens differ: %+v vs %+v", first, second)
	}
	next, err := scanner.Next()
	if err != nil {
		t.Fatalf("next after lookahead: %v", err)
	}
	if next != first {
		t.Fatalf("next token = %+v, want %+v", next, first)
	}
	if next.Kind != TokenReturn {
		t.Fatalf("next token kind = %s, want return", next.Kind)
	}
}

func TestScannerMalformedNumberErrors(t *testing.T) {
	for _, source := range []string{"1foo", "1e+", "0x10"} {
		t.Run(source, func(t *testing.T) {
			_, err := NewScanner("@bad-number.lua", []byte(source)).Next()
			if err == nil || err.Error() == "" {
				t.Fatalf("expected malformed number error for %q, got %v", source, err)
			}
			if err.Error() != "lex: 1:1: malformed number" {
				t.Fatalf("number error = %q, want %q", err.Error(), "lex: 1:1: malformed number")
			}
		})
	}
}

func TestScannerNumberOverflowMatchesLua51Acceptance(t *testing.T) {
	token, err := NewScanner("@overflow.lua", []byte("1e5000")).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if token.Kind != TokenNumber {
		t.Fatalf("token kind = %s, want number", token.Kind)
	}
	if !math.IsInf(token.NumberValue, 1) {
		t.Fatalf("number value = %v, want +Inf", token.NumberValue)
	}
}

func TestScannerStringErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		message string
	}{
		{name: "unfinished eof", source: "'abc", message: "lex: 1:1: unfinished string"},
		{name: "unfinished newline", source: "'abc\n", message: "lex: 1:1: unfinished string"},
		{name: "escape too large", source: "'\\300'", message: "lex: 1:1: escape sequence too large"},
		{name: "invalid long delimiter", source: "[=foo", message: "lex: 1:1: invalid long string delimiter"},
		{name: "unfinished long string", source: "[=[abc", message: "lex: 1:1: unfinished long string"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewScanner("@bad-string.lua", []byte(test.source)).Next()
			if err == nil {
				t.Fatalf("expected lexer error for %q", test.source)
			}
			if err.Error() != test.message {
				t.Fatalf("error = %q, want %q", err.Error(), test.message)
			}
		})
	}
}

func TestScannerStringEscapesMatchLua51Shape(t *testing.T) {
	scanner := NewScanner("@escapes.lua", []byte("'\\a\\b\\f\\n\\r\\t\\v\\065\\\\\\\"\\''"))
	token, err := scanner.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if token.Kind != TokenString {
		t.Fatalf("token kind = %s, want string", token.Kind)
	}
	if token.StringValue != "\a\b\f\n\r\t\vA\\\"'" {
		t.Fatalf("string value = %q, want %q", token.StringValue, "\a\b\f\n\r\t\vA\\\"'")
	}
}

func TestScannerStringEscapedNewlineNormalizesToLF(t *testing.T) {
	scanner := NewScanner("@newline-escape.lua", []byte("\"a\\\r\nb\""))
	token, err := scanner.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if token.StringValue != "a\nb" {
		t.Fatalf("string value = %q, want %q", token.StringValue, "a\nb")
	}
}

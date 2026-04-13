package lexer

import "testing"

func TestReservedWordsKeepLua51Order(t *testing.T) {
	want := []TokenKind{
		TokenAnd,
		TokenBreak,
		TokenDo,
		TokenElse,
		TokenElseIf,
		TokenEnd,
		TokenFalse,
		TokenFor,
		TokenFunction,
		TokenIf,
		TokenIn,
		TokenLocal,
		TokenNil,
		TokenNot,
		TokenOr,
		TokenRepeat,
		TokenReturn,
		TokenThen,
		TokenTrue,
		TokenUntil,
		TokenWhile,
	}
	got := ReservedWordsInLua51Order()
	if len(got) != len(want) {
		t.Fatalf("reserved word count = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("reserved word %d = %s, want %s", index, got[index], want[index])
		}
		if !got[index].IsReservedWord() {
			t.Fatalf("reserved word %s should report IsReservedWord", got[index])
		}
	}
}

func TestLookupKeywordAndSpanContract(t *testing.T) {
	if got, ok := LookupKeyword("function"); !ok || got != TokenFunction {
		t.Fatalf("LookupKeyword(function) = %s, %v", got, ok)
	}
	if _, ok := LookupKeyword("identifier"); ok {
		t.Fatalf("LookupKeyword(identifier) unexpectedly matched")
	}
	start := StartPosition()
	end := Position{Offset: 4, Line: 1, Column: 5}
	span := Span{Start: start, End: end}
	if !span.IsValid() {
		t.Fatalf("span should be valid")
	}
	if span.Len() != 4 {
		t.Fatalf("span length = %d, want 4", span.Len())
	}
	merged := MergeSpans(span, Span{Start: Position{Offset: 8, Line: 2, Column: 1}, End: Position{Offset: 10, Line: 2, Column: 3}})
	if merged.Start.Offset != 0 || merged.End.Offset != 10 {
		t.Fatalf("merged span = %+v, want [0,10)", merged)
	}
}

func TestDiagnosticErrorKeepsPrimaryMessage(t *testing.T) {
	err := NewDiagnosticError(
		Diagnostic{Phase: PhaseParse, Severity: SeverityError, Message: "first", Span: Span{Start: StartPosition(), End: Position{Offset: 1, Line: 1, Column: 2}}},
		Diagnostic{Phase: PhaseBind, Severity: SeverityError, Message: "second"},
	)
	diagErr, ok := err.(*DiagnosticError)
	if !ok {
		t.Fatalf("diagnostic error type = %T, want *DiagnosticError", err)
	}
	primary, ok := diagErr.Primary()
	if !ok || primary.Message != "first" {
		t.Fatalf("primary diagnostic = %+v, %v", primary, ok)
	}
	if diagErr.Error() == "" {
		t.Fatalf("diagnostic error string should not be empty")
	}
}

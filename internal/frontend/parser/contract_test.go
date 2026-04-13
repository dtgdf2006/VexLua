package parser

import (
	"testing"

	"vexlua/internal/frontend/lexer"
)

func TestASTNodeFamiliesCoverPhase0Surface(t *testing.T) {
	span := lexer.Span{Start: lexer.StartPosition(), End: lexer.Position{Offset: 1, Line: 1, Column: 2}}
	name := Name{Span: span, Text: "x", Token: lexer.Token{Kind: lexer.TokenName, Span: span, Lexeme: "x"}}
	body := &FunctionBody{NodeInfo: NodeInfo{Span: span}, Params: []Name{name}, Block: &Block{NodeInfo: NodeInfo{Span: span}}}

	stats := []Stat{
		EmptyStat{NodeInfo: NodeInfo{Span: span}},
		LocalDeclStat{NodeInfo: NodeInfo{Span: span}, Names: []Name{name}},
		LocalFunctionStat{NodeInfo: NodeInfo{Span: span}, Name: name, Body: body},
		AssignmentStat{NodeInfo: NodeInfo{Span: span}, Targets: []AssignableExpr{NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}}},
		FunctionStat{NodeInfo: NodeInfo{Span: span}, Path: []Name{name}, Body: body},
		MethodStat{NodeInfo: NodeInfo{Span: span}, Path: []Name{name}, Method: name, Body: body},
		CallStat{NodeInfo: NodeInfo{Span: span}, Call: CallExpr{NodeInfo: NodeInfo{Span: span}, Callee: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}}},
		IfStat{NodeInfo: NodeInfo{Span: span}},
		WhileStat{NodeInfo: NodeInfo{Span: span}},
		RepeatUntilStat{NodeInfo: NodeInfo{Span: span}},
		NumericForStat{NodeInfo: NodeInfo{Span: span}, Name: name},
		GenericForStat{NodeInfo: NodeInfo{Span: span}, Names: []Name{name}},
		DoStat{NodeInfo: NodeInfo{Span: span}, Body: &Block{NodeInfo: NodeInfo{Span: span}}},
		BreakStat{NodeInfo: NodeInfo{Span: span}},
		ReturnStat{NodeInfo: NodeInfo{Span: span}},
	}
	for _, stat := range stats {
		if got := stat.SpanRange(); !got.IsValid() {
			t.Fatalf("statement %T span = %+v, want valid span", stat, got)
		}
	}

	exprs := []Expr{
		NilExpr{NodeInfo: NodeInfo{Span: span}},
		BoolExpr{NodeInfo: NodeInfo{Span: span}, Value: true},
		NumberExpr{NodeInfo: NodeInfo{Span: span}, Raw: "1", Value: 1},
		StringExpr{NodeInfo: NodeInfo{Span: span}, Raw: "\"x\"", Value: "x"},
		VarargExpr{NodeInfo: NodeInfo{Span: span}},
		NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name},
		UnaryExpr{NodeInfo: NodeInfo{Span: span}, Op: lexer.TokenMinus, Value: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}},
		BinaryExpr{NodeInfo: NodeInfo{Span: span}, Op: lexer.TokenPlus, Left: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}, Right: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}},
		TableConstructorExpr{NodeInfo: NodeInfo{Span: span}, Fields: []TableField{{Span: span, Kind: TableFieldArray, Value: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}}}},
		FunctionLiteralExpr{NodeInfo: NodeInfo{Span: span}, Body: body},
		IndexExpr{NodeInfo: NodeInfo{Span: span}, Receiver: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}, Index: NumberExpr{NodeInfo: NodeInfo{Span: span}, Raw: "1", Value: 1}},
		FieldExpr{NodeInfo: NodeInfo{Span: span}, Receiver: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}, Name: name},
		MethodExpr{NodeInfo: NodeInfo{Span: span}, Receiver: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}, Name: name},
		CallExpr{NodeInfo: NodeInfo{Span: span}, Callee: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}},
		MethodCallExpr{NodeInfo: NodeInfo{Span: span}, Receiver: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}, Name: name},
		ParenExpr{NodeInfo: NodeInfo{Span: span}, Inner: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}},
	}
	for _, expr := range exprs {
		if got := expr.SpanRange(); !got.IsValid() {
			t.Fatalf("expression %T span = %+v, want valid span", expr, got)
		}
	}
}

func TestAssignableExpressionSurface(t *testing.T) {
	span := lexer.Span{Start: lexer.StartPosition(), End: lexer.Position{Offset: 1, Line: 1, Column: 2}}
	name := Name{Span: span, Text: "x", Token: lexer.Token{Kind: lexer.TokenName, Span: span, Lexeme: "x"}}
	targets := []AssignableExpr{
		NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name},
		IndexExpr{NodeInfo: NodeInfo{Span: span}, Receiver: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}, Index: NumberExpr{NodeInfo: NodeInfo{Span: span}, Raw: "1", Value: 1}},
		FieldExpr{NodeInfo: NodeInfo{Span: span}, Receiver: NameExpr{NodeInfo: NodeInfo{Span: span}, Name: name}, Name: name},
	}
	if len(targets) != 3 {
		t.Fatalf("assignable targets = %d, want 3", len(targets))
	}
}

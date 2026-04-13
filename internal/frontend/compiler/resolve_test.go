package compiler

import (
	"testing"

	"vexlua/internal/frontend/lexer"
	"vexlua/internal/frontend/parser"
)

func TestBinderBindsLocalShadowing(t *testing.T) {
	chunk := bindSource(t, "@shadow.lua", []byte(`
local x = 1
do
  local x = 2
  consume(x)
end
consume(x)
`))

	if len(chunk.Func.Locals) != 2 {
		t.Fatalf("top-level locals = %d, want 2", len(chunk.Func.Locals))
	}
	doStat, ok := chunk.Func.Body.Stats[1].(BoundDoStat)
	if !ok {
		t.Fatalf("statement 1 = %T, want BoundDoStat", chunk.Func.Body.Stats[1])
	}
	innerCall, ok := doStat.Body.Stats[1].(BoundCallStat)
	if !ok {
		t.Fatalf("inner statement = %T, want BoundCallStat", doStat.Body.Stats[1])
	}
	outerCall, ok := chunk.Func.Body.Stats[2].(BoundCallStat)
	if !ok {
		t.Fatalf("statement 2 = %T, want BoundCallStat", chunk.Func.Body.Stats[2])
	}
	innerArg := requireBoundNameArg(t, innerCall.Call)
	outerArg := requireBoundNameArg(t, outerCall.Call)
	if innerArg.Ref.Symbol == outerArg.Ref.Symbol {
		t.Fatalf("shadowed symbol ids should differ: inner=%d outer=%d", innerArg.Ref.Symbol, outerArg.Ref.Symbol)
	}
	if symbolByID(t, chunk, innerArg.Ref.Symbol).Scope == symbolByID(t, chunk, outerArg.Ref.Symbol).Scope {
		t.Fatalf("shadowed locals should live in different scopes")
	}
}

func TestBinderCapturesMultiLevelUpvalues(t *testing.T) {
	chunk := bindSource(t, "@upvalues.lua", []byte(`
local x = 1
local function outer(a)
  local y = a
  return function()
    return function()
      return x + y + a
    end
  end
end
`))

	outerDecl, ok := chunk.Func.Body.Stats[1].(BoundLocalDeclStat)
	if !ok {
		t.Fatalf("statement 1 = %T, want BoundLocalDeclStat", chunk.Func.Body.Stats[1])
	}
	outerExpr, ok := outerDecl.Values[0].(BoundFunctionExpr)
	if !ok {
		t.Fatalf("outer value = %T, want BoundFunctionExpr", outerDecl.Values[0])
	}
	middleExpr := requireLastReturnedFunction(t, outerExpr.Func)
	innerExpr := requireReturnedFunction(t, middleExpr.Func)

	if len(outerExpr.Func.Captures) != 1 {
		t.Fatalf("outer captures = %d, want 1", len(outerExpr.Func.Captures))
	}
	if len(middleExpr.Func.Captures) != 3 {
		t.Fatalf("middle captures = %d, want 3", len(middleExpr.Func.Captures))
	}
	if len(innerExpr.Func.Captures) != 3 {
		t.Fatalf("inner captures = %d, want 3", len(innerExpr.Func.Captures))
	}

	xSymbol := chunk.Func.Locals[0]
	aSymbol := outerExpr.Func.Params[0]
	ySymbol := outerExpr.Func.Locals[0]

	assertCapture(t, outerExpr.Func, xSymbol, CaptureFromLocal)
	assertCapture(t, middleExpr.Func, xSymbol, CaptureFromUpvalue)
	assertCapture(t, middleExpr.Func, ySymbol, CaptureFromLocal)
	assertCapture(t, middleExpr.Func, aSymbol, CaptureFromLocal)
	assertCapture(t, innerExpr.Func, xSymbol, CaptureFromUpvalue)
	assertCapture(t, innerExpr.Func, ySymbol, CaptureFromUpvalue)
	assertCapture(t, innerExpr.Func, aSymbol, CaptureFromUpvalue)

	outerXScope := symbolByID(t, chunk, xSymbol).Scope
	if !scopeByID(t, chunk, outerXScope).HasUpvalues {
		t.Fatalf("captured top-level x scope should be marked HasUpvalues")
	}
	outerYScope := symbolByID(t, chunk, ySymbol).Scope
	if !scopeByID(t, chunk, outerYScope).HasUpvalues {
		t.Fatalf("captured local y scope should be marked HasUpvalues")
	}
	outerAScope := symbolByID(t, chunk, aSymbol).Scope
	if !scopeByID(t, chunk, outerAScope).HasUpvalues {
		t.Fatalf("captured param a scope should be marked HasUpvalues")
	}
}

func TestBinderLowersMethodDefinitionWithSelfParam(t *testing.T) {
	chunk := bindSource(t, "@method.lua", []byte(`
function obj:step(v)
  return self[v]
end
`))

	assign, ok := chunk.Func.Body.Stats[0].(BoundAssignStat)
	if !ok {
		t.Fatalf("statement 0 = %T, want BoundAssignStat", chunk.Func.Body.Stats[0])
	}
	if len(assign.Targets) != 1 || len(assign.Values) != 1 {
		t.Fatalf("assignment shape = targets:%d values:%d, want 1/1", len(assign.Targets), len(assign.Values))
	}
	target, ok := assign.Targets[0].(BoundFieldTarget)
	if !ok {
		t.Fatalf("target = %T, want BoundFieldTarget", assign.Targets[0])
	}
	if target.Name != "step" {
		t.Fatalf("target name = %q, want step", target.Name)
	}
	functionExpr, ok := assign.Values[0].(BoundFunctionExpr)
	if !ok {
		t.Fatalf("assignment value = %T, want BoundFunctionExpr", assign.Values[0])
	}
	if len(functionExpr.Func.Params) != 2 {
		t.Fatalf("method params = %d, want 2", len(functionExpr.Func.Params))
	}
	selfSymbol := symbolByID(t, chunk, functionExpr.Func.Params[0])
	if selfSymbol.Name != "self" || selfSymbol.Kind != SymbolParam {
		t.Fatalf("self symbol = %+v, want param named self", selfSymbol)
	}
	vSymbol := symbolByID(t, chunk, functionExpr.Func.Params[1])
	if vSymbol.Name != "v" || vSymbol.Kind != SymbolParam {
		t.Fatalf("v symbol = %+v, want param named v", vSymbol)
	}
}

func TestBinderReportsIllegalVarargUsage(t *testing.T) {
	span := sampleSpan()
	name := parser.Name{Span: span, Text: "f", Token: lexer.Token{Kind: lexer.TokenName, Span: span, Lexeme: "f"}}
	chunk := &parser.Chunk{
		NodeInfo: parser.NodeInfo{Span: span},
		Name:     "@bad-vararg.lua",
		Block: &parser.Block{NodeInfo: parser.NodeInfo{Span: span}, Stats: []parser.Stat{
			parser.LocalFunctionStat{
				NodeInfo: parser.NodeInfo{Span: span},
				Name:     name,
				Body: &parser.FunctionBody{
					NodeInfo:  parser.NodeInfo{Span: span},
					HasVararg: false,
					Block: &parser.Block{NodeInfo: parser.NodeInfo{Span: span}, Stats: []parser.Stat{
						parser.ReturnStat{NodeInfo: parser.NodeInfo{Span: span}, Values: []parser.Expr{parser.VarargExpr{NodeInfo: parser.NodeInfo{Span: span}}}},
					}},
				},
			},
		}},
	}

	_, err := BindChunk(chunk)
	diagnostic := requirePrimaryBindDiagnostic(t, err)
	if diagnostic.Message != "cannot use '...' outside a vararg function" {
		t.Fatalf("diagnostic message = %q, want %q", diagnostic.Message, "cannot use '...' outside a vararg function")
	}
}

func TestBinderReportsIllegalBreakUsage(t *testing.T) {
	span := sampleSpan()
	chunk := &parser.Chunk{
		NodeInfo: parser.NodeInfo{Span: span},
		Name:     "@bad-break.lua",
		Block: &parser.Block{NodeInfo: parser.NodeInfo{Span: span}, Stats: []parser.Stat{
			parser.BreakStat{NodeInfo: parser.NodeInfo{Span: span}},
		}},
	}

	_, err := BindChunk(chunk)
	diagnostic := requirePrimaryBindDiagnostic(t, err)
	if diagnostic.Message != "no loop to break" {
		t.Fatalf("diagnostic message = %q, want %q", diagnostic.Message, "no loop to break")
	}
}

func bindSource(t *testing.T, name string, source []byte) *BoundChunk {
	t.Helper()
	chunk, err := parser.ParseChunk(name, source)
	if err != nil {
		t.Fatalf("ParseChunk: %v", err)
	}
	bound, err := BindChunk(chunk)
	if err != nil {
		t.Fatalf("BindChunk: %v", err)
	}
	return bound
}

func requirePrimaryBindDiagnostic(t *testing.T, err error) lexer.Diagnostic {
	t.Helper()
	if err == nil {
		t.Fatalf("expected bind diagnostic")
	}
	diagErr, ok := err.(*lexer.DiagnosticError)
	if !ok {
		t.Fatalf("error type = %T, want *lexer.DiagnosticError", err)
	}
	primary, ok := diagErr.Primary()
	if !ok {
		t.Fatalf("missing primary diagnostic")
	}
	if primary.Phase != lexer.PhaseBind {
		t.Fatalf("diagnostic phase = %s, want bind", primary.Phase)
	}
	return primary
}

func requireBoundNameArg(t *testing.T, call BoundCallLike) BoundSymbolExpr {
	t.Helper()
	callExpr, ok := call.(BoundCallExpr)
	if !ok {
		t.Fatalf("call expr = %T, want BoundCallExpr", call)
	}
	if len(callExpr.Args) != 1 {
		t.Fatalf("call args = %d, want 1", len(callExpr.Args))
	}
	name, ok := callExpr.Args[0].(BoundSymbolExpr)
	if !ok {
		t.Fatalf("call arg = %T, want BoundSymbolExpr", callExpr.Args[0])
	}
	return name
}

func requireReturnedFunction(t *testing.T, function *BoundFunc) BoundFunctionExpr {
	t.Helper()
	if function == nil || function.Body == nil || len(function.Body.Stats) != 1 {
		t.Fatalf("function body = %+v, want single return", function)
	}
	ret, ok := function.Body.Stats[0].(BoundReturnStat)
	if !ok {
		t.Fatalf("function stat = %T, want BoundReturnStat", function.Body.Stats[0])
	}
	if len(ret.Values) != 1 {
		t.Fatalf("return values = %d, want 1", len(ret.Values))
	}
	value, ok := ret.Values[0].(BoundFunctionExpr)
	if !ok {
		t.Fatalf("return value = %T, want BoundFunctionExpr", ret.Values[0])
	}
	return value
}

func requireLastReturnedFunction(t *testing.T, function *BoundFunc) BoundFunctionExpr {
	t.Helper()
	if function == nil || function.Body == nil || len(function.Body.Stats) == 0 {
		t.Fatalf("function body = %+v, want trailing return", function)
	}
	ret, ok := function.Body.Stats[len(function.Body.Stats)-1].(BoundReturnStat)
	if !ok {
		t.Fatalf("last function stat = %T, want BoundReturnStat", function.Body.Stats[len(function.Body.Stats)-1])
	}
	if len(ret.Values) != 1 {
		t.Fatalf("return values = %d, want 1", len(ret.Values))
	}
	value, ok := ret.Values[0].(BoundFunctionExpr)
	if !ok {
		t.Fatalf("return value = %T, want BoundFunctionExpr", ret.Values[0])
	}
	return value
}

func assertCapture(t *testing.T, function *BoundFunc, symbol SymbolID, source CaptureSource) {
	t.Helper()
	for _, capture := range function.Captures {
		if capture.Symbol == symbol {
			if capture.Source != source {
				t.Fatalf("capture source for symbol %d = %d, want %d", symbol, capture.Source, source)
			}
			return
		}
	}
	t.Fatalf("missing capture for symbol %d", symbol)
}

func symbolByID(t *testing.T, chunk *BoundChunk, symbolID SymbolID) Symbol {
	t.Helper()
	index := int(symbolID) - 1
	if index < 0 || index >= len(chunk.Symbols) {
		t.Fatalf("symbol id %d out of range", symbolID)
	}
	return chunk.Symbols[index]
}

func scopeByID(t *testing.T, chunk *BoundChunk, scopeID ScopeID) Scope {
	t.Helper()
	index := int(scopeID) - 1
	if index < 0 || index >= len(chunk.Scopes) {
		t.Fatalf("scope id %d out of range", scopeID)
	}
	return chunk.Scopes[index]
}

func sampleSpan() lexer.Span {
	return lexer.Span{Start: lexer.StartPosition(), End: lexer.Position{Offset: 1, Line: 1, Column: 2}}
}

package parser

import (
	"os"
	"path/filepath"
	"testing"

	"vexlua/internal/frontend/lexer"
)

func TestParserPhase3StatementFamilies(t *testing.T) {
	source := []byte(`local x, y, wrapped = 1, ..., (sink)

function mod.run(a)
  repeat
    a = a - 1
  until a < 1
  return a
end

function obj:step(v)
  self[v] = self[v] + 1
  return self[v];
end

for i = 1, 3, 1 do
  sink(i)
end

for k, v in pairs(t) do
  consume(k, v)
end

runner(wrapped)
`)

	chunk, err := ParseChunk("@phase3-families.lua", source)
	if err != nil {
		t.Fatalf("ParseChunk: %v", err)
	}
	if len(chunk.Block.Stats) != 6 {
		t.Fatalf("top-level statement count = %d, want 6", len(chunk.Block.Stats))
	}

	localDecl, ok := chunk.Block.Stats[0].(LocalDeclStat)
	if !ok {
		t.Fatalf("statement 0 = %T, want LocalDeclStat", chunk.Block.Stats[0])
	}
	if len(localDecl.Names) != 3 || localDecl.Names[0].Text != "x" || localDecl.Names[1].Text != "y" || localDecl.Names[2].Text != "wrapped" {
		t.Fatalf("local declaration names = %+v, want x/y/wrapped", localDecl.Names)
	}
	if len(localDecl.Values) != 3 {
		t.Fatalf("local declaration values = %d, want 3", len(localDecl.Values))
	}
	if _, ok := localDecl.Values[1].(VarargExpr); !ok {
		t.Fatalf("local declaration value[1] = %T, want VarargExpr", localDecl.Values[1])
	}
	if _, ok := localDecl.Values[2].(ParenExpr); !ok {
		t.Fatalf("local declaration value[2] = %T, want ParenExpr", localDecl.Values[2])
	}

	functionStat, ok := chunk.Block.Stats[1].(FunctionStat)
	if !ok {
		t.Fatalf("statement 1 = %T, want FunctionStat", chunk.Block.Stats[1])
	}
	if len(functionStat.Path) != 2 || functionStat.Path[0].Text != "mod" || functionStat.Path[1].Text != "run" {
		t.Fatalf("function path = %+v, want mod.run", functionStat.Path)
	}
	if functionStat.Body == nil || functionStat.Body.Block == nil || len(functionStat.Body.Block.Stats) != 2 {
		t.Fatalf("function body stats = %+v, want repeat+return", functionStat.Body)
	}
	if _, ok := functionStat.Body.Block.Stats[0].(RepeatUntilStat); !ok {
		t.Fatalf("function body stat[0] = %T, want RepeatUntilStat", functionStat.Body.Block.Stats[0])
	}
	if _, ok := functionStat.Body.Block.Stats[1].(ReturnStat); !ok {
		t.Fatalf("function body stat[1] = %T, want ReturnStat", functionStat.Body.Block.Stats[1])
	}

	methodStat, ok := chunk.Block.Stats[2].(MethodStat)
	if !ok {
		t.Fatalf("statement 2 = %T, want MethodStat", chunk.Block.Stats[2])
	}
	if len(methodStat.Path) != 1 || methodStat.Path[0].Text != "obj" || methodStat.Method.Text != "step" {
		t.Fatalf("method stat = path:%+v method:%+v, want obj:step", methodStat.Path, methodStat.Method)
	}
	if methodStat.Body == nil || methodStat.Body.Block == nil || len(methodStat.Body.Block.Stats) != 2 {
		t.Fatalf("method body stats = %+v, want assignment+return", methodStat.Body)
	}
	if _, ok := methodStat.Body.Block.Stats[0].(AssignmentStat); !ok {
		t.Fatalf("method body stat[0] = %T, want AssignmentStat", methodStat.Body.Block.Stats[0])
	}
	if _, ok := methodStat.Body.Block.Stats[1].(ReturnStat); !ok {
		t.Fatalf("method body stat[1] = %T, want ReturnStat", methodStat.Body.Block.Stats[1])
	}

	numericFor, ok := chunk.Block.Stats[3].(NumericForStat)
	if !ok {
		t.Fatalf("statement 3 = %T, want NumericForStat", chunk.Block.Stats[3])
	}
	if numericFor.Name.Text != "i" {
		t.Fatalf("numeric for name = %q, want i", numericFor.Name.Text)
	}
	if numericFor.Step == nil {
		t.Fatalf("numeric for step should be present")
	}
	if numericFor.Body == nil || len(numericFor.Body.Stats) != 1 {
		t.Fatalf("numeric for body = %+v, want single CallStat", numericFor.Body)
	}
	if _, ok := numericFor.Body.Stats[0].(CallStat); !ok {
		t.Fatalf("numeric for body stat[0] = %T, want CallStat", numericFor.Body.Stats[0])
	}

	genericFor, ok := chunk.Block.Stats[4].(GenericForStat)
	if !ok {
		t.Fatalf("statement 4 = %T, want GenericForStat", chunk.Block.Stats[4])
	}
	if len(genericFor.Names) != 2 || genericFor.Names[0].Text != "k" || genericFor.Names[1].Text != "v" {
		t.Fatalf("generic for names = %+v, want k/v", genericFor.Names)
	}
	if len(genericFor.Iterators) != 1 {
		t.Fatalf("generic for iterators = %d, want 1", len(genericFor.Iterators))
	}
	if _, ok := genericFor.Iterators[0].(CallExpr); !ok {
		t.Fatalf("generic for iterator[0] = %T, want CallExpr", genericFor.Iterators[0])
	}

	callStat, ok := chunk.Block.Stats[5].(CallStat)
	if !ok {
		t.Fatalf("statement 5 = %T, want CallStat", chunk.Block.Stats[5])
	}
	callExpr, ok := callStat.Call.(CallExpr)
	if !ok {
		t.Fatalf("call statement expr = %T, want CallExpr", callStat.Call)
	}
	if len(callExpr.Args) != 1 {
		t.Fatalf("call args = %d, want 1", len(callExpr.Args))
	}
	if _, ok := callExpr.Args[0].(NameExpr); !ok {
		t.Fatalf("call arg[0] = %T, want NameExpr", callExpr.Args[0])
	}

	assertNodeSpanValid(t, chunk)
}

func TestParserPhase3ParsesReferenceSamples(t *testing.T) {
	files := []string{"fibfor.lua", "globals.lua", "hello.lua"}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "reference", "lua-5.1.5", "test", name)
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", name, err)
			}
			chunk, err := ParseChunk("@"+name, source)
			if err != nil {
				t.Fatalf("ParseChunk(%s): %v", name, err)
			}
			assertNodeSpanValid(t, chunk)
		})
	}
}

func TestParserPhase3EnforcesBreakContext(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
	}{
		{name: "top-level", source: "break\n"},
		{name: "nested function", source: "while true do\n  local function inner()\n    break\n  end\nend\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseChunk("@break.lua", []byte(test.source))
			diagnostic := requirePrimaryParseDiagnostic(t, err)
			if diagnostic.Message != "no loop to break" {
				t.Fatalf("diagnostic message = %q, want %q", diagnostic.Message, "no loop to break")
			}
		})
	}
}

func TestParserPhase3EnforcesVarargContext(t *testing.T) {
	_, err := ParseChunk("@bad-vararg.lua", []byte("local function f() return ... end\n"))
	diagnostic := requirePrimaryParseDiagnostic(t, err)
	if diagnostic.Message != "cannot use '...' outside a vararg function" {
		t.Fatalf("diagnostic message = %q, want %q", diagnostic.Message, "cannot use '...' outside a vararg function")
	}

	chunk, err := ParseChunk("@top-vararg.lua", []byte("local first = ...\nreturn first\n"))
	if err != nil {
		t.Fatalf("top-level vararg should parse: %v", err)
	}
	assertNodeSpanValid(t, chunk)

	chunk, err = ParseChunk("@good-vararg.lua", []byte("local function f(...) return ... end\n"))
	if err != nil {
		t.Fatalf("vararg function should parse: %v", err)
	}
	assertNodeSpanValid(t, chunk)
}

func TestParserPhase3RejectsAmbiguousCallAcrossLines(t *testing.T) {
	_, err := ParseChunk("@ambiguous-call.lua", []byte("print\n(1)\n"))
	diagnostic := requirePrimaryParseDiagnostic(t, err)
	if diagnostic.Message != "ambiguous syntax (function call x new statement)" {
		t.Fatalf("diagnostic message = %q, want %q", diagnostic.Message, "ambiguous syntax (function call x new statement)")
	}
}

func TestParserPhase3TerminalStatementsConsumeOptionalSemicolon(t *testing.T) {
	chunk, err := ParseChunk("@terminal-semicolon.lua", []byte("do return 1; end\nwhile true do break; end\n"))
	if err != nil {
		t.Fatalf("ParseChunk: %v", err)
	}
	if len(chunk.Block.Stats) != 2 {
		t.Fatalf("top-level statement count = %d, want 2", len(chunk.Block.Stats))
	}
	doStat, ok := chunk.Block.Stats[0].(DoStat)
	if !ok {
		t.Fatalf("statement 0 = %T, want DoStat", chunk.Block.Stats[0])
	}
	if doStat.Body == nil || len(doStat.Body.Stats) != 1 {
		t.Fatalf("do body stats = %+v, want single ReturnStat", doStat.Body)
	}
	if _, ok := doStat.Body.Stats[0].(ReturnStat); !ok {
		t.Fatalf("do body stat[0] = %T, want ReturnStat", doStat.Body.Stats[0])
	}
	whileStat, ok := chunk.Block.Stats[1].(WhileStat)
	if !ok {
		t.Fatalf("statement 1 = %T, want WhileStat", chunk.Block.Stats[1])
	}
	if whileStat.Body == nil || len(whileStat.Body.Stats) != 1 {
		t.Fatalf("while body stats = %+v, want single BreakStat", whileStat.Body)
	}
	if _, ok := whileStat.Body.Stats[0].(BreakStat); !ok {
		t.Fatalf("while body stat[0] = %T, want BreakStat", whileStat.Body.Stats[0])
	}

	_, err = ParseChunk("@return-last.lua", []byte("do return 1; local x = 2 end\n"))
	diagnostic := requirePrimaryParseDiagnostic(t, err)
	if diagnostic.Message != "end expected" {
		t.Fatalf("diagnostic message = %q, want %q", diagnostic.Message, "end expected")
	}
}

func requirePrimaryParseDiagnostic(t *testing.T, err error) lexer.Diagnostic {
	t.Helper()
	if err == nil {
		t.Fatalf("expected parse diagnostic")
	}
	diagErr, ok := err.(*lexer.DiagnosticError)
	if !ok {
		t.Fatalf("error type = %T, want *lexer.DiagnosticError", err)
	}
	primary, ok := diagErr.Primary()
	if !ok {
		t.Fatalf("missing primary diagnostic")
	}
	if primary.Phase != lexer.PhaseParse {
		t.Fatalf("diagnostic phase = %s, want parse", primary.Phase)
	}
	return primary
}

func assertNodeSpanValid(t *testing.T, node Node) {
	t.Helper()
	if node == nil {
		return
	}
	switch typed := node.(type) {
	case *Chunk:
		if typed == nil {
			return
		}
	case *Block:
		if typed == nil {
			return
		}
	case *FunctionBody:
		if typed == nil {
			return
		}
	}
	if !node.SpanRange().IsValid() {
		t.Fatalf("node %T has invalid span %+v", node, node.SpanRange())
	}
	assertNodeChildrenSpanValid(t, node)
}

func assertNodeChildrenSpanValid(t *testing.T, node Node) {
	t.Helper()
	switch typed := node.(type) {
	case *Chunk:
		assertNodeSpanValid(t, typed.Block)
	case *Block:
		for _, stat := range typed.Stats {
			assertNodeSpanValid(t, stat)
		}
	case *FunctionBody:
		for _, param := range typed.Params {
			assertNameSpanValid(t, param)
		}
		assertNodeSpanValid(t, typed.Block)
	case EmptyStat, BreakStat, NilExpr, VarargExpr:
		return
	case LocalDeclStat:
		for _, name := range typed.Names {
			assertNameSpanValid(t, name)
		}
		for _, value := range typed.Values {
			assertNodeSpanValid(t, value)
		}
	case LocalFunctionStat:
		assertNameSpanValid(t, typed.Name)
		assertNodeSpanValid(t, typed.Body)
	case AssignmentStat:
		for _, target := range typed.Targets {
			assertNodeSpanValid(t, target)
		}
		for _, value := range typed.Values {
			assertNodeSpanValid(t, value)
		}
	case FunctionStat:
		for _, name := range typed.Path {
			assertNameSpanValid(t, name)
		}
		assertNodeSpanValid(t, typed.Body)
	case MethodStat:
		for _, name := range typed.Path {
			assertNameSpanValid(t, name)
		}
		assertNameSpanValid(t, typed.Method)
		assertNodeSpanValid(t, typed.Body)
	case CallStat:
		assertNodeSpanValid(t, typed.Call)
	case IfStat:
		for _, clause := range typed.Clauses {
			if !clause.Span.IsValid() {
				t.Fatalf("if clause has invalid span %+v", clause.Span)
			}
			assertNodeSpanValid(t, clause.Condition)
			assertNodeSpanValid(t, clause.Body)
		}
		assertNodeSpanValid(t, typed.ElseBlock)
	case WhileStat:
		assertNodeSpanValid(t, typed.Condition)
		assertNodeSpanValid(t, typed.Body)
	case RepeatUntilStat:
		assertNodeSpanValid(t, typed.Body)
		assertNodeSpanValid(t, typed.Condition)
	case NumericForStat:
		assertNameSpanValid(t, typed.Name)
		assertNodeSpanValid(t, typed.Initial)
		assertNodeSpanValid(t, typed.Limit)
		assertNodeSpanValid(t, typed.Step)
		assertNodeSpanValid(t, typed.Body)
	case GenericForStat:
		for _, name := range typed.Names {
			assertNameSpanValid(t, name)
		}
		for _, iterator := range typed.Iterators {
			assertNodeSpanValid(t, iterator)
		}
		assertNodeSpanValid(t, typed.Body)
	case DoStat:
		assertNodeSpanValid(t, typed.Body)
	case ReturnStat:
		for _, value := range typed.Values {
			assertNodeSpanValid(t, value)
		}
	case BoolExpr, NumberExpr, StringExpr:
		return
	case NameExpr:
		assertNameSpanValid(t, typed.Name)
	case UnaryExpr:
		assertNodeSpanValid(t, typed.Value)
	case BinaryExpr:
		assertNodeSpanValid(t, typed.Left)
		assertNodeSpanValid(t, typed.Right)
	case TableConstructorExpr:
		for _, field := range typed.Fields {
			if !field.Span.IsValid() {
				t.Fatalf("table field has invalid span %+v", field.Span)
			}
			assertNameSpanValid(t, field.Name)
			assertNodeSpanValid(t, field.Key)
			assertNodeSpanValid(t, field.Value)
		}
	case FunctionLiteralExpr:
		assertNodeSpanValid(t, typed.Body)
	case IndexExpr:
		assertNodeSpanValid(t, typed.Receiver)
		assertNodeSpanValid(t, typed.Index)
	case FieldExpr:
		assertNodeSpanValid(t, typed.Receiver)
		assertNameSpanValid(t, typed.Name)
	case MethodExpr:
		assertNodeSpanValid(t, typed.Receiver)
		assertNameSpanValid(t, typed.Name)
	case CallExpr:
		assertNodeSpanValid(t, typed.Callee)
		for _, arg := range typed.Args {
			assertNodeSpanValid(t, arg)
		}
	case MethodCallExpr:
		assertNodeSpanValid(t, typed.Receiver)
		assertNameSpanValid(t, typed.Name)
		for _, arg := range typed.Args {
			assertNodeSpanValid(t, arg)
		}
	case ParenExpr:
		assertNodeSpanValid(t, typed.Inner)
	default:
		t.Fatalf("unhandled node type %T", node)
	}
}

func assertNameSpanValid(t *testing.T, name Name) {
	t.Helper()
	if name.Text == "" && !name.Span.IsValid() {
		return
	}
	if !name.Span.IsValid() {
		t.Fatalf("name %q has invalid span %+v", name.Text, name.Span)
	}
}

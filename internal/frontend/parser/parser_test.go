package parser

import (
	"fmt"
	"strings"
	"testing"

	"vexlua/internal/frontend/lexer"
)

func TestParserShapeSnapshotCoversPhase2Skeleton(t *testing.T) {
	source := []byte(`local function build(a, ...)
  return foo.bar:baz({
    a,
    key = a.x,
    [1] = function(x) return x end,
  }, "ok")
end

result[1].name, target = build(1, 2), other
if result then
  do ; end
elseif not flag or value < limit then
  return result
else
  while flag do
    break
  end
end
`)
	chunk, err := ParseChunk("@shape.lua", source)
	if err != nil {
		t.Fatalf("ParseChunk: %v", err)
	}
	got := dumpChunk(chunk)
	want := strings.TrimSpace(`
Chunk(@shape.lua)
  Block(stats=3)
    LocalFunctionStat(name=build)
      FunctionBody(params=[a], vararg=true)
        Block(stats=1)
          ReturnStat(values=1)
            MethodCallExpr(name=baz, args=2)
              Receiver:
                FieldExpr(name=bar)
                  Receiver:
                    NameExpr(foo)
              Arg[0]:
                TableConstructorExpr(fields=3)
                  ArrayField:
                    NameExpr(a)
                  NamedField(key):
                    FieldExpr(name=x)
                      Receiver:
                        NameExpr(a)
                  IndexedField:
                    Key:
                      NumberExpr(1)
                    Value:
                      FunctionLiteralExpr
                        FunctionBody(params=[x], vararg=false)
                          Block(stats=1)
                            ReturnStat(values=1)
                              NameExpr(x)
              Arg[1]:
                StringExpr("ok")
    AssignmentStat(targets=2, values=2)
      Target[0]:
        FieldExpr(name=name)
          Receiver:
            IndexExpr
              Receiver:
                NameExpr(result)
              Index:
                NumberExpr(1)
      Target[1]:
        NameExpr(target)
      Value[0]:
        CallExpr(args=2)
          Callee:
            NameExpr(build)
          Arg[0]:
            NumberExpr(1)
          Arg[1]:
            NumberExpr(2)
      Value[1]:
        NameExpr(other)
    IfStat(clauses=2, else=true)
      Clause[0]:
        Condition:
          NameExpr(result)
        Body:
          Block(stats=1)
            DoStat
              Block(stats=1)
                EmptyStat
      Clause[1]:
        Condition:
          BinaryExpr(or)
            Left:
              UnaryExpr(not)
                NameExpr(flag)
            Right:
              BinaryExpr(<)
                Left:
                  NameExpr(value)
                Right:
                  NameExpr(limit)
        Body:
          Block(stats=1)
            ReturnStat(values=1)
              NameExpr(result)
      Else:
        Block(stats=1)
          WhileStat
            Condition:
              NameExpr(flag)
            Body:
              Block(stats=1)
                BreakStat`)
	if strings.TrimSpace(got) != want {
		t.Fatalf("snapshot mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestParserRecoversToNextStatement(t *testing.T) {
	chunk, err := ParseChunk("@recover.lua", []byte("local value =\nreturn value\n"))
	if err == nil {
		t.Fatalf("expected parse diagnostics")
	}
	diagErr, ok := err.(*lexer.DiagnosticError)
	if !ok {
		t.Fatalf("error type = %T, want *lexer.DiagnosticError", err)
	}
	primary, ok := diagErr.Primary()
	if !ok || primary.Phase != lexer.PhaseParse {
		t.Fatalf("primary diagnostic = %+v, %v, want parse-phase diagnostic", primary, ok)
	}
	if chunk == nil || chunk.Block == nil {
		t.Fatalf("partial chunk should still be returned")
	}
	if len(chunk.Block.Stats) != 1 {
		t.Fatalf("statement count = %d, want 1 recovered statement", len(chunk.Block.Stats))
	}
	if _, ok := chunk.Block.Stats[0].(ReturnStat); !ok {
		t.Fatalf("recovered statement = %T, want ReturnStat", chunk.Block.Stats[0])
	}
}

func TestParserDiagnosticFormatsExpectedTokens(t *testing.T) {
	_, err := ParseChunk("@format.lua", []byte("local = 1"))
	if err == nil {
		t.Fatalf("expected parse diagnostics")
	}
	diagErr, ok := err.(*lexer.DiagnosticError)
	if !ok {
		t.Fatalf("error type = %T, want *lexer.DiagnosticError", err)
	}
	primary, ok := diagErr.Primary()
	if !ok {
		t.Fatalf("missing primary diagnostic")
	}
	if primary.Message != "<name> expected" {
		t.Fatalf("primary message = %q, want %q", primary.Message, "<name> expected")
	}
}

func dumpChunk(chunk *Chunk) string {
	var builder strings.Builder
	writeLine(&builder, 0, "Chunk(%s)", chunk.Name)
	dumpBlock(&builder, 1, chunk.Block)
	return strings.TrimRight(builder.String(), "\n")
}

func dumpBlock(builder *strings.Builder, indent int, block *Block) {
	if block == nil {
		writeLine(builder, indent, "<nil block>")
		return
	}
	writeLine(builder, indent, "Block(stats=%d)", len(block.Stats))
	for _, stat := range block.Stats {
		dumpStat(builder, indent+1, stat)
	}
}

func dumpStat(builder *strings.Builder, indent int, stat Stat) {
	switch typed := stat.(type) {
	case EmptyStat:
		writeLine(builder, indent, "EmptyStat")
	case LocalFunctionStat:
		writeLine(builder, indent, "LocalFunctionStat(name=%s)", typed.Name.Text)
		dumpFunctionBody(builder, indent+1, typed.Body)
	case AssignmentStat:
		writeLine(builder, indent, "AssignmentStat(targets=%d, values=%d)", len(typed.Targets), len(typed.Values))
		for index, target := range typed.Targets {
			writeLine(builder, indent+1, "Target[%d]:", index)
			dumpExpr(builder, indent+2, target)
		}
		for index, value := range typed.Values {
			writeLine(builder, indent+1, "Value[%d]:", index)
			dumpExpr(builder, indent+2, value)
		}
	case ReturnStat:
		writeLine(builder, indent, "ReturnStat(values=%d)", len(typed.Values))
		for _, value := range typed.Values {
			dumpExpr(builder, indent+1, value)
		}
	case IfStat:
		writeLine(builder, indent, "IfStat(clauses=%d, else=%v)", len(typed.Clauses), typed.ElseBlock != nil)
		for index, clause := range typed.Clauses {
			writeLine(builder, indent+1, "Clause[%d]:", index)
			writeLine(builder, indent+2, "Condition:")
			dumpExpr(builder, indent+3, clause.Condition)
			writeLine(builder, indent+2, "Body:")
			dumpBlock(builder, indent+3, clause.Body)
		}
		if typed.ElseBlock != nil {
			writeLine(builder, indent+1, "Else:")
			dumpBlock(builder, indent+2, typed.ElseBlock)
		}
	case DoStat:
		writeLine(builder, indent, "DoStat")
		dumpBlock(builder, indent+1, typed.Body)
	case WhileStat:
		writeLine(builder, indent, "WhileStat")
		writeLine(builder, indent+1, "Condition:")
		dumpExpr(builder, indent+2, typed.Condition)
		writeLine(builder, indent+1, "Body:")
		dumpBlock(builder, indent+2, typed.Body)
	case BreakStat:
		writeLine(builder, indent, "BreakStat")
	default:
		writeLine(builder, indent, "%T", stat)
	}
}

func dumpFunctionBody(builder *strings.Builder, indent int, body *FunctionBody) {
	if body == nil {
		writeLine(builder, indent, "<nil body>")
		return
	}
	params := make([]string, 0, len(body.Params))
	for _, param := range body.Params {
		params = append(params, param.Text)
	}
	writeLine(builder, indent, "FunctionBody(params=%v, vararg=%v)", params, body.HasVararg)
	dumpBlock(builder, indent+1, body.Block)
}

func dumpExpr(builder *strings.Builder, indent int, expr Expr) {
	switch typed := expr.(type) {
	case NameExpr:
		writeLine(builder, indent, "NameExpr(%s)", typed.Name.Text)
	case NumberExpr:
		writeLine(builder, indent, "NumberExpr(%s)", typed.Raw)
	case StringExpr:
		writeLine(builder, indent, "StringExpr(%q)", typed.Value)
	case FieldExpr:
		writeLine(builder, indent, "FieldExpr(name=%s)", typed.Name.Text)
		writeLine(builder, indent+1, "Receiver:")
		dumpExpr(builder, indent+2, typed.Receiver)
	case IndexExpr:
		writeLine(builder, indent, "IndexExpr")
		writeLine(builder, indent+1, "Receiver:")
		dumpExpr(builder, indent+2, typed.Receiver)
		writeLine(builder, indent+1, "Index:")
		dumpExpr(builder, indent+2, typed.Index)
	case CallExpr:
		writeLine(builder, indent, "CallExpr(args=%d)", len(typed.Args))
		writeLine(builder, indent+1, "Callee:")
		dumpExpr(builder, indent+2, typed.Callee)
		for index, arg := range typed.Args {
			writeLine(builder, indent+1, "Arg[%d]:", index)
			dumpExpr(builder, indent+2, arg)
		}
	case MethodCallExpr:
		writeLine(builder, indent, "MethodCallExpr(name=%s, args=%d)", typed.Name.Text, len(typed.Args))
		writeLine(builder, indent+1, "Receiver:")
		dumpExpr(builder, indent+2, typed.Receiver)
		for index, arg := range typed.Args {
			writeLine(builder, indent+1, "Arg[%d]:", index)
			dumpExpr(builder, indent+2, arg)
		}
	case TableConstructorExpr:
		writeLine(builder, indent, "TableConstructorExpr(fields=%d)", len(typed.Fields))
		for _, field := range typed.Fields {
			switch field.Kind {
			case TableFieldArray:
				writeLine(builder, indent+1, "ArrayField:")
				dumpExpr(builder, indent+2, field.Value)
			case TableFieldNamed:
				writeLine(builder, indent+1, "NamedField(%s):", field.Name.Text)
				dumpExpr(builder, indent+2, field.Value)
			case TableFieldIndexed:
				writeLine(builder, indent+1, "IndexedField:")
				writeLine(builder, indent+2, "Key:")
				dumpExpr(builder, indent+3, field.Key)
				writeLine(builder, indent+2, "Value:")
				dumpExpr(builder, indent+3, field.Value)
			}
		}
	case FunctionLiteralExpr:
		writeLine(builder, indent, "FunctionLiteralExpr")
		dumpFunctionBody(builder, indent+1, typed.Body)
	case UnaryExpr:
		writeLine(builder, indent, "UnaryExpr(%s)", typed.Op)
		dumpExpr(builder, indent+1, typed.Value)
	case BinaryExpr:
		writeLine(builder, indent, "BinaryExpr(%s)", typed.Op)
		writeLine(builder, indent+1, "Left:")
		dumpExpr(builder, indent+2, typed.Left)
		writeLine(builder, indent+1, "Right:")
		dumpExpr(builder, indent+2, typed.Right)
	default:
		writeLine(builder, indent, "%T", expr)
	}
}

func writeLine(builder *strings.Builder, indent int, format string, args ...any) {
	builder.WriteString(strings.Repeat("  ", indent))
	builder.WriteString(fmt.Sprintf(format, args...))
	builder.WriteByte('\n')
}

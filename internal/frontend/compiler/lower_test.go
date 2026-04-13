package compiler

import (
	"fmt"
	"strings"
	"testing"
)

func TestLowerChunkPhase5Snapshot(t *testing.T) {
	chunk := bindSource(t, "@phase5.lua", []byte(`
local value = flag and obj:step(1, pack()) or limit < max
local tbl = {name = value, [slot] = extra, call(), last()}
return obj:step(value, pack())
`))

	got := dumpLoweredChunk(t, chunk)
	want := strings.TrimSpace(`
Chunk(@phase5.lua)
  Block(stats=3)
    LocalDeclStat(names=[value], values=1)
      Value[0]:
        LogicalExpr(or, results=single)
          Left:
            LogicalExpr(and, results=single)
              Left:
                SymbolExpr(flag)
              Right:
                CallExpr(method=step, results=single, args=2)
                  Receiver:
                    SymbolExpr(obj)
                  Arg[0]:
                    NumberExpr(1)
                  Arg[1]:
                    CallExpr(results=multi, args=0)
                      Callee:
                        SymbolExpr(pack)
          Right:
            CompareExpr(<)
              Left:
                SymbolExpr(limit)
              Right:
                SymbolExpr(max)
    LocalDeclStat(names=[tbl], values=1)
      Value[0]:
        TableExpr(array=2, hash=2, fields=4)
          NamedField(name)
          IndexedField
          ArrayField
          ArrayField(multret)
    ReturnStat(values=1)
      Value[0]:
        CallExpr(method=step, results=tail, args=2)
          Receiver:
            SymbolExpr(obj)
          Arg[0]:
            SymbolExpr(value)
          Arg[1]:
            CallExpr(results=multi, args=0)
              Callee:
                SymbolExpr(pack)`)
	if strings.TrimSpace(got) != want {
		t.Fatalf("snapshot mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestLowerChunkAddsLua51ForControlLocals(t *testing.T) {
	chunk := bindSource(t, "@for.lua", []byte(`
for i = 1, 3, 1 do
  consume(i)
end
for k, v in pairs(t) do
  consume(k, v)
end
`))

	numericFor, ok := chunk.Func.Body.Stats[0].(BoundNumericForStat)
	if !ok {
		t.Fatalf("statement 0 = %T, want BoundNumericForStat", chunk.Func.Body.Stats[0])
	}
	if symbolByID(t, chunk, numericFor.IndexSymbol).Name != numericForIndexName {
		t.Fatalf("numeric for index symbol = %q, want %q", symbolByID(t, chunk, numericFor.IndexSymbol).Name, numericForIndexName)
	}
	if symbolByID(t, chunk, numericFor.LimitSymbol).Name != numericForLimitName {
		t.Fatalf("numeric for limit symbol = %q, want %q", symbolByID(t, chunk, numericFor.LimitSymbol).Name, numericForLimitName)
	}
	if symbolByID(t, chunk, numericFor.StepSymbol).Name != numericForStepName {
		t.Fatalf("numeric for step symbol = %q, want %q", symbolByID(t, chunk, numericFor.StepSymbol).Name, numericForStepName)
	}
	if scopeByID(t, chunk, numericFor.Body.Scope).Symbols == nil || len(scopeByID(t, chunk, numericFor.Body.Scope).Symbols) < 4 {
		t.Fatalf("numeric for scope symbols = %+v, want control locals plus counter", scopeByID(t, chunk, numericFor.Body.Scope).Symbols)
	}

	genericFor, ok := chunk.Func.Body.Stats[1].(BoundGenericForStat)
	if !ok {
		t.Fatalf("statement 1 = %T, want BoundGenericForStat", chunk.Func.Body.Stats[1])
	}
	if symbolByID(t, chunk, genericFor.GeneratorSymbol).Name != genericForGenName {
		t.Fatalf("generic for generator symbol = %q, want %q", symbolByID(t, chunk, genericFor.GeneratorSymbol).Name, genericForGenName)
	}
	if symbolByID(t, chunk, genericFor.StateSymbol).Name != genericForStateName {
		t.Fatalf("generic for state symbol = %q, want %q", symbolByID(t, chunk, genericFor.StateSymbol).Name, genericForStateName)
	}
	if symbolByID(t, chunk, genericFor.ControlSymbol).Name != genericForControlName {
		t.Fatalf("generic for control symbol = %q, want %q", symbolByID(t, chunk, genericFor.ControlSymbol).Name, genericForControlName)
	}
	if len(genericFor.Names) != 2 {
		t.Fatalf("generic for names = %d, want 2", len(genericFor.Names))
	}
}

func TestLowerChunkNormalizesMethodCallsAndLogicalResultModes(t *testing.T) {
	chunk := bindSource(t, "@logical.lua", []byte(`
local one = flag and pack()
return flag and obj:step(pack())
`))

	assign, ok := chunk.Func.Body.Stats[0].(BoundLocalDeclStat)
	if !ok {
		t.Fatalf("statement 0 = %T, want BoundLocalDeclStat", chunk.Func.Body.Stats[0])
	}
	logicalAssign, ok := assign.Values[0].(BoundLogicalExpr)
	if !ok {
		t.Fatalf("assignment value = %T, want BoundLogicalExpr", assign.Values[0])
	}
	assignRightCall, ok := logicalAssign.Right.(BoundCallExpr)
	if !ok {
		t.Fatalf("assignment right expr = %T, want BoundCallExpr", logicalAssign.Right)
	}
	if assignRightCall.ResultMode() != ResultSingle {
		t.Fatalf("assignment right call mode = %d, want single", assignRightCall.ResultMode())
	}

	ret, ok := chunk.Func.Body.Stats[1].(BoundReturnStat)
	if !ok {
		t.Fatalf("statement 1 = %T, want BoundReturnStat", chunk.Func.Body.Stats[1])
	}
	logicalReturn, ok := ret.Values[0].(BoundLogicalExpr)
	if !ok {
		t.Fatalf("return value = %T, want BoundLogicalExpr", ret.Values[0])
	}
	methodCall, ok := logicalReturn.Right.(BoundCallExpr)
	if !ok {
		t.Fatalf("return right expr = %T, want BoundCallExpr", logicalReturn.Right)
	}
	if methodCall.MethodName != "step" || methodCall.Receiver == nil {
		t.Fatalf("lowered method call = %+v, want method receiver + method name", methodCall)
	}
	if methodCall.ResultMode() != ResultSingle {
		t.Fatalf("return method call mode = %d, want single", methodCall.ResultMode())
	}
	if len(methodCall.Args) != 1 {
		t.Fatalf("method call args = %d, want 1", len(methodCall.Args))
	}
	argCall, ok := methodCall.Args[0].(BoundCallExpr)
	if !ok {
		t.Fatalf("method call arg = %T, want BoundCallExpr", methodCall.Args[0])
	}
	if argCall.ResultMode() != ResultMulti {
		t.Fatalf("method call last arg mode = %d, want multi", argCall.ResultMode())
	}

	assertNoMethodCallExpr(t, chunk.Func)
}

func dumpLoweredChunk(t *testing.T, chunk *BoundChunk) string {
	t.Helper()
	var builder strings.Builder
	writeLowerLine(&builder, 0, "Chunk(%s)", chunk.Name)
	dumpLowerBlock(t, &builder, 1, chunk, chunk.Func.Body)
	return strings.TrimRight(builder.String(), "\n")
}

func dumpLowerBlock(t *testing.T, builder *strings.Builder, indent int, chunk *BoundChunk, block *BoundBlock) {
	t.Helper()
	if block == nil {
		writeLowerLine(builder, indent, "<nil block>")
		return
	}
	writeLowerLine(builder, indent, "Block(stats=%d)", len(block.Stats))
	for _, stat := range block.Stats {
		dumpLowerStat(t, builder, indent+1, chunk, stat)
	}
}

func dumpLowerStat(t *testing.T, builder *strings.Builder, indent int, chunk *BoundChunk, stat BoundStat) {
	t.Helper()
	switch typed := stat.(type) {
	case BoundLocalDeclStat:
		writeLowerLine(builder, indent, "LocalDeclStat(names=%v, values=%d)", lowerSymbolNames(chunk, typed.Names), len(typed.Values))
		for index, value := range typed.Values {
			writeLowerLine(builder, indent+1, "Value[%d]:", index)
			dumpLowerExpr(t, builder, indent+2, chunk, value)
		}
	case BoundReturnStat:
		writeLowerLine(builder, indent, "ReturnStat(values=%d)", len(typed.Values))
		for index, value := range typed.Values {
			writeLowerLine(builder, indent+1, "Value[%d]:", index)
			dumpLowerExpr(t, builder, indent+2, chunk, value)
		}
	case BoundNumericForStat:
		writeLowerLine(builder, indent, "NumericForStat(controls=%v)", lowerSymbolNames(chunk, []SymbolID{typed.IndexSymbol, typed.LimitSymbol, typed.StepSymbol, typed.Counter}))
		writeLowerLine(builder, indent+1, "Body:")
		dumpLowerBlock(t, builder, indent+2, chunk, typed.Body)
	case BoundGenericForStat:
		writeLowerLine(builder, indent, "GenericForStat(controls=%v)", lowerSymbolNames(chunk, []SymbolID{typed.GeneratorSymbol, typed.StateSymbol, typed.ControlSymbol}))
		writeLowerLine(builder, indent+1, "Names=%v", lowerSymbolNames(chunk, typed.Names))
		writeLowerLine(builder, indent+1, "Body:")
		dumpLowerBlock(t, builder, indent+2, chunk, typed.Body)
	case BoundCallStat:
		writeLowerLine(builder, indent, "CallStat")
		dumpLowerExpr(t, builder, indent+1, chunk, typed.Call)
	default:
		writeLowerLine(builder, indent, "%T", stat)
	}
}

func dumpLowerExpr(t *testing.T, builder *strings.Builder, indent int, chunk *BoundChunk, expr BoundExpr) {
	t.Helper()
	switch typed := expr.(type) {
	case BoundSymbolExpr:
		name := typed.Ref.GlobalName
		if typed.Ref.Symbol != InvalidSymbolID {
			name = symbolByID(t, chunk, typed.Ref.Symbol).Name
		}
		writeLowerLine(builder, indent, "SymbolExpr(%s)", name)
	case BoundNumberExpr:
		writeLowerLine(builder, indent, "NumberExpr(%s)", typed.Raw)
	case BoundLogicalExpr:
		writeLowerLine(builder, indent, "LogicalExpr(%s, results=%s)", typed.Op, lowerResultModeName(typed.ResultMode()))
		writeLowerLine(builder, indent+1, "Left:")
		dumpLowerExpr(t, builder, indent+2, chunk, typed.Left)
		writeLowerLine(builder, indent+1, "Right:")
		dumpLowerExpr(t, builder, indent+2, chunk, typed.Right)
	case BoundCompareExpr:
		writeLowerLine(builder, indent, "CompareExpr(%s)", typed.Op)
		writeLowerLine(builder, indent+1, "Left:")
		dumpLowerExpr(t, builder, indent+2, chunk, typed.Left)
		writeLowerLine(builder, indent+1, "Right:")
		dumpLowerExpr(t, builder, indent+2, chunk, typed.Right)
	case BoundCallExpr:
		if typed.Receiver != nil {
			writeLowerLine(builder, indent, "CallExpr(method=%s, results=%s, args=%d)", typed.MethodName, lowerResultModeName(typed.ResultMode()), len(typed.Args))
			writeLowerLine(builder, indent+1, "Receiver:")
			dumpLowerExpr(t, builder, indent+2, chunk, typed.Receiver)
		} else {
			writeLowerLine(builder, indent, "CallExpr(results=%s, args=%d)", lowerResultModeName(typed.ResultMode()), len(typed.Args))
			writeLowerLine(builder, indent+1, "Callee:")
			dumpLowerExpr(t, builder, indent+2, chunk, typed.Callee)
		}
		for index, arg := range typed.Args {
			writeLowerLine(builder, indent+1, "Arg[%d]:", index)
			dumpLowerExpr(t, builder, indent+2, chunk, arg)
		}
	case BoundTableExpr:
		writeLowerLine(builder, indent, "TableExpr(array=%d, hash=%d, fields=%d)", typed.ArrayCount, typed.HashCount, len(typed.Fields))
		for index, field := range typed.Fields {
			suffix := ""
			if field.Kind == BoundTableFieldArray && index == len(typed.Fields)-1 && field.Value != nil && field.Value.ResultMode() == ResultMulti {
				suffix = "(multret)"
			}
			switch field.Kind {
			case BoundTableFieldNamed:
				writeLowerLine(builder, indent+1, "NamedField(%s)%s", field.Name, suffix)
			case BoundTableFieldIndexed:
				writeLowerLine(builder, indent+1, "IndexedField%s", suffix)
			case BoundTableFieldArray:
				writeLowerLine(builder, indent+1, "ArrayField%s", suffix)
			}
		}
	default:
		writeLowerLine(builder, indent, "%T", expr)
	}
}

func lowerSymbolNames(chunk *BoundChunk, ids []SymbolID) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == InvalidSymbolID {
			names = append(names, "<invalid>")
			continue
		}
		index := int(id) - 1
		if index < 0 || index >= len(chunk.Symbols) {
			names = append(names, fmt.Sprintf("<symbol:%d>", id))
			continue
		}
		names = append(names, chunk.Symbols[index].Name)
	}
	return names
}

func lowerResultModeName(mode ResultMode) string {
	switch mode {
	case ResultSingle:
		return "single"
	case ResultMulti:
		return "multi"
	case ResultTail:
		return "tail"
	default:
		return fmt.Sprintf("ResultMode(%d)", mode)
	}
}

func writeLowerLine(builder *strings.Builder, indent int, format string, args ...any) {
	builder.WriteString(strings.Repeat("  ", indent))
	builder.WriteString(fmt.Sprintf(format, args...))
	builder.WriteByte('\n')
}

func assertNoMethodCallExpr(t *testing.T, function *BoundFunc) {
	t.Helper()
	if function == nil || function.Body == nil {
		return
	}
	for _, stat := range function.Body.Stats {
		assertNoMethodCallStat(t, stat)
	}
}

func assertNoMethodCallStat(t *testing.T, stat BoundStat) {
	t.Helper()
	switch typed := stat.(type) {
	case BoundLocalDeclStat:
		for _, value := range typed.Values {
			assertNoMethodCallExprNode(t, value)
		}
	case BoundAssignStat:
		for _, value := range typed.Values {
			assertNoMethodCallExprNode(t, value)
		}
	case BoundCallStat:
		assertNoMethodCallExprNode(t, typed.Call)
	case BoundIfStat:
		for _, clause := range typed.Clauses {
			assertNoMethodCallExprNode(t, clause.Condition)
			assertNoMethodCallExpr(t, &BoundFunc{Body: clause.Body})
		}
		assertNoMethodCallExpr(t, &BoundFunc{Body: typed.ElseBlock})
	case BoundWhileStat:
		assertNoMethodCallExprNode(t, typed.Condition)
		assertNoMethodCallExpr(t, &BoundFunc{Body: typed.Body})
	case BoundRepeatStat:
		assertNoMethodCallExpr(t, &BoundFunc{Body: typed.Body})
		assertNoMethodCallExprNode(t, typed.Condition)
	case BoundNumericForStat:
		assertNoMethodCallExprNode(t, typed.Initial)
		assertNoMethodCallExprNode(t, typed.Limit)
		assertNoMethodCallExprNode(t, typed.Step)
		assertNoMethodCallExpr(t, &BoundFunc{Body: typed.Body})
	case BoundGenericForStat:
		for _, iterator := range typed.Iterators {
			assertNoMethodCallExprNode(t, iterator)
		}
		assertNoMethodCallExpr(t, &BoundFunc{Body: typed.Body})
	case BoundDoStat:
		assertNoMethodCallExpr(t, &BoundFunc{Body: typed.Body})
	case BoundReturnStat:
		for _, value := range typed.Values {
			assertNoMethodCallExprNode(t, value)
		}
	}
}

func assertNoMethodCallExprNode(t *testing.T, expr BoundExpr) {
	t.Helper()
	switch typed := expr.(type) {
	case BoundMethodCallExpr:
		t.Fatalf("found unlowered BoundMethodCallExpr: %+v", typed)
	case BoundUnaryExpr:
		assertNoMethodCallExprNode(t, typed.Value)
	case BoundBinaryExpr:
		assertNoMethodCallExprNode(t, typed.Left)
		assertNoMethodCallExprNode(t, typed.Right)
	case BoundLogicalExpr:
		assertNoMethodCallExprNode(t, typed.Left)
		assertNoMethodCallExprNode(t, typed.Right)
	case BoundCompareExpr:
		assertNoMethodCallExprNode(t, typed.Left)
		assertNoMethodCallExprNode(t, typed.Right)
	case BoundTableExpr:
		for _, field := range typed.Fields {
			assertNoMethodCallExprNode(t, field.Key)
			assertNoMethodCallExprNode(t, field.Value)
		}
	case BoundFunctionExpr:
		assertNoMethodCallExpr(t, typed.Func)
	case BoundIndexExpr:
		assertNoMethodCallExprNode(t, typed.Receiver)
		assertNoMethodCallExprNode(t, typed.Index)
	case BoundFieldExpr:
		assertNoMethodCallExprNode(t, typed.Receiver)
	case BoundCallExpr:
		assertNoMethodCallExprNode(t, typed.Callee)
		assertNoMethodCallExprNode(t, typed.Receiver)
		for _, arg := range typed.Args {
			assertNoMethodCallExprNode(t, arg)
		}
	}
}

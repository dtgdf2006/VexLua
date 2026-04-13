package compiler

import (
	"fmt"
	"strings"
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/baseline"
)

func TestEmitChunkPhase6Snapshot(t *testing.T) {
	proto := compileSourceProto(t, "@phase6-snapshot.lua", `
local function pack(...)
  return ...
end

local function make(x)
  return function(y)
    return x + y
  end
end

local tbl = {1, pack(2, 3)}
local sum = 0
for i = 1, 2, 1 do
  sum = sum + i
end

return make(sum)(tbl[2])
`)

	got := normalizeSnapshot(dumpProtoTree(proto))
	want := normalizeSnapshot(`
Root: source=@phase6-snapshot.lua params=0 upvalues=0 vararg=2 max=8 consts=4 protos=2
  K0 = 1
  K1 = 2
  K2 = 3
  K3 = 0
  Code:
    [0000] CLOSURE A=0 Bx=0
    [0001] CLOSURE A=1 Bx=1
    [0002] NEWTABLE A=2 B=2 C=0
    [0003] LOADK A=3 Bx=0
    [0004] MOVE A=4 B=0
    [0005] LOADK A=5 Bx=1
    [0006] LOADK A=6 Bx=2
    [0007] CALL A=4 B=3 C=0
    [0008] SETLIST A=2 B=0 C=1
    [0009] LOADK A=3 Bx=3
    [0010] LOADK A=4 Bx=0
    [0011] LOADK A=5 Bx=1
    [0012] LOADK A=6 Bx=0
    [0013] FORPREP A=4 sBx=1
    [0014] ADD A=3 B=R3 C=R7
    [0015] FORLOOP A=4 sBx=-2
    [0016] MOVE A=4 B=1
    [0017] MOVE A=5 B=3
    [0018] CALL A=4 B=2 C=2
    [0019] GETTABLE A=5 B=2 C=K1
    [0020] TAILCALL A=4 B=2 C=0
    [0021] RETURN A=4 B=0
    [0022] RETURN A=0 B=1
  Child[0]: source=@phase6-snapshot.lua params=0 upvalues=0 vararg=2 max=2 consts=0 protos=0
    Code:
      [0000] VARARG A=0 B=0
      [0001] RETURN A=0 B=0
      [0002] RETURN A=0 B=1
  Child[1]: source=@phase6-snapshot.lua params=1 upvalues=0 vararg=0 max=2 consts=0 protos=1
    Code:
      [0000] CLOSURE A=1 Bx=0
      [0001] MOVE A=0 B=0
      [0002] RETURN A=1 B=2
      [0003] RETURN A=0 B=1
    Child[0]: source=@phase6-snapshot.lua params=1 upvalues=1 vararg=0 max=2 consts=0 protos=0
      Code:
        [0000] GETUPVAL A=1 B=0
        [0001] ADD A=1 B=R1 C=R0
        [0002] RETURN A=1 B=2
        [0003] RETURN A=0 B=1`)
	if got != want {
		t.Fatalf("snapshot mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestEmitChunkExecutesMainSyntaxFamilies(t *testing.T) {
	proto := compileSourceProto(t, "@phase6-main.lua", `
local function pack(...)
  return ...
end

local function make(start)
  local acc = start
  return function(step)
    acc = acc + step
    return acc
  end
end

local function iter(state, control)
  local next = control + 1
  if next <= state then
    return next, next * 10
  end
end

local obj = { value = 0 }
function obj:step(delta)
  self.value = self.value + delta
  return self.value
end

local sum = 0
for i = 1, 3, 1 do
  sum = sum + i
end

for k, v in iter, 2, 0 do
  sum = sum + k + v
end

local next = make(sum)
local tbl = {1, pack(2, 3, 4)}

while obj:step(1) < 2 do
end

repeat
  sum = sum - 1
until sum == 24

return next(5), tbl[1], tbl[2], tbl[3], tbl[4], obj.value
`)

	want := []value.TValue{
		value.NumberValue(44),
		value.NumberValue(1),
		value.NumberValue(2),
		value.NumberValue(3),
		value.NumberValue(4),
		value.NumberValue(2),
	}
	assertInterpreterResults(t, proto, want)
	assertBaselineResults(t, proto, want)
}

func TestEmitChunkClosesCapturedBreakLocals(t *testing.T) {
	proto := compileSourceProto(t, "@phase6-break-close.lua", `
local hold
while true do
  local current = 41
  hold = function()
    return current + 1
  end
  break
end

return hold()
`)

	want := []value.TValue{value.NumberValue(42)}
	assertInterpreterResults(t, proto, want)
	assertBaselineResults(t, proto, want)
}

func TestEmitChunkPhase7LineInfoRegression(t *testing.T) {
	proto := compileSourceProto(t, "@phase7-lines.lua", "local x = 1\nlocal y = x + 2\nif y > 2 then\n  y = y + 1\nend\nreturn y\n")

	if len(proto.LineInfo) != len(proto.Code) {
		t.Fatalf("line info size = %d, want %d", len(proto.LineInfo), len(proto.Code))
	}
	assertLinesPresent(t, proto.LineInfo, []int{1, 2, 3, 4, 6})
	if firstNonZeroLine(proto.LineInfo) != 1 {
		t.Fatalf("first non-zero line = %d, want 1", firstNonZeroLine(proto.LineInfo))
	}
	if lastNonZeroLine(proto.LineInfo) != 6 {
		t.Fatalf("last non-zero line = %d, want 6", lastNonZeroLine(proto.LineInfo))
	}
}

func TestEmitChunkPhase7LocVarLifetimeRegression(t *testing.T) {
	proto := compileSourceProto(t, "@phase7-locvars.lua", "local outer = 1\ndo\n  local inner = outer + 1\nend\nlocal after = outer + 2\nreturn after\n")

	if len(proto.LocVars) != 3 {
		t.Fatalf("locvar count = %d, want 3 (%+v)", len(proto.LocVars), proto.LocVars)
	}
	outer := requireLocVar(t, proto, "outer")
	inner := requireLocVar(t, proto, "inner")
	after := requireLocVar(t, proto, "after")
	if outer.StartPC <= 0 {
		t.Fatalf("outer start pc = %d, want > 0", outer.StartPC)
	}
	if inner.StartPC <= outer.StartPC {
		t.Fatalf("inner start pc = %d, want > outer start %d", inner.StartPC, outer.StartPC)
	}
	if inner.EndPC < inner.StartPC {
		t.Fatalf("inner range = [%d,%d), want a valid Lua 5.1 lifetime", inner.StartPC, inner.EndPC)
	}
	if inner.EndPC > after.StartPC {
		t.Fatalf("inner end pc = %d, want <= after start %d", inner.EndPC, after.StartPC)
	}
	if after.StartPC <= inner.StartPC {
		t.Fatalf("after start pc = %d, want > inner start %d", after.StartPC, inner.StartPC)
	}
	if outer.EndPC != after.EndPC {
		t.Fatalf("top-scope locals should close together: outer end=%d after end=%d", outer.EndPC, after.EndPC)
	}
	if after.EndPC <= after.StartPC {
		t.Fatalf("after range = [%d,%d), want a non-empty lifetime", after.StartPC, after.EndPC)
	}
}

func TestEmitChunkPhase7ChildProtoDebugRegression(t *testing.T) {
	proto := compileSourceProto(t, "@phase7-child.lua", "local up = 4\nlocal function make(arg)\n  local total = arg + up\n  return function(delta)\n    local sum = total + delta\n    return sum\n  end\nend\nreturn make(2)(3)\n")

	if len(proto.Protos) != 1 {
		t.Fatalf("root child proto count = %d, want 1", len(proto.Protos))
	}
	makeProto := proto.Protos[0]
	if makeProto.LineDefined != 2 || makeProto.LastLineDef != 8 {
		t.Fatalf("make proto lines = [%d,%d], want [2,8]", makeProto.LineDefined, makeProto.LastLineDef)
	}
	assertStringSliceEqual(t, makeProto.UpvalueNames, []string{"up"}, "make upvalue names")
	assertLocVarNames(t, makeProto.LocVars, []string{"arg", "total"})
	if len(makeProto.LineInfo) != len(makeProto.Code) {
		t.Fatalf("make proto line info size = %d, want %d", len(makeProto.LineInfo), len(makeProto.Code))
	}
	if len(makeProto.Protos) != 1 {
		t.Fatalf("make child proto count = %d, want 1", len(makeProto.Protos))
	}
	innerProto := makeProto.Protos[0]
	if innerProto.LineDefined != 4 || innerProto.LastLineDef != 7 {
		t.Fatalf("inner proto lines = [%d,%d], want [4,7]", innerProto.LineDefined, innerProto.LastLineDef)
	}
	assertStringSliceEqual(t, innerProto.UpvalueNames, []string{"total"}, "inner upvalue names")
	assertLocVarNames(t, innerProto.LocVars, []string{"delta", "sum"})
	if len(innerProto.LineInfo) != len(innerProto.Code) {
		t.Fatalf("inner proto line info size = %d, want %d", len(innerProto.LineInfo), len(innerProto.Code))
	}
}

func compileSourceProto(t *testing.T, name string, source string) *bytecode.Proto {
	t.Helper()
	proto, err := Compile(name, []byte(source))
	if err != nil {
		t.Fatalf("Compile(%s): %v", name, err)
	}
	if proto == nil {
		t.Fatalf("Compile(%s) returned nil proto", name)
	}
	if err := bytecode.ValidateProto(proto); err != nil {
		t.Fatalf("ValidateProto(%s): %v", name, err)
	}
	return proto
}

func assertInterpreterResults(t *testing.T, proto *bytecode.Proto, want []value.TValue) {
	t.Helper()
	engine := interp.New()
	thread, err := engine.NewThread(4096, 128)
	if err != nil {
		t.Fatalf("engine.NewThread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("engine.NewTable: %v", err)
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("engine.NewClosure: %v", err)
	}
	got, err := engine.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("engine.Call: %v", err)
	}
	assertValuesEqual(t, got, want)
}

func assertBaselineResults(t *testing.T, proto *bytecode.Proto, want []value.TValue) {
	t.Helper()
	engine := interp.New()
	runtime := baseline.NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	compiled, err := runtime.Compile(proto)
	if err != nil {
		t.Fatalf("runtime.Compile: %v", err)
	}
	if compiled == nil || !compiled.Supported {
		t.Fatalf("compiled proto is unsupported: %+v", compiled)
	}
	thread, err := engine.NewThread(4096, 128)
	if err != nil {
		t.Fatalf("engine.NewThread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("engine.NewTable: %v", err)
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("engine.NewClosure: %v", err)
	}
	got, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime.Call: %v", err)
	}
	assertValuesEqual(t, got, want)
}

func assertValuesEqual(t *testing.T, got []value.TValue, want []value.TValue) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("result count = %d, want %d\n got=%v\nwant=%v", len(got), len(want), got, want)
	}
	for index := range want {
		if got[index].Bits() != want[index].Bits() {
			t.Fatalf("result[%d] = %s, want %s", index, got[index], want[index])
		}
	}
}

func dumpProtoTree(proto *bytecode.Proto) string {
	var builder strings.Builder
	dumpProto(&builder, 0, "Root", proto)
	return strings.TrimRight(builder.String(), "\n")
}

func dumpProto(builder *strings.Builder, indent int, label string, proto *bytecode.Proto) {
	writeProtoLine(builder, indent, "%s: source=%s params=%d upvalues=%d vararg=%d max=%d consts=%d protos=%d", label, proto.Source, proto.NumParams, proto.NumUpvalues, proto.IsVararg, proto.MaxStackSize, len(proto.Constants), len(proto.Protos))
	for index, constant := range proto.Constants {
		writeProtoLine(builder, indent+1, "K%d = %s", index, constant)
	}
	writeProtoLine(builder, indent+1, "Code:")
	for _, line := range strings.Split(bytecode.DumpCode(proto.Code), "\n") {
		writeProtoLine(builder, indent+2, "%s", line)
	}
	for index, child := range proto.Protos {
		dumpProto(builder, indent+1, fmt.Sprintf("Child[%d]", index), child)
	}
}

func writeProtoLine(builder *strings.Builder, indent int, format string, args ...any) {
	builder.WriteString(strings.Repeat("  ", indent))
	builder.WriteString(fmt.Sprintf(format, args...))
	builder.WriteByte('\n')
}

func requireLocVar(t *testing.T, proto *bytecode.Proto, name string) bytecode.LocVar {
	t.Helper()
	for _, local := range proto.LocVars {
		if local.Name == name {
			return local
		}
	}
	t.Fatalf("locvar %q not found in %+v", name, proto.LocVars)
	return bytecode.LocVar{}
}

func assertLocVarNames(t *testing.T, locals []bytecode.LocVar, want []string) {
	t.Helper()
	if len(locals) != len(want) {
		t.Fatalf("locvar count = %d, want %d (%+v)", len(locals), len(want), locals)
	}
	for index, name := range want {
		if locals[index].Name != name {
			t.Fatalf("locvar[%d] = %q, want %q", index, locals[index].Name, name)
		}
	}
}

func assertStringSliceEqual(t *testing.T, got []string, want []string, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s count = %d, want %d (%v)", label, len(got), len(want), got)
	}
	for index, item := range want {
		if got[index] != item {
			t.Fatalf("%s[%d] = %q, want %q", label, index, got[index], item)
		}
	}
}

func assertLinesPresent(t *testing.T, lines []int, want []int) {
	t.Helper()
	seen := make(map[int]bool, len(lines))
	for _, line := range lines {
		seen[line] = true
	}
	for _, line := range want {
		if !seen[line] {
			t.Fatalf("line info missing line %d in %v", line, lines)
		}
	}
}

func firstNonZeroLine(lines []int) int {
	for _, line := range lines {
		if line != 0 {
			return line
		}
	}
	return 0
}

func lastNonZeroLine(lines []int) int {
	for index := len(lines) - 1; index >= 0; index-- {
		if lines[index] != 0 {
			return lines[index]
		}
	}
	return 0
}

func normalizeSnapshot(text string) string {
	text = strings.ReplaceAll(text, "\t", "  ")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	indent := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		current := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == -1 || current < indent {
			indent = current
		}
	}
	if indent <= 0 {
		return strings.TrimSpace(text)
	}
	for index, line := range lines {
		if len(line) >= indent {
			lines[index] = line[indent:]
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
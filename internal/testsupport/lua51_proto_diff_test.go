package testsupport_test

import (
	"fmt"
	"math"
	"testing"

	"vexlua/internal/bytecode"
)

func TestLua51ProtoDiffArithmeticControl(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceProtoMatches(t, "@proto-diff-arith.lua", "local a = 1\nlocal b = 2\nif a < b then\n  a = a + b\nend\nreturn a\n")
}

func TestLua51ProtoDiffClosureAndDebug(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceProtoMatches(t, "@proto-diff-closure.lua", "local function make(x)\n  local y = x + 1\n  return function(z)\n    return y + z\n  end\nend\nreturn make(40)(2)\n")
}

func TestLua51ProtoDiffTableMethodAndForLoop(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceProtoMatches(t, "@proto-diff-table.lua", "local t = {11, 22}\nfunction t:id()\n  return self[2]\nend\nfor i = 1, 2 do\n  t[i] = t[i] + 1\nend\nreturn t:id()\n")
}

func (h *lua51DiffHarness) assertSourceProtoMatches(t *testing.T, name string, source string) {
	t.Helper()
	if h.luacPath == "" {
		t.Skip("luac5.1 not found on PATH")
	}
	want := h.compileReferenceProto(t, name, source)
	got := h.compileCurrentProto(t, name, source)
	assertProtoTreeEqual(t, got, want, "Root")
}

func assertProtoTreeEqual(t *testing.T, got *bytecode.Proto, want *bytecode.Proto, path string) {
	t.Helper()
	if got == nil || want == nil {
		if got != want {
			t.Fatalf("%s nil mismatch: got=%v want=%v", path, got == nil, want == nil)
		}
		return
	}
	assertIntEqual(t, path+".LineDefined", got.LineDefined, want.LineDefined)
	assertIntEqual(t, path+".LastLineDef", got.LastLineDef, want.LastLineDef)
	assertIntEqual(t, path+".NumParams", int(got.NumParams), int(want.NumParams))
	assertIntEqual(t, path+".IsVararg", int(got.IsVararg), int(want.IsVararg))
	assertIntEqual(t, path+".MaxStackSize", int(got.MaxStackSize), int(want.MaxStackSize))
	assertIntEqual(t, path+".NumUpvalues", int(got.NumUpvalues), int(want.NumUpvalues))
	assertInstructionSliceEqual(t, path, got.Code, want.Code)
	assertConstantSliceEqual(t, path, got.Constants, want.Constants)
	assertIntSliceExact(t, path+".LineInfo", got.LineInfo, want.LineInfo)
	assertLocVarSliceEqual(t, path+".LocVars", got.LocVars, want.LocVars)
	assertStringSliceExact(t, path+".UpvalueNames", got.UpvalueNames, want.UpvalueNames)
	assertIntEqual(t, path+".ChildCount", len(got.Protos), len(want.Protos))
	for index := range want.Protos {
		assertProtoTreeEqual(t, got.Protos[index], want.Protos[index], fmt.Sprintf("%s.Child[%d]", path, index))
	}
}

func assertInstructionSliceEqual(t *testing.T, path string, got []bytecode.Instruction, want []bytecode.Instruction) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s.Code length mismatch: got=%d want=%d\n--- got ---\n%s\n--- want ---\n%s", path, len(got), len(want), bytecode.DumpCode(got), bytecode.DumpCode(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s.Code[%d] mismatch:\n got  %s\n want %s\n--- got full ---\n%s\n--- want full ---\n%s", path, index, bytecode.FormatInstructionAt(index, got[index]), bytecode.FormatInstructionAt(index, want[index]), bytecode.DumpCode(got), bytecode.DumpCode(want))
		}
	}
}

func assertConstantSliceEqual(t *testing.T, path string, got []bytecode.Constant, want []bytecode.Constant) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s.Constants length mismatch: got=%d want=%d\n got=%v\nwant=%v", path, len(got), len(want), got, want)
	}
	for index := range want {
		if !constantEqual(got[index], want[index]) {
			t.Fatalf("%s.Constants[%d] mismatch: got=%v want=%v", path, index, got[index], want[index])
		}
	}
}

func constantEqual(left bytecode.Constant, right bytecode.Constant) bool {
	if left.Kind != right.Kind || left.Boolean != right.Boolean || left.Text != right.Text {
		return false
	}
	if left.Kind != bytecode.ConstantNumber {
		return left.Number == right.Number
	}
	return left.Number == right.Number || (math.IsNaN(left.Number) && math.IsNaN(right.Number))
}

func assertLocVarSliceEqual(t *testing.T, path string, got []bytecode.LocVar, want []bytecode.LocVar) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length mismatch: got=%d want=%d\n got=%+v\nwant=%+v", path, len(got), len(want), got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s[%d] mismatch: got=%+v want=%+v", path, index, got[index], want[index])
		}
	}
}

func assertStringSliceExact(t *testing.T, path string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length mismatch: got=%d want=%d\n got=%v\nwant=%v", path, len(got), len(want), got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s[%d] mismatch: got=%q want=%q", path, index, got[index], want[index])
		}
	}
}

func assertIntSliceExact(t *testing.T, path string, got []int, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length mismatch: got=%d want=%d\n got=%v\nwant=%v", path, len(got), len(want), got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s[%d] mismatch: got=%d want=%d", path, index, got[index], want[index])
		}
	}
}

func assertIntEqual(t *testing.T, path string, got int, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s mismatch: got=%d want=%d", path, got, want)
	}
}
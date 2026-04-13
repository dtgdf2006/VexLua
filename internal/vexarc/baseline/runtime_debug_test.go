package baseline

import (
	"testing"

	"vexlua/internal/bytecode"
	frontendcompiler "vexlua/internal/frontend/compiler"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/value"
)

func TestRuntimeCompileRetainsSourceDebugMetadata(t *testing.T) {
	proto, err := frontendcompiler.Compile("@phase7-runtime.lua", []byte("local up = 4\nlocal function make(arg)\n  local total = arg + up\n  return function(delta)\n    local sum = total + delta\n    return sum\n  end\nend\nreturn make(2)(3)\n"))
	if err != nil {
		t.Fatalf("compiler.Compile: %v", err)
	}
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	compiled, err := runtime.Compile(proto)
	if err != nil {
		t.Fatalf("runtime.Compile: %v", err)
	}
	if compiled == nil || compiled.ProtoRef == 0 {
		t.Fatalf("compiled proto ref = %#x, want non-zero", compiled.ProtoRef)
	}
	assertRuntimeDebugTreeMatches(t, engine, compiled.ProtoRef, proto)
}

func assertRuntimeDebugTreeMatches(t *testing.T, engine *interp.Engine, ref value.HeapRef44, want *bytecode.Proto) {
	t.Helper()
	lines, err := engine.Protos.LineInfo(ref)
	if err != nil {
		t.Fatalf("LineInfo(%#x): %v", uint64(ref), err)
	}
	assertIntSliceEqual(t, lines, want.LineInfo, "line info")
	locals, err := engine.Protos.LocVars(ref)
	if err != nil {
		t.Fatalf("LocVars(%#x): %v", uint64(ref), err)
	}
	assertLocVarsEqual(t, locals, want.LocVars)
	names, err := engine.Protos.UpvalueNames(ref)
	if err != nil {
		t.Fatalf("UpvalueNames(%#x): %v", uint64(ref), err)
	}
	assertStringSliceEqualRuntime(t, names, want.UpvalueNames, "upvalue names")
	childRefs, err := engine.Protos.ChildProtoRefs(ref)
	if err != nil {
		t.Fatalf("ChildProtoRefs(%#x): %v", uint64(ref), err)
	}
	if len(childRefs) != len(want.Protos) {
		t.Fatalf("child proto ref count = %d, want %d", len(childRefs), len(want.Protos))
	}
	for index, childRef := range childRefs {
		assertRuntimeDebugTreeMatches(t, engine, childRef, want.Protos[index])
	}
}

func assertIntSliceEqual(t *testing.T, got []int, want []int, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s count = %d, want %d", label, len(got), len(want))
	}
	for index, item := range want {
		if got[index] != item {
			t.Fatalf("%s[%d] = %d, want %d", label, index, got[index], item)
		}
	}
}

func assertLocVarsEqual(t *testing.T, got []bytecode.LocVar, want []bytecode.LocVar) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("locvar count = %d, want %d", len(got), len(want))
	}
	for index, item := range want {
		if got[index] != item {
			t.Fatalf("locvar[%d] = %+v, want %+v", index, got[index], item)
		}
	}
}

func assertStringSliceEqualRuntime(t *testing.T, got []string, want []string, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s count = %d, want %d", label, len(got), len(want))
	}
	for index, item := range want {
		if got[index] != item {
			t.Fatalf("%s[%d] = %q, want %q", label, index, got[index], item)
		}
	}
}
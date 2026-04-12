package interp

import (
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/value"
)

func TestEngineExternalRootsRetainAndReleaseAPIResults(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	tableHandle, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	if got := engine.State.ExternalRoots().RefCount(tableHandle.Ref); got != 1 {
		t.Fatalf("new table refcount = %d, want 1", got)
	}
	if err := engine.SetGlobal(env.Value, "t", tableHandle.Value); err != nil {
		t.Fatalf("set global: %v", err)
	}
	globalValue, found, err := engine.GetGlobal(env.Value, "t")
	if err != nil {
		t.Fatalf("get global: %v", err)
	}
	if !found {
		t.Fatalf("expected global value to be found")
	}
	if got := engine.State.ExternalRoots().RefCount(tableHandle.Ref); got != 2 {
		t.Fatalf("global lookup refcount = %d, want 2", got)
	}
	if err := engine.ReleaseValue(globalValue); err != nil {
		t.Fatalf("release global value: %v", err)
	}
	if got := engine.State.ExternalRoots().RefCount(tableHandle.Ref); got != 1 {
		t.Fatalf("refcount after global release = %d, want 1", got)
	}
	proto := &bytecode.Proto{
		Source:       "@external-roots.lua",
		MaxStackSize: 1,
		Constants:    []bytecode.Constant{bytecode.StringConstant("t")},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := engine.Call(thread, closureHandle.Value, nil, 1)
	if err != nil {
		t.Fatalf("call closure: %v", err)
	}
	if len(results) != 1 || results[0].Bits() != value.TableRefValue(tableHandle.Ref).Bits() {
		t.Fatalf("call results = %#v, want table ref %#x", results, uint64(tableHandle.Ref))
	}
	if got := engine.State.ExternalRoots().RefCount(tableHandle.Ref); got != 2 {
		t.Fatalf("call result refcount = %d, want 2", got)
	}
	if err := engine.ReleaseValues(results); err != nil {
		t.Fatalf("release call results: %v", err)
	}
	if got := engine.State.ExternalRoots().RefCount(tableHandle.Ref); got != 1 {
		t.Fatalf("refcount after result release = %d, want 1", got)
	}
	if err := engine.ReleaseRef(tableHandle.Ref); err != nil {
		t.Fatalf("release original table handle: %v", err)
	}
	if got := engine.State.ExternalRoots().RefCount(tableHandle.Ref); got != 0 {
		t.Fatalf("final table refcount = %d, want 0", got)
	}
}

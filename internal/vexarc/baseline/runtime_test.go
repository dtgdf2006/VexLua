package baseline

import (
	"math"
	"testing"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/feedback"
	rtheap "vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/host"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/abi"
	"vexlua/internal/vexarc/amd64"
	"vexlua/internal/vexarc/codecache"
	"vexlua/internal/vexarc/stubs"
)

type safepointAssist struct {
	count int
}

func (assist *safepointAssist) AssistAllocation(uint64) error {
	return nil
}

func (assist *safepointAssist) AssistSafepoint() error {
	assist.count++
	return nil
}

func TestCompiledEntryTrampoline(t *testing.T) {
	assembler := amd64.NewAssembler(16)
	assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	assembler.MoveRegImm32(amd64.RegRDX, 7)
	assembler.Ret()
	cache := codecache.New()
	block, err := cache.Install(assembler.Buffer().Bytes())
	if err != nil {
		t.Fatalf("install executable block: %v", err)
	}
	defer func() {
		_ = cache.Release(block)
	}()
	registers := []value.TValue{value.NilValue()}
	results := []value.TValue{value.NilValue()}
	frame := &state.CallFrameHeader{
		Closure:       value.NilValue(),
		Proto:         value.NilValue(),
		RegsBase:      uint64(uintptr(unsafe.Pointer(&registers[0]))),
		ResultBase:    uint64(uintptr(unsafe.Pointer(&results[0]))),
		Flags:         state.FrameFlagIsLuaFrame,
		RegisterCount: 1,
	}
	ctx := executionContext{}
	status, aux := abi.EnterCompiled(block.Address(), 0, nil, unsafe.Pointer(frame), uintptr(unsafe.Pointer(&registers[0])), unsafe.Pointer(&ctx))
	if status != compiledStatusOK || aux != 7 {
		t.Fatalf("unexpected trampoline result status=%d aux=%d", status, aux)
	}
}

func TestBaselineRuntimeExecutesSimpleLowerings(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-simple.lua",
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(42),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 1, 0, 0),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 2, 1, 0),
			bytecode.CreateABC(bytecode.OP_LOADNIL, 2, 3, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 5, 0),
		},
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closureHandle.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42), value.NumberValue(42), value.NilValue(), value.NilValue()})
}

func TestBaselineRuntimeLoadKReadsNativeConstBase(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	expected, err := engine.InternString("hello")
	if err != nil {
		t.Fatalf("intern string: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-const.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("hello"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{expected.Value})
}

func TestBaselineRuntimeNewTableStubResumesCompiledContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-newtable.lua",
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_NEWTABLE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_LEN, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(0)})
	if runtime.SlowStubCount(stubs.StubNewTable) != 1 {
		t.Fatalf("NEWTABLE should use exactly one runtime stub, got %d", runtime.SlowStubCount(stubs.StubNewTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("NEWTABLE continuation should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeConcatStubResumesCompiledContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	expected, err := engine.InternString("hello")
	if err != nil {
		t.Fatalf("intern expected string: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-concat.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("he"),
			bytecode.StringConstant("llo"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_CONCAT, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{expected.Value})
	if runtime.SlowStubCount(stubs.StubConcat) != 1 {
		t.Fatalf("CONCAT should use exactly one runtime stub, got %d", runtime.SlowStubCount(stubs.StubConcat))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("CONCAT continuation should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCloseStubResumesCompiledContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	upvalueSlot, err := thread.SlotAddress(0)
	if err != nil {
		t.Fatalf("upvalue slot address: %v", err)
	}
	open, err := engine.Upvalues.FindOrCreateOpen(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-close.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(99),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CLOSE, 0, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{value.NumberValue(40)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("CLOSE test should return no values, got %#v", results)
	}
	closedValue, err := engine.Upvalues.Get(open.Ref)
	if err != nil {
		t.Fatalf("read closed upvalue: %v", err)
	}
	if closedValue.Bits() != value.NumberValue(40).Bits() {
		t.Fatalf("closed upvalue value = %s, want %s", closedValue, value.NumberValue(40))
	}
	if runtime.SlowStubCount(stubs.StubClose) != 1 {
		t.Fatalf("CLOSE should use exactly one runtime stub, got %d", runtime.SlowStubCount(stubs.StubClose))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("CLOSE continuation should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeClosureStubResumesCompiledContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	upvalueSlot, err := thread.SlotAddress(0)
	if err != nil {
		t.Fatalf("upvalue slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(upvalueSlot, value.NumberValue(7)); err != nil {
		t.Fatalf("seed outer upvalue slot: %v", err)
	}
	open, err := engine.Upvalues.FindOrCreateOpen(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("open outer upvalue: %v", err)
	}
	closed, err := engine.CloseUpvaluesBoundary(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("close outer upvalue: %v", err)
	}
	if len(closed) != 1 || closed[0].Ref != open.Ref {
		t.Fatalf("closed outer upvalues = %#v, want [%#x]", closed, uint64(open.Ref))
	}
	childProto := &bytecode.Proto{
		Source:       "@jit-closure-child.lua",
		NumUpvalues:  2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 1, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 3, 0),
		},
	}
	proto := &bytecode.Proto{
		Source:       "@jit-closure.lua",
		NumUpvalues:  1,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(10),
			bytecode.NumberConstant(20),
		},
		Protos: []*bytecode.Proto{childProto},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_CLOSURE, 1, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 0, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, []value.HeapRef44{closed[0].Ref})
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	beforeStub := runtime.SlowStubCount(stubs.StubClosure)
	beforeDeopt := runtime.DeoptCount()
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	if len(results) != 1 || !results[0].IsBoxedTag(value.TagLuaClosureRef) {
		t.Fatalf("closure result = %#v, want one Lua closure", results)
	}
	if got := runtime.SlowStubCount(stubs.StubClosure) - beforeStub; got != 1 {
		t.Fatalf("CLOSURE should use exactly one runtime stub, got %d", got)
	}
	if runtime.DeoptCount() != beforeDeopt {
		t.Fatalf("CLOSURE continuation should avoid deopt, got %d", runtime.DeoptCount()-beforeDeopt)
	}
	childResults, err := runtime.Call(thread, results[0], nil, -1)
	if err != nil {
		t.Fatalf("runtime call child closure: %v", err)
	}
	assertValuesEqual(t, childResults, []value.TValue{value.NumberValue(20), value.NumberValue(7)})
	if runtime.DeoptCount() != beforeDeopt {
		t.Fatalf("child closure should still avoid deopt, got %d", runtime.DeoptCount()-beforeDeopt)
	}
}

func TestBaselineRuntimeCachesCompiledProtoByRef(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-cache.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(42),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	handle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	if len(runtime.compiled) != 0 {
		t.Fatalf("compiled cache should start empty, got %d entries", len(runtime.compiled))
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	compiledFirst, ok := runtime.compiled[handle.Ref]
	if !ok || compiledFirst == nil {
		t.Fatalf("compiled cache should contain proto %#x after first call", uint64(handle.Ref))
	}
	firstEntry := compiledFirst.Entry
	if compiledFirst.ProtoRef != handle.Ref {
		t.Fatalf("compiled proto ref %#x, want %#x", uint64(compiledFirst.ProtoRef), uint64(handle.Ref))
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	compiledSecond, err := runtime.CompileRef(handle.Ref)
	if err != nil {
		t.Fatalf("compile ref: %v", err)
	}
	if compiledSecond != compiledFirst {
		t.Fatalf("compile ref should reuse cached compiled code")
	}
	if compiledSecond.Entry != firstEntry {
		t.Fatalf("cached compiled entry %#x, want %#x", compiledSecond.Entry, firstEntry)
	}
	if len(runtime.compiled) != 1 {
		t.Fatalf("compiled cache should retain one entry, got %d", len(runtime.compiled))
	}
}

func TestBaselineRuntimeCallStubResumesContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	calleeProto := &bytecode.Proto{
		Source:       "@callee.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee: %v", err)
	}
	if _, err := runtime.Compile(calleeProto); err != nil {
		t.Fatalf("compile callee: %v", err)
	}
	callerProto := &bytecode.Proto{
		Source:       "@caller.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	caller, err := engine.NewClosure(callerProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new caller: %v", err)
	}
	results, err := runtime.Call(thread, caller.Value, []value.TValue{callee.Value, value.NumberValue(99)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(99)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("covered Lua-to-Lua call should stay inside native call builtin, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("call continuation should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCoveredCallBoundaryAdvancesGCSafepoint(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	calleeProto := &bytecode.Proto{
		Source:       "@call-safepoint-callee.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee: %v", err)
	}
	if _, err := runtime.Compile(calleeProto); err != nil {
		t.Fatalf("compile callee: %v", err)
	}
	callerProto := &bytecode.Proto{
		Source:       "@call-safepoint-caller.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	caller, err := engine.NewClosure(callerProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new caller: %v", err)
	}
	assist := &safepointAssist{}
	engine.SetAllocationAssistant(assist)
	engine.Heap.SetGCThreshold(1)
	if _, err := engine.InternString("call-safepoint-trigger"); err != nil {
		t.Fatalf("intern trigger string: %v", err)
	}
	if !engine.Heap.GCTargetReached() {
		t.Fatalf("gc target should be reached before compiled call")
	}
	results, err := runtime.Call(thread, caller.Value, []value.TValue{callee.Value, value.NumberValue(99)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(99)})
	if assist.count != 1 {
		t.Fatalf("covered call safepoint count = %d, want 1", assist.count)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("covered call safepoint should avoid shared call stubs, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("covered call safepoint should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCallStubChainsAcrossCompiledFrames(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	leafProto := &bytecode.Proto{
		Source:       "@leaf.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	leaf, err := engine.NewClosure(leafProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new leaf: %v", err)
	}
	if _, err := runtime.Compile(leafProto); err != nil {
		t.Fatalf("compile leaf: %v", err)
	}
	midProto := &bytecode.Proto{
		Source:       "@mid.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	mid, err := engine.NewClosure(midProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new mid: %v", err)
	}
	if _, err := runtime.Compile(midProto); err != nil {
		t.Fatalf("compile mid: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@top.lua",
		NumParams:    3,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 3, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	top, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top: %v", err)
	}
	results, err := runtime.Call(thread, top.Value, []value.TValue{mid.Value, leaf.Value, value.NumberValue(77)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(77)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("covered compiled call chain should stay inside native call builtin, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("compiled call chain should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeNestedCompiledCallCanReachHostBoundary(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	addOne, err := engine.RegisterHostFunction("add1", func(v float64) float64 {
		return v + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register host function: %v", err)
	}
	midProto := &bytecode.Proto{
		Source:       "@mid-host-call.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	mid, err := engine.NewClosure(midProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new mid closure: %v", err)
	}
	if _, err := runtime.Compile(midProto); err != nil {
		t.Fatalf("compile mid: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@top-host-call.lua",
		NumParams:    3,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 3, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	top, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, top.Value, []value.TValue{mid.Value, addOne.Value, value.NumberValue(41)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("nested host boundary should use exactly one terminal call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("nested host boundary should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeDirectCompiledCallCopiesMultipleArguments(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	calleeProto := &bytecode.Proto{
		Source:       "@copy-args-callee.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 3, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee closure: %v", err)
	}
	if _, err := runtime.Compile(calleeProto); err != nil {
		t.Fatalf("compile callee: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@copy-args-top.lua",
		NumParams:    3,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 3, 3),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 3, 0),
		},
	}
	top, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, top.Value, []value.TValue{callee.Value, value.NumberValue(11), value.NumberValue(22)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(11), value.NumberValue(22)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("direct compiled call argument copy test should stay inside native call builtin, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
}

func TestBaselineRuntimeDirectCompiledCallSupportsOpenArgs(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	calleeProto := &bytecode.Proto{
		Source:       "@open-call-args-callee.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee closure: %v", err)
	}
	if _, err := runtime.Compile(calleeProto); err != nil {
		t.Fatalf("compile callee: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@open-call-args-top.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 0, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	top, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, top.Value, []value.TValue{callee.Value, value.NumberValue(77)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(77)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("open CALL args should stay inside native call builtin, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("open CALL args should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeDirectCompiledCallSupportsOpenResultsAndOpenReturn(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	calleeProto := &bytecode.Proto{
		Source:       "@open-call-results-callee.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 0, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee closure: %v", err)
	}
	if _, err := runtime.Compile(calleeProto); err != nil {
		t.Fatalf("compile callee: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@open-call-results-top.lua",
		NumParams:    3,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 3, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 0, 0),
		},
	}
	top, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, top.Value, []value.TValue{callee.Value, value.NumberValue(11), value.NumberValue(22)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(11), value.NumberValue(22)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("open CALL results should stay inside native call builtin, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("open CALL results should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeOpenReturnUsesUpdatedTopAfterFixedCall(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	calleeProto := &bytecode.Proto{
		Source:       "@fixed-call-open-return-callee.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee closure: %v", err)
	}
	if _, err := runtime.Compile(calleeProto); err != nil {
		t.Fatalf("compile callee: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@fixed-call-open-return-top.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 0, 0),
		},
	}
	top, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, top.Value, []value.TValue{callee.Value, value.NumberValue(41)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("fixed CALL followed by open RETURN should stay inside native call builtin, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("fixed CALL followed by open RETURN should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeOpenReturnUsesUpdatedTopAfterGetUpvalueBuiltin(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	upvalueSlot, err := thread.SlotAddress(8)
	if err != nil {
		t.Fatalf("upvalue slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(upvalueSlot, value.NumberValue(41)); err != nil {
		t.Fatalf("seed upvalue slot: %v", err)
	}
	open, err := engine.Upvalues.FindOrCreateOpen(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@getupvalue-open-return.lua",
		NumUpvalues:  1,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 0, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, []value.HeapRef44{open.Ref})
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if runtime.SlowStubCount(stubs.StubGetUpvalue) != 0 {
		t.Fatalf("GETUPVAL native builtin followed by open RETURN should avoid Go slow stub, got %d", runtime.SlowStubCount(stubs.StubGetUpvalue))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("GETUPVAL native builtin followed by open RETURN should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeOpenReturnUsesUpdatedTopAfterHostGetGlobalStub(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	baseEnv, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	hostEnv, err := engine.RegisterHostObject("env", map[string]float64{"x": 41}, baseEnv.Value)
	if err != nil {
		t.Fatalf("register host env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@host-getglobal-open-return.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("x"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 0, 0),
		},
	}
	closure, err := engine.NewClosure(proto, hostEnv.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if runtime.DeoptCount() != 0 {
		t.Fatalf("host GETGLOBAL followed by open RETURN should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCompiledVarargFixedCountWithNilFill(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@vararg-fixed.lua",
		IsVararg:     1,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_VARARG, 0, 3, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 3, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{value.NumberValue(41)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41), value.NilValue()})
	if runtime.DeoptCount() != 0 {
		t.Fatalf("fixed VARARG should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCompiledVarargOpenFormSupportsOpenReturn(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@vararg-open.lua",
		IsVararg:     1,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_VARARG, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 0, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{value.NumberValue(11), value.NumberValue(22), value.NumberValue(33)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(11), value.NumberValue(22), value.NumberValue(33)})
	if runtime.DeoptCount() != 0 {
		t.Fatalf("open VARARG should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCompiledSetListFastPath(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	tableHandle, err := engine.NewTable(4, 0)
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@setlist-fast.lua",
		NumParams:    4,
		MaxStackSize: 4,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_SETLIST, 0, 3, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{tableHandle.Value, value.NumberValue(11), value.NumberValue(22), value.NumberValue(33)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{tableHandle.Value})
	assertTableArrayValue(t, engine, tableHandle.Value, 1, value.NumberValue(11))
	assertTableArrayValue(t, engine, tableHandle.Value, 2, value.NumberValue(22))
	assertTableArrayValue(t, engine, tableHandle.Value, 3, value.NumberValue(33))
	assertTableLenHint(t, engine, tableHandle.Value, 3)
	if runtime.SlowStubCount(stubs.StubSetList) != 0 {
		t.Fatalf("covered SETLIST should stay inside native builtin, got %d shared setlist stubs", runtime.SlowStubCount(stubs.StubSetList))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("covered SETLIST should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCompiledSetListOpenCountFromVararg(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	tableHandle, err := engine.NewTable(4, 0)
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@setlist-open-count.lua",
		NumParams:    1,
		IsVararg:     1,
		MaxStackSize: 4,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_VARARG, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_SETLIST, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{tableHandle.Value, value.NumberValue(11), value.NumberValue(22), value.NumberValue(33)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{tableHandle.Value})
	assertTableArrayValue(t, engine, tableHandle.Value, 1, value.NumberValue(11))
	assertTableArrayValue(t, engine, tableHandle.Value, 2, value.NumberValue(22))
	assertTableArrayValue(t, engine, tableHandle.Value, 3, value.NumberValue(33))
	if runtime.SlowStubCount(stubs.StubSetList) != 0 {
		t.Fatalf("open-count SETLIST should stay inside native builtin, got %d shared setlist stubs", runtime.SlowStubCount(stubs.StubSetList))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("open-count SETLIST should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeSetListMarkingFallsBackToBarrierStub(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	tableHandle, err := engine.NewTable(4, 0)
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@setlist-marking.lua",
		NumParams:    4,
		MaxStackSize: 4,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_SETLIST, 0, 3, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{tableHandle.Value, value.NumberValue(11), value.NumberValue(22), value.NumberValue(33)}, -1); err != nil {
		t.Fatalf("warm runtime call: %v", err)
	}
	child1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new child1: %v", err)
	}
	child2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new child2: %v", err)
	}
	child3, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new child3: %v", err)
	}
	currentWhite := engine.Heap.CurrentWhite()
	markHeapRefForTest(t, engine.Heap, tableHandle.Ref, value.MarkBlack)
	markHeapRefForTest(t, engine.Heap, child1.Ref, currentWhite)
	markHeapRefForTest(t, engine.Heap, child2.Ref, currentWhite)
	markHeapRefForTest(t, engine.Heap, child3.Ref, currentWhite)
	engine.Heap.ResetGCQueues()
	engine.Heap.SetGCPhase(rtheap.GCPhaseMark)
	beforeStub := runtime.SlowStubCount(stubs.StubSetList)
	beforeDeopt := runtime.DeoptCount()
	results, err := runtime.Call(thread, closure.Value, []value.TValue{tableHandle.Value, child1.Value, child2.Value, child3.Value}, -1)
	if err != nil {
		t.Fatalf("compiled runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{tableHandle.Value})
	if runtime.SlowStubCount(stubs.StubSetList) != beforeStub+1 {
		t.Fatalf("marking SETLIST should use exactly one shared stub, before=%d after=%d", beforeStub, runtime.SlowStubCount(stubs.StubSetList))
	}
	if runtime.DeoptCount() != beforeDeopt {
		t.Fatalf("marking SETLIST should avoid deopt, before=%d after=%d", beforeDeopt, runtime.DeoptCount())
	}
	if queues := engine.Heap.GCQueueLengths(); queues.GrayAgain != 1 || queues.Remembered != 1 {
		t.Fatalf("marking SETLIST barrier queues = %+v, want grayAgain=1 remembered=1", queues)
	}
	assertTableArrayValue(t, engine, tableHandle.Value, 1, child1.Value)
	assertTableArrayValue(t, engine, tableHandle.Value, 2, child2.Value)
	assertTableArrayValue(t, engine, tableHandle.Value, 3, child3.Value)
	assertTableLenHint(t, engine, tableHandle.Value, 3)
}

func TestBaselineRuntimeCompiledTForLoopWithLuaIterator(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	iteratorProto := &bytecode.Proto{
		Source:       "@tforloop-iter.lua",
		NumParams:    2,
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(10),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_LT, 1, 1, 0),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 2),
			bytecode.CreateABC(bytecode.OP_LOADNIL, 2, 3, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 3, 0),
			bytecode.CreateABC(bytecode.OP_ADD, 2, 1, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_ADD, 3, 2, bytecode.RKAsk(1)),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 3, 0),
		},
	}
	iterator, err := engine.NewClosure(iteratorProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new iterator closure: %v", err)
	}
	if _, err := runtime.Compile(iteratorProto); err != nil {
		t.Fatalf("compile iterator proto: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@tforloop-top.lua",
		NumParams:    4,
		MaxStackSize: 6,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 5, 3, 0),
			bytecode.CreateABC(bytecode.OP_TFORLOOP, 0, 0, 2),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 5, 2, 0),
			bytecode.CreateABC(bytecode.OP_ADD, 5, 5, 4),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, -5),
		},
	}
	closure, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{iterator.Value, value.NumberValue(2), value.NumberValue(0), value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(23)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("covered TFORLOOP should stay inside native call builtin, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("covered TFORLOOP should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTForLoopHostBoundaryResumesCompiledContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	iterator, err := engine.RegisterHostFunction("iter", func(state float64, control float64) (any, any) {
		if control >= state {
			return nil, nil
		}
		next := control + 1
		return next, next + 10
	}, env.Value)
	if err != nil {
		t.Fatalf("register iterator: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@tforloop-host-top.lua",
		NumParams:    4,
		MaxStackSize: 6,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 5, 3, 0),
			bytecode.CreateABC(bytecode.OP_TFORLOOP, 0, 0, 2),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 5, 2, 0),
			bytecode.CreateABC(bytecode.OP_ADD, 5, 5, 4),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, -5),
		},
	}
	closure, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{iterator.Value, value.NumberValue(2), value.NumberValue(0), value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(23)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 3 {
		t.Fatalf("host-backed TFORLOOP should use one shared call stub per iterator step, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("host-backed TFORLOOP should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTForLoopPreservesOpenReturnTop(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	iteratorProto := &bytecode.Proto{
		Source:       "@tforloop-open-return-iter.lua",
		NumParams:    2,
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(10),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_LT, 1, 1, 0),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 2),
			bytecode.CreateABC(bytecode.OP_LOADNIL, 2, 3, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 3, 0),
			bytecode.CreateABC(bytecode.OP_ADD, 2, 1, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_ADD, 3, 2, bytecode.RKAsk(1)),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 3, 0),
		},
	}
	iterator, err := engine.NewClosure(iteratorProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new iterator closure: %v", err)
	}
	if _, err := runtime.Compile(iteratorProto); err != nil {
		t.Fatalf("compile iterator proto: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@tforloop-open-return-top.lua",
		NumParams:    4,
		MaxStackSize: 6,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 5, 3, 0),
			bytecode.CreateABC(bytecode.OP_TFORLOOP, 0, 0, 2),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 5, 0, 0),
			bytecode.CreateABC(bytecode.OP_ADD, 5, 5, 4),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, -5),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	compiledResults, err := runtime.Call(thread, closure.Value, []value.TValue{iterator.Value, value.NumberValue(2), value.NumberValue(0), value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("compiled runtime call: %v", err)
	}
	interpResults, err := engine.Call(thread, closure.Value, []value.TValue{iterator.Value, value.NumberValue(2), value.NumberValue(0), value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("interpreter call: %v", err)
	}
	assertValuesEqual(t, compiledResults, interpResults)
	assertValuesEqual(t, compiledResults, []value.TValue{value.NumberValue(23)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("covered TFORLOOP followed by open RETURN should stay inside native call builtin, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("covered TFORLOOP followed by open RETURN should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeSetListPayloadFallbackResumesContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
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
	proto := &bytecode.Proto{
		Source:       "@setlist-payload-fallback.lua",
		NumParams:    3,
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(999),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_SETLIST, 0, 2, 0),
			bytecode.Instruction(1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{tableHandle.Value, value.NumberValue(7), value.NumberValue(8)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{tableHandle.Value})
	assertTableArrayValue(t, engine, tableHandle.Value, 1, value.NumberValue(7))
	assertTableArrayValue(t, engine, tableHandle.Value, 2, value.NumberValue(8))
	if runtime.SlowStubCount(stubs.StubSetList) != 1 {
		t.Fatalf("capacity-miss SETLIST should use exactly one shared setlist stub, got %d", runtime.SlowStubCount(stubs.StubSetList))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("SETLIST payload fallback should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeBlockedSetListDeoptsInsteadOfUsingSharedStub(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	tableHandle, err := engine.NewTable(4, 0)
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	meta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	if err := engine.Tables.SetMetatable(tableHandle.Ref, meta.Value); err != nil {
		t.Fatalf("set metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@setlist-blocked-deopt.lua",
		NumParams:    3,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_SETLIST, 0, 2, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{tableHandle.Value, value.NumberValue(5), value.NumberValue(6)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{tableHandle.Value})
	assertTableArrayValue(t, engine, tableHandle.Value, 1, value.NumberValue(5))
	assertTableArrayValue(t, engine, tableHandle.Value, 2, value.NumberValue(6))
	if runtime.SlowStubCount(stubs.StubSetList) != 0 {
		t.Fatalf("blocked SETLIST should avoid shared setlist stub, got %d", runtime.SlowStubCount(stubs.StubSetList))
	}
	if runtime.DeoptCount() != 1 {
		t.Fatalf("blocked SETLIST should deopt exactly once, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeDirectCompiledCallPreservesSecondArgumentAfterBoxedFirst(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	boxed, err := engine.RegisterHostFunction("boxed", func(v float64) float64 {
		return v
	}, env.Value)
	if err != nil {
		t.Fatalf("register host function: %v", err)
	}
	calleeProto := &bytecode.Proto{
		Source:       "@boxed-arg-callee.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee closure: %v", err)
	}
	if _, err := runtime.Compile(calleeProto); err != nil {
		t.Fatalf("compile callee: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@boxed-arg-top.lua",
		NumParams:    3,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 3, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	top, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, top.Value, []value.TValue{callee.Value, boxed.Value, value.NumberValue(41)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("boxed-first-arg direct compiled call should stay inside native call builtin, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
}

func TestBaselineRuntimeCompiledFrameRetainsSecondArgumentAfterBoxedFirst(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	boxed, err := engine.RegisterHostFunction("boxed-top", func(v float64) float64 {
		return v
	}, env.Value)
	if err != nil {
		t.Fatalf("register host function: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@boxed-top-return.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := runtime.Compile(proto); err != nil {
		t.Fatalf("compile proto: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{boxed.Value, value.NumberValue(41)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
}

func TestBaselineRuntimeCompiledTailCall(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	calleeProto := &bytecode.Proto{
		Source:       "@tail-leaf.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee: %v", err)
	}
	if _, err := runtime.Compile(calleeProto); err != nil {
		t.Fatalf("compile callee: %v", err)
	}
	tailProto := &bytecode.Proto{
		Source:       "@tailcaller.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 2, 0),
		},
	}
	tail, err := engine.NewClosure(tailProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new tailcaller: %v", err)
	}
	results, err := runtime.Call(thread, tail.Value, []value.TValue{callee.Value, value.NumberValue(123)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(123)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 0 {
		t.Fatalf("covered Lua-to-Lua tailcall should stay inside native tailcall builtin, got %d shared tail stubs", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tailcall continuation should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCompiledTailCallSupportsOpenArgs(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	calleeProto := &bytecode.Proto{
		Source:       "@tail-open-args-leaf.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee: %v", err)
	}
	if _, err := runtime.Compile(calleeProto); err != nil {
		t.Fatalf("compile callee: %v", err)
	}
	tailProto := &bytecode.Proto{
		Source:       "@tailcaller-open-args.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 0, 0),
		},
	}
	tail, err := engine.NewClosure(tailProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new tailcaller: %v", err)
	}
	results, err := runtime.Call(thread, tail.Value, []value.TValue{callee.Value, value.NumberValue(123)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(123)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 0 {
		t.Fatalf("open TAILCALL args should stay inside native tailcall builtin, got %d shared tail stubs", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("open TAILCALL args should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeNestedCompiledTailCallCanReachHostBoundary(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	addOne, err := engine.RegisterHostFunction("add1", func(v float64) float64 {
		return v + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register host function: %v", err)
	}
	midProto := &bytecode.Proto{
		Source:       "@mid-tail-host-call.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	mid, err := engine.NewClosure(midProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new mid closure: %v", err)
	}
	if _, err := runtime.Compile(midProto); err != nil {
		t.Fatalf("compile mid: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@top-tail-host-call.lua",
		NumParams:    3,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 3, 0),
		},
	}
	top, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, top.Value, []value.TValue{mid.Value, addOne.Value, value.NumberValue(41)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 0 {
		t.Fatalf("nested compiled tailcall should not need a Go tail stub, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("nested tailcall host boundary should use exactly one terminal call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("nested tailcall host boundary should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeNativeBuiltinCompositionStaysInCompiledIsland(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	upvalueSlot, err := thread.SlotAddress(32)
	if err != nil {
		t.Fatalf("upvalue slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(upvalueSlot, value.NumberValue(41)); err != nil {
		t.Fatalf("seed upvalue slot: %v", err)
	}
	open, err := engine.Upvalues.FindOrCreateOpen(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}
	leafProto := &bytecode.Proto{
		Source:       "@native-combo-leaf.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	leaf, err := engine.NewClosure(leafProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new leaf: %v", err)
	}
	if _, err := runtime.Compile(leafProto); err != nil {
		t.Fatalf("compile leaf: %v", err)
	}
	midProto := &bytecode.Proto{
		Source:       "@native-combo-mid.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 2, 0),
		},
	}
	mid, err := engine.NewClosure(midProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new mid: %v", err)
	}
	if _, err := runtime.Compile(midProto); err != nil {
		t.Fatalf("compile mid: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@native-builtin-composition.lua",
		NumParams:    3,
		NumUpvalues:  1,
		MaxStackSize: 10,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("missing"),
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(1),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 3, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 4, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 5, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 6, 3),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 4, 1),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 7, 0, bytecode.RKAsk(0)),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 4, -2),
			bytecode.CreateABC(bytecode.OP_MOVE, 7, 1, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 8, 2, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 9, 3, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 7, 3, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 7, 2, 0),
		},
	}
	top, err := engine.NewClosure(topProto, env.Value, []value.HeapRef44{open.Ref})
	if err != nil {
		t.Fatalf("new top: %v", err)
	}
	if _, err := runtime.Compile(topProto); err != nil {
		t.Fatalf("compile top: %v", err)
	}
	results, err := runtime.Call(thread, top.Value, []value.TValue{box.Value, mid.Value, leaf.Value}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if runtime.SlowStubCount(stubs.StubGetTable) != 0 {
		t.Fatalf("covered GETTABLE miss in composition should stay inside native builtin, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.SlowStubCount(stubs.StubGetUpvalue) != 0 {
		t.Fatalf("covered GETUPVAL in composition should stay inside native builtin, got %d", runtime.SlowStubCount(stubs.StubGetUpvalue))
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("covered CALL in composition should stay inside native builtin, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.SlowStubCount(stubs.StubTailCall) != 0 {
		t.Fatalf("covered TAILCALL in composition should stay inside native builtin, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if runtime.SlowStubCount(stubs.StubForPrep) != 0 || runtime.SlowStubCount(stubs.StubForLoop) != 0 {
		t.Fatalf("numeric for composition should stay inside native builtin, got prep=%d loop=%d", runtime.SlowStubCount(stubs.StubForPrep), runtime.SlowStubCount(stubs.StubForLoop))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("covered native builtin composition should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCompilesArithmeticFastPath(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@fallback.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(20),
			bytecode.NumberConstant(22),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_ADD, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	compiled, err := runtime.Compile(proto)
	if err != nil {
		t.Fatalf("compile proto: %v", err)
	}
	if !compiled.Supported {
		t.Fatalf("ADD should compile in the current baseline pipeline, unsupported reason: %s", compiled.UnsupportedReason)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	assertNoNativeFallback(t, runtime)
}

func TestBaselineRuntimeCompiledCallerCanFallbackCallee(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	calleeProto := &bytecode.Proto{
		Source:       "@callee-fallback.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_ADD, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee: %v", err)
	}
	callerProto := &bytecode.Proto{
		Source:       "@caller-compiled.lua",
		NumParams:    3,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 3, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	caller, err := engine.NewClosure(callerProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new caller: %v", err)
	}
	results, err := runtime.Call(thread, caller.Value, []value.TValue{callee.Value, value.NumberValue(19), value.NumberValue(23)}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
}

func TestBaselineRuntimeNumericForLoopSkeleton(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@forloop.lua",
		MaxStackSize: 5,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(3),
			bytecode.NumberConstant(1),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 0, 1),
			bytecode.CreateABC(bytecode.OP_MOVE, 4, 3, 0),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 0, -2),
			bytecode.CreateABC(bytecode.OP_RETURN, 4, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(3)})
	assertNoNativeFallback(t, runtime)
}

func TestBaselineRuntimeNumericForLoopBackedgeAdvancesGCSafepoint(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@forloop-safepoint.lua",
		MaxStackSize: 5,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 0),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 0, 1),
			bytecode.CreateABC(bytecode.OP_MOVE, 4, 3, 0),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 0, -2),
			bytecode.CreateABC(bytecode.OP_RETURN, 4, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	assist := &safepointAssist{}
	engine.SetAllocationAssistant(assist)
	engine.Heap.SetGCThreshold(1)
	if _, err := engine.InternString("forloop-safepoint-trigger"); err != nil {
		t.Fatalf("intern trigger string: %v", err)
	}
	if !engine.Heap.GCTargetReached() {
		t.Fatalf("gc target should be reached before numeric for loop")
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(1)})
	if assist.count != 1 {
		t.Fatalf("loop backedge safepoint count = %d, want 1", assist.count)
	}
	if runtime.SlowStubCount(stubs.StubForPrep) != 0 || runtime.SlowStubCount(stubs.StubForLoop) != 0 {
		t.Fatalf("loop backedge safepoint should avoid shared loop stubs, got prep=%d loop=%d", runtime.SlowStubCount(stubs.StubForPrep), runtime.SlowStubCount(stubs.StubForLoop))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("loop backedge safepoint should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCompiledGetGlobalFastPathAfterWarmup(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "answer", value.NumberValue(42)); err != nil {
		t.Fatalf("set global: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-getglobal.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("answer"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("warm runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StateMonomorphic || cell.AccessKind != feedback.AccessHash || cell.SlotKind != feedback.SlotGetGlobal {
		t.Fatalf("unexpected warmed cell: %+v", cell)
	}
	before := captureRuntimeCounters(runtime)
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("compiled runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	assertCounterDeltaZero(t, runtime, before)
}

func TestBaselineRuntimeTableFastPathPreservesCompiledIsland(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	leafProto := &bytecode.Proto{
		Source:       "@leaf.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	leaf, err := engine.NewClosure(leafProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new leaf closure: %v", err)
	}
	if _, err := runtime.Compile(leafProto); err != nil {
		t.Fatalf("compile leaf: %v", err)
	}
	keyCallee, err := engine.InternString("callee")
	if err != nil {
		t.Fatalf("intern callee key: %v", err)
	}
	keyArg, err := engine.InternString("arg")
	if err != nil {
		t.Fatalf("intern arg key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box table: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyCallee.Value, leaf.Value); err != nil {
		t.Fatalf("seed callee entry: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyArg.Value, value.NumberValue(77)); err != nil {
		t.Fatalf("seed arg entry: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@top-table-call.lua",
		NumParams:    1,
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callee"),
			bytecode.StringConstant("arg"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 2, 0, bytecode.RKAsk(1)),
			bytecode.CreateABC(bytecode.OP_CALL, 1, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1)
	if err != nil {
		t.Fatalf("warm runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(77)})
	before := captureRuntimeCounters(runtime)
	results, err = runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1)
	if err != nil {
		t.Fatalf("compiled runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(77)})
	if runtime.SlowStubCount(stubs.StubGetTable) != before.callSuspend {
		t.Fatalf("table fast path should avoid extra get-table slow stubs: before=%d after=%d", before.callSuspend, runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != before.forPrep {
		t.Fatalf("covered compiled table+call path should stay inside native call builtin: before=%d after=%d", before.forPrep, runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != before.deopt {
		t.Fatalf("table fast path should avoid deopt: before=%d after=%d", before.deopt, runtime.DeoptCount())
	}
}

func TestBaselineRuntimeGenericGetTableMissUsesSlowStubContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	keyValue, err := engine.InternString("value")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box table: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyValue.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("seed table: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@slow-gettable.lua",
		NumParams:    1,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("value"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubGetTable) != 0 {
		t.Fatalf("covered generic gettable should stay inside native builtin, got slow-stub count %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("slow-stub continuation should avoid deopt, got %d", runtime.DeoptCount())
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubGetTable) != 0 {
		t.Fatalf("monomorphic gettable hit should still avoid Go slow stubs, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
}

func TestBaselineRuntimeTableSlowStubContinuesIntoCall(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box table: %v", err)
	}
	ret99, err := engine.RegisterHostFunction("ret99", func() float64 {
		return 99
	}, env.Value)
	if err != nil {
		t.Fatalf("register ret99: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "ret99", ret99.Value); err != nil {
		t.Fatalf("set global ret99: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@slow-gettable-call.lua",
		NumParams:    1,
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("missing"),
			bytecode.StringConstant("ret99"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(0)),
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 2, 1),
			bytecode.CreateABC(bytecode.OP_CALL, 2, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(99)})
	if runtime.SlowStubCount(stubs.StubGetTable) != 0 {
		t.Fatalf("covered missing-key gettable should stay inside native builtin, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("slow-stub continuation should still reach shared call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("covered table miss should stay in compiled island, got deopt=%d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeMetatableBlockedTableUsesSharedSlowStub(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	keyValue, err := engine.InternString("value")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box table: %v", err)
	}
	meta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyValue.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("seed box table: %v", err)
	}
	if err := engine.Tables.SetMetatable(box.Ref, meta.Value); err != nil {
		t.Fatalf("set metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@blocked-gettable.lua",
		NumParams:    1,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("value"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubGetTable) != 0 {
		t.Fatalf("blocked-table covered path should stay inside native builtin, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubGetTable) != 0 {
		t.Fatalf("blocked-table covered path should continue to avoid Go slow stubs, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("metatable blocker should not deopt current covered semantics, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeUpvalueStubsResumeCompiledContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	ret1, err := engine.RegisterHostFunction("add1", func(v float64) float64 {
		return v + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register add1: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "add1", ret1.Value); err != nil {
		t.Fatalf("set global add1: %v", err)
	}
	upvalueSlot, err := thread.SlotAddress(8)
	if err != nil {
		t.Fatalf("upvalue slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(upvalueSlot, value.NumberValue(41)); err != nil {
		t.Fatalf("seed upvalue slot: %v", err)
	}
	open, err := engine.Upvalues.FindOrCreateOpen(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-getupval-call.lua",
		NumUpvalues:  1,
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("add1"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 0, 0, 0),
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 1, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 2, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 1, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, []value.HeapRef44{open.Ref})
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubGetUpvalue) != 0 {
		t.Fatalf("GETUPVAL native builtin should avoid Go slow stub, got %d", runtime.SlowStubCount(stubs.StubGetUpvalue))
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("upvalue continuation should still reach shared call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("upvalue stub continuation should avoid deopt, got %d", runtime.DeoptCount())
	}
	if err := thread.SetValueAtAddress(upvalueSlot, value.NumberValue(99)); err != nil {
		t.Fatalf("mutate open upvalue slot: %v", err)
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(100)})
	if runtime.SlowStubCount(stubs.StubGetUpvalue) != 0 {
		t.Fatalf("GETUPVAL native builtin should remain inside compiled island, got %d", runtime.SlowStubCount(stubs.StubGetUpvalue))
	}
}

func TestBaselineRuntimeSetUpvalueStubResumesCompiledContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	upvalueSlot, err := thread.SlotAddress(8)
	if err != nil {
		t.Fatalf("upvalue slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(upvalueSlot, value.NumberValue(1)); err != nil {
		t.Fatalf("seed upvalue slot: %v", err)
	}
	open, err := engine.Upvalues.FindOrCreateOpen(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-setupval.lua",
		NumUpvalues:  1,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(40),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_SETUPVAL, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, []value.HeapRef44{open.Ref})
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(40)})
	current, err := thread.ValueAtAddress(upvalueSlot)
	if err != nil {
		t.Fatalf("read updated upvalue slot: %v", err)
	}
	if current.Bits() != value.NumberValue(40).Bits() {
		t.Fatalf("open upvalue slot = %s, want %s", current, value.NumberValue(40))
	}
	if runtime.SlowStubCount(stubs.StubSetUpvalue) != 0 {
		t.Fatalf("SETUPVAL native builtin should avoid Go slow stub, got %d", runtime.SlowStubCount(stubs.StubSetUpvalue))
	}
	if runtime.SlowStubCount(stubs.StubGetUpvalue) != 0 {
		t.Fatalf("GETUPVAL native builtin should stay inside compiled island after SETUPVAL, got %d", runtime.SlowStubCount(stubs.StubGetUpvalue))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("setupval/getupval continuation should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeClosedUpvalueNativeBuiltinResumesCompiledContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	upvalueSlot, err := thread.SlotAddress(8)
	if err != nil {
		t.Fatalf("upvalue slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(upvalueSlot, value.NumberValue(10)); err != nil {
		t.Fatalf("seed upvalue slot: %v", err)
	}
	open, err := engine.Upvalues.FindOrCreateOpen(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}
	closed, err := engine.Upvalues.CloseAtOrAbove(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("close upvalue: %v", err)
	}
	if len(closed) != 1 || closed[0].Ref != open.Ref {
		t.Fatalf("closed upvalue handles = %+v, want ref %#x", closed, uint64(open.Ref))
	}
	if err := thread.SetValueAtAddress(upvalueSlot, value.NumberValue(99)); err != nil {
		t.Fatalf("mutate former stack slot: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-closed-upvalue.lua",
		NumUpvalues:  1,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(40),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_SETUPVAL, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, []value.HeapRef44{closed[0].Ref})
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(40)})
	current, err := thread.ValueAtAddress(upvalueSlot)
	if err != nil {
		t.Fatalf("read former stack slot: %v", err)
	}
	if current.Bits() != value.NumberValue(99).Bits() {
		t.Fatalf("closed upvalue should not write back to former stack slot, got %s want %s", current, value.NumberValue(99))
	}
	closedValue, err := engine.Upvalues.Get(closed[0].Ref)
	if err != nil {
		t.Fatalf("read closed upvalue value: %v", err)
	}
	if closedValue.Bits() != value.NumberValue(40).Bits() {
		t.Fatalf("closed upvalue value = %s, want %s", closedValue, value.NumberValue(40))
	}
	if runtime.SlowStubCount(stubs.StubSetUpvalue) != 0 || runtime.SlowStubCount(stubs.StubGetUpvalue) != 0 {
		t.Fatalf("closed upvalue native builtins should avoid Go slow stubs, got set=%d get=%d", runtime.SlowStubCount(stubs.StubSetUpvalue), runtime.SlowStubCount(stubs.StubGetUpvalue))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("closed upvalue native builtins should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeSetUpvalueMarkingFallsBackToBarrierStub(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	upvalueSlot, err := thread.SlotAddress(8)
	if err != nil {
		t.Fatalf("upvalue slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(upvalueSlot, value.NumberValue(10)); err != nil {
		t.Fatalf("seed upvalue slot: %v", err)
	}
	open, err := engine.Upvalues.FindOrCreateOpen(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}
	closed, err := engine.Upvalues.CloseAtOrAbove(thread, upvalueSlot)
	if err != nil {
		t.Fatalf("close upvalue: %v", err)
	}
	if len(closed) != 1 || closed[0].Ref != open.Ref {
		t.Fatalf("closed upvalue handles = %+v, want ref %#x", closed, uint64(open.Ref))
	}
	proto := &bytecode.Proto{
		Source:       "@jit-setupval-marking.lua",
		NumParams:    1,
		NumUpvalues:  1,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_SETUPVAL, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, []value.HeapRef44{closed[0].Ref})
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{value.NumberValue(40)}, -1); err != nil {
		t.Fatalf("warm runtime call: %v", err)
	}
	child, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new child: %v", err)
	}
	markHeapRefForTest(t, engine.Heap, closed[0].Ref, value.MarkBlack)
	markHeapRefForTest(t, engine.Heap, child.Ref, engine.Heap.CurrentWhite())
	engine.Heap.ResetGCQueues()
	engine.Heap.SetGCPhase(rtheap.GCPhaseMark)
	beforeSetStub := runtime.SlowStubCount(stubs.StubSetUpvalue)
	beforeGetStub := runtime.SlowStubCount(stubs.StubGetUpvalue)
	beforeDeopt := runtime.DeoptCount()
	results, err := runtime.Call(thread, closure.Value, []value.TValue{child.Value}, -1)
	if err != nil {
		t.Fatalf("compiled runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{child.Value})
	if runtime.SlowStubCount(stubs.StubSetUpvalue) != beforeSetStub+1 {
		t.Fatalf("marking SETUPVAL should use exactly one shared stub, before=%d after=%d", beforeSetStub, runtime.SlowStubCount(stubs.StubSetUpvalue))
	}
	if runtime.SlowStubCount(stubs.StubGetUpvalue) != beforeGetStub {
		t.Fatalf("marking SETUPVAL should not force GETUPVAL slow stub, before=%d after=%d", beforeGetStub, runtime.SlowStubCount(stubs.StubGetUpvalue))
	}
	if runtime.DeoptCount() != beforeDeopt {
		t.Fatalf("marking SETUPVAL should avoid deopt, before=%d after=%d", beforeDeopt, runtime.DeoptCount())
	}
	if queues := engine.Heap.GCQueueLengths(); queues.GrayAgain != 1 || queues.Remembered != 1 {
		t.Fatalf("marking SETUPVAL barrier queues = %+v, want grayAgain=1 remembered=1", queues)
	}
	closedValue, err := engine.Upvalues.Get(closed[0].Ref)
	if err != nil {
		t.Fatalf("read closed upvalue value: %v", err)
	}
	if closedValue.Bits() != child.Value.Bits() {
		t.Fatalf("closed upvalue value = %s, want %s", closedValue, child.Value)
	}
}

func TestBaselineRuntimeCompiledSetTableFastPathAfterWarmup(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	keyValue, err := engine.InternString("value")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box table: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyValue.Value, value.NumberValue(1)); err != nil {
		t.Fatalf("seed box table: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-settable.lua",
		NumParams:    2,
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("value"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, bytecode.RKAsk(0), 1),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 2, 0, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value, value.NumberValue(99)}, -1)
	if err != nil {
		t.Fatalf("warm runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(99)})
	before := captureRuntimeCounters(runtime)
	results, err = runtime.Call(thread, closure.Value, []value.TValue{box.Value, value.NumberValue(123)}, -1)
	if err != nil {
		t.Fatalf("compiled runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(123)})
	assertCounterDeltaZero(t, runtime, before)
	stored, found, err := engine.Tables.Get(box.Ref, keyValue.Value)
	if err != nil {
		t.Fatalf("read stored value: %v", err)
	}
	if !found || stored.Bits() != value.NumberValue(123).Bits() {
		t.Fatalf("stored value = %s, want %s", stored, value.NumberValue(123))
	}
}

func TestBaselineRuntimeCompiledSetTableMarkingFallsBackToBarrierStub(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	keyValue, err := engine.InternString("value")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box table: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyValue.Value, value.NumberValue(1)); err != nil {
		t.Fatalf("seed box table: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-settable-marking.lua",
		NumParams:    2,
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("value"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, bytecode.RKAsk(0), 1),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 2, 0, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value, value.NumberValue(99)}, -1); err != nil {
		t.Fatalf("warm runtime call: %v", err)
	}
	child, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new child: %v", err)
	}
	markHeapRefForTest(t, engine.Heap, box.Ref, value.MarkBlack)
	markHeapRefForTest(t, engine.Heap, child.Ref, engine.Heap.CurrentWhite())
	engine.Heap.ResetGCQueues()
	engine.Heap.SetGCPhase(rtheap.GCPhaseMark)
	beforeStub := runtime.SlowStubCount(stubs.StubSetTable)
	beforeDeopt := runtime.DeoptCount()
	results, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value, child.Value}, -1)
	if err != nil {
		t.Fatalf("compiled runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{child.Value})
	if runtime.SlowStubCount(stubs.StubSetTable) != beforeStub+1 {
		t.Fatalf("marking SETTABLE should use exactly one shared stub, before=%d after=%d", beforeStub, runtime.SlowStubCount(stubs.StubSetTable))
	}
	if runtime.DeoptCount() != beforeDeopt {
		t.Fatalf("marking SETTABLE should avoid deopt, before=%d after=%d", beforeDeopt, runtime.DeoptCount())
	}
	if queues := engine.Heap.GCQueueLengths(); queues.GrayAgain != 1 || queues.Remembered != 1 {
		t.Fatalf("marking SETTABLE barrier queues = %+v, want grayAgain=1 remembered=1", queues)
	}
	stored, found, err := engine.Tables.Get(box.Ref, keyValue.Value)
	if err != nil {
		t.Fatalf("read stored value: %v", err)
	}
	if !found || stored.Bits() != child.Value.Bits() {
		t.Fatalf("stored value = %s, want %s", stored, child.Value)
	}
}

func TestBaselineRuntimeCompiledSetGlobalFastPathAfterWarmup(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	keyValue, err := engine.InternString("answer")
	if err != nil {
		t.Fatalf("intern answer key: %v", err)
	}
	if err := engine.Tables.Set(env.Ref, keyValue.Value, value.NumberValue(1)); err != nil {
		t.Fatalf("seed global value: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-setglobal.lua",
		NumParams:    1,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("answer"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_SETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{value.NumberValue(99)}, -1)
	if err != nil {
		t.Fatalf("warm runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(99)})
	before := captureRuntimeCounters(runtime)
	results, err = runtime.Call(thread, closure.Value, []value.TValue{value.NumberValue(123)}, -1)
	if err != nil {
		t.Fatalf("compiled runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(123)})
	assertCounterDeltaZero(t, runtime, before)
	stored, found, err := engine.Tables.Get(env.Ref, keyValue.Value)
	if err != nil {
		t.Fatalf("read global value: %v", err)
	}
	if !found || stored.Bits() != value.NumberValue(123).Bits() {
		t.Fatalf("global value = %s, want %s", stored, value.NumberValue(123))
	}
}

func TestBaselineRuntimeHostBridgeStubsResumeCompiledContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	bag := map[string]float64{"x": 5}
	bagObject, err := engine.RegisterHostObject("bag", bag, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	addOne, err := engine.RegisterHostFunction("add1", func(v float64) float64 {
		return v + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register host function: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "bag", bagObject.Value); err != nil {
		t.Fatalf("set global bag: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "add1", addOne.Value); err != nil {
		t.Fatalf("set global add1: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-host-bridge.lua",
		MaxStackSize: 6,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("bag"),
			bytecode.StringConstant("x"),
			bytecode.NumberConstant(42),
			bytecode.StringConstant("add1"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 3, 0, 1),
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 4, 3),
			bytecode.CreateABC(bytecode.OP_MOVE, 5, 3, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 4, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 4, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(43)})
	if bag["x"] != 42 {
		t.Fatalf("host object set should update Go target, got %v", bag)
	}
	if runtime.SlowStubCount(stubs.StubSetTable) != 1 {
		t.Fatalf("host object set should use one shared set-table stub, got %d", runtime.SlowStubCount(stubs.StubSetTable))
	}
	if runtime.SlowStubCount(stubs.StubGetTable) != 1 {
		t.Fatalf("host object get should use one shared get-table stub, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("host function call should use one shared call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("host bridge continuation should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeHostDescriptorRefreshKeepsCompiledContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	bagObject, err := engine.RegisterHostObject("bag", map[string]float64{"x": 5}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "bag", bagObject.Value); err != nil {
		t.Fatalf("set global bag: %v", err)
	}
	before, _, _, err := engine.Hosts.ReadHostObject(bagObject.Ref)
	if err != nil {
		t.Fatalf("read host object wrapper: %v", err)
	}
	if err := engine.Hosts.BumpDescriptorVersion(host.Handle(before.HostHandle)); err != nil {
		t.Fatalf("bump descriptor version: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-host-descriptor-refresh.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("bag"),
			bytecode.StringConstant("x"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(5)})
	updated, _, _, err := engine.Hosts.ReadHostObject(bagObject.Ref)
	if err != nil {
		t.Fatalf("read refreshed host object wrapper: %v", err)
	}
	if updated.DescriptorVersion != before.DescriptorVersion+1 {
		t.Fatalf("wrapper descriptor version = %d, want %d", updated.DescriptorVersion, before.DescriptorVersion+1)
	}
	if runtime.SlowStubCount(stubs.StubGetTable) != 1 {
		t.Fatalf("host descriptor refresh should still use one shared get-table stub, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("descriptor refresh should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeFeedbackTransitionsToMegamorphic(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	keyValue, err := engine.InternString("value")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	left, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left table: %v", err)
	}
	right, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right table: %v", err)
	}
	if err := engine.Tables.Set(left.Ref, keyValue.Value, value.NumberValue(10)); err != nil {
		t.Fatalf("seed left table: %v", err)
	}
	if err := engine.Tables.Set(right.Ref, keyValue.Value, value.NumberValue(20)); err != nil {
		t.Fatalf("seed right table: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-mega.lua",
		NumParams:    1,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("value"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{left.Value}, -1); err != nil {
		t.Fatalf("warm left call: %v", err)
	}
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StateMonomorphic {
		t.Fatalf("expected monomorphic state after warmup, got %+v", cell)
	}
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{right.Value}, -1); err != nil {
		t.Fatalf("megamorphic transition call: %v", err)
	}
	cell = mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StateMegamorphic {
		t.Fatalf("expected megamorphic state after mismatched table, got %+v", cell)
	}
	before := runtime.SlowStubCount(stubs.StubGetTable)
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{left.Value}, -1); err != nil {
		t.Fatalf("post-mega call: %v", err)
	}
	if runtime.SlowStubCount(stubs.StubGetTable) != before {
		t.Fatalf("megamorphic covered path should stay inside native builtin: before=%d after=%d", before, runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("megamorphic call should avoid entry replay deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeVersionMissRewarmsMonomorphicCell(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	keyValue, err := engine.InternString("value")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	keyOther, err := engine.InternString("other")
	if err != nil {
		t.Fatalf("intern other key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box table: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyValue.Value, value.NumberValue(5)); err != nil {
		t.Fatalf("seed value entry: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-version.lua",
		NumParams:    1,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("value"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1); err != nil {
		t.Fatalf("warm call: %v", err)
	}
	before := captureRuntimeCounters(runtime)
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1); err != nil {
		t.Fatalf("compiled hit before version miss: %v", err)
	}
	assertCounterDeltaZero(t, runtime, before)
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	versionBefore := cell.TableVersion()
	if err := engine.Tables.Set(box.Ref, keyOther.Value, value.NumberValue(9)); err != nil {
		t.Fatalf("mutate table to bump version: %v", err)
	}
	beforeStub := runtime.SlowStubCount(stubs.StubGetTable)
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1); err != nil {
		t.Fatalf("version-miss call: %v", err)
	}
	if runtime.SlowStubCount(stubs.StubGetTable) != beforeStub {
		t.Fatalf("version mismatch should rewarm inside native builtin: before=%d after=%d", beforeStub, runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("version mismatch should avoid deopt replay, got %d", runtime.DeoptCount())
	}
	cell = mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StateMonomorphic || cell.TableVersion() == versionBefore {
		t.Fatalf("expected re-warmed monomorphic cell with new version, got %+v", cell)
	}
	before = captureRuntimeCounters(runtime)
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1); err != nil {
		t.Fatalf("post-rewarm compiled hit: %v", err)
	}
	assertCounterDeltaZero(t, runtime, before)
}

func TestBaselineRuntimeFeedbackTransitionsMatchInterpreter(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	interpThread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new interpreter thread: %v", err)
	}
	compiledThread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new compiled thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	keyValue, err := engine.InternString("value")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	left, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left table: %v", err)
	}
	right, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right table: %v", err)
	}
	if err := engine.Tables.Set(left.Ref, keyValue.Value, value.NumberValue(10)); err != nil {
		t.Fatalf("seed left table: %v", err)
	}
	if err := engine.Tables.Set(right.Ref, keyValue.Value, value.NumberValue(20)); err != nil {
		t.Fatalf("seed right table: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@feedback-shared.lua",
		NumParams:    1,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("value"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	interpClosure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new interpreter closure: %v", err)
	}
	compiledClosure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new compiled closure: %v", err)
	}
	compiled, err := runtime.Compile(proto)
	if err != nil {
		t.Fatalf("compile shared proto: %v", err)
	}
	if _, err := engine.Closures.EnsureFeedbackVector(interpClosure.Ref, compiled.FeedbackLayout); err != nil {
		t.Fatalf("ensure interpreter feedback vector: %v", err)
	}
	if _, err := engine.Call(interpThread, interpClosure.Value, []value.TValue{left.Value}, -1); err != nil {
		t.Fatalf("interpreter warm call: %v", err)
	}
	if _, err := runtime.Call(compiledThread, compiledClosure.Value, []value.TValue{left.Value}, -1); err != nil {
		t.Fatalf("compiled warm call: %v", err)
	}
	assertFeedbackCellEqual(t, mustFeedbackCell(t, runtime, interpClosure.Ref, 0), mustFeedbackCell(t, runtime, compiledClosure.Ref, 0))
	if _, err := engine.Call(interpThread, interpClosure.Value, []value.TValue{right.Value}, -1); err != nil {
		t.Fatalf("interpreter mismatch call: %v", err)
	}
	if _, err := runtime.Call(compiledThread, compiledClosure.Value, []value.TValue{right.Value}, -1); err != nil {
		t.Fatalf("compiled mismatch call: %v", err)
	}
	assertFeedbackCellEqual(t, mustFeedbackCell(t, runtime, interpClosure.Ref, 0), mustFeedbackCell(t, runtime, compiledClosure.Ref, 0))
}

func TestBaselineRuntimeDeoptResumesWithoutReplayingEarlierSideEffects(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	var calls int
	hostFunc, err := engine.RegisterHostFunction("bump", func() {
		calls++
	}, env.Value)
	if err != nil {
		t.Fatalf("register host function: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@deopt-no-replay.lua",
		NumParams:    2,
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("value"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 1),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 2, 1, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := runtime.Call(thread, closure.Value, []value.TValue{hostFunc.Value, value.NumberValue(7)}, -1); err == nil {
		t.Fatalf("expected deopted GETTABLE to surface an interpreter error")
	}
	if calls != 1 {
		t.Fatalf("earlier host side effect was replayed %d times, want 1", calls)
	}
	if runtime.DeoptCount() != 1 {
		t.Fatalf("expected exactly one continuation-aware deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeLoopSlowStubResumesContinuation(t *testing.T) {
	engine := interp.New()
	runtime := NewRuntime(engine)
	defer func() { _ = runtime.Close() }()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@loop-slow-stub.lua",
		MaxStackSize: 5,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(math.NaN()),
			bytecode.NumberConstant(3),
			bytecode.NumberConstant(1),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 0, 1),
			bytecode.CreateABC(bytecode.OP_MOVE, 4, 3, 0),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 0, -2),
			bytecode.CreateABC(bytecode.OP_RETURN, 4, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NilValue()})
	if runtime.SlowStubCount(stubs.StubForPrep) != 0 || runtime.SlowStubCount(stubs.StubForLoop) != 0 {
		t.Fatalf("numeric-for native builtins should avoid Go slow stubs, got prep=%d loop=%d", runtime.SlowStubCount(stubs.StubForPrep), runtime.SlowStubCount(stubs.StubForLoop))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("loop slow stub should resume compiled continuation without deopt, got %d", runtime.DeoptCount())
	}
}

type runtimeCounters struct {
	callSuspend uint64
	forPrep     uint64
	forLoop     uint64
	deopt       uint64
}

func captureRuntimeCounters(runtime *Runtime) runtimeCounters {
	return runtimeCounters{
		callSuspend: runtime.SlowStubCount(stubs.StubGetTable),
		forPrep:     runtime.SlowStubCount(stubs.StubLuaCall),
		forLoop:     runtime.SlowStubCount(stubs.StubGetGlobal) + runtime.SlowStubCount(stubs.StubSetGlobal) + runtime.SlowStubCount(stubs.StubSetTable),
		deopt:       runtime.DeoptCount(),
	}
}

func assertCounterDeltaZero(t *testing.T, runtime *Runtime, before runtimeCounters) {
	t.Helper()
	if runtime.SlowStubCount(stubs.StubGetTable) != before.callSuspend {
		t.Fatalf("get-table slow stub count changed: before=%d after=%d", before.callSuspend, runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != before.forPrep {
		t.Fatalf("lua-call slow stub count changed: before=%d after=%d", before.forPrep, runtime.SlowStubCount(stubs.StubLuaCall))
	}
	currentTableSetStubs := runtime.SlowStubCount(stubs.StubGetGlobal) + runtime.SlowStubCount(stubs.StubSetGlobal) + runtime.SlowStubCount(stubs.StubSetTable)
	if currentTableSetStubs != before.forLoop {
		t.Fatalf("table slow stub count changed: before=%d after=%d", before.forLoop, currentTableSetStubs)
	}
	if runtime.DeoptCount() != before.deopt {
		t.Fatalf("deopt count changed: before=%d after=%d", before.deopt, runtime.DeoptCount())
	}
}

func mustFeedbackCell(t *testing.T, runtime *Runtime, closureRef value.HeapRef44, slot uint32) feedback.Cell {
	t.Helper()
	cell, err := runtime.Engine.Closures.ReadFeedbackCell(closureRef, slot)
	if err != nil {
		t.Fatalf("read feedback cell %d: %v", slot, err)
	}
	return cell
}

func assertFeedbackCellEqual(t *testing.T, got feedback.Cell, want feedback.Cell) {
	t.Helper()
	if got != want {
		t.Fatalf("feedback cell mismatch: got %+v want %+v", got, want)
	}
}

func assertNoNativeFallback(t *testing.T, runtime *Runtime) {
	t.Helper()
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("compiled path should avoid shared call slow stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.SlowStubCount(stubs.StubForPrep) != 0 {
		t.Fatalf("compiled path should avoid shared FORPREP slow stub, got %d", runtime.SlowStubCount(stubs.StubForPrep))
	}
	if runtime.SlowStubCount(stubs.StubForLoop) != 0 {
		t.Fatalf("compiled path should avoid shared FORLOOP slow stub, got %d", runtime.SlowStubCount(stubs.StubForLoop))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("compiled path should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func mustHeapOffsetForRefForTest(t *testing.T, runtimeHeap *rtheap.Heap, ref value.HeapRef44) value.HeapOff64 {
	t.Helper()
	address, err := runtimeHeap.DecodeHeapRef(ref)
	if err != nil {
		t.Fatalf("decode heap ref %#x: %v", uint64(ref), err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("offset for heap ref %#x: %v", uint64(ref), err)
	}
	return offset
}

func markHeapRefForTest(t *testing.T, runtimeHeap *rtheap.Heap, ref value.HeapRef44, mark value.MarkBits) {
	t.Helper()
	offset := mustHeapOffsetForRefForTest(t, runtimeHeap, ref)
	header, err := runtimeHeap.HeaderAtOffset(offset)
	if err != nil {
		t.Fatalf("read header for %#x: %v", uint64(ref), err)
	}
	header.Mark = mark
	if err := runtimeHeap.WriteHeader(offset, header); err != nil {
		t.Fatalf("write header for %#x: %v", uint64(ref), err)
	}
}

func assertValuesEqual(t *testing.T, got []value.TValue, want []value.TValue) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("result count = %d, want %d (%v)", len(got), len(want), got)
	}
	for index := range want {
		if got[index].Bits() != want[index].Bits() {
			t.Fatalf("result[%d] = %s, want %s", index, got[index], want[index])
		}
	}
}

func assertTableArrayValue(t *testing.T, engine *interp.Engine, tableValue value.TValue, index int, want value.TValue) {
	t.Helper()
	got, found, err := engine.ReadIndexBoundary(tableValue, value.NumberValue(float64(index)))
	if err != nil {
		t.Fatalf("read table[%d]: %v", index, err)
	}
	if !found {
		t.Fatalf("table[%d] should exist", index)
	}
	if got.Bits() != want.Bits() {
		t.Fatalf("table[%d] = %s, want %s", index, got, want)
	}
}

func assertTableLenHint(t *testing.T, engine *interp.Engine, tableValue value.TValue, want uint32) {
	t.Helper()
	ref, ok := tableValue.HeapRef()
	if !ok {
		t.Fatalf("value %s is not a heap ref", tableValue)
	}
	object, err := engine.Tables.Object(ref)
	if err != nil {
		t.Fatalf("load table object: %v", err)
	}
	if object.ArrayLenHint != want {
		t.Fatalf("table array len hint = %d, want %d", object.ArrayLenHint, want)
	}
}

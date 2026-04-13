package baseline

import (
	"fmt"
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

type hostTaggedScore struct {
	Value float64 `lua:"score"`
}

type hostTaggedMethodScore struct {
	Value float64
}

func (score *hostTaggedMethodScore) DoubleValue() float64 {
	return score.Value * 2
}

func (score *hostTaggedMethodScore) LuaMethodMap() map[string]string {
	return map[string]string{"double-score": "DoubleValue"}
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

func TestBaselineRuntimeLenStubResumesCompiledContinuation(t *testing.T) {
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
	tableValue, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new table value: %v", err)
	}
	for _, key := range []float64{1, 2, 4} {
		if err := engine.Tables.Set(tableValue.Ref, value.NumberValue(key), value.NumberValue(key*10)); err != nil {
			t.Fatalf("seed table key %v: %v", key, err)
		}
	}
	proto := &bytecode.Proto{
		Source:       "@jit-len-stub.lua",
		NumParams:    1,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_LEN, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
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
		t.Fatalf("len stub test should compile, unsupported reason: %s", compiled.UnsupportedReason)
	}
	beforeStub := runtime.SlowStubCount(stubs.StubLen)
	beforeDeopt := runtime.DeoptCount()
	results, err := runtime.Call(thread, closure.Value, []value.TValue{tableValue.Value}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(4)})
	if got := runtime.SlowStubCount(stubs.StubLen) - beforeStub; got != 1 {
		t.Fatalf("len slow path should use exactly one shared stub, got %d", got)
	}
	if runtime.DeoptCount() != beforeDeopt {
		t.Fatalf("len slow path should avoid deopt, got %d", runtime.DeoptCount()-beforeDeopt)
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

func TestBaselineRuntimeConcatStubUsesMetamethodAndAvoidsDeopt(t *testing.T) {
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
	concatKey, err := engine.InternString("__concat")
	if err != nil {
		t.Fatalf("intern __concat key: %v", err)
	}
	labelKey, err := engine.InternString("label")
	if err != nil {
		t.Fatalf("intern label key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	concatCalls := 0
	concatMeta, err := engine.RegisterHostFunction("concat-meta", func(lhs value.TValue, rhs value.TValue) (string, error) {
		concatCalls++
		leftLabel, err := concatRuntimeLabel(engine, lhs, labelKey.Value)
		if err != nil {
			return "", err
		}
		rightLabel, err := concatRuntimeLabel(engine, rhs, labelKey.Value)
		if err != nil {
			return "", err
		}
		return leftLabel + rightLabel, nil
	}, env.Value)
	if err != nil {
		t.Fatalf("register __concat host function: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, concatKey.Value, concatMeta.Value); err != nil {
		t.Fatalf("seed __concat metamethod: %v", err)
	}
	leftTable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left table: %v", err)
	}
	rightTable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right table: %v", err)
	}
	for handle, label := range map[value.HeapRef44]string{leftTable.Ref: "B", rightTable.Ref: "C"} {
		textHandle, err := engine.InternString(label)
		if err != nil {
			t.Fatalf("intern label %q: %v", label, err)
		}
		if err := engine.Tables.Set(handle, labelKey.Value, textHandle.Value); err != nil {
			t.Fatalf("seed label %q: %v", label, err)
		}
	}
	if err := engine.SetValueMetatableBoundary(leftTable.Value, metatable.Value); err != nil {
		t.Fatalf("set left metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(rightTable.Value, metatable.Value); err != nil {
		t.Fatalf("set right metatable: %v", err)
	}
	want, err := engine.InternString("BC")
	if err != nil {
		t.Fatalf("intern expected string: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-concat-meta.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CONCAT, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	beforeStub := runtime.SlowStubCount(stubs.StubConcat)
	beforeDeopt := runtime.DeoptCount()
	results, err := runtime.Call(thread, closure.Value, []value.TValue{leftTable.Value, rightTable.Value}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{want.Value})
	if got := runtime.SlowStubCount(stubs.StubConcat) - beforeStub; got != 1 {
		t.Fatalf("concat slow path should use exactly one shared stub, got %d", got)
	}
	if runtime.DeoptCount() != beforeDeopt {
		t.Fatalf("concat slow path should avoid deopt, got %d", runtime.DeoptCount()-beforeDeopt)
	}
	if concatCalls != 1 {
		t.Fatalf("__concat call count = %d, want 1", concatCalls)
	}
}

func concatRuntimeLabel(engine *interp.Engine, candidate value.TValue, labelKey value.TValue) (string, error) {
	if candidate.IsBoxedTag(value.TagTableRef) {
		ref, _ := candidate.HeapRef()
		labelValue, found, err := engine.Tables.Get(ref, labelKey)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fmt.Errorf("missing label field")
		}
		labelRef, _ := labelValue.HeapRef()
		return engine.Strings.Text(labelRef)
	}
	if candidate.IsBoxedTag(value.TagStringRef) {
		ref, _ := candidate.HeapRef()
		return engine.Strings.Text(ref)
	}
	return "", fmt.Errorf("unexpected concat operand %s", candidate)
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

func TestBaselineRuntimeOpenReturnUsesUpdatedTopAfterTestSet(t *testing.T) {
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
		Source:       "@testset-open-return.lua",
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 0, 1, 0),
			bytecode.CreateABC(bytecode.OP_TESTSET, 2, 0, 1),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 0, 0),
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
		t.Fatalf("TESTSET open return test should compile, unsupported reason: %s", compiled.UnsupportedReason)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.BoolValue(true)})
	assertNoNativeFallback(t, runtime)
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
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("host-backed TFORLOOP should use one shared call stub for the first iterator step only, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
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

func TestBaselineRuntimeArithmeticStubResumesCompiledContinuation(t *testing.T) {
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
		Source:       "@arith-stub.lua",
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("0x10"),
			bytecode.NumberConstant(2),
			bytecode.NumberConstant(-5),
			bytecode.NumberConstant(3),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_ADD, 0, 0, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 3),
			bytecode.CreateABC(bytecode.OP_MOD, 1, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 3, 0),
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
		t.Fatalf("arithmetic stub test should compile, unsupported reason: %s", compiled.UnsupportedReason)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(18), value.NumberValue(1)})
	if runtime.SlowStubCount(stubs.StubArithmetic) == 0 {
		t.Fatalf("arithmetic slow path should resume through shared stub")
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("arithmetic slow path should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeArithmeticStubReturnsLua51ErrorWithoutDeopt(t *testing.T) {
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
		Source:       "@arith-error-stub.lua",
		MaxStackSize: 2,
		Constants:    []bytecode.Constant{bytecode.NumberConstant(1)},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 0, 1, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 0),
			bytecode.CreateABC(bytecode.OP_ADD, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	_, err = runtime.Call(thread, closure.Value, nil, -1)
	if err == nil || err.Error() != "attempt to perform arithmetic on a boolean value" {
		t.Fatalf("runtime arithmetic error = %v, want Lua 5.1 boolean arithmetic error", err)
	}
	if runtime.SlowStubCount(stubs.StubArithmetic) != 1 {
		t.Fatalf("arithmetic error path should use exactly one shared stub, got %d", runtime.SlowStubCount(stubs.StubArithmetic))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("arithmetic error path should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCompareStubResumesContinuation(t *testing.T) {
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
	ltKey, err := engine.InternString("__lt")
	if err != nil {
		t.Fatalf("intern __lt key: %v", err)
	}
	left, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left table: %v", err)
	}
	right, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right table: %v", err)
	}
	leftMeta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left metatable: %v", err)
	}
	rightMeta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right metatable: %v", err)
	}
	ltMeta, err := engine.RegisterHostFunction("lt-meta", func(lhs value.TValue, rhs value.TValue) bool {
		return lhs.Bits() == left.Value.Bits() && rhs.Bits() == right.Value.Bits()
	}, env.Value)
	if err != nil {
		t.Fatalf("register __lt host function: %v", err)
	}
	for _, metatable := range []value.TValue{leftMeta.Value, rightMeta.Value} {
		metaRef, _ := metatable.HeapRef()
		if err := engine.Tables.Set(metaRef, ltKey.Value, ltMeta.Value); err != nil {
			t.Fatalf("seed __lt metamethod: %v", err)
		}
	}
	if err := engine.SetValueMetatableBoundary(left.Value, leftMeta.Value); err != nil {
		t.Fatalf("set left metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(right.Value, rightMeta.Value); err != nil {
		t.Fatalf("set right metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@compare-stub.lua",
		NumParams:    2,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 2, 0, 0),
			bytecode.CreateABC(bytecode.OP_LT, 1, 0, 1),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 2, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
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
		t.Fatalf("compare stub test should compile, unsupported reason: %s", compiled.UnsupportedReason)
	}
	beforeStub := runtime.SlowStubCount(stubs.StubCompare)
	beforeDeopt := runtime.DeoptCount()
	results, err := runtime.Call(thread, closure.Value, []value.TValue{left.Value, right.Value}, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.BoolValue(true)})
	if got := runtime.SlowStubCount(stubs.StubCompare) - beforeStub; got != 1 {
		t.Fatalf("compare slow path should use exactly one shared stub, got %d", got)
	}
	if runtime.DeoptCount() != beforeDeopt {
		t.Fatalf("compare slow path should avoid deopt, got %d", runtime.DeoptCount()-beforeDeopt)
	}
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

func TestBaselineRuntimeForPrepStubCoercesStringNumbers(t *testing.T) {
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
		Source:       "@forprep-string.lua",
		MaxStackSize: 5,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("1"),
			bytecode.StringConstant("3"),
			bytecode.StringConstant("1"),
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
	if runtime.SlowStubCount(stubs.StubForPrep) != 1 {
		t.Fatalf("FORPREP string coercion should use exactly one shared slow stub, got %d", runtime.SlowStubCount(stubs.StubForPrep))
	}
	if runtime.SlowStubCount(stubs.StubForLoop) != 0 {
		t.Fatalf("FORLOOP should stay inside native builtin after coercion, got %d", runtime.SlowStubCount(stubs.StubForLoop))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("FORPREP string coercion should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeForPrepStubReturnsLua51RoleErrorWithoutDeopt(t *testing.T) {
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
		Source:       "@forprep-error.lua",
		MaxStackSize: 5,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("1"),
			bytecode.StringConstant("3"),
			bytecode.StringConstant("oops"),
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
	_, err = runtime.Call(thread, closure.Value, nil, -1)
	if err == nil || err.Error() != "for step must be a number" {
		t.Fatalf("runtime forprep error = %v, want Lua 5.1 step error", err)
	}
	if runtime.SlowStubCount(stubs.StubForPrep) != 1 {
		t.Fatalf("FORPREP error path should use exactly one shared slow stub, got %d", runtime.SlowStubCount(stubs.StubForPrep))
	}
	if runtime.SlowStubCount(stubs.StubForLoop) != 0 {
		t.Fatalf("FORLOOP should not run after FORPREP role error, got %d", runtime.SlowStubCount(stubs.StubForLoop))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("FORPREP error path should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeForPrepStubReturnsLua51LimitErrorWithoutDeopt(t *testing.T) {
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
		Source:       "@forprep-limit-error.lua",
		MaxStackSize: 5,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("1"),
			bytecode.StringConstant("oops"),
			bytecode.StringConstant("1"),
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
	_, err = runtime.Call(thread, closure.Value, nil, -1)
	if err == nil || err.Error() != "for limit must be a number" {
		t.Fatalf("runtime forprep limit error = %v, want Lua 5.1 limit error", err)
	}
	if runtime.SlowStubCount(stubs.StubForPrep) != 1 {
		t.Fatalf("FORPREP limit error path should use exactly one shared slow stub, got %d", runtime.SlowStubCount(stubs.StubForPrep))
	}
	if runtime.SlowStubCount(stubs.StubForLoop) != 0 {
		t.Fatalf("FORLOOP should not run after FORPREP limit error, got %d", runtime.SlowStubCount(stubs.StubForLoop))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("FORPREP limit error path should avoid deopt, got %d", runtime.DeoptCount())
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
		t.Fatalf("blocked-table hit should stay inside native builtin, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubGetTable) != 0 {
		t.Fatalf("blocked-table hit should continue to avoid Go slow stubs, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("metatable blocker should not deopt exact helper path, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeMetatableBlockedMissUsesSharedSlowStub(t *testing.T) {
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
	fallback, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new fallback table: %v", err)
	}
	meta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	valueKey, err := engine.InternString("value")
	if err != nil {
		t.Fatalf("intern value key: %v", err)
	}
	indexKey, err := engine.InternString("__index")
	if err != nil {
		t.Fatalf("intern __index key: %v", err)
	}
	if err := engine.Tables.Set(fallback.Ref, valueKey.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("seed fallback table: %v", err)
	}
	if err := engine.Tables.Set(meta.Ref, indexKey.Value, fallback.Value); err != nil {
		t.Fatalf("seed metatable index: %v", err)
	}
	if err := engine.Tables.SetMetatable(box.Ref, meta.Value); err != nil {
		t.Fatalf("set metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@blocked-miss-gettable.lua",
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
	if runtime.SlowStubCount(stubs.StubGetTable) != 1 {
		t.Fatalf("blocked-table miss should use shared gettable stub once, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{box.Value}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubGetTable) != 2 {
		t.Fatalf("blocked-table miss should stay on shared gettable stub, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("blocked-table miss should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeGlobalMetamethodsStayInCompiledIsland(t *testing.T) {
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
	backing, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new backing table: %v", err)
	}
	meta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	answerKey, err := engine.InternString("answer")
	if err != nil {
		t.Fatalf("intern answer key: %v", err)
	}
	createdKey, err := engine.InternString("created")
	if err != nil {
		t.Fatalf("intern created key: %v", err)
	}
	indexKey, err := engine.InternString("__index")
	if err != nil {
		t.Fatalf("intern __index key: %v", err)
	}
	newIndexKey, err := engine.InternString("__newindex")
	if err != nil {
		t.Fatalf("intern __newindex key: %v", err)
	}
	if err := engine.Tables.Set(backing.Ref, answerKey.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("seed backing answer: %v", err)
	}
	if err := engine.Tables.Set(meta.Ref, indexKey.Value, backing.Value); err != nil {
		t.Fatalf("seed __index table: %v", err)
	}
	if err := engine.Tables.Set(meta.Ref, newIndexKey.Value, backing.Value); err != nil {
		t.Fatalf("seed __newindex table: %v", err)
	}
	if err := engine.Tables.SetMetatable(env.Ref, meta.Value); err != nil {
		t.Fatalf("set env metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@global-metatable.lua",
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("answer"),
			bytecode.StringConstant("created"),
			bytecode.NumberConstant(9),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 2),
			bytecode.CreateABx(bytecode.OP_SETGLOBAL, 1, 1),
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 2, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 4, 0),
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
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42), value.NumberValue(9), value.NumberValue(9)})
	if runtime.SlowStubCount(stubs.StubGetGlobal) != 2 {
		t.Fatalf("global __index path should use shared getglobal stub twice, got %d", runtime.SlowStubCount(stubs.StubGetGlobal))
	}
	if runtime.SlowStubCount(stubs.StubSetGlobal) != 1 {
		t.Fatalf("global __newindex path should use shared setglobal stub once, got %d", runtime.SlowStubCount(stubs.StubSetGlobal))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("global metatable path should avoid deopt, got %d", runtime.DeoptCount())
	}
	stored, found, err := engine.Tables.Get(backing.Ref, createdKey.Value)
	if err != nil {
		t.Fatalf("read created from backing: %v", err)
	}
	if !found || stored.Bits() != value.NumberValue(9).Bits() {
		t.Fatalf("created backing value = %s (found=%v), want number(9)", stored, found)
	}
	stored, found, err = engine.Tables.Get(env.Ref, createdKey.Value)
	if err != nil {
		t.Fatalf("read created from env: %v", err)
	}
	if found {
		t.Fatalf("created should be routed through __newindex table, env still has %s", stored)
	}
}

func TestBaselineRuntimeSelfMetamethodUsesSharedSlowStub(t *testing.T) {
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
	methods, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new methods table: %v", err)
	}
	meta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new receiver table: %v", err)
	}
	idKey, err := engine.InternString("id")
	if err != nil {
		t.Fatalf("intern id key: %v", err)
	}
	indexKey, err := engine.InternString("__index")
	if err != nil {
		t.Fatalf("intern __index key: %v", err)
	}
	methodProto := &bytecode.Proto{
		Source:       "@meta-self-method.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(99),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	methodClosure, err := engine.NewClosure(methodProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new method closure: %v", err)
	}
	if err := engine.Tables.Set(methods.Ref, idKey.Value, methodClosure.Value); err != nil {
		t.Fatalf("seed methods table: %v", err)
	}
	if err := engine.Tables.Set(meta.Ref, indexKey.Value, methods.Value); err != nil {
		t.Fatalf("seed __index table: %v", err)
	}
	if err := engine.Tables.SetMetatable(box.Ref, meta.Value); err != nil {
		t.Fatalf("set receiver metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@meta-self.lua",
		NumParams:    1,
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("id"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_SELF, 1, 0, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_CALL, 1, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
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
	if runtime.SlowStubCount(stubs.StubSelf) != 1 {
		t.Fatalf("SELF metatable path should use shared self stub once, got %d", runtime.SlowStubCount(stubs.StubSelf))
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("SELF continuation should still use shared call stub once, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("SELF metatable path should avoid deopt, got %d", runtime.DeoptCount())
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

func TestBaselineRuntimeDirectHostFunctionCallBecomesCoveredAfterWarmup(t *testing.T) {
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
	observed := make([]float64, 0, 2)
	directHost, err := engine.RegisterHostFunction("direct-call-host", func(x float64) float64 {
		observed = append(observed, x)
		return x + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register direct host function: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-direct-host-call-top.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{directHost.Value, value.NumberValue(41)}, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StateMonomorphic || cell.AccessKind != feedback.AccessCallHostFunction || cell.ValueBits != directHost.Value.Bits() {
		t.Fatalf("direct host call feedback cell = %+v, want direct-host monomorphic", cell)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("first direct host call should use one shared call stub for warmup, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{directHost.Value, value.NumberValue(41)}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("second direct host call should stay on covered host path after warmup, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if len(observed) != 2 || observed[0] != 41 || observed[1] != 41 {
		t.Fatalf("direct host observed args = %v, want [41 41]", observed)
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("direct host call warmup should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeDirectHostFunctionTailCallBecomesCoveredAfterWarmup(t *testing.T) {
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
	observed := make([]float64, 0, 2)
	directHost, err := engine.RegisterHostFunction("direct-tail-host", func(x float64) float64 {
		observed = append(observed, x)
		return x + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register direct tail host function: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-direct-host-tail-top.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{directHost.Value, value.NumberValue(41)}, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StateMonomorphic || cell.AccessKind != feedback.AccessCallHostFunction || cell.ValueBits != directHost.Value.Bits() {
		t.Fatalf("direct host tail feedback cell = %+v, want direct-host monomorphic", cell)
	}
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("first direct host tailcall should use one shared tail stub for warmup, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{directHost.Value, value.NumberValue(41)}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("second direct host tailcall should stay on covered host path after warmup, got %d shared tail stubs", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if len(observed) != 2 || observed[0] != 41 || observed[1] != 41 {
		t.Fatalf("direct host tail observed args = %v, want [41 41]", observed)
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("direct host tailcall warmup should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeDirectHostAndTMCallHostFunctionMixedCallSiteStaysCoveredAfterPolymorphicWarmup(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	directObserved := make([]float64, 0, 2)
	directHost, err := engine.RegisterHostFunction("mixed-direct-host", func(x float64) float64 {
		directObserved = append(directObserved, x)
		return x + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register direct host function: %v", err)
	}
	metaObserved := make([]float64, 0, 2)
	callMeta, err := engine.RegisterHostFunction("mixed-call-meta", func(_ value.TValue, x float64) float64 {
		metaObserved = append(metaObserved, x)
		return x + 2
	}, env.Value)
	if err != nil {
		t.Fatalf("register __call host function: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, callMeta.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-direct-host-mixed-call-top.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{directHost.Value, value.NumberValue(40)}, -1)
	if err != nil {
		t.Fatalf("first direct runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("first direct host call should use one shared call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable.Value, value.NumberValue(40)}, -1)
	if err != nil {
		t.Fatalf("first tm_call runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("first callable-object host call should use one shared call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StatePolymorphic {
		t.Fatalf("mixed direct-host/tm_call feedback cell = %+v, want polymorphic", cell)
	}
	matched, ok, err := engine.MatchCallFeedbackCell(directHost.Value, cell)
	if err != nil || !ok || matched.Bits() != directHost.Value.Bits() {
		t.Fatalf("polymorphic feedback should still match direct host function = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, directHost.Value)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable.Value, cell)
	if err != nil || !ok || matched.Bits() != callMeta.Value.Bits() {
		t.Fatalf("polymorphic feedback should match callable object host metamethod = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, callMeta.Value)
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{directHost.Value, value.NumberValue(40)}, -1)
	if err != nil {
		t.Fatalf("second direct runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable.Value, value.NumberValue(40)}, -1)
	if err != nil {
		t.Fatalf("second tm_call runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("polymorphic warmup should keep later direct-host/tm_call host calls on covered path, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if len(directObserved) != 2 || directObserved[0] != 40 || directObserved[1] != 40 {
		t.Fatalf("direct host observed args = %v, want [40 40]", directObserved)
	}
	if len(metaObserved) != 2 || metaObserved[0] != 40 || metaObserved[1] != 40 {
		t.Fatalf("tm_call host observed args = %v, want [40 40]", metaObserved)
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("mixed direct-host/tm_call host site should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeDirectHostAndTMCallHostFunctionMixedTailSiteStaysCoveredAfterPolymorphicWarmup(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	directObserved := make([]float64, 0, 2)
	directHost, err := engine.RegisterHostFunction("mixed-direct-tail-host", func(x float64) float64 {
		directObserved = append(directObserved, x)
		return x + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register direct tail host function: %v", err)
	}
	metaObserved := make([]float64, 0, 2)
	callMeta, err := engine.RegisterHostFunction("mixed-tail-call-meta", func(_ value.TValue, x float64) float64 {
		metaObserved = append(metaObserved, x)
		return x + 2
	}, env.Value)
	if err != nil {
		t.Fatalf("register tail __call host function: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, callMeta.Value); err != nil {
		t.Fatalf("seed tail __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-direct-host-mixed-tail-top.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{directHost.Value, value.NumberValue(40)}, -1)
	if err != nil {
		t.Fatalf("first direct runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("first direct host tailcall should use one shared tail stub, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable.Value, value.NumberValue(40)}, -1)
	if err != nil {
		t.Fatalf("first tm_call runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 2 {
		t.Fatalf("first callable-object host tailcall should use one shared tail stub, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StatePolymorphic {
		t.Fatalf("mixed direct-host/tm_call tail feedback cell = %+v, want polymorphic", cell)
	}
	matched, ok, err := engine.MatchCallFeedbackCell(directHost.Value, cell)
	if err != nil || !ok || matched.Bits() != directHost.Value.Bits() {
		t.Fatalf("polymorphic tail feedback should still match direct host function = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, directHost.Value)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable.Value, cell)
	if err != nil || !ok || matched.Bits() != callMeta.Value.Bits() {
		t.Fatalf("polymorphic tail feedback should match callable object host metamethod = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, callMeta.Value)
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{directHost.Value, value.NumberValue(40)}, -1)
	if err != nil {
		t.Fatalf("second direct runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable.Value, value.NumberValue(40)}, -1)
	if err != nil {
		t.Fatalf("second tm_call runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 2 {
		t.Fatalf("polymorphic warmup should keep later direct-host/tm_call host tailcalls on covered path, got %d shared tail stubs", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if len(directObserved) != 2 || directObserved[0] != 40 || directObserved[1] != 40 {
		t.Fatalf("direct host tail observed args = %v, want [40 40]", directObserved)
	}
	if len(metaObserved) != 2 || metaObserved[0] != 40 || metaObserved[1] != 40 {
		t.Fatalf("tm_call host tail observed args = %v, want [40 40]", metaObserved)
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("mixed direct-host/tm_call host tail site should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallHostMetamethodResumesCompiledContinuation(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callMeta, err := engine.RegisterHostFunction("call-meta", func(_ value.TValue, x float64, y float64) float64 {
		return x + y
	}, env.Value)
	if err != nil {
		t.Fatalf("register __call host function: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, callMeta.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", callable.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-tmcall-host-metamethod.lua",
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(10),
			bytecode.NumberConstant(32),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 3, 2),
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
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell0 := mustFeedbackCell(t, runtime, closure.Ref, 0)
	cell1 := mustFeedbackCell(t, runtime, closure.Ref, 1)
	if !isResolvedCallFeedbackCell(cell0, feedback.AccessCallResolvedHostFunction, callable.Value) && !isResolvedCallFeedbackCell(cell1, feedback.AccessCallResolvedHostFunction, callable.Value) {
		t.Fatalf("tm_call host metamethod feedback cells = [%+v %+v], want one resolved-host monomorphic cell on callable receiver", cell0, cell1)
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell0 = mustFeedbackCell(t, runtime, closure.Ref, 0)
	cell1 = mustFeedbackCell(t, runtime, closure.Ref, 1)
	if !isResolvedCallFeedbackCell(cell0, feedback.AccessCallResolvedHostFunction, callable.Value) && !isResolvedCallFeedbackCell(cell1, feedback.AccessCallResolvedHostFunction, callable.Value) {
		t.Fatalf("tm_call host metamethod feedback should stay monomorphic, got [%+v %+v]", cell0, cell1)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("tm_call host metamethod should stay on covered host path after warmup, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call host metamethod should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallHostMetamethodVersionInvalidatesCoveredPath(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callMeta1, err := engine.RegisterHostFunction("call-meta-1", func(_ value.TValue, x float64) float64 {
		return x
	}, env.Value)
	if err != nil {
		t.Fatalf("register __call host function1: %v", err)
	}
	callMeta2, err := engine.RegisterHostFunction("call-meta-2", func(_ value.TValue, x float64) float64 {
		return x + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register __call host function2: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, callMeta1.Value); err != nil {
		t.Fatalf("seed __call metamethod1: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", callable.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@jit-tmcall-host-shape-call-top.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(41),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("warm tm_call host callable should use one shared call stub before covered path, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, callMeta2.Value); err != nil {
		t.Fatalf("swap __call host metamethod: %v", err)
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("third runtime call after metatable change: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("host metatable change should force one shared call stub refresh, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("fourth runtime call after metatable change: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("refreshed tm_call host callable should return to covered path, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	cell0 := mustFeedbackCell(t, runtime, closure.Ref, 0)
	cell1 := mustFeedbackCell(t, runtime, closure.Ref, 1)
	if !isResolvedCallFeedbackCell(cell0, feedback.AccessCallResolvedHostFunction, callable.Value) && !isResolvedCallFeedbackCell(cell1, feedback.AccessCallResolvedHostFunction, callable.Value) {
		t.Fatalf("tm_call host feedback should stay monomorphic after metatable change, got [%+v %+v]", cell0, cell1)
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call host metatable invalidation should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallHostWrapperMetamethodBecomesCoveredAfterWarmup(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	wrapper, err := engine.RegisterHostObject("bag", map[string]float64{"x": 1}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	callMeta, err := engine.RegisterHostFunction("call-wrapper", func(_ value.TValue, x float64) float64 {
		return x + 2
	}, env.Value)
	if err != nil {
		t.Fatalf("register wrapper __call host function: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new wrapper metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, callMeta.Value); err != nil {
		t.Fatalf("seed wrapper __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(wrapper.Value, metatable.Value); err != nil {
		t.Fatalf("set wrapper metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", wrapper.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@jit-tmcall-host-wrapper-top.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(40),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("first tm_call host wrapper call should use one shared call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if !isResolvedCallFeedbackCell(cell, feedback.AccessCallResolvedHostFunction, wrapper.Value) {
		cell = mustFeedbackCell(t, runtime, closure.Ref, 1)
	}
	if !isResolvedCallFeedbackCell(cell, feedback.AccessCallResolvedHostFunction, wrapper.Value) {
		t.Fatalf("host-wrapper tm_call feedback cell = %+v, want resolved-host monomorphic", cell)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("second tm_call host wrapper call should stay on covered host path after warmup, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call host wrapper warmup should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallHostWrapperLuaClosureMetamethodBecomesCoveredAfterWarmup(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	metaProto := &bytecode.Proto{
		Source:       "@jit-tmcall-host-wrapper-lua-meta.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaClosure, err := engine.NewClosure(metaProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new tm_call host-wrapper closure: %v", err)
	}
	if _, err := runtime.Compile(metaProto); err != nil {
		t.Fatalf("compile tm_call host-wrapper proto: %v", err)
	}
	wrapper, err := engine.RegisterHostObject("bag", map[string]float64{"x": 1}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new wrapper metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, metaClosure.Value); err != nil {
		t.Fatalf("seed wrapper __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(wrapper.Value, metatable.Value); err != nil {
		t.Fatalf("set wrapper metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", wrapper.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@jit-tmcall-host-wrapper-lua-top.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(40),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(40)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("first tm_call host-wrapper lua closure call should use one shared call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(40)})
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if !isResolvedCallFeedbackCell(cell, feedback.AccessCallResolvedLuaClosure, wrapper.Value) {
		cell = mustFeedbackCell(t, runtime, closure.Ref, 1)
	}
	if !isResolvedCallFeedbackCell(cell, feedback.AccessCallResolvedLuaClosure, wrapper.Value) {
		t.Fatalf("host-wrapper lua tm_call feedback cell = %+v, want resolved-lua monomorphic", cell)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("second tm_call host-wrapper lua closure call should stay on covered path after warmup, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call host-wrapper lua closure warmup should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallLuaClosureMetamethodKeepsNestedCompiledCall(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	addOne, err := engine.RegisterHostFunction("add1", func(v float64) float64 {
		return v + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register host function: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "add1", addOne.Value); err != nil {
		t.Fatalf("set global add1: %v", err)
	}
	metaProto := &bytecode.Proto{
		Source:       "@jit-tmcall-lua-meta.lua",
		NumParams:    2,
		MaxStackSize: 4,
		Constants:    []bytecode.Constant{bytecode.StringConstant("add1")},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 2, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 3, 1, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 2, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
		},
	}
	metaClosure, err := engine.NewClosure(metaProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new tm_call metamethod closure: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, metaClosure.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", callable.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-tmcall-lua-metamethod-top.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(42),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
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
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(43)})
	cell0 := mustFeedbackCell(t, runtime, closure.Ref, 0)
	cell1 := mustFeedbackCell(t, runtime, closure.Ref, 1)
	if !isResolvedCallFeedbackCell(cell0, feedback.AccessCallResolvedLuaClosure, callable.Value) && !isResolvedCallFeedbackCell(cell1, feedback.AccessCallResolvedLuaClosure, callable.Value) {
		t.Fatalf("tm_call covered-call feedback cells = [%+v %+v], want one resolved-lua monomorphic cell on callable receiver", cell0, cell1)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("tm_call lua closure metamethod should keep nested compiled host call, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call lua closure metamethod should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallLuaClosureMetamethodBecomesCoveredAfterWarmup(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	metaProto := &bytecode.Proto{
		Source:       "@jit-tmcall-covered-call-meta.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaClosure, err := engine.NewClosure(metaProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new tm_call metamethod closure: %v", err)
	}
	if _, err := runtime.Compile(metaProto); err != nil {
		t.Fatalf("compile tm_call metamethod proto: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, metaClosure.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", callable.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@jit-tmcall-covered-call-top.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(42),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell0 := mustFeedbackCell(t, runtime, closure.Ref, 0)
	cell1 := mustFeedbackCell(t, runtime, closure.Ref, 1)
	if !isResolvedCallFeedbackCell(cell0, feedback.AccessCallResolvedLuaClosure, callable.Value) && !isResolvedCallFeedbackCell(cell1, feedback.AccessCallResolvedLuaClosure, callable.Value) {
		t.Fatalf("tm_call covered-tail feedback cells = [%+v %+v], want one resolved-lua monomorphic cell on callable receiver", cell0, cell1)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("first tm_call lua closure call should use one shared call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("second tm_call lua closure call should stay on covered fast path after warmup, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call covered-call warmup should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeDirectAndTMCallLuaClosureCallSiteStaysCoveredAfterPolymorphicWarmup(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	directProto := &bytecode.Proto{
		Source:       "@jit-direct-poly-call-callee.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	directClosure, err := engine.NewClosure(directProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new direct closure: %v", err)
	}
	if _, err := runtime.Compile(directProto); err != nil {
		t.Fatalf("compile direct proto: %v", err)
	}
	metaProto := &bytecode.Proto{
		Source:       "@jit-direct-poly-call-meta.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaClosure, err := engine.NewClosure(metaProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new tm_call metamethod closure: %v", err)
	}
	if _, err := runtime.Compile(metaProto); err != nil {
		t.Fatalf("compile meta proto: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, metaClosure.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@jit-direct-poly-call-top.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{directClosure.Value, value.NumberValue(42)}, -1)
	if err != nil {
		t.Fatalf("direct runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 0 {
		t.Fatalf("direct covered call should avoid shared call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable.Value, value.NumberValue(42)}, -1)
	if err != nil {
		t.Fatalf("first tm_call runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("first callable-object call should use one shared call stub, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StatePolymorphic {
		t.Fatalf("mixed direct/tm_call feedback cell = %+v, want polymorphic", cell)
	}
	matched, ok, err := engine.MatchCallFeedbackCell(directClosure.Value, cell)
	if err != nil || !ok || matched.Bits() != directClosure.Value.Bits() {
		t.Fatalf("polymorphic feedback should still match direct closure = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, directClosure.Value)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable.Value, cell)
	if err != nil || !ok || matched.Bits() != metaClosure.Value.Bits() {
		t.Fatalf("polymorphic feedback should match callable object = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, metaClosure.Value)
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{directClosure.Value, value.NumberValue(42)}, -1)
	if err != nil {
		t.Fatalf("second direct runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable.Value, value.NumberValue(42)}, -1)
	if err != nil {
		t.Fatalf("second tm_call runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("polymorphic warmup should keep later direct/tm_call calls on covered path, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("mixed direct/tm_call call site should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallLuaClosureMetamethodVersionInvalidatesCoveredPath(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	metaProto1 := &bytecode.Proto{
		Source:       "@jit-tmcall-shape-call-meta-1.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaProto2 := &bytecode.Proto{
		Source:       "@jit-tmcall-shape-call-meta-2.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Constants:    []bytecode.Constant{bytecode.NumberConstant(1)},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_ADD, 0, 1, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaClosure1, err := engine.NewClosure(metaProto1, env.Value, nil)
	if err != nil {
		t.Fatalf("new tm_call metamethod closure1: %v", err)
	}
	metaClosure2, err := engine.NewClosure(metaProto2, env.Value, nil)
	if err != nil {
		t.Fatalf("new tm_call metamethod closure2: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, metaClosure1.Value); err != nil {
		t.Fatalf("seed __call metamethod 1: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", callable.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@jit-tmcall-shape-call-top.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(41),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(topProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("warm tm_call callable should use one shared call stub before covered path, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, metaClosure2.Value); err != nil {
		t.Fatalf("swap __call metamethod: %v", err)
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("third runtime call after metatable change: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("metatable change should force one shared call stub refresh, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("fourth runtime call after metatable change: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("refreshed tm_call callable should return to covered path, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	cell0 := mustFeedbackCell(t, runtime, closure.Ref, 0)
	cell1 := mustFeedbackCell(t, runtime, closure.Ref, 1)
	if !isResolvedCallFeedbackCell(cell0, feedback.AccessCallResolvedLuaClosure, callable.Value) && !isResolvedCallFeedbackCell(cell1, feedback.AccessCallResolvedLuaClosure, callable.Value) {
		t.Fatalf("tm_call feedback should stay monomorphic after metatable change, got [%+v %+v]", cell0, cell1)
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call metatable version invalidation should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallTypeMetatableRefreshStaysMonomorphic(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callMeta1, err := engine.RegisterHostFunction("call-meta-1", func(value.TValue) float64 { return 41 }, env.Value)
	if err != nil {
		t.Fatalf("register __call host function1: %v", err)
	}
	callMeta2, err := engine.RegisterHostFunction("call-meta-2", func(value.TValue) float64 { return 42 }, env.Value)
	if err != nil {
		t.Fatalf("register __call host function2: %v", err)
	}
	metatable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable1: %v", err)
	}
	metatable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable2: %v", err)
	}
	if err := engine.Tables.Set(metatable1.Ref, callKey.Value, callMeta1.Value); err != nil {
		t.Fatalf("seed metatable1 __call: %v", err)
	}
	if err := engine.Tables.Set(metatable2.Ref, callKey.Value, callMeta2.Value); err != nil {
		t.Fatalf("seed metatable2 __call: %v", err)
	}
	callableNumber := value.NumberValue(7)
	if err := engine.SetValueMetatableBoundary(callableNumber, metatable1.Value); err != nil {
		t.Fatalf("set number metatable1: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-tmcall-type-meta-top.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if !isResolvedCallFeedbackCell(cell, feedback.AccessCallResolvedHostFunction, callableNumber) {
		t.Fatalf("type-metatable tm_call feedback cell = %+v, want resolved-host monomorphic", cell)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("type-metatable tm_call should become covered after warmup, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if err := engine.SetValueMetatableBoundary(callableNumber, metatable2.Value); err != nil {
		t.Fatalf("set number metatable2: %v", err)
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("third runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell = mustFeedbackCell(t, runtime, closure.Ref, 0)
	if !isResolvedCallFeedbackCell(cell, feedback.AccessCallResolvedHostFunction, callableNumber) {
		t.Fatalf("type-metatable tm_call feedback should stay monomorphic after metatable change, got %+v", cell)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("type-metatable metatable change should force one shared call stub refresh, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("fourth runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("refreshed type-metatable tm_call should return to covered path, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("type-metatable tm_call refresh should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallTypeMetatableLuaClosureRefreshStaysMonomorphic(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	metaProto1 := &bytecode.Proto{
		Source:       "@jit-tmcall-type-lua-meta-1.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Constants:    []bytecode.Constant{bytecode.NumberConstant(41)},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaProto2 := &bytecode.Proto{
		Source:       "@jit-tmcall-type-lua-meta-2.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Constants:    []bytecode.Constant{bytecode.NumberConstant(42)},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaClosure1, err := engine.NewClosure(metaProto1, env.Value, nil)
	if err != nil {
		t.Fatalf("new type-metatable lua closure1: %v", err)
	}
	metaClosure2, err := engine.NewClosure(metaProto2, env.Value, nil)
	if err != nil {
		t.Fatalf("new type-metatable lua closure2: %v", err)
	}
	if _, err := runtime.Compile(metaProto1); err != nil {
		t.Fatalf("compile type-metatable lua proto1: %v", err)
	}
	if _, err := runtime.Compile(metaProto2); err != nil {
		t.Fatalf("compile type-metatable lua proto2: %v", err)
	}
	metatable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable1: %v", err)
	}
	metatable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable2: %v", err)
	}
	if err := engine.Tables.Set(metatable1.Ref, callKey.Value, metaClosure1.Value); err != nil {
		t.Fatalf("seed metatable1 __call: %v", err)
	}
	if err := engine.Tables.Set(metatable2.Ref, callKey.Value, metaClosure2.Value); err != nil {
		t.Fatalf("seed metatable2 __call: %v", err)
	}
	callableNumber := value.NumberValue(7)
	if err := engine.SetValueMetatableBoundary(callableNumber, metatable1.Value); err != nil {
		t.Fatalf("set number metatable1: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-tmcall-type-lua-top.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if !isResolvedCallFeedbackCell(cell, feedback.AccessCallResolvedLuaClosure, callableNumber) {
		t.Fatalf("type-metatable lua tm_call feedback cell = %+v, want resolved-lua monomorphic", cell)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("type-metatable lua tm_call should become covered after warmup, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if err := engine.SetValueMetatableBoundary(callableNumber, metatable2.Value); err != nil {
		t.Fatalf("set number metatable2: %v", err)
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("third runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell = mustFeedbackCell(t, runtime, closure.Ref, 0)
	if !isResolvedCallFeedbackCell(cell, feedback.AccessCallResolvedLuaClosure, callableNumber) {
		t.Fatalf("type-metatable lua tm_call feedback should stay monomorphic after metatable change, got %+v", cell)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("type-metatable lua metatable change should force one shared call stub refresh, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("fourth runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubLuaCall) != 2 {
		t.Fatalf("refreshed type-metatable lua tm_call should return to covered path, got %d shared call stubs", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("type-metatable lua tm_call refresh should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallDifferentCallableReceiversBecomePolymorphic(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callMeta1, err := engine.RegisterHostFunction("call-meta-1-poly", func(value.TValue, float64) float64 { return 41 }, env.Value)
	if err != nil {
		t.Fatalf("register __call host function1: %v", err)
	}
	callMeta2, err := engine.RegisterHostFunction("call-meta-2-poly", func(value.TValue, float64) float64 { return 42 }, env.Value)
	if err != nil {
		t.Fatalf("register __call host function2: %v", err)
	}
	callMeta3, err := engine.RegisterHostFunction("call-meta-3-poly", func(value.TValue, float64) float64 { return 43 }, env.Value)
	if err != nil {
		t.Fatalf("register __call host function3: %v", err)
	}
	callable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable1: %v", err)
	}
	callable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable2: %v", err)
	}
	callable3, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable3: %v", err)
	}
	metatable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable1 metatable: %v", err)
	}
	metatable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable2 metatable: %v", err)
	}
	metatable3, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable3 metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable1.Ref, callKey.Value, callMeta1.Value); err != nil {
		t.Fatalf("seed callable1 __call: %v", err)
	}
	if err := engine.Tables.Set(metatable2.Ref, callKey.Value, callMeta2.Value); err != nil {
		t.Fatalf("seed callable2 __call: %v", err)
	}
	if err := engine.Tables.Set(metatable3.Ref, callKey.Value, callMeta3.Value); err != nil {
		t.Fatalf("seed callable3 __call: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable1.Value, metatable1.Value); err != nil {
		t.Fatalf("set callable1 metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable2.Value, metatable2.Value); err != nil {
		t.Fatalf("set callable2 metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable3.Value, metatable3.Value); err != nil {
		t.Fatalf("set callable3 metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-tmcall-polymorphic-top.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{callable1.Value, value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable2.Value, value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StatePolymorphic {
		t.Fatalf("call feedback state = %d, want polymorphic", cell.State)
	}
	matched, ok, err := engine.MatchCallFeedbackCell(callable1.Value, cell)
	if err != nil || !ok || matched.Bits() != callMeta1.Value.Bits() {
		t.Fatalf("polymorphic feedback should match callable1 = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, callMeta1.Value)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable2.Value, cell)
	if err != nil || !ok || matched.Bits() != callMeta2.Value.Bits() {
		t.Fatalf("polymorphic feedback should match callable2 = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, callMeta2.Value)
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable3.Value, value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("third runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(43)})
	cell = mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StateMegamorphic {
		t.Fatalf("third callable receiver should force megamorphic state, got %+v", cell)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable3.Value, cell)
	if err != nil || !ok || matched.Bits() != callMeta3.Value.Bits() {
		t.Fatalf("third callable receiver should populate megamorphic cache for callable3 = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, callMeta3.Value)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable1.Value, cell)
	if err != nil || !ok || matched.Bits() != callMeta1.Value.Bits() {
		t.Fatalf("megamorphic cache should retain callable1 = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, callMeta1.Value)
	}
	before := runtime.SlowStubCount(stubs.StubLuaCall)
	beforeBoundary := runtime.MegamorphicCallBoundaryCount()
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable1.Value, value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("post-megamorphic runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if got := runtime.MegamorphicCallBoundaryCount() - beforeBoundary; got != 0 {
		t.Fatalf("cached megamorphic tm_call receiver should stay on covered path, got %d runtime boundaries", got)
	}
	beforeBoundary = runtime.MegamorphicCallBoundaryCount()
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable3.Value, value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("second cached megamorphic runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(43)})
	if got := runtime.MegamorphicCallBoundaryCount() - beforeBoundary; got != 0 {
		t.Fatalf("another cached megamorphic tm_call receiver should stay on covered path, got %d runtime boundaries", got)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != before {
		t.Fatalf("megamorphic tm_call should stay on call-family runtime boundary without growing shared call stubs: before=%d after=%d", before, runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("polymorphic shared tm_call should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallMegamorphicTailSiteStaysOffSharedTailStub(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callMeta1, err := engine.RegisterHostFunction("tail-call-meta-1-mega", func(value.TValue, float64) float64 { return 41 }, env.Value)
	if err != nil {
		t.Fatalf("register __call host function1: %v", err)
	}
	callMeta2, err := engine.RegisterHostFunction("tail-call-meta-2-mega", func(value.TValue, float64) float64 { return 42 }, env.Value)
	if err != nil {
		t.Fatalf("register __call host function2: %v", err)
	}
	callMeta3, err := engine.RegisterHostFunction("tail-call-meta-3-mega", func(value.TValue, float64) float64 { return 43 }, env.Value)
	if err != nil {
		t.Fatalf("register __call host function3: %v", err)
	}
	callable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable1: %v", err)
	}
	callable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable2: %v", err)
	}
	callable3, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable3: %v", err)
	}
	metatable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable1 metatable: %v", err)
	}
	metatable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable2 metatable: %v", err)
	}
	metatable3, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable3 metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable1.Ref, callKey.Value, callMeta1.Value); err != nil {
		t.Fatalf("seed callable1 __call: %v", err)
	}
	if err := engine.Tables.Set(metatable2.Ref, callKey.Value, callMeta2.Value); err != nil {
		t.Fatalf("seed callable2 __call: %v", err)
	}
	if err := engine.Tables.Set(metatable3.Ref, callKey.Value, callMeta3.Value); err != nil {
		t.Fatalf("seed callable3 __call: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable1.Value, metatable1.Value); err != nil {
		t.Fatalf("set callable1 metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable2.Value, metatable2.Value); err != nil {
		t.Fatalf("set callable2 metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable3.Value, metatable3.Value); err != nil {
		t.Fatalf("set callable3 metatable: %v", err)
	}
	tailProto := &bytecode.Proto{
		Source:       "@jit-tmcall-megamorphic-tail-top.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(tailProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{callable1.Value, value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable2.Value, value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StatePolymorphic {
		t.Fatalf("tail feedback state = %d, want polymorphic", cell.State)
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable3.Value, value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("third runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(43)})
	cell = mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StateMegamorphic {
		t.Fatalf("third callable receiver should force megamorphic tail state, got %+v", cell)
	}
	matched, ok, err := engine.MatchCallFeedbackCell(callable3.Value, cell)
	if err != nil || !ok || matched.Bits() != callMeta3.Value.Bits() {
		t.Fatalf("third callable receiver should populate megamorphic tail cache for callable3 = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, callMeta3.Value)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable1.Value, cell)
	if err != nil || !ok || matched.Bits() != callMeta1.Value.Bits() {
		t.Fatalf("megamorphic tail cache should retain callable1 = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, callMeta1.Value)
	}
	before := runtime.SlowStubCount(stubs.StubTailCall)
	beforeBoundary := runtime.MegamorphicCallBoundaryCount()
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable1.Value, value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("post-megamorphic tail runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if got := runtime.MegamorphicCallBoundaryCount() - beforeBoundary; got != 0 {
		t.Fatalf("cached megamorphic tm_call tail receiver should stay on covered path, got %d runtime boundaries", got)
	}
	beforeBoundary = runtime.MegamorphicCallBoundaryCount()
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable3.Value, value.NumberValue(0)}, -1)
	if err != nil {
		t.Fatalf("second cached megamorphic tail runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(43)})
	if got := runtime.MegamorphicCallBoundaryCount() - beforeBoundary; got != 0 {
		t.Fatalf("another cached megamorphic tm_call tail receiver should stay on covered path, got %d runtime boundaries", got)
	}
	if runtime.SlowStubCount(stubs.StubTailCall) != before {
		t.Fatalf("megamorphic tm_call tail site should stay on call-family runtime boundary without growing shared tail stubs: before=%d after=%d", before, runtime.SlowStubCount(stubs.StubTailCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("megamorphic tm_call tail site should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallHostMetamethodTailCallBecomesCoveredAfterWarmup(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callMeta, err := engine.RegisterHostFunction("tail-call-meta", func(_ value.TValue, x float64) float64 {
		return x + 1
	}, env.Value)
	if err != nil {
		t.Fatalf("register __call host function: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, callMeta.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", callable.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	tailProto := &bytecode.Proto{
		Source:       "@jit-tmcall-host-tail-top.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(41),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(tailProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell0 := mustFeedbackCell(t, runtime, closure.Ref, 0)
	cell1 := mustFeedbackCell(t, runtime, closure.Ref, 1)
	if !isResolvedCallFeedbackCell(cell0, feedback.AccessCallResolvedHostFunction, callable.Value) && !isResolvedCallFeedbackCell(cell1, feedback.AccessCallResolvedHostFunction, callable.Value) {
		t.Fatalf("tm_call host tail feedback cells = [%+v %+v], want one resolved-host monomorphic cell on callable receiver", cell0, cell1)
	}
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("first tm_call host tailcall should use one shared tail stub, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("second tm_call host tailcall should stay on covered host path after warmup, got %d shared tail stubs", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call host tailcall warmup should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallHostWrapperLuaClosureTailCallBecomesCoveredAfterWarmup(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	metaProto := &bytecode.Proto{
		Source:       "@jit-tmcall-host-wrapper-lua-tail-meta.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaClosure, err := engine.NewClosure(metaProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new tm_call host-wrapper tail closure: %v", err)
	}
	if _, err := runtime.Compile(metaProto); err != nil {
		t.Fatalf("compile tm_call host-wrapper tail proto: %v", err)
	}
	wrapper, err := engine.RegisterHostObject("bag", map[string]float64{"x": 1}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new wrapper metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, metaClosure.Value); err != nil {
		t.Fatalf("seed wrapper __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(wrapper.Value, metatable.Value); err != nil {
		t.Fatalf("set wrapper metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", wrapper.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	tailProto := &bytecode.Proto{
		Source:       "@jit-tmcall-host-wrapper-lua-tail-top.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(41),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(tailProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	cell0 := mustFeedbackCell(t, runtime, closure.Ref, 0)
	cell1 := mustFeedbackCell(t, runtime, closure.Ref, 1)
	if !isResolvedCallFeedbackCell(cell0, feedback.AccessCallResolvedLuaClosure, wrapper.Value) && !isResolvedCallFeedbackCell(cell1, feedback.AccessCallResolvedLuaClosure, wrapper.Value) {
		t.Fatalf("tm_call host-wrapper lua tail feedback cells = [%+v %+v], want one resolved-lua monomorphic cell on callable receiver", cell0, cell1)
	}
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("first tm_call host-wrapper lua closure tailcall should use one shared tail stub, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("second tm_call host-wrapper lua closure tailcall should stay on covered path after warmup, got %d shared tail stubs", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call host-wrapper lua closure tailcall warmup should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallTypeMetatableLuaClosureTailRefreshStaysMonomorphic(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	metaProto1 := &bytecode.Proto{
		Source:       "@jit-tmcall-type-lua-tail-meta-1.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Constants:    []bytecode.Constant{bytecode.NumberConstant(41)},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaProto2 := &bytecode.Proto{
		Source:       "@jit-tmcall-type-lua-tail-meta-2.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Constants:    []bytecode.Constant{bytecode.NumberConstant(42)},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaClosure1, err := engine.NewClosure(metaProto1, env.Value, nil)
	if err != nil {
		t.Fatalf("new type-metatable tail lua closure1: %v", err)
	}
	metaClosure2, err := engine.NewClosure(metaProto2, env.Value, nil)
	if err != nil {
		t.Fatalf("new type-metatable tail lua closure2: %v", err)
	}
	if _, err := runtime.Compile(metaProto1); err != nil {
		t.Fatalf("compile type-metatable tail lua proto1: %v", err)
	}
	if _, err := runtime.Compile(metaProto2); err != nil {
		t.Fatalf("compile type-metatable tail lua proto2: %v", err)
	}
	metatable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable1: %v", err)
	}
	metatable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable2: %v", err)
	}
	if err := engine.Tables.Set(metatable1.Ref, callKey.Value, metaClosure1.Value); err != nil {
		t.Fatalf("seed metatable1 __call: %v", err)
	}
	if err := engine.Tables.Set(metatable2.Ref, callKey.Value, metaClosure2.Value); err != nil {
		t.Fatalf("seed metatable2 __call: %v", err)
	}
	callableNumber := value.NumberValue(7)
	if err := engine.SetValueMetatableBoundary(callableNumber, metatable1.Value); err != nil {
		t.Fatalf("set number metatable1: %v", err)
	}
	tailProto := &bytecode.Proto{
		Source:       "@jit-tmcall-type-lua-tail-top.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 1, 0),
		},
	}
	closure, err := engine.NewClosure(tailProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(41)})
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if !isResolvedCallFeedbackCell(cell, feedback.AccessCallResolvedLuaClosure, callableNumber) {
		t.Fatalf("type-metatable lua tail feedback cell = %+v, want resolved-lua monomorphic", cell)
	}
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("type-metatable lua tailcall should become covered after warmup, got %d shared tail stubs", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if err := engine.SetValueMetatableBoundary(callableNumber, metatable2.Value); err != nil {
		t.Fatalf("set number metatable2: %v", err)
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("third runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell = mustFeedbackCell(t, runtime, closure.Ref, 0)
	if !isResolvedCallFeedbackCell(cell, feedback.AccessCallResolvedLuaClosure, callableNumber) {
		t.Fatalf("type-metatable lua tail feedback should stay monomorphic after metatable change, got %+v", cell)
	}
	if runtime.SlowStubCount(stubs.StubTailCall) != 2 {
		t.Fatalf("type-metatable lua tail metatable change should force one shared tail stub refresh, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callableNumber}, -1)
	if err != nil {
		t.Fatalf("fourth runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 2 {
		t.Fatalf("refreshed type-metatable lua tailcall should return to covered path, got %d shared tail stubs", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("type-metatable lua tail refresh should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallLuaClosureTailCallBecomesCoveredAfterWarmup(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	metaProto := &bytecode.Proto{
		Source:       "@jit-tmcall-covered-tail-meta.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaClosure, err := engine.NewClosure(metaProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new tm_call metamethod closure: %v", err)
	}
	if _, err := runtime.Compile(metaProto); err != nil {
		t.Fatalf("compile tm_call metamethod proto: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, metaClosure.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", callable.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	tailProto := &bytecode.Proto{
		Source:       "@jit-tmcall-covered-tail-top.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(42),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(tailProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	cell0 := mustFeedbackCell(t, runtime, closure.Ref, 0)
	cell1 := mustFeedbackCell(t, runtime, closure.Ref, 1)
	if !isResolvedCallFeedbackCell(cell0, feedback.AccessCallResolvedLuaClosure, callable.Value) && !isResolvedCallFeedbackCell(cell1, feedback.AccessCallResolvedLuaClosure, callable.Value) {
		t.Fatalf("tm_call covered-tail feedback cells = [%+v %+v], want one resolved-lua monomorphic cell on callable receiver", cell0, cell1)
	}
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("first tm_call lua closure tailcall should use one shared tail stub, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("second tm_call lua closure tailcall should stay on covered fast path after warmup, got %d shared tail stubs", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call covered-tail warmup should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeDirectAndTMCallLuaClosureTailSiteStaysCoveredAfterPolymorphicWarmup(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	directProto := &bytecode.Proto{
		Source:       "@jit-direct-poly-tail-callee.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	directClosure, err := engine.NewClosure(directProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new direct closure: %v", err)
	}
	if _, err := runtime.Compile(directProto); err != nil {
		t.Fatalf("compile direct proto: %v", err)
	}
	metaProto := &bytecode.Proto{
		Source:       "@jit-direct-poly-tail-meta.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	metaClosure, err := engine.NewClosure(metaProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new tm_call metamethod closure: %v", err)
	}
	if _, err := runtime.Compile(metaProto); err != nil {
		t.Fatalf("compile meta proto: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, metaClosure.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	tailProto := &bytecode.Proto{
		Source:       "@jit-direct-poly-tail-top.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(tailProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new top closure: %v", err)
	}
	results, err := runtime.Call(thread, closure.Value, []value.TValue{directClosure.Value, value.NumberValue(42)}, -1)
	if err != nil {
		t.Fatalf("direct tail runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 0 {
		t.Fatalf("direct covered tailcall should avoid shared tail stub, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable.Value, value.NumberValue(42)}, -1)
	if err != nil {
		t.Fatalf("first tm_call tail runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("first callable-object tailcall should use one shared tail stub, got %d", runtime.SlowStubCount(stubs.StubTailCall))
	}
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StatePolymorphic {
		t.Fatalf("mixed direct/tm_call tail feedback cell = %+v, want polymorphic", cell)
	}
	matched, ok, err := engine.MatchCallFeedbackCell(directClosure.Value, cell)
	if err != nil || !ok || matched.Bits() != directClosure.Value.Bits() {
		t.Fatalf("polymorphic tail feedback should still match direct closure = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, directClosure.Value)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable.Value, cell)
	if err != nil || !ok || matched.Bits() != metaClosure.Value.Bits() {
		t.Fatalf("polymorphic tail feedback should match callable object = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, metaClosure.Value)
	}
	results, err = runtime.Call(thread, closure.Value, []value.TValue{directClosure.Value, value.NumberValue(42)}, -1)
	if err != nil {
		t.Fatalf("second direct tail runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	results, err = runtime.Call(thread, closure.Value, []value.TValue{callable.Value, value.NumberValue(42)}, -1)
	if err != nil {
		t.Fatalf("second tm_call tail runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubTailCall) != 1 {
		t.Fatalf("polymorphic warmup should keep later direct/tm_call tailcalls on covered path, got %d shared tail stubs", runtime.SlowStubCount(stubs.StubTailCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("mixed direct/tm_call tail site should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallLuaClosureTForLoopBecomesCoveredWithinLoop(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	metaProto := &bytecode.Proto{
		Source:       "@jit-tmcall-covered-tfor-meta.lua",
		NumParams:    3,
		MaxStackSize: 5,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(10),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_LT, 1, 2, 1),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 2),
			bytecode.CreateABC(bytecode.OP_LOADNIL, 3, 4, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 3, 3, 0),
			bytecode.CreateABC(bytecode.OP_ADD, 3, 2, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_ADD, 4, 3, bytecode.RKAsk(1)),
			bytecode.CreateABC(bytecode.OP_RETURN, 3, 3, 0),
		},
	}
	metaClosure, err := engine.NewClosure(metaProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new tm_call iterator metamethod closure: %v", err)
	}
	if _, err := runtime.Compile(metaProto); err != nil {
		t.Fatalf("compile tm_call iterator metamethod proto: %v", err)
	}
	iterator, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new iterator table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new iterator metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, metaClosure.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(iterator.Value, metatable.Value); err != nil {
		t.Fatalf("set iterator metatable: %v", err)
	}
	topProto := &bytecode.Proto{
		Source:       "@jit-tmcall-covered-tfor-top.lua",
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
	cell := mustFeedbackCell(t, runtime, closure.Ref, 0)
	if cell.State != feedback.StateMonomorphic || cell.AccessKind != feedback.AccessCallResolvedLuaClosure || cell.ValueBits != iterator.Value.Bits() {
		t.Fatalf("tm_call covered-tfor feedback cell = %+v, want resolved-lua monomorphic on iterator receiver", cell)
	}
	if runtime.SlowStubCount(stubs.StubLuaCall) != 1 {
		t.Fatalf("tm_call lua closure tforloop should use one shared call stub for the first iterator step only, got %d", runtime.SlowStubCount(stubs.StubLuaCall))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("tm_call covered-tforloop should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeTMCallFeedbackMatchesInterpreter(t *testing.T) {
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
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callMeta, err := engine.RegisterHostFunction("call-meta", func(_ value.TValue, x float64, y float64) float64 {
		return x + y
	}, env.Value)
	if err != nil {
		t.Fatalf("register __call host function: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, callMeta.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@feedback-tmcall-shared.lua",
		NumParams:    1,
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(10),
			bytecode.NumberConstant(32),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 1, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 3, 1),
			bytecode.CreateABC(bytecode.OP_CALL, 1, 3, 2),
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
	if _, err := engine.Call(interpThread, interpClosure.Value, []value.TValue{callable.Value}, -1); err != nil {
		t.Fatalf("interpreter warm call: %v", err)
	}
	if _, err := runtime.Call(compiledThread, compiledClosure.Value, []value.TValue{callable.Value}, -1); err != nil {
		t.Fatalf("compiled warm call: %v", err)
	}
	assertFeedbackCellEqual(t, mustFeedbackCell(t, runtime, interpClosure.Ref, 0), mustFeedbackCell(t, runtime, compiledClosure.Ref, 0))
}

func TestBaselineRuntimeFailedCallFeedbackMatchesInterpreter(t *testing.T) {
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
	calleeProto := &bytecode.Proto{
		Source:       "@failed-call-feedback-callee.lua",
		MaxStackSize: 1,
		Constants:    []bytecode.Constant{bytecode.NumberConstant(42)},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	callee, err := engine.NewClosure(calleeProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new callee closure: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@failed-call-feedback-shared.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
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
	if _, err := engine.Call(interpThread, interpClosure.Value, []value.TValue{callee.Value}, -1); err != nil {
		t.Fatalf("interpreter warm call: %v", err)
	}
	if _, err := runtime.Call(compiledThread, compiledClosure.Value, []value.TValue{callee.Value}, -1); err != nil {
		t.Fatalf("compiled warm call: %v", err)
	}
	warmInterp := mustFeedbackCell(t, runtime, interpClosure.Ref, 0)
	warmCompiled := mustFeedbackCell(t, runtime, compiledClosure.Ref, 0)
	assertFeedbackCellEqual(t, warmInterp, warmCompiled)
	if _, err := engine.Call(interpThread, interpClosure.Value, []value.TValue{value.NumberValue(7)}, -1); err == nil {
		t.Fatalf("interpreter invalid call should fail")
	}
	if _, err := runtime.Call(compiledThread, compiledClosure.Value, []value.TValue{value.NumberValue(7)}, -1); err == nil {
		t.Fatalf("compiled invalid call should fail")
	}
	assertFeedbackCellEqual(t, warmInterp, mustFeedbackCell(t, runtime, interpClosure.Ref, 0))
	assertFeedbackCellEqual(t, warmCompiled, mustFeedbackCell(t, runtime, compiledClosure.Ref, 0))
	assertFeedbackCellEqual(t, mustFeedbackCell(t, runtime, interpClosure.Ref, 0), mustFeedbackCell(t, runtime, compiledClosure.Ref, 0))
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

func TestBaselineRuntimeHostNumericMapKeysReuseSharedStubs(t *testing.T) {
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
	bag := map[float64]float64{1: 5}
	bagObject, err := engine.RegisterHostObject("bag", bag, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "bag", bagObject.Value); err != nil {
		t.Fatalf("set global bag: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-host-numeric-keys.lua",
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("bag"),
			bytecode.NumberConstant(2),
			bytecode.NumberConstant(42),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 3, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 3, 2, 0),
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
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if bag[2] != 42 {
		t.Fatalf("host numeric map key should update Go target, got %v", bag)
	}
	if runtime.SlowStubCount(stubs.StubSetTable) != 1 {
		t.Fatalf("host numeric map set should use one shared set-table stub, got %d", runtime.SlowStubCount(stubs.StubSetTable))
	}
	if runtime.SlowStubCount(stubs.StubGetTable) != 1 {
		t.Fatalf("host numeric map get should use one shared get-table stub, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("host numeric map bridge should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeHostDescriptorErrorsCollapseToUserdata(t *testing.T) {
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
	bagObject, err := engine.RegisterHostObject("bag", map[string]float64{"x": 1}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "bag", bagObject.Value); err != nil {
		t.Fatalf("set global bag: %v", err)
	}
	_, _, descriptor, err := engine.Hosts.ReadHostObject(bagObject.Ref)
	if err != nil {
		t.Fatalf("read host object: %v", err)
	}
	descriptor.Get = func(target any, key any) (any, bool, error) {
		return nil, false, fmt.Errorf("boom")
	}
	proto := &bytecode.Proto{
		Source:       "@jit-host-descriptor-error.lua",
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
	_, err = runtime.Call(thread, closure.Value, nil, -1)
	if err == nil || err.Error() != "attempt to index a userdata value" {
		t.Fatalf("runtime error = %v, want userdata index error", err)
	}
	if runtime.SlowStubCount(stubs.StubGetTable) != 1 {
		t.Fatalf("host descriptor error should use one shared get-table stub, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("host descriptor error should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeHostStructLuaFieldTagsReuseSharedStubs(t *testing.T) {
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
	target := &hostTaggedScore{Value: 5}
	bagObject, err := engine.RegisterHostObject("counter", target, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "counter", bagObject.Value); err != nil {
		t.Fatalf("set global counter: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-host-tagged-struct.lua",
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("counter"),
			bytecode.StringConstant("score"),
			bytecode.NumberConstant(42),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 3, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 3, 2, 0),
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
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if target.Value != 42 {
		t.Fatalf("host tagged field should update Go target, got %v", target.Value)
	}
	if runtime.SlowStubCount(stubs.StubSetTable) != 1 {
		t.Fatalf("host tagged field set should use one shared set-table stub, got %d", runtime.SlowStubCount(stubs.StubSetTable))
	}
	if runtime.SlowStubCount(stubs.StubGetTable) != 1 {
		t.Fatalf("host tagged field get should use one shared get-table stub, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("host tagged field bridge should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeHostStructLuaMethodTagsReuseSharedStubs(t *testing.T) {
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
	target := &hostTaggedMethodScore{Value: 21}
	bagObject, err := engine.RegisterHostObject("counter", target, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "counter", bagObject.Value); err != nil {
		t.Fatalf("set global counter: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@jit-host-tagged-method.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("counter"),
			bytecode.StringConstant("double-score"),
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
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	if runtime.SlowStubCount(stubs.StubGetTable) != 1 {
		t.Fatalf("host tagged method get should use one shared get-table stub, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("host tagged method bridge should avoid deopt, got %d", runtime.DeoptCount())
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
		t.Fatalf("expected slow-path GETTABLE to surface an error")
	}
	if calls != 1 {
		t.Fatalf("earlier host side effect was replayed %d times, want 1", calls)
	}
	if runtime.SlowStubCount(stubs.StubGetTable) != 1 {
		t.Fatalf("unsupported GETTABLE should use shared slow stub once, got %d", runtime.SlowStubCount(stubs.StubGetTable))
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("unsupported GETTABLE should avoid deopt after runtime dispatch, got %d", runtime.DeoptCount())
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

func isResolvedCallFeedbackCell(cell feedback.Cell, accessKind feedback.AccessKind, receiver value.TValue) bool {
	return cell.State == feedback.StateMonomorphic && cell.AccessKind == accessKind && cell.ValueBits == receiver.Bits()
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

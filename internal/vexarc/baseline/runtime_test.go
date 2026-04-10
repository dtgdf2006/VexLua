package baseline

import (
	"testing"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/abi"
	"vexlua/internal/vexarc/amd64"
	"vexlua/internal/vexarc/codecache"
)

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

func TestBaselineRuntimeCachesCompiledProtoByRefWithoutHotPathResolve(t *testing.T) {
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
	before := engine.Protos.ResolveCount()
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("first runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	afterFirst := engine.Protos.ResolveCount()
	if afterFirst != before+1 {
		t.Fatalf("first compiled call should resolve proto exactly once: before=%d after=%d", before, afterFirst)
	}
	results, err = runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("second runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
	afterSecond := engine.Protos.ResolveCount()
	if afterSecond != afterFirst {
		t.Fatalf("hot compiled call should not re-resolve proto: first=%d second=%d", afterFirst, afterSecond)
	}
}

func TestBaselineRuntimeCompiledToCompiledCall(t *testing.T) {
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
	if runtime.CallSuspendCount() != 0 {
		t.Fatalf("compiled-to-compiled fast path should avoid SuspendCall, got %d slow-path hits", runtime.CallSuspendCount())
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("compiled-to-compiled fast path should avoid deopt, got %d", runtime.DeoptCount())
	}
}

func TestBaselineRuntimeCompiledIslandChain(t *testing.T) {
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
	assertNoNativeFallback(t, runtime)
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
	assertNoNativeFallback(t, runtime)
}

func TestBaselineRuntimeFallsBackToInterpreter(t *testing.T) {
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
	if compiled.Supported {
		t.Fatalf("ADD should still fall back in Stage 6")
	}
	results, err := runtime.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("runtime call: %v", err)
	}
	assertValuesEqual(t, results, []value.TValue{value.NumberValue(42)})
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

func assertNoNativeFallback(t *testing.T, runtime *Runtime) {
	t.Helper()
	if runtime.CallSuspendCount() != 0 {
		t.Fatalf("compiled path should avoid SuspendCall, got %d", runtime.CallSuspendCount())
	}
	if runtime.ForPrepSuspendCount() != 0 {
		t.Fatalf("compiled path should avoid SuspendForPrep, got %d", runtime.ForPrepSuspendCount())
	}
	if runtime.ForLoopSuspendCount() != 0 {
		t.Fatalf("compiled path should avoid SuspendForLoop, got %d", runtime.ForLoopSuspendCount())
	}
	if runtime.DeoptCount() != 0 {
		t.Fatalf("compiled path should avoid deopt, got %d", runtime.DeoptCount())
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

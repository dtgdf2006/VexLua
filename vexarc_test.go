package vexlua

import (
	"testing"

	"vexlua/internal/benchmarks"
	"vexlua/internal/bytecode"
	rt "vexlua/internal/runtime"
)

func TestVexarcEntryExitFrameworkFallsBackForSyntheticProto(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	proto := engine.BuildSumLoop(100)
	result := runProtoRepeated(t, engine, proto, 2)
	if got := result.Number(); got != 5050 {
		t.Fatalf("synthetic proto result = %v, want 5050", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledEnters == 0 {
		t.Fatalf("expected synthetic proto to enter compiled scaffold, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected synthetic proto to return from compiled code, got %+v", stats)
	}
}

func TestVexarcEntryExitFrameworkFallsBackForScriptedProto(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	proto, err := engine.CompileStringNamed(`
local sum = 0
for i = 1, 100 do
	sum = sum + i
end
return sum
`, "@vexarc_entry_exit.lua")
	if err != nil {
		t.Fatal(err)
	}
	result := runProtoRepeated(t, engine, proto, 2)
	if got := result.Number(); got != 5050 {
		t.Fatalf("scripted proto result = %v, want 5050", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledEnters == 0 {
		t.Fatalf("expected scripted proto to enter compiled scaffold, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected scripted proto to return from compiled code, got %+v", stats)
	}
}

func TestVexarcDirectArrayFastPathForQuickenedTableProto(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	tableValue := engine.runtime.NewTableValue(4)
	handle, _ := tableValue.Handle()
	table := engine.runtime.Heap().Table(handle)
	table.SetIndex(1, rt.NumberValue(40))

	proto := bytecode.NewProto("table_helper_reentry", 4, 0)
	tableConst := proto.AddConstant(tableValue)
	indexConst := proto.AddConstant(rt.NumberValue(1))
	bonusConst := proto.AddConstant(rt.NumberValue(2))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(tableConst))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(indexConst))
	proto.Emit(bytecode.OpGetTableArray, 2, 0, 1, 0)
	proto.Emit(bytecode.OpAddConst, 3, 2, 0, int32(bonusConst))
	proto.Emit(bytecode.OpReturn, 3, 0, 0, 0)

	result, err := engine.Run(proto)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("compiled helper proto result = %v, want 42", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledEnters == 0 {
		t.Fatalf("expected compiled entry for helper proto, got %+v", stats)
	}
	if stats.CompiledHelperCalls != 0 {
		t.Fatalf("expected direct array fast path without helper calls, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected compiled return for helper proto, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for helper proto, got %+v", stats)
	}
}

func TestVexarcCompiledMethodDispatchStaysInCompiledPath(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	proto := compileNamedForTest(t, engine, "@method_dispatch.lua", `
local box = {base = 32}
function box:mix(a, b, c)
	return self.base + a + b + c
end
local sum = 0
for i = 1, 200 do
	sum = sum + box:mix(2, 3, 4)
end
return sum
`)
	result := runProtoRepeated(t, engine, proto, 2)
	if got := result.Number(); got != 8200 {
		t.Fatalf("method dispatch result = %v, want 8200", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledEnters == 0 {
		t.Fatalf("expected compiled entry for method dispatch proto, got %+v", stats)
	}
	if stats.CompiledDirectCalls == 0 {
		t.Fatalf("expected method dispatch proto to hit direct compiled child calls, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected compiled return for method dispatch proto, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for method dispatch proto, got %+v", stats)
	}
}

func TestVexarcCompiledHelperReentryForCallProto(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	fnValue := engine.runtime.NewHostFunction("add1", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		_ = runtime
		return rt.NumberValue(args[0].Number() + 1), nil
	})

	proto := bytecode.NewProto("call_helper_reentry", 4, 0)
	fnConst := proto.AddConstant(fnValue)
	argConst := proto.AddConstant(rt.NumberValue(40))
	bonusConst := proto.AddConstant(rt.NumberValue(1))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(fnConst))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(argConst))
	proto.Emit(bytecode.OpCall, 2, 0, 1, 1)
	proto.Emit(bytecode.OpAddConst, 3, 2, 0, int32(bonusConst))
	proto.Emit(bytecode.OpReturn, 3, 0, 0, 0)

	result, err := engine.Run(proto)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("compiled call helper proto result = %v, want 42", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledEnters == 0 {
		t.Fatalf("expected compiled entry for call helper proto, got %+v", stats)
	}
	if stats.CompiledHelperCalls == 0 {
		t.Fatalf("expected compiled helper calls for call helper proto, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected compiled return for call helper proto, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for call helper proto, got %+v", stats)
	}
}

func TestVexarcCompiledHelperReentryForLoadGlobalProto(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	if err := engine.RegisterFunc("double", func(v float64) float64 { return v * 2 }); err != nil {
		t.Fatal(err)
	}
	proto := engine.BuildFunctionDemo("double", 21)
	result, err := engine.Run(proto)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("compiled load-global proto result = %v, want 42", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledHelperCalls == 0 {
		t.Fatalf("expected compiled helper calls for load-global proto, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for load-global proto, got %+v", stats)
	}
}

func TestVexarcCompiledUpvalueFastPathForOpenClosureProto(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	child := bytecode.NewProto("inner", 1, 0)
	child.Scripted = true
	child.Upvalues = append(child.Upvalues, bytecode.UpvalueDesc{Name: "x", InParentLocal: true, Index: 0})
	child.Emit(bytecode.OpLoadUpvalue, 0, 0, 0, 0)
	child.Emit(bytecode.OpReturn, 0, 0, 0, 0)

	root := bytecode.NewProto("closure_upvalue_root", 4, 0)
	root.Scripted = true
	constX := root.AddConstant(rt.NumberValue(41))
	constOne := root.AddConstant(rt.NumberValue(1))
	childIndex := root.AddChild(child)
	root.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(constX))
	root.Emit(bytecode.OpClosure, 1, 0, 0, int32(childIndex))
	root.Emit(bytecode.OpCall, 2, 1, 0, 0)
	root.Emit(bytecode.OpAddConst, 3, 2, 0, int32(constOne))
	root.Emit(bytecode.OpReturn, 3, 0, 0, 0)

	result, err := engine.Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("compiled closure helper proto result = %v, want 42", got)
	}
	stats := engine.Stats(root)
	if stats.CompiledHelperCalls == 0 {
		t.Fatalf("expected compiled helper calls for closure proto, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for closure proto, got %+v", stats)
	}
	childStats := engine.Stats(child)
	if childStats.CompiledEnters == 0 {
		t.Fatalf("expected compiled entry for child closure proto, got %+v", childStats)
	}
	if childStats.CompiledHelperCalls != 0 {
		t.Fatalf("expected open upvalue child to stay on native fast path, got %+v", childStats)
	}
	if childStats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for child closure proto, got %+v", childStats)
	}
	if childStats.CompiledReturns == 0 {
		t.Fatalf("expected compiled return for child closure proto, got %+v", childStats)
	}
}

func TestVexarcCompiledOpenUpvalueCellSeesLatestParentLocal(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	child := bytecode.NewProto("inner_latest_open", 1, 0)
	child.Scripted = true
	child.Upvalues = append(child.Upvalues, bytecode.UpvalueDesc{Name: "x", InParentLocal: true, Index: 0})
	child.Emit(bytecode.OpLoadUpvalue, 0, 0, 0, 0)
	child.Emit(bytecode.OpReturn, 0, 0, 0, 0)

	root := bytecode.NewProto("open_upvalue_latest_root", 4, 0)
	root.Scripted = true
	seedConst := root.AddConstant(rt.NumberValue(1))
	latestConst := root.AddConstant(rt.NumberValue(41))
	childIndex := root.AddChild(child)
	root.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(seedConst))
	root.Emit(bytecode.OpClosure, 1, 0, 0, int32(childIndex))
	root.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(latestConst))
	root.Emit(bytecode.OpCall, 2, 1, 0, 0)
	root.Emit(bytecode.OpReturn, 2, 0, 0, 0)

	result := runProtoRepeated(t, engine, root, 3)
	if got := result.Number(); got != 41 {
		t.Fatalf("latest open upvalue result = %v, want 41", got)
	}
	rootStats := engine.Stats(root)
	if rootStats.CompiledFallbacks != 0 {
		t.Fatalf("expected root to stay in compiled path, got %+v", rootStats)
	}
	childStats := engine.Stats(child)
	if childStats.CompiledEnters == 0 {
		t.Fatalf("expected compiled entry for latest-open child, got %+v", childStats)
	}
	if childStats.CompiledHelperCalls != 0 {
		t.Fatalf("expected latest-open upvalue child to stay on native fast path, got %+v", childStats)
	}
	if childStats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for latest-open child, got %+v", childStats)
	}
}

func TestVexarcCompiledUpvalueMutationFastPathForClosedClosureProto(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	inner := bytecode.NewProto("inner_mutation", 1, 0)
	inner.Scripted = true
	inner.Upvalues = append(inner.Upvalues, bytecode.UpvalueDesc{Name: "x", InParentLocal: true, Index: 0})
	constOne := inner.AddConstant(rt.NumberValue(1))
	inner.Emit(bytecode.OpLoadUpvalue, 0, 0, 0, 0)
	inner.Emit(bytecode.OpAddConst, 0, 0, 0, int32(constOne))
	inner.Emit(bytecode.OpStoreUpvalue, 0, 0, 0, 0)
	inner.Emit(bytecode.OpReturn, 0, 0, 0, 0)

	makeProto := bytecode.NewProto("make_mutation", 2, 0)
	makeProto.Scripted = true
	constSeed := makeProto.AddConstant(rt.NumberValue(40))
	innerIndex := makeProto.AddChild(inner)
	makeProto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(constSeed))
	makeProto.Emit(bytecode.OpClosure, 1, 0, 0, int32(innerIndex))
	makeProto.Emit(bytecode.OpReturn, 1, 0, 0, 0)

	root := bytecode.NewProto("closure_upvalue_mutation_root", 5, 0)
	root.Scripted = true
	makeIndex := root.AddChild(makeProto)
	root.Emit(bytecode.OpClosure, 0, 0, 0, int32(makeIndex))
	root.Emit(bytecode.OpCall, 1, 0, 0, 0)
	root.Emit(bytecode.OpCall, 2, 1, 0, 0)
	root.Emit(bytecode.OpCall, 3, 1, 0, 0)
	root.Emit(bytecode.OpAddNum, 4, 2, 3, 0)
	root.Emit(bytecode.OpReturn, 4, 0, 0, 0)

	result := runProtoRepeated(t, engine, root, 3)
	if got := result.Number(); got != 83 {
		t.Fatalf("compiled closed upvalue mutation result = %v, want 83", got)
	}
	rootStats := engine.Stats(root)
	if rootStats.CompiledFallbacks != 0 {
		t.Fatalf("expected root to stay in compiled path, got %+v", rootStats)
	}
	innerStats := engine.Stats(inner)
	if innerStats.CompiledEnters == 0 {
		t.Fatalf("expected compiled entry for closed upvalue child, got %+v", innerStats)
	}
	if innerStats.CompiledHelperCalls != 0 {
		t.Fatalf("expected closed upvalue load/store to stay on native fast path, got %+v", innerStats)
	}
	if innerStats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for closed upvalue child, got %+v", innerStats)
	}
	if innerStats.CompiledReturns == 0 {
		t.Fatalf("expected compiled return for closed upvalue child, got %+v", innerStats)
	}
}

func TestVexarcCompiledCompareBranchPrefixKeepsWholeProtoAndDirectCall(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	child := bytecode.NewProto("compare_branch_child", 4, 0)
	child.Scripted = true
	child.NumParams = 1
	zeroConst := child.AddConstant(rt.NumberValue(0))
	fortyOneConst := child.AddConstant(rt.NumberValue(41))
	fortyConst := child.AddConstant(rt.NumberValue(40))
	oneConst := child.AddConstant(rt.NumberValue(1))
	child.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(zeroConst))
	child.Emit(bytecode.OpEqual, 2, 0, 1, 0)
	child.Emit(bytecode.OpJumpIfFalse, 2, 0, 0, 5)
	child.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(fortyOneConst))
	child.Emit(bytecode.OpReturn, 0, 0, 0, 0)
	child.Emit(bytecode.OpLess, 2, 0, 1, 0)
	child.Emit(bytecode.OpNot, 2, 2, 0, 0)
	child.Emit(bytecode.OpJumpIfFalse, 2, 0, 0, 10)
	child.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(fortyConst))
	child.Emit(bytecode.OpReturn, 0, 0, 0, 0)
	child.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(oneConst))
	child.Emit(bytecode.OpReturn, 0, 0, 0, 0)
	childValue := engine.machine.NewClosureValue(child)

	root := bytecode.NewProto("compare_branch_root", 7, 0)
	root.Scripted = true
	childConst := root.AddConstant(childValue)
	zeroArgConst := root.AddConstant(rt.NumberValue(0))
	oneArgConst := root.AddConstant(rt.NumberValue(1))
	root.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(childConst))
	root.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(zeroArgConst))
	root.Emit(bytecode.OpCall, 2, 0, 1, 1)
	root.Emit(bytecode.OpLoadConst, 3, 0, 0, int32(oneArgConst))
	root.Emit(bytecode.OpCall, 4, 0, 3, 1)
	root.Emit(bytecode.OpAddNum, 5, 2, 4, 0)
	root.Emit(bytecode.OpReturn, 5, 0, 0, 0)

	result := runProtoRepeated(t, engine, root, 3)
	if got := result.Number(); got != 81 {
		t.Fatalf("compare/branch direct result = %v, want 81", got)
	}
	rootStats := engine.Stats(root)
	if rootStats.CompiledHelperCalls >= rootStats.Runs*2 {
		t.Fatalf("expected compare/branch child to bypass some call helpers after warmup, got %+v", rootStats)
	}
	if rootStats.CompiledFallbacks != 0 {
		t.Fatalf("expected root to stay in compiled path, got %+v", rootStats)
	}
	childStats := engine.Stats(child)
	if childStats.CompiledHelperCalls != 0 {
		t.Fatalf("expected compare/branch child numeric compare/not ops to stay on native fast path, got %+v", childStats)
	}
	if childStats.CompiledFallbacks != 0 {
		t.Fatalf("expected compare/branch child whole-proto compile without fallback, got %+v", childStats)
	}
	if childStats.CompiledReturns == 0 {
		t.Fatalf("expected compare/branch child to return from compiled code, got %+v", childStats)
	}
}

func TestVexarcCompiledArithmeticOpsStayInWholeProtoPath(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	proto := bytecode.NewProto("arithmetic_helper_mainline", 8, 0)
	proto.Scripted = true
	tenConst := proto.AddConstant(rt.NumberValue(10))
	fourConst := proto.AddConstant(rt.NumberValue(4))
	sevenConst := proto.AddConstant(rt.NumberValue(7))
	oneConst := proto.AddConstant(rt.NumberValue(1))
	hundredConst := proto.AddConstant(rt.NumberValue(100))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(tenConst))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(fourConst))
	proto.Emit(bytecode.OpSub, 2, 0, 1, 0)
	proto.Emit(bytecode.OpLoadConst, 3, 0, 0, int32(sevenConst))
	proto.Emit(bytecode.OpMul, 4, 2, 3, 0)
	proto.Emit(bytecode.OpLoadConst, 5, 0, 0, int32(oneConst))
	proto.Emit(bytecode.OpDiv, 6, 4, 5, 0)
	proto.Emit(bytecode.OpLoadConst, 7, 0, 0, int32(hundredConst))
	proto.Emit(bytecode.OpMod, 6, 6, 7, 0)
	proto.Emit(bytecode.OpPow, 6, 6, 5, 0)
	proto.Emit(bytecode.OpReturn, 6, 0, 0, 0)

	result, err := engine.Run(proto)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("arithmetic whole-proto result = %v, want 42", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledHelperCalls == 0 {
		t.Fatalf("expected mod/pow to stay in compiled mainline via helpers, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected arithmetic proto whole-proto path without fallback, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected arithmetic proto to return from compiled code, got %+v", stats)
	}
}

func TestVexarcCompiledIntegerModStaysOnNativeFastPath(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	proto := bytecode.NewProto("integer_mod_fast_path", 3, 0)
	leftConst := proto.AddConstant(rt.NumberValue(-5))
	rightConst := proto.AddConstant(rt.NumberValue(3))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(leftConst))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(rightConst))
	proto.Emit(bytecode.OpMod, 2, 0, 1, 0)
	proto.Emit(bytecode.OpReturn, 2, 0, 0, 0)

	result := runProtoRepeated(t, engine, proto, 3)
	if got := result.Number(); got != 1 {
		t.Fatalf("integer mod result = %v, want 1", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledEnters == 0 {
		t.Fatalf("expected compiled entry for integer mod proto, got %+v", stats)
	}
	if stats.CompiledHelperCalls != 0 {
		t.Fatalf("expected integer mod proto to stay on native fast path, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected integer mod proto without interpreter fallback, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected integer mod proto to return from compiled code, got %+v", stats)
	}
}

func TestVexarcCompiledSubChildKeepsWholeProtoAndDirectCall(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	child := bytecode.NewProto("sub_child", 3, 0)
	child.Scripted = true
	child.NumParams = 1
	oneConst := child.AddConstant(rt.NumberValue(1))
	child.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(oneConst))
	child.Emit(bytecode.OpSub, 2, 0, 1, 0)
	child.Emit(bytecode.OpReturn, 2, 0, 0, 0)
	childValue := engine.machine.NewClosureValue(child)

	root := bytecode.NewProto("sub_root", 7, 0)
	root.Scripted = true
	childConst := root.AddConstant(childValue)
	fortyThreeConst := root.AddConstant(rt.NumberValue(43))
	fortyFourConst := root.AddConstant(rt.NumberValue(44))
	root.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(childConst))
	root.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(fortyThreeConst))
	root.Emit(bytecode.OpCall, 2, 0, 1, 1)
	root.Emit(bytecode.OpLoadConst, 3, 0, 0, int32(fortyFourConst))
	root.Emit(bytecode.OpCall, 4, 0, 3, 1)
	root.Emit(bytecode.OpAddNum, 5, 2, 4, 0)
	root.Emit(bytecode.OpReturn, 5, 0, 0, 0)

	result := runProtoRepeated(t, engine, root, 3)
	if got := result.Number(); got != 85 {
		t.Fatalf("sub direct-call result = %v, want 85", got)
	}
	rootStats := engine.Stats(root)
	if rootStats.CompiledDirectCalls == 0 {
		t.Fatalf("expected sub child to become direct-enter eligible after warmup, got %+v", rootStats)
	}
	if rootStats.CompiledFallbacks != 0 {
		t.Fatalf("expected root to stay in compiled path, got %+v", rootStats)
	}
	childStats := engine.Stats(child)
	if childStats.CompiledHelperCalls != 0 {
		t.Fatalf("expected sub child numeric fast path without helper calls, got %+v", childStats)
	}
	if childStats.CompiledFallbacks != 0 {
		t.Fatalf("expected sub child whole-proto compile without fallback, got %+v", childStats)
	}
	if childStats.CompiledReturns == 0 {
		t.Fatalf("expected sub child to return from compiled code, got %+v", childStats)
	}
}

func TestVexarcCompiledDirectCallCanEnterRegionStartingAtPC0(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	child := bytecode.NewProto("region_direct_child", 2, 0)
	child.Scripted = true
	stringConst := child.AddConstant(engine.runtime.StringValue("abcd"))
	child.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(stringConst))
	child.Emit(bytecode.OpLen, 1, 0, 0, 0)
	child.Emit(bytecode.OpReturn, 1, 0, 0, 0)
	childValue := engine.machine.NewClosureValue(child)

	root := bytecode.NewProto("region_direct_root", 4, 0)
	root.Scripted = true
	childConst := root.AddConstant(childValue)
	bonusConst := root.AddConstant(rt.NumberValue(1))
	root.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(childConst))
	root.Emit(bytecode.OpCall, 1, 0, 1, 0)
	root.Emit(bytecode.OpAddConst, 2, 1, 0, int32(bonusConst))
	root.Emit(bytecode.OpReturn, 2, 0, 0, 0)

	result := runProtoRepeated(t, engine, root, 3)
	if got := result.Number(); got != 5 {
		t.Fatalf("region direct-call result = %v, want 5", got)
	}
	rootStats := engine.Stats(root)
	if rootStats.CompiledHelperCalls >= rootStats.Runs {
		t.Fatalf("expected one call to bypass helper via direct region entry, got %+v", rootStats)
	}
	childStats := engine.Stats(child)
	if childStats.CompiledEnters == 0 {
		t.Fatalf("expected region-start child proto to enter compiled code, got %+v", childStats)
	}
	if childStats.CompiledHelperCalls == 0 {
		t.Fatalf("expected region-start child proto to stay in compiled mainline via unquickened len helper, got %+v", childStats)
	}
	if childStats.CompiledFallbacks != 0 {
		t.Fatalf("expected region-start child proto to avoid interpreter fallback after helperization, got %+v", childStats)
	}
}

func TestVexarcCompiledHelperReentryForUnquickenedTableOps(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	proto := bytecode.NewProto("table_ops_helper_reentry", 6, 0)
	proto.Scripted = true
	keyConst := proto.AddConstant(rt.NumberValue(1))
	valueConst := proto.AddConstant(rt.NumberValue(41))
	oneConst := proto.AddConstant(rt.NumberValue(1))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(keyConst))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(valueConst))
	proto.Emit(bytecode.OpNewTable, 2, 0, 0, 1)
	proto.Emit(bytecode.OpSetTable, 2, 0, 1, 0)
	proto.Emit(bytecode.OpGetTable, 3, 2, 0, 0)
	proto.Emit(bytecode.OpLoadConst, 4, 0, 0, int32(oneConst))
	proto.Emit(bytecode.OpAddNum, 5, 3, 4, 0)
	proto.Emit(bytecode.OpReturn, 5, 0, 0, 0)

	result, err := engine.Run(proto)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("unquickened table ops result = %v, want 42", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledHelperCalls == 0 {
		t.Fatalf("expected unquickened table ops to stay in compiled mainline via helpers, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected unquickened table ops whole-proto path without fallback, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected unquickened table ops proto to return from compiled code, got %+v", stats)
	}
}

func TestVexarcCompiledUnquickenedFieldLenChildStaysWholeProto(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	tableValue := engine.runtime.NewTableValue(4)
	baseSymbol := engine.runtime.InternSymbol("base")
	if err := engine.runtime.SetField(tableValue, baseSymbol, rt.NumberValue(40)); err != nil {
		t.Fatal(err)
	}
	h, _ := tableValue.Handle()
	table := engine.runtime.Heap().Table(h)
	table.SetIndex(1, rt.NumberValue(10))
	table.SetIndex(2, rt.NumberValue(20))

	child := bytecode.NewProto("field_len_child", 4, 1)
	child.Scripted = true
	child.NumParams = 1
	child.Emit(bytecode.OpGetField, 1, 0, 0, int32(baseSymbol))
	child.Emit(bytecode.OpLen, 2, 0, 0, 0)
	child.Emit(bytecode.OpAddNum, 3, 1, 2, 0)
	child.Emit(bytecode.OpReturn, 3, 0, 0, 0)
	childValue := engine.machine.NewClosureValue(child)

	root := bytecode.NewProto("field_len_root", 3, 0)
	root.Scripted = true
	childConst := root.AddConstant(childValue)
	tableConst := root.AddConstant(tableValue)
	root.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(childConst))
	root.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(tableConst))
	root.Emit(bytecode.OpCall, 2, 0, 1, 1)
	root.Emit(bytecode.OpReturn, 2, 0, 0, 0)

	result := runProtoRepeated(t, engine, root, 3)
	if got := result.Number(); got != 42 {
		t.Fatalf("unquickened field/len child result = %v, want 42", got)
	}
	rootStats := engine.Stats(root)
	if rootStats.CompiledHelperCalls >= rootStats.Runs {
		t.Fatalf("expected field/len child to become direct-enter eligible after warmup, got %+v", rootStats)
	}
	if rootStats.CompiledFallbacks != 0 {
		t.Fatalf("expected root to stay in compiled path, got %+v", rootStats)
	}
	childStats := engine.Stats(child)
	if childStats.CompiledHelperCalls == 0 {
		t.Fatalf("expected field/len child to use helper-backed unquickened ops, got %+v", childStats)
	}
	if childStats.CompiledFallbacks != 0 {
		t.Fatalf("expected field/len child whole-proto compile without fallback, got %+v", childStats)
	}
}

func TestVexarcCompiledHelperReentryForUnquickenedSelf(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	tableValue := engine.runtime.NewTableValue(1)
	touchValue := engine.runtime.NewHostFunction("touch", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NumberValue(-100), nil
		}
		if args[0] != tableValue {
			return rt.NumberValue(-1), nil
		}
		return rt.NumberValue(42), nil
	})
	touchSymbol := engine.runtime.InternSymbol("touch")
	if err := engine.runtime.SetField(tableValue, touchSymbol, touchValue); err != nil {
		t.Fatal(err)
	}

	proto := bytecode.NewProto("self_helper_reentry", 4, 1)
	proto.Scripted = true
	tableConst := proto.AddConstant(tableValue)
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(tableConst))
	proto.Emit(bytecode.OpSelf, 1, 0, 0, int32(touchSymbol))
	proto.Emit(bytecode.OpCall, 3, 1, 2, 1)
	proto.Emit(bytecode.OpReturn, 3, 0, 0, 0)

	result, err := engine.Run(proto)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("unquickened self result = %v, want 42", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledHelperCalls == 0 {
		t.Fatalf("expected unquickened self to stay in compiled mainline via helpers, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected unquickened self proto without interpreter fallback, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected unquickened self proto to return from compiled code, got %+v", stats)
	}
}

func TestVexarcCompiledTailCallUsesDirectLeafCallee(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})

	leaf := bytecode.NewProto("tail_leaf", 1, 0)
	leaf.Scripted = true
	const42 := leaf.AddConstant(rt.NumberValue(42))
	leaf.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(const42))
	leaf.Emit(bytecode.OpReturn, 0, 0, 0, 0)
	leafValue := engine.machine.NewClosureValue(leaf)

	root := bytecode.NewProto("tail_direct_root", 2, 0)
	root.Scripted = true
	leafConst := root.AddConstant(leafValue)
	root.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(leafConst))
	root.Emit(bytecode.OpTailCall, 0, 0, 1, bytecode.PackCallCounts(0, 0))

	result := runProtoRepeated(t, engine, root, 3)
	if got := result.Number(); got != 42 {
		t.Fatalf("tailcall direct result = %v, want 42", got)
	}
	stats := engine.Stats(root)
	if stats.CompiledDirectCalls == 0 {
		t.Fatalf("expected tailcall proto to hit direct compiled callee, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for tailcall direct proto, got %+v", stats)
	}
}

func TestVexarcCompiledGenericForPairsStaysInHelperMainline(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	proto := compileNamedForTest(t, engine, "@generic_for_pairs.lua", `
local t = { a = 1, b = 2, c = 3, d = 4, e = 5 }
local sum = 0
for i = 1, 3000 do
	for _, v in pairs(t) do
		sum = sum + v
	end
end
return sum
`)

	result := runProtoRepeated(t, engine, proto, 3)
	if got := result.Number(); got != 45000 {
		t.Fatalf("generic-for pairs result = %v, want 45000", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledEnters == 0 {
		t.Fatalf("expected compiled entry for generic-for pairs proto, got %+v", stats)
	}
	if stats.CompiledHelperCalls == 0 {
		t.Fatalf("expected iterator helper reentry for generic-for pairs proto, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected compiled return for generic-for pairs proto, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for generic-for pairs proto, got %+v", stats)
	}
}

func TestVexarcTailcallChainKeepsBidirectionalDirectTailCalls(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	proto := compileNamedForTest(t, engine, "@tailcall_chain.lua", `
local bounce

local function step(n, acc)
	if n == 0 then
		return acc
	end
	return bounce(n - 1, acc + 1)
end

bounce = function(n, acc)
	return step(n, acc)
end

local sum = 0
for i = 1, 50 do
	sum = sum + bounce(20, 0)
end
return sum
`)

	result := runProtoRepeated(t, engine, proto, 3)
	if got := result.Number(); got != 1000 {
		t.Fatalf("tailcall chain result = %v, want 1000", got)
	}
	if len(proto.Children) != 2 {
		t.Fatalf("tailcall chain child count = %d, want 2", len(proto.Children))
	}
	stepStats := engine.Stats(proto.Children[0])
	bounceStats := engine.Stats(proto.Children[1])
	rootStats := engine.Stats(proto)
	if rootStats.CompiledFallbacks != 0 {
		t.Fatalf("expected tailcall chain root to stay out of interpreter fallback, got %+v", rootStats)
	}
	if stepStats.CompiledDirectCalls <= stepStats.Runs/2 {
		t.Fatalf("expected step tailcalls to keep direct-hit majority after warmup, got %+v", stepStats)
	}
	if bounceStats.CompiledDirectCalls <= bounceStats.Runs/2 {
		t.Fatalf("expected bounce tailcalls to keep direct-hit majority after warmup, got %+v", bounceStats)
	}
}

func TestVexarcCompiledVarargAndMultiReturnChainStaysInMainline(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	proto := compileNamedForTest(t, engine, "@vararg_multret_chain.lua", `
local function source(x)
	return x, x + 1, x + 2, x + 3
end

local function relay(...)
	return ...
end

local function pack(a, b, c, d)
	return a + b + c + d
end

local sum = 0
for i = 1, 1000 do
	sum = sum + pack(relay(source(i)))
end
return sum
`)

	result := runProtoRepeated(t, engine, proto, 3)
	if got := result.Number(); got != 2008000 {
		t.Fatalf("vararg multret chain result = %v, want 2008000", got)
	}
	if len(proto.Children) != 3 {
		t.Fatalf("vararg multret child count = %d, want 3", len(proto.Children))
	}
	rootStats := engine.Stats(proto)
	if rootStats.CompiledFallbacks != 0 {
		t.Fatalf("expected root to stay in compiled path, got %+v", rootStats)
	}
	if rootStats.CompiledHelperCalls == 0 {
		t.Fatalf("expected root vararg/multret chain to use helper reentry, got %+v", rootStats)
	}
	sourceStats := engine.Stats(proto.Children[0])
	if sourceStats.CompiledHelperCalls == 0 {
		t.Fatalf("expected source multi-return child to use helper-backed RETURN_MULTI, got %+v", sourceStats)
	}
	if sourceStats.CompiledFallbacks != 0 {
		t.Fatalf("expected source multi-return child without interpreter fallback, got %+v", sourceStats)
	}
	relayStats := engine.Stats(proto.Children[1])
	if relayStats.CompiledHelperCalls == 0 {
		t.Fatalf("expected relay vararg child to use helper-backed vararg protocol, got %+v", relayStats)
	}
	if relayStats.CompiledFallbacks != 0 {
		t.Fatalf("expected relay vararg child without interpreter fallback, got %+v", relayStats)
	}
	packStats := engine.Stats(proto.Children[2])
	if packStats.CompiledReturns == 0 {
		t.Fatalf("expected pack child to return from compiled code, got %+v", packStats)
	}
	if packStats.CompiledFallbacks != 0 {
		t.Fatalf("expected pack child without interpreter fallback, got %+v", packStats)
	}
}

func TestVexarcCompiledCallMultiFixedResultsUsesDirectCallee(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	checkValue := engine.runtime.NewHostFunction("check3", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		_ = runtime
		if len(args) != 3 {
			return rt.NumberValue(-100), nil
		}
		if !args[0].IsNumber() || args[0].Number() != 42 {
			return rt.NumberValue(-1), nil
		}
		if args[1].Kind() != rt.KindNil {
			return rt.NumberValue(-2), nil
		}
		if args[2].Kind() != rt.KindNil {
			return rt.NumberValue(-3), nil
		}
		return rt.NumberValue(42), nil
	})

	leaf := bytecode.NewProto("callmulti_leaf", 1, 0)
	leaf.Scripted = true
	const42 := leaf.AddConstant(rt.NumberValue(42))
	leaf.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(const42))
	leaf.Emit(bytecode.OpReturn, 0, 0, 0, 0)
	leafValue := engine.machine.NewClosureValue(leaf)

	root := bytecode.NewProto("callmulti_direct_root", 6, 0)
	root.Scripted = true
	leafConst := root.AddConstant(leafValue)
	checkConst := root.AddConstant(checkValue)
	root.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(leafConst))
	root.Emit(bytecode.OpCallMulti, 1, 0, 1, bytecode.PackCallCounts(0, 3))
	root.Emit(bytecode.OpLoadConst, 4, 0, 0, int32(checkConst))
	root.Emit(bytecode.OpCall, 5, 4, 1, 3)
	root.Emit(bytecode.OpReturn, 5, 0, 0, 0)

	result := runProtoRepeated(t, engine, root, 2)
	if got := result.Number(); got != 42 {
		t.Fatalf("callmulti direct result = %v, want 42", got)
	}
	stats := engine.Stats(root)
	if stats.CompiledDirectCalls == 0 {
		t.Fatalf("expected fixed-result callmulti proto to hit direct compiled callee, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for fixed-result callmulti proto, got %+v", stats)
	}
}

func TestVexarcCompiledHelperReentryForReturnAppendPendingAndAppendTable(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	multiFn := engine.runtime.NewHostFunctionMulti("pair", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		_ = runtime
		_ = args
		return []rt.Value{rt.NumberValue(40), rt.NumberValue(2)}, nil
	})

	returnProto := bytecode.NewProto("return_append_pending", 2, 0)
	returnProto.Scripted = true
	fnConst := returnProto.AddConstant(multiFn)
	returnProto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(fnConst))
	returnProto.Emit(bytecode.OpCallMulti, 0, 0, 1, bytecode.PackCallCounts(0, 0))
	returnProto.Emit(bytecode.OpReturnAppendPending, 0, 0, 0, 0)

	closureValue := engine.machine.NewClosureValue(returnProto)
	results, err := engine.machine.CallValue(closureValue, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(results); got != 2 {
		t.Fatalf("return-append result count = %d, want 2", got)
	}
	if got := results[0].Number(); got != 40 {
		t.Fatalf("return-append first result = %v, want 40", got)
	}
	if got := results[1].Number(); got != 2 {
		t.Fatalf("return-append second result = %v, want 2", got)
	}
	returnStats := engine.Stats(returnProto)
	if returnStats.CompiledHelperCalls == 0 {
		t.Fatalf("expected compiled helper calls for return-append proto, got %+v", returnStats)
	}
	if returnStats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for return-append proto, got %+v", returnStats)
	}

	tableProto := bytecode.NewProto("append_table_pending", 5, 0)
	tableProto.Scripted = true
	fnIndex := tableProto.AddConstant(multiFn)
	oneIndex := tableProto.AddConstant(rt.NumberValue(1))
	twoIndex := tableProto.AddConstant(rt.NumberValue(2))
	tableProto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(fnIndex))
	tableProto.Emit(bytecode.OpNewTable, 1, 0, 0, 2)
	tableProto.Emit(bytecode.OpCallMulti, 0, 0, 2, bytecode.PackCallCounts(0, 0))
	tableProto.Emit(bytecode.OpAppendTable, 1, 1, 0, 0)
	tableProto.Emit(bytecode.OpLoadConst, 2, 0, 0, int32(oneIndex))
	tableProto.Emit(bytecode.OpGetTableArray, 3, 1, 2, 0)
	tableProto.Emit(bytecode.OpLoadConst, 2, 0, 0, int32(twoIndex))
	tableProto.Emit(bytecode.OpGetTableArray, 4, 1, 2, 0)
	tableProto.Emit(bytecode.OpAddNum, 3, 3, 4, 0)
	tableProto.Emit(bytecode.OpReturn, 3, 0, 0, 0)

	tableResult, err := engine.Run(tableProto)
	if err != nil {
		t.Fatal(err)
	}
	if got := tableResult.Number(); got != 42 {
		t.Fatalf("append-table proto result = %v, want 42", got)
	}
	tableStats := engine.Stats(tableProto)
	if tableStats.CompiledHelperCalls == 0 {
		t.Fatalf("expected compiled helper calls for append-table proto, got %+v", tableStats)
	}
	if tableStats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for append-table proto, got %+v", tableStats)
	}
}

func TestVexarcCompiledMetamethodUnaryAddConcatStayInHelperMainline(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	proto := compileNamedForTest(t, engine, "@metamethod_unm_add_concat.lua", `
local mt = {}
mt.__unm = function(value)
	return setmetatable({n = -value.n}, mt)
end
mt.__add = function(lhs, rhs)
	return setmetatable({n = lhs.n + rhs.n}, mt)
end
mt.__concat = function(lhs, rhs)
	return tostring(lhs.n) .. tostring(rhs.n)
end

local sum = 0
for i = 1, 200 do
	local a = setmetatable({n = 40}, mt)
	local b = setmetatable({n = 2}, mt)
	local c = -(-a + -b)
	local s = c .. b
	sum = sum + c.n + string.len(s)
end
return sum
`)

	result := runProtoRepeated(t, engine, proto, 3)
	if got := result.Number(); got != 9000 {
		t.Fatalf("metamethod helper mainline result = %v, want 9000", got)
	}
	stats := engine.Stats(proto)
	if stats.CompiledEnters == 0 {
		t.Fatalf("expected compiled entry for metamethod helper proto, got %+v", stats)
	}
	if stats.CompiledHelperCalls == 0 {
		t.Fatalf("expected unary/add/concat metamethods to use helper-backed mainline, got %+v", stats)
	}
	if stats.CompiledReturns == 0 {
		t.Fatalf("expected compiled return for metamethod helper proto, got %+v", stats)
	}
	if stats.CompiledFallbacks != 0 {
		t.Fatalf("expected no interpreter fallback for metamethod helper proto, got %+v", stats)
	}
}

func TestVexarcCompiledExtendedStdlibAndMetatableWorkloadsStayInHelperMainline(t *testing.T) {
	testCases := []string{"metatable_dispatch", "string_find_match", "string_gsub", "table_sort"}
	for _, name := range testCases {
		t.Run(name, func(t *testing.T) {
			engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
			work := benchmarkWorkloadForTest(t, name)
			proto := compileNamedForTest(t, engine, "@"+work.Name+".lua", work.Source)

			result := runProtoRepeated(t, engine, proto, 3)
			if got := engine.FormatValue(result); got != work.Expected {
				t.Fatalf("workload %s result = %q, want %q", work.Name, got, work.Expected)
			}
			stats := engine.Stats(proto)
			if stats.CompiledEnters == 0 {
				t.Fatalf("expected compiled entry for workload %s, got %+v", work.Name, stats)
			}
			if stats.CompiledHelperCalls == 0 {
				t.Fatalf("expected helper-backed compiled mainline for workload %s, got %+v", work.Name, stats)
			}
			if stats.CompiledReturns == 0 {
				t.Fatalf("expected compiled return for workload %s, got %+v", work.Name, stats)
			}
			if stats.CompiledFallbacks != 0 {
				t.Fatalf("expected no interpreter fallback for workload %s, got %+v", work.Name, stats)
			}
		})
	}
}

func TestVexarcCompiledCoroutineWorkloadsStayInHelperMainline(t *testing.T) {
	testCases := []string{"coroutine_resume", "coroutine_steady_state"}
	for _, name := range testCases {
		t.Run(name, func(t *testing.T) {
			engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
			work := benchmarkWorkloadForTest(t, name)
			proto := compileNamedForTest(t, engine, "@"+work.Name+".lua", work.Source)

			result := runProtoRepeated(t, engine, proto, 3)
			if got := engine.FormatValue(result); got != work.Expected {
				t.Fatalf("workload %s result = %q, want %q", work.Name, got, work.Expected)
			}
			rootStats := engine.Stats(proto)
			if rootStats.CompiledEnters == 0 {
				t.Fatalf("expected compiled root entry for workload %s, got %+v", work.Name, rootStats)
			}
			if rootStats.CompiledHelperCalls == 0 {
				t.Fatalf("expected root helper-backed mainline for workload %s, got %+v", work.Name, rootStats)
			}
			if rootStats.CompiledFallbacks != 0 {
				t.Fatalf("expected no root interpreter fallback for workload %s, got %+v", work.Name, rootStats)
			}
			if len(proto.Children) == 0 {
				t.Fatalf("expected coroutine workload %s to produce child protos", work.Name)
			}
			childCompiled := false
			for _, child := range proto.Children {
				childStats := engine.Stats(child)
				if childStats.CompiledEnters == 0 {
					continue
				}
				childCompiled = true
				if childStats.CompiledHelperCalls == 0 {
					t.Fatalf("expected coroutine child %s to use helper-backed yield/resume path, got %+v", child.Name, childStats)
				}
				if childStats.CompiledFallbacks != 0 {
					t.Fatalf("expected coroutine child %s without interpreter fallback, got %+v", child.Name, childStats)
				}
			}
			if !childCompiled {
				t.Fatalf("expected at least one coroutine child in workload %s to enter compiled code", work.Name)
			}
		})
	}
}

func TestVexarcDebugHookDisablesCompiledChildEntryUntilCleared(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 1})
	proto := compileNamedForTest(t, engine, "@debug_hook_disables_child_vexarc.lua", `
local function hot(v)
	local sum = 0
	for i = 1, 5 do
		sum = sum + i
	end
	return sum + v
end

local total = 0
debug.sethook(function() end, "", 1)
for i = 1, 4 do
	total = total + hot(i)
end
debug.sethook()
total = total + hot(100)
return total
`)

	result, err := engine.Run(proto)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 185 {
		t.Fatalf("debug hook gate result = %v, want 185", got)
	}
	var hotStats ProgramStats
	hotCompiled := false
	for _, child := range proto.Children {
		childStats := engine.Stats(child)
		if childStats.CompiledEnters == 0 {
			continue
		}
		if hotCompiled {
			t.Fatalf("expected only one compiled child after hook cleared, got %+v and %+v", hotStats, childStats)
		}
		hotCompiled = true
		hotStats = childStats
	}
	if !hotCompiled {
		t.Fatalf("expected hot child to enter compiled code after hook cleared")
	}
	if hotStats.Runs != 5 {
		t.Fatalf("expected hot child to run 5 times across hooked and unhooked phases, got %+v", hotStats)
	}
	if hotStats.CompiledEnters != 1 {
		t.Fatalf("expected only post-hook child call to enter compiled code, got %+v", hotStats)
	}
	if hotStats.CompiledFallbacks != 0 {
		t.Fatalf("expected no child interpreter fallback after hook cleared, got %+v", hotStats)
	}
	if hotStats.CompiledReturns == 0 {
		t.Fatalf("expected child to return from compiled code after hook cleared, got %+v", hotStats)
	}
}

func benchmarkWorkloadForTest(t *testing.T, name string) benchmarks.Workload {
	t.Helper()
	for _, work := range benchmarks.ScriptWorkloads() {
		if work.Name == name {
			return work
		}
	}
	t.Fatalf("unknown benchmark workload %q", name)
	return benchmarks.Workload{}
}

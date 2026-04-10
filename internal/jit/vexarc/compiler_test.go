//go:build windows || linux

package vexarc

import (
	"testing"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/jit"
	rt "vexlua/internal/runtime"
)

func newTestProto() *bytecode.Proto {
	proto := bytecode.NewProto("vexarc_test", 2, 0)
	constant := proto.AddConstant(rt.NumberValue(1))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(constant))
	proto.Emit(bytecode.OpMove, 1, 0, 0, 0)
	proto.Emit(bytecode.OpReturn, 1, 0, 0, 0)
	return proto
}

func newNativeThreadState(runtime *rt.Runtime, caches []rt.FieldCache) jit.NativeThreadState {
	thread := jit.NativeThreadState{
		HeapTablesBase: runtime.Heap().TablesBase(),
		HeapTablesLen:  uintptr(runtime.Heap().TablesLen()),
	}
	if len(caches) != 0 {
		thread.FieldCachesBase = uintptr(unsafe.Pointer(&caches[0]))
		thread.FieldCachesLen = uintptr(len(caches))
	}
	return thread
}

func TestCompileWholeProtoInstallsCodeBlobAndMetadata(t *testing.T) {
	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: newTestProto(), Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	meta := unit.Meta()
	if meta.UnitID == 0 {
		t.Fatalf("compiled unit id = 0, want non-zero")
	}
	if meta.CodeSize == 0 {
		t.Fatalf("compiled code size = 0, want non-zero")
	}
	if got, want := len(meta.PCOffsets), len(meta.Proto.Code); got != want {
		t.Fatalf("pc offset count = %d, want %d", got, want)
	}
	slots := []rt.Value{rt.NilValue, rt.NilValue}
	exit, err := unit.Enter(&jit.NativeThreadState{}, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitReturn {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
	}
	if got := exit.ReturnValue.Number(); got != 1 {
		t.Fatalf("return value = %v, want 1", got)
	}
}

func TestCompileRegionInstallsRegionMetadata(t *testing.T) {
	compiler := NewCompilerWithCache(nil)
	region := jit.Region{ID: 7, StartPC: 1, EndPC: 3}
	unit, err := compiler.Compile(jit.CompileRequest{Proto: newTestProto(), Mode: jit.CompileRegion, Region: region})
	if err != nil {
		t.Fatal(err)
	}
	meta := unit.Meta()
	if meta.Mode != jit.CompileRegion {
		t.Fatalf("compile mode = %s, want %s", meta.Mode, jit.CompileRegion)
	}
	if meta.Region != region {
		t.Fatalf("region = %+v, want %+v", meta.Region, region)
	}
	if got, want := len(meta.PCOffsets), region.EndPC-region.StartPC; got != want {
		t.Fatalf("region pc offset count = %d, want %d", got, want)
	}
	if !meta.ContainsPC(region.StartPC) || meta.ContainsPC(region.EndPC) {
		t.Fatalf("region containment check failed for %+v", region)
	}
}

func TestCompileRegionExecutesNativeSubrange(t *testing.T) {
	compiler := NewCompilerWithCache(nil)
	region := jit.Region{ID: 7, StartPC: 1, EndPC: 3}
	unit, err := compiler.Compile(jit.CompileRequest{Proto: newTestProto(), Mode: jit.CompileRegion, Region: region})
	if err != nil {
		t.Fatal(err)
	}
	slots := []rt.Value{rt.NumberValue(1), rt.NilValue}
	exit, err := unit.Enter(&jit.NativeThreadState{}, &jit.NativeFrameState{PC: uint32(region.StartPC), SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitReturn {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
	}
	if got := exit.ReturnValue.Number(); got != 1 {
		t.Fatalf("region return value = %v, want 1", got)
	}
}

func TestCompileWholeProtoExecutesQuickenedNumericLoop(t *testing.T) {
	proto := bytecode.NewProto("sum_loop_quickened", 4, 0)
	zero := proto.AddConstant(rt.NumberValue(0))
	one := proto.AddConstant(rt.NumberValue(1))
	limit := proto.AddConstant(rt.NumberValue(5))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(zero))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(one))
	proto.Emit(bytecode.OpLoadConst, 2, 0, 0, int32(limit))
	proto.Emit(bytecode.OpLoadConst, 3, 0, 0, int32(one))
	proto.Emit(bytecode.OpAddNum, 0, 0, 1, 0)
	proto.Emit(bytecode.OpAddNum, 1, 1, 3, 0)
	proto.Emit(bytecode.OpLessEqualJump, 1, 2, 0, 4)
	proto.Emit(bytecode.OpReturn, 0, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	slots := make([]rt.Value, proto.MaxStack)
	exit, err := unit.Enter(&jit.NativeThreadState{}, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitReturn {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
	}
	if got := exit.ReturnValue.Number(); got != 15 {
		t.Fatalf("compiled loop result = %v, want 15", got)
	}
}

func TestCompileWholeProtoEmitsHelperExitForQuickenedTableOp(t *testing.T) {
	runtime := rt.NewRuntime()
	tableValue := runtime.NewTableValue(2)
	handle, _ := tableValue.Handle()
	runtime.Heap().Table(handle).SetIndex(1, rt.NumberValue(40))

	proto := bytecode.NewProto("table_helper_exit", 4, 0)
	tableConst := proto.AddConstant(tableValue)
	indexConst := proto.AddConstant(rt.NumberValue(1))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(tableConst))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(indexConst))
	proto.Emit(bytecode.OpGetTableArray, 2, 0, 1, 0)
	proto.Emit(bytecode.OpReturn, 2, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	meta := unit.Meta()
	if got := len(meta.HelperCalls); got != 1 {
		t.Fatalf("helper call count = %d, want 1", got)
	}
	if meta.HelperCalls[0].Kind != jit.HelperGetTableArray {
		t.Fatalf("helper kind = %s, want %s", meta.HelperCalls[0].Kind, jit.HelperGetTableArray)
	}
	slots := make([]rt.Value, proto.MaxStack)
	exit, err := unit.Enter(&jit.NativeThreadState{}, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitCallHelper {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitCallHelper)
	}
	if exit.HelperID == 0 {
		t.Fatalf("helper id = 0, want non-zero")
	}
	if exit.ResumePC != 3 {
		t.Fatalf("resume pc = %d, want 3", exit.ResumePC)
	}
	if meta.HelperCalls[0].CodeOffset != exit.CodeOffset {
		t.Fatalf("helper code offset = %d, want %d", meta.HelperCalls[0].CodeOffset, exit.CodeOffset)
	}
}

func TestCompileWholeProtoReturnsDirectlyForArrayFastPath(t *testing.T) {
	runtime := rt.NewRuntime()
	tableValue := runtime.NewTableValue(4)
	handle, _ := tableValue.Handle()
	runtime.Heap().Table(handle).SetIndex(1, rt.NumberValue(40))

	proto := bytecode.NewProto("table_fast_path", 4, 0)
	tableConst := proto.AddConstant(tableValue)
	indexConst := proto.AddConstant(rt.NumberValue(1))
	bonusConst := proto.AddConstant(rt.NumberValue(2))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(tableConst))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(indexConst))
	proto.Emit(bytecode.OpGetTableArray, 2, 0, 1, 0)
	proto.Emit(bytecode.OpAddConst, 3, 2, 0, int32(bonusConst))
	proto.Emit(bytecode.OpReturn, 3, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	slots := make([]rt.Value, proto.MaxStack)
	thread := newNativeThreadState(runtime, nil)
	exit, err := unit.Enter(&thread, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitReturn {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
	}
	if got := exit.ReturnValue.Number(); got != 42 {
		t.Fatalf("direct array fast-path result = %v, want 42", got)
	}
}

func TestCompileWholeProtoReturnsDirectlyForSetTableArrayFastPath(t *testing.T) {
	runtime := rt.NewRuntime()
	tableValue := runtime.NewTableValue(4)
	handle, _ := tableValue.Handle()
	runtime.Heap().Table(handle).SetIndex(1, rt.NumberValue(40))

	proto := bytecode.NewProto("table_set_fast_path", 5, 0)
	tableConst := proto.AddConstant(tableValue)
	indexTwo := proto.AddConstant(rt.NumberValue(2))
	valueTwo := proto.AddConstant(rt.NumberValue(2))
	oneIndex := proto.AddConstant(rt.NumberValue(1))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(tableConst))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(indexTwo))
	proto.Emit(bytecode.OpLoadConst, 2, 0, 0, int32(valueTwo))
	proto.Emit(bytecode.OpSetTableArray, 0, 1, 2, 0)
	proto.Emit(bytecode.OpLoadConst, 3, 0, 0, int32(oneIndex))
	proto.Emit(bytecode.OpGetTableArray, 4, 0, 3, 0)
	proto.Emit(bytecode.OpAddNum, 4, 4, 2, 0)
	proto.Emit(bytecode.OpReturn, 4, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	slots := make([]rt.Value, proto.MaxStack)
	thread := newNativeThreadState(runtime, nil)
	exit, err := unit.Enter(&thread, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitReturn {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
	}
	if got := exit.ReturnValue.Number(); got != 42 {
		t.Fatalf("direct set-table fast-path result = %v, want 42", got)
	}
}

func TestCompileWholeProtoReturnsDirectlyForFieldICFastPath(t *testing.T) {
	runtime := rt.NewRuntime()
	tableValue := runtime.NewTableValue(1)
	handle, _ := tableValue.Handle()
	symbol := runtime.InternSymbol("x")
	slot := runtime.Heap().Table(handle).SetSymbol(symbol, rt.NumberValue(40))
	cache := []rt.FieldCache{{
		Valid:   true,
		Table:   handle,
		Version: runtime.Heap().Table(handle).Version(),
		Slot:    slot,
		Symbol:  symbol,
	}}

	proto := bytecode.NewProto("field_ic_fast_path", 3, 1)
	tableConst := proto.AddConstant(tableValue)
	bonusConst := proto.AddConstant(rt.NumberValue(2))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(tableConst))
	proto.Emit(bytecode.OpGetFieldIC, 1, 0, 0, int32(symbol))
	proto.Emit(bytecode.OpAddConst, 2, 1, 0, int32(bonusConst))
	proto.Emit(bytecode.OpReturn, 2, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	slots := make([]rt.Value, proto.MaxStack)
	thread := newNativeThreadState(runtime, cache)
	exit, err := unit.Enter(&thread, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitReturn {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
	}
	if got := exit.ReturnValue.Number(); got != 42 {
		t.Fatalf("direct field-ic fast-path result = %v, want 42", got)
	}
}

func TestCompileWholeProtoReturnsDirectlyForSelfICFastPath(t *testing.T) {
	runtime := rt.NewRuntime()
	tableValue := runtime.NewTableValue(1)
	handle, _ := tableValue.Handle()
	symbol := runtime.InternSymbol("x")
	slot := runtime.Heap().Table(handle).SetSymbol(symbol, rt.NumberValue(42))
	cache := []rt.FieldCache{{
		Valid:   true,
		Table:   handle,
		Version: runtime.Heap().Table(handle).Version(),
		Slot:    slot,
		Symbol:  symbol,
	}}

	proto := bytecode.NewProto("self_ic_fast_path", 3, 1)
	tableConst := proto.AddConstant(tableValue)
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(tableConst))
	proto.Emit(bytecode.OpSelfIC, 1, 0, 0, int32(symbol))
	proto.Emit(bytecode.OpReturn, 1, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	slots := make([]rt.Value, proto.MaxStack)
	thread := newNativeThreadState(runtime, cache)
	exit, err := unit.Enter(&thread, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitReturn {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
	}
	if got := exit.ReturnValue.Number(); got != 42 {
		t.Fatalf("direct self-ic fast-path result = %v, want 42", got)
	}
}

func TestCompileWholeProtoEmitsHelperExitForLoadGlobal(t *testing.T) {
	proto := bytecode.NewProto("load_global_helper", 3, 0)
	proto.Emit(bytecode.OpLoadGlobal, 0, 0, 0, 7)
	proto.Emit(bytecode.OpReturn, 0, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	meta := unit.Meta()
	if got := len(meta.HelperCalls); got != 1 {
		t.Fatalf("helper call count = %d, want 1", got)
	}
	if meta.HelperCalls[0].Kind != jit.HelperLoadGlobal {
		t.Fatalf("helper kind = %s, want %s", meta.HelperCalls[0].Kind, jit.HelperLoadGlobal)
	}
}

func TestCompileWholeProtoReturnsDirectlyForLoadGlobalFastPath(t *testing.T) {
	runtime := rt.NewRuntime()
	symbol := runtime.InternSymbol("x")
	slot := runtime.Globals().SetSymbol(symbol, rt.NumberValue(42))
	cache := []rt.FieldCache{{
		Valid:   true,
		Table:   runtime.GlobalsHandle(),
		Version: runtime.Globals().Version(),
		Slot:    slot,
		Symbol:  symbol,
	}}

	proto := bytecode.NewProto("load_global_fast_path", 2, 0)
	proto.Emit(bytecode.OpLoadGlobal, 0, 0, 0, int32(symbol))
	proto.Emit(bytecode.OpReturn, 0, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	slots := make([]rt.Value, proto.MaxStack)
	thread := newNativeThreadState(runtime, cache)
	thread.CurrentEnvHandle = runtime.GlobalsHandle()
	exit, err := unit.Enter(&thread, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitReturn {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
	}
	if got := exit.ReturnValue.Number(); got != 42 {
		t.Fatalf("direct load-global fast-path result = %v, want 42", got)
	}
}

func TestCompileWholeProtoReturnsDirectlyForSetFieldFastPath(t *testing.T) {
	runtime := rt.NewRuntime()
	tableValue := runtime.NewTableValue(1)
	handle, _ := tableValue.Handle()
	symbol := runtime.InternSymbol("x")
	slot := runtime.Heap().Table(handle).SetSymbol(symbol, rt.NumberValue(1))
	cache := []rt.FieldCache{{
		Valid:   true,
		Table:   handle,
		Version: runtime.Heap().Table(handle).Version(),
		Slot:    slot,
		Symbol:  symbol,
	}}

	proto := bytecode.NewProto("set_field_fast_path", 3, 0)
	tableConst := proto.AddConstant(tableValue)
	valueConst := proto.AddConstant(rt.NumberValue(42))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(tableConst))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(valueConst))
	proto.Emit(bytecode.OpSetField, 0, 1, 0, int32(symbol))
	proto.Emit(bytecode.OpGetFieldIC, 2, 0, 0, int32(symbol))
	proto.Emit(bytecode.OpReturn, 2, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	slots := make([]rt.Value, proto.MaxStack)
	thread := newNativeThreadState(runtime, cache)
	exit, err := unit.Enter(&thread, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitReturn {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
	}
	if got := exit.ReturnValue.Number(); got != 42 {
		t.Fatalf("direct set-field fast-path result = %v, want 42", got)
	}
}

func TestCompileWholeProtoUsesLuaClosureCallHelperStubForScriptedCall(t *testing.T) {
	child := bytecode.NewProto("call_child", 1, 0)
	child.Scripted = true
	child.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(child.AddConstant(rt.NumberValue(42))))
	child.Emit(bytecode.OpReturn, 0, 0, 0, 0)

	proto := bytecode.NewProto("call_lua_stub", 3, 0)
	proto.Scripted = true
	childIndex := proto.AddChild(child)
	proto.Emit(bytecode.OpClosure, 0, 0, 0, int32(childIndex))
	proto.Emit(bytecode.OpCall, 1, 0, 0, 0)
	proto.Emit(bytecode.OpReturn, 1, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	meta := unit.Meta()
	if got := len(meta.HelperCalls); got < 4 {
		t.Fatalf("helper call count = %d, want at least 4", got)
	}
	if meta.HelperCalls[0].Kind != jit.HelperClosure {
		t.Fatalf("first helper kind = %s, want %s", meta.HelperCalls[0].Kind, jit.HelperClosure)
	}
	if meta.HelperCalls[1].Kind != jit.HelperCallHostFunction {
		t.Fatalf("second helper kind = %s, want %s", meta.HelperCalls[1].Kind, jit.HelperCallHostFunction)
	}
	if meta.HelperCalls[2].Kind != jit.HelperCallLuaClosure {
		t.Fatalf("third helper kind = %s, want %s", meta.HelperCalls[2].Kind, jit.HelperCallLuaClosure)
	}
	if meta.HelperCalls[3].Kind != jit.HelperCall {
		t.Fatalf("fourth helper kind = %s, want %s", meta.HelperCalls[3].Kind, jit.HelperCall)
	}
}

func TestCompileWholeProtoUsesHostFunctionCallHelperStub(t *testing.T) {
	runtime := rt.NewRuntime()
	fnValue := runtime.NewHostFunction("add1", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		_ = runtime
		return rt.NumberValue(args[0].Number() + 1), nil
	})

	proto := bytecode.NewProto("call_host_stub", 4, 0)
	fnConst := proto.AddConstant(fnValue)
	argConst := proto.AddConstant(rt.NumberValue(41))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(fnConst))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(argConst))
	proto.Emit(bytecode.OpCall, 2, 0, 1, 1)
	proto.Emit(bytecode.OpReturn, 2, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	meta := unit.Meta()
	if got := len(meta.HelperCalls); got != 2 {
		t.Fatalf("helper call count = %d, want 2", got)
	}
	if meta.HelperCalls[0].Kind != jit.HelperCallHostFunction {
		t.Fatalf("first helper kind = %s, want %s", meta.HelperCalls[0].Kind, jit.HelperCallHostFunction)
	}
	if meta.HelperCalls[1].Kind != jit.HelperCall {
		t.Fatalf("second helper kind = %s, want %s", meta.HelperCalls[1].Kind, jit.HelperCall)
	}
	if meta.Mode != jit.CompileWholeProto {
		t.Fatalf("compile mode = %s, want %s", meta.Mode, jit.CompileWholeProto)
	}
}

func TestCompileWholeProtoKeepsAddInHelperMainline(t *testing.T) {
	proto := bytecode.NewProto("retry_later_region", 3, 0)
	constValue := proto.AddConstant(rt.NumberValue(41))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(constValue))
	proto.Emit(bytecode.OpAdd, 1, 0, 0, 0)
	proto.Emit(bytecode.OpMove, 2, 0, 0, 0)
	proto.Emit(bytecode.OpReturn, 2, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	meta := unit.Meta()
	if meta.Mode != jit.CompileWholeProto {
		t.Fatalf("compile mode = %s, want %s", meta.Mode, jit.CompileWholeProto)
	}
	if got, want := len(meta.HelperCalls), 1; got != want {
		t.Fatalf("helper call count = %d, want %d", got, want)
	}
	if meta.HelperCalls[0].Kind != jit.HelperAdd {
		t.Fatalf("helper kind = %s, want %s", meta.HelperCalls[0].Kind, jit.HelperAdd)
	}
	if got, want := meta.HelperCalls[0].PC, 1; got != want {
		t.Fatalf("helper pc = %d, want %d", got, want)
	}
}

func TestCompileWholeProtoReturnsDirectlyForCompareAndNotFastPaths(t *testing.T) {
	proto := bytecode.NewProto("compare_not_fast_path", 5, 0)
	five := proto.AddConstant(rt.NumberValue(5))
	seven := proto.AddConstant(rt.NumberValue(7))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(five))
	proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(seven))
	proto.Emit(bytecode.OpLess, 2, 0, 1, 0)
	proto.Emit(bytecode.OpNot, 3, 2, 0, 0)
	proto.Emit(bytecode.OpLessEqual, 4, 0, 1, 0)
	proto.Emit(bytecode.OpReturn, 4, 0, 0, 0)

	compiler := NewCompilerWithCache(nil)
	unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err != nil {
		t.Fatal(err)
	}
	slots := make([]rt.Value, proto.MaxStack)
	exit, err := unit.Enter(&jit.NativeThreadState{}, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
	if err != nil {
		t.Fatal(err)
	}
	if exit.Reason != jit.ExitReturn {
		t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
	}
	if slots[2] != rt.TrueValue {
		t.Fatalf("less result = %v, want true", slots[2])
	}
	if slots[3] != rt.FalseValue {
		t.Fatalf("not result = %v, want false", slots[3])
	}
	if slots[4] != rt.TrueValue {
		t.Fatalf("less-equal result = %v, want true", slots[4])
	}
	if exit.ReturnValue != rt.TrueValue {
		t.Fatalf("return value = %v, want true", exit.ReturnValue)
	}
}

func TestCompileWholeProtoReturnsDirectlyForIntegerModFastPath(t *testing.T) {
	testCases := []struct {
		name  string
		left  float64
		right float64
		want  float64
	}{
		{name: "negative-left", left: -5, right: 3, want: 1},
		{name: "negative-right", left: 5, right: -3, want: -1},
		{name: "same-sign-negative", left: -5, right: -3, want: -2},
		{name: "zero-remainder", left: 6, right: 3, want: 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			proto := bytecode.NewProto("mod_fast_path_"+tc.name, 3, 0)
			left := proto.AddConstant(rt.NumberValue(tc.left))
			right := proto.AddConstant(rt.NumberValue(tc.right))
			proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(left))
			proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(right))
			proto.Emit(bytecode.OpMod, 2, 0, 1, 0)
			proto.Emit(bytecode.OpReturn, 2, 0, 0, 0)

			compiler := NewCompilerWithCache(nil)
			unit, err := compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
			if err != nil {
				t.Fatal(err)
			}
			slots := make([]rt.Value, proto.MaxStack)
			exit, err := unit.Enter(&jit.NativeThreadState{}, &jit.NativeFrameState{SlotsBase: uintptr(unsafe.Pointer(&slots[0]))})
			if err != nil {
				t.Fatal(err)
			}
			if exit.Reason != jit.ExitReturn {
				t.Fatalf("exit reason = %s, want %s", exit.Reason, jit.ExitReturn)
			}
			if got := exit.ReturnValue.Number(); got != tc.want {
				t.Fatalf("return value = %v, want %v", got, tc.want)
			}
		})
	}
}

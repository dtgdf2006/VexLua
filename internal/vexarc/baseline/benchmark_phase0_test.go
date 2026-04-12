package baseline

import (
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/stubs"
)

func BenchmarkStage7ArithmeticAdd(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledArithmeticBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterArithmeticBenchmarkHarness(b))
	})
}

func BenchmarkStage7Self(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledSelfBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterSelfBenchmarkHarness(b))
	})
}

func BenchmarkStage7Length(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledLengthBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterLengthBenchmarkHarness(b))
	})
}

func BenchmarkStage7GetUpvalue(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledGetUpvalueBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterGetUpvalueBenchmarkHarness(b))
	})
}

func BenchmarkStage7SetUpvalue(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledSetUpvalueBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterSetUpvalueBenchmarkHarness(b))
	})
}

func BenchmarkStage7OpenCallArgs(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledExpectedDeltas(b, newCompiledOpenCallArgsBenchmarkHarness(b), []uint64{0}, 0)
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterOpenCallArgsBenchmarkHarness(b))
	})
}

func BenchmarkStage7OpenCallResults(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledExpectedDeltas(b, newCompiledOpenCallResultsBenchmarkHarness(b), []uint64{0}, 0)
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterOpenCallResultsBenchmarkHarness(b))
	})
}

func BenchmarkStage7NewTable(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledExpectedDeltas(b, newCompiledNewTableBenchmarkHarness(b), nil, 1)
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterNewTableBenchmarkHarness(b))
	})
}

func BenchmarkStage7Closure(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledExpectedDeltas(b, newCompiledClosureBenchmarkHarness(b), nil, 1)
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterClosureBenchmarkHarness(b))
	})
}

func benchmarkCompiledExpectedDeltas(b *testing.B, harness *stage7CompiledHarness, expectedStubDeltas []uint64, expectedDeoptDelta uint64) {
	b.Helper()
	if len(expectedStubDeltas) != len(harness.watchedStubs) {
		b.Fatalf("benchmark expected %d stub deltas for %d watched stubs", len(expectedStubDeltas), len(harness.watchedStubs))
	}
	b.ReportAllocs()
	beforeStubs := captureBenchmarkStubCounts(harness.runtime, harness.watchedStubs)
	beforeDeopt := harness.runtime.DeoptCount()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		results, err := harness.runtime.Call(harness.thread, harness.closure, harness.args, -1)
		if err != nil {
			b.Fatalf("compiled runtime call: %v", err)
		}
		if len(results) != 1 || results[0].Bits() != harness.expected.Bits() {
			b.Fatalf("compiled result = %v, want %s", results, harness.expected)
		}
		stage7BenchmarkSink = results[0]
	}
	b.StopTimer()
	assertBenchmarkStubCountsDelta(b, harness.runtime, harness.watchedStubs, beforeStubs, expectedStubDeltas, uint64(b.N))
	wantDeopt := beforeDeopt + expectedDeoptDelta*uint64(b.N)
	if got := harness.runtime.DeoptCount(); got != wantDeopt {
		b.Fatalf("compiled benchmark deopt delta = %d, want %d", got-beforeDeopt, expectedDeoptDelta*uint64(b.N))
	}
}

func assertBenchmarkStubCountsDelta(b *testing.B, runtime *Runtime, watchedStubs []stubs.ID, beforeCounts []uint64, expectedPerCall []uint64, callCount uint64) {
	b.Helper()
	for index, stubID := range watchedStubs {
		want := beforeCounts[index] + expectedPerCall[index]*callCount
		if got := runtime.SlowStubCount(stubID); got != want {
			b.Fatalf("benchmark stub %d delta = %d, want %d", stubID, got-beforeCounts[index], expectedPerCall[index]*callCount)
		}
	}
}

func primeCompiledHarness(b *testing.B, harness *stage7CompiledHarness) {
	b.Helper()
	results, err := harness.runtime.Call(harness.thread, harness.closure, harness.args, -1)
	if err != nil {
		b.Fatalf("prime runtime call: %v", err)
	}
	if len(results) != 1 || results[0].Bits() != harness.expected.Bits() {
		b.Fatalf("prime result = %v, want %s", results, harness.expected)
	}
}

func newCompiledArithmeticBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	closure, err := engine.NewClosure(buildStage7ArithmeticBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new arithmetic closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:  runtime,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{value.NumberValue(10), value.NumberValue(1)},
		expected: value.NumberValue(42),
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterArithmeticBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
	b.Helper()
	engine, thread, env := newStage7InterpreterBenchmarkContext(b)
	closure, err := engine.NewClosure(buildStage7ArithmeticBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new arithmetic closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{value.NumberValue(10), value.NumberValue(1)},
		expected: value.NumberValue(42),
	}
}

func newCompiledSelfBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	methodKey, err := engine.InternString("method")
	if err != nil {
		b.Fatalf("intern method key: %v", err)
	}
	receiver, err := engine.NewTable(0, 1)
	if err != nil {
		b.Fatalf("new receiver: %v", err)
	}
	if err := engine.Tables.Set(receiver.Ref, methodKey.Value, value.NumberValue(42)); err != nil {
		b.Fatalf("seed receiver: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7SelfBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new self closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:  runtime,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{receiver.Value},
		expected: value.NumberValue(42),
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterSelfBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
	b.Helper()
	engine, thread, env := newStage7InterpreterBenchmarkContext(b)
	methodKey, err := engine.InternString("method")
	if err != nil {
		b.Fatalf("intern method key: %v", err)
	}
	receiver, err := engine.NewTable(0, 1)
	if err != nil {
		b.Fatalf("new receiver: %v", err)
	}
	if err := engine.Tables.Set(receiver.Ref, methodKey.Value, value.NumberValue(42)); err != nil {
		b.Fatalf("seed receiver: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7SelfBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new self closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{receiver.Value},
		expected: value.NumberValue(42),
	}
}

func newCompiledLengthBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	textValue, err := engine.InternString("hello")
	if err != nil {
		b.Fatalf("intern length input: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7LengthBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new length closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:  runtime,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{textValue.Value},
		expected: value.NumberValue(5),
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterLengthBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
	b.Helper()
	engine, thread, env := newStage7InterpreterBenchmarkContext(b)
	textValue, err := engine.InternString("hello")
	if err != nil {
		b.Fatalf("intern length input: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7LengthBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new length closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{textValue.Value},
		expected: value.NumberValue(5),
	}
}

func newCompiledGetUpvalueBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	upvalueRef := newStage7ClosedBenchmarkUpvalue(b, engine, thread, 32, value.NumberValue(42))
	closure, err := engine.NewClosure(buildStage7GetUpvalueBenchmarkProto(), env, []value.HeapRef44{upvalueRef})
	if err != nil {
		b.Fatalf("new getupvalue closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		expected:     value.NumberValue(42),
		watchedStubs: []stubs.ID{stubs.StubGetUpvalue},
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterGetUpvalueBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
	b.Helper()
	engine, thread, env := newStage7InterpreterBenchmarkContext(b)
	upvalueRef := newStage7ClosedBenchmarkUpvalue(b, engine, thread, 32, value.NumberValue(42))
	closure, err := engine.NewClosure(buildStage7GetUpvalueBenchmarkProto(), env, []value.HeapRef44{upvalueRef})
	if err != nil {
		b.Fatalf("new getupvalue closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		expected: value.NumberValue(42),
	}
}

func newCompiledSetUpvalueBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	upvalueRef := newStage7ClosedBenchmarkUpvalue(b, engine, thread, 32, value.NumberValue(0))
	closure, err := engine.NewClosure(buildStage7SetUpvalueBenchmarkProto(), env, []value.HeapRef44{upvalueRef})
	if err != nil {
		b.Fatalf("new setupvalue closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		args:         []value.TValue{value.NumberValue(99)},
		expected:     value.NumberValue(99),
		watchedStubs: []stubs.ID{stubs.StubSetUpvalue},
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterSetUpvalueBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
	b.Helper()
	engine, thread, env := newStage7InterpreterBenchmarkContext(b)
	upvalueRef := newStage7ClosedBenchmarkUpvalue(b, engine, thread, 32, value.NumberValue(0))
	closure, err := engine.NewClosure(buildStage7SetUpvalueBenchmarkProto(), env, []value.HeapRef44{upvalueRef})
	if err != nil {
		b.Fatalf("new setupvalue closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{value.NumberValue(99)},
		expected: value.NumberValue(99),
	}
}

func newCompiledOpenCallArgsBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	callee := newStage7DirectCallCallee(b, engine, env)
	closure, err := engine.NewClosure(buildStage7OpenCallArgsBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new open-call-args closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		args:         []value.TValue{callee, value.NumberValue(42)},
		expected:     value.NumberValue(42),
		watchedStubs: []stubs.ID{stubs.StubLuaCall},
	}
	primeCompiledHarness(b, harness)
	return harness
}

func newInterpreterOpenCallArgsBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
	b.Helper()
	engine, thread, env := newStage7InterpreterBenchmarkContext(b)
	callee := newStage7DirectCallCallee(b, engine, env)
	closure, err := engine.NewClosure(buildStage7OpenCallArgsBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new open-call-args closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{callee, value.NumberValue(42)},
		expected: value.NumberValue(42),
	}
}

func newCompiledOpenCallResultsBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	callee := newStage7DirectCallCallee(b, engine, env)
	closure, err := engine.NewClosure(buildStage7OpenCallResultsBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new open-call-results closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		args:         []value.TValue{callee, value.NumberValue(42)},
		expected:     value.NumberValue(42),
		watchedStubs: []stubs.ID{stubs.StubLuaCall},
	}
	primeCompiledHarness(b, harness)
	return harness
}

func newInterpreterOpenCallResultsBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
	b.Helper()
	engine, thread, env := newStage7InterpreterBenchmarkContext(b)
	callee := newStage7DirectCallCallee(b, engine, env)
	closure, err := engine.NewClosure(buildStage7OpenCallResultsBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new open-call-results closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{callee, value.NumberValue(42)},
		expected: value.NumberValue(42),
	}
}

func newCompiledNewTableBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	closure, err := engine.NewClosure(buildStage7NewTableBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new newtable closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:  runtime,
		thread:   thread,
		closure:  closure.Value,
		expected: value.NumberValue(0),
	}
	primeCompiledHarness(b, harness)
	return harness
}

func newInterpreterNewTableBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
	b.Helper()
	engine, thread, env := newStage7InterpreterBenchmarkContext(b)
	closure, err := engine.NewClosure(buildStage7NewTableBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new newtable closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		expected: value.NumberValue(0),
	}
}

func newCompiledClosureBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	closure, err := engine.NewClosure(buildStage7ClosureBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new closure benchmark proto: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:  runtime,
		thread:   thread,
		closure:  closure.Value,
		expected: value.NumberValue(42),
	}
	primeCompiledHarness(b, harness)
	return harness
}

func newInterpreterClosureBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
	b.Helper()
	engine, thread, env := newStage7InterpreterBenchmarkContext(b)
	closure, err := engine.NewClosure(buildStage7ClosureBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new closure benchmark proto: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		expected: value.NumberValue(42),
	}
}

func newStage7CompiledBenchmarkContext(b *testing.B) (*interp.Engine, *Runtime, *state.ThreadState, value.TValue) {
	b.Helper()
	engine := interp.New()
	runtime := NewRuntime(engine)
	b.Cleanup(func() { _ = runtime.Close() })
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		b.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		b.Fatalf("new env: %v", err)
	}
	return engine, runtime, thread, env.Value
}

func newStage7InterpreterBenchmarkContext(b *testing.B) (*interp.Engine, *state.ThreadState, value.TValue) {
	b.Helper()
	engine := interp.New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		b.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		b.Fatalf("new env: %v", err)
	}
	return engine, thread, env.Value
}

func newStage7ClosedBenchmarkUpvalue(b *testing.B, engine *interp.Engine, thread *state.ThreadState, slotIndex uint32, seeded value.TValue) value.HeapRef44 {
	b.Helper()
	upvalueSlot, err := thread.SlotAddress(slotIndex)
	if err != nil {
		b.Fatalf("upvalue slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(upvalueSlot, seeded); err != nil {
		b.Fatalf("seed upvalue slot: %v", err)
	}
	open, err := engine.Upvalues.FindOrCreateOpen(thread, upvalueSlot)
	if err != nil {
		b.Fatalf("open upvalue: %v", err)
	}
	if _, err := engine.Upvalues.CloseAtOrAbove(thread, upvalueSlot); err != nil {
		b.Fatalf("close upvalue: %v", err)
	}
	return open.Ref
}

func buildStage7ArithmeticBenchmarkProto() *bytecode.Proto {
	code := make([]bytecode.Instruction, 0, stage7BenchmarkUnroll+1)
	for index := 0; index < stage7BenchmarkUnroll; index++ {
		code = append(code, bytecode.CreateABC(bytecode.OP_ADD, 0, 0, 1))
	}
	code = append(code, bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0))
	return &bytecode.Proto{
		Source:       "@bench-arithmetic-add.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code:         code,
	}
}

func buildStage7SelfBenchmarkProto() *bytecode.Proto {
	code := make([]bytecode.Instruction, 0, stage7BenchmarkUnroll+1)
	for index := 0; index < stage7BenchmarkUnroll; index++ {
		code = append(code, bytecode.CreateABC(bytecode.OP_SELF, 1, 0, bytecode.RKAsk(0)))
	}
	code = append(code, bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0))
	return &bytecode.Proto{
		Source:       "@bench-self.lua",
		NumParams:    1,
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("method"),
		},
		Code: code,
	}
}

func buildStage7LengthBenchmarkProto() *bytecode.Proto {
	code := make([]bytecode.Instruction, 0, stage7BenchmarkUnroll+1)
	for index := 0; index < stage7BenchmarkUnroll; index++ {
		code = append(code, bytecode.CreateABC(bytecode.OP_LEN, 1, 0, 0))
	}
	code = append(code, bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0))
	return &bytecode.Proto{
		Source:       "@bench-len.lua",
		NumParams:    1,
		MaxStackSize: 2,
		Code:         code,
	}
}

func buildStage7GetUpvalueBenchmarkProto() *bytecode.Proto {
	code := make([]bytecode.Instruction, 0, stage7BenchmarkUnroll+1)
	for index := 0; index < stage7BenchmarkUnroll; index++ {
		code = append(code, bytecode.CreateABC(bytecode.OP_GETUPVAL, 0, 0, 0))
	}
	code = append(code, bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0))
	return &bytecode.Proto{
		Source:       "@bench-getupvalue.lua",
		NumUpvalues:  1,
		MaxStackSize: 1,
		Code:         code,
	}
}

func buildStage7SetUpvalueBenchmarkProto() *bytecode.Proto {
	code := make([]bytecode.Instruction, 0, stage7BenchmarkUnroll+1)
	for index := 0; index < stage7BenchmarkUnroll; index++ {
		code = append(code, bytecode.CreateABC(bytecode.OP_SETUPVAL, 0, 0, 0))
	}
	code = append(code, bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0))
	return &bytecode.Proto{
		Source:       "@bench-setupvalue.lua",
		NumParams:    1,
		NumUpvalues:  1,
		MaxStackSize: 1,
		Code:         code,
	}
}

func buildStage7OpenCallArgsBenchmarkProto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-open-call-args.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 0, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
}

func buildStage7OpenCallResultsBenchmarkProto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-open-call-results.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
}

func buildStage7NewTableBenchmarkProto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-newtable.lua",
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_NEWTABLE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_LEN, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
}

func buildStage7ClosureLeafProto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-closure-leaf.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(42),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
}

func buildStage7ClosureBenchmarkProto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-closure.lua",
		MaxStackSize: 1,
		Protos: []*bytecode.Proto{
			buildStage7ClosureLeafProto(),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_CLOSURE, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
}

package baseline

import (
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/stubs"
)

var stage7BenchmarkSink value.TValue

const stage7BenchmarkUnroll = 32

type stage7CompiledHarness struct {
	runtime     *Runtime
	thread      *state.ThreadState
	closure     value.TValue
	args        []value.TValue
	expected    value.TValue
	watchedStub stubs.ID
}

type stage7InterpreterHarness struct {
	engine   *interp.Engine
	thread   *state.ThreadState
	closure  value.TValue
	args     []value.TValue
	expected value.TValue
}

func BenchmarkStage7MonomorphicGetGlobal(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledGetGlobalBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterGetGlobalBenchmarkHarness(b))
	})
}

func BenchmarkStage7MonomorphicGetTable(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledGetTableBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterGetTableBenchmarkHarness(b))
	})
}

func BenchmarkStage7MonomorphicSetTable(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledSetTableBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterSetTableBenchmarkHarness(b))
	})
}

func benchmarkCompiledHotPath(b *testing.B, harness *stage7CompiledHarness) {
	b.Helper()
	b.ReportAllocs()
	beforeStub := harness.runtime.SlowStubCount(harness.watchedStub)
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
	if harness.runtime.SlowStubCount(harness.watchedStub) != beforeStub {
		b.Fatalf("compiled benchmark should stay on monomorphic fast path: before=%d after=%d", beforeStub, harness.runtime.SlowStubCount(harness.watchedStub))
	}
	if harness.runtime.DeoptCount() != beforeDeopt {
		b.Fatalf("compiled benchmark should avoid deopt: before=%d after=%d", beforeDeopt, harness.runtime.DeoptCount())
	}
}

func benchmarkInterpreterPath(b *testing.B, harness *stage7InterpreterHarness) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		results, err := harness.engine.Call(harness.thread, harness.closure, harness.args, -1)
		if err != nil {
			b.Fatalf("interpreter call: %v", err)
		}
		if len(results) != 1 || results[0].Bits() != harness.expected.Bits() {
			b.Fatalf("interpreter result = %v, want %s", results, harness.expected)
		}
		stage7BenchmarkSink = results[0]
	}
}

func newCompiledGetGlobalBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
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
	if err := engine.SetGlobal(env.Value, "answer", value.NumberValue(42)); err != nil {
		b.Fatalf("set global: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7GetGlobalBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:     runtime,
		thread:      thread,
		closure:     closure.Value,
		expected:    value.NumberValue(42),
		watchedStub: stubs.StubGetGlobal,
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterGetGlobalBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
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
	if err := engine.SetGlobal(env.Value, "answer", value.NumberValue(42)); err != nil {
		b.Fatalf("set global: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7GetGlobalBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		expected: value.NumberValue(42),
	}
}

func newCompiledGetTableBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
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
	keyValue, err := engine.InternString("value")
	if err != nil {
		b.Fatalf("intern key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		b.Fatalf("new box: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyValue.Value, value.NumberValue(42)); err != nil {
		b.Fatalf("seed box table: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7GetTableBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:     runtime,
		thread:      thread,
		closure:     closure.Value,
		args:        []value.TValue{box.Value},
		expected:    value.NumberValue(42),
		watchedStub: stubs.StubGetTable,
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterGetTableBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
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
	keyValue, err := engine.InternString("value")
	if err != nil {
		b.Fatalf("intern key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		b.Fatalf("new box: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyValue.Value, value.NumberValue(42)); err != nil {
		b.Fatalf("seed box table: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7GetTableBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{box.Value},
		expected: value.NumberValue(42),
	}
}

func newCompiledSetTableBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
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
	keyValue, err := engine.InternString("value")
	if err != nil {
		b.Fatalf("intern key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		b.Fatalf("new box: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyValue.Value, value.NumberValue(1)); err != nil {
		b.Fatalf("seed box table: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7SetTableBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:     runtime,
		thread:      thread,
		closure:     closure.Value,
		args:        []value.TValue{box.Value, value.NumberValue(99)},
		expected:    value.NumberValue(99),
		watchedStub: stubs.StubSetTable,
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterSetTableBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
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
	keyValue, err := engine.InternString("value")
	if err != nil {
		b.Fatalf("intern key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		b.Fatalf("new box: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyValue.Value, value.NumberValue(1)); err != nil {
		b.Fatalf("seed box table: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7SetTableBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{box.Value, value.NumberValue(99)},
		expected: value.NumberValue(99),
	}
}

func warmCompiledHarness(b *testing.B, harness *stage7CompiledHarness) {
	b.Helper()
	results, err := harness.runtime.Call(harness.thread, harness.closure, harness.args, -1)
	if err != nil {
		b.Fatalf("warm runtime call: %v", err)
	}
	if len(results) != 1 || results[0].Bits() != harness.expected.Bits() {
		b.Fatalf("warm result = %v, want %s", results, harness.expected)
	}
	beforeStub := harness.runtime.SlowStubCount(harness.watchedStub)
	beforeDeopt := harness.runtime.DeoptCount()
	results, err = harness.runtime.Call(harness.thread, harness.closure, harness.args, -1)
	if err != nil {
		b.Fatalf("verify monomorphic runtime call: %v", err)
	}
	if len(results) != 1 || results[0].Bits() != harness.expected.Bits() {
		b.Fatalf("verify result = %v, want %s", results, harness.expected)
	}
	if harness.runtime.SlowStubCount(harness.watchedStub) != beforeStub {
		b.Fatalf("warm path should already be monomorphic: before=%d after=%d", beforeStub, harness.runtime.SlowStubCount(harness.watchedStub))
	}
	if harness.runtime.DeoptCount() != beforeDeopt {
		b.Fatalf("warm path should avoid deopt: before=%d after=%d", beforeDeopt, harness.runtime.DeoptCount())
	}
}

func buildStage7GetGlobalBenchmarkProto() *bytecode.Proto {
	code := make([]bytecode.Instruction, 0, stage7BenchmarkUnroll+1)
	for index := 0; index < stage7BenchmarkUnroll; index++ {
		code = append(code, bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0))
	}
	code = append(code, bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0))
	return &bytecode.Proto{
		Source:       "@bench-getglobal.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("answer"),
		},
		Code: code,
	}
}

func buildStage7GetTableBenchmarkProto() *bytecode.Proto {
	code := make([]bytecode.Instruction, 0, stage7BenchmarkUnroll+1)
	for index := 0; index < stage7BenchmarkUnroll; index++ {
		code = append(code, bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(0)))
	}
	code = append(code, bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0))
	return &bytecode.Proto{
		Source:       "@bench-gettable.lua",
		NumParams:    1,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("value"),
		},
		Code: code,
	}
}

func buildStage7SetTableBenchmarkProto() *bytecode.Proto {
	code := make([]bytecode.Instruction, 0, stage7BenchmarkUnroll+1)
	for index := 0; index < stage7BenchmarkUnroll; index++ {
		code = append(code, bytecode.CreateABC(bytecode.OP_SETTABLE, 0, bytecode.RKAsk(0), 1))
	}
	code = append(code, bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0))
	return &bytecode.Proto{
		Source:       "@bench-settable.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("value"),
		},
		Code: code,
	}
}

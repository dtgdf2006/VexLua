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
const stage7BenchmarkLoopTrips = 1024

type stage7CompiledHarness struct {
	runtime      *Runtime
	thread       *state.ThreadState
	closure      value.TValue
	args         []value.TValue
	expected     value.TValue
	watchedStubs []stubs.ID
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

func BenchmarkStage7DirectCompiledCall(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledDirectCallBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterDirectCallBenchmarkHarness(b))
	})
}

func BenchmarkStage7DirectCompiledCallLoop(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledDirectCallLoopBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterDirectCallLoopBenchmarkHarness(b))
	})
}

func BenchmarkStage7DirectCompiledCallChain(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledDirectCallChainBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterDirectCallChainBenchmarkHarness(b))
	})
}

func BenchmarkStage7TailCallLoop(b *testing.B) {
	b.Run("compiled", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledTailCallLoopBenchmarkHarness(b))
	})
	b.Run("interpreter", func(b *testing.B) {
		benchmarkInterpreterPath(b, newInterpreterTailCallLoopBenchmarkHarness(b))
	})
}

func benchmarkCompiledHotPath(b *testing.B, harness *stage7CompiledHarness) {
	b.Helper()
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
	assertBenchmarkStubCountsStable(b, harness.runtime, harness.watchedStubs, beforeStubs)
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
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		expected:     value.NumberValue(42),
		watchedStubs: []stubs.ID{stubs.StubGetGlobal},
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
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		args:         []value.TValue{box.Value},
		expected:     value.NumberValue(42),
		watchedStubs: []stubs.ID{stubs.StubGetTable},
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
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		args:         []value.TValue{box.Value, value.NumberValue(99)},
		expected:     value.NumberValue(99),
		watchedStubs: []stubs.ID{stubs.StubSetTable},
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newCompiledDirectCallBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
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
	callee := newStage7DirectCallCallee(b, engine, env.Value)
	closure, err := engine.NewClosure(buildStage7DirectCallBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		args:         []value.TValue{callee, value.NumberValue(42)},
		expected:     value.NumberValue(42),
		watchedStubs: []stubs.ID{stubs.StubLuaCall},
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterDirectCallBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
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
	callee := newStage7DirectCallCallee(b, engine, env.Value)
	closure, err := engine.NewClosure(buildStage7DirectCallBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{callee, value.NumberValue(42)},
		expected: value.NumberValue(42),
	}
}

func newCompiledDirectCallLoopBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
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
	callee := newStage7DirectCallCallee(b, engine, env.Value)
	closure, err := engine.NewClosure(buildStage7DirectCallLoopBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	expected := float64(stage7BenchmarkLoopTrips * (stage7BenchmarkLoopTrips + 1) / 2)
	harness := &stage7CompiledHarness{
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		args:         []value.TValue{callee},
		expected:     value.NumberValue(expected),
		watchedStubs: []stubs.ID{stubs.StubLuaCall, stubs.StubForPrep, stubs.StubForLoop},
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterDirectCallLoopBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
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
	callee := newStage7DirectCallCallee(b, engine, env.Value)
	closure, err := engine.NewClosure(buildStage7DirectCallLoopBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	expected := float64(stage7BenchmarkLoopTrips * (stage7BenchmarkLoopTrips + 1) / 2)
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{callee},
		expected: value.NumberValue(expected),
	}
}

func newCompiledDirectCallChainBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
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
	mid1, mid2, leaf := newStage7DirectCallChainClosures(b, engine, env.Value)
	closure, err := engine.NewClosure(buildStage7DirectCallChainBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	harness := &stage7CompiledHarness{
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		args:         []value.TValue{mid1, mid2, leaf, value.NumberValue(42)},
		expected:     value.NumberValue(42),
		watchedStubs: []stubs.ID{stubs.StubLuaCall},
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterDirectCallChainBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
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
	mid1, mid2, leaf := newStage7DirectCallChainClosures(b, engine, env.Value)
	closure, err := engine.NewClosure(buildStage7DirectCallChainBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{mid1, mid2, leaf, value.NumberValue(42)},
		expected: value.NumberValue(42),
	}
}

func newCompiledTailCallLoopBenchmarkHarness(b *testing.B) *stage7CompiledHarness {
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
	tailMid, leaf := newStage7TailCallLoopClosures(b, engine, env.Value)
	closure, err := engine.NewClosure(buildStage7TailCallLoopBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	expected := float64(stage7BenchmarkLoopTrips * (stage7BenchmarkLoopTrips + 1) / 2)
	harness := &stage7CompiledHarness{
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		args:         []value.TValue{tailMid, leaf},
		expected:     value.NumberValue(expected),
		watchedStubs: []stubs.ID{stubs.StubLuaCall, stubs.StubTailCall, stubs.StubForPrep, stubs.StubForLoop},
	}
	warmCompiledHarness(b, harness)
	return harness
}

func newInterpreterTailCallLoopBenchmarkHarness(b *testing.B) *stage7InterpreterHarness {
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
	tailMid, leaf := newStage7TailCallLoopClosures(b, engine, env.Value)
	closure, err := engine.NewClosure(buildStage7TailCallLoopBenchmarkProto(), env.Value, nil)
	if err != nil {
		b.Fatalf("new closure: %v", err)
	}
	expected := float64(stage7BenchmarkLoopTrips * (stage7BenchmarkLoopTrips + 1) / 2)
	return &stage7InterpreterHarness{
		engine:   engine,
		thread:   thread,
		closure:  closure.Value,
		args:     []value.TValue{tailMid, leaf},
		expected: value.NumberValue(expected),
	}
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
	beforeStubs := captureBenchmarkStubCounts(harness.runtime, harness.watchedStubs)
	beforeDeopt := harness.runtime.DeoptCount()
	results, err = harness.runtime.Call(harness.thread, harness.closure, harness.args, -1)
	if err != nil {
		b.Fatalf("verify monomorphic runtime call: %v", err)
	}
	if len(results) != 1 || results[0].Bits() != harness.expected.Bits() {
		b.Fatalf("verify result = %v, want %s", results, harness.expected)
	}
	assertBenchmarkStubCountsStable(b, harness.runtime, harness.watchedStubs, beforeStubs)
	if harness.runtime.DeoptCount() != beforeDeopt {
		b.Fatalf("warm path should avoid deopt: before=%d after=%d", beforeDeopt, harness.runtime.DeoptCount())
	}
}

func captureBenchmarkStubCounts(runtime *Runtime, watchedStubs []stubs.ID) []uint64 {
	counts := make([]uint64, len(watchedStubs))
	for index, stubID := range watchedStubs {
		counts[index] = runtime.SlowStubCount(stubID)
	}
	return counts
}

func assertBenchmarkStubCountsStable(b *testing.B, runtime *Runtime, watchedStubs []stubs.ID, beforeCounts []uint64) {
	b.Helper()
	for index, stubID := range watchedStubs {
		after := runtime.SlowStubCount(stubID)
		if after != beforeCounts[index] {
			b.Fatalf("benchmark path should stay on monomorphic fast path for stub %d: before=%d after=%d", stubID, beforeCounts[index], after)
		}
	}
}

func newStage7DirectCallCallee(b *testing.B, engine *interp.Engine, env value.TValue) value.TValue {
	b.Helper()
	closure, err := engine.NewClosure(buildStage7DirectCallLeafProto(), env, nil)
	if err != nil {
		b.Fatalf("new direct-call callee: %v", err)
	}
	return closure.Value
}

func newStage7DirectCallChainClosures(b *testing.B, engine *interp.Engine, env value.TValue) (value.TValue, value.TValue, value.TValue) {
	b.Helper()
	leaf, err := engine.NewClosure(buildStage7DirectCallLeafProto(), env, nil)
	if err != nil {
		b.Fatalf("new direct-call chain leaf: %v", err)
	}
	mid2, err := engine.NewClosure(buildStage7DirectCallChainMid2Proto(), env, nil)
	if err != nil {
		b.Fatalf("new direct-call chain mid2: %v", err)
	}
	mid1, err := engine.NewClosure(buildStage7DirectCallChainMid1Proto(), env, nil)
	if err != nil {
		b.Fatalf("new direct-call chain mid1: %v", err)
	}
	return mid1.Value, mid2.Value, leaf.Value
}

func newStage7TailCallLoopClosures(b *testing.B, engine *interp.Engine, env value.TValue) (value.TValue, value.TValue) {
	b.Helper()
	leaf, err := engine.NewClosure(buildStage7DirectCallLeafProto(), env, nil)
	if err != nil {
		b.Fatalf("new tailcall loop leaf: %v", err)
	}
	tailMid, err := engine.NewClosure(buildStage7TailCallMidProto(), env, nil)
	if err != nil {
		b.Fatalf("new tailcall loop mid: %v", err)
	}
	return tailMid.Value, leaf.Value
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

func buildStage7DirectCallLeafProto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-direct-call-leaf.lua",
		NumParams:    1,
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
}

func buildStage7DirectCallBenchmarkProto() *bytecode.Proto {
	code := make([]bytecode.Instruction, 0, stage7BenchmarkUnroll*3+1)
	for index := 0; index < stage7BenchmarkUnroll; index++ {
		code = append(code,
			bytecode.CreateABC(bytecode.OP_MOVE, 2, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 3, 1, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 2, 2, 2),
		)
	}
	code = append(code, bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0))
	return &bytecode.Proto{
		Source:       "@bench-direct-call.lua",
		NumParams:    2,
		MaxStackSize: 4,
		Code:         code,
	}
}

func buildStage7DirectCallLoopBenchmarkProto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-direct-call-loop.lua",
		NumParams:    1,
		MaxStackSize: 8,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(0),
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(float64(stage7BenchmarkLoopTrips)),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 3, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 4, 1),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 2, 4),
			bytecode.CreateABC(bytecode.OP_MOVE, 6, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 7, 5, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 6, 2, 2),
			bytecode.CreateABC(bytecode.OP_ADD, 1, 1, 6),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 2, -5),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
}

func buildStage7DirectCallChainMid2Proto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-direct-call-chain-mid2.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
}

func buildStage7DirectCallChainMid1Proto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-direct-call-chain-mid1.lua",
		NumParams:    3,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 3, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
}

func buildStage7DirectCallChainBenchmarkProto() *bytecode.Proto {
	code := make([]bytecode.Instruction, 0, stage7BenchmarkUnroll*5+1)
	for index := 0; index < stage7BenchmarkUnroll; index++ {
		code = append(code,
			bytecode.CreateABC(bytecode.OP_MOVE, 4, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 5, 1, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 6, 2, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 7, 3, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 4, 4, 2),
		)
	}
	code = append(code, bytecode.CreateABC(bytecode.OP_RETURN, 4, 2, 0))
	return &bytecode.Proto{
		Source:       "@bench-direct-call-chain.lua",
		NumParams:    4,
		MaxStackSize: 8,
		Code:         code,
	}
}

func buildStage7TailCallMidProto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-tailcall-mid.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 2, 0),
		},
	}
}

func buildStage7TailCallLoopBenchmarkProto() *bytecode.Proto {
	return &bytecode.Proto{
		Source:       "@bench-tailcall-loop.lua",
		NumParams:    2,
		MaxStackSize: 10,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(0),
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(float64(stage7BenchmarkLoopTrips)),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 3, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 4, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 5, 1),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 3, 5),
			bytecode.CreateABC(bytecode.OP_MOVE, 7, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 8, 1, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 9, 6, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 7, 3, 2),
			bytecode.CreateABC(bytecode.OP_ADD, 2, 2, 7),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 3, -6),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
		},
	}
}

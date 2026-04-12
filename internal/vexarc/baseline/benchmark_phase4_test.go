package baseline

import (
	"testing"

	"vexlua/internal/interp"
	rtheap "vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/stubs"
)

type phase43SafepointAssist struct {
	safepoints uint64
}

func (assist *phase43SafepointAssist) AssistAllocation(uint64) error {
	return nil
}

func (assist *phase43SafepointAssist) AssistSafepoint() error {
	assist.safepoints++
	return nil
}

type phase43SetTableMarkingHarness struct {
	compiled *stage7CompiledHarness
	engine   *interp.Engine
	tableRef value.HeapRef44
	childRef value.HeapRef44
}

func BenchmarkPhase43CoveredCallSafepoint(b *testing.B) {
	b.Run("compiled-fastpath", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledDirectCallBenchmarkHarness(b))
	})
	b.Run("compiled-safepoint-requested", func(b *testing.B) {
		harness, assist := newCompiledCallSafepointBenchmarkHarness(b)
		benchmarkCompiledSafepointPath(b, harness, assist, uint64(stage7BenchmarkUnroll))
	})
}

func BenchmarkPhase43MarkingSetTableBarrier(b *testing.B) {
	b.Run("compiled-fastpath", func(b *testing.B) {
		benchmarkCompiledHotPath(b, newCompiledSetTableBenchmarkHarness(b))
	})
	b.Run("compiled-marking-barrier", func(b *testing.B) {
		harness := newCompiledSetTableMarkingBenchmarkHarness(b)
		benchmarkCompiledMarkingSetTablePath(b, harness, []uint64{uint64(stage7BenchmarkUnroll)})
	})
}

func newCompiledCallSafepointBenchmarkHarness(b *testing.B) (*stage7CompiledHarness, *phase43SafepointAssist) {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	assist := &phase43SafepointAssist{}
	engine.SetAllocationAssistant(assist)
	engine.Heap.SetGCThreshold(1)
	if _, err := engine.InternString("phase4.3-call-safepoint-trigger"); err != nil {
		b.Fatalf("intern safepoint trigger: %v", err)
	}
	if !engine.Heap.GCTargetReached() {
		b.Fatalf("gc target should be reached for safepoint benchmark")
	}
	callee := newStage7DirectCallCallee(b, engine, env)
	closure, err := engine.NewClosure(buildStage7DirectCallBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new safepoint benchmark closure: %v", err)
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
	assist.safepoints = 0
	return harness, assist
}

func newCompiledSetTableMarkingBenchmarkHarness(b *testing.B) *phase43SetTableMarkingHarness {
	b.Helper()
	engine, runtime, thread, env := newStage7CompiledBenchmarkContext(b)
	keyValue, err := engine.InternString("value")
	if err != nil {
		b.Fatalf("intern settable key: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		b.Fatalf("new benchmark box: %v", err)
	}
	child, err := engine.NewTable(0, 0)
	if err != nil {
		b.Fatalf("new benchmark child: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, keyValue.Value, child.Value); err != nil {
		b.Fatalf("seed benchmark box: %v", err)
	}
	closure, err := engine.NewClosure(buildStage7SetTableBenchmarkProto(), env, nil)
	if err != nil {
		b.Fatalf("new marking settable closure: %v", err)
	}
	compiled := &stage7CompiledHarness{
		runtime:      runtime,
		thread:       thread,
		closure:      closure.Value,
		args:         []value.TValue{box.Value, child.Value},
		expected:     child.Value,
		watchedStubs: []stubs.ID{stubs.StubSetTable},
	}
	warmCompiledHarness(b, compiled)
	return &phase43SetTableMarkingHarness{
		compiled: compiled,
		engine:   engine,
		tableRef: box.Ref,
		childRef: child.Ref,
	}
}

func benchmarkCompiledSafepointPath(b *testing.B, harness *stage7CompiledHarness, assist *phase43SafepointAssist, expectedPerCall uint64) {
	b.Helper()
	b.ReportAllocs()
	beforeStubs := captureBenchmarkStubCounts(harness.runtime, harness.watchedStubs)
	beforeDeopt := harness.runtime.DeoptCount()
	beforeSafepoints := assist.safepoints
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		results, err := harness.runtime.Call(harness.thread, harness.closure, harness.args, -1)
		if err != nil {
			b.Fatalf("compiled safepoint runtime call: %v", err)
		}
		if len(results) != 1 || results[0].Bits() != harness.expected.Bits() {
			b.Fatalf("compiled safepoint result = %v, want %s", results, harness.expected)
		}
		stage7BenchmarkSink = results[0]
	}
	b.StopTimer()
	assertBenchmarkStubCountsStable(b, harness.runtime, harness.watchedStubs, beforeStubs)
	if harness.runtime.DeoptCount() != beforeDeopt {
		b.Fatalf("compiled safepoint benchmark should avoid deopt: before=%d after=%d", beforeDeopt, harness.runtime.DeoptCount())
	}
	wantSafepoints := beforeSafepoints + expectedPerCall*uint64(b.N)
	if assist.safepoints != wantSafepoints {
		b.Fatalf("compiled safepoint count = %d, want %d", assist.safepoints-beforeSafepoints, expectedPerCall*uint64(b.N))
	}
}

func benchmarkCompiledMarkingSetTablePath(b *testing.B, harness *phase43SetTableMarkingHarness, expectedStubDeltas []uint64) {
	b.Helper()
	b.ReportAllocs()
	beforeStubs := captureBenchmarkStubCounts(harness.compiled.runtime, harness.compiled.watchedStubs)
	beforeDeopt := harness.compiled.runtime.DeoptCount()
	b.ResetTimer()
	b.StopTimer()
	for index := 0; index < b.N; index++ {
		prepareMarkingSetTableIteration(b, harness)
		b.StartTimer()
		results, err := harness.compiled.runtime.Call(harness.compiled.thread, harness.compiled.closure, harness.compiled.args, -1)
		if err != nil {
			b.Fatalf("compiled marking runtime call: %v", err)
		}
		if len(results) != 1 || results[0].Bits() != harness.compiled.expected.Bits() {
			b.Fatalf("compiled marking result = %v, want %s", results, harness.compiled.expected)
		}
		stage7BenchmarkSink = results[0]
		b.StopTimer()
		queues := harness.engine.Heap.GCQueueLengths()
		if queues.GrayAgain != 1 || queues.Remembered != 1 {
			b.Fatalf("compiled marking barrier queues = %+v, want grayAgain=1 remembered=1", queues)
		}
	}
	assertBenchmarkStubCountsDelta(b, harness.compiled.runtime, harness.compiled.watchedStubs, beforeStubs, expectedStubDeltas, uint64(b.N))
	if harness.compiled.runtime.DeoptCount() != beforeDeopt {
		b.Fatalf("compiled marking benchmark should avoid deopt: before=%d after=%d", beforeDeopt, harness.compiled.runtime.DeoptCount())
	}
	if harness.engine.Heap.GCPhase() != rtheap.GCPhaseMark {
		b.Fatalf("benchmark heap phase = %d, want mark", harness.engine.Heap.GCPhase())
	}
}

func prepareMarkingSetTableIteration(b *testing.B, harness *phase43SetTableMarkingHarness) {
	b.Helper()
	harness.engine.Heap.ResetGCQueues()
	harness.engine.Heap.SetGCPhase(rtheap.GCPhaseMark)
	markHeapRefForBenchmark(b, harness.engine.Heap, harness.tableRef, value.MarkBlack)
	markHeapRefForBenchmark(b, harness.engine.Heap, harness.childRef, harness.engine.Heap.CurrentWhite())
}

func mustHeapOffsetForBenchmark(b *testing.B, runtimeHeap *rtheap.Heap, ref value.HeapRef44) value.HeapOff64 {
	b.Helper()
	address, err := runtimeHeap.DecodeHeapRef(ref)
	if err != nil {
		b.Fatalf("decode heap ref %#x: %v", uint64(ref), err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		b.Fatalf("offset for heap ref %#x: %v", uint64(ref), err)
	}
	return offset
}

func markHeapRefForBenchmark(b *testing.B, runtimeHeap *rtheap.Heap, ref value.HeapRef44, mark value.MarkBits) {
	b.Helper()
	offset := mustHeapOffsetForBenchmark(b, runtimeHeap, ref)
	header, err := runtimeHeap.HeaderAtOffset(offset)
	if err != nil {
		b.Fatalf("header at heap ref %#x: %v", uint64(ref), err)
	}
	header.Mark = mark
	if err := runtimeHeap.WriteHeader(offset, header); err != nil {
		b.Fatalf("write header at heap ref %#x: %v", uint64(ref), err)
	}
}

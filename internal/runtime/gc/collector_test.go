package gc

import (
	"fmt"
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/feedback"
	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/host"
	"vexlua/internal/runtime/state"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/table"
	"vexlua/internal/runtime/value"
)

func TestCollectorShellSeedsRootsAndRespectsLiveTop(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{Threshold: 4096, StepBudget: 64, ProtoStore: engine.Protos})
	if collector.Threshold() != 4096 || collector.StepBudget() != 64 {
		t.Fatalf("collector config = threshold %d budget %d, want 4096/64", collector.Threshold(), collector.StepBudget())
	}
	thread, err := engine.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	proto := &bytecode.Proto{Source: "@collector-shell.lua", MaxStackSize: 2}
	protoHandle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	closureHandle, err := engine.NewClosure(proto, value.NilValue(), nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	liveRoot := mustIntern(t, engine.Strings, "collector-live-root")
	hiddenRoot := mustIntern(t, engine.Strings, "collector-hidden-root")
	externalRoot, err := engine.InternString("collector-external-root")
	if err != nil {
		t.Fatalf("intern external root: %v", err)
	}
	frame, err := thread.PushFrame(state.FrameSpec{
		Closure:       closureHandle.Value,
		Proto:         protoHandle.Value,
		RegisterBase:  0,
		RegisterCount: 2,
		Top:           1,
	})
	if err != nil {
		t.Fatalf("push frame: %v", err)
	}
	if err := thread.SetRegister(frame, 0, liveRoot.Value); err != nil {
		t.Fatalf("set live register: %v", err)
	}
	if err := thread.SetRegister(frame, 1, hiddenRoot.Value); err != nil {
		t.Fatalf("set hidden register: %v", err)
	}
	collector.BeginMarkPhase()
	if collector.Phase() != heap.GCPhaseMark {
		t.Fatalf("collector phase = %d, want mark", collector.Phase())
	}
	if err := collector.SeedRoots(engine.State); err != nil {
		t.Fatalf("seed roots: %v", err)
	}
	visited := make(map[value.HeapRef44]struct{})
	for _, ref := range collector.Heap().GrayQueueSnapshot() {
		visited[ref] = struct{}{}
	}
	for _, ref := range []value.HeapRef44{closureHandle.Ref, protoHandle.Ref, liveRoot.Ref, externalRoot.Ref} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing seeded root %#x", uint64(ref))
		}
	}
	if _, ok := visited[hiddenRoot.Ref]; ok {
		t.Fatalf("unexpected seeded hidden root %#x", uint64(hiddenRoot.Ref))
	}
	collector.SetPhase(heap.GCPhasePause)
	if collector.QueueLengths() != (heap.GCQueueLengths{}) {
		t.Fatalf("pause should clear collector queues, got %+v", collector.QueueLengths())
	}
}

func TestCollectorRunFullMarkMarksReachableGraphAndTransitionsToSweepStrings(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos})
	thread, err := engine.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	liveString, err := engine.Strings.Intern("phase1-batch1-live")
	if err != nil {
		t.Fatalf("intern live string: %v", err)
	}
	hiddenString, err := engine.Strings.Intern("phase1-batch1-hidden")
	if err != nil {
		t.Fatalf("intern hidden string: %v", err)
	}
	envHandle, err := engine.Tables.New(0, 1)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	if err := engine.Tables.Set(envHandle.Ref, value.NumberValue(1), liveString.Value); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	hiddenTable, err := engine.Tables.New(0, 0)
	if err != nil {
		t.Fatalf("new hidden table: %v", err)
	}
	proto := &bytecode.Proto{Source: "@phase1-batch1.lua", MaxStackSize: 1}
	protoHandle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	closureHandle, err := engine.Closures.NewLuaClosure(proto, envHandle.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := thread.PushFrame(state.FrameSpec{Closure: closureHandle.Value, Proto: protoHandle.Value, RegisterBase: 0, RegisterCount: 1, Top: 0}); err != nil {
		t.Fatalf("push frame: %v", err)
	}
	if err := collector.RunFullMark(engine.State); err != nil {
		t.Fatalf("run full mark: %v", err)
	}
	if collector.Phase() != heap.GCPhaseSweepStrings {
		t.Fatalf("collector phase = %d, want sweep strings", collector.Phase())
	}
	for _, ref := range []value.HeapRef44{closureHandle.Ref, protoHandle.Ref, envHandle.Ref, liveString.Ref} {
		if mark := markForRef(t, engine.Heap, ref); !mark.Has(value.MarkBlack) {
			t.Fatalf("reachable ref %#x mark = %#x, want black", uint64(ref), uint8(mark))
		}
	}
	deadWhite := otherWhite(engine.Heap.CurrentWhite())
	for _, ref := range []value.HeapRef44{hiddenString.Ref, hiddenTable.Ref} {
		if mark := markForRef(t, engine.Heap, ref); !mark.Has(deadWhite) || mark.Has(value.MarkBlack) {
			t.Fatalf("unreachable ref %#x mark = %#x, want current white only", uint64(ref), uint8(mark))
		}
	}
	if len(collector.WeakRefsSnapshot()) != 0 {
		t.Fatalf("unexpected weak refs after strong-only graph")
	}
	if queues := collector.QueueLengths(); queues.Gray != 0 || queues.GrayAgain != 0 {
		t.Fatalf("collector queues after batch1 = %+v, want empty gray queues", queues)
	}
}

func TestCollectorAtomicConsumesGrayAgainFromBarrier(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0xFACEB00C)
	tables := table.NewStore(runtimeHeap)
	vm := state.NewVMState(runtimeHeap)
	collector := NewCollector(runtimeHeap, nil, Config{})
	parent, err := tables.New(0, 0)
	if err != nil {
		t.Fatalf("new parent table: %v", err)
	}
	child, err := strings.Intern("atomic-grayagain-child")
	if err != nil {
		t.Fatalf("intern child string: %v", err)
	}
	if err := collector.StartCollection(); err != nil {
		t.Fatalf("start collection: %v", err)
	}
	if err := collector.SeedRoots(vm, Values(parent.Value)); err != nil {
		t.Fatalf("seed roots: %v", err)
	}
	if err := collector.Propagate(); err != nil {
		t.Fatalf("propagate roots: %v", err)
	}
	if mark := markForRef(t, runtimeHeap, parent.Ref); !mark.Has(value.MarkBlack) {
		t.Fatalf("parent mark after initial propagate = %#x, want black", uint8(mark))
	}
	if err := tables.Set(parent.Ref, value.NumberValue(1), child.Value); err != nil {
		t.Fatalf("mutate parent during marking: %v", err)
	}
	if queues := collector.QueueLengths(); queues.GrayAgain != 1 {
		t.Fatalf("queues after barrier = %+v, want grayAgain=1", queues)
	}
	if err := collector.RunAtomic(vm, Values(parent.Value)); err != nil {
		t.Fatalf("run atomic: %v", err)
	}
	if collector.Phase() != heap.GCPhaseSweepStrings {
		t.Fatalf("collector phase = %d, want sweep strings", collector.Phase())
	}
	if mark := markForRef(t, runtimeHeap, child.Ref); !mark.Has(value.MarkBlack) {
		t.Fatalf("child mark after atomic = %#x, want black", uint8(mark))
	}
	if queues := collector.QueueLengths(); queues.Gray != 0 || queues.GrayAgain != 0 {
		t.Fatalf("queues after atomic = %+v, want empty gray queues", queues)
	}
}

func TestCollectorClosureSetEnvRegraysBlackClosure(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos})
	env1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env1: %v", err)
	}
	proto := &bytecode.Proto{Source: "@closure-env-barrier.lua", MaxStackSize: 1}
	closureHandle, err := engine.NewClosure(proto, env1.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if err := collector.StartCollection(); err != nil {
		t.Fatalf("start collection: %v", err)
	}
	if err := collector.SeedRoots(engine.State); err != nil {
		t.Fatalf("seed roots: %v", err)
	}
	if err := collector.Propagate(); err != nil {
		t.Fatalf("propagate roots: %v", err)
	}
	if mark := markForRef(t, engine.Heap, closureHandle.Ref); !mark.Has(value.MarkBlack) {
		t.Fatalf("closure mark after propagate = %#x, want black", uint8(mark))
	}
	env2, err := engine.Tables.New(0, 0)
	if err != nil {
		t.Fatalf("new env2: %v", err)
	}
	if err := engine.Closures.SetEnv(closureHandle.Ref, env2.Value); err != nil {
		t.Fatalf("set closure env: %v", err)
	}
	if queues := collector.QueueLengths(); queues.GrayAgain != 1 || queues.Remembered != 1 {
		t.Fatalf("queues after closure env barrier = %+v, want grayAgain=1 remembered=1", queues)
	}
	if err := collector.RunAtomic(engine.State); err != nil {
		t.Fatalf("run atomic: %v", err)
	}
	if mark := markForRef(t, engine.Heap, env2.Ref); !mark.Has(value.MarkBlack) {
		t.Fatalf("env2 mark after atomic = %#x, want black", uint8(mark))
	}
	if env, err := engine.Closures.Env(closureHandle.Ref); err != nil {
		t.Fatalf("read closure env: %v", err)
	} else if env.Bits() != env2.Value.Bits() {
		t.Fatalf("closure env = %s, want %s", env, env2.Value)
	}
}

func TestCollectorWrapperSetEnvRegraysBlackWrapper(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos})
	env1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env1: %v", err)
	}
	wrapper, err := engine.RegisterHostObject("bag", map[string]float64{"x": 1}, env1.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	if err := collector.StartCollection(); err != nil {
		t.Fatalf("start collection: %v", err)
	}
	if err := collector.SeedRoots(engine.State); err != nil {
		t.Fatalf("seed roots: %v", err)
	}
	if err := collector.Propagate(); err != nil {
		t.Fatalf("propagate roots: %v", err)
	}
	if mark := markForRef(t, engine.Heap, wrapper.Ref); !mark.Has(value.MarkBlack) {
		t.Fatalf("wrapper mark after propagate = %#x, want black", uint8(mark))
	}
	env2, err := engine.Tables.New(0, 0)
	if err != nil {
		t.Fatalf("new env2: %v", err)
	}
	if _, err := engine.Hosts.SetWrapperEnv(wrapper.Ref, env2.Value); err != nil {
		t.Fatalf("set wrapper env: %v", err)
	}
	if queues := collector.QueueLengths(); queues.GrayAgain != 1 || queues.Remembered != 1 {
		t.Fatalf("queues after wrapper env barrier = %+v, want grayAgain=1 remembered=1", queues)
	}
	if err := collector.RunAtomic(engine.State); err != nil {
		t.Fatalf("run atomic: %v", err)
	}
	if mark := markForRef(t, engine.Heap, env2.Ref); !mark.Has(value.MarkBlack) {
		t.Fatalf("env2 mark after wrapper atomic = %#x, want black", uint8(mark))
	}
	updated, _, _, err := engine.Hosts.ReadHostObject(wrapper.Ref)
	if err != nil {
		t.Fatalf("read host wrapper: %v", err)
	}
	if updated.Env.Bits() != env2.Value.Bits() {
		t.Fatalf("wrapper env bits = %#x, want %#x", uint64(updated.Env.Bits()), uint64(env2.Value.Bits()))
	}
	if _, err := engine.Hosts.DescriptorVersion(host.Handle(updated.HostHandle)); err != nil {
		t.Fatalf("descriptor version after wrapper env update: %v", err)
	}
}

func TestCollectorConfiguredProtoStoreKeepsDetachedProtoAlive(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos})
	proto := &bytecode.Proto{Source: "@proto-store-root.lua", MaxStackSize: 1}
	handle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	if err := collector.RunFullMark(engine.State); err != nil {
		t.Fatalf("run full mark: %v", err)
	}
	if _, err := collector.RunSweepStrings(engine.Strings); err != nil {
		t.Fatalf("run sweep strings: %v", err)
	}
	if _, err := collector.RunSweepObjects(); err != nil {
		t.Fatalf("run sweep objects: %v", err)
	}
	if _, err := engine.Heap.DecodeHeapRef(handle.Ref); err != nil {
		t.Fatalf("configured proto store root should keep proto live: %v", err)
	}
	again, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("re-intern proto: %v", err)
	}
	if again.Ref != handle.Ref {
		t.Fatalf("re-interned proto ref = %#x, want %#x", uint64(again.Ref), uint64(handle.Ref))
	}
}

func TestCollectorSweepStringsRemovesDeadInternedEntriesAndTransitionsToSweepObjects(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos})
	thread, err := engine.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	liveString, err := engine.Strings.Intern("phase1-batch2-live")
	if err != nil {
		t.Fatalf("intern live string: %v", err)
	}
	deadString, err := engine.Strings.Intern("phase1-batch2-dead")
	if err != nil {
		t.Fatalf("intern dead string: %v", err)
	}
	envHandle, err := engine.Tables.New(0, 1)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	if err := engine.Tables.Set(envHandle.Ref, value.NumberValue(1), liveString.Value); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	proto := &bytecode.Proto{Source: "@phase1-batch2.lua", MaxStackSize: 1}
	protoHandle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	closureHandle, err := engine.Closures.NewLuaClosure(proto, envHandle.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := thread.PushFrame(state.FrameSpec{Closure: closureHandle.Value, Proto: protoHandle.Value, RegisterBase: 0, RegisterCount: 1, Top: 0}); err != nil {
		t.Fatalf("push frame: %v", err)
	}
	if err := collector.RunFullMark(engine.State); err != nil {
		t.Fatalf("run full mark: %v", err)
	}
	removed, err := collector.RunSweepStrings(engine.Strings)
	if err != nil {
		t.Fatalf("run sweep strings: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed strings = %d, want 1", removed)
	}
	if collector.Phase() != heap.GCPhaseSweepObjects {
		t.Fatalf("collector phase = %d, want sweep objects", collector.Phase())
	}
	if _, found, err := engine.Strings.Lookup("phase1-batch2-dead"); err != nil {
		t.Fatalf("lookup dead string: %v", err)
	} else if found {
		t.Fatalf("dead string should have been removed from intern table")
	}
	if handle, found, err := engine.Strings.Lookup("phase1-batch2-live"); err != nil {
		t.Fatalf("lookup live string: %v", err)
	} else if !found || handle.Ref != liveString.Ref {
		t.Fatalf("live string lookup mismatch: found=%v ref=%#x want %#x", found, uint64(handle.Ref), uint64(liveString.Ref))
	}
	deadWhite := otherWhite(engine.Heap.CurrentWhite())
	if mark := markForRef(t, engine.Heap, deadString.Ref); !mark.Has(deadWhite) || mark.Has(value.MarkBlack) {
		t.Fatalf("dead string mark after sweep-strings = %#x, want current white and not black", uint8(mark))
	}
	if mark := markForRef(t, engine.Heap, liveString.Ref); !mark.Has(value.MarkBlack) {
		t.Fatalf("live string mark after sweep-strings = %#x, want black", uint8(mark))
	}
}

func TestCollectorAssistAllocationAdvancesIncrementalStateMachine(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos, VM: engine.State, Strings: engine.Strings})
	collector.SetThreshold(32)
	collector.SetStepBudget(16)
	if engine.Heap.NextGCTrigger() == 0 {
		t.Fatalf("next gc trigger should be initialized")
	}
	root, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new root table: %v", err)
	}
	handles := []value.HeapRef44{root.Ref}
	for index := 0; index < 4; index++ {
		tableHandle, err := engine.NewTable(1, 1)
		if err != nil {
			t.Fatalf("new prebuilt table %d: %v", index, err)
		}
		handles = append(handles, tableHandle.Ref)
	}
	engine.SetAllocationAssistant(collector)
	seenMark := false
	seenAtomic := false
	seenSweepStrings := false
	seenSweepObjects := false
	started := false
	for index := 0; index < 64; index++ {
		handle, err := engine.InternString(fmt.Sprintf("phase2-batch1-step-%d", index))
		if err != nil {
			t.Fatalf("intern step %d: %v", index, err)
		}
		handles = append(handles, handle.Ref)
		switch collector.Phase() {
		case heap.GCPhaseMark:
			seenMark = true
			started = true
		case heap.GCPhaseAtomic:
			seenAtomic = true
			started = true
		case heap.GCPhaseSweepStrings:
			seenSweepStrings = true
			started = true
		case heap.GCPhaseSweepObjects:
			seenSweepObjects = true
			started = true
		}
		if started && collector.Phase() == heap.GCPhasePause {
			break
		}
	}
	if !started {
		t.Fatalf("incremental assist never started a collection cycle")
	}
	if collector.Phase() != heap.GCPhasePause {
		t.Fatalf("collector phase after assist cycle = %d, want pause", collector.Phase())
	}
	if !seenMark || !seenAtomic || !seenSweepStrings || !seenSweepObjects {
		t.Fatalf("incremental assist phases seen = mark:%v atomic:%v sweepStrings:%v sweepObjects:%v", seenMark, seenAtomic, seenSweepStrings, seenSweepObjects)
	}
	if collector.AllocationDebt() != 0 {
		t.Fatalf("allocation debt after cycle = %d, want 0", collector.AllocationDebt())
	}
	if engine.Heap.NextGCTrigger() <= engine.Heap.LiveBytes() {
		t.Fatalf("next gc trigger = %d, want > live bytes %d", engine.Heap.NextGCTrigger(), engine.Heap.LiveBytes())
	}
	for _, ref := range handles {
		if _, err := engine.Heap.DecodeHeapRef(ref); err != nil {
			t.Fatalf("live ref %#x should survive incremental assist cycle: %v", uint64(ref), err)
		}
	}
}

func TestCollectorAssistSafepointStartsIncrementalCycle(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos, VM: engine.State, Strings: engine.Strings})
	collector.SetThreshold(1)
	collector.SetStepBudget(16)
	if _, err := engine.InternString("phase2-safepoint-trigger"); err != nil {
		t.Fatalf("intern trigger string: %v", err)
	}
	if !engine.Heap.GCTargetReached() {
		t.Fatalf("gc target should be reached before safepoint assist")
	}
	if err := collector.AssistSafepoint(); err != nil {
		t.Fatalf("assist safepoint: %v", err)
	}
	if collector.Phase() != heap.GCPhaseMark {
		t.Fatalf("collector phase after safepoint assist = %d, want mark", collector.Phase())
	}
}

func TestCollectorRecordWeakRefDedupsEdges(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	collector := NewCollector(runtimeHeap, nil, Config{})
	edge := WeakRef{Kind: WeakRefWeakTableValue, Owner: 0x10, Target: 0x20, Slot: 3}
	if err := collector.recordWeakRef(edge); err != nil {
		t.Fatalf("record weak ref: %v", err)
	}
	if err := collector.recordWeakRef(edge); err != nil {
		t.Fatalf("record duplicate weak ref: %v", err)
	}
	if snapshot := collector.WeakRefsSnapshot(); len(snapshot) != 1 {
		t.Fatalf("weak refs snapshot len = %d, want 1", len(snapshot))
	}
}

func TestCollectorStepOncePreparesCollectionIncrementally(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos, VM: engine.State, Strings: engine.Strings})
	collector.SetStepBudget(16)
	for index := 0; index < 4; index++ {
		if _, err := engine.NewTable(1, 1); err != nil {
			t.Fatalf("new table %d: %v", index, err)
		}
	}
	if err := collector.stepOnce(); err != nil {
		t.Fatalf("first prepare step: %v", err)
	}
	if collector.Phase() != heap.GCPhasePause {
		t.Fatalf("collector phase after first prepare step = %d, want pause", collector.Phase())
	}
	if !collector.preparing {
		t.Fatalf("collector should remain in incremental prepare state after first step")
	}
	if queues := collector.QueueLengths(); queues != (heap.GCQueueLengths{}) {
		t.Fatalf("queues during prepare = %+v, want empty", queues)
	}
	for attempts := 0; attempts < 32 && collector.Phase() == heap.GCPhasePause; attempts++ {
		if err := collector.stepOnce(); err != nil {
			t.Fatalf("prepare continuation %d: %v", attempts, err)
		}
	}
	if collector.Phase() != heap.GCPhaseMark {
		t.Fatalf("collector phase after prepare completes = %d, want mark", collector.Phase())
	}
	if queues := collector.QueueLengths(); queues.Gray == 0 {
		t.Fatalf("expected seeded gray queue after prepare completes, got %+v", queues)
	}
}

func TestCollectorRunSweepObjectsFinishesPartialIncrementalSweep(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos, VM: engine.State, Strings: engine.Strings})
	collector.SetStepBudget(16)
	deadTables := make([]table.Handle, 0, 3)
	for index := 0; index < 3; index++ {
		handle, err := engine.Tables.New(1, 1)
		if err != nil {
			t.Fatalf("new dead table %d: %v", index, err)
		}
		deadTables = append(deadTables, handle)
	}
	if err := collector.RunFullMark(engine.State); err != nil {
		t.Fatalf("run full mark: %v", err)
	}
	if _, err := collector.RunSweepStrings(engine.Strings); err != nil {
		t.Fatalf("run sweep strings: %v", err)
	}
	if err := collector.stepOnce(); err != nil {
		t.Fatalf("incremental sweep step: %v", err)
	}
	if collector.Phase() != heap.GCPhaseSweepObjects {
		t.Fatalf("collector phase after partial sweep step = %d, want sweep objects", collector.Phase())
	}
	if !collector.sweeping {
		t.Fatalf("collector should remain in sweeping state after partial sweep step")
	}
	stats, err := collector.RunSweepObjects()
	if err != nil {
		t.Fatalf("finish partial sweep: %v", err)
	}
	if collector.Phase() != heap.GCPhasePause {
		t.Fatalf("collector phase after finishing sweep = %d, want pause", collector.Phase())
	}
	if stats.FreedObjects != len(deadTables) {
		t.Fatalf("freed objects after resumed sweep = %d, want %d", stats.FreedObjects, len(deadTables))
	}
	if stats.FreedPayloads != len(deadTables)*3 {
		t.Fatalf("freed payloads after resumed sweep = %d, want %d", stats.FreedPayloads, len(deadTables)*3)
	}
	for _, handle := range deadTables {
		if address, err := engine.Heap.DecodeHeapRef(handle.Ref); err == nil {
			if err := engine.Heap.ValidateObjectAddress(address); err == nil {
				t.Fatalf("dead table %#x should not validate after resumed sweep", uint64(handle.Ref))
			}
		}
	}
}

func TestCollectorWeakFeedbackMutationDuringMarkIsScrubbed(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos})
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@batch2-feedback.lua",
		MaxStackSize: 1,
		Constants:    []bytecode.Constant{bytecode.StringConstant("dead-key")},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	layout := feedback.LayoutForProto(proto)
	if _, err := engine.Closures.EnsureFeedbackVector(closureHandle.Ref, layout); err != nil {
		t.Fatalf("ensure feedback vector: %v", err)
	}
	if err := collector.StartCollection(); err != nil {
		t.Fatalf("start collection: %v", err)
	}
	if err := collector.SeedRoots(engine.State); err != nil {
		t.Fatalf("seed roots: %v", err)
	}
	if err := collector.Propagate(); err != nil {
		t.Fatalf("propagate roots: %v", err)
	}
	deadKey, err := engine.Strings.Intern("batch2-feedback-dead-key")
	if err != nil {
		t.Fatalf("intern dead key: %v", err)
	}
	deadTarget, err := engine.Tables.New(0, 0)
	if err != nil {
		t.Fatalf("new dead target: %v", err)
	}
	cell := feedback.NewTableMonomorphicCell(feedback.SlotGetGlobal, feedback.AccessHash, deadTarget.Ref, 7, 3, deadKey.Value.Bits())
	if err := engine.Closures.WriteFeedbackCell(closureHandle.Ref, 0, cell); err != nil {
		t.Fatalf("write feedback cell: %v", err)
	}
	if queues := collector.QueueLengths(); queues.Weak != 1 {
		t.Fatalf("queues after feedback mutation = %+v, want weak=1", queues)
	}
	if err := collector.RunAtomic(engine.State); err != nil {
		t.Fatalf("run atomic: %v", err)
	}
	if _, err := collector.RunSweepStrings(engine.Strings); err != nil {
		t.Fatalf("run sweep strings: %v", err)
	}
	stats, err := collector.RunSweepObjects()
	if err != nil {
		t.Fatalf("run sweep objects: %v", err)
	}
	if stats.ScrubbedFeedbackCells != 1 {
		t.Fatalf("scrubbed feedback cells = %d, want 1", stats.ScrubbedFeedbackCells)
	}
	updated, err := engine.Closures.ReadFeedbackCell(closureHandle.Ref, 0)
	if err != nil {
		t.Fatalf("read feedback cell after scrub: %v", err)
	}
	if updated.State != feedback.StateGeneric || updated.HeapRef != 0 || updated.ValueBits != 0 {
		t.Fatalf("scrubbed feedback cell = %+v, want generic zeroed", updated)
	}
}

func TestCollectorWeakTableMutationDuringMarkIsScrubbed(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos})
	weakTable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new weak table: %v", err)
	}
	setWeakValuesFlag(t, engine.Heap, weakTable.Ref)
	if err := collector.StartCollection(); err != nil {
		t.Fatalf("start collection: %v", err)
	}
	if err := collector.SeedRoots(engine.State); err != nil {
		t.Fatalf("seed roots: %v", err)
	}
	if err := collector.Propagate(); err != nil {
		t.Fatalf("propagate roots: %v", err)
	}
	deadValue, err := engine.Tables.New(0, 0)
	if err != nil {
		t.Fatalf("new dead value: %v", err)
	}
	if err := engine.Tables.Set(weakTable.Ref, value.NumberValue(1), deadValue.Value); err != nil {
		t.Fatalf("set weak table value: %v", err)
	}
	if queues := collector.QueueLengths(); queues.Weak != 1 {
		t.Fatalf("queues after weak table mutation = %+v, want weak=1", queues)
	}
	if err := collector.RunAtomic(engine.State); err != nil {
		t.Fatalf("run atomic: %v", err)
	}
	if _, err := collector.RunSweepStrings(engine.Strings); err != nil {
		t.Fatalf("run sweep strings: %v", err)
	}
	stats, err := collector.RunSweepObjects()
	if err != nil {
		t.Fatalf("run sweep objects: %v", err)
	}
	if stats.ClearedWeakTableEdges != 1 {
		t.Fatalf("cleared weak table edges = %d, want 1", stats.ClearedWeakTableEdges)
	}
	if got, found, err := engine.Tables.Get(weakTable.Ref, value.NumberValue(1)); err != nil {
		t.Fatalf("get weak table value after scrub: %v", err)
	} else if found || !got.IsBoxedTag(value.TagNil) {
		t.Fatalf("weak table value after scrub = %v found=%v, want nil/false", got, found)
	}
}

func TestCollectorSweepObjectsCompletesCycleAndReusesDeadTableSpans(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos})
	deadTable, err := engine.Tables.New(1, 1)
	if err != nil {
		t.Fatalf("new dead table: %v", err)
	}
	deadOffset := mustOffsetForRef(t, engine.Heap, deadTable.Ref)
	deadObject, err := engine.Tables.Object(deadTable.Ref)
	if err != nil {
		t.Fatalf("read dead table object: %v", err)
	}
	oldWhite := engine.Heap.CurrentWhite()
	if err := collector.RunFullMark(engine.State); err != nil {
		t.Fatalf("run full mark: %v", err)
	}
	if _, err := collector.RunSweepStrings(engine.Strings); err != nil {
		t.Fatalf("run sweep strings: %v", err)
	}
	stats, err := collector.RunSweepObjects()
	if err != nil {
		t.Fatalf("run sweep objects: %v", err)
	}
	if collector.Phase() != heap.GCPhasePause {
		t.Fatalf("collector phase = %d, want pause", collector.Phase())
	}
	if engine.Heap.CurrentWhite() == oldWhite {
		t.Fatalf("current white did not flip after full cycle")
	}
	if stats.FreedObjects != 1 {
		t.Fatalf("freed objects = %d, want 1", stats.FreedObjects)
	}
	if stats.FreedPayloads != 3 {
		t.Fatalf("freed payloads = %d, want 3", stats.FreedPayloads)
	}
	for _, offset := range []value.HeapOff64{deadOffset, deadObject.ArrayData, deadObject.CtrlData, deadObject.EntriesData} {
		meta, err := engine.Heap.SpanMetadata(offset)
		if err != nil {
			t.Fatalf("span metadata %#x: %v", uint64(offset), err)
		}
		if meta.State != heap.SpanStateFree {
			t.Fatalf("span %#x state = %d, want free", uint64(offset), meta.State)
		}
	}
	if address, err := engine.Heap.DecodeHeapRef(deadTable.Ref); err == nil {
		if err := engine.Heap.ValidateObjectAddress(address); err == nil {
			t.Fatalf("dead table address should not validate after sweep")
		}
	}
	reused, err := engine.Tables.New(1, 1)
	if err != nil {
		t.Fatalf("new reused table: %v", err)
	}
	reusedOffset := mustOffsetForRef(t, engine.Heap, reused.Ref)
	if reusedOffset != deadOffset {
		t.Fatalf("reused table offset = %#x, want %#x", uint64(reusedOffset), uint64(deadOffset))
	}
}

func TestCollectorSweepObjectsScrubsWeakFeedbackAndWeakTableEntries(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos})
	thread, err := engine.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.Tables.New(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@phase1-batch3.lua",
		MaxStackSize: 1,
		Constants:    []bytecode.Constant{bytecode.StringConstant("dead-key")},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	closureHandle, err := engine.Closures.NewLuaClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := thread.PushFrame(state.FrameSpec{Closure: closureHandle.Value, Proto: value.ProtoRefValue(mustProtoRef(t, engine, proto)), RegisterBase: 0, RegisterCount: 1, Top: 0}); err != nil {
		t.Fatalf("push frame: %v", err)
	}
	layout := feedback.LayoutForProto(proto)
	if _, err := engine.Closures.EnsureFeedbackVector(closureHandle.Ref, layout); err != nil {
		t.Fatalf("ensure feedback vector: %v", err)
	}
	deadKey, err := engine.Strings.Intern("dead-key")
	if err != nil {
		t.Fatalf("intern dead key: %v", err)
	}
	deadTarget, err := engine.Tables.New(0, 0)
	if err != nil {
		t.Fatalf("new dead target: %v", err)
	}
	cell := feedback.NewTableMonomorphicCell(feedback.SlotGetGlobal, feedback.AccessHash, deadTarget.Ref, 7, 3, deadKey.Value.Bits())
	if err := engine.Closures.WriteFeedbackCell(closureHandle.Ref, 0, cell); err != nil {
		t.Fatalf("write feedback cell: %v", err)
	}
	weakTable, err := engine.Tables.New(0, 0)
	if err != nil {
		t.Fatalf("new weak table: %v", err)
	}
	if err := engine.Tables.Set(weakTable.Ref, value.NumberValue(1), deadTarget.Value); err != nil {
		t.Fatalf("seed weak table: %v", err)
	}
	setWeakValuesFlag(t, engine.Heap, weakTable.Ref)
	if err := collector.RunFullMark(engine.State, Values(weakTable.Value)); err != nil {
		t.Fatalf("run full mark: %v", err)
	}
	if _, err := collector.RunSweepStrings(engine.Strings); err != nil {
		t.Fatalf("run sweep strings: %v", err)
	}
	stats, err := collector.RunSweepObjects()
	if err != nil {
		t.Fatalf("run sweep objects: %v", err)
	}
	if stats.ScrubbedFeedbackCells != 1 {
		t.Fatalf("scrubbed feedback cells = %d, want 1", stats.ScrubbedFeedbackCells)
	}
	if stats.ClearedWeakTableEdges != 1 {
		t.Fatalf("cleared weak table edges = %d, want 1", stats.ClearedWeakTableEdges)
	}
	updated, err := engine.Closures.ReadFeedbackCell(closureHandle.Ref, 0)
	if err != nil {
		t.Fatalf("read feedback cell after scrub: %v", err)
	}
	if updated.State != feedback.StateGeneric || updated.HeapRef != 0 || updated.ValueBits != 0 {
		t.Fatalf("scrubbed feedback cell = %+v, want generic zeroed", updated)
	}
	if got, found, err := engine.Tables.Get(weakTable.Ref, value.NumberValue(1)); err != nil {
		t.Fatalf("weak table get after scrub: %v", err)
	} else if found || !got.IsBoxedTag(value.TagNil) {
		t.Fatalf("weak table value after scrub = %v found=%v, want nil/false", got, found)
	}
	if _, found, err := engine.Strings.Lookup("dead-key"); err != nil {
		t.Fatalf("lookup dead key after sweep: %v", err)
	} else if found {
		t.Fatalf("dead feedback key should have been removed from intern table")
	}
}

func TestCollectorSweepObjectsFinalizesDeadHostWrapper(t *testing.T) {
	engine := interp.New()
	collector := NewCollector(engine.Heap, engine.Hosts, Config{ProtoStore: engine.Protos})
	handle, err := engine.Hosts.RegisterObject("bag", map[string]float64{"x": 1})
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	wrapper, err := engine.Hosts.WrapObject(handle, value.NilValue())
	if err != nil {
		t.Fatalf("wrap host object: %v", err)
	}
	if err := collector.RunFullMark(engine.State); err != nil {
		t.Fatalf("run full mark: %v", err)
	}
	if _, err := collector.RunSweepStrings(engine.Strings); err != nil {
		t.Fatalf("run sweep strings: %v", err)
	}
	stats, err := collector.RunSweepObjects()
	if err != nil {
		t.Fatalf("run sweep objects: %v", err)
	}
	if stats.FinalizedHostWrappers != 1 {
		t.Fatalf("finalized host wrappers = %d, want 1", stats.FinalizedHostWrappers)
	}
	if _, err := engine.Hosts.DescriptorVersion(handle); err != nil {
		t.Fatalf("registry handle should still exist after wrapper finalize: %v", err)
	}
	if err := engine.Hosts.Release(handle); err != nil {
		t.Fatalf("release remaining host handle: %v", err)
	}
	if _, err := engine.Hosts.DescriptorVersion(handle); err == nil {
		t.Fatalf("released host handle should be gone from registry")
	}
	if address, err := engine.Heap.DecodeHeapRef(wrapper.Ref); err == nil {
		if err := engine.Heap.ValidateObjectAddress(address); err == nil {
			t.Fatalf("dead wrapper should not validate after sweep")
		}
	}
}

func markForRef(t *testing.T, runtimeHeap *heap.Heap, ref value.HeapRef44) value.MarkBits {
	t.Helper()
	address, err := runtimeHeap.DecodeHeapRef(ref)
	if err != nil {
		t.Fatalf("decode ref %#x: %v", uint64(ref), err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("offset for ref %#x: %v", uint64(ref), err)
	}
	header, err := runtimeHeap.HeaderAtOffset(offset)
	if err != nil {
		t.Fatalf("header for ref %#x: %v", uint64(ref), err)
	}
	return header.Mark
}

func mustOffsetForRef(t *testing.T, runtimeHeap *heap.Heap, ref value.HeapRef44) value.HeapOff64 {
	t.Helper()
	address, err := runtimeHeap.DecodeHeapRef(ref)
	if err != nil {
		t.Fatalf("decode ref %#x: %v", uint64(ref), err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("offset for ref %#x: %v", uint64(ref), err)
	}
	return offset
}

func setWeakValuesFlag(t *testing.T, runtimeHeap *heap.Heap, ref value.HeapRef44) {
	t.Helper()
	address, err := runtimeHeap.DecodeHeapRef(ref)
	if err != nil {
		t.Fatalf("decode weak table ref: %v", err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("weak table offset: %v", err)
	}
	bytes, err := runtimeHeap.Resolve(offset, table.ObjectSize)
	if err != nil {
		t.Fatalf("resolve weak table bytes: %v", err)
	}
	object, err := table.ReadObject(bytes)
	if err != nil {
		t.Fatalf("read weak table object: %v", err)
	}
	object.Flags = object.Flags.With(table.FlagWeakValues)
	if err := table.WriteObject(bytes, object); err != nil {
		t.Fatalf("write weak table object: %v", err)
	}
}

func mustProtoRef(t *testing.T, engine *interp.Engine, proto *bytecode.Proto) value.HeapRef44 {
	t.Helper()
	handle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	return handle.Ref
}

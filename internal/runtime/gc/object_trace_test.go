package gc

import (
	"os"
	"strings"
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/feedback"
	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/baseline"
)

func TestTracerProtoTracesConstAreaAndChildProtoRefs(t *testing.T) {
	engine := interp.New()
	tracer := NewTracer(engine.Heap, engine.Hosts)
	child := &bytecode.Proto{Source: "@child.lua", MaxStackSize: 1}
	parent := &bytecode.Proto{
		Source:       "@parent.lua",
		MaxStackSize: 2,
		Constants:    []bytecode.Constant{bytecode.StringConstant("const-root")},
		Protos:       []*bytecode.Proto{child},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_CLOSURE, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	constRoot, err := engine.Strings.Intern("const-root")
	if err != nil {
		t.Fatalf("intern const string: %v", err)
	}
	handle, err := engine.Protos.Intern(parent)
	if err != nil {
		t.Fatalf("intern parent proto: %v", err)
	}
	if _, err := engine.Protos.ConstantBase(parent, engine.Strings); err != nil {
		t.Fatalf("ensure constant base: %v", err)
	}
	visited, weak := collectObjectEdges(t, tracer, handle.Ref)
	childHandle, err := engine.Protos.Intern(child)
	if err != nil {
		t.Fatalf("intern child proto: %v", err)
	}
	for _, ref := range []value.HeapRef44{constRoot.Ref, childHandle.Ref} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing proto strong edge %#x", uint64(ref))
		}
	}
	if len(weak) != 0 {
		t.Fatalf("proto should not emit weak refs, got %+v", weak)
	}
}

func TestTracerClosureTreatsFeedbackCellsAsWeakAndScrubsDeadTargets(t *testing.T) {
	engine := interp.New()
	tracer := NewTracer(engine.Heap, engine.Hosts)
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	thread, err := engine.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	address, err := thread.SlotAddress(0)
	if err != nil {
		t.Fatalf("slot address: %v", err)
	}
	upvalueRoot, err := engine.Strings.Intern("upvalue-root")
	if err != nil {
		t.Fatalf("intern upvalue root: %v", err)
	}
	if err := thread.SetValueAtAddress(address, upvalueRoot.Value); err != nil {
		t.Fatalf("seed upvalue slot: %v", err)
	}
	upvalueHandle, err := engine.Upvalues.FindOrCreateOpen(thread, address)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@feedback.lua",
		NumUpvalues:  1,
		MaxStackSize: 2,
		Constants:    []bytecode.Constant{bytecode.StringConstant("key")},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, []value.HeapRef44{upvalueHandle.Ref})
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	layout := feedback.LayoutForProto(proto)
	if _, err := engine.Closures.EnsureFeedbackVector(closureHandle.Ref, layout); err != nil {
		t.Fatalf("ensure feedback vector: %v", err)
	}
	keyRoot, err := engine.Strings.Intern("feedback-key")
	if err != nil {
		t.Fatalf("intern feedback key: %v", err)
	}
	tableRoot, err := engine.NewTable(0, 1)
	if err != nil {
		t.Fatalf("new table root: %v", err)
	}
	cell := feedback.NewTableMonomorphicCell(feedback.SlotGetGlobal, feedback.AccessHash, tableRoot.Ref, 7, 3, keyRoot.Value.Bits())
	if err := engine.Closures.WriteFeedbackCell(closureHandle.Ref, 0, cell); err != nil {
		t.Fatalf("write feedback cell: %v", err)
	}
	strong, weak := collectObjectEdges(t, tracer, closureHandle.Ref)
	for _, ref := range []value.HeapRef44{env.Ref, upvalueHandle.Ref} {
		if _, ok := strong[ref]; !ok {
			t.Fatalf("missing closure strong edge %#x", uint64(ref))
		}
	}
	for _, ref := range []value.HeapRef44{tableRoot.Ref, keyRoot.Ref} {
		if _, ok := strong[ref]; ok {
			t.Fatalf("feedback cache ref %#x should not be strong", uint64(ref))
		}
	}
	if !hasWeakRef(weak, WeakRefFeedbackCellHeapRef, closureHandle.Ref, tableRoot.Ref, 0) {
		t.Fatalf("missing weak feedback heap ref edge: %+v", weak)
	}
	if !hasWeakRef(weak, WeakRefFeedbackCellValueBits, closureHandle.Ref, keyRoot.Ref, 0) {
		t.Fatalf("missing weak feedback value-bits edge: %+v", weak)
	}
	scrubbed, err := tracer.ScrubDeadFeedbackCells(closureHandle.Ref, func(ref value.HeapRef44) bool {
		return ref == tableRoot.Ref || ref == keyRoot.Ref
	})
	if err != nil {
		t.Fatalf("scrub feedback cells: %v", err)
	}
	if scrubbed != 1 {
		t.Fatalf("scrubbed cells = %d, want 1", scrubbed)
	}
	updated, err := engine.Closures.ReadFeedbackCell(closureHandle.Ref, 0)
	if err != nil {
		t.Fatalf("read scrubbed feedback cell: %v", err)
	}
	if updated.State != feedback.StateGeneric || updated.HeapRef != 0 || updated.ValueBits != 0 {
		t.Fatalf("scrubbed feedback cell = %+v, want generic zeroed cell", updated)
	}
}

func TestTracerClosureScrubsPolymorphicFeedbackEntriesIndividually(t *testing.T) {
	engine := interp.New()
	tracer := NewTracer(engine.Heap, engine.Hosts)
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@poly-feedback.lua",
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 0, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := engine.Closures.EnsureFeedbackVector(closureHandle.Ref, feedback.LayoutForProto(proto)); err != nil {
		t.Fatalf("ensure feedback vector: %v", err)
	}
	receiver1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new receiver1: %v", err)
	}
	receiver2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new receiver2: %v", err)
	}
	target1, err := engine.NewClosure(&bytecode.Proto{Source: "@poly-target-1.lua", MaxStackSize: 1, Constants: []bytecode.Constant{bytecode.NumberConstant(41)}, Code: []bytecode.Instruction{bytecode.CreateABx(bytecode.OP_LOADK, 0, 0), bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0)}}, env.Value, nil)
	if err != nil {
		t.Fatalf("new target1: %v", err)
	}
	target2, err := engine.NewClosure(&bytecode.Proto{Source: "@poly-target-2.lua", MaxStackSize: 1, Constants: []bytecode.Constant{bytecode.NumberConstant(42)}, Code: []bytecode.Instruction{bytecode.CreateABx(bytecode.OP_LOADK, 0, 0), bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0)}}, env.Value, nil)
	if err != nil {
		t.Fatalf("new target2: %v", err)
	}
	payload, err := engine.Heap.AllocPayload(feedback.CallPolymorphicDataSize, heap.PayloadLayoutOpaque, 0)
	if err != nil {
		t.Fatalf("alloc call polymorphic payload: %v", err)
	}
	entries := [feedback.CallPolymorphicEntryCount]feedback.CallPolymorphicEntry{
		feedback.NewCallPolymorphicEntry(feedback.AccessCallResolvedLuaClosure, target1.Ref, receiver1.Value.Bits(), feedback.CallShape{Kind: feedback.CallShapeTableMetatable, VersionA: 7, VersionB: 11}),
		feedback.NewCallPolymorphicEntry(feedback.AccessCallResolvedLuaClosure, target2.Ref, receiver2.Value.Bits(), feedback.CallShape{Kind: feedback.CallShapeTableMetatable, VersionA: 8, VersionB: 12}),
	}
	if err := feedback.WriteCallPolymorphicEntries(payload.Bytes, entries); err != nil {
		t.Fatalf("write call polymorphic entries: %v", err)
	}
	if err := engine.Closures.WriteFeedbackCell(closureHandle.Ref, 0, feedback.NewCallPolymorphicCell(feedback.SlotCall, payload.Offset)); err != nil {
		t.Fatalf("write polymorphic feedback cell: %v", err)
	}
	strong, weak := collectObjectEdges(t, tracer, closureHandle.Ref)
	for _, ref := range []value.HeapRef44{receiver1.Ref, receiver2.Ref, target1.Ref, target2.Ref} {
		if _, ok := strong[ref]; ok {
			t.Fatalf("polymorphic feedback ref %#x should not be strong", uint64(ref))
		}
	}
	for _, ref := range []value.HeapRef44{target1.Ref, target2.Ref} {
		if !hasWeakRef(weak, WeakRefFeedbackCellHeapRef, closureHandle.Ref, ref, 0) {
			t.Fatalf("missing polymorphic weak heap edge for %#x: %+v", uint64(ref), weak)
		}
	}
	for _, ref := range []value.HeapRef44{receiver1.Ref, receiver2.Ref} {
		if !hasWeakRef(weak, WeakRefFeedbackCellValueBits, closureHandle.Ref, ref, 0) {
			t.Fatalf("missing polymorphic weak value edge for %#x: %+v", uint64(ref), weak)
		}
	}
	scrubbed, err := tracer.ScrubDeadFeedbackCells(closureHandle.Ref, func(ref value.HeapRef44) bool {
		return ref == target1.Ref || ref == receiver1.Ref
	})
	if err != nil {
		t.Fatalf("scrub polymorphic feedback cells: %v", err)
	}
	if scrubbed != 1 {
		t.Fatalf("scrubbed polymorphic cells = %d, want 1", scrubbed)
	}
	updated, err := engine.Closures.ReadFeedbackCell(closureHandle.Ref, 0)
	if err != nil {
		t.Fatalf("read scrubbed polymorphic feedback cell: %v", err)
	}
	if updated.State != feedback.StateMonomorphic || updated.AccessKind != feedback.AccessCallResolvedLuaClosure || updated.TargetRef() != target2.Ref || updated.ValueBits != receiver2.Value.Bits() {
		t.Fatalf("scrubbed polymorphic feedback cell = %+v, want receiver2/target2 monomorphic", updated)
	}
	metadata, err := engine.Heap.SpanMetadata(payload.Offset)
	if err != nil {
		t.Fatalf("read polymorphic payload metadata: %v", err)
	}
	if metadata.State != heap.SpanStateFree {
		t.Fatalf("polymorphic payload state = %d, want free", metadata.State)
	}
}

func TestTracerPolymorphicFeedbackSourceAudit(t *testing.T) {
	source, err := os.ReadFile("object_trace.go")
	if err != nil {
		t.Fatalf("read object_trace.go: %v", err)
	}
	text := string(source)
	for _, needle := range []string{
		"callPolymorphicEntryDead(",
		"visitWeak(WeakRef{Kind: WeakRefFeedbackCellHeapRef, Owner: closureRef, Target: entry.TargetRef, Slot: slot})",
		"visitWeakTValue(value.FromRaw(entry.ValueBits), WeakRef{Kind: WeakRefFeedbackCellValueBits, Owner: closureRef, Slot: slot}, visitWeak)",
		"live[0].MonomorphicCell(cell.SlotKind)",
		"feedback.NewGenericCell(cell.SlotKind)",
		"tracer.heap.FreeSpan(offset)",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("polymorphic feedback GC contract should retain %q", needle)
		}
	}
}

func TestTracerClosedUpvalueTracesClosedValue(t *testing.T) {
	engine := interp.New()
	tracer := NewTracer(engine.Heap, engine.Hosts)
	thread, err := engine.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	root, err := engine.Strings.Intern("closed-upvalue-root")
	if err != nil {
		t.Fatalf("intern root: %v", err)
	}
	address, err := thread.SlotAddress(0)
	if err != nil {
		t.Fatalf("slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(address, root.Value); err != nil {
		t.Fatalf("set slot: %v", err)
	}
	opened, err := engine.Upvalues.FindOrCreateOpen(thread, address)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}
	closed, err := engine.Upvalues.CloseAtOrAbove(thread, address)
	if err != nil {
		t.Fatalf("close upvalue: %v", err)
	}
	if len(closed) != 1 || closed[0].Ref != opened.Ref {
		t.Fatalf("closed upvalues = %+v, want ref %#x", closed, uint64(opened.Ref))
	}
	strong, weak := collectObjectEdges(t, tracer, opened.Ref)
	if _, ok := strong[root.Ref]; !ok {
		t.Fatalf("missing closed upvalue strong edge %#x", uint64(root.Ref))
	}
	if len(weak) != 0 {
		t.Fatalf("closed upvalue should not emit weak refs, got %+v", weak)
	}
}

func TestCompiledMetadataRootsCarryMetadataHeapRefs(t *testing.T) {
	engine := interp.New()
	runtime := baseline.NewRuntime(engine)
	proto := &bytecode.Proto{
		Source:       "@compiled-roots-extra.lua",
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	handle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	extra, err := engine.Strings.Intern("compiled-metadata-root")
	if err != nil {
		t.Fatalf("intern metadata root: %v", err)
	}
	compiled, err := runtime.CompileRef(handle.Ref)
	if err != nil {
		t.Fatalf("compile proto: %v", err)
	}
	compiled.Metadata.AddHeapRef(extra.Ref)
	visited := collectRoots(t, func(visit VisitFunc) error {
		return CompiledMetadataRoots(runtime).WalkRoots(visit)
	})
	for _, ref := range []value.HeapRef44{handle.Ref, extra.Ref} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing compiled metadata root %#x", uint64(ref))
		}
	}
}

func collectObjectEdges(t *testing.T, tracer *Tracer, ref value.HeapRef44) (map[value.HeapRef44]struct{}, []WeakRef) {
	t.Helper()
	strong := make(map[value.HeapRef44]struct{})
	weak := make([]WeakRef, 0)
	if err := tracer.TraceObject(ref, func(edge value.HeapRef44) error {
		strong[edge] = struct{}{}
		return nil
	}, func(edge WeakRef) error {
		weak = append(weak, edge)
		return nil
	}); err != nil {
		t.Fatalf("trace object %#x: %v", uint64(ref), err)
	}
	return strong, weak
}

func hasWeakRef(edges []WeakRef, kind WeakRefKind, owner value.HeapRef44, target value.HeapRef44, slot uint32) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.Owner == owner && edge.Target == target && edge.Slot == slot {
			return true
		}
	}
	return false
}

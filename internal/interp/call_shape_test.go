package interp

import (
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/closure"
	"vexlua/internal/runtime/feedback"
	"vexlua/internal/runtime/heap"
	rtmeta "vexlua/internal/runtime/meta"
	"vexlua/internal/runtime/value"
)

func TestTypeMetatableVersionBumpsOnSet(t *testing.T) {
	engine := New()
	number := value.NumberValue(7)
	kind, ok := rtmeta.KindForValue(number)
	if !ok {
		t.Fatalf("number kind lookup failed")
	}
	if got := engine.Meta.Version(kind); got != 0 {
		t.Fatalf("initial number metatable version = %d, want 0", got)
	}
	metatable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable1: %v", err)
	}
	metatable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable2: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(number, metatable1.Value); err != nil {
		t.Fatalf("set number metatable1: %v", err)
	}
	version1 := engine.Meta.Version(kind)
	if version1 == 0 {
		t.Fatalf("number metatable version after first set = 0, want non-zero")
	}
	if err := engine.SetValueMetatableBoundary(number, metatable1.Value); err != nil {
		t.Fatalf("repeat set number metatable1: %v", err)
	}
	if got := engine.Meta.Version(kind); got != version1 {
		t.Fatalf("repeat set should keep version stable, got %d want %d", got, version1)
	}
	if err := engine.SetValueMetatableBoundary(number, metatable2.Value); err != nil {
		t.Fatalf("set number metatable2: %v", err)
	}
	version2 := engine.Meta.Version(kind)
	if version2 <= version1 {
		t.Fatalf("number metatable version should bump on new metatable, got %d <= %d", version2, version1)
	}
	if err := engine.SetValueMetatableBoundary(number, value.NilValue()); err != nil {
		t.Fatalf("clear number metatable: %v", err)
	}
	version3 := engine.Meta.Version(kind)
	if version3 <= version2 {
		t.Fatalf("number metatable version should bump on clear, got %d <= %d", version3, version2)
	}
}

func TestMatchCallFeedbackCellTracksTableCallShape(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	closure1 := newConstantLuaClosure(t, engine, env.Value, "@table-shape-call-1.lua", 41)
	closure2 := newConstantLuaClosure(t, engine, env.Value, "@table-shape-call-2.lua", 42)
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, closure1.Value); err != nil {
		t.Fatalf("seed __call closure1: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	resolved, _, err := engine.ResolveCallBoundary(callable.Value, nil)
	if err != nil {
		t.Fatalf("resolve call boundary: %v", err)
	}
	shape1, err := engine.describeCallShape(callable.Value)
	if err != nil {
		t.Fatalf("describe call shape: %v", err)
	}
	cell := feedback.NewCallMonomorphicCell(feedback.SlotCall, feedback.AccessCallResolvedLuaClosure, closure1.Ref, callable.Value.Bits(), shape1)
	matched, ok, err := engine.MatchCallFeedbackCell(callable.Value, cell)
	if err != nil || !ok || matched.Bits() != resolved.Bits() {
		t.Fatalf("match call feedback cell = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, resolved)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, closure2.Value); err != nil {
		t.Fatalf("swap __call closure: %v", err)
	}
	if _, ok, err := engine.MatchCallFeedbackCell(callable.Value, cell); err != nil || ok {
		t.Fatalf("stale table call feedback should miss after metatable table change, got ok=%v err=%v", ok, err)
	}
	shape2, err := engine.describeCallShape(callable.Value)
	if err != nil {
		t.Fatalf("describe refreshed call shape: %v", err)
	}
	refreshed := feedback.NewCallMonomorphicCell(feedback.SlotCall, feedback.AccessCallResolvedLuaClosure, closure2.Ref, callable.Value.Bits(), shape2)
	matched, ok, err = engine.MatchCallFeedbackCell(callable.Value, refreshed)
	if err != nil || !ok || matched.Bits() != closure2.Value.Bits() {
		t.Fatalf("refreshed table call feedback = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, closure2.Value)
	}
}

func TestMatchCallFeedbackCellTracksTypeMetatableShape(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	metatable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable1: %v", err)
	}
	metatable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable2: %v", err)
	}
	closure1 := newConstantLuaClosure(t, engine, env.Value, "@type-shape-call-1.lua", 41)
	closure2 := newConstantLuaClosure(t, engine, env.Value, "@type-shape-call-2.lua", 42)
	if err := engine.Tables.Set(metatable1.Ref, callKey.Value, closure1.Value); err != nil {
		t.Fatalf("seed metatable1 __call: %v", err)
	}
	if err := engine.Tables.Set(metatable2.Ref, callKey.Value, closure2.Value); err != nil {
		t.Fatalf("seed metatable2 __call: %v", err)
	}
	number := value.NumberValue(7)
	if err := engine.SetValueMetatableBoundary(number, metatable1.Value); err != nil {
		t.Fatalf("set type metatable1: %v", err)
	}
	shape1, err := engine.describeCallShape(number)
	if err != nil {
		t.Fatalf("describe type call shape: %v", err)
	}
	cell := feedback.NewCallMonomorphicCell(feedback.SlotCall, feedback.AccessCallResolvedLuaClosure, closure1.Ref, number.Bits(), shape1)
	matched, ok, err := engine.MatchCallFeedbackCell(number, cell)
	if err != nil || !ok || matched.Bits() != closure1.Value.Bits() {
		t.Fatalf("type-metatable call feedback = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, closure1.Value)
	}
	if err := engine.SetValueMetatableBoundary(number, metatable2.Value); err != nil {
		t.Fatalf("set type metatable2: %v", err)
	}
	if _, ok, err := engine.MatchCallFeedbackCell(number, cell); err != nil || ok {
		t.Fatalf("stale type-metatable call feedback should miss after type metatable change, got ok=%v err=%v", ok, err)
	}
	shape2, err := engine.describeCallShape(number)
	if err != nil {
		t.Fatalf("describe refreshed type call shape: %v", err)
	}
	refreshed := feedback.NewCallMonomorphicCell(feedback.SlotCall, feedback.AccessCallResolvedLuaClosure, closure2.Ref, number.Bits(), shape2)
	matched, ok, err = engine.MatchCallFeedbackCell(number, refreshed)
	if err != nil || !ok || matched.Bits() != closure2.Value.Bits() {
		t.Fatalf("refreshed type-metatable call feedback = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, closure2.Value)
	}
}

func TestMatchCallFeedbackCellTracksHostWrapperMetatableShape(t *testing.T) {
	engine := New()
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
	call1, err := engine.RegisterHostFunction("call1", func(value.TValue) float64 { return 41 }, env.Value)
	if err != nil {
		t.Fatalf("register host call1: %v", err)
	}
	call2, err := engine.RegisterHostFunction("call2", func(value.TValue) float64 { return 42 }, env.Value)
	if err != nil {
		t.Fatalf("register host call2: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, call1.Value); err != nil {
		t.Fatalf("seed host __call: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(wrapper.Value, metatable.Value); err != nil {
		t.Fatalf("set wrapper metatable: %v", err)
	}
	shape1, err := engine.describeCallShape(wrapper.Value)
	if err != nil {
		t.Fatalf("describe host wrapper shape: %v", err)
	}
	cell := feedback.NewCallMonomorphicCell(feedback.SlotCall, feedback.AccessCallResolvedHostFunction, call1.Ref, wrapper.Value.Bits(), shape1)
	matched, ok, err := engine.MatchCallFeedbackCell(wrapper.Value, cell)
	if err != nil || !ok || matched.Bits() != call1.Value.Bits() {
		t.Fatalf("host-wrapper call feedback = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, call1.Value)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, call2.Value); err != nil {
		t.Fatalf("swap host __call: %v", err)
	}
	if _, ok, err := engine.MatchCallFeedbackCell(wrapper.Value, cell); err != nil || ok {
		t.Fatalf("stale host-wrapper call feedback should miss after metatable table change, got ok=%v err=%v", ok, err)
	}
	shape2, err := engine.describeCallShape(wrapper.Value)
	if err != nil {
		t.Fatalf("describe refreshed host wrapper shape: %v", err)
	}
	refreshed := feedback.NewCallMonomorphicCell(feedback.SlotCall, feedback.AccessCallResolvedHostFunction, call2.Ref, wrapper.Value.Bits(), shape2)
	matched, ok, err = engine.MatchCallFeedbackCell(wrapper.Value, refreshed)
	if err != nil || !ok || matched.Bits() != call2.Value.Bits() {
		t.Fatalf("refreshed host-wrapper call feedback = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, call2.Value)
	}
}

func TestMatchCallFeedbackCellTracksPolymorphicResolvedTargets(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable1: %v", err)
	}
	callable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable2: %v", err)
	}
	metatable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable1: %v", err)
	}
	metatable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable2: %v", err)
	}
	closure1 := newConstantLuaClosure(t, engine, env.Value, "@poly-shape-call-1.lua", 41)
	closure2 := newConstantLuaClosure(t, engine, env.Value, "@poly-shape-call-2.lua", 42)
	if err := engine.Tables.Set(metatable1.Ref, callKey.Value, closure1.Value); err != nil {
		t.Fatalf("seed __call closure1: %v", err)
	}
	if err := engine.Tables.Set(metatable2.Ref, callKey.Value, closure2.Value); err != nil {
		t.Fatalf("seed __call closure2: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable1.Value, metatable1.Value); err != nil {
		t.Fatalf("set callable1 metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable2.Value, metatable2.Value); err != nil {
		t.Fatalf("set callable2 metatable: %v", err)
	}
	shape1, err := engine.describeCallShape(callable1.Value)
	if err != nil {
		t.Fatalf("describe callable1 shape: %v", err)
	}
	shape2, err := engine.describeCallShape(callable2.Value)
	if err != nil {
		t.Fatalf("describe callable2 shape: %v", err)
	}
	payload, err := engine.Heap.AllocPayload(feedback.CallPolymorphicDataSize, heap.PayloadLayoutOpaque, 0)
	if err != nil {
		t.Fatalf("alloc polymorphic payload: %v", err)
	}
	entries := [feedback.CallPolymorphicEntryCount]feedback.CallPolymorphicEntry{
		feedback.NewCallPolymorphicEntry(feedback.AccessCallResolvedLuaClosure, closure1.Ref, callable1.Value.Bits(), shape1),
		feedback.NewCallPolymorphicEntry(feedback.AccessCallResolvedLuaClosure, closure2.Ref, callable2.Value.Bits(), shape2),
	}
	if err := feedback.WriteCallPolymorphicEntries(payload.Bytes, entries); err != nil {
		t.Fatalf("write polymorphic entries: %v", err)
	}
	cell := feedback.NewCallPolymorphicCell(feedback.SlotCall, payload.Offset)
	matched, ok, err := engine.MatchCallFeedbackCell(callable1.Value, cell)
	if err != nil || !ok || matched.Bits() != closure1.Value.Bits() {
		t.Fatalf("polymorphic call match for callable1 = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, closure1.Value)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable2.Value, cell)
	if err != nil || !ok || matched.Bits() != closure2.Value.Bits() {
		t.Fatalf("polymorphic call match for callable2 = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, closure2.Value)
	}
	if err := engine.Tables.Set(metatable1.Ref, callKey.Value, closure2.Value); err != nil {
		t.Fatalf("swap callable1 __call: %v", err)
	}
	if _, ok, err := engine.MatchCallFeedbackCell(callable1.Value, cell); err != nil || ok {
		t.Fatalf("stale polymorphic entry should miss after callable1 metatable change, got ok=%v err=%v", ok, err)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable2.Value, cell)
	if err != nil || !ok || matched.Bits() != closure2.Value.Bits() {
		t.Fatalf("callable2 polymorphic entry should remain valid, got (%s, %v, %v)", matched, ok, err)
	}
}

func TestMatchCallFeedbackCellTracksMegamorphicInlineCell(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	closure1 := newConstantLuaClosure(t, engine, env.Value, "@mega-shape-call-1.lua", 41)
	closure2 := newConstantLuaClosure(t, engine, env.Value, "@mega-shape-call-2.lua", 42)
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, closure1.Value); err != nil {
		t.Fatalf("seed __call closure1: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	shape1, err := engine.describeCallShape(callable.Value)
	if err != nil {
		t.Fatalf("describe callable shape1: %v", err)
	}
	cell := feedback.NewMegamorphicCallCell(feedback.SlotCall, feedback.AccessCallResolvedLuaClosure, closure1.Ref, callable.Value.Bits(), shape1)
	matched, ok, err := engine.MatchCallFeedbackCell(callable.Value, cell)
	if err != nil || !ok || matched.Bits() != closure1.Value.Bits() {
		t.Fatalf("megamorphic call match = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, closure1.Value)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, closure2.Value); err != nil {
		t.Fatalf("swap __call closure: %v", err)
	}
	if _, ok, err := engine.MatchCallFeedbackCell(callable.Value, cell); err != nil || ok {
		t.Fatalf("stale inline megamorphic call cache should miss after metatable change, got ok=%v err=%v", ok, err)
	}
	shape2, err := engine.describeCallShape(callable.Value)
	if err != nil {
		t.Fatalf("describe callable shape2: %v", err)
	}
	refreshed := feedback.NewMegamorphicCallCell(feedback.SlotCall, feedback.AccessCallResolvedLuaClosure, closure2.Ref, callable.Value.Bits(), shape2)
	matched, ok, err = engine.MatchCallFeedbackCell(callable.Value, refreshed)
	if err != nil || !ok || matched.Bits() != closure2.Value.Bits() {
		t.Fatalf("refreshed inline megamorphic call cache = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, closure2.Value)
	}
}

func TestMatchCallFeedbackCellTracksMegamorphicSidecarEntries(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable1: %v", err)
	}
	callable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable2: %v", err)
	}
	metatable1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable1: %v", err)
	}
	metatable2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable2: %v", err)
	}
	closure1 := newConstantLuaClosure(t, engine, env.Value, "@mega-sidecar-call-1.lua", 41)
	closure2 := newConstantLuaClosure(t, engine, env.Value, "@mega-sidecar-call-2.lua", 42)
	if err := engine.Tables.Set(metatable1.Ref, callKey.Value, closure1.Value); err != nil {
		t.Fatalf("seed callable1 __call: %v", err)
	}
	if err := engine.Tables.Set(metatable2.Ref, callKey.Value, closure2.Value); err != nil {
		t.Fatalf("seed callable2 __call: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable1.Value, metatable1.Value); err != nil {
		t.Fatalf("set callable1 metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable2.Value, metatable2.Value); err != nil {
		t.Fatalf("set callable2 metatable: %v", err)
	}
	shape1, err := engine.describeCallShape(callable1.Value)
	if err != nil {
		t.Fatalf("describe callable1 shape: %v", err)
	}
	shape2, err := engine.describeCallShape(callable2.Value)
	if err != nil {
		t.Fatalf("describe callable2 shape: %v", err)
	}
	payload, err := engine.Heap.AllocPayload(feedback.CallMegamorphicDataSize, heap.PayloadLayoutOpaque, 0)
	if err != nil {
		t.Fatalf("alloc megamorphic payload: %v", err)
	}
	entries := [feedback.CallMegamorphicEntryCount]feedback.CallPolymorphicEntry{
		feedback.NewCallPolymorphicEntry(feedback.AccessCallResolvedLuaClosure, closure1.Ref, callable1.Value.Bits(), shape1),
		feedback.NewCallPolymorphicEntry(feedback.AccessCallResolvedLuaClosure, closure2.Ref, callable2.Value.Bits(), shape2),
	}
	if err := feedback.WriteCallMegamorphicEntries(payload.Bytes, entries); err != nil {
		t.Fatalf("write megamorphic entries: %v", err)
	}
	cell := feedback.NewMegamorphicCallSidecarCell(feedback.SlotCall, payload.Offset)
	matched, ok, err := engine.MatchCallFeedbackCell(callable1.Value, cell)
	if err != nil || !ok || matched.Bits() != closure1.Value.Bits() {
		t.Fatalf("megamorphic sidecar should match callable1 = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, closure1.Value)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable2.Value, cell)
	if err != nil || !ok || matched.Bits() != closure2.Value.Bits() {
		t.Fatalf("megamorphic sidecar should match callable2 = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, closure2.Value)
	}
	if err := engine.Tables.Set(metatable1.Ref, callKey.Value, closure2.Value); err != nil {
		t.Fatalf("swap callable1 __call: %v", err)
	}
	if _, ok, err := engine.MatchCallFeedbackCell(callable1.Value, cell); err != nil || ok {
		t.Fatalf("stale megamorphic sidecar entry should miss after callable1 metatable change, got ok=%v err=%v", ok, err)
	}
	matched, ok, err = engine.MatchCallFeedbackCell(callable2.Value, cell)
	if err != nil || !ok || matched.Bits() != closure2.Value.Bits() {
		t.Fatalf("callable2 megamorphic sidecar entry should remain valid = (%s, %v, %v), want (%s, true, nil)", matched, ok, err, closure2.Value)
	}
}

func newConstantLuaClosure(t *testing.T, engine *Engine, env value.TValue, source string, constant float64) closure.Handle {
	t.Helper()
	proto := &bytecode.Proto{
		Source:       source,
		MaxStackSize: 1,
		Constants:    []bytecode.Constant{bytecode.NumberConstant(constant)},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closure, err := engine.NewClosure(proto, env, nil)
	if err != nil {
		t.Fatalf("new constant closure %s: %v", source, err)
	}
	return closure
}

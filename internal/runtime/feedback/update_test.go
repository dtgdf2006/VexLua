package feedback

import (
	"testing"

	"vexlua/internal/bytecode"
	rttable "vexlua/internal/runtime/table"
	rtupvalue "vexlua/internal/runtime/upvalue"
	"vexlua/internal/runtime/value"
)

func TestSlotInfoForProtoPCWithoutColdLayoutCache(t *testing.T) {
	proto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(1)),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, bytecode.RKAsk(2), 1),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 1),
		},
	}
	tests := []struct {
		pc        int
		kind      SlotKind
		slotIndex uint32
		ok        bool
	}{
		{pc: 0, kind: SlotGetGlobal, slotIndex: 0, ok: true},
		{pc: 1, ok: false},
		{pc: 2, kind: SlotGetTable, slotIndex: 1, ok: true},
		{pc: 3, kind: SlotSetTable, slotIndex: 2, ok: true},
		{pc: 4, kind: SlotCall, slotIndex: 3, ok: true},
	}
	for _, test := range tests {
		slot, slotIndex, ok := SlotInfoForProtoPC(proto, test.pc)
		if ok != test.ok {
			t.Fatalf("pc %d presence = %v, want %v", test.pc, ok, test.ok)
		}
		if !test.ok {
			continue
		}
		if slot.Kind != test.kind || slotIndex != test.slotIndex {
			t.Fatalf("pc %d slot = (%d,%d), want (%d,%d)", test.pc, slot.Kind, slotIndex, test.kind, test.slotIndex)
		}
	}
}

func TestNextTableCellMatchesSharedICTransitions(t *testing.T) {
	current := NewGenericCell(SlotGetTable)
	access := rttable.FastAccess{Kind: rttable.FastAccessHash, TableRef: 0x11, TableVersion: 7, SlotIndex: 3}
	next, changed := NextTableCell(current, SlotGetTable, access, true, value.NumberValue(42).Bits())
	if !changed {
		t.Fatalf("expected generic->monomorphic transition")
	}
	if next.State != StateMonomorphic || next.AccessKind != AccessHash || next.TableRef() != 0x11 || next.TableVersion() != 7 || next.CachedIndex() != 3 {
		t.Fatalf("unexpected monomorphic cell: %+v", next)
	}
	updated, changed := NextTableCell(next, SlotGetTable, rttable.FastAccess{Kind: rttable.FastAccessHash, TableRef: 0x11, TableVersion: 9, SlotIndex: 3}, true, value.NumberValue(42).Bits())
	if !changed {
		t.Fatalf("expected monomorphic version refresh")
	}
	if updated.TableVersion() != 9 {
		t.Fatalf("version refresh failed: %+v", updated)
	}
	mega, changed := NextTableCell(updated, SlotGetTable, rttable.FastAccess{Kind: rttable.FastAccessHash, TableRef: 0x22, TableVersion: 1, SlotIndex: 0}, true, value.NumberValue(42).Bits())
	if !changed || mega.State != StateMegamorphic {
		t.Fatalf("expected monomorphic->megamorphic transition, got %+v changed=%v", mega, changed)
	}
	unchanged, changed := NextTableCell(mega, SlotGetTable, access, true, value.NumberValue(42).Bits())
	if changed || unchanged != mega {
		t.Fatalf("megamorphic cell should remain stable, got %+v changed=%v", unchanged, changed)
	}
}

func TestNextCallCellTracksTargetFamilies(t *testing.T) {
	current := NewGenericCell(SlotCall)
	callee := value.LuaClosureRefValue(0x33)
	next, changed := NextCallCell(current, SlotCall, callee, callee, CallShape{Kind: CallShapeDirect})
	if !changed {
		t.Fatalf("expected generic->monomorphic call transition")
	}
	if next.State != StateMonomorphic || next.AccessKind != AccessCallLuaClosure || next.TargetRef() != 0x33 {
		t.Fatalf("unexpected call cell: %+v", next)
	}
	refreshed, changed := NextCallCell(next, SlotCall, value.LuaClosureRefValue(0x33), value.LuaClosureRefValue(0x33), CallShape{Kind: CallShapeDirect})
	if changed || refreshed != next {
		t.Fatalf("same call target should remain stable, got %+v changed=%v", refreshed, changed)
	}
	mega, changed := NextCallCell(refreshed, SlotCall, value.HostFunctionRefValue(0x44), value.HostFunctionRefValue(0x44), CallShape{Kind: CallShapeDirect})
	if !changed || mega.State != StateMegamorphic {
		t.Fatalf("expected call megamorphic transition, got %+v changed=%v", mega, changed)
	}
	if mega.AccessKind != AccessCallHostFunction || mega.TargetRef() != 0x44 || mega.ValueBits != value.HostFunctionRefValue(0x44).Bits() {
		t.Fatalf("megamorphic call cell should retain last direct hit, got %+v", mega)
	}
	unchanged, changed := NextCallCell(mega, SlotCall, value.HostFunctionRefValue(0x44), value.HostFunctionRefValue(0x44), CallShape{Kind: CallShapeDirect})
	if changed || unchanged != mega {
		t.Fatalf("megamorphic call cell should remain stable, got %+v changed=%v", unchanged, changed)
	}
}

func TestNextCallCellTracksTMCallResolvedTargets(t *testing.T) {
	current := NewGenericCell(SlotCall)
	callable := value.TableRefValue(0x55)
	resolved := value.LuaClosureRefValue(0x33)
	shape := CallShape{Kind: CallShapeTableMetatable, VersionA: 7, VersionB: 11}
	next, changed := NextCallCell(current, SlotCall, callable, resolved, shape)
	if !changed {
		t.Fatalf("expected generic->monomorphic tm_call transition")
	}
	if next.State != StateMonomorphic || next.AccessKind != AccessCallResolvedLuaClosure || next.TargetRef() != 0x33 || next.ValueBits != callable.Bits() || next.CallShapeKind() != CallShapeTableMetatable || next.CallShapeVersionA() != 7 || next.CallShapeVersionB() != 11 {
		t.Fatalf("unexpected tm_call cell: %+v", next)
	}
	refreshed, changed := NextCallCell(next, SlotCall, callable, resolved, shape)
	if changed || refreshed != next {
		t.Fatalf("same tm_call target should remain stable, got %+v changed=%v", refreshed, changed)
	}
	otherCallable := value.TableRefValue(0x66)
	mega, changed := NextCallCell(refreshed, SlotCall, otherCallable, resolved, shape)
	if !changed || mega.State != StateMegamorphic {
		t.Fatalf("expected tm_call megamorphic transition, got %+v changed=%v", mega, changed)
	}
	if mega.AccessKind != AccessCallResolvedLuaClosure || mega.TargetRef() != 0x33 || mega.ValueBits != otherCallable.Bits() {
		t.Fatalf("megamorphic tm_call cell should retain last resolved hit, got %+v", mega)
	}
}

func TestNextCallCellRefreshesSameReceiverAcrossShapeChanges(t *testing.T) {
	callable := value.TableRefValue(0x55)
	current := NewCallMonomorphicCell(SlotCall, AccessCallResolvedLuaClosure, 0x33, callable.Bits(), CallShape{Kind: CallShapeTableMetatable, VersionA: 7, VersionB: 11})
	next, changed := NextCallCell(current, SlotCall, callable, value.HostFunctionRefValue(0x44), CallShape{Kind: CallShapeTableMetatable, VersionA: 8, VersionB: 12})
	if !changed {
		t.Fatalf("expected same receiver to refresh call cell across shape change")
	}
	if next.State != StateMonomorphic || next.AccessKind != AccessCallResolvedHostFunction || next.TargetRef() != 0x44 || next.CallShapeVersionA() != 8 || next.CallShapeVersionB() != 12 {
		t.Fatalf("unexpected refreshed call cell: %+v", next)
	}
	mega, changed := NextCallCell(next, SlotCall, value.TableRefValue(0x66), value.HostFunctionRefValue(0x44), CallShape{Kind: CallShapeTableMetatable, VersionA: 8, VersionB: 12})
	if !changed || mega.State != StateMegamorphic {
		t.Fatalf("expected different receiver to force megamorphic, got %+v changed=%v", mega, changed)
	}
	if mega.AccessKind != AccessCallResolvedHostFunction || mega.TargetRef() != 0x44 || mega.ValueBits != value.TableRefValue(0x66).Bits() {
		t.Fatalf("megamorphic cell should retain refreshed resolved-host hit, got %+v", mega)
	}
}

func TestNextCallTransitionPromotesSecondReceiverToPolymorphic(t *testing.T) {
	current := NewCallMonomorphicCell(SlotCall, AccessCallResolvedLuaClosure, 0x33, value.TableRefValue(0x55).Bits(), CallShape{Kind: CallShapeTableMetatable, VersionA: 7, VersionB: 11})
	next, entries, changed := NextCallTransition(current, nil, SlotCall, value.TableRefValue(0x66), value.HostFunctionRefValue(0x44), CallShape{Kind: CallShapeTableMetatable, VersionA: 8, VersionB: 12})
	if !changed {
		t.Fatalf("expected monomorphic->polymorphic transition")
	}
	if next.State != StatePolymorphic {
		t.Fatalf("next state = %d, want polymorphic", next.State)
	}
	if len(entries) != CallPolymorphicEntryCount {
		t.Fatalf("entry count = %d, want %d", len(entries), CallPolymorphicEntryCount)
	}
	if entries[0].AccessKind != AccessCallResolvedLuaClosure || entries[0].TargetRef != 0x33 || entries[0].ValueBits != value.TableRefValue(0x55).Bits() {
		t.Fatalf("first polymorphic entry = %+v", entries[0])
	}
	if entries[1].AccessKind != AccessCallResolvedHostFunction || entries[1].TargetRef != 0x44 || entries[1].ValueBits != value.TableRefValue(0x66).Bits() {
		t.Fatalf("second polymorphic entry = %+v", entries[1])
	}
}

func TestNextCallTransitionRefreshesExistingPolymorphicEntry(t *testing.T) {
	current := NewCallPolymorphicCell(SlotCall, 0x123)
	entries := []CallPolymorphicEntry{
		NewCallPolymorphicEntry(AccessCallResolvedLuaClosure, 0x33, value.TableRefValue(0x55).Bits(), CallShape{Kind: CallShapeTableMetatable, VersionA: 7, VersionB: 11}),
		NewCallPolymorphicEntry(AccessCallResolvedHostFunction, 0x44, value.TableRefValue(0x66).Bits(), CallShape{Kind: CallShapeTableMetatable, VersionA: 8, VersionB: 12}),
	}
	next, updatedEntries, changed := NextCallTransition(current, entries, SlotCall, value.TableRefValue(0x66), value.HostFunctionRefValue(0x45), CallShape{Kind: CallShapeTableMetatable, VersionA: 9, VersionB: 13})
	if !changed {
		t.Fatalf("expected polymorphic entry refresh")
	}
	if next != current {
		t.Fatalf("polymorphic cell should keep sidecar cell shape, got %+v want %+v", next, current)
	}
	if len(updatedEntries) != CallPolymorphicEntryCount {
		t.Fatalf("updated entry count = %d, want %d", len(updatedEntries), CallPolymorphicEntryCount)
	}
	if updatedEntries[1].TargetRef != 0x45 || updatedEntries[1].Shape.VersionA != 9 || updatedEntries[1].Shape.VersionB != 13 {
		t.Fatalf("updated polymorphic entry = %+v", updatedEntries[1])
	}
	mega, megaEntries, changed := NextCallTransition(current, entries, SlotCall, value.TableRefValue(0x77), value.LuaClosureRefValue(0x88), CallShape{Kind: CallShapeTableMetatable, VersionA: 10, VersionB: 14})
	if !changed || mega.State != StateMegamorphic || !mega.HasMegamorphicCallSidecar() {
		t.Fatalf("third receiver should force megamorphic, got cell=%+v entries=%v changed=%v", mega, megaEntries, changed)
	}
	if len(megaEntries) != 3 {
		t.Fatalf("megamorphic entry count = %d, want 3", len(megaEntries))
	}
	if megaEntries[0].AccessKind != AccessCallResolvedLuaClosure || megaEntries[0].TargetRef != 0x88 || megaEntries[0].ValueBits != value.TableRefValue(0x77).Bits() {
		t.Fatalf("megamorphic transition should front-load newest resolved-lua hit, got %+v", megaEntries[0])
	}
	refreshedMega, megaEntries, changed := NextCallTransition(mega, megaEntries, SlotCall, value.TableRefValue(0x66), value.HostFunctionRefValue(0x45), CallShape{Kind: CallShapeTableMetatable, VersionA: 9, VersionB: 13})
	if !changed || !refreshedMega.HasMegamorphicCallSidecar() {
		t.Fatalf("megamorphic refresh should update in-place cache, got cell=%+v entries=%v changed=%v", refreshedMega, megaEntries, changed)
	}
	if len(megaEntries) != 3 {
		t.Fatalf("refreshed megamorphic entry count = %d, want 3", len(megaEntries))
	}
	if megaEntries[0].AccessKind != AccessCallResolvedHostFunction || megaEntries[0].TargetRef != 0x45 || megaEntries[0].ValueBits != value.TableRefValue(0x66).Bits() {
		t.Fatalf("megamorphic refresh should front-load latest resolved-host hit, got %+v", megaEntries[0])
	}
	refreshedMega, megaEntries, changed = NextCallTransition(refreshedMega, megaEntries, SlotCall, value.TableRefValue(0x99), value.HostFunctionRefValue(0x46), CallShape{Kind: CallShapeTableMetatable, VersionA: 11, VersionB: 15})
	if !changed || !refreshedMega.HasMegamorphicCallSidecar() {
		t.Fatalf("fourth receiver should stay on megamorphic sidecar, got cell=%+v entries=%v changed=%v", refreshedMega, megaEntries, changed)
	}
	if len(megaEntries) != CallMegamorphicEntryCount {
		t.Fatalf("expanded megamorphic entry count = %d, want %d", len(megaEntries), CallMegamorphicEntryCount)
	}
	refreshedMega, megaEntries, changed = NextCallTransition(refreshedMega, megaEntries, SlotCall, value.TableRefValue(0xAA), value.HostFunctionRefValue(0x47), CallShape{Kind: CallShapeTableMetatable, VersionA: 12, VersionB: 16})
	if !changed || !refreshedMega.HasMegamorphicCallSidecar() {
		t.Fatalf("fifth receiver should keep fixed-width megamorphic sidecar, got cell=%+v entries=%v changed=%v", refreshedMega, megaEntries, changed)
	}
	if len(megaEntries) != CallMegamorphicEntryCount {
		t.Fatalf("evicted megamorphic entry count = %d, want %d", len(megaEntries), CallMegamorphicEntryCount)
	}
	for _, entry := range megaEntries {
		if entry.ValueBits == value.TableRefValue(0x55).Bits() {
			t.Fatalf("oldest megamorphic receiver should be evicted once cache is full, entries=%+v", megaEntries)
		}
	}
	if megaEntries[0].TargetRef != 0x47 || megaEntries[0].ValueBits != value.TableRefValue(0xAA).Bits() {
		t.Fatalf("newest megamorphic receiver should stay at front, got %+v", megaEntries[0])
	}
}

func TestNextUpvalueCellRefreshesSameRefAcrossOpenAndClosedStates(t *testing.T) {
	current := NewGenericCell(SlotGetUpvalue)
	next, changed := NextUpvalueCell(current, SlotGetUpvalue, 0x55, rtupvalue.StateOpen, value.NumberValue(11).Bits())
	if !changed {
		t.Fatalf("expected generic->monomorphic upvalue transition")
	}
	if next.State != StateMonomorphic || next.AccessKind != AccessUpvalueOpen || next.TargetRef() != 0x55 || next.ObservedValueBits() != value.NumberValue(11).Bits() {
		t.Fatalf("unexpected upvalue cell: %+v", next)
	}
	refreshed, changed := NextUpvalueCell(next, SlotGetUpvalue, 0x55, rtupvalue.StateClosed, value.NumberValue(22).Bits())
	if !changed {
		t.Fatalf("expected same-ref upvalue refresh")
	}
	if refreshed.AccessKind != AccessUpvalueClosed || refreshed.ObservedValueBits() != value.NumberValue(22).Bits() {
		t.Fatalf("upvalue refresh failed: %+v", refreshed)
	}
	mega, changed := NextUpvalueCell(refreshed, SlotGetUpvalue, 0x66, rtupvalue.StateClosed, value.NumberValue(22).Bits())
	if !changed || mega.State != StateMegamorphic {
		t.Fatalf("expected upvalue megamorphic transition, got %+v changed=%v", mega, changed)
	}
}

package feedback

import (
	"testing"

	"vexlua/internal/bytecode"
	rttable "vexlua/internal/runtime/table"
	"vexlua/internal/runtime/value"
)

func TestSlotInfoForProtoPCWithoutColdLayoutCache(t *testing.T) {
	proto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(1)),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, bytecode.RKAsk(2), 1),
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
	if next.State != StateMonomorphic || next.AccessKind != AccessHash || next.TableRef != 0x11 || next.TableVersion != 7 || next.CachedIndex != 3 {
		t.Fatalf("unexpected monomorphic cell: %+v", next)
	}
	updated, changed := NextTableCell(next, SlotGetTable, rttable.FastAccess{Kind: rttable.FastAccessHash, TableRef: 0x11, TableVersion: 9, SlotIndex: 3}, true, value.NumberValue(42).Bits())
	if !changed {
		t.Fatalf("expected monomorphic version refresh")
	}
	if updated.TableVersion != 9 {
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

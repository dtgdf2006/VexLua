package feedback

import (
	"testing"

	"vexlua/internal/bytecode"
)

func TestLayoutBuildsSlotsForCoveredCellFamilies(t *testing.T) {
	proto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(1)),
			bytecode.CreateABx(bytecode.OP_SETGLOBAL, 1, 2),
			bytecode.CreateABC(bytecode.OP_SETUPVAL, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, bytecode.RKAsk(3), 1),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 1),
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 1, 0),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 0, 1),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 0, -1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	layout := LayoutForProto(proto)
	if layout.SlotCount() != 8 {
		t.Fatalf("slot count = %d, want 8", layout.SlotCount())
	}
	tests := []struct {
		pc   int
		kind SlotKind
	}{
		{pc: 0, kind: SlotGetGlobal},
		{pc: 1, kind: SlotGetUpvalue},
		{pc: 2, kind: SlotGetTable},
		{pc: 3, kind: SlotSetGlobal},
		{pc: 4, kind: SlotSetUpvalue},
		{pc: 5, kind: SlotSetTable},
		{pc: 6, kind: SlotCall},
		{pc: 7, kind: SlotTailCall},
	}
	for index, test := range tests {
		slot, slotIndex, ok := layout.SlotAtPC(test.pc)
		if !ok {
			t.Fatalf("pc %d missing slot", test.pc)
		}
		if slotIndex != uint32(index) {
			t.Fatalf("pc %d slot index = %d, want %d", test.pc, slotIndex, index)
		}
		if slot.Kind != test.kind {
			t.Fatalf("pc %d slot kind = %d, want %d", test.pc, slot.Kind, test.kind)
		}
	}
	for _, pc := range []int{8, 9, 10} {
		if _, _, ok := layout.SlotAtPC(pc); ok {
			t.Fatalf("pc %d should not get a feedback cell slot", pc)
		}
	}
}

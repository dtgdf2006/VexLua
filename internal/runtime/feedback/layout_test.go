package feedback

import (
	"testing"

	"vexlua/internal/bytecode"
)

func TestLayoutBuildsSlotsForTableAndGlobalOpcodes(t *testing.T) {
	proto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, bytecode.RKAsk(1)),
			bytecode.CreateABx(bytecode.OP_SETGLOBAL, 1, 2),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, bytecode.RKAsk(3), 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	layout := LayoutForProto(proto)
	if layout.SlotCount() != 4 {
		t.Fatalf("slot count = %d, want 4", layout.SlotCount())
	}
	tests := []struct {
		pc   int
		kind SlotKind
	}{
		{pc: 0, kind: SlotGetGlobal},
		{pc: 1, kind: SlotGetTable},
		{pc: 2, kind: SlotSetGlobal},
		{pc: 3, kind: SlotSetTable},
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
	if _, _, ok := layout.SlotAtPC(4); ok {
		t.Fatalf("RETURN should not get a feedback slot")
	}
}

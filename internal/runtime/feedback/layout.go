package feedback

import (
	"sync"

	"vexlua/internal/bytecode"
)

type SlotKind uint8

const (
	SlotInvalid SlotKind = iota
	SlotGetGlobal
	SlotGetTable
	SlotSetGlobal
	SlotSetTable
	SlotGetUpvalue
	SlotSetUpvalue
	SlotCall
	SlotTailCall
)

type Slot struct {
	PC   int
	Kind SlotKind
}

type Layout struct {
	slots     []Slot
	pcToIndex []int32
}

// layoutCache stores immutable per-proto slot layouts for both cold-path
// preparation and runtime PC-to-slot lookups.
var layoutCache sync.Map

// LayoutForProto returns the shared immutable slot layout for a proto.
func LayoutForProto(proto *bytecode.Proto) *Layout {
	if cached, ok := layoutCache.Load(proto); ok {
		return cached.(*Layout)
	}
	layout := buildLayout(proto)
	actual, _ := layoutCache.LoadOrStore(proto, layout)
	return actual.(*Layout)
}

func (layout *Layout) SlotCount() uint32 {
	return uint32(len(layout.slots))
}

func (layout *Layout) Slots() []Slot {
	return append([]Slot(nil), layout.slots...)
}

func (layout *Layout) SlotAtPC(pc int) (Slot, uint32, bool) {
	if pc < 0 || pc >= len(layout.pcToIndex) {
		return Slot{}, 0, false
	}
	index := layout.pcToIndex[pc]
	if index < 0 {
		return Slot{}, 0, false
	}
	return layout.slots[index], uint32(index), true
}

func buildLayout(proto *bytecode.Proto) *Layout {
	layout := &Layout{}
	layout.pcToIndex = make([]int32, len(proto.Code))
	for index := range layout.pcToIndex {
		layout.pcToIndex[index] = -1
	}
	for pc, instruction := range proto.Code {
		kind, ok := slotKindForOpcode(instruction.Opcode())
		if !ok {
			continue
		}
		layout.pcToIndex[pc] = int32(len(layout.slots))
		layout.slots = append(layout.slots, Slot{PC: pc, Kind: kind})
	}
	return layout
}

func slotKindForOpcode(opcode bytecode.Opcode) (SlotKind, bool) {
	switch opcode {
	case bytecode.OP_GETGLOBAL:
		return SlotGetGlobal, true
	case bytecode.OP_GETTABLE:
		return SlotGetTable, true
	case bytecode.OP_SETGLOBAL:
		return SlotSetGlobal, true
	case bytecode.OP_SETTABLE:
		return SlotSetTable, true
	case bytecode.OP_GETUPVAL:
		return SlotGetUpvalue, true
	case bytecode.OP_SETUPVAL:
		return SlotSetUpvalue, true
	case bytecode.OP_CALL:
		return SlotCall, true
	case bytecode.OP_TFORLOOP:
		return SlotCall, true
	case bytecode.OP_TAILCALL:
		return SlotTailCall, true
	default:
		return SlotInvalid, false
	}
}

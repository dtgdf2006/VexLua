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
)

type Slot struct {
	PC   int
	Kind SlotKind
}

type Layout struct {
	slots     []Slot
	pcToIndex []int32
}

// coldLayoutCache is only for compile-time and other cold-path preparation.
// Runtime feedback updates should use SlotInfoForProtoPC instead of consulting
// this cache on every access.
var coldLayoutCache sync.Map

// LayoutForProto is a cold-path helper for compilation and feedback-vector
// allocation. Hot runtime feedback updates must not depend on this cache.
func LayoutForProto(proto *bytecode.Proto) *Layout {
	if proto == nil {
		return &Layout{}
	}
	if cached, ok := coldLayoutCache.Load(proto); ok {
		return cached.(*Layout)
	}
	layout := buildLayout(proto)
	actual, _ := coldLayoutCache.LoadOrStore(proto, layout)
	return actual.(*Layout)
}

func (layout *Layout) SlotCount() uint32 {
	if layout == nil {
		return 0
	}
	return uint32(len(layout.slots))
}

func (layout *Layout) Slots() []Slot {
	if layout == nil {
		return nil
	}
	return append([]Slot(nil), layout.slots...)
}

func (layout *Layout) SlotAtPC(pc int) (Slot, uint32, bool) {
	if layout == nil || pc < 0 || pc >= len(layout.pcToIndex) {
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
	if proto == nil {
		return layout
	}
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
	default:
		return SlotInvalid, false
	}
}

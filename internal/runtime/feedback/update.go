package feedback

import (
	"vexlua/internal/bytecode"
	rttable "vexlua/internal/runtime/table"
	"vexlua/internal/runtime/value"
)

func SlotInfoForProtoPC(proto *bytecode.Proto, pc int) (Slot, uint32, bool) {
	if proto == nil || pc < 0 || pc >= len(proto.Code) {
		return Slot{}, 0, false
	}
	var slotIndex uint32
	for bytecodePC, instruction := range proto.Code {
		kind, ok := slotKindForOpcode(instruction.Opcode())
		if !ok {
			continue
		}
		if bytecodePC == pc {
			return Slot{PC: bytecodePC, Kind: kind}, slotIndex, true
		}
		slotIndex++
		if bytecodePC > pc {
			break
		}
	}
	return Slot{}, 0, false
}

func AccessKindForFastAccess(kind rttable.FastAccessKind) AccessKind {
	switch kind {
	case rttable.FastAccessArray:
		return AccessArray
	case rttable.FastAccessHash:
		return AccessHash
	default:
		return AccessInvalid
	}
}

func NextTableCell(current Cell, slotKind SlotKind, access rttable.FastAccess, eligible bool, keyBits value.Raw) (Cell, bool) {
	if current.SlotKind != SlotInvalid && current.SlotKind != slotKind {
		return current, false
	}
	next := current
	if !eligible {
		if current.State == StateMonomorphic {
			next = NewMegamorphicCell(slotKind)
		}
		return next, next != current
	}
	candidate := NewMonomorphicCell(slotKind, AccessKindForFastAccess(access.Kind), access.TableRef, access.TableVersion, access.SlotIndex, keyBits)
	switch current.State {
	case StateGeneric:
		next = candidate
	case StateMonomorphic:
		if current.TableRef == candidate.TableRef && current.KeyBits == candidate.KeyBits && current.AccessKind == candidate.AccessKind && current.SlotKind == candidate.SlotKind {
			next = candidate
		} else {
			next = NewMegamorphicCell(slotKind)
		}
	case StateMegamorphic:
		return current, false
	}
	return next, next != current
}

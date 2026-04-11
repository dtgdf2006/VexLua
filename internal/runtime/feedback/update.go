package feedback

import (
	"vexlua/internal/bytecode"
	rttable "vexlua/internal/runtime/table"
	rtupvalue "vexlua/internal/runtime/upvalue"
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

func AccessKindForCallTarget(callee value.TValue) (AccessKind, value.HeapRef44, bool) {
	switch {
	case callee.IsBoxedTag(value.TagLuaClosureRef):
		ref, _ := callee.HeapRef()
		return AccessCallLuaClosure, ref, true
	case callee.IsBoxedTag(value.TagHostFunctionRef):
		ref, _ := callee.HeapRef()
		return AccessCallHostFunction, ref, true
	default:
		return AccessInvalid, 0, false
	}
}

func AccessKindForUpvalueState(state rtupvalue.State) AccessKind {
	switch state {
	case rtupvalue.StateOpen:
		return AccessUpvalueOpen
	case rtupvalue.StateClosed:
		return AccessUpvalueClosed
	default:
		return AccessInvalid
	}
}

type monomorphicMatch func(current Cell, candidate Cell) bool

func nextCell(current Cell, slotKind SlotKind, eligible bool, candidate Cell, match monomorphicMatch) (Cell, bool) {
	if current.SlotKind != SlotInvalid && current.SlotKind != slotKind {
		return current, false
	}
	if !eligible {
		if current.State == StateMonomorphic {
			next := NewMegamorphicCell(slotKind)
			return next, next != current
		}
		return current, false
	}
	switch current.State {
	case StateGeneric:
		return candidate, candidate != current
	case StateMonomorphic:
		if match(current, candidate) {
			return candidate, candidate != current
		}
		next := NewMegamorphicCell(slotKind)
		return next, next != current
	case StateMegamorphic:
		return current, false
	default:
		return current, false
	}
}

func NextTableCell(current Cell, slotKind SlotKind, access rttable.FastAccess, eligible bool, keyBits value.Raw) (Cell, bool) {
	candidate := NewTableMonomorphicCell(slotKind, AccessKindForFastAccess(access.Kind), access.TableRef, access.TableVersion, access.SlotIndex, keyBits)
	return nextCell(current, slotKind, eligible, candidate, func(current Cell, candidate Cell) bool {
		return current.AccessKind == candidate.AccessKind && current.TableRef() == candidate.TableRef() && current.KeyBits() == candidate.KeyBits() && current.SlotKind == candidate.SlotKind
	})
}

func NextCallCell(current Cell, slotKind SlotKind, callee value.TValue) (Cell, bool) {
	accessKind, targetRef, eligible := AccessKindForCallTarget(callee)
	candidate := NewCallMonomorphicCell(slotKind, accessKind, targetRef, callee.Bits())
	return nextCell(current, slotKind, eligible, candidate, func(current Cell, candidate Cell) bool {
		return current.AccessKind == candidate.AccessKind && current.TargetRef() == candidate.TargetRef() && current.SlotKind == candidate.SlotKind
	})
}

func NextUpvalueCell(current Cell, slotKind SlotKind, upvalueRef value.HeapRef44, upvalueState rtupvalue.State, observedBits value.Raw) (Cell, bool) {
	accessKind := AccessKindForUpvalueState(upvalueState)
	eligible := accessKind != AccessInvalid && upvalueRef != 0
	candidate := NewUpvalueMonomorphicCell(slotKind, accessKind, upvalueRef, observedBits)
	return nextCell(current, slotKind, eligible, candidate, func(current Cell, candidate Cell) bool {
		return current.TargetRef() == candidate.TargetRef() && current.SlotKind == candidate.SlotKind
	})
}

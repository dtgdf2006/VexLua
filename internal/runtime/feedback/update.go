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

func AccessKindForCallTarget(originalCallee value.TValue, resolvedCallee value.TValue) (AccessKind, value.HeapRef44, bool) {
	switch {
	case resolvedCallee.IsBoxedTag(value.TagLuaClosureRef):
		ref, _ := resolvedCallee.HeapRef()
		if originalCallee.Bits() == resolvedCallee.Bits() {
			return AccessCallLuaClosure, ref, true
		}
		return AccessCallResolvedLuaClosure, ref, true
	case resolvedCallee.IsBoxedTag(value.TagHostFunctionRef):
		ref, _ := resolvedCallee.HeapRef()
		if originalCallee.Bits() == resolvedCallee.Bits() {
			return AccessCallHostFunction, ref, true
		}
		return AccessCallResolvedHostFunction, ref, true
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

func callEntryFromCell(cell Cell) CallPolymorphicEntry {
	return NewCallPolymorphicEntry(cell.AccessKind, cell.TargetRef(), cell.ValueBits, CallShape{Kind: cell.CallShapeKind(), VersionA: cell.CallShapeVersionA(), VersionB: cell.CallShapeVersionB()})
}

func callPolyEntriesEqual(left []CallPolymorphicEntry, right []CallPolymorphicEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func compactCallEntries(entries []CallPolymorphicEntry) []CallPolymorphicEntry {
	compact := make([]CallPolymorphicEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.AccessKind == AccessInvalid || entry.ValueBits == 0 {
			continue
		}
		compact = append(compact, entry)
	}
	return compact
}

func nextMegamorphicCallEntries(current []CallPolymorphicEntry, candidate CallPolymorphicEntry) []CallPolymorphicEntry {
	entries := make([]CallPolymorphicEntry, 0, CallMegamorphicEntryCount)
	entries = append(entries, candidate)
	for _, entry := range compactCallEntries(current) {
		if entry.ValueBits == candidate.ValueBits {
			continue
		}
		entries = append(entries, entry)
		if len(entries) == CallMegamorphicEntryCount {
			break
		}
	}
	return entries
}

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

func NextCallCell(current Cell, slotKind SlotKind, originalCallee value.TValue, resolvedCallee value.TValue, shape CallShape) (Cell, bool) {
	if current.SlotKind != SlotInvalid && current.SlotKind != slotKind {
		return current, false
	}
	accessKind, targetRef, eligible := AccessKindForCallTarget(originalCallee, resolvedCallee)
	candidate := NewCallMonomorphicCell(slotKind, accessKind, targetRef, originalCallee.Bits(), shape)
	if !eligible {
		switch current.State {
		case StateMonomorphic, StateMegamorphic:
			next := NewMegamorphicCell(slotKind)
			return next, next != current
		default:
			return current, false
		}
	}
	switch current.State {
	case StateGeneric:
		return candidate, candidate != current
	case StateMonomorphic:
		if current.ValueBits == candidate.ValueBits && current.SlotKind == candidate.SlotKind {
			return candidate, candidate != current
		}
		next := NewMegamorphicCallCell(slotKind, accessKind, targetRef, originalCallee.Bits(), shape)
		return next, next != current
	case StateMegamorphic:
		next := NewMegamorphicCallCell(slotKind, accessKind, targetRef, originalCallee.Bits(), shape)
		return next, next != current
	default:
		return current, false
	}
}

func NextCallTransition(current Cell, currentPoly []CallPolymorphicEntry, slotKind SlotKind, originalCallee value.TValue, resolvedCallee value.TValue, shape CallShape) (Cell, []CallPolymorphicEntry, bool) {
	if current.SlotKind != SlotInvalid && current.SlotKind != slotKind {
		return current, nil, false
	}
	accessKind, targetRef, eligible := AccessKindForCallTarget(originalCallee, resolvedCallee)
	candidate := NewCallMonomorphicCell(slotKind, accessKind, targetRef, originalCallee.Bits(), shape)
	candidateEntry := NewCallPolymorphicEntry(accessKind, targetRef, originalCallee.Bits(), shape)
	if !eligible {
		switch current.State {
		case StateMonomorphic, StatePolymorphic:
			next := NewMegamorphicCell(slotKind)
			return next, nil, next != current
		case StateMegamorphic:
			next := NewMegamorphicCell(slotKind)
			return next, nil, next != current
		default:
			return current, nil, false
		}
	}
	switch current.State {
	case StateGeneric:
		return candidate, nil, candidate != current
	case StateMonomorphic:
		if current.ValueBits == candidate.ValueBits && current.SlotKind == candidate.SlotKind {
			return candidate, nil, candidate != current
		}
		return NewCallPolymorphicCell(slotKind, 0), []CallPolymorphicEntry{callEntryFromCell(current), candidateEntry}, true
	case StatePolymorphic:
		if len(currentPoly) != CallPolymorphicEntryCount {
			next := NewMegamorphicCell(slotKind)
			return next, nil, next != current
		}
		entries := append([]CallPolymorphicEntry(nil), currentPoly...)
		for index := range entries {
			if entries[index].ValueBits != candidateEntry.ValueBits {
				continue
			}
			entries[index] = candidateEntry
			if callPolyEntriesEqual(entries, currentPoly) {
				return current, nil, false
			}
			return current, entries, true
		}
		next := NewMegamorphicCallSidecarCell(slotKind, 0)
		return next, nextMegamorphicCallEntries(entries, candidateEntry), true
	case StateMegamorphic:
		if current.HasMegamorphicCallSidecar() {
			entries := compactCallEntries(currentPoly)
			updatedEntries := nextMegamorphicCallEntries(entries, candidateEntry)
			if callPolyEntriesEqual(entries, updatedEntries) {
				return current, nil, false
			}
			next := NewMegamorphicCallSidecarCell(slotKind, 0)
			return next, updatedEntries, true
		}
		if current.ValueBits == candidate.ValueBits && current.SlotKind == candidate.SlotKind {
			next := NewMegamorphicCallCell(slotKind, accessKind, targetRef, originalCallee.Bits(), shape)
			return next, nil, next != current
		}
		next := NewMegamorphicCallSidecarCell(slotKind, 0)
		return next, nextMegamorphicCallEntries([]CallPolymorphicEntry{callEntryFromCell(current)}, candidateEntry), true
	default:
		return current, nil, false
	}
}

func NextUpvalueCell(current Cell, slotKind SlotKind, upvalueRef value.HeapRef44, upvalueState rtupvalue.State, observedBits value.Raw) (Cell, bool) {
	accessKind := AccessKindForUpvalueState(upvalueState)
	eligible := accessKind != AccessInvalid && upvalueRef != 0
	candidate := NewUpvalueMonomorphicCell(slotKind, accessKind, upvalueRef, observedBits)
	return nextCell(current, slotKind, eligible, candidate, func(current Cell, candidate Cell) bool {
		return current.TargetRef() == candidate.TargetRef() && current.SlotKind == candidate.SlotKind
	})
}

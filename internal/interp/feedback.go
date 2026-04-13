package interp

import (
	"fmt"

	"vexlua/internal/bytecode"

	"vexlua/internal/runtime/feedback"
	rttable "vexlua/internal/runtime/table"
	"vexlua/internal/runtime/value"
)

func (engine *Engine) recordGetFeedback(act *activation, pc int, kind feedback.SlotKind, tableValue value.TValue, key value.TValue) {
	engine.recordActivationTableFeedback(act, pc, kind, tableValue, key, value.NilValue(), false)
}

func (engine *Engine) recordSetFeedback(act *activation, pc int, kind feedback.SlotKind, tableValue value.TValue, key value.TValue, slotValue value.TValue) {
	engine.recordActivationTableFeedback(act, pc, kind, tableValue, key, slotValue, true)
}

func (engine *Engine) recordCallFeedback(act *activation, pc int, kind feedback.SlotKind, callee value.TValue) {
	if act == nil {
		return
	}
	resolvedCallee, _, err := engine.ResolveCallBoundary(callee, nil)
	if err != nil {
		return
	}
	closureRef, err := engine.activationClosureRef(act)
	if err != nil {
		return
	}
	proto, err := engine.activationProto(act)
	if err != nil {
		return
	}
	engine.UpdateCallFeedbackAtPC(closureRef, proto, pc, kind, callee, resolvedCallee)
}

func (engine *Engine) recordUpvalueFeedback(act *activation, pc int, kind feedback.SlotKind, upvalueRef value.HeapRef44, observed value.TValue) {
	if act == nil {
		return
	}
	closureRef, err := engine.activationClosureRef(act)
	if err != nil {
		return
	}
	proto, err := engine.activationProto(act)
	if err != nil {
		return
	}
	engine.UpdateUpvalueFeedbackAtPC(closureRef, proto, pc, kind, upvalueRef, observed)
}

func (engine *Engine) recordActivationTableFeedback(act *activation, pc int, kind feedback.SlotKind, tableValue value.TValue, key value.TValue, slotValue value.TValue, isStore bool) {
	if act == nil {
		return
	}
	closureRef, err := engine.activationClosureRef(act)
	if err != nil {
		return
	}
	proto, err := engine.activationProto(act)
	if err != nil {
		return
	}
	engine.UpdateTableFeedbackAtPC(closureRef, proto, pc, kind, tableValue, key, slotValue, isStore)
}

func (engine *Engine) UpdateTableFeedbackAtPC(closureRef value.HeapRef44, proto *bytecode.Proto, pc int, kind feedback.SlotKind, tableValue value.TValue, key value.TValue, slotValue value.TValue, isStore bool) {
	engine.updateFeedbackAtPC(closureRef, proto, pc, kind, func(slotIndex uint32) {
		engine.UpdateTableFeedbackAtSlot(closureRef, kind, slotIndex, tableValue, key, slotValue, isStore)
	})
}

func (engine *Engine) UpdateTableFeedbackAtSlot(closureRef value.HeapRef44, kind feedback.SlotKind, slotIndex uint32, tableValue value.TValue, key value.TValue, slotValue value.TValue, isStore bool) {
	access, eligible, err := engine.describeFeedbackAccess(tableValue, key, slotValue, isStore)
	if err != nil {
		return
	}
	engine.updateFeedbackAtSlot(closureRef, kind, slotIndex, func(current feedback.Cell) (feedback.Cell, bool) {
		return feedback.NextTableCell(current, kind, access, eligible, key.Bits())
	})
}

func (engine *Engine) UpdateCallFeedbackAtPC(closureRef value.HeapRef44, proto *bytecode.Proto, pc int, kind feedback.SlotKind, callee value.TValue, resolvedCallee value.TValue) {
	engine.updateFeedbackAtPC(closureRef, proto, pc, kind, func(slotIndex uint32) {
		engine.UpdateCallFeedbackAtSlot(closureRef, kind, slotIndex, callee, resolvedCallee)
	})
}

func (engine *Engine) UpdateCallFeedbackAtSlot(closureRef value.HeapRef44, kind feedback.SlotKind, slotIndex uint32, callee value.TValue, resolvedCallee value.TValue) {
	shape, err := engine.describeCallShape(callee)
	if err != nil {
		return
	}
	closureObject, err := engine.Closures.Object(closureRef)
	if err != nil || closureObject.FeedbackData == 0 || slotIndex >= closureObject.FeedbackSize {
		return
	}
	current, err := engine.Closures.ReadFeedbackCell(closureRef, slotIndex)
	if err != nil || current.SlotKind != kind {
		return
	}
	var currentPoly []feedback.CallPolymorphicEntry
	if current.State == feedback.StatePolymorphic {
		currentPoly, err = engine.readCallPolymorphicEntries(current.CallPolymorphicDataOffset())
		if err != nil {
			return
		}
	}
	if current.HasMegamorphicCallSidecar() {
		currentPoly, err = engine.readCallMegamorphicEntries(current.CallMegamorphicDataOffset())
		if err != nil {
			return
		}
	}
	next, nextPoly, changed := feedback.NextCallTransition(current, currentPoly, kind, callee, resolvedCallee, shape)
	if !changed {
		return
	}
	allocatedOffset := value.HeapOff64(0)
	allocatedNew := false
	if next.State == feedback.StatePolymorphic {
		if current.State == feedback.StatePolymorphic && current.CallPolymorphicDataOffset() != 0 {
			allocatedOffset = current.CallPolymorphicDataOffset()
			if err := engine.writeCallPolymorphicEntries(allocatedOffset, nextPoly); err != nil {
				return
			}
		} else {
			allocatedOffset, err = engine.allocCallPolymorphicEntries(closureRef, nextPoly)
			if err != nil {
				return
			}
			allocatedNew = true
		}
		next.HeapRef = value.HeapRef44(allocatedOffset)
	}
	if next.HasMegamorphicCallSidecar() {
		if current.HasMegamorphicCallSidecar() && current.CallMegamorphicDataOffset() != 0 {
			allocatedOffset = current.CallMegamorphicDataOffset()
			if err := engine.writeCallMegamorphicEntries(allocatedOffset, nextPoly); err != nil {
				return
			}
		} else {
			allocatedOffset, err = engine.allocCallMegamorphicEntries(closureRef, nextPoly)
			if err != nil {
				return
			}
			allocatedNew = true
		}
		next.HeapRef = value.HeapRef44(allocatedOffset)
	}
	if err := engine.Closures.WriteFeedbackCell(closureRef, slotIndex, next); err != nil {
		if allocatedNew && allocatedOffset != 0 {
			_ = engine.Heap.FreeSpan(allocatedOffset)
		}
	}
}

func (engine *Engine) UpdateUpvalueFeedbackAtPC(closureRef value.HeapRef44, proto *bytecode.Proto, pc int, kind feedback.SlotKind, upvalueRef value.HeapRef44, observed value.TValue) {
	engine.updateFeedbackAtPC(closureRef, proto, pc, kind, func(slotIndex uint32) {
		engine.UpdateUpvalueFeedbackAtSlot(closureRef, kind, slotIndex, upvalueRef, observed)
	})
}

func (engine *Engine) UpdateUpvalueFeedbackAtSlot(closureRef value.HeapRef44, kind feedback.SlotKind, slotIndex uint32, upvalueRef value.HeapRef44, observed value.TValue) {
	upvalueObject, err := engine.Upvalues.Object(upvalueRef)
	if err != nil {
		return
	}
	engine.updateFeedbackAtSlot(closureRef, kind, slotIndex, func(current feedback.Cell) (feedback.Cell, bool) {
		return feedback.NextUpvalueCell(current, kind, upvalueRef, upvalueObject.State, observed.Bits())
	})
}

func (engine *Engine) updateFeedbackAtPC(closureRef value.HeapRef44, proto *bytecode.Proto, pc int, kind feedback.SlotKind, apply func(slotIndex uint32)) {
	slot, slotIndex, ok := feedback.SlotInfoForProtoPC(proto, pc)
	if !ok || slot.Kind != kind {
		return
	}
	apply(slotIndex)
}

func (engine *Engine) updateFeedbackAtSlot(closureRef value.HeapRef44, kind feedback.SlotKind, slotIndex uint32, update func(current feedback.Cell) (feedback.Cell, bool)) {
	closureObject, err := engine.Closures.Object(closureRef)
	if err != nil || closureObject.FeedbackData == 0 || slotIndex >= closureObject.FeedbackSize {
		return
	}
	current, err := engine.Closures.ReadFeedbackCell(closureRef, slotIndex)
	if err != nil || current.SlotKind != kind {
		return
	}
	next, changed := update(current)
	if changed {
		_ = engine.Closures.WriteFeedbackCell(closureRef, slotIndex, next)
	}
}

func (engine *Engine) describeFeedbackAccess(tableValue value.TValue, key value.TValue, slotValue value.TValue, isStore bool) (rttable.FastAccess, bool, error) {
	if !tableValue.IsBoxedTag(value.TagTableRef) {
		return rttable.FastAccess{}, false, nil
	}
	ref, _ := tableValue.HeapRef()
	if isStore {
		access, ok, err := engine.Tables.DescribeFastSet(ref, key, slotValue)
		return access, ok, err
	}
	access, ok, err := engine.Tables.DescribeFastGet(ref, key)
	return access, ok, err
}

func (engine *Engine) readCallPolymorphicEntries(offset value.HeapOff64) ([]feedback.CallPolymorphicEntry, error) {
	if engine == nil || engine.Heap == nil || offset == 0 {
		return nil, fmt.Errorf("call polymorphic offset cannot be zero")
	}
	bytes, err := engine.Heap.Resolve(offset, feedback.CallPolymorphicDataSize)
	if err != nil {
		return nil, err
	}
	entries, err := feedback.ReadCallPolymorphicEntries(bytes)
	if err != nil {
		return nil, err
	}
	return entries[:], nil
}

func (engine *Engine) writeCallPolymorphicEntries(offset value.HeapOff64, entries []feedback.CallPolymorphicEntry) error {
	if engine == nil || engine.Heap == nil || offset == 0 {
		return fmt.Errorf("call polymorphic offset cannot be zero")
	}
	if len(entries) != feedback.CallPolymorphicEntryCount {
		return fmt.Errorf("call polymorphic entry count = %d, want %d", len(entries), feedback.CallPolymorphicEntryCount)
	}
	bytes, err := engine.Heap.Resolve(offset, feedback.CallPolymorphicDataSize)
	if err != nil {
		return err
	}
	return feedback.WriteCallPolymorphicEntries(bytes, feedbackSliceToCallPolyArray(entries))
}

func (engine *Engine) readCallMegamorphicEntries(offset value.HeapOff64) ([]feedback.CallPolymorphicEntry, error) {
	if engine == nil || engine.Heap == nil || offset == 0 {
		return nil, fmt.Errorf("call megamorphic offset cannot be zero")
	}
	bytes, err := engine.Heap.Resolve(offset, feedback.CallMegamorphicDataSize)
	if err != nil {
		return nil, err
	}
	entries, err := feedback.ReadCallMegamorphicEntries(bytes)
	if err != nil {
		return nil, err
	}
	compact := make([]feedback.CallPolymorphicEntry, 0, feedback.CallMegamorphicEntryCount)
	for _, entry := range entries {
		if entry.AccessKind == feedback.AccessInvalid || entry.ValueBits == 0 {
			continue
		}
		compact = append(compact, entry)
	}
	return compact, nil
}

func (engine *Engine) writeCallMegamorphicEntries(offset value.HeapOff64, entries []feedback.CallPolymorphicEntry) error {
	if engine == nil || engine.Heap == nil || offset == 0 {
		return fmt.Errorf("call megamorphic offset cannot be zero")
	}
	if len(entries) > feedback.CallMegamorphicEntryCount {
		return fmt.Errorf("call megamorphic entry count = %d, want <= %d", len(entries), feedback.CallMegamorphicEntryCount)
	}
	bytes, err := engine.Heap.Resolve(offset, feedback.CallMegamorphicDataSize)
	if err != nil {
		return err
	}
	return feedback.WriteCallMegamorphicEntries(bytes, feedbackSliceToCallMegaArray(entries))
}

func (engine *Engine) allocCallPolymorphicEntries(closureRef value.HeapRef44, entries []feedback.CallPolymorphicEntry) (value.HeapOff64, error) {
	if len(entries) != feedback.CallPolymorphicEntryCount {
		return 0, fmt.Errorf("call polymorphic entry count = %d, want %d", len(entries), feedback.CallPolymorphicEntryCount)
	}
	offset, bytes, err := engine.Closures.AllocFeedbackPayload(closureRef, feedback.CallPolymorphicDataSize)
	if err != nil {
		return 0, err
	}
	if err := feedback.WriteCallPolymorphicEntries(bytes, feedbackSliceToCallPolyArray(entries)); err != nil {
		_ = engine.Heap.FreeSpan(offset)
		return 0, err
	}
	return offset, nil
}

func (engine *Engine) allocCallMegamorphicEntries(closureRef value.HeapRef44, entries []feedback.CallPolymorphicEntry) (value.HeapOff64, error) {
	if len(entries) > feedback.CallMegamorphicEntryCount {
		return 0, fmt.Errorf("call megamorphic entry count = %d, want <= %d", len(entries), feedback.CallMegamorphicEntryCount)
	}
	offset, bytes, err := engine.Closures.AllocFeedbackPayload(closureRef, feedback.CallMegamorphicDataSize)
	if err != nil {
		return 0, err
	}
	if err := feedback.WriteCallMegamorphicEntries(bytes, feedbackSliceToCallMegaArray(entries)); err != nil {
		_ = engine.Heap.FreeSpan(offset)
		return 0, err
	}
	return offset, nil
}

func feedbackSliceToCallPolyArray(entries []feedback.CallPolymorphicEntry) [feedback.CallPolymorphicEntryCount]feedback.CallPolymorphicEntry {
	var fixed [feedback.CallPolymorphicEntryCount]feedback.CallPolymorphicEntry
	copy(fixed[:], entries)
	return fixed
}

func feedbackSliceToCallMegaArray(entries []feedback.CallPolymorphicEntry) [feedback.CallMegamorphicEntryCount]feedback.CallPolymorphicEntry {
	var fixed [feedback.CallMegamorphicEntryCount]feedback.CallPolymorphicEntry
	copy(fixed[:], entries)
	return fixed
}

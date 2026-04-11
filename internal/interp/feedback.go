package interp

import (
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
	slot, slotIndex, ok := feedback.SlotInfoForProtoPC(proto, pc)
	if !ok || slot.Kind != kind {
		return
	}
	engine.UpdateTableFeedbackAtSlot(closureRef, kind, slotIndex, tableValue, key, slotValue, isStore)
}

func (engine *Engine) UpdateTableFeedbackAtSlot(closureRef value.HeapRef44, kind feedback.SlotKind, slotIndex uint32, tableValue value.TValue, key value.TValue, slotValue value.TValue, isStore bool) {
	closureObject, err := engine.Closures.Object(closureRef)
	if err != nil || closureObject.FeedbackData == 0 || slotIndex >= closureObject.FeedbackSize {
		return
	}
	current, err := engine.Closures.ReadFeedbackCell(closureRef, slotIndex)
	if err != nil {
		return
	}
	if current.SlotKind != kind {
		return
	}
	access, eligible, err := engine.describeFeedbackAccess(tableValue, key, slotValue, isStore)
	if err != nil {
		return
	}
	next, changed := feedback.NextTableCell(current, kind, access, eligible, key.Bits())
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

package gc

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/runtime/closure"
	"vexlua/internal/runtime/feedback"
	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/host"
	rproto "vexlua/internal/runtime/proto"
	rttable "vexlua/internal/runtime/table"
	"vexlua/internal/runtime/upvalue"
	"vexlua/internal/runtime/value"
)

type WeakRefKind uint8

const (
	WeakRefFeedbackCellHeapRef WeakRefKind = iota + 1
	WeakRefFeedbackCellValueBits
	WeakRefWeakTableKey
	WeakRefWeakTableValue
)

type WeakRef struct {
	Kind   WeakRefKind
	Owner  value.HeapRef44
	Target value.HeapRef44
	Slot   uint32
}

type WeakVisitFunc func(WeakRef) error

type Tracer struct {
	heap  *heap.Heap
	hosts *host.Registry
}

func NewTracer(runtimeHeap *heap.Heap, hosts *host.Registry) *Tracer {
	return &Tracer{heap: runtimeHeap, hosts: hosts}
}

func (tracer *Tracer) TraceObject(ref value.HeapRef44, visitStrong VisitFunc, visitWeak WeakVisitFunc) error {
	common, bytes, err := tracer.objectBytes(ref)
	if err != nil {
		return err
	}
	switch common.Kind {
	case value.KindString, value.KindHostDescriptor:
		return nil
	case value.KindTable:
		return tracer.traceTable(ref, bytes, visitStrong, visitWeak)
	case value.KindLuaClosure:
		return tracer.traceClosure(ref, bytes, visitStrong, visitWeak)
	case value.KindProto:
		return tracer.traceProto(bytes, visitStrong)
	case value.KindUpValue:
		return tracer.traceUpvalue(bytes, visitStrong)
	case value.KindHostObject:
		return tracer.traceHostObject(ref, visitStrong)
	case value.KindHostFunction:
		return tracer.traceHostFunction(ref, visitStrong)
	default:
		return fmt.Errorf("gc tracer does not support object kind %s", common.Kind)
	}
}

func (tracer *Tracer) ScrubDeadFeedbackCells(closureRef value.HeapRef44, isDead func(value.HeapRef44) bool) (int, error) {
	if isDead == nil {
		return 0, fmt.Errorf("dead predicate cannot be nil")
	}
	_, bytes, err := tracer.objectBytes(closureRef)
	if err != nil {
		return 0, err
	}
	object, err := closure.ReadObject(bytes)
	if err != nil {
		return 0, err
	}
	if object.FeedbackData == 0 || object.FeedbackSize == 0 {
		return 0, nil
	}
	scrubbed := 0
	for slot := uint32(0); slot < object.FeedbackSize; slot++ {
		cellOffset := object.FeedbackData + value.HeapOff64(feedback.CellOffset(slot))
		cellBytes, err := tracer.heap.Resolve(cellOffset, feedback.CellSize)
		if err != nil {
			return scrubbed, err
		}
		cell, err := feedback.ReadCell(cellBytes)
		if err != nil {
			return scrubbed, err
		}
		if cell.State == feedback.StatePolymorphic {
			entries, err := tracer.readCallPolymorphicEntries(cell.CallPolymorphicDataOffset())
			if err != nil {
				return scrubbed, err
			}
			live := make([]feedback.CallPolymorphicEntry, 0, feedback.CallPolymorphicEntryCount)
			for _, entry := range entries {
				if callPolymorphicEntryDead(entry, isDead) {
					continue
				}
				live = append(live, entry)
			}
			if len(live) == feedback.CallPolymorphicEntryCount {
				continue
			}
			if len(live) == 1 {
				if err := feedback.WriteCell(cellBytes, live[0].MonomorphicCell(cell.SlotKind)); err != nil {
					return scrubbed, err
				}
			} else {
				if err := feedback.WriteCell(cellBytes, feedback.NewGenericCell(cell.SlotKind)); err != nil {
					return scrubbed, err
				}
			}
			if offset := cell.CallPolymorphicDataOffset(); offset != 0 {
				if err := tracer.heap.FreeSpan(offset); err != nil {
					return scrubbed, err
				}
			}
			scrubbed++
			continue
		}
		if cell.HasMegamorphicCallSidecar() {
			entries, err := tracer.readCallMegamorphicEntries(cell.CallMegamorphicDataOffset())
			if err != nil {
				return scrubbed, err
			}
			live := make([]feedback.CallPolymorphicEntry, 0, feedback.CallMegamorphicEntryCount)
			for _, entry := range entries {
				if callPolymorphicEntryDead(entry, isDead) {
					continue
				}
				live = append(live, entry)
			}
			if len(live) == len(entries) {
				continue
			}
			switch len(live) {
			case 0:
				if err := feedback.WriteCell(cellBytes, feedback.NewGenericCell(cell.SlotKind)); err != nil {
					return scrubbed, err
				}
				if offset := cell.CallMegamorphicDataOffset(); offset != 0 {
					if err := tracer.heap.FreeSpan(offset); err != nil {
						return scrubbed, err
					}
				}
			case 1:
				if err := feedback.WriteCell(cellBytes, live[0].MonomorphicCell(cell.SlotKind)); err != nil {
					return scrubbed, err
				}
				if offset := cell.CallMegamorphicDataOffset(); offset != 0 {
					if err := tracer.heap.FreeSpan(offset); err != nil {
						return scrubbed, err
					}
				}
			default:
				if err := tracer.writeCallMegamorphicEntries(cell.CallMegamorphicDataOffset(), live); err != nil {
					return scrubbed, err
				}
			}
			scrubbed++
			continue
		}
		if !feedbackCellDead(cell, isDead) {
			continue
		}
		if err := feedback.WriteCell(cellBytes, feedback.NewGenericCell(cell.SlotKind)); err != nil {
			return scrubbed, err
		}
		scrubbed++
	}
	return scrubbed, nil
}

func (tracer *Tracer) ScrubDeadWeakTableEntries(tableRef value.HeapRef44, isDead func(value.HeapRef44) bool) (int, error) {
	if isDead == nil {
		return 0, fmt.Errorf("dead predicate cannot be nil")
	}
	_, bytes, err := tracer.objectBytes(tableRef)
	if err != nil {
		return 0, err
	}
	object, err := rttable.ReadObject(bytes)
	if err != nil {
		return 0, err
	}
	if !object.Flags.Has(rttable.FlagWeakKeys) && !object.Flags.Has(rttable.FlagWeakValues) {
		return 0, nil
	}
	removed := 0
	changed := false
	if object.Flags.Has(rttable.FlagWeakValues) && object.ArrayCap > 0 && object.ArrayData != 0 {
		arrayBytes, err := tracer.heap.Resolve(object.ArrayData, uint64(object.ArrayCap)*value.TValueSize)
		if err != nil {
			return removed, err
		}
		for index := uint32(0); index < object.ArrayCap; index++ {
			start := index * value.TValueSize
			slotValue := readTValue(arrayBytes[start : start+value.TValueSize])
			dead, err := tvalueDead(slotValue, isDead)
			if err != nil {
				return removed, err
			}
			if !dead {
				continue
			}
			writeTValue(arrayBytes[start:start+value.TValueSize], value.NilValue())
			removed++
			changed = true
		}
		if changed {
			object.ArrayLenHint = weakArrayLenHint(arrayBytes, object.ArrayCap)
		}
	}
	if object.HashCapacity > 0 && object.CtrlData != 0 && object.EntriesData != 0 {
		ctrlBytes, err := tracer.heap.Resolve(object.CtrlData, uint64(object.HashCapacity)+1)
		if err != nil {
			return removed, err
		}
		entriesBytes, err := tracer.heap.Resolve(object.EntriesData, uint64(object.HashCapacity)*rttable.EntrySize)
		if err != nil {
			return removed, err
		}
		for slot := uint32(0); slot < object.HashCapacity; slot++ {
			ctrl := ctrlBytes[slot]
			if ctrl == rttable.CtrlEmpty || ctrl == rttable.CtrlDeleted || ctrl == rttable.CtrlSentinel {
				continue
			}
			start := slot * rttable.EntrySize
			entry, err := rttable.ReadEntry(entriesBytes[start : start+rttable.EntrySize])
			if err != nil {
				return removed, err
			}
			remove := false
			if object.Flags.Has(rttable.FlagWeakKeys) {
				dead, err := tvalueDead(entry.Key, isDead)
				if err != nil {
					return removed, err
				}
				remove = remove || dead
			}
			if object.Flags.Has(rttable.FlagWeakValues) {
				dead, err := tvalueDead(entry.Value, isDead)
				if err != nil {
					return removed, err
				}
				remove = remove || dead
			}
			if !remove {
				continue
			}
			ctrlBytes[slot] = rttable.CtrlDeleted
			if err := rttable.WriteEntry(entriesBytes[start:start+rttable.EntrySize], rttable.Entry{Key: value.NilValue(), Value: value.NilValue()}); err != nil {
				return removed, err
			}
			if object.HashCount > 0 {
				object.HashCount--
			}
			removed++
			changed = true
		}
		if changed {
			object.GrowthLeft = weakRemainingGrowth(object.HashCapacity, object.HashCount)
		}
	}
	if !changed {
		return removed, nil
	}
	object.BumpVersion()
	object.SyncLayoutFlags()
	if err := rttable.WriteObject(bytes, object); err != nil {
		return removed, err
	}
	return removed, nil
}

func (tracer *Tracer) traceTable(tableRef value.HeapRef44, bytes []byte, visitStrong VisitFunc, visitWeak WeakVisitFunc) error {
	object, err := rttable.ReadObject(bytes)
	if err != nil {
		return err
	}
	if err := visitStrongTValue(object.Metatable, visitStrong); err != nil {
		return err
	}
	if object.ArrayCap > 0 && object.ArrayData != 0 {
		arrayBytes, err := tracer.heap.Resolve(object.ArrayData, uint64(object.ArrayCap)*value.TValueSize)
		if err != nil {
			return err
		}
		for index := uint32(0); index < object.ArrayCap; index++ {
			slotValue := readTValue(arrayBytes[index*value.TValueSize : (index+1)*value.TValueSize])
			if object.Flags.Has(rttable.FlagWeakValues) {
				if err := visitWeakTableTValue(slotValue, WeakRef{Kind: WeakRefWeakTableValue, Owner: tableRef, Slot: index}, visitStrong, visitWeak); err != nil {
					return err
				}
				continue
			}
			if err := visitStrongTValue(slotValue, visitStrong); err != nil {
				return err
			}
		}
	}
	if object.HashCapacity == 0 || object.CtrlData == 0 || object.EntriesData == 0 {
		return nil
	}
	ctrlBytes, err := tracer.heap.Resolve(object.CtrlData, uint64(object.HashCapacity)+1)
	if err != nil {
		return err
	}
	entriesBytes, err := tracer.heap.Resolve(object.EntriesData, uint64(object.HashCapacity)*rttable.EntrySize)
	if err != nil {
		return err
	}
	for slot := uint32(0); slot < object.HashCapacity; slot++ {
		ctrl := ctrlBytes[slot]
		if ctrl == rttable.CtrlEmpty || ctrl == rttable.CtrlDeleted || ctrl == rttable.CtrlSentinel {
			continue
		}
		entry, err := rttable.ReadEntry(entriesBytes[slot*rttable.EntrySize : (slot+1)*rttable.EntrySize])
		if err != nil {
			return err
		}
		if object.Flags.Has(rttable.FlagWeakKeys) {
			if err := visitWeakTableTValue(entry.Key, WeakRef{Kind: WeakRefWeakTableKey, Owner: tableRef, Slot: slot}, visitStrong, visitWeak); err != nil {
				return err
			}
		} else if err := visitStrongTValue(entry.Key, visitStrong); err != nil {
			return err
		}
		if object.Flags.Has(rttable.FlagWeakValues) {
			if err := visitWeakTableTValue(entry.Value, WeakRef{Kind: WeakRefWeakTableValue, Owner: tableRef, Slot: slot}, visitStrong, visitWeak); err != nil {
				return err
			}
		} else if err := visitStrongTValue(entry.Value, visitStrong); err != nil {
			return err
		}
	}
	return nil
}

func (tracer *Tracer) traceClosure(closureRef value.HeapRef44, bytes []byte, visitStrong VisitFunc, visitWeak WeakVisitFunc) error {
	object, err := closure.ReadObject(bytes)
	if err != nil {
		return err
	}
	if err := visitStrongTValue(object.Proto, visitStrong); err != nil {
		return err
	}
	if err := visitStrongTValue(object.Env, visitStrong); err != nil {
		return err
	}
	if object.UpvalueCount > 0 && object.UpvaluesData != 0 {
		upvalueBytes, err := tracer.heap.Resolve(object.UpvaluesData, uint64(object.UpvalueCount)*8)
		if err != nil {
			return err
		}
		for index := uint16(0); index < object.UpvalueCount; index++ {
			ref := value.HeapRef44(binary.LittleEndian.Uint64(upvalueBytes[index*8 : (index+1)*8]))
			if ref == 0 || visitStrong == nil {
				continue
			}
			if err := visitStrong(ref); err != nil {
				return err
			}
		}
	}
	if object.FeedbackData == 0 || object.FeedbackSize == 0 || visitWeak == nil {
		return nil
	}
	for slot := uint32(0); slot < object.FeedbackSize; slot++ {
		cellBytes, err := tracer.heap.Resolve(object.FeedbackData+value.HeapOff64(feedback.CellOffset(slot)), feedback.CellSize)
		if err != nil {
			return err
		}
		cell, err := feedback.ReadCell(cellBytes)
		if err != nil {
			return err
		}
		if cell.State == feedback.StatePolymorphic {
			entries, err := tracer.readCallPolymorphicEntries(cell.CallPolymorphicDataOffset())
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.TargetRef != 0 {
					if err := visitWeak(WeakRef{Kind: WeakRefFeedbackCellHeapRef, Owner: closureRef, Target: entry.TargetRef, Slot: slot}); err != nil {
						return err
					}
				}
				if err := visitWeakTValue(value.FromRaw(entry.ValueBits), WeakRef{Kind: WeakRefFeedbackCellValueBits, Owner: closureRef, Slot: slot}, visitWeak); err != nil {
					return err
				}
			}
			continue
		}
		if cell.HasMegamorphicCallSidecar() {
			entries, err := tracer.readCallMegamorphicEntries(cell.CallMegamorphicDataOffset())
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.TargetRef != 0 {
					if err := visitWeak(WeakRef{Kind: WeakRefFeedbackCellHeapRef, Owner: closureRef, Target: entry.TargetRef, Slot: slot}); err != nil {
						return err
					}
				}
				if err := visitWeakTValue(value.FromRaw(entry.ValueBits), WeakRef{Kind: WeakRefFeedbackCellValueBits, Owner: closureRef, Slot: slot}, visitWeak); err != nil {
					return err
				}
			}
			continue
		}
		if cell.HeapRef != 0 {
			if err := visitWeak(WeakRef{Kind: WeakRefFeedbackCellHeapRef, Owner: closureRef, Target: cell.HeapRef, Slot: slot}); err != nil {
				return err
			}
		}
		if err := visitWeakTValue(value.FromRaw(cell.ValueBits), WeakRef{Kind: WeakRefFeedbackCellValueBits, Owner: closureRef, Slot: slot}, visitWeak); err != nil {
			return err
		}
	}
	return nil
}

func (tracer *Tracer) traceProto(bytes []byte, visitStrong VisitFunc) error {
	object, err := rproto.ReadObject(bytes)
	if err != nil {
		return err
	}
	if object.ConstantCount > 0 && object.ConstBasePtr != 0 {
		constBytes, err := tracer.resolveNativeTValues(object.ConstBasePtr, object.ConstantCount)
		if err != nil {
			return err
		}
		for index := uint32(0); index < object.ConstantCount; index++ {
			slotValue := readTValue(constBytes[index*value.TValueSize : (index+1)*value.TValueSize])
			if err := visitStrongTValue(slotValue, visitStrong); err != nil {
				return err
			}
		}
	}
	if object.ProtoCount == 0 || object.ChildProtoData == 0 || visitStrong == nil {
		return nil
	}
	childBytes, err := tracer.heap.Resolve(object.ChildProtoData, uint64(object.ProtoCount)*8)
	if err != nil {
		return err
	}
	for index := uint16(0); index < object.ProtoCount; index++ {
		ref := value.HeapRef44(binary.LittleEndian.Uint64(childBytes[index*8 : (index+1)*8]))
		if ref == 0 {
			continue
		}
		if err := visitStrong(ref); err != nil {
			return err
		}
	}
	return nil
}

func (tracer *Tracer) traceUpvalue(bytes []byte, visitStrong VisitFunc) error {
	object, err := upvalue.ReadObject(bytes)
	if err != nil {
		return err
	}
	if object.State != upvalue.StateClosed {
		return nil
	}
	return visitStrongTValue(object.ClosedValue, visitStrong)
}

func (tracer *Tracer) traceHostObject(ref value.HeapRef44, visitStrong VisitFunc) error {
	if tracer.hosts == nil {
		return fmt.Errorf("host registry is required to trace host object %#x", uint64(ref))
	}
	header, _, _, err := tracer.hosts.ReadHostObject(ref)
	if err != nil {
		return err
	}
	return tracer.traceHostWrapper(header, visitStrong)
}

func (tracer *Tracer) traceHostFunction(ref value.HeapRef44, visitStrong VisitFunc) error {
	if tracer.hosts == nil {
		return fmt.Errorf("host registry is required to trace host function %#x", uint64(ref))
	}
	header, _, _, err := tracer.hosts.ReadHostFunction(ref)
	if err != nil {
		return err
	}
	return tracer.traceHostWrapper(header, visitStrong)
}

func (tracer *Tracer) traceHostWrapper(header host.WrapperHeader, visitStrong VisitFunc) error {
	if err := visitStrongTValue(header.Env, visitStrong); err != nil {
		return err
	}
	if err := visitStrongTValue(header.Metatable, visitStrong); err != nil {
		return err
	}
	if header.NativeMeta == 0 || visitStrong == nil {
		return nil
	}
	address, err := tracer.heap.AddressForOffset(header.NativeMeta)
	if err != nil {
		return err
	}
	ref, err := tracer.heap.EncodeHeapRef(address)
	if err != nil {
		return err
	}
	return visitStrong(ref)
}

func (tracer *Tracer) objectBytes(ref value.HeapRef44) (value.CommonHeader, []byte, error) {
	address, err := tracer.heap.DecodeHeapRef(ref)
	if err != nil {
		return value.CommonHeader{}, nil, err
	}
	offset, err := tracer.heap.OffsetForAddress(address)
	if err != nil {
		return value.CommonHeader{}, nil, err
	}
	common, err := tracer.heap.HeaderAtOffset(offset)
	if err != nil {
		return value.CommonHeader{}, nil, err
	}
	bytes, err := tracer.heap.Resolve(offset, uint64(common.SizeBytes))
	if err != nil {
		return value.CommonHeader{}, nil, err
	}
	return common, bytes, nil
}

func (tracer *Tracer) resolveNativeTValues(base uint64, count uint32) ([]byte, error) {
	nativeBase := tracer.heap.NativeBase()
	if uintptr(base) < nativeBase {
		return nil, fmt.Errorf("native data base %#x precedes heap native base %#x", base, nativeBase)
	}
	offset := value.HeapOff64(uint64(uintptr(base) - nativeBase))
	return tracer.heap.Resolve(offset, uint64(count)*value.TValueSize)
}

func feedbackCellDead(cell feedback.Cell, isDead func(value.HeapRef44) bool) bool {
	if cell.State == feedback.StatePolymorphic || cell.HasMegamorphicCallSidecar() {
		return false
	}
	if cell.HeapRef != 0 && isDead(cell.HeapRef) {
		return true
	}
	ref, ok := value.FromRaw(cell.ValueBits).HeapRef()
	return ok && ref != 0 && isDead(ref)
}

func callPolymorphicEntryDead(entry feedback.CallPolymorphicEntry, isDead func(value.HeapRef44) bool) bool {
	if entry.TargetRef != 0 && isDead(entry.TargetRef) {
		return true
	}
	ref, ok := value.FromRaw(entry.ValueBits).HeapRef()
	return ok && ref != 0 && isDead(ref)
}

func (tracer *Tracer) readCallPolymorphicEntries(offset value.HeapOff64) ([]feedback.CallPolymorphicEntry, error) {
	if offset == 0 {
		return nil, fmt.Errorf("call polymorphic offset cannot be zero")
	}
	bytes, err := tracer.heap.Resolve(offset, feedback.CallPolymorphicDataSize)
	if err != nil {
		return nil, err
	}
	entries, err := feedback.ReadCallPolymorphicEntries(bytes)
	if err != nil {
		return nil, err
	}
	return entries[:], nil
}

func (tracer *Tracer) readCallMegamorphicEntries(offset value.HeapOff64) ([]feedback.CallPolymorphicEntry, error) {
	if offset == 0 {
		return nil, fmt.Errorf("call megamorphic offset cannot be zero")
	}
	bytes, err := tracer.heap.Resolve(offset, feedback.CallMegamorphicDataSize)
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

func (tracer *Tracer) writeCallMegamorphicEntries(offset value.HeapOff64, entries []feedback.CallPolymorphicEntry) error {
	if offset == 0 {
		return fmt.Errorf("call megamorphic offset cannot be zero")
	}
	if len(entries) > feedback.CallMegamorphicEntryCount {
		return fmt.Errorf("call megamorphic entry count = %d, want <= %d", len(entries), feedback.CallMegamorphicEntryCount)
	}
	bytes, err := tracer.heap.Resolve(offset, feedback.CallMegamorphicDataSize)
	if err != nil {
		return err
	}
	var fixed [feedback.CallMegamorphicEntryCount]feedback.CallPolymorphicEntry
	copy(fixed[:], entries)
	return feedback.WriteCallMegamorphicEntries(bytes, fixed)
}

func visitStrongTValue(slotValue value.TValue, visitStrong VisitFunc) error {
	if visitStrong == nil {
		return nil
	}
	ref, ok := slotValue.HeapRef()
	if !ok || ref == 0 {
		return nil
	}
	return visitStrong(ref)
}

func visitWeakTValue(slotValue value.TValue, base WeakRef, visitWeak WeakVisitFunc) error {
	if visitWeak == nil {
		return nil
	}
	ref, ok := slotValue.HeapRef()
	if !ok || ref == 0 {
		return nil
	}
	base.Target = ref
	return visitWeak(base)
}

func visitWeakTableTValue(slotValue value.TValue, base WeakRef, visitStrong VisitFunc, visitWeak WeakVisitFunc) error {
	if slotValue.IsBoxedTag(value.TagStringRef) {
		return visitStrongTValue(slotValue, visitStrong)
	}
	return visitWeakTValue(slotValue, base, visitWeak)
}

func tvalueDead(slotValue value.TValue, isDead func(value.HeapRef44) bool) (bool, error) {
	ref, ok := slotValue.HeapRef()
	if !ok || ref == 0 {
		return false, nil
	}
	return isDead(ref), nil
}

func weakArrayLenHint(bytes []byte, capacity uint32) uint32 {
	var hint uint32
	for index := uint32(0); index < capacity; index++ {
		start := index * value.TValueSize
		if readTValue(bytes[start : start+value.TValueSize]).IsBoxedTag(value.TagNil) {
			break
		}
		hint = index + 1
	}
	return hint
}

func weakRemainingGrowth(capacity uint32, count uint32) uint32 {
	if capacity == 0 {
		return 0
	}
	limit := capacity - capacity/8
	if count >= limit {
		return 0
	}
	return limit - count
}

func readTValue(buffer []byte) value.TValue {
	return value.FromRaw(value.Raw(binary.LittleEndian.Uint64(buffer)))
}

func writeTValue(buffer []byte, slotValue value.TValue) {
	binary.LittleEndian.PutUint64(buffer, uint64(slotValue.Bits()))
}

package table

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"

	"vexlua/internal/runtime/heap"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
)

type Handle struct {
	Ref   value.HeapRef44
	Value value.TValue
}

type Store struct {
	heap *heap.Heap
}

func NewStore(runtimeHeap *heap.Heap) *Store {
	if runtimeHeap == nil {
		panic("table store requires a heap")
	}
	return &Store{heap: runtimeHeap}
}

func (store *Store) New(arrayCap uint32, hashCap uint32) (Handle, error) {
	allocation, err := store.heap.AllocObject(value.CommonHeader{
		Kind:      value.KindTable,
		SizeBytes: ObjectSize,
		Version:   1,
	})
	if err != nil {
		return Handle{}, err
	}
	ref, err := store.heap.EncodeHeapRef(allocation.Address)
	if err != nil {
		return Handle{}, err
	}
	object := NewObject(mix64To32(uint64(ref) ^ 0xA511E9B3))
	if arrayCap > 0 {
		object, err = store.ensureArrayCapacity(object, arrayCap)
		if err != nil {
			return Handle{}, err
		}
	}
	if hashCap > 0 {
		object, err = store.ensureHashCapacity(object, hashCap)
		if err != nil {
			return Handle{}, err
		}
	}
	if err := WriteObject(allocation.Bytes, object); err != nil {
		return Handle{}, err
	}
	return Handle{Ref: ref, Value: value.TableRefValue(ref)}, nil
}

func (store *Store) Object(ref value.HeapRef44) (Object, error) {
	_, _, object, err := store.loadObject(ref)
	return object, err
}

func (store *Store) Get(tableRef value.HeapRef44, key value.TValue) (value.TValue, bool, error) {
	if err := validateKey(key); err != nil {
		return value.NilValue(), false, err
	}
	_, _, object, err := store.loadObject(tableRef)
	if err != nil {
		return value.NilValue(), false, err
	}
	if index, ok := arrayIndex(key); ok && index <= object.ArrayCap {
		slot, err := store.readArraySlot(object, index)
		if err != nil {
			return value.NilValue(), false, err
		}
		if !isNilValue(slot) {
			return slot, true, nil
		}
	}
	if object.HashCapacity == 0 {
		return value.NilValue(), false, nil
	}
	fullHash, keyClass, _, err := store.hashKey(object, key)
	if err != nil {
		return value.NilValue(), false, err
	}
	_, found, entry, err := store.findEntry(object, key, fullHash, keyClass)
	if err != nil {
		return value.NilValue(), false, err
	}
	if !found || isNilValue(entry.Value) {
		return value.NilValue(), false, nil
	}
	return entry.Value, true, nil
}

func (store *Store) Set(tableRef value.HeapRef44, key value.TValue, newValue value.TValue) error {
	if err := validateKey(key); err != nil {
		return err
	}
	offset, objectBytes, object, err := store.loadObject(tableRef)
	if err != nil {
		return err
	}
	updated, err := store.setValue(object, key, newValue)
	if err != nil {
		return err
	}
	return store.writeObject(offset, objectBytes, updated)
}

func (store *Store) SetMetatable(tableRef value.HeapRef44, metatable value.TValue) error {
	offset, objectBytes, object, err := store.loadObject(tableRef)
	if err != nil {
		return err
	}
	if object.Metatable.Bits() == metatable.Bits() {
		return nil
	}
	object.Metatable = metatable
	if isNilValue(metatable) {
		object.Flags = object.Flags.Without(FlagHasMetatable | FlagIndexFastPathBlocked | FlagNewIndexFastPathBlocked)
	} else {
		object.Flags = object.Flags.With(FlagHasMetatable | FlagIndexFastPathBlocked | FlagNewIndexFastPathBlocked)
	}
	object.BumpVersion()
	return store.writeObject(offset, objectBytes, object)
}

func (store *Store) setValue(object Object, key value.TValue, newValue value.TValue) (Object, error) {
	if index, ok := arrayIndex(key); ok && shouldUseArrayPath(object.ArrayCap, index, newValue) {
		return store.setArrayValue(object, index, newValue)
	}
	return store.setHashValue(object, key, newValue)
}

func (store *Store) setArrayValue(object Object, index uint32, newValue value.TValue) (Object, error) {
	var err error
	if index > object.ArrayCap {
		object, err = store.ensureArrayCapacity(object, index)
		if err != nil {
			return Object{}, err
		}
	}
	previous, err := store.readArraySlot(object, index)
	if err != nil {
		return Object{}, err
	}
	if previous.Bits() != newValue.Bits() && isNilValue(previous) != isNilValue(newValue) {
		object.BumpVersion()
	}
	if err := store.writeArraySlot(object, index, newValue); err != nil {
		return Object{}, err
	}
	if err := store.refreshArrayLenHint(&object); err != nil {
		return Object{}, err
	}
	object.SyncLayoutFlags()
	return object, nil
}

func (store *Store) setHashValue(object Object, key value.TValue, newValue value.TValue) (Object, error) {
	if object.HashCapacity == 0 && !isNilValue(newValue) {
		var err error
		object, err = store.ensureHashCapacity(object, MinHashCapacity)
		if err != nil {
			return Object{}, err
		}
	}
	if object.HashCapacity == 0 {
		return object, nil
	}
	if !isNilValue(newValue) && object.HashCount+1 > maxLoad(object.HashCapacity) {
		var err error
		object, err = store.rehash(object, object.HashCapacity*2)
		if err != nil {
			return Object{}, err
		}
	}
	fullHash, keyClass, keyAux, err := store.hashKey(object, key)
	if err != nil {
		return Object{}, err
	}
	slot, found, entry, err := store.findEntry(object, key, fullHash, keyClass)
	if err != nil {
		return Object{}, err
	}
	ctrl, entries, err := store.hashRegions(object)
	if err != nil {
		return Object{}, err
	}
	if found {
		if isNilValue(newValue) {
			ctrl[slot] = CtrlDeleted
			if err := store.writeEntryAt(entries, slot, emptyEntry()); err != nil {
				return Object{}, err
			}
			if object.HashCount > 0 {
				object.HashCount--
			}
			object.GrowthLeft = remainingGrowth(object.HashCapacity, object.HashCount)
			object.BumpVersion()
			object.SyncLayoutFlags()
			return object, nil
		}
		entry.Value = newValue
		if err := store.writeEntryAt(entries, slot, entry); err != nil {
			return Object{}, err
		}
		return object, nil
	}
	if isNilValue(newValue) {
		return object, nil
	}
	ctrl[slot] = ctrlFingerprint(fullHash)
	if err := store.writeEntryAt(entries, slot, Entry{
		FullHash: fullHash,
		KeyClass: keyClass,
		KeyAux:   keyAux,
		Key:      key,
		Value:    newValue,
	}); err != nil {
		return Object{}, err
	}
	object.HashCount++
	object.GrowthLeft = remainingGrowth(object.HashCapacity, object.HashCount)
	object.BumpVersion()
	object.SyncLayoutFlags()
	return object, nil
}

func (store *Store) ensureArrayCapacity(object Object, minimum uint32) (Object, error) {
	target := nextPowerOfTwo(minimum)
	if target < object.ArrayCap {
		target = object.ArrayCap
	}
	arrayAllocation, err := store.heap.Alloc(uint64(target) * value.TValueSize)
	if err != nil {
		return Object{}, err
	}
	fillTValueRegion(arrayAllocation.Bytes, value.NilValue())
	if object.ArrayCap > 0 && object.ArrayData != 0 {
		oldBytes, err := store.heap.Resolve(object.ArrayData, uint64(object.ArrayCap)*value.TValueSize)
		if err != nil {
			return Object{}, err
		}
		copy(arrayAllocation.Bytes, oldBytes)
	}
	if object.ArrayData != 0 && object.ArrayData != arrayAllocation.Offset {
		object.BumpVersion()
	}
	object.ArrayData = arrayAllocation.Offset
	object.ArrayCap = target
	object.SyncLayoutFlags()
	return object, nil
}

func (store *Store) ensureHashCapacity(object Object, minimum uint32) (Object, error) {
	target := normalizeHashCapacity(minimum)
	if target < object.HashCapacity {
		target = object.HashCapacity
	}
	ctrlAllocation, err := store.heap.Alloc(uint64(target) + 1)
	if err != nil {
		return Object{}, err
	}
	for index := uint32(0); index < target; index++ {
		ctrlAllocation.Bytes[index] = CtrlEmpty
	}
	ctrlAllocation.Bytes[target] = CtrlSentinel
	entriesAllocation, err := store.heap.Alloc(uint64(target) * EntrySize)
	if err != nil {
		return Object{}, err
	}
	for index := uint32(0); index < target; index++ {
		if err := store.writeEntryAt(entriesAllocation.Bytes, index, emptyEntry()); err != nil {
			return Object{}, err
		}
	}
	if object.CtrlData != 0 || object.EntriesData != 0 {
		object.BumpVersion()
	}
	object.CtrlData = ctrlAllocation.Offset
	object.EntriesData = entriesAllocation.Offset
	object.HashCapacity = target
	object.HashCount = 0
	object.GrowthLeft = remainingGrowth(target, 0)
	object.SyncLayoutFlags()
	return object, nil
}

func (store *Store) rehash(object Object, minimum uint32) (Object, error) {
	oldCtrl, oldEntries, err := store.hashRegions(object)
	if err != nil {
		return Object{}, err
	}
	oldCapacity := object.HashCapacity
	oldObject := object
	object, err = store.ensureHashCapacity(object, minimum)
	if err != nil {
		return Object{}, err
	}
	object.Flags = object.Flags.With(FlagRehashing)
	object.HashCount = 0
	object.GrowthLeft = remainingGrowth(object.HashCapacity, 0)
	for index := uint32(0); index < oldCapacity; index++ {
		if oldCtrl[index] == CtrlEmpty || oldCtrl[index] == CtrlDeleted || oldCtrl[index] == CtrlSentinel {
			continue
		}
		entry, err := store.readEntryAt(oldEntries, index)
		if err != nil {
			return Object{}, err
		}
		if isNilValue(entry.Value) {
			continue
		}
		slot, found, _, err := store.findEntry(object, entry.Key, entry.FullHash, entry.KeyClass)
		if err != nil {
			return Object{}, err
		}
		if found {
			return Object{}, fmt.Errorf("rehash found duplicate key at slot %d", slot)
		}
		newCtrl, newEntries, err := store.hashRegions(object)
		if err != nil {
			return Object{}, err
		}
		newCtrl[slot] = ctrlFingerprint(entry.FullHash)
		if err := store.writeEntryAt(newEntries, slot, entry); err != nil {
			return Object{}, err
		}
		object.HashCount++
	}
	object.GrowthLeft = remainingGrowth(object.HashCapacity, object.HashCount)
	object.Flags = object.Flags.Without(FlagRehashing)
	if oldObject.HashCapacity != object.HashCapacity || oldObject.CtrlData != object.CtrlData || oldObject.EntriesData != object.EntriesData {
		object.BumpVersion()
	}
	object.SyncLayoutFlags()
	return object, nil
}

func (store *Store) findEntry(object Object, key value.TValue, fullHash uint32, keyClass KeyClass) (uint32, bool, Entry, error) {
	ctrl, entries, err := store.hashRegions(object)
	if err != nil {
		return 0, false, Entry{}, err
	}
	start := fullHash & (object.HashCapacity - 1)
	firstDeleted := int32(-1)
	for probe := uint32(0); probe < object.HashCapacity; probe++ {
		slot := (start + probe) & (object.HashCapacity - 1)
		current := ctrl[slot]
		switch current {
		case CtrlEmpty:
			if firstDeleted >= 0 {
				return uint32(firstDeleted), false, Entry{}, nil
			}
			return slot, false, Entry{}, nil
		case CtrlDeleted:
			if firstDeleted < 0 {
				firstDeleted = int32(slot)
			}
		default:
			if current != ctrlFingerprint(fullHash) {
				continue
			}
			entry, err := store.readEntryAt(entries, slot)
			if err != nil {
				return 0, false, Entry{}, err
			}
			if entry.FullHash == fullHash && entry.KeyClass == keyClass && valuesEqual(entry.Key, key) {
				return slot, true, entry, nil
			}
		}
	}
	if firstDeleted >= 0 {
		return uint32(firstDeleted), false, Entry{}, nil
	}
	return 0, false, Entry{}, fmt.Errorf("table hash capacity %d is exhausted", object.HashCapacity)
}

func (store *Store) hashKey(object Object, key value.TValue) (uint32, KeyClass, uint64, error) {
	if key.IsNumber() {
		number, _ := key.Float64()
		if math.IsNaN(number) {
			return 0, 0, 0, fmt.Errorf("table key cannot be NaN")
		}
		if integer, ok := intLikeNumber(number); ok {
			return mix64To32(uint64(object.HashSeed)<<32 ^ uint64(integer)), KeyClassIntLikeNumber, uint64(integer), nil
		}
		bits := uint64(value.NumberValue(number).Bits())
		if number == 0 {
			bits = 0
		}
		return mix64To32(bits ^ uint64(object.HashSeed)<<32), KeyClassNonIntNumber, 0, nil
	}
	if key.IsBoxedTag(value.TagStringRef) {
		ref, _ := key.HeapRef()
		header, _, err := rtstring.HeaderAt(store.heap, ref)
		if err != nil {
			return 0, 0, 0, err
		}
		return mix64To32(uint64(header.Hash) ^ uint64(object.HashSeed)<<32), KeyClassInternedString, uint64(ref), nil
	}
	if key.IsBoxedTag(value.TagLightHandle) {
		payload := key.Payload()
		return mix64To32(uint64(object.HashSeed)<<32 ^ payload), KeyClassLightHandle, payload, nil
	}
	if ref, ok := key.HeapRef(); ok {
		payload := uint64(ref)
		return mix64To32(uint64(object.HashSeed)<<32 ^ uint64(key.Tag())<<48 ^ payload), KeyClassHeapObjectIdentity, payload, nil
	}
	return mix64To32(uint64(key.Bits()) ^ uint64(object.HashSeed)<<32), KeyClassGeneric, key.Payload(), nil
}

func (store *Store) refreshArrayLenHint(object *Object) error {
	if object.ArrayCap == 0 || object.ArrayData == 0 {
		object.ArrayLenHint = 0
		return nil
	}
	arrayBytes, err := store.heap.Resolve(object.ArrayData, uint64(object.ArrayCap)*value.TValueSize)
	if err != nil {
		return err
	}
	var hint uint32
	for index := uint32(0); index < object.ArrayCap; index++ {
		slot := readTValue(arrayBytes[index*value.TValueSize : (index+1)*value.TValueSize])
		if isNilValue(slot) {
			break
		}
		hint = index + 1
	}
	object.ArrayLenHint = hint
	return nil
}

func (store *Store) readArraySlot(object Object, index uint32) (value.TValue, error) {
	if index == 0 || index > object.ArrayCap || object.ArrayData == 0 {
		return value.NilValue(), nil
	}
	arrayBytes, err := store.heap.Resolve(object.ArrayData, uint64(object.ArrayCap)*value.TValueSize)
	if err != nil {
		return value.NilValue(), err
	}
	start := (index - 1) * value.TValueSize
	return readTValue(arrayBytes[start : start+value.TValueSize]), nil
}

func (store *Store) writeArraySlot(object Object, index uint32, slotValue value.TValue) error {
	if index == 0 || index > object.ArrayCap || object.ArrayData == 0 {
		return fmt.Errorf("array index %d is outside table capacity %d", index, object.ArrayCap)
	}
	arrayBytes, err := store.heap.Resolve(object.ArrayData, uint64(object.ArrayCap)*value.TValueSize)
	if err != nil {
		return err
	}
	start := (index - 1) * value.TValueSize
	writeTValue(arrayBytes[start:start+value.TValueSize], slotValue)
	return nil
}

func (store *Store) hashRegions(object Object) ([]byte, []byte, error) {
	if object.HashCapacity == 0 || object.CtrlData == 0 || object.EntriesData == 0 {
		return nil, nil, fmt.Errorf("table has no hash part")
	}
	ctrlBytes, err := store.heap.Resolve(object.CtrlData, uint64(object.HashCapacity)+1)
	if err != nil {
		return nil, nil, err
	}
	entriesBytes, err := store.heap.Resolve(object.EntriesData, uint64(object.HashCapacity)*EntrySize)
	if err != nil {
		return nil, nil, err
	}
	return ctrlBytes, entriesBytes, nil
}

func (store *Store) readEntryAt(entries []byte, index uint32) (Entry, error) {
	start := index * EntrySize
	return ReadEntry(entries[start : start+EntrySize])
}

func (store *Store) writeEntryAt(entries []byte, index uint32, entry Entry) error {
	start := index * EntrySize
	return WriteEntry(entries[start:start+EntrySize], entry)
}

func (store *Store) loadObject(ref value.HeapRef44) (value.HeapOff64, []byte, Object, error) {
	address, err := store.heap.DecodeHeapRef(ref)
	if err != nil {
		return 0, nil, Object{}, err
	}
	offset, err := store.heap.OffsetForAddress(address)
	if err != nil {
		return 0, nil, Object{}, err
	}
	objectBytes, err := store.heap.Resolve(offset, ObjectSize)
	if err != nil {
		return 0, nil, Object{}, err
	}
	object, err := ReadObject(objectBytes)
	if err != nil {
		return 0, nil, Object{}, err
	}
	return offset, objectBytes, object, nil
}

func (store *Store) writeObject(offset value.HeapOff64, objectBytes []byte, object Object) error {
	if err := WriteObject(objectBytes, object); err != nil {
		return err
	}
	return store.heap.WriteHeader(offset, object.Common)
}

func shouldUseArrayPath(currentCap uint32, index uint32, newValue value.TValue) bool {
	if index == 0 {
		return false
	}
	if index <= currentCap {
		return true
	}
	if isNilValue(newValue) {
		return false
	}
	if currentCap == 0 {
		return index <= 16
	}
	if index <= 1024 {
		return true
	}
	return index <= currentCap*2
}

func normalizeHashCapacity(capacity uint32) uint32 {
	if capacity == 0 {
		return MinHashCapacity
	}
	if capacity < MinHashCapacity {
		capacity = MinHashCapacity
	}
	if capacity&(capacity-1) == 0 {
		return capacity
	}
	return nextPowerOfTwo(capacity)
}

func maxLoad(capacity uint32) uint32 {
	if capacity == 0 {
		return 0
	}
	return capacity - capacity/8
}

func remainingGrowth(capacity uint32, count uint32) uint32 {
	limit := maxLoad(capacity)
	if count >= limit {
		return 0
	}
	return limit - count
}

func nextPowerOfTwo(valueToRound uint32) uint32 {
	if valueToRound <= 1 {
		return 1
	}
	return 1 << bits.Len32(valueToRound-1)
}

func ctrlFingerprint(fullHash uint32) byte {
	return byte((fullHash >> 25) & 0x7F)
}

func mix64To32(valueToMix uint64) uint32 {
	valueToMix ^= valueToMix >> 33
	valueToMix *= 0xff51afd7ed558ccd
	valueToMix ^= valueToMix >> 33
	valueToMix *= 0xc4ceb9fe1a85ec53
	valueToMix ^= valueToMix >> 33
	return uint32(valueToMix) ^ uint32(valueToMix>>32)
}

func validateKey(key value.TValue) error {
	if isNilValue(key) {
		return fmt.Errorf("table key cannot be nil")
	}
	if key.IsNumber() {
		number, _ := key.Float64()
		if math.IsNaN(number) {
			return fmt.Errorf("table key cannot be NaN")
		}
	}
	return nil
}

func arrayIndex(key value.TValue) (uint32, bool) {
	if !key.IsNumber() {
		return 0, false
	}
	number, _ := key.Float64()
	if number <= 0 || number > float64(^uint32(0)) || math.Trunc(number) != number {
		return 0, false
	}
	return uint32(number), true
}

func intLikeNumber(number float64) (uint64, bool) {
	if math.Trunc(number) != number {
		return 0, false
	}
	if number == 0 {
		return 0, true
	}
	if number < 0 {
		return uint64(int64(number)), true
	}
	return uint64(number), true
}

func valuesEqual(left value.TValue, right value.TValue) bool {
	if left.IsNumber() && right.IsNumber() {
		leftNumber, _ := left.Float64()
		rightNumber, _ := right.Float64()
		return leftNumber == rightNumber
	}
	return left.Bits() == right.Bits()
}

func isNilValue(candidate value.TValue) bool {
	return candidate.IsBoxedTag(value.TagNil)
}

func fillTValueRegion(buffer []byte, fillValue value.TValue) {
	for offset := uint32(0); offset+value.TValueSize <= uint32(len(buffer)); offset += value.TValueSize {
		writeTValue(buffer[offset:offset+value.TValueSize], fillValue)
	}
}

func readTValue(buffer []byte) value.TValue {
	return value.FromRaw(value.Raw(binary.LittleEndian.Uint64(buffer)))
}

func writeTValue(buffer []byte, slotValue value.TValue) {
	binary.LittleEndian.PutUint64(buffer, uint64(slotValue.Bits()))
}

func emptyEntry() Entry {
	return Entry{Key: value.NilValue(), Value: value.NilValue()}
}

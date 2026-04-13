package closure

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/feedback"
	"vexlua/internal/runtime/heap"
	rproto "vexlua/internal/runtime/proto"
	"vexlua/internal/runtime/value"
)

type Store struct {
	heap   *heap.Heap
	protos *rproto.Store
}

func NewStore(runtimeHeap *heap.Heap, protos *rproto.Store) *Store {
	if runtimeHeap == nil {
		panic("closure store requires a heap")
	}
	if protos == nil {
		panic("closure store requires a proto store")
	}
	return &Store{heap: runtimeHeap, protos: protos}
}

func (store *Store) NewLuaClosure(proto *bytecode.Proto, env value.TValue, upvalues []value.HeapRef44) (Handle, error) {
	if proto == nil {
		return Handle{}, fmt.Errorf("proto cannot be nil")
	}
	if int(proto.NumUpvalues) != len(upvalues) {
		return Handle{}, fmt.Errorf("proto expects %d upvalues, got %d", proto.NumUpvalues, len(upvalues))
	}
	protoHandle, err := store.protos.Intern(proto)
	if err != nil {
		return Handle{}, err
	}
	object := NewObject(protoHandle.Value, env, uint16(len(upvalues)), 0)
	allocation, err := store.heap.AllocObject(object.Common)
	if err != nil {
		return Handle{}, err
	}
	if len(upvalues) > 0 {
		upvalueAllocation, err := store.heap.AllocPayload(uint64(len(upvalues))*8, heap.PayloadLayoutHeapRefArray, allocation.Offset)
		if err != nil {
			return Handle{}, err
		}
		for index, ref := range upvalues {
			binary.LittleEndian.PutUint64(upvalueAllocation.Bytes[index*8:(index+1)*8], uint64(ref))
		}
		object.UpvaluesData = upvalueAllocation.Offset
	}
	if err := WriteObject(allocation.Bytes, object); err != nil {
		return Handle{}, err
	}
	ref, err := store.heap.EncodeHeapRef(allocation.Address)
	if err != nil {
		return Handle{}, err
	}
	return Handle{Ref: ref, Value: value.LuaClosureRefValue(ref)}, nil
}

func (store *Store) Object(ref value.HeapRef44) (Object, error) {
	_, bytes, err := store.objectBytes(ref)
	if err != nil {
		return Object{}, err
	}
	return ReadObject(bytes)
}

func (store *Store) Proto(ref value.HeapRef44) (*bytecode.Proto, error) {
	protoRef, err := store.ProtoRef(ref)
	if err != nil {
		return nil, err
	}
	return store.protos.Resolve(protoRef)
}

func (store *Store) ProtoRef(ref value.HeapRef44) (value.HeapRef44, error) {
	object, err := store.Object(ref)
	if err != nil {
		return 0, err
	}
	protoRef, ok := object.ProtoRef()
	if !ok {
		return 0, fmt.Errorf("closure %#x has non-proto reference %s", uint64(ref), object.Proto)
	}
	return protoRef, nil
}

func (store *Store) Env(ref value.HeapRef44) (value.TValue, error) {
	object, err := store.Object(ref)
	if err != nil {
		return value.NilValue(), err
	}
	return object.Env, nil
}

func (store *Store) SetEnv(ref value.HeapRef44, env value.TValue) error {
	offset, objectBytes, err := store.objectBytes(ref)
	if err != nil {
		return err
	}
	object, err := ReadObject(objectBytes)
	if err != nil {
		return err
	}
	if object.Env.Bits() == env.Bits() {
		return nil
	}
	object.Env = env
	if err := WriteObject(objectBytes, object); err != nil {
		return err
	}
	return store.heap.WriteBarrierValueByOffset(offset, env)
}

func (store *Store) UpvalueBase(ref value.HeapRef44) (uintptr, error) {
	object, err := store.Object(ref)
	if err != nil {
		return 0, err
	}
	if object.UpvalueCount == 0 || object.UpvaluesData == 0 {
		return 0, nil
	}
	return store.heap.NativeAddressForOffset(object.UpvaluesData)
}

func (store *Store) UpvalueRefAt(ref value.HeapRef44, index int) (value.HeapRef44, error) {
	object, err := store.Object(ref)
	if err != nil {
		return 0, err
	}
	if index < 0 || index >= int(object.UpvalueCount) {
		return 0, fmt.Errorf("closure upvalue %d is out of range", index)
	}
	if object.UpvaluesData == 0 {
		return 0, fmt.Errorf("closure %#x has no upvalue vector", uint64(ref))
	}
	bytes, err := store.heap.Resolve(object.UpvaluesData+value.HeapOff64(index*8), 8)
	if err != nil {
		return 0, err
	}
	return value.HeapRef44(binary.LittleEndian.Uint64(bytes)), nil
}

func (store *Store) UpvalueRefs(ref value.HeapRef44) ([]value.HeapRef44, error) {
	object, err := store.Object(ref)
	if err != nil {
		return nil, err
	}
	if object.UpvalueCount == 0 || object.UpvaluesData == 0 {
		return nil, nil
	}
	refs := make([]value.HeapRef44, object.UpvalueCount)
	for index := range refs {
		upvalueRef, err := store.UpvalueRefAt(ref, index)
		if err != nil {
			return nil, err
		}
		refs[index] = upvalueRef
	}
	return refs, nil
}

func (store *Store) EnsureFeedbackVector(ref value.HeapRef44, layout *feedback.Layout) (uintptr, error) {
	if layout == nil || layout.SlotCount() == 0 {
		return 0, nil
	}
	offset, objectBytes, err := store.objectBytes(ref)
	if err != nil {
		return 0, err
	}
	object, err := ReadObject(objectBytes)
	if err != nil {
		return 0, err
	}
	if object.FeedbackData != 0 {
		if object.FeedbackSize != layout.SlotCount() {
			return 0, fmt.Errorf("closure %#x feedback slot count mismatch: have %d want %d", uint64(ref), object.FeedbackSize, layout.SlotCount())
		}
		return store.heap.NativeAddressForOffset(object.FeedbackData)
	}
	vector, err := store.heap.AllocPayload(feedback.VectorSize(layout.SlotCount()), heap.PayloadLayoutFeedbackVector, offset)
	if err != nil {
		return 0, err
	}
	if err := feedback.WriteHeader(vector.Bytes[:feedback.HeaderSize], feedback.NewHeader(layout.SlotCount())); err != nil {
		return 0, err
	}
	for index, slot := range layout.Slots() {
		start := feedback.CellOffset(uint32(index))
		if err := feedback.WriteCell(vector.Bytes[start:start+feedback.CellSize], feedback.NewGenericCell(slot.Kind)); err != nil {
			return 0, err
		}
	}
	object.FeedbackData = vector.Offset
	object.FeedbackSize = layout.SlotCount()
	if err := WriteObject(objectBytes, object); err != nil {
		return 0, err
	}
	return store.heap.NativeAddressForOffset(object.FeedbackData)
}

func (store *Store) FeedbackVectorBase(ref value.HeapRef44) (uintptr, error) {
	object, err := store.Object(ref)
	if err != nil {
		return 0, err
	}
	if object.FeedbackData == 0 {
		return 0, nil
	}
	return store.heap.NativeAddressForOffset(object.FeedbackData)
}

func (store *Store) ReadFeedbackHeader(ref value.HeapRef44) (feedback.Header, error) {
	object, err := store.Object(ref)
	if err != nil {
		return feedback.Header{}, err
	}
	if object.FeedbackData == 0 {
		return feedback.Header{}, fmt.Errorf("closure %#x has no feedback vector", uint64(ref))
	}
	bytes, err := store.heap.Resolve(object.FeedbackData, feedback.HeaderSize)
	if err != nil {
		return feedback.Header{}, err
	}
	return feedback.ReadHeader(bytes)
}

func (store *Store) WriteFeedbackHeader(ref value.HeapRef44, header feedback.Header) error {
	object, err := store.Object(ref)
	if err != nil {
		return err
	}
	if object.FeedbackData == 0 {
		return fmt.Errorf("closure %#x has no feedback vector", uint64(ref))
	}
	bytes, err := store.heap.Resolve(object.FeedbackData, feedback.HeaderSize)
	if err != nil {
		return err
	}
	return feedback.WriteHeader(bytes, header)
}

func (store *Store) ReadFeedbackCell(ref value.HeapRef44, slot uint32) (feedback.Cell, error) {
	object, err := store.Object(ref)
	if err != nil {
		return feedback.Cell{}, err
	}
	if object.FeedbackData == 0 {
		return feedback.Cell{}, fmt.Errorf("closure %#x has no feedback vector", uint64(ref))
	}
	if slot >= object.FeedbackSize {
		return feedback.Cell{}, fmt.Errorf("feedback slot %d is outside %d slots", slot, object.FeedbackSize)
	}
	bytes, err := store.heap.Resolve(object.FeedbackData+value.HeapOff64(feedback.CellOffset(slot)), feedback.CellSize)
	if err != nil {
		return feedback.Cell{}, err
	}
	return feedback.ReadCell(bytes)
}

func (store *Store) AllocFeedbackPayload(ref value.HeapRef44, size uint64) (value.HeapOff64, []byte, error) {
	offset, _, err := store.objectBytes(ref)
	if err != nil {
		return 0, nil, err
	}
	allocation, err := store.heap.AllocPayload(size, heap.PayloadLayoutOpaque, offset)
	if err != nil {
		return 0, nil, err
	}
	return allocation.Offset, allocation.Bytes, nil
}

func (store *Store) WriteFeedbackCell(ref value.HeapRef44, slot uint32, cell feedback.Cell) error {
	offset, objectBytes, err := store.objectBytes(ref)
	if err != nil {
		return err
	}
	object, err := ReadObject(objectBytes)
	if err != nil {
		return err
	}
	if object.FeedbackData == 0 {
		return fmt.Errorf("closure %#x has no feedback vector", uint64(ref))
	}
	if slot >= object.FeedbackSize {
		return fmt.Errorf("feedback slot %d is outside %d slots", slot, object.FeedbackSize)
	}
	cellOffset := object.FeedbackData + value.HeapOff64(feedback.CellOffset(slot))
	bytes, err := store.heap.Resolve(cellOffset, feedback.CellSize)
	if err != nil {
		return err
	}
	current, err := feedback.ReadCell(bytes)
	if err != nil {
		return err
	}
	if err := feedback.WriteCell(bytes, cell); err != nil {
		return err
	}
	currentOffset := current.CallSidecarDataOffset()
	if currentOffset != 0 && currentOffset != cell.CallSidecarDataOffset() {
		if err := store.heap.FreeSpan(currentOffset); err != nil {
			return err
		}
	}
	return store.heap.RememberWeakOwnerByOffset(offset)
}

func (store *Store) objectBytes(ref value.HeapRef44) (value.HeapOff64, []byte, error) {
	address, err := store.heap.DecodeHeapRef(ref)
	if err != nil {
		return 0, nil, err
	}
	offset, err := store.heap.OffsetForAddress(address)
	if err != nil {
		return 0, nil, err
	}
	bytes, err := store.heap.Resolve(offset, ObjectSize)
	if err != nil {
		return 0, nil, err
	}
	return offset, bytes, nil
}

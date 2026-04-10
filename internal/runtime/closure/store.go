package closure

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/bytecode"
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
	var upvaluesOffset value.HeapOff64
	if len(upvalues) > 0 {
		allocation, err := store.heap.Alloc(uint64(len(upvalues)) * 8)
		if err != nil {
			return Handle{}, err
		}
		for index, ref := range upvalues {
			binary.LittleEndian.PutUint64(allocation.Bytes[index*8:(index+1)*8], uint64(ref))
		}
		if err := store.heap.SyncNative(allocation.Offset, allocation.Bytes); err != nil {
			return Handle{}, err
		}
		upvaluesOffset = allocation.Offset
	}
	object := NewObject(protoHandle.Value, env, uint16(len(upvalues)), upvaluesOffset)
	allocation, err := store.heap.AllocObject(object.Common)
	if err != nil {
		return Handle{}, err
	}
	if err := WriteObject(allocation.Bytes, object); err != nil {
		return Handle{}, err
	}
	if err := store.heap.SyncNative(allocation.Offset, allocation.Bytes); err != nil {
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

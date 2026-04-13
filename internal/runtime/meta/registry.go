package meta

import (
	"encoding/binary"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

type Kind uint8

const (
	KindInvalid Kind = iota
	KindNil
	KindBoolean
	KindNumber
	KindString
	KindFunction
	KindThread
	KindUserData
	KindLightUserData
)

const (
	KindCount = int(KindLightUserData) + 1

	RegistryEntryVersionOffset   = 0x00
	RegistryEntryMetatableOffset = 0x08
	RegistryEntrySize            = 0x10
)

type Registry struct {
	heap               *heap.Heap
	snapshotOffset     value.HeapOff64
	snapshotNativeBase uintptr
	snapshotBytes      []byte
}

func NewRegistry(runtimeHeap *heap.Heap) *Registry {
	allocation, err := runtimeHeap.AllocPayload(uint64(RegistryEntrySize*KindCount), heap.PayloadLayoutOpaque, 0)
	if err != nil {
		panic(err)
	}
	nativeBase, err := runtimeHeap.NativeAddressForOffset(allocation.Offset)
	if err != nil {
		panic(err)
	}
	registry := &Registry{
		heap:               runtimeHeap,
		snapshotOffset:     allocation.Offset,
		snapshotNativeBase: nativeBase,
		snapshotBytes:      allocation.Bytes,
	}
	for kind := 0; kind < KindCount; kind++ {
		registry.writeSnapshotEntry(kind, 0, value.NilValue())
	}
	return registry
}

func (registry *Registry) SnapshotOffset() value.HeapOff64 {
	return registry.snapshotOffset
}

func (registry *Registry) SnapshotNativeAddress() uintptr {
	return registry.snapshotNativeBase
}

func KindForValue(slotValue value.TValue) (Kind, bool) {
	if slotValue.IsNumber() {
		return KindNumber, true
	}
	switch slotValue.Tag() {
	case value.TagNil:
		return KindNil, true
	case value.TagBool:
		return KindBoolean, true
	case value.TagStringRef:
		return KindString, true
	case value.TagLuaClosureRef, value.TagHostFunctionRef, value.TagNativeClosureRef:
		return KindFunction, true
	case value.TagThreadRef:
		return KindThread, true
	case value.TagHostObjectRef:
		return KindUserData, true
	case value.TagLightHandle:
		return KindLightUserData, true
	default:
		return KindInvalid, false
	}
}

func TypeName(slotValue value.TValue) string {
	if slotValue.IsNumber() {
		return "number"
	}
	switch slotValue.Tag() {
	case value.TagNil:
		return "nil"
	case value.TagBool:
		return "boolean"
	case value.TagStringRef:
		return "string"
	case value.TagTableRef:
		return "table"
	case value.TagLuaClosureRef, value.TagHostFunctionRef, value.TagNativeClosureRef:
		return "function"
	case value.TagThreadRef:
		return "thread"
	case value.TagHostObjectRef, value.TagLightHandle:
		return "userdata"
	default:
		return "value"
	}
}

func (registry *Registry) Get(kind Kind) (value.TValue, bool) {
	index, ok := registry.entryIndex(kind)
	if !ok {
		return value.NilValue(), false
	}
	metatable := registry.entryMetatable(index)
	if metatable.IsBoxedTag(value.TagNil) {
		return value.NilValue(), false
	}
	return metatable, true
}

func (registry *Registry) Version(kind Kind) uint32 {
	index, ok := registry.entryIndex(kind)
	if !ok {
		return 0
	}
	return registry.entryVersion(index)
}

func (registry *Registry) Set(kind Kind, metatable value.TValue) {
	index, ok := registry.entryIndex(kind)
	if !ok {
		return
	}
	current := registry.entryMetatable(index)
	if current.Bits() == metatable.Bits() {
		return
	}
	nextVersion := registry.entryVersion(index) + 1
	if nextVersion == 0 {
		nextVersion = 1
	}
	registry.writeSnapshotEntry(index, nextVersion, metatable)
}

func (registry *Registry) entryIndex(kind Kind) (int, bool) {
	index := int(kind)
	if index <= int(KindInvalid) || index >= KindCount {
		return 0, false
	}
	return index, true
}

func (registry *Registry) entryVersion(index int) uint32 {
	if index < 0 || index >= KindCount {
		return 0
	}
	base := index * RegistryEntrySize
	return binary.LittleEndian.Uint32(registry.snapshotBytes[base+RegistryEntryVersionOffset : base+RegistryEntryVersionOffset+4])
}

func (registry *Registry) entryMetatable(index int) value.TValue {
	if index < 0 || index >= KindCount {
		return value.NilValue()
	}
	base := index * RegistryEntrySize
	return value.FromRaw(value.Raw(binary.LittleEndian.Uint64(registry.snapshotBytes[base+RegistryEntryMetatableOffset : base+RegistryEntryMetatableOffset+8])))
}

func (registry *Registry) writeSnapshotEntry(index int, version uint32, metatable value.TValue) {
	if index < 0 || index >= KindCount {
		return
	}
	base := index * RegistryEntrySize
	binary.LittleEndian.PutUint32(registry.snapshotBytes[base+RegistryEntryVersionOffset:base+RegistryEntryVersionOffset+4], version)
	binary.LittleEndian.PutUint32(registry.snapshotBytes[base+4:base+8], 0)
	binary.LittleEndian.PutUint64(registry.snapshotBytes[base+RegistryEntryMetatableOffset:base+RegistryEntryMetatableOffset+8], uint64(metatable.Bits()))
}

package host

import (
	"encoding/binary"
	"fmt"
	"testing"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

func TestHostWrapperAndDescriptorLayoutContract(t *testing.T) {
	wrapper := newHostObjectHeader(11, 7, value.BoolValue(true), value.HeapOff64(0x1122334455667788))
	wrapper.Flags = WrapperFlagCallable | WrapperFlagIndexable
	wrapper.MetatableVersion = 9
	wrapper.Metatable = value.NumberValue(42)
	wrapperBuffer := make([]byte, hostObjectSize)
	if err := writeWrapperHeader(wrapperBuffer, wrapper); err != nil {
		t.Fatalf("write host wrapper: %v", err)
	}
	if got := binary.LittleEndian.Uint64(wrapperBuffer[hostHandleOffset : hostHandleOffset+8]); got != wrapper.HostHandle {
		t.Fatalf("host handle = %d, want %d", got, wrapper.HostHandle)
	}
	if got := binary.LittleEndian.Uint32(wrapperBuffer[reservedDescriptorOff : reservedDescriptorOff+4]); got != wrapper.MetatableVersion {
		t.Fatalf("wrapper metatable version = %d, want %d", got, wrapper.MetatableVersion)
	}
	if got := binary.LittleEndian.Uint32(wrapperBuffer[descriptorVersionOffset : descriptorVersionOffset+4]); got != wrapper.DescriptorVersion {
		t.Fatalf("descriptor version = %d, want %d", got, wrapper.DescriptorVersion)
	}
	if got := binary.LittleEndian.Uint32(wrapperBuffer[reservedCacheSlotOff : reservedCacheSlotOff+4]); got != 0 {
		t.Fatalf("reserved cache slot = %#x, want 0", got)
	}
	if got := WrapperFlags(binary.LittleEndian.Uint32(wrapperBuffer[flagsOffset : flagsOffset+4])); got != wrapper.Flags {
		t.Fatalf("wrapper flags = %#x, want %#x", uint32(got), uint32(wrapper.Flags))
	}
	if got := value.Raw(binary.LittleEndian.Uint64(wrapperBuffer[envOffset : envOffset+8])); got != wrapper.Env.Bits() {
		t.Fatalf("wrapper env bits = %#x, want %#x", uint64(got), uint64(wrapper.Env.Bits()))
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(wrapperBuffer[nativeMetaOffset : nativeMetaOffset+8])); got != wrapper.NativeMeta {
		t.Fatalf("wrapper native meta = %#x, want %#x", uint64(got), uint64(wrapper.NativeMeta))
	}
	if got := value.Raw(binary.LittleEndian.Uint64(wrapperBuffer[reserved1Offset : reserved1Offset+8])); got != wrapper.Metatable.Bits() {
		t.Fatalf("wrapper metatable bits = %#x, want %#x", uint64(got), uint64(wrapper.Metatable.Bits()))
	}
	descriptor := newNativeDescriptor(13, 5, 17, 19, 2, DescriptorFlagCallable|DescriptorFlagVariadic, DescriptorKindFunction)
	descriptorBuffer := make([]byte, hostDescriptorSize)
	if err := writeNativeDescriptor(descriptorBuffer, descriptor); err != nil {
		t.Fatalf("write native descriptor: %v", err)
	}
	if got := binary.LittleEndian.Uint32(descriptorBuffer[hostDescriptorIDOffset : hostDescriptorIDOffset+4]); got != descriptor.DescriptorID {
		t.Fatalf("descriptor id = %d, want %d", got, descriptor.DescriptorID)
	}
	if got := binary.LittleEndian.Uint32(descriptorBuffer[hostDescriptorVersionOffset : hostDescriptorVersionOffset+4]); got != descriptor.DescriptorVersion {
		t.Fatalf("descriptor version = %d, want %d", got, descriptor.DescriptorVersion)
	}
	if got := binary.LittleEndian.Uint32(descriptorBuffer[hostDescriptorShapeIDOffset : hostDescriptorShapeIDOffset+4]); got != descriptor.ShapeID {
		t.Fatalf("shape id = %d, want %d", got, descriptor.ShapeID)
	}
	if got := binary.LittleEndian.Uint32(descriptorBuffer[hostDescriptorCacheSlotOff : hostDescriptorCacheSlotOff+4]); got != descriptor.CacheSlot {
		t.Fatalf("cache slot = %d, want %d", got, descriptor.CacheSlot)
	}
	if got := binary.LittleEndian.Uint16(descriptorBuffer[hostDescriptorArityOffset : hostDescriptorArityOffset+2]); got != descriptor.Arity {
		t.Fatalf("arity = %d, want %d", got, descriptor.Arity)
	}
	if got := DescriptorFlags(binary.LittleEndian.Uint16(descriptorBuffer[hostDescriptorFlagsOffset : hostDescriptorFlagsOffset+2])); got != descriptor.Flags {
		t.Fatalf("descriptor flags = %#x, want %#x", uint16(got), uint16(descriptor.Flags))
	}
	if got := DescriptorKind(binary.LittleEndian.Uint32(descriptorBuffer[hostDescriptorKindOffset : hostDescriptorKindOffset+4])); got != descriptor.Kind {
		t.Fatalf("descriptor kind = %d, want %d", got, descriptor.Kind)
	}
	if got := binary.LittleEndian.Uint64(descriptorBuffer[hostDescriptorReserved0Off : hostDescriptorReserved0Off+8]); got != 0 {
		t.Fatalf("descriptor reserved0 = %#x, want 0", got)
	}
	if got := binary.LittleEndian.Uint64(descriptorBuffer[hostDescriptorReserved1Off : hostDescriptorReserved1Off+8]); got != 0 {
		t.Fatalf("descriptor reserved1 = %#x, want 0", got)
	}
	if got := binary.LittleEndian.Uint64(descriptorBuffer[hostDescriptorReserved2Off : hostDescriptorReserved2Off+8]); got != 0 {
		t.Fatalf("descriptor reserved2 = %#x, want 0", got)
	}
}

type sampleStruct struct {
	Name  string
	Count int
}

func TestRegistryWrapsHostObjectWithoutGoPointerLeak(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	registry := NewRegistry(runtimeHeap)
	target := &sampleStruct{Name: "vex", Count: 3}
	handle, err := registry.RegisterObject("sample", target)
	if err != nil {
		t.Fatalf("register object: %v", err)
	}
	wrapper, err := registry.WrapObject(handle, value.NilValue())
	if err != nil {
		t.Fatalf("wrap object: %v", err)
	}
	header, storedTarget, descriptor, err := registry.ReadHostObject(wrapper.Ref)
	if err != nil {
		t.Fatalf("read host object: %v", err)
	}
	if storedTarget != target {
		t.Fatalf("unexpected target in registry")
	}
	if descriptor.Name != "sample" {
		t.Fatalf("descriptor name = %q, want sample", descriptor.Name)
	}
	if header.HostHandle != uint64(handle) {
		t.Fatalf("wrapper stored wrong handle %d", header.HostHandle)
	}
	if header.DescriptorVersion == 0 {
		t.Fatalf("wrapper descriptor version should not be zero")
	}
	if header.NativeMeta == 0 {
		t.Fatalf("wrapper should store native descriptor metadata offset")
	}
	native, err := registry.ReadNativeDescriptor(header.NativeMeta)
	if err != nil {
		t.Fatalf("read native descriptor: %v", err)
	}
	if native.DescriptorVersion != header.DescriptorVersion {
		t.Fatalf("native descriptor cache mismatch: %+v", native)
	}
	if native.DescriptorID == 0 || native.ShapeID == 0 || native.CacheSlot == 0 {
		t.Fatalf("native descriptor should expose non-zero identity fields: %+v", native)
	}
	if native.Kind != DescriptorKindObject {
		t.Fatalf("native descriptor kind = %d, want object", native.Kind)
	}
	if native.Flags != DescriptorFlagIndexable|DescriptorFlagWritable {
		t.Fatalf("native descriptor flags = %#x", uint16(native.Flags))
	}
	address, err := runtimeHeap.DecodeHeapRef(wrapper.Ref)
	if err != nil {
		t.Fatalf("decode wrapper ref: %v", err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("offset for wrapper ref: %v", err)
	}
	bytes, err := runtimeHeap.Resolve(offset, hostObjectSize)
	if err != nil {
		t.Fatalf("resolve wrapper bytes: %v", err)
	}
	if fmt.Sprintf("%x", bytes) == fmt.Sprintf("%x", uintptrToBytes(uintptr(handle))) {
		t.Fatalf("wrapper bytes unexpectedly mirror a raw Go pointer")
	}
}

func TestRegistryBuildsCallableNativeDescriptorForFunctions(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	registry := NewRegistry(runtimeHeap)
	handle, err := registry.RegisterFunction("sum2", func(a float64, b float64) float64 {
		return a + b
	})
	if err != nil {
		t.Fatalf("register function: %v", err)
	}
	wrapper, err := registry.WrapFunction(handle, value.NilValue())
	if err != nil {
		t.Fatalf("wrap function: %v", err)
	}
	header, _, descriptor, err := registry.ReadHostFunction(wrapper.Ref)
	if err != nil {
		t.Fatalf("read host function: %v", err)
	}
	if descriptor.Name != "sum2" {
		t.Fatalf("descriptor name = %q, want sum2", descriptor.Name)
	}
	native, err := registry.ReadNativeDescriptor(header.NativeMeta)
	if err != nil {
		t.Fatalf("read native descriptor: %v", err)
	}
	if native.Kind != DescriptorKindFunction {
		t.Fatalf("native descriptor kind = %d, want function", native.Kind)
	}
	if native.Arity != 2 {
		t.Fatalf("native descriptor arity = %d, want 2", native.Arity)
	}
	if native.Flags != DescriptorFlagCallable {
		t.Fatalf("native descriptor flags = %#x", uint16(native.Flags))
	}
	if native.CacheSlot == 0 || native.ShapeID == 0 {
		t.Fatalf("native descriptor metadata should be initialized: %+v", native)
	}
	if err := registry.BumpDescriptorVersion(handle); err != nil {
		t.Fatalf("bump descriptor version: %v", err)
	}
	updated, err := registry.ReadNativeDescriptor(header.NativeMeta)
	if err != nil {
		t.Fatalf("read updated native descriptor: %v", err)
	}
	if updated.DescriptorVersion != native.DescriptorVersion+1 {
		t.Fatalf("native descriptor version = %d, want %d", updated.DescriptorVersion, native.DescriptorVersion+1)
	}
}

func TestRegistryDescriptorVersionReadsCanonicalNativeDescriptor(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	registry := NewRegistry(runtimeHeap)
	handle, err := registry.RegisterObject("map", map[string]int{"x": 1})
	if err != nil {
		t.Fatalf("register object: %v", err)
	}
	entry := registry.entries[handle]
	if entry == nil {
		t.Fatalf("expected registry entry for handle %d", handle)
	}
	bytes, err := runtimeHeap.Resolve(entry.nativeMeta, hostDescriptorSize)
	if err != nil {
		t.Fatalf("resolve native descriptor bytes: %v", err)
	}
	native, err := readNativeDescriptor(bytes)
	if err != nil {
		t.Fatalf("read native descriptor: %v", err)
	}
	native.DescriptorVersion = 41
	native.Common.Version = 41
	if err := writeNativeDescriptor(bytes, native); err != nil {
		t.Fatalf("write native descriptor: %v", err)
	}
	version, err := registry.DescriptorVersion(handle)
	if err != nil {
		t.Fatalf("descriptor version: %v", err)
	}
	if version != 41 {
		t.Fatalf("descriptor version = %d, want 41", version)
	}
}

func TestRegistryDescriptorVersionAndRelease(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	registry := NewRegistry(runtimeHeap)
	target := map[string]int{"x": 1}
	handle, err := registry.RegisterObject("map", target)
	if err != nil {
		t.Fatalf("register object: %v", err)
	}
	version1, err := registry.DescriptorVersion(handle)
	if err != nil {
		t.Fatalf("descriptor version: %v", err)
	}
	if err := registry.BumpDescriptorVersion(handle); err != nil {
		t.Fatalf("bump descriptor version: %v", err)
	}
	version2, err := registry.DescriptorVersion(handle)
	if err != nil {
		t.Fatalf("descriptor version after bump: %v", err)
	}
	if version2 != version1+1 {
		t.Fatalf("descriptor version did not increase: %d -> %d", version1, version2)
	}
	if err := registry.Release(handle); err != nil {
		t.Fatalf("release handle: %v", err)
	}
	if _, err := registry.DescriptorVersion(handle); err == nil {
		t.Fatalf("released handle should not remain in registry")
	}
}

func TestRegistryRefreshWrapperUpdatesCachedDescriptorVersion(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	registry := NewRegistry(runtimeHeap)
	handle, err := registry.RegisterObject("map", map[string]int{"x": 1})
	if err != nil {
		t.Fatalf("register object: %v", err)
	}
	wrapper, err := registry.WrapObject(handle, value.NilValue())
	if err != nil {
		t.Fatalf("wrap object: %v", err)
	}
	header, err := registry.readWrapper(wrapper.Ref, value.KindHostObject, hostObjectSize)
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	if err := registry.BumpDescriptorVersion(handle); err != nil {
		t.Fatalf("bump descriptor version: %v", err)
	}
	refreshed, err := registry.RefreshWrapper(wrapper.Ref)
	if err != nil {
		t.Fatalf("refresh wrapper: %v", err)
	}
	if refreshed.DescriptorVersion != header.DescriptorVersion+1 {
		t.Fatalf("refreshed version = %d, want %d", refreshed.DescriptorVersion, header.DescriptorVersion+1)
	}
	if refreshed.Flags != WrapperFlagIndexable|WrapperFlagWritable {
		t.Fatalf("refreshed flags = %#x", uint32(refreshed.Flags))
	}
}

func TestRegistrySetWrapperMetatablePersistsOnWrapper(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	registry := NewRegistry(runtimeHeap)
	handle, err := registry.RegisterObject("map", map[string]int{"x": 1})
	if err != nil {
		t.Fatalf("register object: %v", err)
	}
	wrapper, err := registry.WrapObject(handle, value.NilValue())
	if err != nil {
		t.Fatalf("wrap object: %v", err)
	}
	metatable := value.NumberValue(7)
	updated, err := registry.SetWrapperMetatable(wrapper.Ref, metatable)
	if err != nil {
		t.Fatalf("set wrapper metatable: %v", err)
	}
	if updated.MetatableVersion == 0 {
		t.Fatalf("updated wrapper metatable version = 0, want non-zero")
	}
	if updated.Metatable.Bits() != metatable.Bits() {
		t.Fatalf("updated wrapper metatable = %#x, want %#x", uint64(updated.Metatable.Bits()), uint64(metatable.Bits()))
	}
	header, _, _, err := registry.ReadHostObject(wrapper.Ref)
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	if header.MetatableVersion != updated.MetatableVersion {
		t.Fatalf("stored wrapper metatable version = %d, want %d", header.MetatableVersion, updated.MetatableVersion)
	}
	if header.Metatable.Bits() != metatable.Bits() {
		t.Fatalf("stored wrapper metatable = %#x, want %#x", uint64(header.Metatable.Bits()), uint64(metatable.Bits()))
	}
}

func uintptrToBytes(value uintptr) []byte {
	return []byte(fmt.Sprintf("%x", value))
}

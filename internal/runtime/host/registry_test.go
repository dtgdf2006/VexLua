package host

import (
	"fmt"
	"testing"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

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
	if descriptor.Kind != DescriptorKindObject {
		t.Fatalf("unexpected descriptor kind %d", descriptor.Kind)
	}
	if header.HostHandle != uint64(handle) {
		t.Fatalf("wrapper stored wrong handle %d", header.HostHandle)
	}
	if header.DescriptorID != descriptor.ID || header.DescriptorVersion != descriptor.Version {
		t.Fatalf("wrapper descriptor cache mismatch")
	}
	if header.CacheSlot != descriptor.CacheSlot {
		t.Fatalf("wrapper cache slot = %d, want %d", header.CacheSlot, descriptor.CacheSlot)
	}
	if header.NativeMeta == 0 {
		t.Fatalf("wrapper should store native descriptor metadata offset")
	}
	native, err := registry.ReadNativeDescriptor(header.NativeMeta)
	if err != nil {
		t.Fatalf("read native descriptor: %v", err)
	}
	if native.DescriptorID != descriptor.ID || native.DescriptorVersion != header.DescriptorVersion {
		t.Fatalf("native descriptor cache mismatch: %+v", native)
	}
	if native.ShapeID != descriptor.ShapeID || native.CacheSlot != descriptor.CacheSlot {
		t.Fatalf("native descriptor shape/cache mismatch: %+v vs %+v", native, descriptor)
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
	if native.CacheSlot != descriptor.CacheSlot || native.ShapeID != descriptor.ShapeID {
		t.Fatalf("native descriptor metadata mismatch: %+v vs %+v", native, descriptor)
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

func uintptrToBytes(value uintptr) []byte {
	return []byte(fmt.Sprintf("%x", value))
}

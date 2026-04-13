package meta

import (
	"encoding/binary"
	"testing"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

func TestRegistryPublishesNativeReadableSnapshot(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	registry := NewRegistry(runtimeHeap)
	if registry.SnapshotOffset() == 0 {
		t.Fatalf("snapshot offset = 0, want non-zero")
	}
	if registry.SnapshotNativeAddress() == 0 {
		t.Fatalf("snapshot native address = 0, want non-zero")
	}
	bytes, err := runtimeHeap.Resolve(registry.SnapshotOffset(), uint64(RegistryEntrySize*KindCount))
	if err != nil {
		t.Fatalf("resolve snapshot bytes: %v", err)
	}
	numberBase := int(KindNumber) * RegistryEntrySize
	if got := binary.LittleEndian.Uint32(bytes[numberBase+RegistryEntryVersionOffset : numberBase+RegistryEntryVersionOffset+4]); got != 0 {
		t.Fatalf("initial number version = %d, want 0", got)
	}
	if got := value.Raw(binary.LittleEndian.Uint64(bytes[numberBase+RegistryEntryMetatableOffset : numberBase+RegistryEntryMetatableOffset+8])); got != value.NilValue().Bits() {
		t.Fatalf("initial number metatable bits = %#x, want %#x", uint64(got), uint64(value.NilValue().Bits()))
	}
	metatable := value.TableRefValue(0x123)
	registry.Set(KindNumber, metatable)
	if got := registry.Version(KindNumber); got != 1 {
		t.Fatalf("number version after first set = %d, want 1", got)
	}
	if got, found := registry.Get(KindNumber); !found || got.Bits() != metatable.Bits() {
		t.Fatalf("number metatable = %s (found=%v), want %s", got, found, metatable)
	}
	if got := binary.LittleEndian.Uint32(bytes[numberBase+RegistryEntryVersionOffset : numberBase+RegistryEntryVersionOffset+4]); got != 1 {
		t.Fatalf("snapshot number version = %d, want 1", got)
	}
	if got := value.Raw(binary.LittleEndian.Uint64(bytes[numberBase+RegistryEntryMetatableOffset : numberBase+RegistryEntryMetatableOffset+8])); got != metatable.Bits() {
		t.Fatalf("snapshot number metatable bits = %#x, want %#x", uint64(got), uint64(metatable.Bits()))
	}
	registry.Set(KindNumber, metatable)
	if got := registry.Version(KindNumber); got != 1 {
		t.Fatalf("repeat set should keep version stable, got %d want 1", got)
	}
	registry.Set(KindNumber, value.NilValue())
	if got := registry.Version(KindNumber); got != 2 {
		t.Fatalf("clear should bump version, got %d want 2", got)
	}
	if _, found := registry.Get(KindNumber); found {
		t.Fatalf("cleared number metatable should not be found")
	}
	if got := value.Raw(binary.LittleEndian.Uint64(bytes[numberBase+RegistryEntryMetatableOffset : numberBase+RegistryEntryMetatableOffset+8])); got != value.NilValue().Bits() {
		t.Fatalf("snapshot number metatable bits after clear = %#x, want %#x", uint64(got), uint64(value.NilValue().Bits()))
	}
}

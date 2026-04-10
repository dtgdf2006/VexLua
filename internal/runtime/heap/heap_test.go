package heap_test

import (
	"testing"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

func TestHeapAllocObjectAlignmentAndHeaderRoundTrip(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	header := value.CommonHeader{
		Kind:      value.KindString,
		Mark:      value.MarkWhite0,
		Flags:     value.HeaderFlagHasEmbeddedRefs,
		SizeBytes: 0x18,
		Version:   3,
		Aux:       9,
	}
	alloc, err := h.AllocObject(header)
	if err != nil {
		t.Fatalf("unexpected allocation error: %v", err)
	}
	if alloc.Offset == 0 {
		t.Fatalf("expected non-zero heap offset")
	}
	if alloc.Address%value.ObjectAlignment != 0 {
		t.Fatalf("allocation address is not aligned: %#x", alloc.Address)
	}
	readHeader, err := h.HeaderAtOffset(alloc.Offset)
	if err != nil {
		t.Fatalf("unexpected read header error: %v", err)
	}
	if readHeader.Kind != value.KindString {
		t.Fatalf("unexpected object kind: %v", readHeader.Kind)
	}
	if readHeader.SizeBytes%value.ObjectAlignment != 0 {
		t.Fatalf("expected aligned size, got %#x", readHeader.SizeBytes)
	}
	if err := h.ValidateObjectAddress(alloc.Address); err != nil {
		t.Fatalf("unexpected object address validation error: %v", err)
	}
}

func TestHeapAddressAndReferenceRoundTrip(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	alloc, err := h.AllocObject(value.CommonHeader{Kind: value.KindTable, SizeBytes: value.CommonHeaderSize})
	if err != nil {
		t.Fatalf("unexpected allocation error: %v", err)
	}
	offset, err := h.OffsetForAddress(alloc.Address)
	if err != nil {
		t.Fatalf("unexpected offset error: %v", err)
	}
	if offset != alloc.Offset {
		t.Fatalf("unexpected offset round-trip: %#x != %#x", uint64(offset), uint64(alloc.Offset))
	}
	address, err := h.AddressForOffset(offset)
	if err != nil {
		t.Fatalf("unexpected address error: %v", err)
	}
	if address != alloc.Address {
		t.Fatalf("unexpected address round-trip: %#x != %#x", address, alloc.Address)
	}
	ref, err := h.EncodeHeapRef(alloc.Address)
	if err != nil {
		t.Fatalf("unexpected heap ref encode error: %v", err)
	}
	boxed := value.TableRefValue(ref)
	decodedRef, ok := boxed.HeapRef()
	if !ok || decodedRef != ref {
		t.Fatalf("unexpected boxed ref decoding: %v", boxed)
	}
	decodedAddress, err := h.DecodeHeapRef(ref)
	if err != nil {
		t.Fatalf("unexpected heap ref decode error: %v", err)
	}
	if decodedAddress != alloc.Address {
		t.Fatalf("unexpected heap ref round-trip address: %#x != %#x", decodedAddress, alloc.Address)
	}
	header, err := h.HeaderAtAddress(decodedAddress)
	if err != nil {
		t.Fatalf("unexpected header read error: %v", err)
	}
	if header.Kind != value.KindTable {
		t.Fatalf("unexpected decoded header kind: %v", header.Kind)
	}
}

func TestHeapAllocCrossesPagesWhenNeeded(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 32)
	first, err := h.AllocObject(value.CommonHeader{Kind: value.KindString, SizeBytes: 0x20})
	if err != nil {
		t.Fatalf("unexpected first allocation error: %v", err)
	}
	second, err := h.AllocObject(value.CommonHeader{Kind: value.KindProto, SizeBytes: 0x20})
	if err != nil {
		t.Fatalf("unexpected second allocation error: %v", err)
	}
	if second.Address <= first.Address {
		t.Fatalf("expected later allocation to have larger logical address")
	}
	if err := h.ValidateObjectAddress(second.Address); err != nil {
		t.Fatalf("unexpected validation error for second allocation: %v", err)
	}
}

func TestHeapSyncsNativeMirrorAtLogicalOffset(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	alloc, err := h.AllocObject(value.CommonHeader{Kind: value.KindProto, SizeBytes: 0x20})
	if err != nil {
		t.Fatalf("unexpected allocation error: %v", err)
	}
	copy(alloc.Bytes[:8], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	if err := h.SyncNative(alloc.Offset, alloc.Bytes); err != nil {
		t.Fatalf("sync native mirror: %v", err)
	}
	nativeAddress, err := h.NativeAddressForOffset(alloc.Offset)
	if err != nil {
		t.Fatalf("resolve native address: %v", err)
	}
	if nativeAddress%value.ObjectAlignment != 0 {
		t.Fatalf("native address is not aligned: %#x", nativeAddress)
	}
	nativeBytes, err := h.ResolveNative(alloc.Offset, 8)
	if err != nil {
		t.Fatalf("resolve native bytes: %v", err)
	}
	var got [8]byte
	copy(got[:], nativeBytes)
	if got != [8]byte{1, 2, 3, 4, 5, 6, 7, 8} {
		t.Fatalf("native mirror bytes = %v, want [1 2 3 4 5 6 7 8]", got)
	}
	if nativeAddress-uintptr(alloc.Offset) != h.NativeBase() {
		t.Fatalf("native base mismatch: address=%#x offset=%#x base=%#x", nativeAddress, uint64(alloc.Offset), h.NativeBase())
	}
}

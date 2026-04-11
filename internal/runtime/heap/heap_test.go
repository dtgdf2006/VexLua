package heap_test

import (
	"runtime"
	"testing"
	"unsafe"

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

func TestHeapNativeAddressRoundTrip(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	alloc, err := h.AllocObject(value.CommonHeader{Kind: value.KindProto, SizeBytes: value.CommonHeaderSize})
	if err != nil {
		t.Fatalf("unexpected allocation error: %v", err)
	}
	nativeAddress, err := h.NativeAddressForOffset(alloc.Offset)
	if err != nil {
		t.Fatalf("unexpected native address error: %v", err)
	}
	offset, err := h.OffsetForNativeAddress(nativeAddress)
	if err != nil {
		t.Fatalf("unexpected native offset error: %v", err)
	}
	if offset != alloc.Offset {
		t.Fatalf("unexpected native offset round-trip: %#x != %#x", uint64(offset), uint64(alloc.Offset))
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

func TestHeapResolveReturnsCanonicalNativeBytes(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	alloc, err := h.AllocObject(value.CommonHeader{Kind: value.KindProto, SizeBytes: 0x20})
	if err != nil {
		t.Fatalf("unexpected allocation error: %v", err)
	}
	copy(alloc.Bytes[:8], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	resolvedBytes, err := h.Resolve(alloc.Offset, 8)
	if err != nil {
		t.Fatalf("resolve bytes: %v", err)
	}
	nativeAddress, err := h.NativeAddressForOffset(alloc.Offset)
	if err != nil {
		t.Fatalf("resolve native address: %v", err)
	}
	if nativeAddress%value.ObjectAlignment != 0 {
		t.Fatalf("native address is not aligned: %#x", nativeAddress)
	}
	if uintptr(unsafe.Pointer(&alloc.Bytes[0])) != nativeAddress {
		t.Fatalf("allocation bytes base mismatch: got %#x want %#x", uintptr(unsafe.Pointer(&alloc.Bytes[0])), nativeAddress)
	}
	if uintptr(unsafe.Pointer(&resolvedBytes[0])) != nativeAddress {
		t.Fatalf("resolved bytes base mismatch: got %#x want %#x", uintptr(unsafe.Pointer(&resolvedBytes[0])), nativeAddress)
	}
	resolvedBytes[0] = 9
	nativeBytes, err := h.Resolve(alloc.Offset, 8)
	if err != nil {
		t.Fatalf("resolve canonical bytes after write: %v", err)
	}
	if alloc.Bytes[0] != 9 || nativeBytes[0] != 9 {
		t.Fatalf("canonical bytes diverged after resolve write: alloc=%d native=%d", alloc.Bytes[0], nativeBytes[0])
	}
	var got [8]byte
	copy(got[:], nativeBytes)
	if got != [8]byte{9, 2, 3, 4, 5, 6, 7, 8} {
		t.Fatalf("canonical native bytes = %v, want [9 2 3 4 5 6 7 8]", got)
	}
	if nativeAddress-uintptr(alloc.Offset) != h.NativeBase() {
		t.Fatalf("native base mismatch: address=%#x offset=%#x base=%#x", nativeAddress, uint64(alloc.Offset), h.NativeBase())
	}
}

func TestHeapNativeBaseRemainsStableAcrossCommitGrowth(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	first, err := h.AllocObject(value.CommonHeader{Kind: value.KindProto, SizeBytes: 0x20})
	if err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	copy(first.Bytes[:8], []byte{1, 3, 5, 7, 9, 11, 13, 15})
	base := h.NativeBase()
	large, err := h.Alloc(256 * 1024)
	if err != nil {
		t.Fatalf("large allocation: %v", err)
	}
	copy(large.Bytes[:8], []byte{2, 4, 6, 8, 10, 12, 14, 16})
	if h.NativeBase() != base {
		t.Fatalf("native base changed across commit growth: got %#x want %#x", h.NativeBase(), base)
	}
}

func TestHeapNativeAddressRemainsValidAfterCommitGrowth(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	first, err := h.AllocObject(value.CommonHeader{Kind: value.KindProto, SizeBytes: 0x20})
	if err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	copy(first.Bytes[:8], []byte{42, 41, 40, 39, 38, 37, 36, 35})
	nativeAddress, err := h.NativeAddressForOffset(first.Offset)
	if err != nil {
		t.Fatalf("native address: %v", err)
	}
	bytesAtAddress, err := h.Resolve(first.Offset, 8)
	if err != nil {
		t.Fatalf("resolve original bytes: %v", err)
	}
	if uintptr(unsafe.Pointer(&bytesAtAddress[0])) != nativeAddress {
		t.Fatalf("native pointer mismatch: ptr=%#x address=%#x", uintptr(unsafe.Pointer(&bytesAtAddress[0])), nativeAddress)
	}
	large, err := h.Alloc(256 * 1024)
	if err != nil {
		t.Fatalf("large allocation: %v", err)
	}
	copy(large.Bytes[:8], []byte{1, 1, 2, 3, 5, 8, 13, 21})
	var got [8]byte
	copy(got[:], bytesAtAddress)
	if got != [8]byte{42, 41, 40, 39, 38, 37, 36, 35} {
		t.Fatalf("native bytes at original address = %v, want [42 41 40 39 38 37 36 35]", got)
	}
}

func TestHeapPublishedNativeAddressesSurviveGC(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	alloc, err := h.AllocObject(value.CommonHeader{Kind: value.KindProto, SizeBytes: 0x20})
	if err != nil {
		t.Fatalf("allocation: %v", err)
	}
	copy(alloc.Bytes[:8], []byte{9, 8, 7, 6, 5, 4, 3, 2})
	base := h.NativeBase()
	nativeAddress, err := h.NativeAddressForOffset(alloc.Offset)
	if err != nil {
		t.Fatalf("native address: %v", err)
	}
	runtime.GC()
	if h.NativeBase() != base {
		t.Fatalf("native base changed after GC: got %#x want %#x", h.NativeBase(), base)
	}
	bytesAtAddress, err := h.Resolve(alloc.Offset, 8)
	if err != nil {
		t.Fatalf("resolve allocation bytes after GC: %v", err)
	}
	var got [8]byte
	copy(got[:], bytesAtAddress)
	if got != [8]byte{9, 8, 7, 6, 5, 4, 3, 2} {
		t.Fatalf("native bytes after GC = %v, want [9 8 7 6 5 4 3 2]", got)
	}
	runtime.KeepAlive(h)
	if nativeAddress-uintptr(alloc.Offset) != base {
		t.Fatalf("published native address base mismatch after GC: address=%#x offset=%#x base=%#x", nativeAddress, uint64(alloc.Offset), base)
	}
}

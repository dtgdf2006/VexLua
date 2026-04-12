package rtstring

import (
	"testing"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

func TestInternDeduplicatesIdentity(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := NewInternTable(runtimeHeap, 0x12345678)

	first, err := strings.Intern("hello")
	if err != nil {
		t.Fatalf("intern first string: %v", err)
	}
	second, err := strings.Intern("hello")
	if err != nil {
		t.Fatalf("intern second string: %v", err)
	}
	third, err := strings.Intern("world")
	if err != nil {
		t.Fatalf("intern third string: %v", err)
	}

	if first.Ref != second.Ref {
		t.Fatalf("interned string identity mismatch: %#x != %#x", uint64(first.Ref), uint64(second.Ref))
	}
	if !IdentityEqual(first.Value, second.Value) {
		t.Fatalf("expected identical interned TValue identities")
	}
	if third.Ref == first.Ref {
		t.Fatalf("different contents should not reuse the same ref")
	}
	if strings.Count() != 2 {
		t.Fatalf("expected two unique interned strings, got %d", strings.Count())
	}
	if _, text, err := StringAt(runtimeHeap, first.Ref); err != nil {
		t.Fatalf("decode first string: %v", err)
	} else if text != "hello" {
		t.Fatalf("unexpected string payload %q", text)
	}
}

func TestStringObjectHeaderAndHash(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := NewInternTable(runtimeHeap, 0x9E3779B9)

	handle, err := strings.Intern("abcdefghijklmnopqrstuvwxyz")
	if err != nil {
		t.Fatalf("intern string: %v", err)
	}
	header, err := strings.Header(handle.Ref)
	if err != nil {
		t.Fatalf("read string header: %v", err)
	}

	if header.Common.Kind.String() != "String" {
		t.Fatalf("unexpected object kind %s", header.Common.Kind)
	}
	if header.Length != uint32(len("abcdefghijklmnopqrstuvwxyz")) {
		t.Fatalf("unexpected string length %d", header.Length)
	}
	expected := HashString("abcdefghijklmnopqrstuvwxyz", strings.Seed())
	if header.Hash != expected {
		t.Fatalf("unexpected hash %#x, want %#x", header.Hash, expected)
	}
	if header.Common.SizeBytes < HeaderSize+uint32(header.Length)+1 {
		t.Fatalf("string object is smaller than payload")
	}
}

func TestStringObjectLivesInContiguousNativeHeapBytes(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := NewInternTable(runtimeHeap, 0xCAFEBABE)
	handle, err := strings.Intern("native-bytes")
	if err != nil {
		t.Fatalf("intern string: %v", err)
	}
	header, err := strings.Header(handle.Ref)
	if err != nil {
		t.Fatalf("read string header: %v", err)
	}
	address, err := runtimeHeap.DecodeHeapRef(handle.Ref)
	if err != nil {
		t.Fatalf("decode heap ref: %v", err)
	}
	if err := runtimeHeap.ValidateObjectAddress(address); err != nil {
		t.Fatalf("validate string address: %v", err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("offset for string address: %v", err)
	}
	if address != runtimeHeap.Base()+uintptr(offset) {
		t.Fatalf("string address %#x does not match heap base+offset %#x", address, runtimeHeap.Base()+uintptr(offset))
	}
	objectBytes, err := runtimeHeap.Resolve(offset, uint64(header.Common.SizeBytes))
	if err != nil {
		t.Fatalf("resolve string object: %v", err)
	}
	decodedHeader, text, err := Decode(objectBytes)
	if err != nil {
		t.Fatalf("decode object bytes: %v", err)
	}
	if decodedHeader != header {
		t.Fatalf("decoded header mismatch: %+v != %+v", decodedHeader, header)
	}
	if text != "native-bytes" {
		t.Fatalf("decoded text %q, want %q", text, "native-bytes")
	}
	if string(objectBytes[DataOffset:DataOffset+len(text)]) != text {
		t.Fatalf("object bytes payload mismatch")
	}
	if objectBytes[DataOffset+len(text)] != 0 {
		t.Fatalf("string object is missing trailing terminator")
	}
}

func TestSweepDeadRemovesEntriesAndUpdatesCount(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := NewInternTable(runtimeHeap, 0xBADC0DE)
	live, err := strings.Intern("live")
	if err != nil {
		t.Fatalf("intern live string: %v", err)
	}
	dead, err := strings.Intern("dead")
	if err != nil {
		t.Fatalf("intern dead string: %v", err)
	}
	removed, err := strings.SweepDead(func(ref value.HeapRef44) (bool, error) {
		return ref == dead.Ref, nil
	})
	if err != nil {
		t.Fatalf("sweep dead strings: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed strings = %d, want 1", removed)
	}
	if strings.Count() != 1 {
		t.Fatalf("string count after sweep = %d, want 1", strings.Count())
	}
	if _, found, err := strings.Lookup("dead"); err != nil {
		t.Fatalf("lookup dead string: %v", err)
	} else if found {
		t.Fatalf("dead string should have been removed from intern table")
	}
	if handle, found, err := strings.Lookup("live"); err != nil {
		t.Fatalf("lookup live string: %v", err)
	} else if !found || handle.Ref != live.Ref {
		t.Fatalf("live string lookup mismatch: found=%v ref=%#x want %#x", found, uint64(handle.Ref), uint64(live.Ref))
	}
}

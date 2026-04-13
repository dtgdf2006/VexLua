package feedback

import (
	"testing"

	"vexlua/internal/runtime/value"
)

func TestFeedbackHeaderAndCellRoundTrip(t *testing.T) {
	headerBytes := make([]byte, HeaderSize)
	header := Header{SlotCount: 3, InterruptBudget: 256, LoopHotness: 7, OSRState: 2, Flags: 9}
	if err := WriteHeader(headerBytes, header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	decodedHeader, err := ReadHeader(headerBytes)
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	if decodedHeader != header {
		t.Fatalf("decoded header = %+v, want %+v", decodedHeader, header)
	}
	cellBytes := make([]byte, CellSize)
	cell := NewTableMonomorphicCell(SlotGetTable, AccessHash, 0x123, 99, 7, value.TableRefValue(0x456).Bits())
	if err := WriteCell(cellBytes, cell); err != nil {
		t.Fatalf("write cell: %v", err)
	}
	decodedCell, err := ReadCell(cellBytes)
	if err != nil {
		t.Fatalf("read cell: %v", err)
	}
	if decodedCell != cell {
		t.Fatalf("decoded cell = %+v, want %+v", decodedCell, cell)
	}
	if decodedCell.Prefix() != PackCellPrefix(StateMonomorphic, AccessHash, SlotGetTable) {
		t.Fatalf("cell prefix = %#x, want %#x", decodedCell.Prefix(), PackCellPrefix(StateMonomorphic, AccessHash, SlotGetTable))
	}
	if decodedCell.TableVersion() != 99 || decodedCell.CachedIndex() != 7 || decodedCell.TableRef() != 0x123 || decodedCell.KeyBits() != value.TableRefValue(0x456).Bits() {
		t.Fatalf("decoded table view = %+v", decodedCell)
	}
}

func TestFamilySpecificMonomorphicCellConstructors(t *testing.T) {
	callCell := NewCallMonomorphicCell(SlotCall, AccessCallResolvedLuaClosure, 0x77, value.TableRefValue(0x123).Bits(), CallShape{Kind: CallShapeTableMetatable, VersionA: 5, VersionB: 9})
	if callCell.TargetRef() != 0x77 || callCell.ObservedValueBits() != value.TableRefValue(0x123).Bits() || callCell.CallShapeKind() != CallShapeTableMetatable || callCell.CallShapeVersionA() != 5 || callCell.CallShapeVersionB() != 9 {
		t.Fatalf("call cell = %+v", callCell)
	}
	upvalueCell := NewUpvalueMonomorphicCell(SlotGetUpvalue, AccessUpvalueClosed, 0x88, value.NumberValue(42).Bits())
	if upvalueCell.TargetRef() != 0x88 || upvalueCell.ObservedValueBits() != value.NumberValue(42).Bits() {
		t.Fatalf("upvalue cell = %+v", upvalueCell)
	}
}

func TestMegamorphicCallSidecarHelpersRoundTrip(t *testing.T) {
	cell := NewMegamorphicCallSidecarCell(SlotCall, 0x123)
	if !cell.HasMegamorphicCallSidecar() || !cell.HasCallSidecar() || cell.CallMegamorphicDataOffset() != 0x123 || cell.CallSidecarDataOffset() != 0x123 {
		t.Fatalf("megamorphic sidecar cell = %+v", cell)
	}
	entries := [CallMegamorphicEntryCount]CallPolymorphicEntry{
		NewCallPolymorphicEntry(AccessCallResolvedLuaClosure, 0x77, value.TableRefValue(0x123).Bits(), CallShape{Kind: CallShapeTableMetatable, VersionA: 5, VersionB: 9}),
		NewCallPolymorphicEntry(AccessCallResolvedHostFunction, 0x88, value.TableRefValue(0x456).Bits(), CallShape{Kind: CallShapeHostObjectMetatable, VersionA: 7, VersionB: 11}),
	}
	bytes := make([]byte, CallMegamorphicDataSize)
	if err := WriteCallMegamorphicEntries(bytes, entries); err != nil {
		t.Fatalf("write megamorphic entries: %v", err)
	}
	decoded, err := ReadCallMegamorphicEntries(bytes)
	if err != nil {
		t.Fatalf("read megamorphic entries: %v", err)
	}
	if decoded != entries {
		t.Fatalf("decoded megamorphic entries = %+v, want %+v", decoded, entries)
	}
}

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
	cell := NewMonomorphicCell(SlotGetTable, AccessHash, 0x123, 99, 7, value.TableRefValue(0x456).Bits())
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
}

package runtime

import "testing"

func TestTableSequentialSetIndexUsesAmortizedGrowth(t *testing.T) {
	table := newTable(0)
	previousCap := cap(table.array)
	growths := 0

	for index := 1; index <= 256; index++ {
		table.SetIndex(index, NumberValue(float64(index)))
		if currentCap := cap(table.array); currentCap != previousCap {
			growths++
			previousCap = currentCap
		}
	}

	if growths > 16 {
		t.Fatalf("sequential array writes grew %d times, want amortized growth", growths)
	}
	if got := table.Length(); got != 256 {
		t.Fatalf("table.Length() = %d, want 256", got)
	}
	for index := 1; index <= 256; index++ {
		value, found := table.GetIndex(index)
		if !found {
			t.Fatalf("GetIndex(%d) reported missing value", index)
		}
		if got := value.Number(); got != float64(index) {
			t.Fatalf("GetIndex(%d) = %v, want %d", index, got, index)
		}
	}
}

func TestTableSparseArrayHolesRemainAbsent(t *testing.T) {
	table := newTable(0)
	table.SetIndex(3, TrueValue)

	if got := table.Length(); got != 0 {
		t.Fatalf("table.Length() = %d, want 0 for sparse leading hole", got)
	}
	for _, index := range []int{1, 2} {
		value, found := table.GetIndex(index)
		if found || value != NilValue {
			t.Fatalf("GetIndex(%d) = (%v, %v), want (nil, false)", index, value, found)
		}
		value, found = table.RawGet(NumberValue(float64(index)))
		if found || value != NilValue {
			t.Fatalf("RawGet(%d) = (%v, %v), want (nil, false)", index, value, found)
		}
	}
	index, value, found := table.nextArrayEntry(0)
	if !found || index != 3 || value != TrueValue {
		t.Fatalf("nextArrayEntry(0) = (%d, %v, %v), want (3, true, true)", index, value, found)
	}
}

func TestTableSetIndexNilTrimsTrailingArray(t *testing.T) {
	table := newTable(0)
	for index := 1; index <= 4; index++ {
		table.SetIndex(index, NumberValue(float64(index)))
	}

	originalCap := cap(table.array)
	table.SetIndex(4, NilValue)
	if got := len(table.array); got != 3 {
		t.Fatalf("len(table.array) after trimming tail = %d, want 3", got)
	}
	if cap(table.array) != originalCap {
		t.Fatalf("cap(table.array) changed from %d to %d while trimming tail", originalCap, cap(table.array))
	}
	table.SetIndex(3, NilValue)
	if got := len(table.array); got != 2 {
		t.Fatalf("len(table.array) after second trim = %d, want 2", got)
	}
}

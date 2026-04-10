package metadata

import "testing"

func TestOffsetTableBuilderAndIterator(t *testing.T) {
	builder := &OffsetTableBuilder{}
	for _, position := range []uint32{0, 5, 9, 24} {
		if err := builder.AddPosition(position); err != nil {
			t.Fatalf("add position %d: %v", position, err)
		}
	}
	iterator := NewOffsetIterator(builder.Bytes())
	want := []uint32{0, 5, 9, 24}
	for index, expected := range want {
		if !iterator.Advance() {
			t.Fatalf("advance %d failed", index)
		}
		if iterator.CurrentCodeOffset() != expected {
			t.Fatalf("offset[%d] = %d, want %d", index, iterator.CurrentCodeOffset(), expected)
		}
	}
	if iterator.Advance() {
		t.Fatalf("iterator should be exhausted")
	}
}

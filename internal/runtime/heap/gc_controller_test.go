package heap_test

import (
	"testing"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

func TestHeapWriteBarrierRegraysBlackParent(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	parent, err := h.AllocObject(value.CommonHeader{Kind: value.KindTable, SizeBytes: value.CommonHeaderSize})
	if err != nil {
		t.Fatalf("alloc parent: %v", err)
	}
	child, err := h.AllocObject(value.CommonHeader{Kind: value.KindString, SizeBytes: value.CommonHeaderSize})
	if err != nil {
		t.Fatalf("alloc child: %v", err)
	}
	setMark(t, h, parent.Offset, value.MarkBlack)
	setMark(t, h, child.Offset, value.MarkWhite0)
	if err := h.SetCurrentWhite(value.MarkWhite0); err != nil {
		t.Fatalf("set current white: %v", err)
	}
	h.SetGCPhase(heap.GCPhaseMark)
	parentRef, err := h.EncodeHeapRef(parent.Address)
	if err != nil {
		t.Fatalf("encode parent ref: %v", err)
	}
	childRef, err := h.EncodeHeapRef(child.Address)
	if err != nil {
		t.Fatalf("encode child ref: %v", err)
	}
	if err := h.WriteBarrier(parentRef, childRef); err != nil {
		t.Fatalf("write barrier: %v", err)
	}
	header, err := h.HeaderAtOffset(parent.Offset)
	if err != nil {
		t.Fatalf("read parent header: %v", err)
	}
	if !header.Mark.Has(value.MarkGray) || !header.Mark.Has(value.MarkRemembered) {
		t.Fatalf("parent mark %#x should include gray+remembered", uint8(header.Mark))
	}
	if header.Mark.Has(value.MarkBlack) {
		t.Fatalf("parent mark %#x should have dropped black bit", uint8(header.Mark))
	}
	queues := h.GCQueueLengths()
	if queues.GrayAgain != 1 || queues.Remembered != 1 {
		t.Fatalf("barrier queues = %+v, want grayAgain=1 remembered=1", queues)
	}
	h.SetGCPhase(heap.GCPhasePause)
	queues = h.GCQueueLengths()
	if queues != (heap.GCQueueLengths{}) {
		t.Fatalf("pause should reset queues, got %+v", queues)
	}
}

func TestHeapWriteBarrierRegraysBlackParentDuringAtomic(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	parent, err := h.AllocObject(value.CommonHeader{Kind: value.KindTable, SizeBytes: value.CommonHeaderSize})
	if err != nil {
		t.Fatalf("alloc parent: %v", err)
	}
	child, err := h.AllocObject(value.CommonHeader{Kind: value.KindString, SizeBytes: value.CommonHeaderSize})
	if err != nil {
		t.Fatalf("alloc child: %v", err)
	}
	setMark(t, h, parent.Offset, value.MarkBlack)
	setMark(t, h, child.Offset, value.MarkWhite0)
	if err := h.SetCurrentWhite(value.MarkWhite0); err != nil {
		t.Fatalf("set current white: %v", err)
	}
	h.SetGCPhase(heap.GCPhaseAtomic)
	parentRef, err := h.EncodeHeapRef(parent.Address)
	if err != nil {
		t.Fatalf("encode parent ref: %v", err)
	}
	childRef, err := h.EncodeHeapRef(child.Address)
	if err != nil {
		t.Fatalf("encode child ref: %v", err)
	}
	if err := h.WriteBarrier(parentRef, childRef); err != nil {
		t.Fatalf("write barrier during atomic: %v", err)
	}
	header, err := h.HeaderAtOffset(parent.Offset)
	if err != nil {
		t.Fatalf("read parent header: %v", err)
	}
	if !header.Mark.Has(value.MarkGray) || !header.Mark.Has(value.MarkRemembered) {
		t.Fatalf("parent mark %#x should include gray+remembered during atomic", uint8(header.Mark))
	}
	if header.Mark.Has(value.MarkBlack) {
		t.Fatalf("parent mark %#x should have dropped black bit during atomic", uint8(header.Mark))
	}
	queues := h.GCQueueLengths()
	if queues.GrayAgain != 1 || queues.Remembered != 1 {
		t.Fatalf("atomic barrier queues = %+v, want grayAgain=1 remembered=1", queues)
	}
}

func TestHeapWriteBarrierIgnoresNonMarkingPhase(t *testing.T) {
	h := heap.MustNew(heap.DefaultHeapBase, 64)
	parent, err := h.AllocObject(value.CommonHeader{Kind: value.KindTable, SizeBytes: value.CommonHeaderSize})
	if err != nil {
		t.Fatalf("alloc parent: %v", err)
	}
	child, err := h.AllocObject(value.CommonHeader{Kind: value.KindString, SizeBytes: value.CommonHeaderSize})
	if err != nil {
		t.Fatalf("alloc child: %v", err)
	}
	setMark(t, h, parent.Offset, value.MarkBlack)
	setMark(t, h, child.Offset, value.MarkWhite0)
	parentRef, err := h.EncodeHeapRef(parent.Address)
	if err != nil {
		t.Fatalf("encode parent ref: %v", err)
	}
	childRef, err := h.EncodeHeapRef(child.Address)
	if err != nil {
		t.Fatalf("encode child ref: %v", err)
	}
	if err := h.WriteBarrier(parentRef, childRef); err != nil {
		t.Fatalf("write barrier outside marking: %v", err)
	}
	header, err := h.HeaderAtOffset(parent.Offset)
	if err != nil {
		t.Fatalf("read parent header: %v", err)
	}
	if header.Mark != value.MarkBlack {
		t.Fatalf("parent mark = %#x, want black unchanged", uint8(header.Mark))
	}
	if queues := h.GCQueueLengths(); queues != (heap.GCQueueLengths{}) {
		t.Fatalf("queues outside marking = %+v, want empty", queues)
	}
}

func setMark(t *testing.T, runtimeHeap *heap.Heap, offset value.HeapOff64, mark value.MarkBits) {
	t.Helper()
	header, err := runtimeHeap.HeaderAtOffset(offset)
	if err != nil {
		t.Fatalf("read header at %#x: %v", uint64(offset), err)
	}
	header.Mark = mark
	if err := runtimeHeap.WriteHeader(offset, header); err != nil {
		t.Fatalf("write header at %#x: %v", uint64(offset), err)
	}
}

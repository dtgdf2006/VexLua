package upvalue

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/state"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
)

func TestUpvalueObjectLayoutContract(t *testing.T) {
	object := NewObject(StateClosed, 7, uintptr(0x1122334455667788))
	object.ClosedValue = value.NumberValue(42)
	object.NextOpen = value.HeapRef44(0xAABBCC)
	object.PrevOpen = value.HeapRef44(0xDDEEFF)
	buffer := make([]byte, ObjectSize)
	if err := WriteObject(buffer, object); err != nil {
		t.Fatalf("write upvalue object: %v", err)
	}
	if got := State(buffer[StateOffset]); got != object.State {
		t.Fatalf("state = %d, want %d", got, object.State)
	}
	if got := buffer[FlagsOffset]; got != 0 {
		t.Fatalf("flags byte = %#x, want 0", got)
	}
	if got := binary.LittleEndian.Uint16(buffer[Reserved0Offset : Reserved0Offset+2]); got != 0 {
		t.Fatalf("reserved0 = %#x, want 0", got)
	}
	if got := binary.LittleEndian.Uint32(buffer[Reserved1Offset : Reserved1Offset+4]); got != 0 {
		t.Fatalf("reserved1 = %#x, want 0", got)
	}
	if got := binary.LittleEndian.Uint64(buffer[SlotAddrOffset : SlotAddrOffset+8]); got != object.SlotAddress {
		t.Fatalf("slot address = %#x, want %#x", got, object.SlotAddress)
	}
	if got := value.Raw(binary.LittleEndian.Uint64(buffer[ClosedValueOffset : ClosedValueOffset+8])); got != object.ClosedValue.Bits() {
		t.Fatalf("closed value bits = %#x, want %#x", uint64(got), uint64(object.ClosedValue.Bits()))
	}
	if got := value.HeapRef44(binary.LittleEndian.Uint64(buffer[NextOpenOffset : NextOpenOffset+8])); got != object.NextOpen {
		t.Fatalf("next open = %#x, want %#x", uint64(got), uint64(object.NextOpen))
	}
	if got := value.HeapRef44(binary.LittleEndian.Uint64(buffer[PrevOpenOffset : PrevOpenOffset+8])); got != object.PrevOpen {
		t.Fatalf("prev open = %#x, want %#x", uint64(got), uint64(object.PrevOpen))
	}
	if got := binary.LittleEndian.Uint64(buffer[ThreadIDOffset : ThreadIDOffset+8]); got != object.ThreadID {
		t.Fatalf("thread id = %d, want %d", got, object.ThreadID)
	}
}

func TestOpenUpvalueOrderingAndClosePath(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	vm := state.NewVMState(runtimeHeap)
	thread, err := vm.NewThread(16, 4)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	manager := NewManager(runtimeHeap, vm)

	slot2, _ := thread.SlotAddress(2)
	slot5, _ := thread.SlotAddress(5)
	if err := thread.SetValueAtAddress(slot2, value.NumberValue(2)); err != nil {
		t.Fatalf("seed slot2: %v", err)
	}
	if err := thread.SetValueAtAddress(slot5, value.NumberValue(5)); err != nil {
		t.Fatalf("seed slot5: %v", err)
	}

	low, err := manager.FindOrCreateOpen(thread, slot2)
	if err != nil {
		t.Fatalf("open low slot: %v", err)
	}
	high, err := manager.FindOrCreateOpen(thread, slot5)
	if err != nil {
		t.Fatalf("open high slot: %v", err)
	}
	duplicate, err := manager.FindOrCreateOpen(thread, slot5)
	if err != nil {
		t.Fatalf("re-open high slot: %v", err)
	}
	if duplicate.Ref != high.Ref {
		t.Fatalf("expected same open upvalue for duplicate lookup")
	}
	if manager.OpenHead(thread) != high.Ref {
		t.Fatalf("expected higher slot to be list head")
	}
	theader := (*state.ThreadStateHeader)(thread.NativePointer())
	if value.HeapRef44(theader.OpenUpvalueHead) != high.Ref {
		t.Fatalf("thread header open head = %#x, want %#x", theader.OpenUpvalueHead, uint64(high.Ref))
	}

	closed, err := manager.CloseAtOrAbove(thread, slot5)
	if err != nil {
		t.Fatalf("close high slot: %v", err)
	}
	if len(closed) != 1 || closed[0].Ref != high.Ref {
		t.Fatalf("expected to close only the high slot")
	}
	if manager.OpenHead(thread) != low.Ref {
		t.Fatalf("expected low slot to remain open after partial close")
	}
	if value.HeapRef44(theader.OpenUpvalueHead) != low.Ref {
		t.Fatalf("thread header open head after close = %#x, want %#x", theader.OpenUpvalueHead, uint64(low.Ref))
	}

	remaining, err := manager.Object(low.Ref)
	if err != nil {
		t.Fatalf("read low upvalue object: %v", err)
	}
	if remaining.State != StateOpen {
		t.Fatalf("expected low upvalue to remain open")
	}
}

func TestUpvalueUsesSingleCanonicalBytes(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	vm := state.NewVMState(runtimeHeap)
	thread, err := vm.NewThread(16, 4)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	manager := NewManager(runtimeHeap, vm)
	slot, err := thread.SlotAddress(3)
	if err != nil {
		t.Fatalf("slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(slot, value.NumberValue(11)); err != nil {
		t.Fatalf("seed slot: %v", err)
	}
	handle, err := manager.FindOrCreateOpen(thread, slot)
	if err != nil {
		t.Fatalf("create open upvalue: %v", err)
	}
	offset := mustOffsetForRef(t, runtimeHeap, handle.Ref)
	bytes, err := runtimeHeap.Resolve(offset, ObjectSize)
	if err != nil {
		t.Fatalf("resolve canonical upvalue bytes: %v", err)
	}
	nativeAddress, err := runtimeHeap.NativeAddressForOffset(offset)
	if err != nil {
		t.Fatalf("resolve native upvalue address: %v", err)
	}
	if uintptr(unsafe.Pointer(&bytes[0])) != nativeAddress {
		t.Fatalf("upvalue bytes base %#x, want %#x", uintptr(unsafe.Pointer(&bytes[0])), nativeAddress)
	}
	closed, err := manager.CloseAtOrAbove(thread, slot)
	if err != nil {
		t.Fatalf("close upvalue: %v", err)
	}
	if len(closed) != 1 || closed[0].Ref != handle.Ref {
		t.Fatalf("unexpected closed upvalue set: %#v", closed)
	}
	if err := manager.Set(handle.Ref, value.BoolValue(true)); err != nil {
		t.Fatalf("write closed upvalue: %v", err)
	}
	decodedNative, err := ReadObject(bytes)
	if err != nil {
		t.Fatalf("decode canonical upvalue bytes: %v", err)
	}
	if decodedNative.State != StateClosed {
		t.Fatalf("native upvalue state = %d, want %d", decodedNative.State, StateClosed)
	}
	if boolean, ok := decodedNative.ClosedValue.Bool(); !ok || !boolean {
		t.Fatalf("native closed value = %s, want bool(true)", decodedNative.ClosedValue)
	}
}

func TestClosedUpvalueWritesTriggerWriteBarrierDuringMarkPhase(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0x2468ACE0)
	vm := state.NewVMState(runtimeHeap)
	thread, err := vm.NewThread(16, 4)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	manager := NewManager(runtimeHeap, vm)
	slot, err := thread.SlotAddress(3)
	if err != nil {
		t.Fatalf("slot address: %v", err)
	}
	first, err := strings.Intern("close-barrier")
	if err != nil {
		t.Fatalf("intern first child: %v", err)
	}
	if err := thread.SetValueAtAddress(slot, first.Value); err != nil {
		t.Fatalf("seed slot: %v", err)
	}
	handle, err := manager.FindOrCreateOpen(thread, slot)
	if err != nil {
		t.Fatalf("create open upvalue: %v", err)
	}
	markRef(t, runtimeHeap, handle.Ref, value.MarkBlack)
	markRef(t, runtimeHeap, first.Ref, value.MarkWhite0)
	if err := runtimeHeap.SetCurrentWhite(value.MarkWhite0); err != nil {
		t.Fatalf("set current white: %v", err)
	}
	runtimeHeap.SetGCPhase(heap.GCPhaseMark)
	closed, err := manager.CloseAtOrAbove(thread, slot)
	if err != nil {
		t.Fatalf("close upvalue: %v", err)
	}
	if len(closed) != 1 || closed[0].Ref != handle.Ref {
		t.Fatalf("unexpected closed handles: %#v", closed)
	}
	queues := runtimeHeap.GCQueueLengths()
	if queues.GrayAgain != 1 || queues.Remembered != 1 {
		t.Fatalf("close barrier queues = %+v, want grayAgain=1 remembered=1", queues)
	}
	runtimeHeap.SetGCPhase(heap.GCPhasePause)
	second, err := strings.Intern("set-barrier")
	if err != nil {
		t.Fatalf("intern second child: %v", err)
	}
	markRef(t, runtimeHeap, handle.Ref, value.MarkBlack)
	markRef(t, runtimeHeap, second.Ref, value.MarkWhite0)
	runtimeHeap.SetGCPhase(heap.GCPhaseMark)
	if err := manager.Set(handle.Ref, second.Value); err != nil {
		t.Fatalf("set closed upvalue: %v", err)
	}
	queues = runtimeHeap.GCQueueLengths()
	if queues.GrayAgain != 1 || queues.Remembered != 1 {
		t.Fatalf("set barrier queues = %+v, want grayAgain=1 remembered=1", queues)
	}
}

func mustOffsetForRef(t *testing.T, runtimeHeap *heap.Heap, ref value.HeapRef44) value.HeapOff64 {
	t.Helper()
	address, err := runtimeHeap.DecodeHeapRef(ref)
	if err != nil {
		t.Fatalf("decode heap ref %#x: %v", uint64(ref), err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("offset for heap ref %#x: %v", uint64(ref), err)
	}
	return offset
}

func markRef(t *testing.T, runtimeHeap *heap.Heap, ref value.HeapRef44, mark value.MarkBits) {
	t.Helper()
	offset := mustOffsetForRef(t, runtimeHeap, ref)
	header, err := runtimeHeap.HeaderAtOffset(offset)
	if err != nil {
		t.Fatalf("read header for %#x: %v", uint64(ref), err)
	}
	header.Mark = mark
	if err := runtimeHeap.WriteHeader(offset, header); err != nil {
		t.Fatalf("write header for %#x: %v", uint64(ref), err)
	}
}

package upvalue

import (
	"testing"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
)

func TestOpenUpvalueOrderingAndClosePath(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	vm := state.NewVMState(runtimeHeap)
	thread, err := vm.NewThread(16, 4)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	manager := NewManager(runtimeHeap)

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

	remaining, err := manager.Object(low.Ref)
	if err != nil {
		t.Fatalf("read low upvalue object: %v", err)
	}
	if remaining.State != StateOpen {
		t.Fatalf("expected low upvalue to remain open")
	}
}

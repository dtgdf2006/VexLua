package upvalue

import (
	"fmt"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
)

type Manager struct {
	heap *heap.Heap
	vm   *state.VMState
}

func NewManager(runtimeHeap *heap.Heap, vm *state.VMState) *Manager {
	if runtimeHeap == nil {
		panic("upvalue manager requires a heap")
	}
	if vm == nil {
		panic("upvalue manager requires vm state")
	}
	return &Manager{heap: runtimeHeap, vm: vm}
}

func (manager *Manager) FindOrCreateOpen(thread *state.ThreadState, slotAddress uintptr) (Handle, error) {
	if thread == nil {
		return Handle{}, fmt.Errorf("thread cannot be nil")
	}
	if _, err := thread.ValueAtAddress(slotAddress); err != nil {
		return Handle{}, err
	}
	head := thread.OpenUpvalueHead()
	var previous value.HeapRef44
	current := head
	for current != 0 {
		object, err := manager.Object(current)
		if err != nil {
			return Handle{}, err
		}
		if object.State != StateOpen {
			return Handle{}, fmt.Errorf("open upvalue list contains non-open state %d", object.State)
		}
		if object.SlotAddress == uint64(slotAddress) {
			return Handle{Ref: current, Value: value.UpValueRefValue(current)}, nil
		}
		if object.SlotAddress < uint64(slotAddress) {
			break
		}
		previous = current
		current = object.NextOpen
	}
	object := NewObject(StateOpen, thread.ID, slotAddress)
	object.PrevOpen = previous
	object.NextOpen = current
	handle, err := manager.allocObject(object)
	if err != nil {
		return Handle{}, err
	}
	if previous == 0 {
		thread.SetOpenUpvalueHead(handle.Ref)
	} else {
		previousObject, err := manager.Object(previous)
		if err != nil {
			return Handle{}, err
		}
		previousObject.NextOpen = handle.Ref
		if err := manager.writeObject(previous, previousObject); err != nil {
			return Handle{}, err
		}
	}
	if current != 0 {
		currentObject, err := manager.Object(current)
		if err != nil {
			return Handle{}, err
		}
		currentObject.PrevOpen = handle.Ref
		if err := manager.writeObject(current, currentObject); err != nil {
			return Handle{}, err
		}
	}
	return handle, nil
}

func (manager *Manager) OpenHead(thread *state.ThreadState) value.HeapRef44 {
	if thread == nil {
		return 0
	}
	return thread.OpenUpvalueHead()
}

func (manager *Manager) CloseAtOrAbove(thread *state.ThreadState, level uintptr) ([]Handle, error) {
	if thread == nil {
		return nil, fmt.Errorf("thread cannot be nil")
	}
	head := thread.OpenUpvalueHead()
	if head == 0 {
		return nil, nil
	}
	closed := make([]Handle, 0)
	current := head
	for current != 0 {
		object, err := manager.Object(current)
		if err != nil {
			return nil, err
		}
		if object.State != StateOpen {
			return nil, fmt.Errorf("open upvalue list contains non-open state %d", object.State)
		}
		if object.SlotAddress < uint64(level) {
			break
		}
		next := object.NextOpen
		currentValue, err := thread.ValueAtAddress(uintptr(object.SlotAddress))
		if err != nil {
			return nil, err
		}
		object.State = StateClosed
		object.ClosedValue = currentValue
		object.NextOpen = 0
		object.PrevOpen = 0
		if err := manager.writeObject(current, object); err != nil {
			return nil, err
		}
		closed = append(closed, Handle{Ref: current, Value: value.UpValueRefValue(current)})
		current = next
	}
	thread.SetOpenUpvalueHead(current)
	if current != 0 {
		object, err := manager.Object(current)
		if err != nil {
			return nil, err
		}
		object.PrevOpen = 0
		if err := manager.writeObject(current, object); err != nil {
			return nil, err
		}
	}
	return closed, nil
}

func (manager *Manager) Get(ref value.HeapRef44) (value.TValue, error) {
	object, err := manager.Object(ref)
	if err != nil {
		return value.NilValue(), err
	}
	if object.State == StateClosed {
		return object.ClosedValue, nil
	}
	thread := manager.vm.ThreadByID(object.ThreadID)
	if thread == nil {
		return value.NilValue(), fmt.Errorf("open upvalue %#x is missing owner thread", uint64(ref))
	}
	return thread.ValueAtAddress(uintptr(object.SlotAddress))
}

func (manager *Manager) Set(ref value.HeapRef44, slotValue value.TValue) error {
	object, err := manager.Object(ref)
	if err != nil {
		return err
	}
	if object.State == StateClosed {
		object.ClosedValue = slotValue
		return manager.writeObject(ref, object)
	}
	thread := manager.vm.ThreadByID(object.ThreadID)
	if thread == nil {
		return fmt.Errorf("open upvalue %#x is missing owner thread", uint64(ref))
	}
	return thread.SetValueAtAddress(uintptr(object.SlotAddress), slotValue)
}

func (manager *Manager) Object(ref value.HeapRef44) (Object, error) {
	_, bytes, err := manager.objectBytes(ref)
	if err != nil {
		return Object{}, err
	}
	return ReadObject(bytes)
}

func (manager *Manager) allocObject(object Object) (Handle, error) {
	allocation, err := manager.heap.AllocObject(object.Common)
	if err != nil {
		return Handle{}, err
	}
	if err := WriteObject(allocation.Bytes, object); err != nil {
		return Handle{}, err
	}
	ref, err := manager.heap.EncodeHeapRef(allocation.Address)
	if err != nil {
		return Handle{}, err
	}
	return Handle{Ref: ref, Value: value.UpValueRefValue(ref)}, nil
}

func (manager *Manager) writeObject(ref value.HeapRef44, object Object) error {
	_, bytes, err := manager.objectBytes(ref)
	if err != nil {
		return err
	}
	return WriteObject(bytes, object)
}

func (manager *Manager) objectBytes(ref value.HeapRef44) (value.HeapOff64, []byte, error) {
	address, err := manager.heap.DecodeHeapRef(ref)
	if err != nil {
		return 0, nil, err
	}
	offset, err := manager.heap.OffsetForAddress(address)
	if err != nil {
		return 0, nil, err
	}
	bytes, err := manager.heap.Resolve(offset, ObjectSize)
	if err != nil {
		return 0, nil, err
	}
	return offset, bytes, nil
}

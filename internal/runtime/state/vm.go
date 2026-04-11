package state

import (
	"fmt"
	"unsafe"

	"vexlua/internal/runtime/value"
)

const (
	VMStateHeaderSize              = 0x38
	VMStateHeapBaseOffset          = 0x00
	VMStateActiveThreadStackOffset = 0x08
	VMStateActiveThreadFrameOffset = 0x10
	VMStateActiveThreadStackEndOff = 0x18
	VMStateActiveThreadFrameEndOff = 0x20
	VMStateThreadCountOffset       = 0x28
	VMStateFlagsOffset             = 0x2C
	VMStateActiveThreadStateOffset = 0x30
)

type VMStateHeader struct {
	HeapBase              uint64
	ActiveThreadStackBase uint64
	ActiveThreadFrameBase uint64
	ActiveThreadStackEnd  uint64
	ActiveThreadFrameEnd  uint64
	ThreadCount           uint32
	Flags                 uint32
	ActiveThreadStateBase uint64
}

func (vm *VMState) NativePointer() unsafe.Pointer {
	if vm == nil {
		return nil
	}
	return unsafe.Pointer(vm.nativeHeader)
}

func (vm *VMState) SyncActiveThread(thread *ThreadState) {
	if vm == nil {
		return
	}
	vm.syncHeader(thread)
}

func (vm *VMState) syncHeader(thread *ThreadState) {
	if vm == nil || vm.nativeHeader == nil {
		return
	}
	vm.nativeHeader.HeapBase = uint64(vm.HeapBase)
	vm.nativeHeader.ThreadCount = uint32(len(vm.threads))
	if thread == nil {
		vm.nativeHeader.ActiveThreadStackBase = 0
		vm.nativeHeader.ActiveThreadFrameBase = 0
		vm.nativeHeader.ActiveThreadStackEnd = 0
		vm.nativeHeader.ActiveThreadFrameEnd = 0
		vm.nativeHeader.ActiveThreadStateBase = 0
		return
	}
	vm.nativeHeader.ActiveThreadStackBase = uint64(thread.stackBase)
	vm.nativeHeader.ActiveThreadFrameBase = uint64(thread.frameBase)
	vm.nativeHeader.ActiveThreadStackEnd = uint64(thread.stackBase + uintptr(len(thread.stack))*value.TValueSize)
	vm.nativeHeader.ActiveThreadFrameEnd = uint64(thread.frameBase + uintptr(len(thread.frames))*CallFrameHeaderSize)
	vm.nativeHeader.ActiveThreadStateBase = uint64(uintptr(thread.NativePointer()))
}

func ValidateVMStateLayout() error {
	if unsafe.Sizeof(VMStateHeader{}) != VMStateHeaderSize {
		return fmt.Errorf("VMStateHeader size mismatch: got %#x want %#x", unsafe.Sizeof(VMStateHeader{}), VMStateHeaderSize)
	}
	if unsafe.Offsetof(VMStateHeader{}.HeapBase) != VMStateHeapBaseOffset {
		return fmt.Errorf("VMStateHeader.HeapBase offset mismatch: got %#x want %#x", unsafe.Offsetof(VMStateHeader{}.HeapBase), VMStateHeapBaseOffset)
	}
	if unsafe.Offsetof(VMStateHeader{}.ActiveThreadStackBase) != VMStateActiveThreadStackOffset {
		return fmt.Errorf("VMStateHeader.ActiveThreadStackBase offset mismatch: got %#x want %#x", unsafe.Offsetof(VMStateHeader{}.ActiveThreadStackBase), VMStateActiveThreadStackOffset)
	}
	if unsafe.Offsetof(VMStateHeader{}.ActiveThreadFrameBase) != VMStateActiveThreadFrameOffset {
		return fmt.Errorf("VMStateHeader.ActiveThreadFrameBase offset mismatch: got %#x want %#x", unsafe.Offsetof(VMStateHeader{}.ActiveThreadFrameBase), VMStateActiveThreadFrameOffset)
	}
	if unsafe.Offsetof(VMStateHeader{}.ActiveThreadStackEnd) != VMStateActiveThreadStackEndOff {
		return fmt.Errorf("VMStateHeader.ActiveThreadStackEnd offset mismatch: got %#x want %#x", unsafe.Offsetof(VMStateHeader{}.ActiveThreadStackEnd), VMStateActiveThreadStackEndOff)
	}
	if unsafe.Offsetof(VMStateHeader{}.ActiveThreadFrameEnd) != VMStateActiveThreadFrameEndOff {
		return fmt.Errorf("VMStateHeader.ActiveThreadFrameEnd offset mismatch: got %#x want %#x", unsafe.Offsetof(VMStateHeader{}.ActiveThreadFrameEnd), VMStateActiveThreadFrameEndOff)
	}
	if unsafe.Offsetof(VMStateHeader{}.ThreadCount) != VMStateThreadCountOffset {
		return fmt.Errorf("VMStateHeader.ThreadCount offset mismatch: got %#x want %#x", unsafe.Offsetof(VMStateHeader{}.ThreadCount), VMStateThreadCountOffset)
	}
	if unsafe.Offsetof(VMStateHeader{}.Flags) != VMStateFlagsOffset {
		return fmt.Errorf("VMStateHeader.Flags offset mismatch: got %#x want %#x", unsafe.Offsetof(VMStateHeader{}.Flags), VMStateFlagsOffset)
	}
	if unsafe.Offsetof(VMStateHeader{}.ActiveThreadStateBase) != VMStateActiveThreadStateOffset {
		return fmt.Errorf("VMStateHeader.ActiveThreadStateBase offset mismatch: got %#x want %#x", unsafe.Offsetof(VMStateHeader{}.ActiveThreadStateBase), VMStateActiveThreadStateOffset)
	}
	if err := ValidateThreadStateLayout(); err != nil {
		return err
	}
	return nil
}

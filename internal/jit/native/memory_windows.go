//go:build windows

package native

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	memCommit       = 0x1000
	memReserve      = 0x2000
	memRelease      = 0x8000
	pageReadWrite   = 0x04
	pageExecuteRead = 0x20
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procVirtualAlloc   = kernel32.NewProc("VirtualAlloc")
	procVirtualProtect = kernel32.NewProc("VirtualProtect")
	procVirtualFree    = kernel32.NewProc("VirtualFree")
)

type ExecutableMemory struct {
	addr   uintptr
	size   int
	sealed bool
}

func AllocExecutable(size int) (*ExecutableMemory, error) {
	if size <= 0 {
		size = 1
	}
	addr, _, err := procVirtualAlloc.Call(0, uintptr(size), memCommit|memReserve, pageReadWrite)
	if addr == 0 {
		return nil, fmt.Errorf("VirtualAlloc executable memory failed: %w", err)
	}
	return &ExecutableMemory{addr: addr, size: size}, nil
}

func (mem *ExecutableMemory) Write(code []byte) error {
	if mem == nil || mem.addr == 0 {
		return fmt.Errorf("executable memory is not allocated")
	}
	if len(code) > mem.size {
		return fmt.Errorf("code size %d exceeds executable allocation %d", len(code), mem.size)
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(mem.addr)), mem.size)
	copy(dst, code)
	for i := len(code); i < len(dst); i++ {
		dst[i] = 0x90
	}
	return nil
}

func (mem *ExecutableMemory) Seal() error {
	if mem == nil || mem.addr == 0 {
		return fmt.Errorf("executable memory is not allocated")
	}
	if mem.sealed {
		return nil
	}
	var oldProtect uintptr
	r1, _, err := procVirtualProtect.Call(mem.addr, uintptr(mem.size), pageExecuteRead, uintptr(unsafe.Pointer(&oldProtect)))
	if r1 == 0 {
		return fmt.Errorf("VirtualProtect executable memory failed: %w", err)
	}
	mem.sealed = true
	return nil
}

func (mem *ExecutableMemory) Entry(offset uint32) uintptr {
	if mem == nil || mem.addr == 0 || int(offset) >= mem.size {
		return 0
	}
	return mem.addr + uintptr(offset)
}

func (mem *ExecutableMemory) Size() int {
	if mem == nil {
		return 0
	}
	return mem.size
}

func (mem *ExecutableMemory) Close() error {
	if mem == nil || mem.addr == 0 {
		return nil
	}
	r1, _, err := procVirtualFree.Call(mem.addr, 0, memRelease)
	if r1 == 0 {
		return fmt.Errorf("VirtualFree executable memory failed: %w", err)
	}
	mem.addr = 0
	mem.size = 0
	mem.sealed = false
	return nil
}

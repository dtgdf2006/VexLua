//go:build linux

package native

import (
	"fmt"
	"syscall"
	"unsafe"
)

type ExecutableMemory struct {
	data   []byte
	sealed bool
}

func AllocExecutable(size int) (*ExecutableMemory, error) {
	if size <= 0 {
		size = 1
	}
	data, err := syscall.Mmap(-1, 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, fmt.Errorf("mmap executable memory failed: %w", err)
	}
	return &ExecutableMemory{data: data}, nil
}

func (mem *ExecutableMemory) Write(code []byte) error {
	if mem == nil || len(mem.data) == 0 {
		return fmt.Errorf("executable memory is not allocated")
	}
	if len(code) > len(mem.data) {
		return fmt.Errorf("code size %d exceeds executable allocation %d", len(code), len(mem.data))
	}
	copy(mem.data, code)
	for i := len(code); i < len(mem.data); i++ {
		mem.data[i] = 0x90
	}
	return nil
}

func (mem *ExecutableMemory) Seal() error {
	if mem == nil || len(mem.data) == 0 {
		return fmt.Errorf("executable memory is not allocated")
	}
	if mem.sealed {
		return nil
	}
	if err := syscall.Mprotect(mem.data, syscall.PROT_READ|syscall.PROT_EXEC); err != nil {
		return fmt.Errorf("mprotect executable memory failed: %w", err)
	}
	mem.sealed = true
	return nil
}

func (mem *ExecutableMemory) Entry(offset uint32) uintptr {
	if mem == nil || int(offset) >= len(mem.data) || len(mem.data) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&mem.data[offset]))
}

func (mem *ExecutableMemory) Size() int {
	if mem == nil {
		return 0
	}
	return len(mem.data)
}

func (mem *ExecutableMemory) Close() error {
	if mem == nil || len(mem.data) == 0 {
		return nil
	}
	err := syscall.Munmap(mem.data)
	mem.data = nil
	mem.sealed = false
	if err != nil {
		return fmt.Errorf("munmap executable memory failed: %w", err)
	}
	return nil
}

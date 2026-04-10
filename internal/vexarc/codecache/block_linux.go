//go:build linux && amd64

package codecache

import (
	"fmt"
	"syscall"
	"unsafe"
)

func allocExecutable(code []byte) (*Block, error) {
	if len(code) == 0 {
		return nil, fmt.Errorf("executable code cannot be empty")
	}
	mapped, err := syscall.Mmap(-1, 0, len(code), syscall.PROT_READ|syscall.PROT_WRITE|syscall.PROT_EXEC, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, fmt.Errorf("mmap failed: %w", err)
	}
	copy(mapped, code)
	return &Block{addr: uintptr(unsafe.Pointer(&mapped[0])), size: uintptr(len(mapped)), mmap: mapped}, nil
}

func freeExecutable(block *Block) error {
	if block == nil || block.addr == 0 {
		return nil
	}
	if err := syscall.Munmap(block.mmap); err != nil {
		return fmt.Errorf("munmap failed: %w", err)
	}
	block.addr = 0
	block.size = 0
	block.mmap = nil
	return nil
}

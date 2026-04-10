//go:build linux && amd64

package heap

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

type linuxNativeArena struct {
	mapping       []byte
	basePointer   unsafe.Pointer
	reservedSize  uint64
	committedSize uint64
	pageSize      uint64
}

func nativeArenaPageSize() (uint64, error) {
	pageSize := os.Getpagesize()
	if pageSize <= 0 {
		return 0, fmt.Errorf("invalid page size %d", pageSize)
	}
	return uint64(pageSize), nil
}

func reserveNativeArena(reserveSize uint64, pageSize uint64) (platformNativeArena, error) {
	mapping, err := syscall.Mmap(-1, 0, int(reserveSize), syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, fmt.Errorf("mmap reserve failed: %w", err)
	}
	return &linuxNativeArena{
		mapping:      mapping,
		basePointer:  unsafe.Pointer(&mapping[0]),
		reservedSize: reserveSize,
		pageSize:     pageSize,
	}, nil
}

func (arena *linuxNativeArena) base() uintptr {
	return uintptr(arena.basePointer)
}

func (arena *linuxNativeArena) pointer() unsafe.Pointer {
	return arena.basePointer
}

func (arena *linuxNativeArena) ensureCommitted(size uint64) error {
	if size <= arena.committedSize {
		return nil
	}
	if size > arena.reservedSize {
		return fmt.Errorf("native arena exhausted: need %d bytes, reserved %d", size, arena.reservedSize)
	}
	newCommitted := alignUp(size, arena.pageSize)
	if err := syscall.Mprotect(arena.mapping[arena.committedSize:newCommitted], syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
		return fmt.Errorf("mprotect commit failed: %w", err)
	}
	arena.committedSize = newCommitted
	return nil

}

func (arena *linuxNativeArena) close() error {
	if arena == nil || arena.mapping == nil {
		return nil
	}
	if err := syscall.Munmap(arena.mapping); err != nil {
		return fmt.Errorf("munmap failed: %w", err)
	}
	arena.mapping = nil
	arena.basePointer = nil
	arena.reservedSize = 0
	arena.committedSize = 0
	return nil
}

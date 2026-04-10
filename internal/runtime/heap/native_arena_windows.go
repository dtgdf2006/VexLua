//go:build windows && amd64

package heap

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	nativeMemCommit  = 0x1000
	nativeMemReserve = 0x2000
	nativeMemRelease = 0x8000
	pageReadWrite    = 0x04
)

var (
	nativeKernel32         = syscall.NewLazyDLL("kernel32.dll")
	procNativeVirtualAlloc = nativeKernel32.NewProc("VirtualAlloc")
	procNativeVirtualFree  = nativeKernel32.NewProc("VirtualFree")
)

type windowsNativeArena struct {
	baseAddress   uintptr
	mapping       []byte
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
	address, _, callErr := procNativeVirtualAlloc.Call(0, uintptr(reserveSize), nativeMemReserve, pageReadWrite)
	if address == 0 {
		return nil, fmt.Errorf("VirtualAlloc reserve failed: %v", callErr)
	}
	mapping, err := sliceFromAddress(address, reserveSize)
	if err != nil {
		result, _, freeErr := procNativeVirtualFree.Call(address, 0, nativeMemRelease)
		if result == 0 {
			return nil, fmt.Errorf("native arena slice creation failed: %v (release failed: %v)", err, freeErr)
		}
		return nil, err
	}
	return &windowsNativeArena{baseAddress: address, mapping: mapping, reservedSize: reserveSize, pageSize: pageSize}, nil
}

func (arena *windowsNativeArena) base() uintptr {
	return arena.baseAddress
}

func (arena *windowsNativeArena) pointer() unsafe.Pointer {
	return unsafe.Pointer(unsafe.SliceData(arena.mapping))
}

func (arena *windowsNativeArena) ensureCommitted(size uint64) error {
	if size <= arena.committedSize {
		return nil
	}
	if size > arena.reservedSize {
		return fmt.Errorf("native arena exhausted: need %d bytes, reserved %d", size, arena.reservedSize)
	}
	newCommitted := alignUp(size, arena.pageSize)
	commitSize := newCommitted - arena.committedSize
	if commitSize == 0 {
		return nil
	}
	address := arena.baseAddress + uintptr(arena.committedSize)
	result, _, callErr := procNativeVirtualAlloc.Call(address, uintptr(commitSize), nativeMemCommit, pageReadWrite)
	if result == 0 {
		return fmt.Errorf("VirtualAlloc commit failed: %v", callErr)
	}
	if uintptr(result) != address {
		return fmt.Errorf("VirtualAlloc commit returned unexpected address %#x, want %#x", result, address)
	}
	arena.committedSize = newCommitted
	return nil
}

func (arena *windowsNativeArena) close() error {
	if arena == nil || arena.baseAddress == 0 {
		return nil
	}
	result, _, callErr := procNativeVirtualFree.Call(arena.baseAddress, 0, nativeMemRelease)
	if result == 0 {
		return fmt.Errorf("VirtualFree failed: %v", callErr)
	}
	arena.baseAddress = 0
	arena.mapping = nil
	arena.reservedSize = 0
	arena.committedSize = 0
	return nil
}

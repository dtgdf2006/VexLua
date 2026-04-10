//go:build windows && amd64

package codecache

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	memCommit            = 0x1000
	memReserve           = 0x2000
	memRelease           = 0x8000
	pageExecuteReadWrite = 0x40
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procVirtualAlloc  = kernel32.NewProc("VirtualAlloc")
	procVirtualFree   = kernel32.NewProc("VirtualFree")
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

func allocExecutable(code []byte) (*Block, error) {
	if len(code) == 0 {
		return nil, fmt.Errorf("executable code cannot be empty")
	}
	address, _, callErr := procVirtualAlloc.Call(0, uintptr(len(code)), memCommit|memReserve, pageExecuteReadWrite)
	if address == 0 {
		return nil, fmt.Errorf("VirtualAlloc failed: %v", callErr)
	}
	procRtlMoveMemory.Call(address, uintptr(unsafe.Pointer(&code[0])), uintptr(len(code)))
	return &Block{addr: address, size: uintptr(len(code))}, nil
}

func freeExecutable(block *Block) error {
	if block == nil || block.addr == 0 {
		return nil
	}
	result, _, callErr := procVirtualFree.Call(block.addr, 0, memRelease)
	if result == 0 {
		return fmt.Errorf("VirtualFree failed: %v", callErr)
	}
	block.addr = 0
	block.size = 0
	return nil
}

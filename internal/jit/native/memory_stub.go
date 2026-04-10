//go:build !windows && !linux

package native

import "fmt"

type ExecutableMemory struct{}

func AllocExecutable(size int) (*ExecutableMemory, error) {
	return nil, fmt.Errorf("executable memory is not implemented on this platform")
}

func (mem *ExecutableMemory) Write(code []byte) error {
	return fmt.Errorf("executable memory is not implemented on this platform")
}

func (mem *ExecutableMemory) Seal() error {
	return fmt.Errorf("executable memory is not implemented on this platform")
}

func (mem *ExecutableMemory) Entry(offset uint32) uintptr {
	return 0
}

func (mem *ExecutableMemory) Size() int {
	return 0
}

func (mem *ExecutableMemory) Close() error {
	return nil
}

package heap

import (
	"fmt"
	"unsafe"
)

type rawSliceHeader struct {
	Data uintptr
	Len  int
	Cap  int
}

type nativeArena interface {
	Base() uintptr
	EnsureCommitted(size uint64) error
	Bytes(offset uint64, size uint64) ([]byte, error)
}

type platformNativeArena interface {
	base() uintptr
	pointer() unsafe.Pointer
	ensureCommitted(size uint64) error
	close() error
}

type reservedNativeArena struct {
	impl         platformNativeArena
	reservedSize uint64
	pageSize     uint64
}

func newNativeArena(reserveSize uint64, initialSize uint64) (nativeArena, error) {
	pageSize, err := nativeArenaPageSize()
	if err != nil {
		return nil, err
	}
	if reserveSize < initialSize {
		reserveSize = initialSize
	}
	reserveSize = alignUp(reserveSize, pageSize)
	impl, err := reserveNativeArena(reserveSize, pageSize)
	if err != nil {
		return nil, err
	}
	arena := &reservedNativeArena{
		impl:         impl,
		reservedSize: reserveSize,
		pageSize:     pageSize,
	}
	if err := arena.EnsureCommitted(initialSize); err != nil {
		_ = impl.close()
		return nil, err
	}
	return arena, nil
}

func sliceFromAddress(address uintptr, size uint64) ([]byte, error) {
	if size == 0 {
		return []byte{}, nil
	}
	length := int(size)
	if uint64(length) != size {
		return nil, fmt.Errorf("native slice size %d exceeds int range", size)
	}
	header := rawSliceHeader{Data: address, Len: length, Cap: length}
	return *(*[]byte)(unsafe.Pointer(&header)), nil
}

func (arena *reservedNativeArena) Base() uintptr {
	return arena.impl.base()
}

func (arena *reservedNativeArena) EnsureCommitted(size uint64) error {
	if size == 0 {
		return nil
	}
	if size > arena.reservedSize {
		return fmt.Errorf("native arena exhausted: need %d bytes, reserved %d", size, arena.reservedSize)
	}
	return arena.impl.ensureCommitted(size)
}

func (arena *reservedNativeArena) Bytes(offset uint64, size uint64) ([]byte, error) {
	if size == 0 {
		return []byte{}, nil
	}
	end := offset + size
	if end < offset {
		return nil, fmt.Errorf("native arena offset overflow: offset=%d size=%d", offset, size)
	}
	if end > arena.reservedSize {
		return nil, fmt.Errorf("native arena slice exceeds reservation: offset=%d size=%d reserved=%d", offset, size, arena.reservedSize)
	}
	return unsafe.Slice((*byte)(unsafe.Add(arena.impl.pointer(), uintptr(offset))), int(size)), nil
}

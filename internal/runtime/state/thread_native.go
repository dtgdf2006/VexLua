package state

import (
	"fmt"
	"unsafe"
)

const (
	ThreadStateHeaderSize       = 0x30
	ThreadStackBaseOffset       = 0x00
	ThreadStackEndOffset        = 0x08
	ThreadFrameBaseOffset       = 0x10
	ThreadFrameEndOffset        = 0x18
	ThreadOpenUpvalueHeadOffset = 0x20
	ThreadCurrentFrameOffset    = 0x28
)

type ThreadStateHeader struct {
	StackBase       uint64
	StackEnd        uint64
	FrameBase       uint64
	FrameEnd        uint64
	OpenUpvalueHead uint64
	CurrentFrame    uint64
}

func ValidateThreadStateLayout() error {
	if unsafe.Sizeof(ThreadStateHeader{}) != ThreadStateHeaderSize {
		return fmt.Errorf("ThreadStateHeader size mismatch: got %#x want %#x", unsafe.Sizeof(ThreadStateHeader{}), ThreadStateHeaderSize)
	}
	if unsafe.Offsetof(ThreadStateHeader{}.StackBase) != ThreadStackBaseOffset {
		return fmt.Errorf("ThreadStateHeader.StackBase offset mismatch: got %#x want %#x", unsafe.Offsetof(ThreadStateHeader{}.StackBase), ThreadStackBaseOffset)
	}
	if unsafe.Offsetof(ThreadStateHeader{}.StackEnd) != ThreadStackEndOffset {
		return fmt.Errorf("ThreadStateHeader.StackEnd offset mismatch: got %#x want %#x", unsafe.Offsetof(ThreadStateHeader{}.StackEnd), ThreadStackEndOffset)
	}
	if unsafe.Offsetof(ThreadStateHeader{}.FrameBase) != ThreadFrameBaseOffset {
		return fmt.Errorf("ThreadStateHeader.FrameBase offset mismatch: got %#x want %#x", unsafe.Offsetof(ThreadStateHeader{}.FrameBase), ThreadFrameBaseOffset)
	}
	if unsafe.Offsetof(ThreadStateHeader{}.FrameEnd) != ThreadFrameEndOffset {
		return fmt.Errorf("ThreadStateHeader.FrameEnd offset mismatch: got %#x want %#x", unsafe.Offsetof(ThreadStateHeader{}.FrameEnd), ThreadFrameEndOffset)
	}
	if unsafe.Offsetof(ThreadStateHeader{}.OpenUpvalueHead) != ThreadOpenUpvalueHeadOffset {
		return fmt.Errorf("ThreadStateHeader.OpenUpvalueHead offset mismatch: got %#x want %#x", unsafe.Offsetof(ThreadStateHeader{}.OpenUpvalueHead), ThreadOpenUpvalueHeadOffset)
	}
	if unsafe.Offsetof(ThreadStateHeader{}.CurrentFrame) != ThreadCurrentFrameOffset {
		return fmt.Errorf("ThreadStateHeader.CurrentFrame offset mismatch: got %#x want %#x", unsafe.Offsetof(ThreadStateHeader{}.CurrentFrame), ThreadCurrentFrameOffset)
	}
	return nil
}

package upvalue

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/runtime/value"
)

type State uint8

const (
	ObjectSize        = 0x40
	StateOffset       = 0x10
	FlagsOffset       = 0x11
	Reserved0Offset   = 0x12
	Reserved1Offset   = 0x14
	SlotAddrOffset    = 0x18
	ClosedValueOffset = 0x20
	NextOpenOffset    = 0x28
	PrevOpenOffset    = 0x30
	ThreadIDOffset    = 0x38
)

const (
	StateInvalid State = iota
	StateOpen
	StateClosed
)

type Object struct {
	Common      value.CommonHeader
	State       State
	SlotAddress uint64
	ClosedValue value.TValue
	NextOpen    value.HeapRef44
	PrevOpen    value.HeapRef44
	ThreadID    uint64
}

type Handle struct {
	Ref   value.HeapRef44
	Value value.TValue
}

func NewObject(state State, threadID uint64, slotAddress uintptr) Object {
	return Object{
		Common: value.CommonHeader{
			Kind:      value.KindUpValue,
			SizeBytes: ObjectSize,
			Version:   1,
		},
		State:       state,
		SlotAddress: uint64(slotAddress),
		ClosedValue: value.NilValue(),
		ThreadID:    threadID,
	}
}

func ReadObject(buffer []byte) (Object, error) {
	if len(buffer) < ObjectSize {
		return Object{}, fmt.Errorf("buffer too small for upvalue object: %d", len(buffer))
	}
	common, err := value.ReadCommonHeader(buffer)
	if err != nil {
		return Object{}, err
	}
	if common.Kind != value.KindUpValue {
		return Object{}, fmt.Errorf("expected %s object, got %s", value.KindUpValue, common.Kind)
	}
	return Object{
		Common:      common,
		State:       State(buffer[StateOffset]),
		SlotAddress: binary.LittleEndian.Uint64(buffer[SlotAddrOffset : SlotAddrOffset+8]),
		ClosedValue: value.FromRaw(value.Raw(binary.LittleEndian.Uint64(buffer[ClosedValueOffset : ClosedValueOffset+8]))),
		NextOpen:    value.HeapRef44(binary.LittleEndian.Uint64(buffer[NextOpenOffset : NextOpenOffset+8])),
		PrevOpen:    value.HeapRef44(binary.LittleEndian.Uint64(buffer[PrevOpenOffset : PrevOpenOffset+8])),
		ThreadID:    binary.LittleEndian.Uint64(buffer[ThreadIDOffset : ThreadIDOffset+8]),
	}, nil
}

func WriteObject(buffer []byte, object Object) error {
	if len(buffer) < ObjectSize {
		return fmt.Errorf("buffer too small for upvalue object: %d", len(buffer))
	}
	if current, err := value.ReadCommonHeader(buffer); err == nil && current.Kind == object.Common.Kind {
		object.Common.Mark = current.Mark
	}
	if err := value.WriteCommonHeader(buffer, object.Common); err != nil {
		return err
	}
	buffer[StateOffset] = byte(object.State)
	buffer[FlagsOffset] = 0
	binary.LittleEndian.PutUint16(buffer[Reserved0Offset:Reserved0Offset+2], 0)
	binary.LittleEndian.PutUint32(buffer[Reserved1Offset:Reserved1Offset+4], 0)
	binary.LittleEndian.PutUint64(buffer[SlotAddrOffset:SlotAddrOffset+8], object.SlotAddress)
	binary.LittleEndian.PutUint64(buffer[ClosedValueOffset:ClosedValueOffset+8], uint64(object.ClosedValue.Bits()))
	binary.LittleEndian.PutUint64(buffer[NextOpenOffset:NextOpenOffset+8], uint64(object.NextOpen))
	binary.LittleEndian.PutUint64(buffer[PrevOpenOffset:PrevOpenOffset+8], uint64(object.PrevOpen))
	binary.LittleEndian.PutUint64(buffer[ThreadIDOffset:ThreadIDOffset+8], object.ThreadID)
	return nil
}

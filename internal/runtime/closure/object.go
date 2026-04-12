package closure

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/runtime/value"
)

const (
	ObjectSize         = 0x40
	ProtoOffset        = 0x10
	EnvOffset          = 0x18
	UpvalueCountOffset = 0x20
	FlagsOffset        = 0x22
	Reserved0Offset    = 0x24
	UpvaluesDataOffset = 0x28
	FeedbackVectorOff  = 0x30
	FeedbackSlotsOff   = 0x38
	Reserved2Offset    = 0x3C
)

type Object struct {
	Common       value.CommonHeader
	Proto        value.TValue
	Env          value.TValue
	UpvalueCount uint16
	Flags        uint16
	UpvaluesData value.HeapOff64
	FeedbackData value.HeapOff64
	FeedbackSize uint32
}

type Handle struct {
	Ref   value.HeapRef44
	Value value.TValue
}

func (object Object) ProtoRef() (value.HeapRef44, bool) {
	return object.Proto.HeapRef()
}

func NewObject(proto value.TValue, env value.TValue, upvalueCount uint16, upvaluesData value.HeapOff64) Object {
	return Object{
		Common: value.CommonHeader{
			Kind:      value.KindLuaClosure,
			SizeBytes: ObjectSize,
			Version:   1,
		},
		Proto:        proto,
		Env:          env,
		UpvalueCount: upvalueCount,
		UpvaluesData: upvaluesData,
	}
}

func ReadObject(buffer []byte) (Object, error) {
	if len(buffer) < ObjectSize {
		return Object{}, fmt.Errorf("buffer too small for closure object: %d", len(buffer))
	}
	common, err := value.ReadCommonHeader(buffer)
	if err != nil {
		return Object{}, err
	}
	if common.Kind != value.KindLuaClosure {
		return Object{}, fmt.Errorf("expected %s object, got %s", value.KindLuaClosure, common.Kind)
	}
	return Object{
		Common:       common,
		Proto:        value.FromRaw(value.Raw(binary.LittleEndian.Uint64(buffer[ProtoOffset : ProtoOffset+8]))),
		Env:          value.FromRaw(value.Raw(binary.LittleEndian.Uint64(buffer[EnvOffset : EnvOffset+8]))),
		UpvalueCount: binary.LittleEndian.Uint16(buffer[UpvalueCountOffset : UpvalueCountOffset+2]),
		Flags:        binary.LittleEndian.Uint16(buffer[FlagsOffset : FlagsOffset+2]),
		UpvaluesData: value.HeapOff64(binary.LittleEndian.Uint64(buffer[UpvaluesDataOffset : UpvaluesDataOffset+8])),
		FeedbackData: value.HeapOff64(binary.LittleEndian.Uint64(buffer[FeedbackVectorOff : FeedbackVectorOff+8])),
		FeedbackSize: binary.LittleEndian.Uint32(buffer[FeedbackSlotsOff : FeedbackSlotsOff+4]),
	}, nil
}

func WriteObject(buffer []byte, object Object) error {
	if len(buffer) < ObjectSize {
		return fmt.Errorf("buffer too small for closure object: %d", len(buffer))
	}
	if current, err := value.ReadCommonHeader(buffer); err == nil && current.Kind == object.Common.Kind {
		object.Common.Mark = current.Mark
	}
	if err := value.WriteCommonHeader(buffer, object.Common); err != nil {
		return err
	}
	binary.LittleEndian.PutUint64(buffer[ProtoOffset:ProtoOffset+8], uint64(object.Proto.Bits()))
	binary.LittleEndian.PutUint64(buffer[EnvOffset:EnvOffset+8], uint64(object.Env.Bits()))
	binary.LittleEndian.PutUint16(buffer[UpvalueCountOffset:UpvalueCountOffset+2], object.UpvalueCount)
	binary.LittleEndian.PutUint16(buffer[FlagsOffset:FlagsOffset+2], object.Flags)
	binary.LittleEndian.PutUint32(buffer[Reserved0Offset:Reserved0Offset+4], 0)
	binary.LittleEndian.PutUint64(buffer[UpvaluesDataOffset:UpvaluesDataOffset+8], uint64(object.UpvaluesData))
	binary.LittleEndian.PutUint64(buffer[FeedbackVectorOff:FeedbackVectorOff+8], uint64(object.FeedbackData))
	binary.LittleEndian.PutUint32(buffer[FeedbackSlotsOff:FeedbackSlotsOff+4], object.FeedbackSize)
	binary.LittleEndian.PutUint32(buffer[Reserved2Offset:Reserved2Offset+4], 0)
	return nil
}

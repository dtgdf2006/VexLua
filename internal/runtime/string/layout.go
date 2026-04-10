package rtstring

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/runtime/value"
)

const (
	HeaderSize   = 0x20
	HashOffset   = 0x10
	LengthOffset = 0x14
	DataOffset   = 0x20
)

type Header struct {
	Common value.CommonHeader
	Hash   uint32
	Length uint32
}

func LayoutSize(length int) (uint32, error) {
	if length < 0 {
		return 0, fmt.Errorf("string length cannot be negative")
	}
	size := uint64(DataOffset) + uint64(length) + 1
	if size > ^uint64(0)>>1 {
		return 0, fmt.Errorf("string length %d is too large", length)
	}
	aligned := alignUp(size, value.ObjectAlignment)
	if aligned > uint64(^uint32(0)) {
		return 0, fmt.Errorf("string object size %#x exceeds u32", aligned)
	}
	return uint32(aligned), nil
}

func NewHeader(length int, hash uint32) (Header, error) {
	size, err := LayoutSize(length)
	if err != nil {
		return Header{}, err
	}
	return Header{
		Common: value.CommonHeader{
			Kind:      value.KindString,
			SizeBytes: size,
			Version:   1,
		},
		Hash:   hash,
		Length: uint32(length),
	}, nil
}

func ReadHeader(buffer []byte) (Header, error) {
	if len(buffer) < HeaderSize {
		return Header{}, fmt.Errorf("buffer too small for string header: %d", len(buffer))
	}
	common, err := value.ReadCommonHeader(buffer)
	if err != nil {
		return Header{}, err
	}
	if common.Kind != value.KindString {
		return Header{}, fmt.Errorf("expected %s object, got %s", value.KindString, common.Kind)
	}
	if common.SizeBytes < HeaderSize {
		return Header{}, fmt.Errorf("string object too small: %#x", common.SizeBytes)
	}
	return Header{
		Common: common,
		Hash:   binary.LittleEndian.Uint32(buffer[HashOffset : HashOffset+4]),
		Length: binary.LittleEndian.Uint32(buffer[LengthOffset : LengthOffset+4]),
	}, nil
}

func WriteObject(buffer []byte, header Header, text string) error {
	if err := header.Common.Validate(); err != nil {
		return err
	}
	if header.Common.Kind != value.KindString {
		return fmt.Errorf("invalid string object kind: %s", header.Common.Kind)
	}
	if len(text) != int(header.Length) {
		return fmt.Errorf("string length mismatch: header=%d text=%d", header.Length, len(text))
	}
	if len(buffer) < int(header.Common.SizeBytes) {
		return fmt.Errorf("buffer too small for string object: need %d, got %d", header.Common.SizeBytes, len(buffer))
	}
	if err := value.WriteCommonHeader(buffer, header.Common); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(buffer[HashOffset:HashOffset+4], header.Hash)
	binary.LittleEndian.PutUint32(buffer[LengthOffset:LengthOffset+4], header.Length)
	copy(buffer[DataOffset:DataOffset+len(text)], text)
	buffer[DataOffset+len(text)] = 0
	for index := DataOffset + len(text) + 1; index < int(header.Common.SizeBytes); index++ {
		buffer[index] = 0
	}
	return nil
}

func Decode(buffer []byte) (Header, string, error) {
	header, err := ReadHeader(buffer)
	if err != nil {
		return Header{}, "", err
	}
	end := DataOffset + int(header.Length)
	if len(buffer) < end+1 {
		return Header{}, "", fmt.Errorf("buffer too small for string payload: need %d, got %d", end+1, len(buffer))
	}
	if buffer[end] != 0 {
		return Header{}, "", fmt.Errorf("string object is missing trailing terminator")
	}
	return header, string(buffer[DataOffset:end]), nil
}

func alignUp(valueToAlign uint64, alignment uint64) uint64 {
	if alignment == 0 {
		return valueToAlign
	}
	mask := alignment - 1
	return (valueToAlign + mask) &^ mask
}

package value

import (
	"encoding/binary"
	"fmt"
)

type MarkBits uint8

type HeaderFlags uint8

const (
	MarkWhite0 MarkBits = 1 << iota
	MarkWhite1
	MarkGray
	MarkBlack
	MarkFinalizable
	MarkRemembered
	MarkPinned
	MarkWeakContainer
)

const (
	HeaderFlagImmutable HeaderFlags = 1 << iota
	HeaderFlagExternalPayload
	HeaderFlagHostManaged
	HeaderFlagHasEmbeddedRefs
	HeaderFlagReserved4
	HeaderFlagReserved5
	HeaderFlagReserved6
	HeaderFlagReserved7
)

type CommonHeader struct {
	Kind      ObjectKind
	Mark      MarkBits
	Flags     HeaderFlags
	SizeBytes uint32
	Version   uint32
	Aux       uint32
}

func NewCommonHeader(kind ObjectKind, sizeBytes uint32) CommonHeader {
	return CommonHeader{Kind: kind, SizeBytes: sizeBytes}
}

func (header CommonHeader) Validate() error {
	if header.Kind == 0 {
		return fmt.Errorf("object kind cannot be zero")
	}
	if header.SizeBytes < CommonHeaderSize {
		return fmt.Errorf("object size %#x is smaller than common header size %#x", header.SizeBytes, CommonHeaderSize)
	}
	if header.SizeBytes%ObjectAlignment != 0 {
		return fmt.Errorf("object size %#x is not %d-byte aligned", header.SizeBytes, ObjectAlignment)
	}
	return nil
}

func (flags HeaderFlags) Has(mask HeaderFlags) bool {
	return flags&mask == mask
}

func (flags HeaderFlags) With(mask HeaderFlags) HeaderFlags {
	return flags | mask
}

func (flags HeaderFlags) Without(mask HeaderFlags) HeaderFlags {
	return flags &^ mask
}

func (mark MarkBits) Has(mask MarkBits) bool {
	return mark&mask == mask
}

func (mark MarkBits) With(mask MarkBits) MarkBits {
	return mark | mask
}

func (mark MarkBits) Without(mask MarkBits) MarkBits {
	return mark &^ mask
}

func ReadCommonHeader(buffer []byte) (CommonHeader, error) {
	if len(buffer) < CommonHeaderSize {
		return CommonHeader{}, fmt.Errorf("buffer too small for common header: %d", len(buffer))
	}
	return CommonHeader{
		Kind:      ObjectKind(binary.LittleEndian.Uint16(buffer[CommonHeaderKindOffset : CommonHeaderKindOffset+2])),
		Mark:      MarkBits(buffer[CommonHeaderMarkOffset]),
		Flags:     HeaderFlags(buffer[CommonHeaderFlagsOffset]),
		SizeBytes: binary.LittleEndian.Uint32(buffer[CommonHeaderSizeBytesOffset : CommonHeaderSizeBytesOffset+4]),
		Version:   binary.LittleEndian.Uint32(buffer[CommonHeaderVersionOffset : CommonHeaderVersionOffset+4]),
		Aux:       binary.LittleEndian.Uint32(buffer[CommonHeaderAuxOffset : CommonHeaderAuxOffset+4]),
	}, nil
}

func MustReadCommonHeader(buffer []byte) CommonHeader {
	header, err := ReadCommonHeader(buffer)
	if err != nil {
		panic(err)
	}
	return header
}

func WriteCommonHeader(buffer []byte, header CommonHeader) error {
	if len(buffer) < CommonHeaderSize {
		return fmt.Errorf("buffer too small for common header: %d", len(buffer))
	}
	if err := header.Validate(); err != nil {
		return err
	}
	binary.LittleEndian.PutUint16(buffer[CommonHeaderKindOffset:CommonHeaderKindOffset+2], uint16(header.Kind))
	buffer[CommonHeaderMarkOffset] = byte(header.Mark)
	buffer[CommonHeaderFlagsOffset] = byte(header.Flags)
	binary.LittleEndian.PutUint32(buffer[CommonHeaderSizeBytesOffset:CommonHeaderSizeBytesOffset+4], header.SizeBytes)
	binary.LittleEndian.PutUint32(buffer[CommonHeaderVersionOffset:CommonHeaderVersionOffset+4], header.Version)
	binary.LittleEndian.PutUint32(buffer[CommonHeaderAuxOffset:CommonHeaderAuxOffset+4], header.Aux)
	return nil
}

func ReadObjectKind(buffer []byte) (ObjectKind, error) {
	if len(buffer) < CommonHeaderKindOffset+2 {
		return 0, fmt.Errorf("buffer too small for object kind: %d", len(buffer))
	}
	return ObjectKind(binary.LittleEndian.Uint16(buffer[CommonHeaderKindOffset : CommonHeaderKindOffset+2])), nil
}

func ReadMarkBits(buffer []byte) (MarkBits, error) {
	if len(buffer) < CommonHeaderMarkOffset+1 {
		return 0, fmt.Errorf("buffer too small for mark bits: %d", len(buffer))
	}
	return MarkBits(buffer[CommonHeaderMarkOffset]), nil
}

func ReadHeaderFlags(buffer []byte) (HeaderFlags, error) {
	if len(buffer) < CommonHeaderFlagsOffset+1 {
		return 0, fmt.Errorf("buffer too small for flags: %d", len(buffer))
	}
	return HeaderFlags(buffer[CommonHeaderFlagsOffset]), nil
}

func ReadSizeBytes(buffer []byte) (uint32, error) {
	if len(buffer) < CommonHeaderSizeBytesOffset+4 {
		return 0, fmt.Errorf("buffer too small for size bytes: %d", len(buffer))
	}
	return binary.LittleEndian.Uint32(buffer[CommonHeaderSizeBytesOffset : CommonHeaderSizeBytesOffset+4]), nil
}

func ReadVersion(buffer []byte) (uint32, error) {
	if len(buffer) < CommonHeaderVersionOffset+4 {
		return 0, fmt.Errorf("buffer too small for version: %d", len(buffer))
	}
	return binary.LittleEndian.Uint32(buffer[CommonHeaderVersionOffset : CommonHeaderVersionOffset+4]), nil
}

func ReadAux(buffer []byte) (uint32, error) {
	if len(buffer) < CommonHeaderAuxOffset+4 {
		return 0, fmt.Errorf("buffer too small for aux: %d", len(buffer))
	}
	return binary.LittleEndian.Uint32(buffer[CommonHeaderAuxOffset : CommonHeaderAuxOffset+4]), nil
}

func WriteObjectKind(buffer []byte, kind ObjectKind) error {
	if len(buffer) < CommonHeaderKindOffset+2 {
		return fmt.Errorf("buffer too small for object kind: %d", len(buffer))
	}
	binary.LittleEndian.PutUint16(buffer[CommonHeaderKindOffset:CommonHeaderKindOffset+2], uint16(kind))
	return nil
}

func WriteMarkBits(buffer []byte, mark MarkBits) error {
	if len(buffer) < CommonHeaderMarkOffset+1 {
		return fmt.Errorf("buffer too small for mark bits: %d", len(buffer))
	}
	buffer[CommonHeaderMarkOffset] = byte(mark)
	return nil
}

func WriteHeaderFlags(buffer []byte, flags HeaderFlags) error {
	if len(buffer) < CommonHeaderFlagsOffset+1 {
		return fmt.Errorf("buffer too small for flags: %d", len(buffer))
	}
	buffer[CommonHeaderFlagsOffset] = byte(flags)
	return nil
}

func WriteSizeBytes(buffer []byte, sizeBytes uint32) error {
	if len(buffer) < CommonHeaderSizeBytesOffset+4 {
		return fmt.Errorf("buffer too small for size bytes: %d", len(buffer))
	}
	binary.LittleEndian.PutUint32(buffer[CommonHeaderSizeBytesOffset:CommonHeaderSizeBytesOffset+4], sizeBytes)
	return nil
}

func WriteVersion(buffer []byte, version uint32) error {
	if len(buffer) < CommonHeaderVersionOffset+4 {
		return fmt.Errorf("buffer too small for version: %d", len(buffer))
	}
	binary.LittleEndian.PutUint32(buffer[CommonHeaderVersionOffset:CommonHeaderVersionOffset+4], version)
	return nil
}

func WriteAux(buffer []byte, aux uint32) error {
	if len(buffer) < CommonHeaderAuxOffset+4 {
		return fmt.Errorf("buffer too small for aux: %d", len(buffer))
	}
	binary.LittleEndian.PutUint32(buffer[CommonHeaderAuxOffset:CommonHeaderAuxOffset+4], aux)
	return nil
}

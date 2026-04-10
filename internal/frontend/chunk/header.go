package chunk

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	Signature         = "\x1bLua"
	LUACVersion  byte = 0x51
	LUACFormat   byte = 0
	HeaderSize        = 12
	LittleEndian byte = 1
	BigEndian    byte = 0
)

type BinaryFormat struct {
	Version         byte
	FormatID        byte
	Endianness      byte
	IntSize         byte
	SizeTSize       byte
	InstructionSize byte
	NumberSize      byte
	IntegralNumbers byte
	ByteOrder       binary.ByteOrder
}

func DefaultFormat() BinaryFormat {
	return BinaryFormat{
		Version:         LUACVersion,
		FormatID:        LUACFormat,
		Endianness:      LittleEndian,
		IntSize:         4,
		SizeTSize:       8,
		InstructionSize: 4,
		NumberSize:      8,
		IntegralNumbers: 0,
		ByteOrder:       binary.LittleEndian,
	}
}

func (format BinaryFormat) HeaderBytes() [HeaderSize]byte {
	var header [HeaderSize]byte
	copy(header[:4], []byte(Signature))
	header[4] = format.Version
	header[5] = format.FormatID
	header[6] = format.Endianness
	header[7] = format.IntSize
	header[8] = format.SizeTSize
	header[9] = format.InstructionSize
	header[10] = format.NumberSize
	header[11] = format.IntegralNumbers
	return header
}

func ExpectedHeaderBytes() [HeaderSize]byte {
	return DefaultFormat().HeaderBytes()
}

func ValidateHeaderBytes(header []byte) error {
	if len(header) != HeaderSize {
		return fmt.Errorf("bad header size: got %d want %d", len(header), HeaderSize)
	}
	expected := ExpectedHeaderBytes()
	if !bytes.Equal(header, expected[:]) {
		return fmt.Errorf("bad header")
	}
	return nil
}

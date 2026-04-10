package metadata

import (
	"encoding/binary"
	"fmt"
	"math"
)

const UnmappedOffset = math.MaxUint32

type OffsetTableBuilder struct {
	previous uint32
	bytes    []byte
}

func (builder *OffsetTableBuilder) AddPosition(pcOffset uint32) error {
	if pcOffset < builder.previous {
		return fmt.Errorf("pc offset moved backwards: prev=%d next=%d", builder.previous, pcOffset)
	}
	delta := pcOffset - builder.previous
	builder.bytes = binary.AppendUvarint(builder.bytes, uint64(delta))
	builder.previous = pcOffset
	return nil
}

func (builder *OffsetTableBuilder) Bytes() []byte {
	return append([]byte(nil), builder.bytes...)
}

type CodeMetadata struct {
	BytecodeToCode []uint32
	OffsetTable    []byte
}

func NewCodeMetadata(instructionCount int) CodeMetadata {
	positions := make([]uint32, instructionCount+1)
	for index := range positions {
		positions[index] = UnmappedOffset
	}
	return CodeMetadata{BytecodeToCode: positions}
}

func (metadata *CodeMetadata) RecordBytecodeOffset(bytecodeOffset int, codeOffset uint32) error {
	if bytecodeOffset < 0 || bytecodeOffset >= len(metadata.BytecodeToCode) {
		return fmt.Errorf("bytecode offset out of range: %d", bytecodeOffset)
	}
	metadata.BytecodeToCode[bytecodeOffset] = codeOffset
	return nil
}

func (metadata *CodeMetadata) Finalize(builder *OffsetTableBuilder) {
	if builder == nil {
		metadata.OffsetTable = nil
		return
	}
	metadata.OffsetTable = builder.Bytes()
}

func (metadata CodeMetadata) CodeOffset(bytecodeOffset int) (uint32, bool) {
	if bytecodeOffset < 0 || bytecodeOffset >= len(metadata.BytecodeToCode) {
		return 0, false
	}
	offset := metadata.BytecodeToCode[bytecodeOffset]
	if offset == UnmappedOffset {
		return 0, false
	}
	return offset, true
}

type OffsetIterator struct {
	data         []byte
	index        int
	current      uint32
	bytecodeSlot int
	done         bool
}

func NewOffsetIterator(table []byte) *OffsetIterator {
	return &OffsetIterator{data: table}
}

func (iterator *OffsetIterator) Done() bool {
	return iterator.done || iterator.index >= len(iterator.data)
}

func (iterator *OffsetIterator) CurrentCodeOffset() uint32 {
	return iterator.current
}

func (iterator *OffsetIterator) CurrentBytecodeOffset() int {
	return iterator.bytecodeSlot
}

func (iterator *OffsetIterator) Advance() bool {
	if iterator.Done() {
		iterator.done = true
		return false
	}
	delta, width := binary.Uvarint(iterator.data[iterator.index:])
	if width <= 0 {
		iterator.done = true
		return false
	}
	iterator.current += uint32(delta)
	iterator.index += width
	iterator.bytecodeSlot++
	return true
}

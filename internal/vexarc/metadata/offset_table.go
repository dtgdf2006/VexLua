package metadata

import (
	"encoding/binary"
	"fmt"
	"math"
)

const UnmappedOffset = math.MaxUint32

type ContinuationKind uint32

const (
	ContinuationInvalid ContinuationKind = iota
	ContinuationGetGlobal
	ContinuationGetTable
	ContinuationSetGlobal
	ContinuationSetTable
	ContinuationGetUpvalue
	ContinuationSetUpvalue
	ContinuationCall
	ContinuationTailCall
	ContinuationForPrep
	ContinuationForLoop
	ContinuationSelf
	ContinuationArithmetic
	ContinuationUnaryTest
	ContinuationLength
	ContinuationCompare
	ContinuationDeopt
)

const (
	ContinuationFlagAlternateResume uint32 = 1 << iota
	ContinuationFlagFinalExit
	ContinuationFlagNativeBuiltinABI
	ContinuationFlagDeoptOnUncovered
)

type ContinuationSite struct {
	Kind             ContinuationKind
	Flags            uint32
	StubID           uint32
	BytecodePC       uint32
	DeoptPC          uint32
	ResumePC         uint32
	ResumeCodeOffset uint32
	AltResumePC      uint32
	AltResumeCodeOff uint32
	Operand0         uint32
	Operand1         uint32
	Operand2         uint32
	Operand3         uint32
	LiveSlots        uint32
}

func (site ContinuationSite) HasAlternateResume() bool {
	return site.Flags&ContinuationFlagAlternateResume != 0
}

func (site ContinuationSite) IsFinalExit() bool {
	return site.Flags&ContinuationFlagFinalExit != 0
}

func (site ContinuationSite) UsesNativeBuiltinABI() bool {
	return site.Flags&ContinuationFlagNativeBuiltinABI != 0
}

func (site ContinuationSite) DeoptsOnUncovered() bool {
	return site.Flags&ContinuationFlagDeoptOnUncovered != 0
}

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
	Sites          []ContinuationSite
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

func (metadata *CodeMetadata) AddContinuationSite(site ContinuationSite) uint32 {
	site.ResumeCodeOffset = UnmappedOffset
	site.AltResumeCodeOff = UnmappedOffset
	metadata.Sites = append(metadata.Sites, site)
	return uint32(len(metadata.Sites) - 1)
}

func (metadata *CodeMetadata) Finalize(builder *OffsetTableBuilder) {
	if builder == nil {
		metadata.OffsetTable = nil
	} else {
		metadata.OffsetTable = builder.Bytes()
	}
	for index := range metadata.Sites {
		site := &metadata.Sites[index]
		site.ResumeCodeOffset = metadata.lookupCodeOffset(site.ResumePC)
		site.AltResumeCodeOff = metadata.lookupCodeOffset(site.AltResumePC)
	}
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

func (metadata CodeMetadata) ContinuationSite(siteID uint32) (ContinuationSite, bool) {
	if int(siteID) < 0 || int(siteID) >= len(metadata.Sites) {
		return ContinuationSite{}, false
	}
	return metadata.Sites[siteID], true
}

func (metadata CodeMetadata) lookupCodeOffset(bytecodePC uint32) uint32 {
	if bytecodePC == UnmappedOffset {
		return UnmappedOffset
	}
	offset, ok := metadata.CodeOffset(int(bytecodePC))
	if !ok {
		return UnmappedOffset
	}
	return offset
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

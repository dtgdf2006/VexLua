package metadata

import (
	"encoding/binary"
	"fmt"
	"math"
	mathbits "math/bits"

	"vexlua/internal/runtime/value"
)

const UnmappedOffset = math.MaxUint32
const NoLiveSlotSet = math.MaxUint32

type LiveSlotSet struct {
	RegisterWords   []uint64
	DynamicTopStart uint32
}

func NewLiveSlotSet(registerCount int) LiveSlotSet {
	if registerCount < 0 {
		registerCount = 0
	}
	wordCount := 0
	if registerCount > 0 {
		wordCount = (registerCount + 63) / 64
	}
	return LiveSlotSet{
		RegisterWords:   make([]uint64, wordCount),
		DynamicTopStart: NoLiveSlotSet,
	}
}

func (set *LiveSlotSet) AddRegister(index int) {
	if index < 0 {
		return
	}
	word := index / 64
	bit := uint(index % 64)
	if word >= len(set.RegisterWords) {
		grown := make([]uint64, word+1)
		copy(grown, set.RegisterWords)
		set.RegisterWords = grown
	}
	set.RegisterWords[word] |= uint64(1) << bit
}

func (set *LiveSlotSet) AddRange(start int, end int) {
	if start < 0 || end < start {
		return
	}
	for index := start; index <= end; index++ {
		set.AddRegister(index)
	}
}

func (set *LiveSlotSet) SetDynamicTopStart(start int) {
	if start < 0 {
		return
	}
	if set.DynamicTopStart == NoLiveSlotSet || uint32(start) < set.DynamicTopStart {
		set.DynamicTopStart = uint32(start)
	}
}

func (set LiveSlotSet) Clone() LiveSlotSet {
	cloned := LiveSlotSet{DynamicTopStart: set.DynamicTopStart}
	if len(set.RegisterWords) != 0 {
		cloned.RegisterWords = append([]uint64(nil), set.RegisterWords...)
	}
	return cloned
}

func (set LiveSlotSet) HasStaticRegister(index uint32) bool {
	word := int(index / 64)
	if word >= len(set.RegisterWords) {
		return false
	}
	bit := uint(index % 64)
	return set.RegisterWords[word]&(uint64(1)<<bit) != 0
}

func (set LiveSlotSet) HasDynamicRange() bool {
	return set.DynamicTopStart != NoLiveSlotSet
}

func (set LiveSlotSet) WalkRegisters(frameTop uint32, visit func(uint32) error) error {
	for wordIndex, word := range set.RegisterWords {
		remaining := word
		for remaining != 0 {
			bit := remaining & -remaining
			slot := uint32(wordIndex*64 + mathbits.TrailingZeros64(remaining))
			if err := visit(slot); err != nil {
				return err
			}
			remaining &^= bit
		}
	}
	if set.DynamicTopStart == NoLiveSlotSet || frameTop <= set.DynamicTopStart {
		return nil
	}
	for slot := set.DynamicTopStart; slot < frameTop; slot++ {
		if set.HasStaticRegister(slot) {
			continue
		}
		if err := visit(slot); err != nil {
			return err
		}
	}
	return nil
}

func (set LiveSlotSet) UpperBound(registerCount int) uint32 {
	if registerCount < 0 {
		registerCount = 0
	}
	if set.DynamicTopStart != NoLiveSlotSet {
		return uint32(registerCount)
	}
	upper := uint32(0)
	for wordIndex, word := range set.RegisterWords {
		if word == 0 {
			continue
		}
		candidate := uint32(wordIndex*64 + mathbits.Len64(word))
		if candidate > upper {
			upper = candidate
		}
	}
	return upper
}

func (set LiveSlotSet) Equivalent(other LiveSlotSet) bool {
	if set.DynamicTopStart != other.DynamicTopStart {
		return false
	}
	if len(set.RegisterWords) != len(other.RegisterWords) {
		return false
	}
	for index, word := range set.RegisterWords {
		if word != other.RegisterWords[index] {
			return false
		}
	}
	return true
}

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
	ContinuationSetList
	ContinuationCompare
	ContinuationNewTable
	ContinuationConcat
	ContinuationClose
	ContinuationClosure
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
	LiveSetID        uint32
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
	BytecodeLive   []uint32
	OffsetTable    []byte
	Sites          []ContinuationSite
	LiveSlotSets   []LiveSlotSet
	HeapRefs       []value.HeapRef44
}

func NewCodeMetadata(instructionCount int) CodeMetadata {
	positions := make([]uint32, instructionCount+1)
	live := make([]uint32, instructionCount+1)
	for index := range positions {
		positions[index] = UnmappedOffset
		live[index] = NoLiveSlotSet
	}
	return CodeMetadata{BytecodeToCode: positions, BytecodeLive: live}
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
	if site.LiveSetID == 0 && len(metadata.LiveSlotSets) == 0 {
		site.LiveSetID = NoLiveSlotSet
	}
	metadata.Sites = append(metadata.Sites, site)
	return uint32(len(metadata.Sites) - 1)
}

func (metadata *CodeMetadata) AddLiveSlotSet(set LiveSlotSet) uint32 {
	normalized := set.Clone()
	for index, existing := range metadata.LiveSlotSets {
		if existing.Equivalent(normalized) {
			return uint32(index)
		}
	}
	metadata.LiveSlotSets = append(metadata.LiveSlotSets, normalized)
	return uint32(len(metadata.LiveSlotSets) - 1)
}

func (metadata *CodeMetadata) RecordBytecodeLiveSlots(bytecodeOffset int, set LiveSlotSet) error {
	if bytecodeOffset < 0 || bytecodeOffset >= len(metadata.BytecodeLive) {
		return fmt.Errorf("bytecode live slot offset out of range: %d", bytecodeOffset)
	}
	metadata.BytecodeLive[bytecodeOffset] = metadata.AddLiveSlotSet(set)
	return nil
}

func (metadata *CodeMetadata) AddHeapRef(ref value.HeapRef44) {
	if ref == 0 {
		return
	}
	metadata.HeapRefs = append(metadata.HeapRefs, ref)
}

func (metadata *CodeMetadata) Finalize(builder *OffsetTableBuilder) {
	metadata.OffsetTable = builder.Bytes()
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
	if int(siteID) >= len(metadata.Sites) {
		return ContinuationSite{}, false
	}
	return metadata.Sites[siteID], true
}

func (metadata CodeMetadata) LiveSlotSetID(bytecodeOffset int) (uint32, bool) {
	if bytecodeOffset < 0 || bytecodeOffset >= len(metadata.BytecodeLive) {
		return 0, false
	}
	id := metadata.BytecodeLive[bytecodeOffset]
	if id == NoLiveSlotSet {
		return 0, false
	}
	return id, true
}

func (metadata CodeMetadata) LiveSlotSetAtBytecode(bytecodeOffset int) (LiveSlotSet, bool) {
	id, ok := metadata.LiveSlotSetID(bytecodeOffset)
	if !ok {
		return LiveSlotSet{}, false
	}
	if int(id) >= len(metadata.LiveSlotSets) {
		return LiveSlotSet{}, false
	}
	return metadata.LiveSlotSets[id].Clone(), true
}

func (metadata CodeMetadata) WalkHeapRefs(visit func(value.HeapRef44) error) error {
	for _, ref := range metadata.HeapRefs {
		if err := visit(ref); err != nil {
			return err
		}
	}
	return nil
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

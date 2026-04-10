package jit

import (
	"fmt"
	"sort"

	"vexlua/internal/bytecode"
)

type PCOffset struct {
	PC         int
	CodeOffset uint32
}

type HelperKind uint8

const (
	HelperGetField HelperKind = iota + 1
	HelperSelf
	HelperGetTable
	HelperSetTable
	HelperLen
	HelperAdd
	HelperGetFieldIC
	HelperSelfIC
	HelperGetTableArray
	HelperSetTableArray
	HelperLenTable
	HelperEqual
	HelperLess
	HelperLessEqual
	HelperNot
	HelperUnm
	HelperConcat
	HelperSub
	HelperMul
	HelperDiv
	HelperMod
	HelperPow
	HelperCallHostFunction
	HelperCall
	HelperCallLuaClosure
	HelperCallMulti
	HelperTailCall
	HelperAppendTable
	HelperReturnAppendPending
	HelperLoadGlobal
	HelperStoreGlobal
	HelperSetField
	HelperNewTable
	HelperClosure
	HelperLoadUpvalue
	HelperStoreUpvalue
	HelperVararg
	HelperYield
	HelperIterPairs
	HelperIterIPairs
	HelperClose
	HelperReturnMulti
)

type HelperCallDescriptor struct {
	ID              uint32
	PC              int
	ResumePC        int
	CodeOffset      uint32
	Kind            HelperKind
	InlineCacheSlot int
	CallCacheSlot   int
}

type SideExitDescriptor struct {
	ID         uint32
	PC         int
	ResumePC   int
	CodeOffset uint32
	Reason     ExitReason
}

type CompiledUnitMeta struct {
	UnitID           uint32
	Proto            *bytecode.Proto
	Mode             CompileMode
	Region           Region
	EntryOffset      uint32
	CodeSize         uint32
	PCOffsets        []PCOffset
	HelperCalls      []HelperCallDescriptor
	SideExits        []SideExitDescriptor
	LiveSlotMaps     [][]uint64
	SpillSlots       int
	InlineCacheSlots []int
	CallCacheSlots   []int
}

func (kind HelperKind) String() string {
	switch kind {
	case HelperGetField:
		return "get-field"
	case HelperSelf:
		return "self"
	case HelperGetTable:
		return "get-table"
	case HelperSetTable:
		return "set-table"
	case HelperLen:
		return "len"
	case HelperAdd:
		return "add"
	case HelperGetFieldIC:
		return "get-field-ic"
	case HelperSelfIC:
		return "self-ic"
	case HelperGetTableArray:
		return "get-table-array"
	case HelperSetTableArray:
		return "set-table-array"
	case HelperLenTable:
		return "len-table"
	case HelperEqual:
		return "equal"
	case HelperLess:
		return "less"
	case HelperLessEqual:
		return "less-equal"
	case HelperNot:
		return "not"
	case HelperUnm:
		return "unm"
	case HelperConcat:
		return "concat"
	case HelperSub:
		return "sub"
	case HelperMul:
		return "mul"
	case HelperDiv:
		return "div"
	case HelperMod:
		return "mod"
	case HelperPow:
		return "pow"
	case HelperCallHostFunction:
		return "call-host-function"
	case HelperCall:
		return "call"
	case HelperCallLuaClosure:
		return "call-lua-closure"
	case HelperCallMulti:
		return "call-multi"
	case HelperTailCall:
		return "tail-call"
	case HelperAppendTable:
		return "append-table"
	case HelperReturnAppendPending:
		return "return-append-pending"
	case HelperLoadGlobal:
		return "load-global"
	case HelperStoreGlobal:
		return "store-global"
	case HelperSetField:
		return "set-field"
	case HelperNewTable:
		return "new-table"
	case HelperClosure:
		return "closure"
	case HelperLoadUpvalue:
		return "load-upvalue"
	case HelperStoreUpvalue:
		return "store-upvalue"
	case HelperVararg:
		return "vararg"
	case HelperYield:
		return "yield"
	case HelperIterPairs:
		return "iter-pairs"
	case HelperIterIPairs:
		return "iter-ipairs"
	case HelperClose:
		return "close"
	case HelperReturnMulti:
		return "return-multi"
	default:
		return fmt.Sprintf("helper-kind(%d)", kind)
	}
}

func NewWholeProtoMeta(proto *bytecode.Proto) *CompiledUnitMeta {
	return &CompiledUnitMeta{
		Proto:  proto,
		Mode:   CompileWholeProto,
		Region: Region{StartPC: 0, EndPC: len(proto.Code)},
	}
}

func NewRegionMeta(proto *bytecode.Proto, region Region) *CompiledUnitMeta {
	return &CompiledUnitMeta{
		Proto:  proto,
		Mode:   CompileRegion,
		Region: region,
	}
}

func (meta *CompiledUnitMeta) Range() (int, int) {
	return meta.Region.StartPC, meta.Region.EndPC
}

func (meta *CompiledUnitMeta) ContainsPC(pc int) bool {
	start, end := meta.Range()
	return pc >= start && pc < end
}

func (meta *CompiledUnitMeta) Finalize(entryOffset uint32, codeSize int) error {
	if codeSize <= 0 {
		return fmt.Errorf("compiled unit %q has invalid code size %d", meta.Proto.Name, codeSize)
	}
	if entryOffset >= uint32(codeSize) {
		return fmt.Errorf("compiled unit %q entry offset %d is outside code size %d", meta.Proto.Name, entryOffset, codeSize)
	}
	meta.EntryOffset = entryOffset
	meta.CodeSize = uint32(codeSize)
	return nil
}

func (meta *CompiledUnitMeta) AddSideExit(pc int, reason ExitReason) error {
	return meta.AddSideExitAt(pc, pc, reason, 0)
}

func (meta *CompiledUnitMeta) AddSideExitAt(pc int, resumePC int, reason ExitReason, codeOffset uint32) error {
	if !meta.ContainsPC(pc) {
		return fmt.Errorf("pc %d is outside compiled region [%d,%d)", pc, meta.Region.StartPC, meta.Region.EndPC)
	}
	meta.SideExits = append(meta.SideExits, SideExitDescriptor{
		ID:         uint32(len(meta.SideExits) + 1),
		PC:         pc,
		ResumePC:   resumePC,
		CodeOffset: codeOffset,
		Reason:     reason,
	})
	return nil
}

func (meta *CompiledUnitMeta) AddHelperCall(pc int, resumePC int, kind HelperKind, codeOffset uint32) (uint32, error) {
	return meta.AddHelperCallWithSlots(pc, resumePC, kind, codeOffset, -1, -1)
}

func (meta *CompiledUnitMeta) AddHelperCallWithInlineCache(pc int, resumePC int, kind HelperKind, codeOffset uint32, inlineCacheSlot int) (uint32, error) {
	return meta.AddHelperCallWithSlots(pc, resumePC, kind, codeOffset, inlineCacheSlot, -1)
}

func (meta *CompiledUnitMeta) AddHelperCallWithCallCache(pc int, resumePC int, kind HelperKind, codeOffset uint32, callCacheSlot int) (uint32, error) {
	return meta.AddHelperCallWithSlots(pc, resumePC, kind, codeOffset, -1, callCacheSlot)
}

func (meta *CompiledUnitMeta) AddHelperCallWithSlots(pc int, resumePC int, kind HelperKind, codeOffset uint32, inlineCacheSlot int, callCacheSlot int) (uint32, error) {
	if !meta.ContainsPC(pc) {
		return 0, fmt.Errorf("pc %d is outside compiled region [%d,%d)", pc, meta.Region.StartPC, meta.Region.EndPC)
	}
	id := uint32(len(meta.HelperCalls) + 1)
	meta.HelperCalls = append(meta.HelperCalls, HelperCallDescriptor{
		ID:              id,
		PC:              pc,
		ResumePC:        resumePC,
		CodeOffset:      codeOffset,
		Kind:            kind,
		InlineCacheSlot: inlineCacheSlot,
		CallCacheSlot:   callCacheSlot,
	})
	return id, nil
}

func (meta *CompiledUnitMeta) HelperCallForID(id uint32) (HelperCallDescriptor, bool) {
	if id == 0 {
		return HelperCallDescriptor{}, false
	}
	index := int(id - 1)
	if index >= 0 && index < len(meta.HelperCalls) {
		desc := meta.HelperCalls[index]
		if desc.ID == id {
			return desc, true
		}
	}
	for _, item := range meta.HelperCalls {
		if item.ID == id {
			return item, true
		}
	}
	return HelperCallDescriptor{}, false
}

func (meta *CompiledUnitMeta) CodeOffsetForPC(pc int) (uint32, bool) {
	index := pc - meta.Region.StartPC
	if index >= 0 && index < len(meta.PCOffsets) {
		item := meta.PCOffsets[index]
		if item.PC == pc {
			return item.CodeOffset, true
		}
	}
	for _, item := range meta.PCOffsets {
		if item.PC == pc {
			return item.CodeOffset, true
		}
	}
	return 0, false
}

type BytecodeOffsetTableBuilder struct {
	meta  *CompiledUnitMeta
	byPC  map[int]uint32
	order []int
}

func NewBytecodeOffsetTableBuilder(meta *CompiledUnitMeta) *BytecodeOffsetTableBuilder {
	return &BytecodeOffsetTableBuilder{
		meta: meta,
		byPC: make(map[int]uint32),
	}
}

func (b *BytecodeOffsetTableBuilder) Add(pc int, codeOffset uint32) error {
	if b.meta == nil {
		return fmt.Errorf("bytecode offset builder has no metadata")
	}
	if !b.meta.ContainsPC(pc) {
		return fmt.Errorf("pc %d is outside compiled region [%d,%d)", pc, b.meta.Region.StartPC, b.meta.Region.EndPC)
	}
	if _, exists := b.byPC[pc]; !exists {
		b.order = append(b.order, pc)
	}
	b.byPC[pc] = codeOffset
	return nil
}

func (b *BytecodeOffsetTableBuilder) FillInterpretFallback() {
	if b.meta == nil {
		return
	}
	for pc := b.meta.Region.StartPC; pc < b.meta.Region.EndPC; pc++ {
		_ = b.Add(pc, 0)
	}
	if len(b.order) == 0 {
		_ = b.Add(b.meta.Region.StartPC, 0)
	}
}

func (b *BytecodeOffsetTableBuilder) Finish() {
	if b.meta == nil {
		return
	}
	sort.Ints(b.order)
	b.meta.PCOffsets = b.meta.PCOffsets[:0]
	for _, pc := range b.order {
		b.meta.PCOffsets = append(b.meta.PCOffsets, PCOffset{PC: pc, CodeOffset: b.byPC[pc]})
	}
}

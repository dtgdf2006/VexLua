package feedback

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/runtime/value"
)

type State uint8

type AccessKind uint8

const (
	HeaderSize             = 0x20
	HeaderSlotCountOffset  = 0x00
	HeaderBudgetOffset     = 0x04
	HeaderHotnessOffset    = 0x08
	HeaderOSRStateOffset   = 0x0C
	HeaderFlagsOffset      = 0x10
	CellSize               = 0x20
	CellStateOffset        = 0x00
	CellAccessKindOffset   = 0x01
	CellSlotKindOffset     = 0x02
	CellFlagsOffset        = 0x03
	CellTableVersionOffset = 0x04
	CellCachedIndexOffset  = 0x08
	CellReservedOffset     = 0x0C
	CellTableRefOffset     = 0x10
	CellKeyBitsOffset      = 0x18
)

const (
	StateGeneric State = iota
	StateMonomorphic
	StateMegamorphic
)

const (
	AccessInvalid AccessKind = iota
	AccessArray
	AccessHash
)

type Header struct {
	SlotCount       uint32
	InterruptBudget int32
	LoopHotness     uint32
	OSRState        uint32
	Flags           uint32
}

type Cell struct {
	State        State
	AccessKind   AccessKind
	SlotKind     SlotKind
	Flags        uint8
	TableVersion uint32
	CachedIndex  uint32
	TableRef     value.HeapRef44
	KeyBits      value.Raw
}

func NewHeader(slotCount uint32) Header {
	return Header{SlotCount: slotCount, InterruptBudget: 1024}
}

func NewGenericCell(kind SlotKind) Cell {
	return Cell{State: StateGeneric, SlotKind: kind}
}

func NewMegamorphicCell(kind SlotKind) Cell {
	return Cell{State: StateMegamorphic, SlotKind: kind}
}

func NewMonomorphicCell(kind SlotKind, accessKind AccessKind, tableRef value.HeapRef44, tableVersion uint32, cachedIndex uint32, keyBits value.Raw) Cell {
	return Cell{
		State:        StateMonomorphic,
		AccessKind:   accessKind,
		SlotKind:     kind,
		TableVersion: tableVersion,
		CachedIndex:  cachedIndex,
		TableRef:     tableRef,
		KeyBits:      keyBits,
	}
}

func VectorSize(slotCount uint32) uint64 {
	return uint64(HeaderSize) + uint64(slotCount)*CellSize
}

func CellOffset(slot uint32) uint64 {
	return uint64(HeaderSize) + uint64(slot)*CellSize
}

func PackCellPrefix(state State, accessKind AccessKind, slotKind SlotKind) uint32 {
	return uint32(state) | uint32(accessKind)<<8 | uint32(slotKind)<<16
}

func (cell Cell) Prefix() uint32 {
	return PackCellPrefix(cell.State, cell.AccessKind, cell.SlotKind)
}

func ReadHeader(buffer []byte) (Header, error) {
	if len(buffer) < HeaderSize {
		return Header{}, fmt.Errorf("buffer too small for feedback header: %d", len(buffer))
	}
	return Header{
		SlotCount:       binary.LittleEndian.Uint32(buffer[HeaderSlotCountOffset : HeaderSlotCountOffset+4]),
		InterruptBudget: int32(binary.LittleEndian.Uint32(buffer[HeaderBudgetOffset : HeaderBudgetOffset+4])),
		LoopHotness:     binary.LittleEndian.Uint32(buffer[HeaderHotnessOffset : HeaderHotnessOffset+4]),
		OSRState:        binary.LittleEndian.Uint32(buffer[HeaderOSRStateOffset : HeaderOSRStateOffset+4]),
		Flags:           binary.LittleEndian.Uint32(buffer[HeaderFlagsOffset : HeaderFlagsOffset+4]),
	}, nil
}

func WriteHeader(buffer []byte, header Header) error {
	if len(buffer) < HeaderSize {
		return fmt.Errorf("buffer too small for feedback header: %d", len(buffer))
	}
	binary.LittleEndian.PutUint32(buffer[HeaderSlotCountOffset:HeaderSlotCountOffset+4], header.SlotCount)
	binary.LittleEndian.PutUint32(buffer[HeaderBudgetOffset:HeaderBudgetOffset+4], uint32(header.InterruptBudget))
	binary.LittleEndian.PutUint32(buffer[HeaderHotnessOffset:HeaderHotnessOffset+4], header.LoopHotness)
	binary.LittleEndian.PutUint32(buffer[HeaderOSRStateOffset:HeaderOSRStateOffset+4], header.OSRState)
	binary.LittleEndian.PutUint32(buffer[HeaderFlagsOffset:HeaderFlagsOffset+4], header.Flags)
	binary.LittleEndian.PutUint32(buffer[0x14:0x18], 0)
	binary.LittleEndian.PutUint64(buffer[0x18:0x20], 0)
	return nil
}

func ReadCell(buffer []byte) (Cell, error) {
	if len(buffer) < CellSize {
		return Cell{}, fmt.Errorf("buffer too small for feedback cell: %d", len(buffer))
	}
	return Cell{
		State:        State(buffer[CellStateOffset]),
		AccessKind:   AccessKind(buffer[CellAccessKindOffset]),
		SlotKind:     SlotKind(buffer[CellSlotKindOffset]),
		Flags:        buffer[CellFlagsOffset],
		TableVersion: binary.LittleEndian.Uint32(buffer[CellTableVersionOffset : CellTableVersionOffset+4]),
		CachedIndex:  binary.LittleEndian.Uint32(buffer[CellCachedIndexOffset : CellCachedIndexOffset+4]),
		TableRef:     value.HeapRef44(binary.LittleEndian.Uint64(buffer[CellTableRefOffset : CellTableRefOffset+8])),
		KeyBits:      value.Raw(binary.LittleEndian.Uint64(buffer[CellKeyBitsOffset : CellKeyBitsOffset+8])),
	}, nil
}

func WriteCell(buffer []byte, cell Cell) error {
	if len(buffer) < CellSize {
		return fmt.Errorf("buffer too small for feedback cell: %d", len(buffer))
	}
	buffer[CellStateOffset] = byte(cell.State)
	buffer[CellAccessKindOffset] = byte(cell.AccessKind)
	buffer[CellSlotKindOffset] = byte(cell.SlotKind)
	buffer[CellFlagsOffset] = cell.Flags
	binary.LittleEndian.PutUint32(buffer[CellTableVersionOffset:CellTableVersionOffset+4], cell.TableVersion)
	binary.LittleEndian.PutUint32(buffer[CellCachedIndexOffset:CellCachedIndexOffset+4], cell.CachedIndex)
	binary.LittleEndian.PutUint32(buffer[CellReservedOffset:CellReservedOffset+4], 0)
	binary.LittleEndian.PutUint64(buffer[CellTableRefOffset:CellTableRefOffset+8], uint64(cell.TableRef))
	binary.LittleEndian.PutUint64(buffer[CellKeyBitsOffset:CellKeyBitsOffset+8], uint64(cell.KeyBits))
	return nil
}

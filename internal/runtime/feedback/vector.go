package feedback

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/runtime/value"
)

type State uint8

type AccessKind uint8

const (
	HeaderSize            = 0x20
	HeaderSlotCountOffset = 0x00
	HeaderBudgetOffset    = 0x04
	HeaderHotnessOffset   = 0x08
	HeaderOSRStateOffset  = 0x0C
	HeaderFlagsOffset     = 0x10
	CellSize              = 0x20
	CellStateOffset       = 0x00
	CellAccessKindOffset  = 0x01
	CellSlotKindOffset    = 0x02
	CellFlagsOffset       = 0x03
	CellPayload32AOffset  = 0x04
	CellPayload32BOffset  = 0x08
	CellPayload32COffset  = 0x0C
	CellHeapRefOffset     = 0x10
	CellValueBitsOffset   = 0x18
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
	AccessCallLuaClosure
	AccessCallHostFunction
	AccessUpvalueOpen
	AccessUpvalueClosed
)

type Header struct {
	SlotCount       uint32
	InterruptBudget int32
	LoopHotness     uint32
	OSRState        uint32
	Flags           uint32
}

type Cell struct {
	State      State
	AccessKind AccessKind
	SlotKind   SlotKind
	Flags      uint8
	Payload32A uint32
	Payload32B uint32
	Payload32C uint32
	HeapRef    value.HeapRef44
	ValueBits  value.Raw
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

func NewMonomorphicCell(kind SlotKind, accessKind AccessKind, payload32A uint32, payload32B uint32, payload32C uint32, heapRef value.HeapRef44, valueBits value.Raw) Cell {
	return Cell{
		State:      StateMonomorphic,
		AccessKind: accessKind,
		SlotKind:   kind,
		Payload32A: payload32A,
		Payload32B: payload32B,
		Payload32C: payload32C,
		HeapRef:    heapRef,
		ValueBits:  valueBits,
	}
}

func NewTableMonomorphicCell(kind SlotKind, accessKind AccessKind, tableRef value.HeapRef44, tableVersion uint32, cachedIndex uint32, keyBits value.Raw) Cell {
	return NewMonomorphicCell(kind, accessKind, tableVersion, cachedIndex, 0, tableRef, keyBits)
}

func NewCallMonomorphicCell(kind SlotKind, accessKind AccessKind, targetRef value.HeapRef44, calleeBits value.Raw) Cell {
	return NewMonomorphicCell(kind, accessKind, 0, 0, 0, targetRef, calleeBits)
}

func NewUpvalueMonomorphicCell(kind SlotKind, accessKind AccessKind, upvalueRef value.HeapRef44, observedBits value.Raw) Cell {
	return NewMonomorphicCell(kind, accessKind, 0, 0, 0, upvalueRef, observedBits)
}

func (cell Cell) TableVersion() uint32 {
	return cell.Payload32A
}

func (cell Cell) CachedIndex() uint32 {
	return cell.Payload32B
}

func (cell Cell) TableRef() value.HeapRef44 {
	return cell.HeapRef
}

func (cell Cell) KeyBits() value.Raw {
	return cell.ValueBits
}

func (cell Cell) TargetRef() value.HeapRef44 {
	return cell.HeapRef
}

func (cell Cell) ObservedValueBits() value.Raw {
	return cell.ValueBits
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
		State:      State(buffer[CellStateOffset]),
		AccessKind: AccessKind(buffer[CellAccessKindOffset]),
		SlotKind:   SlotKind(buffer[CellSlotKindOffset]),
		Flags:      buffer[CellFlagsOffset],
		Payload32A: binary.LittleEndian.Uint32(buffer[CellPayload32AOffset : CellPayload32AOffset+4]),
		Payload32B: binary.LittleEndian.Uint32(buffer[CellPayload32BOffset : CellPayload32BOffset+4]),
		Payload32C: binary.LittleEndian.Uint32(buffer[CellPayload32COffset : CellPayload32COffset+4]),
		HeapRef:    value.HeapRef44(binary.LittleEndian.Uint64(buffer[CellHeapRefOffset : CellHeapRefOffset+8])),
		ValueBits:  value.Raw(binary.LittleEndian.Uint64(buffer[CellValueBitsOffset : CellValueBitsOffset+8])),
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
	binary.LittleEndian.PutUint32(buffer[CellPayload32AOffset:CellPayload32AOffset+4], cell.Payload32A)
	binary.LittleEndian.PutUint32(buffer[CellPayload32BOffset:CellPayload32BOffset+4], cell.Payload32B)
	binary.LittleEndian.PutUint32(buffer[CellPayload32COffset:CellPayload32COffset+4], cell.Payload32C)
	binary.LittleEndian.PutUint64(buffer[CellHeapRefOffset:CellHeapRefOffset+8], uint64(cell.HeapRef))
	binary.LittleEndian.PutUint64(buffer[CellValueBitsOffset:CellValueBitsOffset+8], uint64(cell.ValueBits))
	return nil
}

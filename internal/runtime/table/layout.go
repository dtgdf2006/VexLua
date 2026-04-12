package table

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/runtime/value"
)

type Flags uint32

type KeyClass uint8

const (
	ObjectSize               = 0x60
	FlagsOffset              = 0x10
	ShapeIDOffset            = 0x14
	TableVersionOffset       = 0x18
	Reserved0Offset          = 0x1C
	MetatableOffset          = 0x20
	ArrayDataOffset          = 0x28
	ArrayLenHintOffset       = 0x30
	ArrayCapOffset           = 0x34
	CtrlDataOffset           = 0x38
	EntriesDataOffset        = 0x40
	HashCountOffset          = 0x48
	HashCapacityOffset       = 0x4C
	GrowthLeftOffset         = 0x50
	HashSeedOffset           = 0x54
	Reserved1Offset          = 0x58
	EntrySize                = 0x20
	EntryFullHashOffset      = 0x00
	EntryKeyClassOffset      = 0x04
	EntryReserved0Off        = 0x05
	EntryReserved1Off        = 0x06
	EntryKeyAuxOffset        = 0x08
	EntryKeyOffset           = 0x10
	EntryValueOffset         = 0x18
	MinHashCapacity          = 16
	CtrlEmpty           byte = 0x80
	CtrlDeleted         byte = 0xFE
	CtrlSentinel        byte = 0xFF
)

const (
	FlagHasMetatable Flags = 1 << iota
	FlagIndexFastPathBlocked
	FlagNewIndexFastPathBlocked
	FlagWeakKeys
	FlagWeakValues
	FlagHasArrayPart
	FlagHasHashPart
	FlagRehashing
	FlagFrozen
	FlagReadOnly
)

const (
	KeyClassGeneric KeyClass = iota
	KeyClassInternedString
	KeyClassIntLikeNumber
	KeyClassNonIntNumber
	KeyClassLightHandle
	KeyClassHeapObjectIdentity
)

type Object struct {
	Common       value.CommonHeader
	Flags        Flags
	ShapeID      uint32
	TableVersion uint32
	Metatable    value.TValue
	ArrayData    value.HeapOff64
	ArrayLenHint uint32
	ArrayCap     uint32
	CtrlData     value.HeapOff64
	EntriesData  value.HeapOff64
	HashCount    uint32
	HashCapacity uint32
	GrowthLeft   uint32
	HashSeed     uint32
}

type Entry struct {
	FullHash uint32
	KeyClass KeyClass
	KeyAux   uint64
	Key      value.TValue
	Value    value.TValue
}

func NewObject(hashSeed uint32) Object {
	return Object{
		Common: value.CommonHeader{
			Kind:      value.KindTable,
			SizeBytes: ObjectSize,
			Version:   1,
		},
		TableVersion: 1,
		Metatable:    value.NilValue(),
		HashSeed:     hashSeed,
	}
}

func (flags Flags) Has(mask Flags) bool {
	return flags&mask == mask
}

func (flags Flags) With(mask Flags) Flags {
	return flags | mask
}

func (flags Flags) Without(mask Flags) Flags {
	return flags &^ mask
}

func (object *Object) BumpVersion() {
	object.TableVersion++
	if object.TableVersion == 0 {
		object.TableVersion = 1
	}
}

func (object *Object) SyncLayoutFlags() {
	if isNilValue(object.Metatable) {
		object.Flags = object.Flags.Without(FlagHasMetatable)
	} else {
		object.Flags = object.Flags.With(FlagHasMetatable)
	}
	if object.ArrayCap == 0 || object.ArrayData == 0 {
		object.Flags = object.Flags.Without(FlagHasArrayPart)
	} else {
		object.Flags = object.Flags.With(FlagHasArrayPart)
	}
	if object.HashCapacity == 0 || object.CtrlData == 0 || object.EntriesData == 0 {
		object.Flags = object.Flags.Without(FlagHasHashPart)
	} else {
		object.Flags = object.Flags.With(FlagHasHashPart)
	}
	if isNilValue(object.Metatable) {
		object.Flags = object.Flags.Without(FlagIndexFastPathBlocked | FlagNewIndexFastPathBlocked)
	}
}

func ReadObject(buffer []byte) (Object, error) {
	if len(buffer) < ObjectSize {
		return Object{}, fmt.Errorf("buffer too small for table object: %d", len(buffer))
	}
	common, err := value.ReadCommonHeader(buffer)
	if err != nil {
		return Object{}, err
	}
	if common.Kind != value.KindTable {
		return Object{}, fmt.Errorf("expected %s object, got %s", value.KindTable, common.Kind)
	}
	return Object{
		Common:       common,
		Flags:        Flags(binary.LittleEndian.Uint32(buffer[FlagsOffset : FlagsOffset+4])),
		ShapeID:      binary.LittleEndian.Uint32(buffer[ShapeIDOffset : ShapeIDOffset+4]),
		TableVersion: binary.LittleEndian.Uint32(buffer[TableVersionOffset : TableVersionOffset+4]),
		Metatable:    value.FromRaw(value.Raw(binary.LittleEndian.Uint64(buffer[MetatableOffset : MetatableOffset+8]))),
		ArrayData:    value.HeapOff64(binary.LittleEndian.Uint64(buffer[ArrayDataOffset : ArrayDataOffset+8])),
		ArrayLenHint: binary.LittleEndian.Uint32(buffer[ArrayLenHintOffset : ArrayLenHintOffset+4]),
		ArrayCap:     binary.LittleEndian.Uint32(buffer[ArrayCapOffset : ArrayCapOffset+4]),
		CtrlData:     value.HeapOff64(binary.LittleEndian.Uint64(buffer[CtrlDataOffset : CtrlDataOffset+8])),
		EntriesData:  value.HeapOff64(binary.LittleEndian.Uint64(buffer[EntriesDataOffset : EntriesDataOffset+8])),
		HashCount:    binary.LittleEndian.Uint32(buffer[HashCountOffset : HashCountOffset+4]),
		HashCapacity: binary.LittleEndian.Uint32(buffer[HashCapacityOffset : HashCapacityOffset+4]),
		GrowthLeft:   binary.LittleEndian.Uint32(buffer[GrowthLeftOffset : GrowthLeftOffset+4]),
		HashSeed:     binary.LittleEndian.Uint32(buffer[HashSeedOffset : HashSeedOffset+4]),
	}, nil
}

func WriteObject(buffer []byte, object Object) error {
	if len(buffer) < ObjectSize {
		return fmt.Errorf("buffer too small for table object: %d", len(buffer))
	}
	object.SyncLayoutFlags()
	if current, err := value.ReadCommonHeader(buffer); err == nil && current.Kind == object.Common.Kind {
		object.Common.Mark = current.Mark
	}
	if err := value.WriteCommonHeader(buffer, object.Common); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(buffer[FlagsOffset:FlagsOffset+4], uint32(object.Flags))
	binary.LittleEndian.PutUint32(buffer[ShapeIDOffset:ShapeIDOffset+4], object.ShapeID)
	binary.LittleEndian.PutUint32(buffer[TableVersionOffset:TableVersionOffset+4], object.TableVersion)
	binary.LittleEndian.PutUint32(buffer[Reserved0Offset:Reserved0Offset+4], 0)
	binary.LittleEndian.PutUint64(buffer[MetatableOffset:MetatableOffset+8], uint64(object.Metatable.Bits()))
	binary.LittleEndian.PutUint64(buffer[ArrayDataOffset:ArrayDataOffset+8], uint64(object.ArrayData))
	binary.LittleEndian.PutUint32(buffer[ArrayLenHintOffset:ArrayLenHintOffset+4], object.ArrayLenHint)
	binary.LittleEndian.PutUint32(buffer[ArrayCapOffset:ArrayCapOffset+4], object.ArrayCap)
	binary.LittleEndian.PutUint64(buffer[CtrlDataOffset:CtrlDataOffset+8], uint64(object.CtrlData))
	binary.LittleEndian.PutUint64(buffer[EntriesDataOffset:EntriesDataOffset+8], uint64(object.EntriesData))
	binary.LittleEndian.PutUint32(buffer[HashCountOffset:HashCountOffset+4], object.HashCount)
	binary.LittleEndian.PutUint32(buffer[HashCapacityOffset:HashCapacityOffset+4], object.HashCapacity)
	binary.LittleEndian.PutUint32(buffer[GrowthLeftOffset:GrowthLeftOffset+4], object.GrowthLeft)
	binary.LittleEndian.PutUint32(buffer[HashSeedOffset:HashSeedOffset+4], object.HashSeed)
	binary.LittleEndian.PutUint64(buffer[Reserved1Offset:Reserved1Offset+8], 0)
	return nil
}

func ReadEntry(buffer []byte) (Entry, error) {
	if len(buffer) < EntrySize {
		return Entry{}, fmt.Errorf("buffer too small for table entry: %d", len(buffer))
	}
	return Entry{
		FullHash: binary.LittleEndian.Uint32(buffer[EntryFullHashOffset : EntryFullHashOffset+4]),
		KeyClass: KeyClass(buffer[EntryKeyClassOffset]),
		KeyAux:   binary.LittleEndian.Uint64(buffer[EntryKeyAuxOffset : EntryKeyAuxOffset+8]),
		Key:      value.FromRaw(value.Raw(binary.LittleEndian.Uint64(buffer[EntryKeyOffset : EntryKeyOffset+8]))),
		Value:    value.FromRaw(value.Raw(binary.LittleEndian.Uint64(buffer[EntryValueOffset : EntryValueOffset+8]))),
	}, nil
}

func WriteEntry(buffer []byte, entry Entry) error {
	if len(buffer) < EntrySize {
		return fmt.Errorf("buffer too small for table entry: %d", len(buffer))
	}
	binary.LittleEndian.PutUint32(buffer[EntryFullHashOffset:EntryFullHashOffset+4], entry.FullHash)
	buffer[EntryKeyClassOffset] = byte(entry.KeyClass)
	buffer[EntryReserved0Off] = 0
	binary.LittleEndian.PutUint16(buffer[EntryReserved1Off:EntryReserved1Off+2], 0)
	binary.LittleEndian.PutUint64(buffer[EntryKeyAuxOffset:EntryKeyAuxOffset+8], entry.KeyAux)
	binary.LittleEndian.PutUint64(buffer[EntryKeyOffset:EntryKeyOffset+8], uint64(entry.Key.Bits()))
	binary.LittleEndian.PutUint64(buffer[EntryValueOffset:EntryValueOffset+8], uint64(entry.Value.Bits()))
	return nil
}

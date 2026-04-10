package runtime

import "unsafe"

type vexarcSliceLayout struct {
	Data uintptr
	Len  int
	Cap  int
}

const (
	VexarcValueBoxBase      = boxBase
	VexarcValueBoxCheckMask = boxCheckMask
	VexarcValuePayloadMask  = payloadMask
	VexarcValueTagShift     = tagShift
	VexarcValueHandleTag    = tagHandle
	VexarcValueNil          = boxBase | (tagNil << tagShift)
	VexarcHandleKindShift   = handleKindShift
	VexarcObjectKindTable   = uint64(ObjectTable)

	VexarcTableArrayOffset   = uintptr(unsafe.Offsetof(Table{}.array))
	VexarcTableFieldsOffset  = uintptr(unsafe.Offsetof(Table{}.fields))
	VexarcTableMetaOffset    = uintptr(unsafe.Offsetof(Table{}.meta))
	VexarcTableVersionOffset = uintptr(unsafe.Offsetof(Table{}.version))

	VexarcFieldCacheSize          = uintptr(unsafe.Sizeof(FieldCache{}))
	VexarcFieldCacheTableOffset   = uintptr(unsafe.Offsetof(FieldCache{}.Table))
	VexarcFieldCacheVersionOffset = uintptr(unsafe.Offsetof(FieldCache{}.Version))
	VexarcFieldCacheSlotOffset    = uintptr(unsafe.Offsetof(FieldCache{}.Slot))

	VexarcSliceDataOffset = uintptr(unsafe.Offsetof(vexarcSliceLayout{}.Data))
	VexarcSliceLenOffset  = uintptr(unsafe.Offsetof(vexarcSliceLayout{}.Len))
	VexarcSliceCapOffset  = uintptr(unsafe.Offsetof(vexarcSliceLayout{}.Cap))
)

// Package value defines VexLua value encoding and common runtime object kinds.
package value

type Raw uint64

type Tag uint8

type ObjectKind uint16

type HeapRef44 uint64

type HeapOff64 uint64

const (
	TValueSize       = 8
	ObjectAlignment  = 16
	CommonHeaderSize = 0x10

	BoxedMarker  Raw = 0xFFFF000000000000
	CanonicalNaN Raw = 0x7FF8000000000000

	TagShift        = 44
	PayloadBits     = 44
	PayloadMask Raw = (1 << PayloadBits) - 1
)

const (
	CommonHeaderKindOffset      = 0x00
	CommonHeaderMarkOffset      = 0x02
	CommonHeaderFlagsOffset     = 0x03
	CommonHeaderSizeBytesOffset = 0x04
	CommonHeaderVersionOffset   = 0x08
	CommonHeaderAuxOffset       = 0x0C
)

const (
	TagNil Tag = iota
	TagBool
	TagI32
	TagStringRef
	TagTableRef
	TagLuaClosureRef
	TagProtoRef
	TagUpValueRef
	TagThreadRef
	TagHostObjectRef
	TagHostFunctionRef
	TagNativeClosureRef
	TagLightHandle
	TagReserved1
	TagReserved2
	TagReserved3
)

const (
	KindString ObjectKind = 1 + iota
	KindTable
	KindLuaClosure
	KindProto
	KindUpValue
	KindThread
	KindHostObject
	KindHostFunction
	KindHostDescriptor
	KindNativeClosure
	KindCodeMetadata
)

package value

import (
	"fmt"
	"math"
)

const TagInvalid Tag = 0xFF

type TValue Raw

func FromRaw(raw Raw) TValue {
	return TValue(raw)
}

func (value TValue) Bits() Raw {
	return Raw(value)
}

func CanonicalNaNValue() TValue {
	return TValue(CanonicalNaN)
}

func NumberValue(number float64) TValue {
	if math.IsNaN(number) {
		return CanonicalNaNValue()
	}
	return TValue(math.Float64bits(number))
}

func CanonicalizeNumber(number float64) float64 {
	return math.Float64frombits(uint64(NumberValue(number).Bits()))
}

func (value TValue) IsNumber() bool {
	return value.Bits()>>48 != 0xFFFF
}

func (value TValue) IsBoxed() bool {
	return value.Bits()>>48 == 0xFFFF
}

func (value TValue) IsBoxedTag(tag Tag) bool {
	return value.IsBoxed() && value.Tag() == tag
}

func (value TValue) Tag() Tag {
	if !value.IsBoxed() {
		return TagInvalid
	}
	return Tag((value.Bits() >> TagShift) & 0xF)
}

func (value TValue) Payload() uint64 {
	if !value.IsBoxed() {
		return 0
	}
	return uint64(value.Bits() & PayloadMask)
}

func (value TValue) Float64() (float64, bool) {
	if !value.IsNumber() {
		return 0, false
	}
	return math.Float64frombits(uint64(value.Bits())), true
}

func (value TValue) Bool() (bool, bool) {
	if !value.IsBoxedTag(TagBool) {
		return false, false
	}
	return value.Payload() != 0, true
}

func (value TValue) HeapRef() (HeapRef44, bool) {
	if !IsHeapRefTag(value.Tag()) {
		return 0, false
	}
	return HeapRef44(value.Payload()), true
}

func (value TValue) String() string {
	if value.IsNumber() {
		number, _ := value.Float64()
		return fmt.Sprintf("number(%g)", number)
	}
	if value.IsBoxedTag(TagNil) {
		return "nil"
	}
	if value.IsBoxedTag(TagBool) {
		boolean, _ := value.Bool()
		return fmt.Sprintf("bool(%t)", boolean)
	}
	if ref, ok := value.HeapRef(); ok {
		return fmt.Sprintf("%s(%#x)", value.Tag(), uint64(ref))
	}
	return fmt.Sprintf("boxed(tag=%s,payload=%#x)", value.Tag(), value.Payload())
}

func BoxedValue(tag Tag, payload uint64) TValue {
	if tag > TagReserved3 {
		panic(fmt.Sprintf("invalid tag: %d", tag))
	}
	if payload > uint64(PayloadMask) {
		panic(fmt.Sprintf("payload out of range: %#x", payload))
	}
	return TValue(BoxedMarker | (Raw(tag) << TagShift) | Raw(payload))
}

func NilValue() TValue {
	return BoxedValue(TagNil, 0)
}

func BoolValue(boolean bool) TValue {
	if boolean {
		return BoxedValue(TagBool, 1)
	}
	return BoxedValue(TagBool, 0)
}

func StringRefValue(ref HeapRef44) TValue {
	return mustHeapRefValue(TagStringRef, ref)
}

func TableRefValue(ref HeapRef44) TValue {
	return mustHeapRefValue(TagTableRef, ref)
}

func LuaClosureRefValue(ref HeapRef44) TValue {
	return mustHeapRefValue(TagLuaClosureRef, ref)
}

func ProtoRefValue(ref HeapRef44) TValue {
	return mustHeapRefValue(TagProtoRef, ref)
}

func UpValueRefValue(ref HeapRef44) TValue {
	return mustHeapRefValue(TagUpValueRef, ref)
}

func ThreadRefValue(ref HeapRef44) TValue {
	return mustHeapRefValue(TagThreadRef, ref)
}

func HostObjectRefValue(ref HeapRef44) TValue {
	return mustHeapRefValue(TagHostObjectRef, ref)
}

func HostFunctionRefValue(ref HeapRef44) TValue {
	return mustHeapRefValue(TagHostFunctionRef, ref)
}

func NativeClosureRefValue(ref HeapRef44) TValue {
	return mustHeapRefValue(TagNativeClosureRef, ref)
}

func LightHandleValue(handle uint64) TValue {
	if handle > uint64(PayloadMask) {
		panic(fmt.Sprintf("light handle out of range: %#x", handle))
	}
	return BoxedValue(TagLightHandle, handle)
}

func IsHeapRefTag(tag Tag) bool {
	return tag >= TagStringRef && tag <= TagNativeClosureRef
}

func mustHeapRefValue(tag Tag, ref HeapRef44) TValue {
	if !IsHeapRefTag(tag) {
		panic(fmt.Sprintf("tag %d is not a heap reference tag", tag))
	}
	if ref == 0 {
		panic("heap reference cannot be zero")
	}
	if uint64(ref) > uint64(PayloadMask) {
		panic(fmt.Sprintf("heap reference out of range: %#x", uint64(ref)))
	}
	return BoxedValue(tag, uint64(ref))
}

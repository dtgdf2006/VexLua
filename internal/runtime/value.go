package runtime

import (
	"fmt"
	"math"
)

type Value uint64

type ValueKind uint8

const (
	KindNumber ValueKind = iota
	KindNil
	KindBool
	KindHandle
)

const (
	boxBase      uint64 = 0x7ffc000000000000
	boxCheckMask uint64 = 0xffff000000000000
	tagShift            = 44
	payloadMask  uint64 = (uint64(1) << tagShift) - 1

	tagNumberNaN uint64 = 0
	tagNil       uint64 = 1
	tagBool      uint64 = 2
	tagHandle    uint64 = 3
)

var (
	NilValue   = boxed(tagNil, 0)
	FalseValue = boxed(tagBool, 0)
	TrueValue  = boxed(tagBool, 1)
	NaNValue   = boxed(tagNumberNaN, 1)
)

func boxed(tag, payload uint64) Value {
	return Value(boxBase | (tag << tagShift) | (payload & payloadMask))
}

func isBoxed(bits uint64) bool {
	return bits&boxCheckMask == boxBase
}

func NumberValue(v float64) Value {
	if math.IsNaN(v) {
		return NaNValue
	}
	bits := math.Float64bits(v)
	if isBoxed(bits) {
		return NaNValue
	}
	return Value(bits)
}

func BoolValue(v bool) Value {
	if v {
		return TrueValue
	}
	return FalseValue
}

func HandleValue(h Handle) Value {
	return boxed(tagHandle, uint64(h))
}

func (v Value) Kind() ValueKind {
	bits := uint64(v)
	if !isBoxed(bits) {
		return KindNumber
	}
	switch (bits >> tagShift) & 0xF {
	case tagNil:
		return KindNil
	case tagBool:
		return KindBool
	case tagHandle:
		return KindHandle
	default:
		return KindNumber
	}
}

func (v Value) IsNumber() bool {
	return v.Kind() == KindNumber
}

func (v Value) Number() float64 {
	if v == NaNValue {
		return math.NaN()
	}
	return math.Float64frombits(uint64(v))
}

func (v Value) Bool() bool {
	return uint64(v)&payloadMask == 1
}

func (v Value) Handle() (Handle, bool) {
	if v.Kind() != KindHandle {
		return 0, false
	}
	return Handle(uint64(v) & payloadMask), true
}

func (v Value) String() string {
	switch v.Kind() {
	case KindNil:
		return "nil"
	case KindBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case KindNumber:
		return fmt.Sprintf("%g", v.Number())
	case KindHandle:
		h, _ := v.Handle()
		return h.String()
	default:
		return "<unknown>"
	}
}

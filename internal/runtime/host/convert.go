package host

import (
	"fmt"
	"math"
	"reflect"

	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
)

func ToHostValue(strings *rtstring.InternTable, slotValue value.TValue) (any, error) {
	if slotValue.IsBoxedTag(value.TagNil) {
		return nil, nil
	}
	if boolean, ok := slotValue.Bool(); ok {
		return boolean, nil
	}
	if number, ok := slotValue.Float64(); ok {
		return number, nil
	}
	if slotValue.IsBoxedTag(value.TagStringRef) {
		if strings == nil {
			return nil, fmt.Errorf("string table is nil")
		}
		ref, _ := slotValue.HeapRef()
		return strings.Text(ref)
	}
	return slotValue, nil
}

func FromHostValue(strings *rtstring.InternTable, candidate any) (value.TValue, error) {
	if candidate == nil {
		return value.NilValue(), nil
	}
	switch typed := candidate.(type) {
	case value.TValue:
		return typed, nil
	case bool:
		return value.BoolValue(typed), nil
	case string:
		if strings == nil {
			return value.NilValue(), fmt.Errorf("string table is nil")
		}
		handle, err := strings.Intern(typed)
		if err != nil {
			return value.NilValue(), err
		}
		return handle.Value, nil
	case float32:
		return value.NumberValue(float64(typed)), nil
	case float64:
		return value.NumberValue(typed), nil
	case int:
		return value.NumberValue(float64(typed)), nil
	case int8:
		return value.NumberValue(float64(typed)), nil
	case int16:
		return value.NumberValue(float64(typed)), nil
	case int32:
		return value.NumberValue(float64(typed)), nil
	case int64:
		return value.NumberValue(float64(typed)), nil
	case uint:
		return value.NumberValue(float64(typed)), nil
	case uint8:
		return value.NumberValue(float64(typed)), nil
	case uint16:
		return value.NumberValue(float64(typed)), nil
	case uint32:
		return value.NumberValue(float64(typed)), nil
	case uint64:
		if typed > math.MaxInt64 {
			return value.NilValue(), fmt.Errorf("uint64 %d is too large to round-trip through Lua number", typed)
		}
		return value.NumberValue(float64(typed)), nil
	default:
		return value.NilValue(), fmt.Errorf("unsupported host value type %T", candidate)
	}
}

func valueFromReflect(strings *rtstring.InternTable, reflected reflect.Value) (value.TValue, error) {
	if !reflected.IsValid() {
		return value.NilValue(), nil
	}
	return FromHostValue(strings, reflected.Interface())
}

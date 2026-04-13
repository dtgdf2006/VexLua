package interp

import (
	"fmt"

	rtmeta "vexlua/internal/runtime/meta"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
)

const metaLenName = "__len"

func (engine *Engine) LengthBoundary(thread *state.ThreadState, slotValue value.TValue) (value.TValue, error) {
	if slotValue.IsBoxedTag(value.TagStringRef) {
		ref, _ := slotValue.HeapRef()
		header, err := engine.Strings.Header(ref)
		if err != nil {
			return value.NilValue(), err
		}
		return value.NumberValue(float64(header.Length)), nil
	}
	if slotValue.IsBoxedTag(value.TagTableRef) {
		ref, _ := slotValue.HeapRef()
		length, err := engine.Tables.Length(ref)
		if err != nil {
			return value.NilValue(), err
		}
		return value.NumberValue(float64(length)), nil
	}
	metamethod, ok, err := engine.valueMetamethod(slotValue, metaLenName)
	if err != nil {
		return value.NilValue(), err
	}
	if !ok {
		return value.NilValue(), lengthBoundaryTypeError(slotValue)
	}
	if thread == nil {
		return value.NilValue(), fmt.Errorf("thread cannot be nil when calling %s", metaLenName)
	}
	results, err := engine.CallValueBoundary(thread, metamethod, []value.TValue{slotValue}, 1)
	if err != nil {
		return value.NilValue(), err
	}
	if len(results) == 0 {
		return value.NilValue(), nil
	}
	return results[0], nil
}

func lengthBoundaryTypeError(targetValue value.TValue) error {
	return fmt.Errorf("attempt to get length of a %s value", rtmeta.TypeName(targetValue))
}

package interp

import (
	"fmt"
	"strings"

	"vexlua/internal/bytecode"
	rtmeta "vexlua/internal/runtime/meta"
	"vexlua/internal/runtime/state"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
)

const (
	metaEqName = "__eq"
	metaLtName = "__lt"
	metaLeName = "__le"
)

func (engine *Engine) CompareBoundary(thread *state.ThreadState, opcode bytecode.Opcode, left value.TValue, right value.TValue) (bool, error) {
	switch opcode {
	case bytecode.OP_EQ:
		return engine.compareEqualBoundary(thread, left, right)
	case bytecode.OP_LT:
		return engine.compareLessBoundary(thread, left, right)
	case bytecode.OP_LE:
		return engine.compareLessEqualBoundary(thread, left, right)
	default:
		return false, fmt.Errorf("unsupported compare opcode %s", opcode)
	}
}

func (engine *Engine) compareEqualBoundary(thread *state.ThreadState, left value.TValue, right value.TValue) (bool, error) {
	if left.IsNumber() && right.IsNumber() {
		leftNumber, _ := left.Float64()
		rightNumber, _ := right.Float64()
		return leftNumber == rightNumber, nil
	}
	if left.Tag() != right.Tag() {
		return false, nil
	}
	switch left.Tag() {
	case value.TagNil:
		return true, nil
	case value.TagTableRef, value.TagHostObjectRef:
		if left.Bits() == right.Bits() {
			return true, nil
		}
		result, handled, err := engine.callEqualMetamethod(thread, left, right)
		if err != nil {
			return false, err
		}
		if handled {
			return result, nil
		}
		return false, nil
	default:
		return left.Bits() == right.Bits(), nil
	}
}

func (engine *Engine) compareLessBoundary(thread *state.ThreadState, left value.TValue, right value.TValue) (bool, error) {
	if left.IsNumber() && right.IsNumber() {
		leftNumber, _ := left.Float64()
		rightNumber, _ := right.Float64()
		return leftNumber < rightNumber, nil
	}
	if left.Tag() != right.Tag() {
		return false, compareOrderError(left, right)
	}
	if left.IsBoxedTag(value.TagStringRef) {
		compare, err := engine.compareStringValues(left, right)
		if err != nil {
			return false, err
		}
		return compare < 0, nil
	}
	result, handled, err := engine.callOrderMetamethod(thread, left, right, metaLtName)
	if err != nil {
		return false, err
	}
	if handled {
		return result, nil
	}
	return false, compareOrderError(left, right)
}

func (engine *Engine) compareLessEqualBoundary(thread *state.ThreadState, left value.TValue, right value.TValue) (bool, error) {
	if left.IsNumber() && right.IsNumber() {
		leftNumber, _ := left.Float64()
		rightNumber, _ := right.Float64()
		return leftNumber <= rightNumber, nil
	}
	if left.Tag() != right.Tag() {
		return false, compareOrderError(left, right)
	}
	if left.IsBoxedTag(value.TagStringRef) {
		compare, err := engine.compareStringValues(left, right)
		if err != nil {
			return false, err
		}
		return compare <= 0, nil
	}
	result, handled, err := engine.callOrderMetamethod(thread, left, right, metaLeName)
	if err != nil {
		return false, err
	}
	if handled {
		return result, nil
	}
	result, handled, err = engine.callOrderMetamethod(thread, right, left, metaLtName)
	if err != nil {
		return false, err
	}
	if handled {
		return !result, nil
	}
	return false, compareOrderError(left, right)
}

func (engine *Engine) compareStringValues(left value.TValue, right value.TValue) (int, error) {
	leftRef, _ := left.HeapRef()
	_, leftText, err := rtstring.StringAt(engine.Heap, leftRef)
	if err != nil {
		return 0, err
	}
	rightRef, _ := right.HeapRef()
	_, rightText, err := rtstring.StringAt(engine.Heap, rightRef)
	if err != nil {
		return 0, err
	}
	return strings.Compare(leftText, rightText), nil
}

func (engine *Engine) callEqualMetamethod(thread *state.ThreadState, left value.TValue, right value.TValue) (bool, bool, error) {
	metamethod, handled, err := engine.sharedEqualMetamethod(left, right)
	if err != nil || !handled {
		return false, handled, err
	}
	result, err := engine.invokeCompareMetamethod(thread, metamethod, left, right, metaEqName)
	if err != nil {
		return false, false, err
	}
	return result, true, nil
}

func (engine *Engine) sharedEqualMetamethod(left value.TValue, right value.TValue) (value.TValue, bool, error) {
	leftMetatable, found, err := engine.equalComparableMetatable(left)
	if err != nil || !found {
		return value.NilValue(), false, err
	}
	rightMetatable, found, err := engine.equalComparableMetatable(right)
	if err != nil || !found {
		return value.NilValue(), false, err
	}
	metamethod, ok, err := engine.metamethodFromMetatable(leftMetatable, metaEqName)
	if err != nil || !ok {
		return value.NilValue(), false, err
	}
	if leftMetatable.Bits() == rightMetatable.Bits() {
		return metamethod, true, nil
	}
	rightMethod, ok, err := engine.metamethodFromMetatable(rightMetatable, metaEqName)
	if err != nil || !ok {
		return value.NilValue(), false, err
	}
	if metamethod.Bits() != rightMethod.Bits() {
		return value.NilValue(), false, nil
	}
	return metamethod, true, nil
}

func (engine *Engine) equalComparableMetatable(target value.TValue) (value.TValue, bool, error) {
	if target.IsBoxedTag(value.TagTableRef) {
		ref, _ := target.HeapRef()
		return engine.tableMetatableValue(ref)
	}
	if target.IsBoxedTag(value.TagHostObjectRef) {
		ref, _ := target.HeapRef()
		return engine.hostObjectMetatableValue(ref)
	}
	return value.NilValue(), false, nil
}

func (engine *Engine) callOrderMetamethod(thread *state.ThreadState, left value.TValue, right value.TValue, metamethodName string) (bool, bool, error) {
	leftMethod, ok, err := engine.comparisonMetamethod(left, metamethodName)
	if err != nil || !ok {
		return false, false, err
	}
	rightMethod, ok, err := engine.comparisonMetamethod(right, metamethodName)
	if err != nil {
		return false, false, err
	}
	if !ok || leftMethod.Bits() != rightMethod.Bits() {
		return false, false, nil
	}
	result, err := engine.invokeCompareMetamethod(thread, leftMethod, left, right, metamethodName)
	if err != nil {
		return false, false, err
	}
	return result, true, nil
}

func (engine *Engine) comparisonMetamethod(target value.TValue, metamethodName string) (value.TValue, bool, error) {
	if target.IsBoxedTag(value.TagTableRef) {
		ref, _ := target.HeapRef()
		return engine.tableMetamethodValue(ref, metamethodName)
	}
	if target.IsBoxedTag(value.TagHostObjectRef) {
		ref, _ := target.HeapRef()
		return engine.hostObjectMetamethodValue(ref, metamethodName)
	}
	return engine.valueMetamethod(target, metamethodName)
}

func (engine *Engine) hostObjectMetamethodValue(ref value.HeapRef44, metamethodName string) (value.TValue, bool, error) {
	metatable, found, err := engine.hostObjectMetatableValue(ref)
	if err != nil || !found {
		return value.NilValue(), false, err
	}
	return engine.metamethodFromMetatable(metatable, metamethodName)
}

func (engine *Engine) hostObjectMetatableValue(ref value.HeapRef44) (value.TValue, bool, error) {
	header, _, _, err := engine.Hosts.ReadHostObject(ref)
	if err != nil {
		return value.NilValue(), false, err
	}
	if header.Metatable.IsBoxedTag(value.TagNil) {
		return value.NilValue(), false, nil
	}
	return header.Metatable, true, nil
}

func (engine *Engine) invokeCompareMetamethod(thread *state.ThreadState, metamethod value.TValue, left value.TValue, right value.TValue, metamethodName string) (bool, error) {
	if thread == nil {
		return false, fmt.Errorf("thread cannot be nil when calling %s", metamethodName)
	}
	results, err := engine.CallValueBoundary(thread, metamethod, []value.TValue{left, right}, 1)
	if err != nil {
		return false, err
	}
	result := value.NilValue()
	if len(results) > 0 {
		result = results[0]
	}
	return !isFalse(result), nil
}

func compareOrderError(left value.TValue, right value.TValue) error {
	leftType := rtmeta.TypeName(left)
	rightType := rtmeta.TypeName(right)
	if leftType == rightType {
		return fmt.Errorf("attempt to compare two %s values", leftType)
	}
	return fmt.Errorf("attempt to compare %s with %s", leftType, rightType)
}

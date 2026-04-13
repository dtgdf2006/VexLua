package interp

import (
	"fmt"

	"vexlua/internal/bytecode"
	rtlua "vexlua/internal/runtime/lua"
	rtmeta "vexlua/internal/runtime/meta"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
)

const (
	metaAddName = "__add"
	metaSubName = "__sub"
	metaMulName = "__mul"
	metaDivName = "__div"
	metaModName = "__mod"
	metaPowName = "__pow"
	metaUnmName = "__unm"
)

func (engine *Engine) ArithmeticBoundary(thread *state.ThreadState, opcode bytecode.Opcode, left value.TValue, right value.TValue) (value.TValue, error) {
	leftNumberValue, leftNumeric, err := rtlua.ToNumber(left, engine.Strings.Text)
	if err != nil {
		return value.NilValue(), err
	}
	rightNumberValue, rightNumeric, err := rtlua.ToNumber(right, engine.Strings.Text)
	if err != nil {
		return value.NilValue(), err
	}
	if leftNumeric && rightNumeric {
		leftNumber, _ := leftNumberValue.Float64()
		rightNumber, _ := rightNumberValue.Float64()
		result, err := rtlua.ArithmeticNumberResult(opcode, leftNumber, rightNumber)
		if err != nil {
			return value.NilValue(), err
		}
		return value.NumberValue(result), nil
	}
	metamethodName, err := arithmeticMetamethodName(opcode)
	if err != nil {
		return value.NilValue(), err
	}
	result, handled, err := engine.callArithmeticMetamethod(thread, metamethodName, left, right)
	if err != nil {
		return value.NilValue(), err
	}
	if handled {
		return result, nil
	}
	blame := right
	if !leftNumeric {
		blame = left
	}
	return value.NilValue(), fmt.Errorf("attempt to perform arithmetic on a %s value", rtmeta.TypeName(blame))
}

func (engine *Engine) callArithmeticMetamethod(thread *state.ThreadState, metamethodName string, left value.TValue, right value.TValue) (value.TValue, bool, error) {
	metamethod, ok, err := engine.valueMetamethod(left, metamethodName)
	if err != nil {
		return value.NilValue(), false, err
	}
	if !ok && left.Bits() != right.Bits() {
		metamethod, ok, err = engine.valueMetamethod(right, metamethodName)
		if err != nil {
			return value.NilValue(), false, err
		}
	}
	if !ok {
		return value.NilValue(), false, nil
	}
	if thread == nil {
		return value.NilValue(), false, fmt.Errorf("thread cannot be nil when calling %s", metamethodName)
	}
	results, err := engine.CallValueBoundary(thread, metamethod, []value.TValue{left, right}, 1)
	if err != nil {
		return value.NilValue(), false, err
	}
	if len(results) == 0 {
		return value.NilValue(), true, nil
	}
	return results[0], true, nil
}

func arithmeticMetamethodName(opcode bytecode.Opcode) (string, error) {
	switch opcode {
	case bytecode.OP_ADD:
		return metaAddName, nil
	case bytecode.OP_SUB:
		return metaSubName, nil
	case bytecode.OP_MUL:
		return metaMulName, nil
	case bytecode.OP_DIV:
		return metaDivName, nil
	case bytecode.OP_MOD:
		return metaModName, nil
	case bytecode.OP_POW:
		return metaPowName, nil
	case bytecode.OP_UNM:
		return metaUnmName, nil
	default:
		return "", fmt.Errorf("unsupported arithmetic opcode %s", opcode)
	}
}

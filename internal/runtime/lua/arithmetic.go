package lua

import (
	"fmt"
	"math"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/value"
)

func ArithmeticNumberResult(opcode bytecode.Opcode, left float64, right float64) (float64, error) {
	switch opcode {
	case bytecode.OP_ADD:
		return value.CanonicalizeNumber(left + right), nil
	case bytecode.OP_SUB:
		return value.CanonicalizeNumber(left - right), nil
	case bytecode.OP_MUL:
		return value.CanonicalizeNumber(left * right), nil
	case bytecode.OP_DIV:
		return value.CanonicalizeNumber(left / right), nil
	case bytecode.OP_MOD:
		return value.CanonicalizeNumber(left - math.Floor(left/right)*right), nil
	case bytecode.OP_POW:
		return value.CanonicalizeNumber(math.Pow(left, right)), nil
	case bytecode.OP_UNM:
		return value.CanonicalizeNumber(-left), nil
	default:
		return 0, fmt.Errorf("unsupported arithmetic opcode %s", opcode)
	}
}

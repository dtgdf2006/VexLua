package lua

import (
	"testing"

	"vexlua/internal/bytecode"
)

func TestArithmeticNumberResultMatchesLua51ModAndPow(t *testing.T) {
	tests := []struct {
		name   string
		opcode bytecode.Opcode
		left   float64
		right  float64
		want   float64
	}{
		{name: "mod-floor-positive-divisor", opcode: bytecode.OP_MOD, left: -5, right: 3, want: 1},
		{name: "mod-floor-negative-divisor", opcode: bytecode.OP_MOD, left: 5, right: -3, want: -1},
		{name: "pow-fractional-exponent", opcode: bytecode.OP_POW, left: 9, right: 0.5, want: 3},
		{name: "unm", opcode: bytecode.OP_UNM, left: 7, right: 0, want: -7},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ArithmeticNumberResult(test.opcode, test.left, test.right)
			if err != nil {
				t.Fatalf("ArithmeticNumberResult(%s): %v", test.opcode, err)
			}
			if got != test.want {
				t.Fatalf("ArithmeticNumberResult(%s, %v, %v) = %v, want %v", test.opcode, test.left, test.right, got, test.want)
			}
		})
	}
}

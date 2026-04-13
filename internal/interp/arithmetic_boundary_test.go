package interp

import (
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/value"
)

func TestArithmeticBoundaryUsesLua51CoercionAndRightOperandMetamethod(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}

	hexText, err := engine.InternString("0x10")
	if err != nil {
		t.Fatalf("intern hex text: %v", err)
	}
	threeText, err := engine.InternString("3")
	if err != nil {
		t.Fatalf("intern three text: %v", err)
	}
	twoText, err := engine.InternString("2")
	if err != nil {
		t.Fatalf("intern two text: %v", err)
	}

	coercionTests := []struct {
		name   string
		opcode bytecode.Opcode
		left   value.TValue
		right  value.TValue
		want   value.TValue
	}{
		{name: "add-hex-string", opcode: bytecode.OP_ADD, left: hexText.Value, right: value.NumberValue(2), want: value.NumberValue(18)},
		{name: "mul-decimal-strings", opcode: bytecode.OP_MUL, left: threeText.Value, right: twoText.Value, want: value.NumberValue(6)},
		{name: "pow", opcode: bytecode.OP_POW, left: value.NumberValue(9), right: value.NumberValue(0.5), want: value.NumberValue(3)},
		{name: "mod", opcode: bytecode.OP_MOD, left: value.NumberValue(-5), right: value.NumberValue(3), want: value.NumberValue(1)},
		{name: "unm-string", opcode: bytecode.OP_UNM, left: threeText.Value, right: threeText.Value, want: value.NumberValue(-3)},
	}

	for _, test := range coercionTests {
		t.Run(test.name, func(t *testing.T) {
			got, err := engine.ArithmeticBoundary(thread, test.opcode, test.left, test.right)
			if err != nil {
				t.Fatalf("ArithmeticBoundary(%s): %v", test.opcode, err)
			}
			if got.Bits() != test.want.Bits() {
				t.Fatalf("ArithmeticBoundary(%s) = %s, want %s", test.opcode, got, test.want)
			}
		})
	}

	addKey, err := engine.InternString(metaAddName)
	if err != nil {
		t.Fatalf("intern __add key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	addCalls := 0
	addMeta, err := engine.RegisterHostFunction("right-add-meta", func(value.TValue, value.TValue) float64 {
		addCalls++
		return 42
	}, env.Value)
	if err != nil {
		t.Fatalf("register __add host function: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, addKey.Value, addMeta.Value); err != nil {
		t.Fatalf("seed __add metamethod: %v", err)
	}
	rightTable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right table: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(rightTable.Value, metatable.Value); err != nil {
		t.Fatalf("set right metatable: %v", err)
	}
	result, err := engine.ArithmeticBoundary(thread, bytecode.OP_ADD, value.NumberValue(1), rightTable.Value)
	if err != nil {
		t.Fatalf("right-operand metamethod arithmetic: %v", err)
	}
	if result.Bits() != value.NumberValue(42).Bits() {
		t.Fatalf("right-operand metamethod result = %s, want 42", result)
	}
	if addCalls != 1 {
		t.Fatalf("right-operand __add call count = %d, want 1", addCalls)
	}
}

func TestArithmeticBoundaryUsesLua51ErrorBlame(t *testing.T) {
	engine := New()
	leftTable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left table: %v", err)
	}
	_, err = engine.ArithmeticBoundary(nil, bytecode.OP_ADD, value.NumberValue(1), value.BoolValue(true))
	if err == nil || err.Error() != "attempt to perform arithmetic on a boolean value" {
		t.Fatalf("right blame error = %v, want boolean arithmetic error", err)
	}
	_, err = engine.ArithmeticBoundary(nil, bytecode.OP_ADD, leftTable.Value, value.NumberValue(1))
	if err == nil || err.Error() != "attempt to perform arithmetic on a table value" {
		t.Fatalf("left blame error = %v, want table arithmetic error", err)
	}
}

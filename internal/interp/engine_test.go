package interp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/frontend/chunk"
	"vexlua/internal/runtime/feedback"
	"vexlua/internal/runtime/host"
	rtlua "vexlua/internal/runtime/lua"
	"vexlua/internal/runtime/value"
)

func TestInterpreterArithmeticChunkDiff(t *testing.T) {
	proto := &bytecode.Proto{
		Source:       "@arith.lua",
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(2),
			bytecode.NumberConstant(3),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABC(bytecode.OP_MUL, 1, 1, 2),
			bytecode.CreateABC(bytecode.OP_ADD, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 1, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 3, 0),
		},
	}
	assertProtoExecAndChunkDiff(t, proto, value.NilValue(), nil, -1, []value.TValue{value.NumberValue(7), value.BoolValue(true)})
}

func TestInterpreterArithmeticChunkDiffUsesRightOperandMetamethodAndCoercion(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	addKey, err := engine.InternString("__add")
	if err != nil {
		t.Fatalf("intern __add key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	addCalls := 0
	addMeta, err := engine.RegisterHostFunction("arith-right-chunk-meta", func(value.TValue, value.TValue) float64 {
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
	proto := &bytecode.Proto{
		Source:       "@arith-crafted-meta.lua",
		NumParams:    1,
		MaxStackSize: 5,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("0x10"),
			bytecode.NumberConstant(2),
			bytecode.NumberConstant(9),
			bytecode.NumberConstant(0.5),
			bytecode.StringConstant("3"),
			bytecode.NumberConstant(1),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 1),
			bytecode.CreateABC(bytecode.OP_ADD, 1, 1, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 3, 3),
			bytecode.CreateABC(bytecode.OP_POW, 2, 2, 3),
			bytecode.CreateABx(bytecode.OP_LOADK, 3, 4),
			bytecode.CreateABC(bytecode.OP_UNM, 3, 3, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 4, 5),
			bytecode.CreateABC(bytecode.OP_ADD, 0, 4, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 5, 0),
		},
	}
	assertProtoExecAndChunkDiffInEngine(t, engine, proto, env.Value, []value.TValue{rightTable.Value}, -1, []value.TValue{value.NumberValue(42), value.NumberValue(18), value.NumberValue(3), value.NumberValue(-3)})
	if addCalls != 2 {
		t.Fatalf("crafted arithmetic __add call count = %d, want 2", addCalls)
	}
}

func TestInterpreterTableChunkDiff(t *testing.T) {
	proto := &bytecode.Proto{
		Source:       "@table.lua",
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(42),
			bytecode.StringConstant("answer"),
			bytecode.NumberConstant(84),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_NEWTABLE, 0, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 1),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, 1, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 3),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, 1, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 0),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 2, 0, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 3, 0),
		},
	}
	assertProtoExecAndChunkDiff(t, proto, value.NilValue(), nil, -1, []value.TValue{value.NumberValue(42), value.NumberValue(84)})
}

func TestInterpreterClosureAndUpvalueCapture(t *testing.T) {
	child := &bytecode.Proto{
		Source:       "@adder-inner.lua",
		NumUpvalues:  1,
		NumParams:    1,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_ADD, 0, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	outer := &bytecode.Proto{
		Source:       "@adder-outer.lua",
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(40),
			bytecode.NumberConstant(2),
		},
		Protos: []*bytecode.Proto{child},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_CLOSURE, 1, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 1),
			bytecode.CreateABC(bytecode.OP_CALL, 1, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	assertProtoExecAndChunkDiff(t, outer, value.NilValue(), nil, -1, []value.TValue{value.NumberValue(42)})
}

func TestInterpreterSelfAndSetListChunkDiff(t *testing.T) {
	child := &bytecode.Proto{
		Source:       "@method.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(99),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	proto := &bytecode.Proto{
		Source:       "@self-setlist.lua",
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(11),
			bytecode.NumberConstant(22),
			bytecode.NumberConstant(33),
			bytecode.NumberConstant(2),
			bytecode.StringConstant("id"),
		},
		Protos: []*bytecode.Proto{child},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_NEWTABLE, 0, 0, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 3, 2),
			bytecode.CreateABC(bytecode.OP_SETLIST, 0, 3, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 3),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 0, 1),
			bytecode.CreateABx(bytecode.OP_CLOSURE, 2, 0),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, bytecode.RKAsk(4), 2),
			bytecode.CreateABC(bytecode.OP_SELF, 2, 0, bytecode.RKAsk(4)),
			bytecode.CreateABC(bytecode.OP_CALL, 2, 2, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 3, 0),
		},
	}
	assertProtoExecAndChunkDiff(t, proto, value.NilValue(), nil, -1, []value.TValue{value.NumberValue(22), value.NumberValue(99)})
}

func TestInterpreterNotLenConcatAndNumericForChunkDiff(t *testing.T) {
	proto := &bytecode.Proto{
		Source:       "@extended-opcodes.lua",
		MaxStackSize: 10,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("hello"),
			bytecode.NumberConstant(42),
			bytecode.StringConstant("he"),
			bytecode.StringConstant("llo"),
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(3),
			bytecode.NumberConstant(0),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_NEWTABLE, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, bytecode.RKAsk(0), bytecode.RKAsk(1)),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 3),
			bytecode.CreateABC(bytecode.OP_CONCAT, 1, 1, 2),
			bytecode.CreateABC(bytecode.OP_LEN, 2, 1, 0),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 3, 0, 1),
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 4, 0, 0),
			bytecode.CreateABC(bytecode.OP_NOT, 4, 4, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 5, 4),
			bytecode.CreateABx(bytecode.OP_LOADK, 6, 5),
			bytecode.CreateABx(bytecode.OP_LOADK, 7, 4),
			bytecode.CreateABx(bytecode.OP_LOADK, 9, 6),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 5, 1),
			bytecode.CreateABC(bytecode.OP_ADD, 9, 9, 8),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 5, -2),
			bytecode.CreateABC(bytecode.OP_MOVE, 5, 9, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 5, 0),
		},
	}
	assertProtoExecAndChunkDiff(t, proto, value.NilValue(), nil, -1, []value.TValue{value.NumberValue(5), value.NumberValue(42), value.BoolValue(true), value.NumberValue(6)})
}

func TestInterpreterNumericForChunkDiffSupportsDescendingStringStep(t *testing.T) {
	proto := &bytecode.Proto{
		Source:       "@descending-for.lua",
		MaxStackSize: 6,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("3"),
			bytecode.StringConstant("1"),
			bytecode.StringConstant("-1"),
			bytecode.NumberConstant(0),
			bytecode.NumberConstant(10),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 4, 3),
			bytecode.CreateABx(bytecode.OP_LOADK, 5, 4),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 0, 2),
			bytecode.CreateABC(bytecode.OP_MUL, 4, 4, 5),
			bytecode.CreateABC(bytecode.OP_ADD, 4, 4, 3),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 0, -3),
			bytecode.CreateABC(bytecode.OP_RETURN, 4, 2, 0),
		},
	}
	assertProtoExecAndChunkDiff(t, proto, value.NilValue(), nil, -1, []value.TValue{value.NumberValue(321)})
}

func TestInterpreterCompareMetamethodChunkDiff(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	eqKey, err := engine.InternString("__eq")
	if err != nil {
		t.Fatalf("intern __eq key: %v", err)
	}
	ltKey, err := engine.InternString("__lt")
	if err != nil {
		t.Fatalf("intern __lt key: %v", err)
	}
	left, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left table: %v", err)
	}
	right, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right table: %v", err)
	}
	leftMeta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left metatable: %v", err)
	}
	rightMeta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right metatable: %v", err)
	}
	eqCalls := 0
	ltCalls := 0
	eqMeta, err := engine.RegisterHostFunction("eq-chunk-meta", func(value.TValue, value.TValue) bool {
		eqCalls++
		return true
	}, env.Value)
	if err != nil {
		t.Fatalf("register __eq host function: %v", err)
	}
	ltMeta, err := engine.RegisterHostFunction("lt-chunk-meta", func(lhs value.TValue, rhs value.TValue) bool {
		ltCalls++
		return lhs.Bits() == left.Value.Bits() && rhs.Bits() == right.Value.Bits()
	}, env.Value)
	if err != nil {
		t.Fatalf("register __lt host function: %v", err)
	}
	for _, metatable := range []value.TValue{leftMeta.Value, rightMeta.Value} {
		metaRef, _ := metatable.HeapRef()
		if err := engine.Tables.Set(metaRef, eqKey.Value, eqMeta.Value); err != nil {
			t.Fatalf("seed __eq metamethod: %v", err)
		}
		if err := engine.Tables.Set(metaRef, ltKey.Value, ltMeta.Value); err != nil {
			t.Fatalf("seed __lt metamethod: %v", err)
		}
	}
	if err := engine.SetValueMetatableBoundary(left.Value, leftMeta.Value); err != nil {
		t.Fatalf("set left metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(right.Value, rightMeta.Value); err != nil {
		t.Fatalf("set right metatable: %v", err)
	}
	compareCases := []struct {
		name   string
		source string
		opcode bytecode.Opcode
		args   []value.TValue
		want   value.TValue
	}{
		{name: "eq", source: "@compare-crafted-eq.lua", opcode: bytecode.OP_EQ, args: []value.TValue{left.Value, right.Value}, want: value.BoolValue(true)},
		{name: "lt", source: "@compare-crafted-lt.lua", opcode: bytecode.OP_LT, args: []value.TValue{left.Value, right.Value}, want: value.BoolValue(true)},
		{name: "le", source: "@compare-crafted-le.lua", opcode: bytecode.OP_LE, args: []value.TValue{left.Value, right.Value}, want: value.BoolValue(true)},
		{name: "reverse-le", source: "@compare-crafted-reverse-le.lua", opcode: bytecode.OP_LE, args: []value.TValue{right.Value, left.Value}, want: value.BoolValue(false)},
	}
	for _, compareCase := range compareCases {
		proto := &bytecode.Proto{
			Source:       compareCase.source,
			NumParams:    2,
			MaxStackSize: 3,
			Code: []bytecode.Instruction{
				bytecode.CreateABC(bytecode.OP_LOADBOOL, 2, 0, 0),
				bytecode.CreateABC(compareCase.opcode, 1, 0, 1),
				bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
				bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
				bytecode.CreateABC(bytecode.OP_LOADBOOL, 2, 1, 0),
				bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
			},
		}
		assertProtoExecAndChunkDiffInEngine(t, engine, proto, env.Value, compareCase.args, -1, []value.TValue{compareCase.want})
	}
	if eqCalls != 2 {
		t.Fatalf("crafted compare __eq call count = %d, want 2", eqCalls)
	}
	if ltCalls != 6 {
		t.Fatalf("crafted compare __lt call count = %d, want 6", ltCalls)
	}
}

func TestInterpreterCompareStringChunkDiffHandlesEmbeddedNUL(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	left, err := engine.InternString("ab\x00c")
	if err != nil {
		t.Fatalf("intern left string: %v", err)
	}
	right, err := engine.InternString("ab\x00d")
	if err != nil {
		t.Fatalf("intern right string: %v", err)
	}
	equal, err := engine.InternString("ab\x00c")
	if err != nil {
		t.Fatalf("intern equal string: %v", err)
	}
	compareCases := []struct {
		name   string
		source string
		opcode bytecode.Opcode
		args   []value.TValue
		want   value.TValue
	}{
		{name: "lt", source: "@compare-string-nul-lt.lua", opcode: bytecode.OP_LT, args: []value.TValue{left.Value, right.Value}, want: value.BoolValue(true)},
		{name: "le", source: "@compare-string-nul-le.lua", opcode: bytecode.OP_LE, args: []value.TValue{left.Value, equal.Value}, want: value.BoolValue(true)},
		{name: "eq", source: "@compare-string-nul-eq.lua", opcode: bytecode.OP_EQ, args: []value.TValue{left.Value, equal.Value}, want: value.BoolValue(true)},
		{name: "reverse-lt", source: "@compare-string-nul-reverse-lt.lua", opcode: bytecode.OP_LT, args: []value.TValue{right.Value, left.Value}, want: value.BoolValue(false)},
	}
	for _, compareCase := range compareCases {
		proto := &bytecode.Proto{
			Source:       compareCase.source,
			NumParams:    2,
			MaxStackSize: 3,
			Code: []bytecode.Instruction{
				bytecode.CreateABC(bytecode.OP_LOADBOOL, 2, 0, 0),
				bytecode.CreateABC(compareCase.opcode, 1, 0, 1),
				bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
				bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
				bytecode.CreateABC(bytecode.OP_LOADBOOL, 2, 1, 0),
				bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
			},
		}
		assertProtoExecAndChunkDiffInEngine(t, engine, proto, env.Value, compareCase.args, -1, []value.TValue{compareCase.want})
	}
}

func TestInterpreterLenChunkDiffUsesHoleBoundaryAndTypeMetatable(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	lenKey, err := engine.InternString("__len")
	if err != nil {
		t.Fatalf("intern __len key: %v", err)
	}
	tableValue, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	for _, key := range []float64{1, 2, 4} {
		if err := engine.Tables.Set(tableValue.Ref, value.NumberValue(key), value.NumberValue(key*10)); err != nil {
			t.Fatalf("seed table key %v: %v", key, err)
		}
	}
	tableMetatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new table metatable: %v", err)
	}
	tableLenCalls := 0
	tableLenMeta, err := engine.RegisterHostFunction("table-len-chunk-meta", func(value.TValue) float64 {
		tableLenCalls++
		return 99
	}, env.Value)
	if err != nil {
		t.Fatalf("register table __len host function: %v", err)
	}
	if err := engine.Tables.Set(tableMetatable.Ref, lenKey.Value, tableLenMeta.Value); err != nil {
		t.Fatalf("seed table __len metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(tableValue.Value, tableMetatable.Value); err != nil {
		t.Fatalf("set table metatable: %v", err)
	}
	numberMetatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new number metatable: %v", err)
	}
	numberLenCalls := 0
	numberLenMeta, err := engine.RegisterHostFunction("number-len-chunk-meta", func(number float64) float64 {
		numberLenCalls++
		return number + 35
	}, env.Value)
	if err != nil {
		t.Fatalf("register number __len host function: %v", err)
	}
	if err := engine.Tables.Set(numberMetatable.Ref, lenKey.Value, numberLenMeta.Value); err != nil {
		t.Fatalf("seed number __len metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(value.NumberValue(7), numberMetatable.Value); err != nil {
		t.Fatalf("set number metatable: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@len-crafted.lua",
		NumParams:    2,
		MaxStackSize: 4,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_LEN, 2, 0, 0),
			bytecode.CreateABC(bytecode.OP_LEN, 3, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 3, 0),
		},
	}
	assertProtoExecAndChunkDiffInEngine(t, engine, proto, env.Value, []value.TValue{tableValue.Value, value.NumberValue(7)}, -1, []value.TValue{value.NumberValue(4), value.NumberValue(42)})
	if tableLenCalls != 0 {
		t.Fatalf("crafted len table __len call count = %d, want 0", tableLenCalls)
	}
	if numberLenCalls != 2 {
		t.Fatalf("crafted len number __len call count = %d, want 2", numberLenCalls)
	}
}

func TestInterpreterLenChunkDiffReturnsZeroForHashOnlyTable(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	tableValue, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	if err := engine.Tables.Set(tableValue.Ref, value.NumberValue(3), value.NumberValue(30)); err != nil {
		t.Fatalf("seed hash-only table: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@len-hash-only.lua",
		NumParams:    1,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_LEN, 1, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	assertProtoExecAndChunkDiffInEngine(t, engine, proto, env.Value, []value.TValue{tableValue.Value}, -1, []value.TValue{value.NumberValue(0)})
}

func TestInterpreterConcatChunkDiffBatchesRightFoldWithMetamethod(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	concatKey, err := engine.InternString("__concat")
	if err != nil {
		t.Fatalf("intern __concat key: %v", err)
	}
	labelKey, err := engine.InternString("label")
	if err != nil {
		t.Fatalf("intern label key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	concatCalls := 0
	concatMeta, err := engine.RegisterHostFunction("concat-chunk-meta", func(lhs any, rhs any) (value.TValue, error) {
		concatCalls++
		leftValue, err := host.FromHostValue(engine.Strings, lhs)
		if err != nil {
			return value.NilValue(), err
		}
		rightValue, err := host.FromHostValue(engine.Strings, rhs)
		if err != nil {
			return value.NilValue(), err
		}
		leftLabel, err := concatBoundaryLabel(engine, leftValue, labelKey.Value)
		if err != nil {
			return value.NilValue(), err
		}
		rightLabel, err := concatBoundaryLabel(engine, rightValue, labelKey.Value)
		if err != nil {
			return value.NilValue(), err
		}
		handle, err := engine.InternString(leftLabel + rightLabel)
		if err != nil {
			return value.NilValue(), err
		}
		return handle.Value, nil
	}, env.Value)
	if err != nil {
		t.Fatalf("register __concat host function: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, concatKey.Value, concatMeta.Value); err != nil {
		t.Fatalf("seed __concat metamethod: %v", err)
	}
	leftTable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left table: %v", err)
	}
	rightTable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right table: %v", err)
	}
	for handle, label := range map[value.HeapRef44]string{leftTable.Ref: "B", rightTable.Ref: "C"} {
		textHandle, err := engine.InternString(label)
		if err != nil {
			t.Fatalf("intern label %q: %v", label, err)
		}
		if err := engine.Tables.Set(handle, labelKey.Value, textHandle.Value); err != nil {
			t.Fatalf("seed label %q: %v", label, err)
		}
	}
	if err := engine.SetValueMetatableBoundary(leftTable.Value, metatable.Value); err != nil {
		t.Fatalf("set left metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(rightTable.Value, metatable.Value); err != nil {
		t.Fatalf("set right metatable: %v", err)
	}
	prefix, err := engine.InternString("A")
	if err != nil {
		t.Fatalf("intern prefix: %v", err)
	}
	suffix, err := engine.InternString("D")
	if err != nil {
		t.Fatalf("intern suffix: %v", err)
	}
	want, err := engine.InternString("ABCD")
	if err != nil {
		t.Fatalf("intern expected string: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@concat-crafted.lua",
		NumParams:    4,
		MaxStackSize: 4,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CONCAT, 0, 0, 3),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	assertProtoExecAndChunkDiffInEngine(t, engine, proto, env.Value, []value.TValue{prefix.Value, leftTable.Value, rightTable.Value, suffix.Value}, -1, []value.TValue{want.Value})
	if concatCalls != 4 {
		t.Fatalf("crafted concat __concat call count = %d, want 4", concatCalls)
	}
}

func TestInterpreterConcatChunkDiffBatchesStringNumberAroundMetamethod(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	concatKey, err := engine.InternString("__concat")
	if err != nil {
		t.Fatalf("intern __concat key: %v", err)
	}
	labelKey, err := engine.InternString("label")
	if err != nil {
		t.Fatalf("intern label key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	concatCalls := 0
	concatMeta, err := engine.RegisterHostFunction("concat-batch-chunk-meta", func(lhs any, rhs any) (value.TValue, error) {
		concatCalls++
		leftValue, err := host.FromHostValue(engine.Strings, lhs)
		if err != nil {
			return value.NilValue(), err
		}
		rightValue, err := host.FromHostValue(engine.Strings, rhs)
		if err != nil {
			return value.NilValue(), err
		}
		leftLabel, err := concatBoundaryLabel(engine, leftValue, labelKey.Value)
		if err != nil {
			return value.NilValue(), err
		}
		rightLabel, err := concatBoundaryLabel(engine, rightValue, labelKey.Value)
		if err != nil {
			return value.NilValue(), err
		}
		handle, err := engine.InternString(leftLabel + rightLabel)
		if err != nil {
			return value.NilValue(), err
		}
		return handle.Value, nil
	}, env.Value)
	if err != nil {
		t.Fatalf("register __concat host function: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, concatKey.Value, concatMeta.Value); err != nil {
		t.Fatalf("seed __concat metamethod: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box table: %v", err)
	}
	label, err := engine.InternString("B")
	if err != nil {
		t.Fatalf("intern label: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, labelKey.Value, label.Value); err != nil {
		t.Fatalf("seed label: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(box.Value, metatable.Value); err != nil {
		t.Fatalf("set box metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "box", box.Value); err != nil {
		t.Fatalf("set global box: %v", err)
	}
	want, err := engine.InternString("A10BZ")
	if err != nil {
		t.Fatalf("intern expected string: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@concat-batch-crafted.lua",
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("A"),
			bytecode.NumberConstant(10),
			bytecode.StringConstant("box"),
			bytecode.StringConstant("Z"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 2, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 3, 3),
			bytecode.CreateABC(bytecode.OP_CONCAT, 0, 0, 3),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	assertProtoExecAndChunkDiffInEngine(t, engine, proto, env.Value, nil, -1, []value.TValue{want.Value})
	if concatCalls != 2 {
		t.Fatalf("crafted concat batch __concat call count = %d, want 2", concatCalls)
	}
}

func TestInterpreterPhase3CrossFamilyChunkDiff(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	concatKey, err := engine.InternString("__concat")
	if err != nil {
		t.Fatalf("intern __concat key: %v", err)
	}
	labelKey, err := engine.InternString("label")
	if err != nil {
		t.Fatalf("intern label key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	concatCalls := 0
	concatMeta, err := engine.RegisterHostFunction("phase3-cross-concat-meta", func(lhs any, rhs any) (value.TValue, error) {
		concatCalls++
		leftValue, err := host.FromHostValue(engine.Strings, lhs)
		if err != nil {
			return value.NilValue(), err
		}
		rightValue, err := host.FromHostValue(engine.Strings, rhs)
		if err != nil {
			return value.NilValue(), err
		}
		leftLabel, err := concatChunkBoundaryLabel(engine, leftValue, labelKey.Value)
		if err != nil {
			return value.NilValue(), err
		}
		rightLabel, err := concatChunkBoundaryLabel(engine, rightValue, labelKey.Value)
		if err != nil {
			return value.NilValue(), err
		}
		handle, err := engine.InternString(leftLabel + rightLabel)
		if err != nil {
			return value.NilValue(), err
		}
		return handle.Value, nil
	}, env.Value)
	if err != nil {
		t.Fatalf("register __concat host function: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, concatKey.Value, concatMeta.Value); err != nil {
		t.Fatalf("seed __concat metamethod: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box table: %v", err)
	}
	label, err := engine.InternString("B")
	if err != nil {
		t.Fatalf("intern label: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, labelKey.Value, label.Value); err != nil {
		t.Fatalf("seed label: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(box.Value, metatable.Value); err != nil {
		t.Fatalf("set box metatable: %v", err)
	}
	sparse, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new sparse table: %v", err)
	}
	for _, key := range []float64{1, 2, 4} {
		if err := engine.Tables.Set(sparse.Ref, value.NumberValue(key), value.NumberValue(key)); err != nil {
			t.Fatalf("seed sparse key %v: %v", key, err)
		}
	}
	proto := &bytecode.Proto{
		Source:       "@phase3-cross-family-crafted.lua",
		NumParams:    2,
		MaxStackSize: 8,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("3"),
			bytecode.StringConstant("1"),
			bytecode.StringConstant("-1"),
			bytecode.NumberConstant(0),
			bytecode.NumberConstant(10),
			bytecode.StringConstant("A"),
			bytecode.StringConstant("AB400"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 3, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 4, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 6, 3),
			bytecode.CreateABx(bytecode.OP_LOADK, 7, 4),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 2, 2),
			bytecode.CreateABC(bytecode.OP_MUL, 6, 6, 7),
			bytecode.CreateABC(bytecode.OP_ADD, 6, 6, 5),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 2, -3),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 5),
			bytecode.CreateABC(bytecode.OP_MOVE, 3, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 4, 6, 0),
			bytecode.CreateABC(bytecode.OP_CONCAT, 2, 2, 4),
			bytecode.CreateABC(bytecode.OP_LEN, 3, 2, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 4, 6),
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 5, 0, 0),
			bytecode.CreateABC(bytecode.OP_LT, 0, 2, 4),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 5, 1, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 4, 5, 0),
			bytecode.CreateABC(bytecode.OP_LEN, 5, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 3, 5, 0),
		},
	}
	assertProtoExecAndChunkDiffInEngine(t, engine, proto, env.Value, []value.TValue{box.Value, sparse.Value}, -1, []value.TValue{value.NumberValue(5), value.BoolValue(true), value.NumberValue(4), value.NumberValue(321)})
	if concatCalls != 2 {
		t.Fatalf("cross-family __concat call count = %d, want 2", concatCalls)
	}
}

func TestInterpreterPhase3CrossFamilyErrorChunkDiff(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	concatKey, err := engine.InternString("__concat")
	if err != nil {
		t.Fatalf("intern __concat key: %v", err)
	}
	labelKey, err := engine.InternString("label")
	if err != nil {
		t.Fatalf("intern label key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	concatCalls := 0
	concatMeta, err := engine.RegisterHostFunction("phase3-cross-error-concat-meta", func(lhs any, rhs any) (value.TValue, error) {
		concatCalls++
		leftValue, err := host.FromHostValue(engine.Strings, lhs)
		if err != nil {
			return value.NilValue(), err
		}
		rightValue, err := host.FromHostValue(engine.Strings, rhs)
		if err != nil {
			return value.NilValue(), err
		}
		leftLabel, err := concatChunkBoundaryLabel(engine, leftValue, labelKey.Value)
		if err != nil {
			return value.NilValue(), err
		}
		rightLabel, err := concatChunkBoundaryLabel(engine, rightValue, labelKey.Value)
		if err != nil {
			return value.NilValue(), err
		}
		handle, err := engine.InternString(leftLabel + rightLabel)
		if err != nil {
			return value.NilValue(), err
		}
		return handle.Value, nil
	}, env.Value)
	if err != nil {
		t.Fatalf("register __concat host function: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, concatKey.Value, concatMeta.Value); err != nil {
		t.Fatalf("seed __concat metamethod: %v", err)
	}
	box, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new box table: %v", err)
	}
	label, err := engine.InternString("B")
	if err != nil {
		t.Fatalf("intern label: %v", err)
	}
	if err := engine.Tables.Set(box.Ref, labelKey.Value, label.Value); err != nil {
		t.Fatalf("seed label: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(box.Value, metatable.Value); err != nil {
		t.Fatalf("set box metatable: %v", err)
	}
	compareTarget, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new compare target table: %v", err)
	}
	concatCompareProto := &bytecode.Proto{
		Source:       "@phase3-cross-error-concat-compare-crafted.lua",
		NumParams:    2,
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("A"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 3, 0, 0),
			bytecode.CreateABC(bytecode.OP_CONCAT, 0, 2, 3),
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 2, 0, 0),
			bytecode.CreateABC(bytecode.OP_LT, 1, 0, 1),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
			bytecode.CreateABC(bytecode.OP_LOADBOOL, 2, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 2, 2, 0),
		},
	}
	assertProtoExecAndChunkDiffBothErrorInEngine(t, engine, concatCompareProto, env.Value, []value.TValue{box.Value, compareTarget.Value}, -1, "attempt to compare string with table")
	if concatCalls != 2 {
		t.Fatalf("cross-family error __concat call count = %d, want 2", concatCalls)
	}

	lenForPrepProto := &bytecode.Proto{
		Source:       "@phase3-cross-error-len-forprep-crafted.lua",
		NumParams:    1,
		MaxStackSize: 6,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_MOVE, 3, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_LEN, 1, 3, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 0),
			bytecode.CreateAsBx(bytecode.OP_FORPREP, 0, 1),
			bytecode.CreateABC(bytecode.OP_MOVE, 4, 3, 0),
			bytecode.CreateAsBx(bytecode.OP_FORLOOP, 0, -2),
			bytecode.CreateABC(bytecode.OP_RETURN, 4, 2, 0),
		},
	}
	assertProtoExecAndChunkDiffBothErrorInEngine(t, engine, lenForPrepProto, env.Value, []value.TValue{value.BoolValue(true)}, -1, "attempt to get length of a boolean value")
}

func TestInterpreterTForLoopWithHostIterator(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	iterator, err := engine.RegisterHostFunction("iter", func(state float64, control float64) (any, any) {
		if control >= 2 {
			return nil, nil
		}
		next := control + 1
		return next, next + 10
	}, env.Value)
	if err != nil {
		t.Fatalf("register iterator: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "iter", iterator.Value); err != nil {
		t.Fatalf("set global iter: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@tforloop.lua",
		MaxStackSize: 6,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("iter"),
			bytecode.NumberConstant(0),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 5, 1),
			bytecode.CreateABC(bytecode.OP_TFORLOOP, 0, 0, 2),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 5, 2, 0),
			bytecode.CreateABC(bytecode.OP_ADD, 5, 5, 4),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, -5),
		},
	}
	closure, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("create closure: %v", err)
	}
	results, err := engine.Call(thread, closure.Value, nil, -1)
	if err != nil {
		t.Fatalf("execute tforloop closure: %v", err)
	}
	assertResultsEqual(t, results, []value.TValue{value.NumberValue(23)})
}

func TestInterpreterTMCallSupportsTailcallAndTForLoop(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callMeta, err := engine.RegisterHostFunction("call-meta", func(_ value.TValue, x float64, y float64) float64 {
		return x + y
	}, env.Value)
	if err != nil {
		t.Fatalf("register __call host function: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create callable table: %v", err)
	}
	callableMetatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create callable metatable: %v", err)
	}
	if err := engine.Tables.Set(callableMetatable.Ref, callKey.Value, callMeta.Value); err != nil {
		t.Fatalf("seed callable __call: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, callableMetatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "callable", callable.Value); err != nil {
		t.Fatalf("set global callable: %v", err)
	}
	tailProto := &bytecode.Proto{
		Source:       "@tmcall-tail.lua",
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("callable"),
			bytecode.NumberConstant(10),
			bytecode.NumberConstant(32),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 3, 0),
		},
	}
	tailClosure, err := engine.NewClosure(tailProto, env.Value, nil)
	if err != nil {
		t.Fatalf("create tm_call tail closure: %v", err)
	}
	results, err := engine.Call(thread, tailClosure.Value, nil, -1)
	if err != nil {
		t.Fatalf("run tm_call tail closure: %v", err)
	}
	assertResultsEqual(t, results, []value.TValue{value.NumberValue(42)})

	iteratorMeta, err := engine.RegisterHostFunction("iter-meta", func(_ value.TValue, state float64, control float64) (any, any) {
		if control >= state {
			return nil, nil
		}
		next := control + 1
		return next, next + 10
	}, env.Value)
	if err != nil {
		t.Fatalf("register iterator __call host function: %v", err)
	}
	iterator, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create iterator table: %v", err)
	}
	iteratorMetatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create iterator metatable: %v", err)
	}
	if err := engine.Tables.Set(iteratorMetatable.Ref, callKey.Value, iteratorMeta.Value); err != nil {
		t.Fatalf("seed iterator __call: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(iterator.Value, iteratorMetatable.Value); err != nil {
		t.Fatalf("set iterator metatable: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "iter", iterator.Value); err != nil {
		t.Fatalf("set global iter: %v", err)
	}
	tforProto := &bytecode.Proto{
		Source:       "@tmcall-tforloop.lua",
		MaxStackSize: 6,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("iter"),
			bytecode.NumberConstant(2),
			bytecode.NumberConstant(0),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABx(bytecode.OP_LOADK, 5, 2),
			bytecode.CreateABC(bytecode.OP_TFORLOOP, 0, 0, 2),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 5, 2, 0),
			bytecode.CreateABC(bytecode.OP_ADD, 5, 5, 4),
			bytecode.CreateAsBx(bytecode.OP_JMP, 0, -5),
		},
	}
	tforClosure, err := engine.NewClosure(tforProto, env.Value, nil)
	if err != nil {
		t.Fatalf("create tm_call tfor closure: %v", err)
	}
	results, err = engine.Call(thread, tforClosure.Value, nil, -1)
	if err != nil {
		t.Fatalf("run tm_call tfor closure: %v", err)
	}
	assertResultsEqual(t, results, []value.TValue{value.NumberValue(23)})
}

func TestInterpreterVarargTailcallAndProtectedCall(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env table: %v", err)
	}

	sumProto := &bytecode.Proto{
		Source:       "@sum.lua",
		NumParams:    2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_ADD, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	sumClosure, err := engine.NewClosure(sumProto, env.Value, nil)
	if err != nil {
		t.Fatalf("create sum closure: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "sum", sumClosure.Value); err != nil {
		t.Fatalf("set global sum: %v", err)
	}

	varargProto := &bytecode.Proto{
		Source:       "@vararg.lua",
		IsVararg:     1,
		MaxStackSize: 4,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_VARARG, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 0, 0),
		},
	}
	varargClosure, err := engine.NewClosure(varargProto, env.Value, nil)
	if err != nil {
		t.Fatalf("create vararg closure: %v", err)
	}
	results, err := engine.Call(thread, varargClosure.Value, []value.TValue{value.NumberValue(1), value.NumberValue(2), value.NumberValue(3)}, -1)
	if err != nil {
		t.Fatalf("run vararg closure: %v", err)
	}
	assertResultsEqual(t, results, []value.TValue{value.NumberValue(1), value.NumberValue(2), value.NumberValue(3)})

	tailProto := &bytecode.Proto{
		Source:       "@tail.lua",
		NumParams:    2,
		MaxStackSize: 5,
		Constants:    []bytecode.Constant{bytecode.StringConstant("sum")},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 2, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 3, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 4, 1, 0),
			bytecode.CreateABC(bytecode.OP_TAILCALL, 2, 3, 0),
		},
	}
	tailClosure, err := engine.NewClosure(tailProto, env.Value, nil)
	if err != nil {
		t.Fatalf("create tail closure: %v", err)
	}
	results, err = engine.Call(thread, tailClosure.Value, []value.TValue{value.NumberValue(10), value.NumberValue(32)}, -1)
	if err != nil {
		t.Fatalf("run tail closure: %v", err)
	}
	assertResultsEqual(t, results, []value.TValue{value.NumberValue(42)})

	badProto := &bytecode.Proto{
		Source:       "@bad.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(2),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 0, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	badClosure, err := engine.NewClosure(badProto, env.Value, nil)
	if err != nil {
		t.Fatalf("create bad closure: %v", err)
	}
	outcome := engine.ProtectedCall(thread, badClosure.Value, nil, -1)
	if outcome.Status != StatusError || outcome.Err == nil {
		t.Fatalf("expected protected call failure, got status=%d err=%v", outcome.Status, outcome.Err)
	}
	results, err = engine.Call(thread, sumClosure.Value, []value.TValue{value.NumberValue(20), value.NumberValue(22)}, -1)
	if err != nil {
		t.Fatalf("protected call should leave thread reusable: %v", err)
	}
	assertResultsEqual(t, results, []value.TValue{value.NumberValue(42)})
}

func TestInterpreterFailedCallDoesNotInvalidateWarmCallFeedback(t *testing.T) {
	tests := []struct {
		name         string
		proto        *bytecode.Proto
		wantAccess   feedback.AccessKind
		usesIterator bool
	}{
		{
			name: "call",
			proto: &bytecode.Proto{
				Source:       "@failed-call-feedback-call.lua",
				NumParams:    1,
				MaxStackSize: 1,
				Code: []bytecode.Instruction{
					bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 2),
					bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
				},
			},
			wantAccess: feedback.AccessCallLuaClosure,
		},
		{
			name: "tailcall",
			proto: &bytecode.Proto{
				Source:       "@failed-call-feedback-tail.lua",
				NumParams:    1,
				MaxStackSize: 1,
				Code: []bytecode.Instruction{
					bytecode.CreateABC(bytecode.OP_TAILCALL, 0, 1, 0),
				},
			},
			wantAccess: feedback.AccessCallLuaClosure,
		},
		{
			name: "tforloop",
			proto: &bytecode.Proto{
				Source:       "@failed-call-feedback-tfor.lua",
				NumParams:    3,
				MaxStackSize: 5,
				Code: []bytecode.Instruction{
					bytecode.CreateABC(bytecode.OP_TFORLOOP, 0, 0, 2),
					bytecode.CreateAsBx(bytecode.OP_JMP, 0, 0),
					bytecode.CreateABC(bytecode.OP_RETURN, 3, 2, 0),
				},
			},
			wantAccess:   feedback.AccessCallHostFunction,
			usesIterator: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := New()
			thread, err := engine.NewThread(0, 0)
			if err != nil {
				t.Fatalf("create thread: %v", err)
			}
			env, err := engine.NewTable(0, 0)
			if err != nil {
				t.Fatalf("create env: %v", err)
			}
			directCallee := newConstantLuaClosure(t, engine, env.Value, "@failed-call-feedback-direct.lua", 42)
			iterator, err := engine.RegisterHostFunction("iter", func(float64, float64) float64 {
				return 7
			}, env.Value)
			if err != nil {
				t.Fatalf("register iterator: %v", err)
			}
			closure, err := engine.NewClosure(test.proto, env.Value, nil)
			if err != nil {
				t.Fatalf("create closure: %v", err)
			}
			if _, err := engine.Closures.EnsureFeedbackVector(closure.Ref, feedback.LayoutForProto(test.proto)); err != nil {
				t.Fatalf("ensure feedback vector: %v", err)
			}

			warmArgs := []value.TValue{directCallee.Value}
			failArgs := []value.TValue{value.NumberValue(7)}
			if test.usesIterator {
				warmArgs = []value.TValue{iterator.Value, value.NumberValue(1), value.NumberValue(0)}
				failArgs = []value.TValue{value.NumberValue(7), value.NumberValue(1), value.NumberValue(0)}
			}

			if _, err := engine.Call(thread, closure.Value, warmArgs, -1); err != nil {
				t.Fatalf("warm call: %v", err)
			}
			warmed := mustInterpFeedbackCell(t, engine, closure.Ref, 0)
			if warmed.State != feedback.StateMonomorphic || warmed.AccessKind != test.wantAccess {
				t.Fatalf("warm feedback cell = %+v, want monomorphic access %d", warmed, test.wantAccess)
			}

			outcome := engine.ProtectedCall(thread, closure.Value, failArgs, -1)
			if outcome.Status != StatusError || outcome.Err == nil {
				t.Fatalf("expected protected call failure, got status=%d err=%v", outcome.Status, outcome.Err)
			}
			after := mustInterpFeedbackCell(t, engine, closure.Ref, 0)
			if after != warmed {
				t.Fatalf("failed call should not invalidate warm feedback: got %+v want %+v", after, warmed)
			}
		})
	}
}

func concatChunkBoundaryLabel(engine *Engine, candidate value.TValue, labelKey value.TValue) (string, error) {
	if text, ok, err := rtlua.ToStringText(candidate, func(ref value.HeapRef44) (string, error) {
		return engine.Strings.Text(ref)
	}); err != nil {
		return "", err
	} else if ok {
		return text, nil
	}
	if candidate.IsBoxedTag(value.TagTableRef) {
		ref, _ := candidate.HeapRef()
		labelValue, found, err := engine.Tables.Get(ref, labelKey)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fmt.Errorf("missing label field")
		}
		labelRef, _ := labelValue.HeapRef()
		return engine.Strings.Text(labelRef)
	}
	return "", fmt.Errorf("unexpected concat operand %s", candidate)
}

func assertProtoExecAndChunkDiff(t *testing.T, proto *bytecode.Proto, env value.TValue, args []value.TValue, nresults int, expected []value.TValue) {
	t.Helper()
	engine := New()
	if env.IsBoxedTag(value.TagNil) {
		tableHandle, err := engine.NewTable(0, 0)
		if err != nil {
			t.Fatalf("create env: %v", err)
		}
		env = tableHandle.Value
	}
	assertProtoExecAndChunkDiffInEngine(t, engine, proto, env, args, nresults, expected)
}

func assertProtoExecAndChunkDiffBothError(t *testing.T, proto *bytecode.Proto, env value.TValue, args []value.TValue, nresults int, wantErr string) {
	t.Helper()
	engine := New()
	if env.IsBoxedTag(value.TagNil) {
		tableHandle, err := engine.NewTable(0, 0)
		if err != nil {
			t.Fatalf("create env: %v", err)
		}
		env = tableHandle.Value
	}
	assertProtoExecAndChunkDiffBothErrorInEngine(t, engine, proto, env, args, nresults, wantErr)
}

func assertProtoExecAndChunkDiffInEngine(t *testing.T, engine *Engine, proto *bytecode.Proto, env value.TValue, args []value.TValue, nresults int, expected []value.TValue) {
	t.Helper()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	directClosure, err := engine.NewClosure(proto, env, nil)
	if err != nil {
		t.Fatalf("create direct closure: %v", err)
	}
	directResults, err := engine.Call(thread, directClosure.Value, args, nresults)
	if err != nil {
		t.Fatalf("execute direct proto: %v", err)
	}
	assertResultsEqual(t, directResults, expected)

	loadedProto, err := chunk.Load(proto.Source, encodeChunk(t, proto))
	if err != nil {
		t.Fatalf("load encoded chunk: %v", err)
	}
	loadedClosure, err := engine.NewClosure(loadedProto, env, nil)
	if err != nil {
		t.Fatalf("create loaded closure: %v", err)
	}
	loadedResults, err := engine.Call(thread, loadedClosure.Value, args, nresults)
	if err != nil {
		t.Fatalf("execute loaded proto: %v", err)
	}
	assertResultsEqual(t, loadedResults, directResults)
}

func assertProtoExecAndChunkDiffBothErrorInEngine(t *testing.T, engine *Engine, proto *bytecode.Proto, env value.TValue, args []value.TValue, nresults int, wantErr string) {
	t.Helper()
	directThread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create direct thread: %v", err)
	}
	directClosure, err := engine.NewClosure(proto, env, nil)
	if err != nil {
		t.Fatalf("create direct closure: %v", err)
	}
	if _, err := engine.Call(directThread, directClosure.Value, args, nresults); err == nil || normalizeChunkErrorText(err.Error()) != wantErr {
		t.Fatalf("execute direct proto: got %v want %s", err, wantErr)
	}

	loadedProto, err := chunk.Load(proto.Source, encodeChunk(t, proto))
	if err != nil {
		t.Fatalf("load encoded chunk: %v", err)
	}
	loadedThread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create loaded thread: %v", err)
	}
	loadedClosure, err := engine.NewClosure(loadedProto, env, nil)
	if err != nil {
		t.Fatalf("create loaded closure: %v", err)
	}
	if _, err := engine.Call(loadedThread, loadedClosure.Value, args, nresults); err == nil || normalizeChunkErrorText(err.Error()) != wantErr {
		t.Fatalf("execute loaded proto: got %v want %s", err, wantErr)
	}
}

func normalizeChunkErrorText(text string) string {
	trimmed := strings.TrimSpace(text)
	if index := strings.LastIndex(trimmed, ": "); index >= 0 {
		return trimmed[index+2:]
	}
	return trimmed
}

func assertResultsEqual(t *testing.T, got []value.TValue, want []value.TValue) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("result count mismatch: got %d want %d", len(got), len(want))
	}
	for index := range want {
		if !tvaluesEqual(got[index], want[index]) {
			t.Fatalf("result %d mismatch: got %s want %s", index, got[index], want[index])
		}
	}
}

func mustInterpFeedbackCell(t *testing.T, engine *Engine, closureRef value.HeapRef44, slot uint32) feedback.Cell {
	t.Helper()
	cell, err := engine.Closures.ReadFeedbackCell(closureRef, slot)
	if err != nil {
		t.Fatalf("read feedback cell %d: %v", slot, err)
	}
	return cell
}

func tvaluesEqual(left value.TValue, right value.TValue) bool {
	if left.IsNumber() && right.IsNumber() {
		leftNumber, _ := left.Float64()
		rightNumber, _ := right.Float64()
		return leftNumber == rightNumber || (math.IsNaN(leftNumber) && math.IsNaN(rightNumber))
	}
	return left.Bits() == right.Bits()
}

func encodeChunk(t *testing.T, proto *bytecode.Proto) []byte {
	t.Helper()
	if err := bytecode.ValidateProto(proto); err != nil {
		t.Fatalf("proto is invalid: %v", err)
	}
	var buffer bytes.Buffer
	header := chunk.DefaultFormat().HeaderBytes()
	buffer.Write(header[:])
	writeProto(t, &buffer, proto)
	return buffer.Bytes()
}

func writeProto(t *testing.T, buffer *bytes.Buffer, proto *bytecode.Proto) {
	t.Helper()
	writeString(buffer, proto.Source)
	writeInt32(buffer, int32(proto.LineDefined))
	writeInt32(buffer, int32(proto.LastLineDef))
	buffer.WriteByte(proto.NumUpvalues)
	buffer.WriteByte(proto.NumParams)
	buffer.WriteByte(proto.IsVararg)
	buffer.WriteByte(proto.MaxStackSize)

	writeInt32(buffer, int32(len(proto.Code)))
	for _, instruction := range proto.Code {
		writeUint32(buffer, uint32(instruction))
	}

	writeInt32(buffer, int32(len(proto.Constants)))
	for _, constant := range proto.Constants {
		switch constant.Kind {
		case bytecode.ConstantNil:
			buffer.WriteByte(0)
		case bytecode.ConstantBoolean:
			buffer.WriteByte(1)
			if constant.Boolean {
				buffer.WriteByte(1)
			} else {
				buffer.WriteByte(0)
			}
		case bytecode.ConstantNumber:
			buffer.WriteByte(3)
			writeFloat64(buffer, constant.Number)
		case bytecode.ConstantString:
			buffer.WriteByte(4)
			writeString(buffer, constant.Text)
		default:
			t.Fatalf("unsupported constant kind %s", constant.Kind)
		}
	}

	writeInt32(buffer, int32(len(proto.Protos)))
	for _, child := range proto.Protos {
		writeProto(t, buffer, child)
	}

	writeInt32(buffer, int32(len(proto.LineInfo)))
	for _, line := range proto.LineInfo {
		writeInt32(buffer, int32(line))
	}
	writeInt32(buffer, int32(len(proto.LocVars)))
	for _, local := range proto.LocVars {
		writeString(buffer, local.Name)
		writeInt32(buffer, int32(local.StartPC))
		writeInt32(buffer, int32(local.EndPC))
	}
	writeInt32(buffer, int32(len(proto.UpvalueNames)))
	for _, name := range proto.UpvalueNames {
		writeString(buffer, name)
	}
}

func writeString(buffer *bytes.Buffer, text string) {
	if text == "" {
		writeUint64(buffer, 0)
		return
	}
	writeUint64(buffer, uint64(len(text)+1))
	buffer.WriteString(text)
	buffer.WriteByte(0)
}

func writeInt32(buffer *bytes.Buffer, value int32) {
	if err := binary.Write(buffer, binary.LittleEndian, value); err != nil {
		panic(err)
	}
}

func writeUint32(buffer *bytes.Buffer, value uint32) {
	if err := binary.Write(buffer, binary.LittleEndian, value); err != nil {
		panic(err)
	}
}

func writeUint64(buffer *bytes.Buffer, value uint64) {
	if err := binary.Write(buffer, binary.LittleEndian, value); err != nil {
		panic(err)
	}
}

func writeFloat64(buffer *bytes.Buffer, value float64) {
	if err := binary.Write(buffer, binary.LittleEndian, value); err != nil {
		panic(err)
	}
}

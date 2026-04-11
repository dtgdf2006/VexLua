package interp

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/frontend/chunk"
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

func assertProtoExecAndChunkDiff(t *testing.T, proto *bytecode.Proto, env value.TValue, args []value.TValue, nresults int, expected []value.TValue) {
	t.Helper()
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	if env.IsBoxedTag(value.TagNil) {
		tableHandle, err := engine.NewTable(0, 0)
		if err != nil {
			t.Fatalf("create env: %v", err)
		}
		env = tableHandle.Value
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

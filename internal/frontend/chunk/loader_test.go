package chunk_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/frontend/chunk"
)

type encodedConstant struct {
	typeTag byte
	boolVal bool
	numVal  float64
	strVal  string
}

type encodedLocVar struct {
	name    string
	startPC int32
	endPC   int32
}

type encodedProto struct {
	source       *string
	lineDefined  int32
	lastLineDef  int32
	numUpvalues  byte
	numParams    byte
	isVararg     byte
	maxStackSize byte
	code         []bytecode.Instruction
	constants    []encodedConstant
	protos       []*encodedProto
	lineInfo     []int32
	locVars      []encodedLocVar
	upvalueNames []string
}

func TestLoadValidChunk(t *testing.T) {
	mainSource := "main.lua"
	child := &encodedProto{
		source:       nil,
		lineDefined:  2,
		lastLineDef:  2,
		numUpvalues:  0,
		numParams:    0,
		isVararg:     0,
		maxStackSize: 2,
		code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	main := &encodedProto{
		source:       &mainSource,
		lineDefined:  0,
		lastLineDef:  0,
		numUpvalues:  0,
		numParams:    0,
		isVararg:     2,
		maxStackSize: 3,
		code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 1),
			bytecode.CreateABx(bytecode.OP_CLOSURE, 1, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
		constants: []encodedConstant{
			{typeTag: 4, strVal: "global_name"},
			{typeTag: 3, numVal: 3.5},
			{typeTag: 1, boolVal: true},
			{typeTag: 0},
		},
		protos:   []*encodedProto{child},
		lineInfo: []int32{1, 2, 3},
		locVars: []encodedLocVar{
			{name: "a", startPC: 0, endPC: 2},
		},
	}

	data := encodeChunk(t, main)
	proto, err := chunk.Load("@valid", data)
	if err != nil {
		t.Fatalf("expected chunk load success, got: %v", err)
	}
	if proto.Source != mainSource {
		t.Fatalf("unexpected source: %q", proto.Source)
	}
	if len(proto.Constants) != 4 || proto.Constants[1].Kind != bytecode.ConstantNumber {
		t.Fatalf("unexpected constants: %+v", proto.Constants)
	}
	if len(proto.Protos) != 1 {
		t.Fatalf("unexpected child proto count: %d", len(proto.Protos))
	}
	if proto.Protos[0].Source != mainSource {
		t.Fatalf("expected child source fallback to parent, got %q", proto.Protos[0].Source)
	}
	if got := proto.NewClosureTemplate().UpvalueCount; got != 0 {
		t.Fatalf("unexpected closure template upvalue count: %d", got)
	}
	if proto.Code[0].Opcode() != bytecode.OP_LOADK {
		t.Fatalf("unexpected first opcode: %s", proto.Code[0].Opcode())
	}
}

func TestLoadRejectsBadHeader(t *testing.T) {
	mainSource := "header.lua"
	proto := &encodedProto{
		source:       &mainSource,
		maxStackSize: 2,
		code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	data := encodeChunk(t, proto)
	data[0] = '!'

	_, err := chunk.Load("@bad_header", data)
	if err == nil || !strings.Contains(err.Error(), "bad header") {
		t.Fatalf("expected bad header error, got: %v", err)
	}
}

func TestLoadRejectsBadConstantType(t *testing.T) {
	mainSource := "badconst.lua"
	proto := &encodedProto{
		source:       &mainSource,
		maxStackSize: 2,
		code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
		constants: []encodedConstant{{typeTag: 99}},
	}

	_, err := chunk.Load("@bad_const", encodeChunk(t, proto))
	if err == nil || !strings.Contains(err.Error(), "bad constant") {
		t.Fatalf("expected bad constant error, got: %v", err)
	}
}

func TestLoadRejectsBadCode(t *testing.T) {
	mainSource := "badcode.lua"
	proto := &encodedProto{
		source:       &mainSource,
		maxStackSize: 2,
		code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 3),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
		constants: []encodedConstant{{typeTag: 3, numVal: 1}},
	}

	_, err := chunk.Load("@bad_code", encodeChunk(t, proto))
	if err == nil || !strings.Contains(err.Error(), "bad code") {
		t.Fatalf("expected bad code error, got: %v", err)
	}
}

func encodeChunk(t *testing.T, proto *encodedProto) []byte {
	t.Helper()
	var buffer bytes.Buffer
	header := chunk.ExpectedHeaderBytes()
	buffer.Write(header[:])
	writeProto(t, &buffer, proto)
	return buffer.Bytes()
}

func writeProto(t *testing.T, buffer *bytes.Buffer, proto *encodedProto) {
	t.Helper()
	writeString(buffer, proto.source)
	writeInt32(buffer, proto.lineDefined)
	writeInt32(buffer, proto.lastLineDef)
	buffer.WriteByte(proto.numUpvalues)
	buffer.WriteByte(proto.numParams)
	buffer.WriteByte(proto.isVararg)
	buffer.WriteByte(proto.maxStackSize)

	writeInt32(buffer, int32(len(proto.code)))
	for _, inst := range proto.code {
		writeUint32(buffer, uint32(inst))
	}

	writeInt32(buffer, int32(len(proto.constants)))
	for _, constant := range proto.constants {
		buffer.WriteByte(constant.typeTag)
		switch constant.typeTag {
		case 1:
			if constant.boolVal {
				buffer.WriteByte(1)
			} else {
				buffer.WriteByte(0)
			}
		case 3:
			writeFloat64(buffer, constant.numVal)
		case 4:
			value := constant.strVal
			writeString(buffer, &value)
		}
	}

	writeInt32(buffer, int32(len(proto.protos)))
	for _, child := range proto.protos {
		writeProto(t, buffer, child)
	}

	writeInt32(buffer, int32(len(proto.lineInfo)))
	for _, line := range proto.lineInfo {
		writeInt32(buffer, line)
	}

	writeInt32(buffer, int32(len(proto.locVars)))
	for _, locVar := range proto.locVars {
		name := locVar.name
		writeString(buffer, &name)
		writeInt32(buffer, locVar.startPC)
		writeInt32(buffer, locVar.endPC)
	}

	writeInt32(buffer, int32(len(proto.upvalueNames)))
	for _, upvalueName := range proto.upvalueNames {
		name := upvalueName
		writeString(buffer, &name)
	}
}

func writeString(buffer *bytes.Buffer, value *string) {
	if value == nil {
		writeUint64(buffer, 0)
		return
	}
	bytesValue := append([]byte(*value), 0)
	writeUint64(buffer, uint64(len(bytesValue)))
	buffer.Write(bytesValue)
}

func writeInt32(buffer *bytes.Buffer, value int32) {
	_ = binary.Write(buffer, binary.LittleEndian, value)
}

func writeUint32(buffer *bytes.Buffer, value uint32) {
	_ = binary.Write(buffer, binary.LittleEndian, value)
}

func writeUint64(buffer *bytes.Buffer, value uint64) {
	_ = binary.Write(buffer, binary.LittleEndian, value)
}

func writeFloat64(buffer *bytes.Buffer, value float64) {
	_ = binary.Write(buffer, binary.LittleEndian, value)
}

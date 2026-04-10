package chunk

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"vexlua/internal/bytecode"
)

const (
	luaTypeNil     = 0
	luaTypeBoolean = 1
	luaTypeNumber  = 3
	luaTypeString  = 4

	maxProtoDepth = 200
)

type loader struct {
	reader     io.Reader
	chunkName  string
	format     BinaryFormat
	protoDepth int
}

func Load(name string, data []byte) (*bytecode.Proto, error) {
	return LoadReader(name, bytes.NewReader(data))
}

func LoadReader(name string, reader io.Reader) (*bytecode.Proto, error) {
	ld := &loader{
		reader:    reader,
		chunkName: normalizeChunkName(name),
		format:    DefaultFormat(),
	}
	if err := ld.loadHeader(); err != nil {
		return nil, err
	}
	return ld.loadFunction("=?")
}

func normalizeChunkName(name string) string {
	if name == "" {
		return "binary string"
	}
	if name[0] == '@' || name[0] == '=' {
		return name[1:]
	}
	if name[0] == Signature[0] {
		return "binary string"
	}
	return name
}

func (ld *loader) chunkError(reason string) error {
	return fmt.Errorf("%s: %s in precompiled chunk", ld.chunkName, reason)
}

func (ld *loader) loadHeader() error {
	var header [HeaderSize]byte
	if _, err := io.ReadFull(ld.reader, header[:]); err != nil {
		return ld.chunkError("unexpected end")
	}
	if err := ValidateHeaderBytes(header[:]); err != nil {
		return ld.chunkError(err.Error())
	}
	return nil
}

func (ld *loader) loadFunction(parentSource string) (*bytecode.Proto, error) {
	ld.protoDepth++
	defer func() { ld.protoDepth-- }()
	if ld.protoDepth > maxProtoDepth {
		return nil, ld.chunkError("code too deep")
	}

	source, isNil, err := ld.loadString()
	if err != nil {
		return nil, err
	}
	if isNil {
		source = parentSource
	}

	lineDefined, err := ld.readInt("linedefined")
	if err != nil {
		return nil, err
	}
	lastLineDefined, err := ld.readInt("lastlinedefined")
	if err != nil {
		return nil, err
	}
	numUpvalues, err := ld.readByte()
	if err != nil {
		return nil, err
	}
	numParams, err := ld.readByte()
	if err != nil {
		return nil, err
	}
	isVararg, err := ld.readByte()
	if err != nil {
		return nil, err
	}
	maxStackSize, err := ld.readByte()
	if err != nil {
		return nil, err
	}

	code, err := ld.loadCode()
	if err != nil {
		return nil, err
	}
	constants, protos, err := ld.loadConstantsAndProtos(source)
	if err != nil {
		return nil, err
	}
	lineInfo, locVars, upvalueNames, err := ld.loadDebug()
	if err != nil {
		return nil, err
	}

	proto := &bytecode.Proto{
		Source:       source,
		LineDefined:  lineDefined,
		LastLineDef:  lastLineDefined,
		NumUpvalues:  numUpvalues,
		NumParams:    numParams,
		IsVararg:     isVararg,
		MaxStackSize: maxStackSize,
		Code:         code,
		Constants:    constants,
		Protos:       protos,
		LineInfo:     lineInfo,
		LocVars:      locVars,
		UpvalueNames: upvalueNames,
	}

	if err := bytecode.ValidateProto(proto); err != nil {
		return nil, ld.chunkError("bad code: " + err.Error())
	}

	return proto, nil
}

func (ld *loader) loadCode() ([]bytecode.Instruction, error) {
	count, err := ld.readInt("code size")
	if err != nil {
		return nil, err
	}
	code := make([]bytecode.Instruction, count)
	for i := range code {
		word, err := ld.readUint32()
		if err != nil {
			return nil, err
		}
		code[i] = bytecode.Instruction(word)
	}
	return code, nil
}

func (ld *loader) loadConstantsAndProtos(source string) ([]bytecode.Constant, []*bytecode.Proto, error) {
	count, err := ld.readInt("constant count")
	if err != nil {
		return nil, nil, err
	}
	constants := make([]bytecode.Constant, 0, count)
	for i := 0; i < count; i++ {
		typeTag, err := ld.readByte()
		if err != nil {
			return nil, nil, err
		}
		constant, err := ld.loadConstant(typeTag)
		if err != nil {
			return nil, nil, err
		}
		constants = append(constants, constant)
	}

	protoCount, err := ld.readInt("child proto count")
	if err != nil {
		return nil, nil, err
	}
	protos := make([]*bytecode.Proto, 0, protoCount)
	for i := 0; i < protoCount; i++ {
		proto, err := ld.loadFunction(source)
		if err != nil {
			return nil, nil, err
		}
		protos = append(protos, proto)
	}

	return constants, protos, nil
}

func (ld *loader) loadConstant(typeTag byte) (bytecode.Constant, error) {
	switch typeTag {
	case luaTypeNil:
		return bytecode.NilConstant(), nil
	case luaTypeBoolean:
		value, err := ld.readByte()
		if err != nil {
			return bytecode.Constant{}, err
		}
		return bytecode.BooleanConstant(value != 0), nil
	case luaTypeNumber:
		value, err := ld.readNumber()
		if err != nil {
			return bytecode.Constant{}, err
		}
		return bytecode.NumberConstant(value), nil
	case luaTypeString:
		value, _, err := ld.loadString()
		if err != nil {
			return bytecode.Constant{}, err
		}
		return bytecode.StringConstant(value), nil
	default:
		return bytecode.Constant{}, ld.chunkError("bad constant")
	}
}

func (ld *loader) loadDebug() ([]int, []bytecode.LocVar, []string, error) {
	lineCount, err := ld.readInt("line info count")
	if err != nil {
		return nil, nil, nil, err
	}
	lineInfo := make([]int, 0, lineCount)
	for i := 0; i < lineCount; i++ {
		line, err := ld.readInt("line info")
		if err != nil {
			return nil, nil, nil, err
		}
		lineInfo = append(lineInfo, line)
	}

	locVarCount, err := ld.readInt("locvar count")
	if err != nil {
		return nil, nil, nil, err
	}
	locVars := make([]bytecode.LocVar, 0, locVarCount)
	for i := 0; i < locVarCount; i++ {
		name, _, err := ld.loadString()
		if err != nil {
			return nil, nil, nil, err
		}
		startPC, err := ld.readInt("locvar startpc")
		if err != nil {
			return nil, nil, nil, err
		}
		endPC, err := ld.readInt("locvar endpc")
		if err != nil {
			return nil, nil, nil, err
		}
		locVars = append(locVars, bytecode.LocVar{Name: name, StartPC: startPC, EndPC: endPC})
	}

	upvalueCount, err := ld.readInt("upvalue name count")
	if err != nil {
		return nil, nil, nil, err
	}
	upvalueNames := make([]string, 0, upvalueCount)
	for i := 0; i < upvalueCount; i++ {
		name, _, err := ld.loadString()
		if err != nil {
			return nil, nil, nil, err
		}
		upvalueNames = append(upvalueNames, name)
	}

	return lineInfo, locVars, upvalueNames, nil
}

func (ld *loader) loadString() (string, bool, error) {
	size, err := ld.readSizeT()
	if err != nil {
		return "", false, err
	}
	if size == 0 {
		return "", true, nil
	}
	if size == 1 {
		if _, err := ld.readBytes(1); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	bytesValue, err := ld.readBytes(size)
	if err != nil {
		return "", false, err
	}
	if bytesValue[len(bytesValue)-1] == 0 {
		bytesValue = bytesValue[:len(bytesValue)-1]
	}
	return string(bytesValue), false, nil
}

func (ld *loader) readByte() (byte, error) {
	var value [1]byte
	if _, err := io.ReadFull(ld.reader, value[:]); err != nil {
		return 0, ld.chunkError("unexpected end")
	}
	return value[0], nil
}

func (ld *loader) readBytes(size uint64) ([]byte, error) {
	if size > uint64(math.MaxInt32) {
		return nil, ld.chunkError("string too large")
	}
	data := make([]byte, int(size))
	if _, err := io.ReadFull(ld.reader, data); err != nil {
		return nil, ld.chunkError("unexpected end")
	}
	return data, nil
}

func (ld *loader) readUint32() (uint32, error) {
	var value uint32
	if err := binary.Read(ld.reader, ld.format.ByteOrder, &value); err != nil {
		return 0, ld.chunkError("unexpected end")
	}
	return value, nil
}

func (ld *loader) readInt(label string) (int, error) {
	var value int32
	if err := binary.Read(ld.reader, ld.format.ByteOrder, &value); err != nil {
		return 0, ld.chunkError("unexpected end")
	}
	if value < 0 {
		return 0, ld.chunkError("bad integer for " + label)
	}
	return int(value), nil
}

func (ld *loader) readSizeT() (uint64, error) {
	var value uint64
	if err := binary.Read(ld.reader, ld.format.ByteOrder, &value); err != nil {
		return 0, ld.chunkError("unexpected end")
	}
	return value, nil
}

func (ld *loader) readNumber() (float64, error) {
	var value float64
	if err := binary.Read(ld.reader, ld.format.ByteOrder, &value); err != nil {
		return 0, ld.chunkError("unexpected end")
	}
	return value, nil
}

package chunk51

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"vexlua/internal/bytecode"
	rt "vexlua/internal/runtime"
)

var legacyMagic = []byte{'V', 'X', 'L', '5', '1', 0}

const (
	constNil byte = iota
	constBool
	constNumber
	constString
)

func Dump(runtime *rt.Runtime, proto *bytecode.Proto) ([]byte, error) {
	if data, err := dumpLua51(runtime, proto); err == nil {
		return data, nil
	}
	return dumpLegacy(runtime, proto)
}

func dumpLegacy(runtime *rt.Runtime, proto *bytecode.Proto) ([]byte, error) {
	buf := &bytes.Buffer{}
	if _, err := buf.Write(legacyMagic); err != nil {
		return nil, err
	}
	if err := writeProto(buf, runtime, proto); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func Load(runtime *rt.Runtime, data []byte) (*bytecode.Proto, error) {
	if bytes.HasPrefix(data, lua51Signature) {
		return loadLua51(runtime, data)
	}
	if bytes.HasPrefix(data, legacyMagic) {
		return loadLegacy(runtime, data)
	}
	return nil, fmt.Errorf("unsupported chunk header")
}

func loadLegacy(runtime *rt.Runtime, data []byte) (*bytecode.Proto, error) {
	r := bytes.NewReader(data)
	header := make([]byte, len(legacyMagic))
	if _, err := r.Read(header); err != nil {
		return nil, err
	}
	if !bytes.Equal(header, legacyMagic) {
		return nil, fmt.Errorf("invalid chunk header")
	}
	return readProto(r, runtime)
}

func writeProto(buf *bytes.Buffer, runtime *rt.Runtime, proto *bytecode.Proto) error {
	if err := writeString(buf, proto.Name); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(proto.MaxStack)); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(proto.InlineCaches)); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(proto.NumParams)); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, proto.Scripted); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(proto.Upvalues))); err != nil {
		return err
	}
	for _, up := range proto.Upvalues {
		if err := binary.Write(buf, binary.LittleEndian, up.InParentLocal); err != nil {
			return err
		}
		if err := binary.Write(buf, binary.LittleEndian, up.Index); err != nil {
			return err
		}
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(proto.Constants))); err != nil {
		return err
	}
	for _, constant := range proto.Constants {
		if err := writeValue(buf, runtime, constant); err != nil {
			return err
		}
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(proto.Code))); err != nil {
		return err
	}
	for _, instr := range proto.Code {
		if err := binary.Write(buf, binary.LittleEndian, byte(instr.Op)); err != nil {
			return err
		}
		if err := binary.Write(buf, binary.LittleEndian, instr.A); err != nil {
			return err
		}
		if err := binary.Write(buf, binary.LittleEndian, instr.B); err != nil {
			return err
		}
		if err := binary.Write(buf, binary.LittleEndian, instr.C); err != nil {
			return err
		}
		if err := binary.Write(buf, binary.LittleEndian, instr.D); err != nil {
			return err
		}
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(proto.Children))); err != nil {
		return err
	}
	for _, child := range proto.Children {
		if err := writeProto(buf, runtime, child); err != nil {
			return err
		}
	}
	return nil
}

func readProto(r *bytes.Reader, runtime *rt.Runtime) (*bytecode.Proto, error) {
	name, err := readString(r)
	if err != nil {
		return nil, err
	}
	proto := bytecode.NewProto(name, 0, 0)
	var maxStack uint32
	if err := binary.Read(r, binary.LittleEndian, &maxStack); err != nil {
		return nil, err
	}
	proto.MaxStack = int(maxStack)
	var inlineCaches uint32
	if err := binary.Read(r, binary.LittleEndian, &inlineCaches); err != nil {
		return nil, err
	}
	proto.InlineCaches = int(inlineCaches)
	var numParams uint32
	if err := binary.Read(r, binary.LittleEndian, &numParams); err != nil {
		return nil, err
	}
	proto.NumParams = int(numParams)
	if err := binary.Read(r, binary.LittleEndian, &proto.Scripted); err != nil {
		return nil, err
	}
	var upvalueCount uint32
	if err := binary.Read(r, binary.LittleEndian, &upvalueCount); err != nil {
		return nil, err
	}
	proto.Upvalues = make([]bytecode.UpvalueDesc, 0, upvalueCount)
	for i := uint32(0); i < upvalueCount; i++ {
		var up bytecode.UpvalueDesc
		if err := binary.Read(r, binary.LittleEndian, &up.InParentLocal); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &up.Index); err != nil {
			return nil, err
		}
		proto.Upvalues = append(proto.Upvalues, up)
	}
	var constantCount uint32
	if err := binary.Read(r, binary.LittleEndian, &constantCount); err != nil {
		return nil, err
	}
	proto.Constants = make([]rt.Value, 0, constantCount)
	for i := uint32(0); i < constantCount; i++ {
		value, err := readValue(r, runtime)
		if err != nil {
			return nil, err
		}
		proto.Constants = append(proto.Constants, value)
	}
	var codeCount uint32
	if err := binary.Read(r, binary.LittleEndian, &codeCount); err != nil {
		return nil, err
	}
	proto.Code = make([]bytecode.Instr, 0, codeCount)
	for i := uint32(0); i < codeCount; i++ {
		var op byte
		instr := bytecode.Instr{}
		if err := binary.Read(r, binary.LittleEndian, &op); err != nil {
			return nil, err
		}
		instr.Op = bytecode.Op(op)
		if err := binary.Read(r, binary.LittleEndian, &instr.A); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &instr.B); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &instr.C); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &instr.D); err != nil {
			return nil, err
		}
		proto.Code = append(proto.Code, instr)
	}
	var childCount uint32
	if err := binary.Read(r, binary.LittleEndian, &childCount); err != nil {
		return nil, err
	}
	proto.Children = make([]*bytecode.Proto, 0, childCount)
	for i := uint32(0); i < childCount; i++ {
		child, err := readProto(r, runtime)
		if err != nil {
			return nil, err
		}
		proto.Children = append(proto.Children, child)
	}
	return proto, nil
}

func writeValue(buf *bytes.Buffer, runtime *rt.Runtime, value rt.Value) error {
	switch value.Kind() {
	case rt.KindNil:
		return binary.Write(buf, binary.LittleEndian, constNil)
	case rt.KindBool:
		if err := binary.Write(buf, binary.LittleEndian, constBool); err != nil {
			return err
		}
		return binary.Write(buf, binary.LittleEndian, value.Bool())
	case rt.KindNumber:
		if err := binary.Write(buf, binary.LittleEndian, constNumber); err != nil {
			return err
		}
		return binary.Write(buf, binary.LittleEndian, value.Number())
	case rt.KindHandle:
		h, _ := value.Handle()
		if h.Kind() != rt.ObjectString {
			return fmt.Errorf("unsupported constant handle kind %s", h.Kind())
		}
		if err := binary.Write(buf, binary.LittleEndian, constString); err != nil {
			return err
		}
		s, _ := runtime.ToString(value)
		return writeString(buf, s)
	default:
		return fmt.Errorf("unsupported constant kind %d", value.Kind())
	}
}

func readValue(r *bytes.Reader, runtime *rt.Runtime) (rt.Value, error) {
	var kind byte
	if err := binary.Read(r, binary.LittleEndian, &kind); err != nil {
		return rt.NilValue, err
	}
	switch kind {
	case constNil:
		return rt.NilValue, nil
	case constBool:
		var value bool
		if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
			return rt.NilValue, err
		}
		return rt.BoolValue(value), nil
	case constNumber:
		var value float64
		if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
			return rt.NilValue, err
		}
		return rt.NumberValue(value), nil
	case constString:
		value, err := readString(r)
		if err != nil {
			return rt.NilValue, err
		}
		return runtime.StringValue(value), nil
	default:
		return rt.NilValue, fmt.Errorf("unsupported constant tag %d", kind)
	}
}

func writeString(buf *bytes.Buffer, value string) error {
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := buf.WriteString(value)
	return err
}

func readString(r *bytes.Reader) (string, error) {
	var length uint32
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return "", err
	}
	buf := make([]byte, length)
	if _, err := r.Read(buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

package bytecode

import (
	"fmt"
	"strconv"
)

type ConstantKind uint8

const (
	ConstantNil ConstantKind = iota
	ConstantBoolean
	ConstantNumber
	ConstantString
)

type Constant struct {
	Kind    ConstantKind
	Boolean bool
	Number  float64
	Text    string
}

func NilConstant() Constant {
	return Constant{Kind: ConstantNil}
}

func BooleanConstant(value bool) Constant {
	return Constant{Kind: ConstantBoolean, Boolean: value}
}

func NumberConstant(value float64) Constant {
	return Constant{Kind: ConstantNumber, Number: value}
}

func StringConstant(value string) Constant {
	return Constant{Kind: ConstantString, Text: value}
}

func (kind ConstantKind) String() string {
	switch kind {
	case ConstantNil:
		return "nil"
	case ConstantBoolean:
		return "boolean"
	case ConstantNumber:
		return "number"
	case ConstantString:
		return "string"
	default:
		return fmt.Sprintf("ConstantKind(%d)", kind)
	}
}

func (constant Constant) String() string {
	switch constant.Kind {
	case ConstantNil:
		return "nil"
	case ConstantBoolean:
		if constant.Boolean {
			return "true"
		}
		return "false"
	case ConstantNumber:
		return strconv.FormatFloat(constant.Number, 'g', -1, 64)
	case ConstantString:
		return strconv.Quote(constant.Text)
	default:
		return fmt.Sprintf("<unknown constant kind %d>", constant.Kind)
	}
}

type LocVar struct {
	Name    string
	StartPC int
	EndPC   int
}

type Proto struct {
	Source       string
	LineDefined  int
	LastLineDef  int
	NumUpvalues  uint8
	NumParams    uint8
	IsVararg     uint8
	MaxStackSize uint8
	Code         []Instruction
	Constants    []Constant
	Protos       []*Proto
	LineInfo     []int
	LocVars      []LocVar
	UpvalueNames []string
}

type ClosureTemplate struct {
	Proto        *Proto
	UpvalueCount int
}

func (proto *Proto) InstructionCount() int {
	return len(proto.Code)
}

func (proto *Proto) ConstantCount() int {
	return len(proto.Constants)
}

func (proto *Proto) NewClosureTemplate() ClosureTemplate {
	return ClosureTemplate{Proto: proto, UpvalueCount: int(proto.NumUpvalues)}
}

func (proto *Proto) Iterator() *Iterator {
	return NewIterator(proto.Code)
}

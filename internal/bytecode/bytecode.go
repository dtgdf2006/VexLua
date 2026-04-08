package bytecode

import (
	"fmt"

	rt "vexlua/internal/runtime"
)

type Op uint8

const (
	OpNoop Op = iota
	OpLoadConst
	OpMove
	OpLoadUpvalue
	OpStoreUpvalue
	OpClosure
	OpNewTable
	OpLoadGlobal
	OpStoreGlobal
	OpGetField
	OpGetFieldIC
	OpSetField
	OpGetTable
	OpGetTableArray
	OpSetTable
	OpSetTableArray
	OpAppendTable
	OpAdd
	OpAddNum
	OpAddConst
	OpSub
	OpMul
	OpDiv
	OpMod
	OpPow
	OpLen
	OpLenTable
	OpConcat
	OpEqual
	OpLess
	OpLessEqual
	OpNot
	OpCall
	OpTailCall
	OpCallMulti
	OpVararg
	OpYield
	OpReturn
	OpReturnMulti
	OpReturnAppendPending
	OpJump
	OpJumpIfFalse
	OpJumpIfTrue
	OpLessEqualJump
	OpSelf
	OpSelfIC
	OpIterPairs
	OpIterIPairs
	OpUnm
	OpClose
)

type UpvalueDesc struct {
	Name          string
	InParentLocal bool
	Index         uint16
}

type LocalVar struct {
	Name    string
	Slot    int
	StartPC int
	EndPC   int
}

type Instr struct {
	Op Op
	A  uint16
	B  uint16
	C  uint16
	D  int32
}

type Proto struct {
	Name            string
	Source          string
	MaxStack        int
	InlineCaches    int
	NumParams       int
	Vararg          bool
	Scripted        bool
	LineDefined     int
	LastLineDefined int
	Constants       []rt.Value
	Children        []*Proto
	Upvalues        []UpvalueDesc
	LocalsDebug     []LocalVar
	Code            []Instr
	LineInfo        []int
	currentLine     int
}

func NewProto(name string, maxStack int, inlineCaches int) *Proto {
	return &Proto{
		Name:         name,
		MaxStack:     maxStack,
		InlineCaches: inlineCaches,
		Children:     make([]*Proto, 0, 2),
		Upvalues:     make([]UpvalueDesc, 0, 2),
		LocalsDebug:  make([]LocalVar, 0, 2),
		Constants:    make([]rt.Value, 0, 8),
		Code:         make([]Instr, 0, 16),
		LineInfo:     make([]int, 0, 16),
	}
}

func (p *Proto) AddChild(child *Proto) int {
	p.Children = append(p.Children, child)
	return len(p.Children) - 1
}

func (p *Proto) AddConstant(v rt.Value) int {
	p.Constants = append(p.Constants, v)
	return len(p.Constants) - 1
}

func (p *Proto) Emit(op Op, a, b, c uint16, d int32) {
	p.Code = append(p.Code, Instr{Op: op, A: a, B: b, C: c, D: d})
	p.LineInfo = append(p.LineInfo, p.currentLine)
}

func (p *Proto) SetCurrentLine(line int) {
	p.currentLine = line
}

func (p *Proto) CurrentLine(pc int) int {
	if pc < 0 || pc >= len(p.LineInfo) {
		return -1
	}
	if p.LineInfo[pc] <= 0 {
		return -1
	}
	return p.LineInfo[pc]
}

func (p *Proto) SetSourceRecursive(source string) {
	p.Source = source
	for _, child := range p.Children {
		child.SetSourceRecursive(source)
	}
}

func (op Op) String() string {
	switch op {
	case OpNoop:
		return "NOOP"
	case OpLoadConst:
		return "LOAD_CONST"
	case OpMove:
		return "MOVE"
	case OpLoadUpvalue:
		return "LOAD_UPVALUE"
	case OpStoreUpvalue:
		return "STORE_UPVALUE"
	case OpClosure:
		return "CLOSURE"
	case OpNewTable:
		return "NEW_TABLE"
	case OpLoadGlobal:
		return "LOAD_GLOBAL"
	case OpStoreGlobal:
		return "STORE_GLOBAL"
	case OpGetField:
		return "GET_FIELD"
	case OpGetFieldIC:
		return "GET_FIELD_IC"
	case OpSetField:
		return "SET_FIELD"
	case OpGetTable:
		return "GET_TABLE"
	case OpGetTableArray:
		return "GET_TABLE_ARRAY"
	case OpSetTable:
		return "SET_TABLE"
	case OpSetTableArray:
		return "SET_TABLE_ARRAY"
	case OpAppendTable:
		return "APPEND_TABLE"
	case OpAdd:
		return "ADD"
	case OpAddNum:
		return "ADD_NUM"
	case OpAddConst:
		return "ADD_CONST"
	case OpSub:
		return "SUB"
	case OpMul:
		return "MUL"
	case OpDiv:
		return "DIV"
	case OpMod:
		return "MOD"
	case OpPow:
		return "POW"
	case OpLen:
		return "LEN"
	case OpLenTable:
		return "LEN_TABLE"
	case OpConcat:
		return "CONCAT"
	case OpEqual:
		return "EQ"
	case OpLess:
		return "LT"
	case OpLessEqual:
		return "LE"
	case OpNot:
		return "NOT"
	case OpCall:
		return "CALL"
	case OpTailCall:
		return "TAILCALL"
	case OpCallMulti:
		return "CALL_MULTI"
	case OpVararg:
		return "VARARG"
	case OpYield:
		return "YIELD"
	case OpUnm:
		return "UNM"
	case OpReturn:
		return "RETURN"
	case OpReturnMulti:
		return "RETURN_MULTI"
	case OpReturnAppendPending:
		return "RETURN_APPEND_PENDING"
	case OpJump:
		return "JUMP"
	case OpJumpIfFalse:
		return "JUMP_IF_FALSE"
	case OpJumpIfTrue:
		return "JUMP_IF_TRUE"
	case OpLessEqualJump:
		return "LE_JUMP"
	case OpSelf:
		return "SELF"
	case OpSelfIC:
		return "SELF_IC"
	case OpIterPairs:
		return "ITER_PAIRS"
	case OpIterIPairs:
		return "ITER_IPAIRS"
	case OpClose:
		return "CLOSE"
	default:
		return fmt.Sprintf("OP_%d", op)
	}
}

const callAppendPendingFlag = 1 << 15

func PackCallCounts(argCount int, resultCount int) int32 {
	return PackCallCountsWithPending(argCount, resultCount, false)
}

func PackCallCountsWithPending(argCount int, resultCount int, appendPending bool) int32 {
	encodedArgs := argCount &^ callAppendPendingFlag
	if appendPending {
		encodedArgs |= callAppendPendingFlag
	}
	return int32((resultCount&0xffff)<<16 | (encodedArgs & 0xffff))
}

func UnpackCallCounts(v int32) (int, int) {
	argCount, resultCount, _ := UnpackCallSpec(v)
	return argCount, resultCount
}

func UnpackCallSpec(v int32) (int, int, bool) {
	uv := uint32(v)
	rawArgs := int(uv & 0xffff)
	appendPending := rawArgs&callAppendPendingFlag != 0
	argCount := rawArgs &^ callAppendPendingFlag
	resultCount := int((uv >> 16) & 0xffff)
	return argCount, resultCount, appendPending
}

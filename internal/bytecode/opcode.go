package bytecode

import "fmt"

type OpMode uint8

const (
	ModeABC OpMode = iota
	ModeABx
	ModeAsBx
)

type ArgMode uint8

const (
	ArgN ArgMode = iota
	ArgU
	ArgR
	ArgK
)

type Opcode uint8

const (
	OP_MOVE Opcode = iota
	OP_LOADK
	OP_LOADBOOL
	OP_LOADNIL
	OP_GETUPVAL
	OP_GETGLOBAL
	OP_GETTABLE
	OP_SETGLOBAL
	OP_SETUPVAL
	OP_SETTABLE
	OP_NEWTABLE
	OP_SELF
	OP_ADD
	OP_SUB
	OP_MUL
	OP_DIV
	OP_MOD
	OP_POW
	OP_UNM
	OP_NOT
	OP_LEN
	OP_CONCAT
	OP_JMP
	OP_EQ
	OP_LT
	OP_LE
	OP_TEST
	OP_TESTSET
	OP_CALL
	OP_TAILCALL
	OP_RETURN
	OP_FORLOOP
	OP_FORPREP
	OP_TFORLOOP
	OP_SETLIST
	OP_CLOSE
	OP_CLOSURE
	OP_VARARG
)

const NumOpcodes = int(OP_VARARG) + 1

type OpcodeInfo struct {
	Name   string
	Mode   OpMode
	BMode  ArgMode
	CMode  ArgMode
	SetsA  bool
	IsTest bool
}

var opcodeTable = [...]OpcodeInfo{
	{Name: "MOVE", Mode: ModeABC, BMode: ArgR, CMode: ArgN, SetsA: true},
	{Name: "LOADK", Mode: ModeABx, BMode: ArgK, CMode: ArgN, SetsA: true},
	{Name: "LOADBOOL", Mode: ModeABC, BMode: ArgU, CMode: ArgU, SetsA: true},
	{Name: "LOADNIL", Mode: ModeABC, BMode: ArgR, CMode: ArgN, SetsA: true},
	{Name: "GETUPVAL", Mode: ModeABC, BMode: ArgU, CMode: ArgN, SetsA: true},
	{Name: "GETGLOBAL", Mode: ModeABx, BMode: ArgK, CMode: ArgN, SetsA: true},
	{Name: "GETTABLE", Mode: ModeABC, BMode: ArgR, CMode: ArgK, SetsA: true},
	{Name: "SETGLOBAL", Mode: ModeABx, BMode: ArgK, CMode: ArgN},
	{Name: "SETUPVAL", Mode: ModeABC, BMode: ArgU, CMode: ArgN},
	{Name: "SETTABLE", Mode: ModeABC, BMode: ArgK, CMode: ArgK},
	{Name: "NEWTABLE", Mode: ModeABC, BMode: ArgU, CMode: ArgU, SetsA: true},
	{Name: "SELF", Mode: ModeABC, BMode: ArgR, CMode: ArgK, SetsA: true},
	{Name: "ADD", Mode: ModeABC, BMode: ArgK, CMode: ArgK, SetsA: true},
	{Name: "SUB", Mode: ModeABC, BMode: ArgK, CMode: ArgK, SetsA: true},
	{Name: "MUL", Mode: ModeABC, BMode: ArgK, CMode: ArgK, SetsA: true},
	{Name: "DIV", Mode: ModeABC, BMode: ArgK, CMode: ArgK, SetsA: true},
	{Name: "MOD", Mode: ModeABC, BMode: ArgK, CMode: ArgK, SetsA: true},
	{Name: "POW", Mode: ModeABC, BMode: ArgK, CMode: ArgK, SetsA: true},
	{Name: "UNM", Mode: ModeABC, BMode: ArgR, CMode: ArgN, SetsA: true},
	{Name: "NOT", Mode: ModeABC, BMode: ArgR, CMode: ArgN, SetsA: true},
	{Name: "LEN", Mode: ModeABC, BMode: ArgR, CMode: ArgN, SetsA: true},
	{Name: "CONCAT", Mode: ModeABC, BMode: ArgR, CMode: ArgR, SetsA: true},
	{Name: "JMP", Mode: ModeAsBx, BMode: ArgR, CMode: ArgN},
	{Name: "EQ", Mode: ModeABC, BMode: ArgK, CMode: ArgK, IsTest: true},
	{Name: "LT", Mode: ModeABC, BMode: ArgK, CMode: ArgK, IsTest: true},
	{Name: "LE", Mode: ModeABC, BMode: ArgK, CMode: ArgK, IsTest: true},
	{Name: "TEST", Mode: ModeABC, BMode: ArgR, CMode: ArgU, SetsA: true, IsTest: true},
	{Name: "TESTSET", Mode: ModeABC, BMode: ArgR, CMode: ArgU, SetsA: true, IsTest: true},
	{Name: "CALL", Mode: ModeABC, BMode: ArgU, CMode: ArgU, SetsA: true},
	{Name: "TAILCALL", Mode: ModeABC, BMode: ArgU, CMode: ArgU, SetsA: true},
	{Name: "RETURN", Mode: ModeABC, BMode: ArgU, CMode: ArgN},
	{Name: "FORLOOP", Mode: ModeAsBx, BMode: ArgR, CMode: ArgN, SetsA: true},
	{Name: "FORPREP", Mode: ModeAsBx, BMode: ArgR, CMode: ArgN, SetsA: true},
	{Name: "TFORLOOP", Mode: ModeABC, BMode: ArgN, CMode: ArgU, IsTest: true},
	{Name: "SETLIST", Mode: ModeABC, BMode: ArgU, CMode: ArgU},
	{Name: "CLOSE", Mode: ModeABC, BMode: ArgN, CMode: ArgN},
	{Name: "CLOSURE", Mode: ModeABx, BMode: ArgU, CMode: ArgN, SetsA: true},
	{Name: "VARARG", Mode: ModeABC, BMode: ArgU, CMode: ArgN, SetsA: true},
}

func (op Opcode) Valid() bool {
	return int(op) >= 0 && int(op) < len(opcodeTable)
}

func (op Opcode) Info() OpcodeInfo {
	if !op.Valid() {
		return OpcodeInfo{Name: fmt.Sprintf("Opcode(%d)", op)}
	}
	return opcodeTable[op]
}

func (op Opcode) String() string {
	return op.Info().Name
}

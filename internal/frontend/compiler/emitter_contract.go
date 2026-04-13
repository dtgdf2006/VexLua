package compiler

// JumpList is the patch-list payload shared by future emitter control-flow helpers.
type JumpList struct {
	Entries []int
}

// ExprResultKind is the Go-facing analogue of Lua 5.1's expkind.
type ExprResultKind uint8

const (
	ExprResultVoid ExprResultKind = iota
	ExprResultNil
	ExprResultBoolConst
	ExprResultNumberConst
	ExprResultStringConst
	ExprResultLocalReg
	ExprResultUpvalue
	ExprResultGlobalName
	ExprResultIndexed
	ExprResultJumpCond
	ExprResultRelocatableInsn
	ExprResultNonRelocReg
	ExprResultCallResult
	ExprResultVarargResult
)

// ExprResult freezes the minimum expression-lowering contract used by the future emitter.
type ExprResult struct {
	Kind       ExprResultKind
	Info       int
	Aux        int
	Number     float64
	TrueJumps  JumpList
	FalseJumps JumpList
}

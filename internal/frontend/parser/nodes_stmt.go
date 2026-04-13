package parser

type EmptyStat struct{ NodeInfo }

func (EmptyStat) statNode() {}

type LocalDeclStat struct {
	NodeInfo
	Names  []Name
	Values []Expr
}

func (LocalDeclStat) statNode() {}

type LocalFunctionStat struct {
	NodeInfo
	Name Name
	Body *FunctionBody
}

func (LocalFunctionStat) statNode() {}

type AssignmentStat struct {
	NodeInfo
	Targets []AssignableExpr
	Values  []Expr
}

func (AssignmentStat) statNode() {}

type FunctionStat struct {
	NodeInfo
	Path []Name
	Body *FunctionBody
}

func (FunctionStat) statNode() {}

type MethodStat struct {
	NodeInfo
	Path   []Name
	Method Name
	Body   *FunctionBody
}

func (MethodStat) statNode() {}

type CallStat struct {
	NodeInfo
	Call Expr
}

func (CallStat) statNode() {}

type IfStat struct {
	NodeInfo
	Clauses   []IfClause
	ElseBlock *Block
}

func (IfStat) statNode() {}

type WhileStat struct {
	NodeInfo
	Condition Expr
	Body      *Block
}

func (WhileStat) statNode() {}

type RepeatUntilStat struct {
	NodeInfo
	Body      *Block
	Condition Expr
}

func (RepeatUntilStat) statNode() {}

type NumericForStat struct {
	NodeInfo
	Name    Name
	Initial Expr
	Limit   Expr
	Step    Expr
	Body    *Block
}

func (NumericForStat) statNode() {}

type GenericForStat struct {
	NodeInfo
	Names     []Name
	Iterators []Expr
	Body      *Block
}

func (GenericForStat) statNode() {}

type DoStat struct {
	NodeInfo
	Body *Block
}

func (DoStat) statNode() {}

type BreakStat struct{ NodeInfo }

func (BreakStat) statNode() {}

type ReturnStat struct {
	NodeInfo
	Values []Expr
}

func (ReturnStat) statNode() {}

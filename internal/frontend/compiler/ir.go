package compiler

import (
	"vexlua/internal/frontend/lexer"
)

// ResultMode records whether a bound expression is consumed as a single value,
// a multi-result value, or a tail-position value.
type ResultMode uint8

const (
	ResultSingle ResultMode = iota
	ResultMulti
	ResultTail
)

// BoundNode is the common interface for all bound IR nodes.
type BoundNode interface {
	SpanRange() lexer.Span
}

// BoundInfo carries source span information shared by all bound nodes.
type BoundInfo struct {
	Span lexer.Span
}

func (info BoundInfo) SpanRange() lexer.Span {
	return info.Span
}

// BoundExprInfo carries source span information plus result-mode metadata.
type BoundExprInfo struct {
	BoundInfo
	Results ResultMode
}

func (info BoundExprInfo) ResultMode() ResultMode {
	return info.Results
}

// BoundChunk is the bound root for a source chunk.
type BoundChunk struct {
	BoundInfo
	Name    string
	Func    *BoundFunc
	Symbols []Symbol
	Scopes  []Scope
}

// BoundFunc is the canonical post-binding function form.
type BoundFunc struct {
	BoundInfo
	Name      string
	Params    []SymbolID
	Locals    []SymbolID
	Captures  []CaptureDesc
	HasVararg bool
	Body      *BoundBlock
}

// BoundBlock is the canonical lexical block form after scope binding.
type BoundBlock struct {
	BoundInfo
	Scope     ScopeID
	Breakable bool
	Stats     []BoundStat
}

// BoundStat is the common interface for bound statements.
type BoundStat interface {
	BoundNode
	boundStat()
}

// BoundExpr is the common interface for bound expressions.
type BoundExpr interface {
	BoundNode
	boundExpr()
	ResultMode() ResultMode
}

// BoundTarget is the common interface for assignment targets after binding.
type BoundTarget interface {
	BoundNode
	boundTarget()
}

// BoundCallLike marks expressions that can appear in statement-call position.
type BoundCallLike interface {
	BoundExpr
	boundCallLike()
}

type BoundLocalDeclStat struct {
	BoundInfo
	Names  []SymbolID
	Values []BoundExpr
}

func (BoundLocalDeclStat) boundStat() {}

type BoundAssignStat struct {
	BoundInfo
	Targets []BoundTarget
	Values  []BoundExpr
}

func (BoundAssignStat) boundStat() {}

type BoundCallStat struct {
	BoundInfo
	Call BoundCallLike
}

func (BoundCallStat) boundStat() {}

type BoundIfClause struct {
	Span      lexer.Span
	Condition BoundExpr
	Body      *BoundBlock
}

type BoundIfStat struct {
	BoundInfo
	Clauses   []BoundIfClause
	ElseBlock *BoundBlock
}

func (BoundIfStat) boundStat() {}

type BoundWhileStat struct {
	BoundInfo
	Condition BoundExpr
	Body      *BoundBlock
}

func (BoundWhileStat) boundStat() {}

type BoundRepeatStat struct {
	BoundInfo
	Body      *BoundBlock
	Condition BoundExpr
}

func (BoundRepeatStat) boundStat() {}

type BoundNumericForStat struct {
	BoundInfo
	IndexSymbol SymbolID
	LimitSymbol SymbolID
	StepSymbol  SymbolID
	Counter     SymbolID
	Initial     BoundExpr
	Limit       BoundExpr
	Step        BoundExpr
	Body        *BoundBlock
}

func (BoundNumericForStat) boundStat() {}

type BoundGenericForStat struct {
	BoundInfo
	GeneratorSymbol SymbolID
	StateSymbol     SymbolID
	ControlSymbol   SymbolID
	Names           []SymbolID
	Iterators       []BoundExpr
	Body            *BoundBlock
}

func (BoundGenericForStat) boundStat() {}

type BoundDoStat struct {
	BoundInfo
	Body *BoundBlock
}

func (BoundDoStat) boundStat() {}

type BoundBreakStat struct{ BoundInfo }

func (BoundBreakStat) boundStat() {}

type BoundReturnStat struct {
	BoundInfo
	Values []BoundExpr
}

func (BoundReturnStat) boundStat() {}

type BoundNilExpr struct{ BoundExprInfo }

func (BoundNilExpr) boundExpr() {}

type BoundBoolExpr struct {
	BoundExprInfo
	Value bool
}

func (BoundBoolExpr) boundExpr() {}

type BoundNumberExpr struct {
	BoundExprInfo
	Raw   string
	Value float64
}

func (BoundNumberExpr) boundExpr() {}

type BoundStringExpr struct {
	BoundExprInfo
	Raw   string
	Value string
}

func (BoundStringExpr) boundExpr() {}

type BoundVarargExpr struct{ BoundExprInfo }

func (BoundVarargExpr) boundExpr() {}

type BoundSymbolExpr struct {
	BoundExprInfo
	Ref NameRef
}

func (BoundSymbolExpr) boundExpr() {}

type BoundUnaryExpr struct {
	BoundExprInfo
	Op    lexer.TokenKind
	Value BoundExpr
}

func (BoundUnaryExpr) boundExpr() {}

type BoundBinaryExpr struct {
	BoundExprInfo
	Op    lexer.TokenKind
	Left  BoundExpr
	Right BoundExpr
}

func (BoundBinaryExpr) boundExpr() {}

type BoundLogicalExpr struct {
	BoundExprInfo
	Op    lexer.TokenKind
	Left  BoundExpr
	Right BoundExpr
}

func (BoundLogicalExpr) boundExpr() {}

type BoundCompareExpr struct {
	BoundExprInfo
	Op    lexer.TokenKind
	Left  BoundExpr
	Right BoundExpr
}

func (BoundCompareExpr) boundExpr() {}

type BoundTableFieldKind uint8

const (
	BoundTableFieldArray BoundTableFieldKind = iota
	BoundTableFieldNamed
	BoundTableFieldIndexed
)

type BoundTableField struct {
	Span  lexer.Span
	Kind  BoundTableFieldKind
	Name  string
	Key   BoundExpr
	Value BoundExpr
}

type BoundTableExpr struct {
	BoundExprInfo
	Fields     []BoundTableField
	ArrayCount int
	HashCount  int
}

func (BoundTableExpr) boundExpr() {}

type BoundFunctionExpr struct {
	BoundExprInfo
	Func *BoundFunc
}

func (BoundFunctionExpr) boundExpr() {}

type BoundIndexExpr struct {
	BoundExprInfo
	Receiver BoundExpr
	Index    BoundExpr
}

func (BoundIndexExpr) boundExpr() {}

type BoundFieldExpr struct {
	BoundExprInfo
	Receiver BoundExpr
	Name     string
}

func (BoundFieldExpr) boundExpr() {}

type BoundCallExpr struct {
	BoundExprInfo
	Callee     BoundExpr
	Receiver   BoundExpr
	MethodName string
	Args       []BoundExpr
}

func (BoundCallExpr) boundExpr()     {}
func (BoundCallExpr) boundCallLike() {}

type BoundMethodCallExpr struct {
	BoundExprInfo
	Receiver BoundExpr
	Name     string
	Args     []BoundExpr
}

func (BoundMethodCallExpr) boundExpr()     {}
func (BoundMethodCallExpr) boundCallLike() {}

type BoundSymbolTarget struct {
	BoundInfo
	Ref NameRef
}

func (BoundSymbolTarget) boundTarget() {}

type BoundIndexTarget struct {
	BoundInfo
	Receiver BoundExpr
	Index    BoundExpr
}

func (BoundIndexTarget) boundTarget() {}

type BoundFieldTarget struct {
	BoundInfo
	Receiver BoundExpr
	Name     string
}

func (BoundFieldTarget) boundTarget() {}

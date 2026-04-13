package parser

import "vexlua/internal/frontend/lexer"

// Node is the common interface shared by all AST nodes.
type Node interface {
	SpanRange() lexer.Span
}

// NodeInfo carries the source span for a node.
type NodeInfo struct {
	Span lexer.Span
}

func (info NodeInfo) SpanRange() lexer.Span {
	return info.Span
}

// Name is the parser-level identifier payload preserved across AST nodes.
type Name struct {
	Span  lexer.Span
	Text  string
	Token lexer.Token
}

// Chunk is the AST root for a source file.
type Chunk struct {
	NodeInfo
	Name  string
	Block *Block
}

// Block is a sequence of statements with a single lexical scope.
type Block struct {
	NodeInfo
	Stats []Stat
}

// FunctionBody preserves source-level function syntax before binding.
type FunctionBody struct {
	NodeInfo
	Params    []Name
	HasVararg bool
	Block     *Block
}

// Stat is the common interface for AST statements.
type Stat interface {
	Node
	statNode()
}

// Expr is the common interface for AST expressions.
type Expr interface {
	Node
	exprNode()
}

// AssignableExpr marks expressions that can appear on the left-hand side of an assignment.
type AssignableExpr interface {
	Expr
	assignableNode()
}

// TableFieldKind identifies the three table-constructor field forms in Lua 5.1.
type TableFieldKind uint8

const (
	TableFieldArray TableFieldKind = iota
	TableFieldNamed
	TableFieldIndexed
)

// TableField preserves the exact field shape emitted by the parser.
type TableField struct {
	Span  lexer.Span
	Kind  TableFieldKind
	Name  Name
	Key   Expr
	Value Expr
}

// IfClause preserves each conditional arm before binding or lowering.
type IfClause struct {
	Span      lexer.Span
	Condition Expr
	Body      *Block
}

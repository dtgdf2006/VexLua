package parser

import "vexlua/internal/frontend/lexer"

type NilExpr struct{ NodeInfo }

func (NilExpr) exprNode() {}

type BoolExpr struct {
	NodeInfo
	Value bool
}

func (BoolExpr) exprNode() {}

type NumberExpr struct {
	NodeInfo
	Raw   string
	Value float64
}

func (NumberExpr) exprNode() {}

type StringExpr struct {
	NodeInfo
	Raw   string
	Value string
}

func (StringExpr) exprNode() {}

type VarargExpr struct{ NodeInfo }

func (VarargExpr) exprNode() {}

type NameExpr struct {
	NodeInfo
	Name Name
}

func (NameExpr) exprNode()       {}
func (NameExpr) assignableNode() {}

type UnaryExpr struct {
	NodeInfo
	Op    lexer.TokenKind
	Value Expr
}

func (UnaryExpr) exprNode() {}

type BinaryExpr struct {
	NodeInfo
	Op    lexer.TokenKind
	Left  Expr
	Right Expr
}

func (BinaryExpr) exprNode() {}

type TableConstructorExpr struct {
	NodeInfo
	Fields []TableField
}

func (TableConstructorExpr) exprNode() {}

type FunctionLiteralExpr struct {
	NodeInfo
	Body *FunctionBody
}

func (FunctionLiteralExpr) exprNode() {}

type IndexExpr struct {
	NodeInfo
	Receiver Expr
	Index    Expr
}

func (IndexExpr) exprNode()       {}
func (IndexExpr) assignableNode() {}

type FieldExpr struct {
	NodeInfo
	Receiver Expr
	Name     Name
}

func (FieldExpr) exprNode()       {}
func (FieldExpr) assignableNode() {}

type MethodExpr struct {
	NodeInfo
	Receiver Expr
	Name     Name
}

func (MethodExpr) exprNode() {}

type CallExpr struct {
	NodeInfo
	Callee Expr
	Args   []Expr
}

func (CallExpr) exprNode() {}

type MethodCallExpr struct {
	NodeInfo
	Receiver Expr
	Name     Name
	Args     []Expr
}

func (MethodCallExpr) exprNode() {}

type ParenExpr struct {
	NodeInfo
	Inner Expr
}

func (ParenExpr) exprNode() {}

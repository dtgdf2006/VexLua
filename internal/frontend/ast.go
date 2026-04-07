package frontend

type Chunk struct {
	Statements []Stmt
}

type Stmt interface {
	stmtNode()
}

type Expr interface {
	exprNode()
}

type LocalAssignStmt struct {
	Names  []string
	Values []Expr
}

type BreakStmt struct{}

type AssignStmt struct {
	Targets []Expr
	Values  []Expr
}

type FunctionStmt struct {
	Local  bool
	Name   string
	Target Expr
	Params []string
	Vararg bool
	Body   []Stmt
}

type IfClause struct {
	Cond Expr
	Body []Stmt
}

type IfStmt struct {
	Clauses  []IfClause
	ElseBody []Stmt
}

type WhileStmt struct {
	Cond Expr
	Body []Stmt
}

type RepeatStmt struct {
	Body []Stmt
	Cond Expr
}

type ForNumericStmt struct {
	Name  string
	Start Expr
	Limit Expr
	Step  Expr
	Body  []Stmt
}

type ForGenericStmt struct {
	Names []string
	Exprs []Expr
	Body  []Stmt
}

type ReturnStmt struct {
	Values []Expr
}

type ExprStmt struct {
	Expr Expr
}

type NameExpr struct {
	Name string
}

type NumberExpr struct {
	Value float64
}

type StringExpr struct {
	Value string
}

type BoolExpr struct {
	Value bool
}

type NilExpr struct{}

type VarargExpr struct{}

type UnaryExpr struct {
	Op   TokenType
	Expr Expr
}

type BinaryExpr struct {
	Op    TokenType
	Left  Expr
	Right Expr
}

type CallExpr struct {
	Callee Expr
	Args   []Expr
}

type MethodCallExpr struct {
	Receiver Expr
	Name     string
	Args     []Expr
}

type FieldExpr struct {
	Target Expr
	Name   string
}

type FunctionExpr struct {
	Params []string
	Vararg bool
	Body   []Stmt
}

type IndexExpr struct {
	Target Expr
	Key    Expr
}

type TableFieldKind uint8

const (
	TableFieldArray TableFieldKind = iota
	TableFieldNamed
	TableFieldExpr
)

type TableField struct {
	Kind  TableFieldKind
	Name  string
	Key   Expr
	Value Expr
}

type TableExpr struct {
	Fields []TableField
}

func (*LocalAssignStmt) stmtNode() {}
func (*BreakStmt) stmtNode()       {}
func (*AssignStmt) stmtNode()      {}
func (*FunctionStmt) stmtNode()    {}
func (*IfStmt) stmtNode()          {}
func (*WhileStmt) stmtNode()       {}
func (*RepeatStmt) stmtNode()      {}
func (*ForNumericStmt) stmtNode()  {}
func (*ForGenericStmt) stmtNode()  {}
func (*ReturnStmt) stmtNode()      {}
func (*ExprStmt) stmtNode()        {}

func (*NameExpr) exprNode()       {}
func (*NumberExpr) exprNode()     {}
func (*StringExpr) exprNode()     {}
func (*BoolExpr) exprNode()       {}
func (*NilExpr) exprNode()        {}
func (*VarargExpr) exprNode()     {}
func (*UnaryExpr) exprNode()      {}
func (*BinaryExpr) exprNode()     {}
func (*CallExpr) exprNode()       {}
func (*MethodCallExpr) exprNode() {}
func (*FieldExpr) exprNode()      {}
func (*FunctionExpr) exprNode()   {}
func (*IndexExpr) exprNode()      {}
func (*TableExpr) exprNode()      {}

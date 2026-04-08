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
	Line   int
	Names  []string
	Values []Expr
}

type BreakStmt struct {
	Line int
}

type AssignStmt struct {
	Line    int
	Targets []Expr
	Values  []Expr
}

type FunctionStmt struct {
	Line    int
	EndLine int
	Local   bool
	Name    string
	Target  Expr
	Params  []string
	Vararg  bool
	Body    []Stmt
}

type IfClause struct {
	Cond Expr
	Body []Stmt
}

type IfStmt struct {
	Line     int
	Clauses  []IfClause
	ElseBody []Stmt
}

type WhileStmt struct {
	Line int
	Cond Expr
	Body []Stmt
}

type RepeatStmt struct {
	Line int
	Body []Stmt
	Cond Expr
}

type DoStmt struct {
	Line int
	Body []Stmt
}

type ForNumericStmt struct {
	Line  int
	Name  string
	Start Expr
	Limit Expr
	Step  Expr
	Body  []Stmt
}

type ForGenericStmt struct {
	Line  int
	Names []string
	Exprs []Expr
	Body  []Stmt
}

type ReturnStmt struct {
	Line   int
	Values []Expr
}

type ExprStmt struct {
	Line int
	Expr Expr
}

type NameExpr struct {
	Line int
	Name string
}

type NumberExpr struct {
	Line  int
	Value float64
}

type StringExpr struct {
	Line  int
	Value string
}

type BoolExpr struct {
	Line  int
	Value bool
}

type NilExpr struct {
	Line int
}

type VarargExpr struct {
	Line int
}

type UnaryExpr struct {
	Line int
	Op   TokenType
	Expr Expr
}

type BinaryExpr struct {
	Line  int
	Op    TokenType
	Left  Expr
	Right Expr
}

type CallExpr struct {
	Line   int
	Callee Expr
	Args   []Expr
}

type MethodCallExpr struct {
	Line     int
	Receiver Expr
	Name     string
	Args     []Expr
}

type FieldExpr struct {
	Line   int
	Target Expr
	Name   string
}

type FunctionExpr struct {
	Line    int
	EndLine int
	Params  []string
	Vararg  bool
	Body    []Stmt
}

type IndexExpr struct {
	Line   int
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
	Line   int
	Fields []TableField
}

func (*LocalAssignStmt) stmtNode() {}
func (*BreakStmt) stmtNode()       {}
func (*AssignStmt) stmtNode()      {}
func (*FunctionStmt) stmtNode()    {}
func (*IfStmt) stmtNode()          {}
func (*WhileStmt) stmtNode()       {}
func (*RepeatStmt) stmtNode()      {}
func (*DoStmt) stmtNode()          {}
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

package ir

type VarKind uint8

const (
	VarLocal VarKind = iota
	VarUpvalue
	VarGlobal
)

type Function struct {
	Name            string
	Params          []string
	Vararg          bool
	Body            []Stmt
	Locals          int
	Upvalues        []UpvalueDesc
	LineDefined     int
	LastLineDefined int
}

type UpvalueDesc struct {
	Name          string
	InParentLocal bool
	Index         int
}

type VarRef struct {
	Name  string
	Kind  VarKind
	Index int
}

type Stmt interface {
	stmtNode()
}

type Expr interface {
	exprNode()
}

type AssignTarget interface {
	targetNode()
}

type IfClause struct {
	Cond Expr
	Body []Stmt
}

type LocalAssignStmt struct {
	Line   int
	Slots  []int
	Values []Expr
}

type BreakStmt struct {
	Line int
}

type AssignStmt struct {
	Line    int
	Targets []AssignTarget
	Values  []Expr
}

type ReturnStmt struct {
	Line   int
	Values []Expr
}

type ExprStmt struct {
	Line int
	Expr Expr
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
	Slot  int
	Start Expr
	Limit Expr
	Step  Expr
	Body  []Stmt
}

type ForGenericStmt struct {
	Line         int
	IteratorSlot int
	StateSlot    int
	ControlSlot  int
	VarSlots     []int
	Exprs        []Expr
	Body         []Stmt
}

type VarTarget struct {
	Line int
	Ref  VarRef
}

type FieldTarget struct {
	Line   int
	Target Expr
	Name   string
}

type IndexTarget struct {
	Line   int
	Target Expr
	Key    Expr
}

type VarExpr struct {
	Line int
	Ref  VarRef
}

type LiteralExpr struct {
	Line  int
	Value any
}

type VarargExpr struct {
	Line int
}

type UnaryExpr struct {
	Line int
	Op   string
	Expr Expr
}

type BinaryExpr struct {
	Line  int
	Op    string
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

type ClosureExpr struct {
	Fn *Function
}

func (*LocalAssignStmt) stmtNode() {}
func (*BreakStmt) stmtNode()       {}
func (*AssignStmt) stmtNode()      {}
func (*ReturnStmt) stmtNode()      {}
func (*ExprStmt) stmtNode()        {}
func (*IfStmt) stmtNode()          {}
func (*WhileStmt) stmtNode()       {}
func (*RepeatStmt) stmtNode()      {}
func (*DoStmt) stmtNode()          {}
func (*ForNumericStmt) stmtNode()  {}
func (*ForGenericStmt) stmtNode()  {}

func (*VarTarget) targetNode()   {}
func (*FieldTarget) targetNode() {}
func (*IndexTarget) targetNode() {}

func (*VarExpr) exprNode()        {}
func (*LiteralExpr) exprNode()    {}
func (*VarargExpr) exprNode()     {}
func (*UnaryExpr) exprNode()      {}
func (*BinaryExpr) exprNode()     {}
func (*CallExpr) exprNode()       {}
func (*MethodCallExpr) exprNode() {}
func (*FieldExpr) exprNode()      {}
func (*IndexExpr) exprNode()      {}
func (*TableExpr) exprNode()      {}
func (*ClosureExpr) exprNode()    {}

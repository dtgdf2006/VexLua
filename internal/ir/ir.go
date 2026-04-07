package ir

type VarKind uint8

const (
	VarLocal VarKind = iota
	VarUpvalue
	VarGlobal
)

type Function struct {
	Name     string
	Params   []string
	Vararg   bool
	Body     []Stmt
	Locals   int
	Upvalues []UpvalueDesc
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
	Slots  []int
	Values []Expr
}

type BreakStmt struct{}

type AssignStmt struct {
	Targets []AssignTarget
	Values  []Expr
}

type ReturnStmt struct {
	Values []Expr
}

type ExprStmt struct {
	Expr Expr
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
	Slot  int
	Start Expr
	Limit Expr
	Step  Expr
	Body  []Stmt
}

type ForGenericStmt struct {
	IteratorSlot int
	StateSlot    int
	ControlSlot  int
	VarSlots     []int
	Exprs        []Expr
	Body         []Stmt
}

type VarTarget struct {
	Ref VarRef
}

type FieldTarget struct {
	Target Expr
	Name   string
}

type IndexTarget struct {
	Target Expr
	Key    Expr
}

type VarExpr struct {
	Ref VarRef
}

type LiteralExpr struct {
	Value any
}

type VarargExpr struct{}

type UnaryExpr struct {
	Op   string
	Expr Expr
}

type BinaryExpr struct {
	Op    string
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

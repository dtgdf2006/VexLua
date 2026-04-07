package ir

import (
	"fmt"

	"vexlua/internal/frontend"
)

type resolver struct {
	parent      *resolver
	locals      map[string]int
	upvalues    []UpvalueDesc
	upvalueKeys map[string]int
	nextLocal   int
	name        string
	params      []string
	vararg      bool
}

func Lower(chunk *frontend.Chunk) (*Function, error) {
	res := newResolver(nil, "chunk", nil, false)
	body, err := res.lowerBlock(chunk.Statements)
	if err != nil {
		return nil, err
	}
	return &Function{
		Name:     "chunk",
		Params:   nil,
		Vararg:   false,
		Body:     body,
		Locals:   res.nextLocal,
		Upvalues: res.upvalues,
	}, nil
}

func newResolver(parent *resolver, name string, params []string, vararg bool) *resolver {
	r := &resolver{
		parent:      parent,
		locals:      make(map[string]int, len(params)+8),
		upvalues:    make([]UpvalueDesc, 0, 4),
		upvalueKeys: make(map[string]int, 4),
		name:        name,
		params:      append([]string(nil), params...),
		vararg:      vararg,
	}
	for _, param := range params {
		r.declareLocal(param)
	}
	return r
}

func (r *resolver) lowerBlock(stmts []frontend.Stmt) ([]Stmt, error) {
	result := make([]Stmt, 0, len(stmts))
	for _, stmt := range stmts {
		lowered, err := r.lowerStmt(stmt)
		if err != nil {
			return nil, err
		}
		result = append(result, lowered)
	}
	return result, nil
}

func (r *resolver) lowerStmt(stmt frontend.Stmt) (Stmt, error) {
	switch s := stmt.(type) {
	case *frontend.LocalAssignStmt:
		slots := make([]int, 0, len(s.Names))
		for _, name := range s.Names {
			slots = append(slots, r.declareLocal(name))
		}
		values, err := r.lowerExprList(s.Values)
		if err != nil {
			return nil, err
		}
		return &LocalAssignStmt{Slots: slots, Values: values}, nil
	case *frontend.BreakStmt:
		return &BreakStmt{}, nil
	case *frontend.AssignStmt:
		targets := make([]AssignTarget, 0, len(s.Targets))
		for _, targetExpr := range s.Targets {
			target, err := r.lowerTarget(targetExpr)
			if err != nil {
				return nil, err
			}
			targets = append(targets, target)
		}
		values, err := r.lowerExprList(s.Values)
		if err != nil {
			return nil, err
		}
		return &AssignStmt{Targets: targets, Values: values}, nil
	case *frontend.FunctionStmt:
		closure, err := r.lowerFunctionStmt(s)
		if err != nil {
			return nil, err
		}
		return closure, nil
	case *frontend.IfStmt:
		clauses := make([]IfClause, 0, len(s.Clauses))
		for _, clause := range s.Clauses {
			cond, err := r.lowerExpr(clause.Cond)
			if err != nil {
				return nil, err
			}
			body, err := r.lowerNestedBlock(clause.Body)
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, IfClause{Cond: cond, Body: body})
		}
		elseBody, err := r.lowerNestedBlock(s.ElseBody)
		if err != nil {
			return nil, err
		}
		return &IfStmt{Clauses: clauses, ElseBody: elseBody}, nil
	case *frontend.WhileStmt:
		cond, err := r.lowerExpr(s.Cond)
		if err != nil {
			return nil, err
		}
		body, err := r.lowerNestedBlock(s.Body)
		if err != nil {
			return nil, err
		}
		return &WhileStmt{Cond: cond, Body: body}, nil
	case *frontend.RepeatStmt:
		body, err := r.lowerNestedBlock(s.Body)
		if err != nil {
			return nil, err
		}
		cond, err := r.lowerExpr(s.Cond)
		if err != nil {
			return nil, err
		}
		return &RepeatStmt{Body: body, Cond: cond}, nil
	case *frontend.ForNumericStmt:
		start, err := r.lowerExpr(s.Start)
		if err != nil {
			return nil, err
		}
		limit, err := r.lowerExpr(s.Limit)
		if err != nil {
			return nil, err
		}
		step, err := r.lowerExpr(s.Step)
		if err != nil {
			return nil, err
		}
		saved := r.snapshotLocals()
		slot := r.declareLocal(s.Name)
		body, err := r.lowerNestedBlock(s.Body)
		r.locals = saved
		if err != nil {
			return nil, err
		}
		return &ForNumericStmt{Slot: slot, Start: start, Limit: limit, Step: step, Body: body}, nil
	case *frontend.ForGenericStmt:
		exprs, err := r.lowerExprList(s.Exprs)
		if err != nil {
			return nil, err
		}
		saved := r.snapshotLocals()
		iterSlot := r.declareLocal("$iter")
		stateSlot := r.declareLocal("$state")
		controlSlot := r.declareLocal("$control")
		varSlots := make([]int, 0, len(s.Names))
		for _, name := range s.Names {
			varSlots = append(varSlots, r.declareLocal(name))
		}
		body, err := r.lowerNestedBlock(s.Body)
		r.locals = saved
		if err != nil {
			return nil, err
		}
		return &ForGenericStmt{IteratorSlot: iterSlot, StateSlot: stateSlot, ControlSlot: controlSlot, VarSlots: varSlots, Exprs: exprs, Body: body}, nil
	case *frontend.ReturnStmt:
		values, err := r.lowerExprList(s.Values)
		if err != nil {
			return nil, err
		}
		return &ReturnStmt{Values: values}, nil
	case *frontend.ExprStmt:
		expr, err := r.lowerExpr(s.Expr)
		if err != nil {
			return nil, err
		}
		return &ExprStmt{Expr: expr}, nil
	default:
		return nil, fmt.Errorf("unsupported statement %T", stmt)
	}
}

func (r *resolver) lowerNestedBlock(stmts []frontend.Stmt) ([]Stmt, error) {
	saved := r.snapshotLocals()
	body, err := r.lowerBlock(stmts)
	r.locals = saved
	return body, err
}

func (r *resolver) snapshotLocals() map[string]int {
	locals := make(map[string]int, len(r.locals))
	for name, slot := range r.locals {
		locals[name] = slot
	}
	return locals
}

func (r *resolver) lowerFunctionStmt(stmt *frontend.FunctionStmt) (Stmt, error) {
	if stmt.Local {
		slot := r.declareLocal(stmt.Name)
		fn, err := r.lowerFunction(stmt.Name, stmt.Params, stmt.Vararg, stmt.Body)
		if err != nil {
			return nil, err
		}
		return &AssignStmt{Targets: []AssignTarget{&VarTarget{Ref: VarRef{Name: stmt.Name, Kind: VarLocal, Index: slot}}}, Values: []Expr{&ClosureExpr{Fn: fn}}}, nil
	}
	fn, err := r.lowerFunction("function", stmt.Params, stmt.Vararg, stmt.Body)
	if err != nil {
		return nil, err
	}
	target, err := r.lowerTarget(stmt.Target)
	if err != nil {
		return nil, err
	}
	return &AssignStmt{Targets: []AssignTarget{target}, Values: []Expr{&ClosureExpr{Fn: fn}}}, nil
}

func (r *resolver) lowerFunction(name string, params []string, vararg bool, body []frontend.Stmt) (*Function, error) {
	child := newResolver(r, name, params, vararg)
	loweredBody, err := child.lowerBlock(body)
	if err != nil {
		return nil, err
	}
	return &Function{
		Name:     name,
		Params:   append([]string(nil), params...),
		Vararg:   vararg,
		Body:     loweredBody,
		Locals:   child.nextLocal,
		Upvalues: child.upvalues,
	}, nil
}

func (r *resolver) lowerTarget(expr frontend.Expr) (AssignTarget, error) {
	switch target := expr.(type) {
	case *frontend.NameExpr:
		return &VarTarget{Ref: r.resolveName(target.Name)}, nil
	case *frontend.FieldExpr:
		value, err := r.lowerExpr(target.Target)
		if err != nil {
			return nil, err
		}
		return &FieldTarget{Target: value, Name: target.Name}, nil
	case *frontend.IndexExpr:
		value, err := r.lowerExpr(target.Target)
		if err != nil {
			return nil, err
		}
		key, err := r.lowerExpr(target.Key)
		if err != nil {
			return nil, err
		}
		return &IndexTarget{Target: value, Key: key}, nil
	default:
		return nil, fmt.Errorf("invalid assignment target %T", expr)
	}
}

func (r *resolver) lowerExpr(expr frontend.Expr) (Expr, error) {
	switch e := expr.(type) {
	case *frontend.NameExpr:
		return &VarExpr{Ref: r.resolveName(e.Name)}, nil
	case *frontend.NumberExpr:
		return &LiteralExpr{Value: e.Value}, nil
	case *frontend.StringExpr:
		return &LiteralExpr{Value: e.Value}, nil
	case *frontend.BoolExpr:
		return &LiteralExpr{Value: e.Value}, nil
	case *frontend.NilExpr:
		return &LiteralExpr{Value: nil}, nil
	case *frontend.VarargExpr:
		if !r.vararg {
			return nil, fmt.Errorf("cannot use '...' outside vararg function")
		}
		return &VarargExpr{}, nil
	case *frontend.UnaryExpr:
		expr, err := r.lowerExpr(e.Expr)
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: e.Op.String(), Expr: expr}, nil
	case *frontend.BinaryExpr:
		left, err := r.lowerExpr(e.Left)
		if err != nil {
			return nil, err
		}
		right, err := r.lowerExpr(e.Right)
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: e.Op.String(), Left: left, Right: right}, nil
	case *frontend.CallExpr:
		callee, err := r.lowerExpr(e.Callee)
		if err != nil {
			return nil, err
		}
		args := make([]Expr, 0, len(e.Args))
		for _, arg := range e.Args {
			lowered, err := r.lowerExpr(arg)
			if err != nil {
				return nil, err
			}
			args = append(args, lowered)
		}
		return &CallExpr{Callee: callee, Args: args}, nil
	case *frontend.MethodCallExpr:
		receiver, err := r.lowerExpr(e.Receiver)
		if err != nil {
			return nil, err
		}
		args := make([]Expr, 0, len(e.Args))
		for _, arg := range e.Args {
			lowered, err := r.lowerExpr(arg)
			if err != nil {
				return nil, err
			}
			args = append(args, lowered)
		}
		return &MethodCallExpr{Receiver: receiver, Name: e.Name, Args: args}, nil
	case *frontend.FieldExpr:
		target, err := r.lowerExpr(e.Target)
		if err != nil {
			return nil, err
		}
		return &FieldExpr{Target: target, Name: e.Name}, nil
	case *frontend.IndexExpr:
		target, err := r.lowerExpr(e.Target)
		if err != nil {
			return nil, err
		}
		key, err := r.lowerExpr(e.Key)
		if err != nil {
			return nil, err
		}
		return &IndexExpr{Target: target, Key: key}, nil
	case *frontend.TableExpr:
		fields := make([]TableField, 0, len(e.Fields))
		for _, field := range e.Fields {
			value, err := r.lowerExpr(field.Value)
			if err != nil {
				return nil, err
			}
			lowered := TableField{Kind: TableFieldKind(field.Kind), Name: field.Name, Value: value}
			if field.Key != nil {
				key, err := r.lowerExpr(field.Key)
				if err != nil {
					return nil, err
				}
				lowered.Key = key
			}
			fields = append(fields, lowered)
		}
		return &TableExpr{Fields: fields}, nil
	case *frontend.FunctionExpr:
		fn, err := r.lowerFunction("lambda", e.Params, e.Vararg, e.Body)
		if err != nil {
			return nil, err
		}
		return &ClosureExpr{Fn: fn}, nil
	default:
		return nil, fmt.Errorf("unsupported expression %T", expr)
	}
}

func (r *resolver) lowerExprList(exprs []frontend.Expr) ([]Expr, error) {
	values := make([]Expr, 0, len(exprs))
	for _, expr := range exprs {
		lowered, err := r.lowerExpr(expr)
		if err != nil {
			return nil, err
		}
		values = append(values, lowered)
	}
	return values, nil
}

func (r *resolver) declareLocal(name string) int {
	slot := r.nextLocal
	r.locals[name] = slot
	r.nextLocal++
	return slot
}

func (r *resolver) resolveName(name string) VarRef {
	if slot, ok := r.locals[name]; ok {
		return VarRef{Name: name, Kind: VarLocal, Index: slot}
	}
	if idx, ok := r.resolveUpvalue(name); ok {
		return VarRef{Name: name, Kind: VarUpvalue, Index: idx}
	}
	return VarRef{Name: name, Kind: VarGlobal}
}

func (r *resolver) resolveUpvalue(name string) (int, bool) {
	if r.parent == nil {
		return 0, false
	}
	if slot, ok := r.parent.locals[name]; ok {
		return r.addUpvalue(name, true, slot), true
	}
	if idx, ok := r.parent.resolveUpvalue(name); ok {
		return r.addUpvalue(name, false, idx), true
	}
	return 0, false
}

func (r *resolver) addUpvalue(name string, inParentLocal bool, index int) int {
	key := fmt.Sprintf("%s:%t:%d", name, inParentLocal, index)
	if idx, ok := r.upvalueKeys[key]; ok {
		return idx
	}
	idx := len(r.upvalues)
	r.upvalueKeys[key] = idx
	r.upvalues = append(r.upvalues, UpvalueDesc{Name: name, InParentLocal: inParentLocal, Index: index})
	return idx
}

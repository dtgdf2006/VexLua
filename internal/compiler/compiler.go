package compiler

import (
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/frontend"
	"vexlua/internal/ir"
	rt "vexlua/internal/runtime"
)

type Compiler struct {
	runtime *rt.Runtime
}

type funcCompiler struct {
	runtime   *rt.Runtime
	proto     *bytecode.Proto
	nextTemp  int
	maxStack  int
	nextIC    int
	childSlot map[*ir.Function]int
	nilConst  int
	zeroConst int
	loops     []*loopScope
}

type loopScope struct {
	breakJumps []int
}

func New(runtime *rt.Runtime) *Compiler {
	return &Compiler{runtime: runtime}
}

func (c *Compiler) CompileSource(source string) (*bytecode.Proto, error) {
	chunk, err := frontend.Parse(source)
	if err != nil {
		return nil, err
	}
	function, err := ir.Lower(chunk)
	if err != nil {
		return nil, err
	}
	return c.CompileFunction(function)
}

func (c *Compiler) CompileFunction(fn *ir.Function) (*bytecode.Proto, error) {
	fc := &funcCompiler{
		runtime:   c.runtime,
		proto:     bytecode.NewProto(fn.Name, max(1, fn.Locals), 0),
		nextTemp:  fn.Locals,
		maxStack:  max(1, fn.Locals),
		childSlot: make(map[*ir.Function]int),
	}
	fc.proto.Scripted = true
	fc.proto.NumParams = len(fn.Params)
	fc.proto.Vararg = fn.Vararg
	fc.nilConst = fc.proto.AddConstant(rt.NilValue)
	fc.zeroConst = fc.proto.AddConstant(rt.NumberValue(0))
	for _, up := range fn.Upvalues {
		fc.proto.Upvalues = append(fc.proto.Upvalues, bytecode.UpvalueDesc{InParentLocal: up.InParentLocal, Index: uint16(up.Index)})
	}
	for _, stmt := range fn.Body {
		if err := fc.compileStmt(stmt); err != nil {
			return nil, err
		}
	}
	retReg := fc.allocTemp()
	fc.proto.Emit(bytecode.OpLoadConst, uint16(retReg), 0, 0, int32(fc.nilConst))
	fc.proto.Emit(bytecode.OpReturn, uint16(retReg), 0, 0, 0)
	fc.proto.MaxStack = fc.maxStack
	fc.proto.InlineCaches = fc.nextIC
	return fc.proto, nil
}

func (c *funcCompiler) compileStmt(stmt ir.Stmt) error {
	switch s := stmt.(type) {
	case *ir.LocalAssignStmt:
		return c.compileAssignSlots(s.Slots, s.Values)
	case *ir.AssignStmt:
		return c.compileAssignTargets(s.Targets, s.Values)
	case *ir.BreakStmt:
		if len(c.loops) == 0 {
			return fmt.Errorf("break outside loop")
		}
		idx := c.emit(bytecode.OpJump, 0, 0, 0, 0)
		loop := c.loops[len(c.loops)-1]
		loop.breakJumps = append(loop.breakJumps, idx)
		return nil
	case *ir.ReturnStmt:
		return c.compileReturnValues(s.Values)
	case *ir.ExprStmt:
		reg := c.allocTemp()
		return c.compileExprTo(reg, s.Expr)
	case *ir.IfStmt:
		return c.compileIfStmt(s)
	case *ir.WhileStmt:
		return c.compileWhileStmt(s)
	case *ir.RepeatStmt:
		return c.compileRepeatStmt(s)
	case *ir.ForNumericStmt:
		return c.compileForNumericStmt(s)
	case *ir.ForGenericStmt:
		return c.compileForGenericStmt(s)
	}
	return fmt.Errorf("unsupported statement %T", stmt)
}

func (c *funcCompiler) compileIfStmt(stmt *ir.IfStmt) error {
	endJumps := make([]int, 0, len(stmt.Clauses))
	for _, clause := range stmt.Clauses {
		condReg := c.allocTemp()
		if err := c.compileExprTo(condReg, clause.Cond); err != nil {
			return err
		}
		jumpFalse := c.emit(bytecode.OpJumpIfFalse, uint16(condReg), 0, 0, 0)
		for _, bodyStmt := range clause.Body {
			if err := c.compileStmt(bodyStmt); err != nil {
				return err
			}
		}
		endJumps = append(endJumps, c.emit(bytecode.OpJump, 0, 0, 0, 0))
		c.patchJump(jumpFalse, len(c.proto.Code))
	}
	for _, bodyStmt := range stmt.ElseBody {
		if err := c.compileStmt(bodyStmt); err != nil {
			return err
		}
	}
	end := len(c.proto.Code)
	for _, jump := range endJumps {
		c.patchJump(jump, end)
	}
	return nil
}

func (c *funcCompiler) compileWhileStmt(stmt *ir.WhileStmt) error {
	loop := c.pushLoop()
	loopStart := len(c.proto.Code)
	condReg := c.allocTemp()
	if err := c.compileExprTo(condReg, stmt.Cond); err != nil {
		return err
	}
	jumpFalse := c.emit(bytecode.OpJumpIfFalse, uint16(condReg), 0, 0, 0)
	for _, bodyStmt := range stmt.Body {
		if err := c.compileStmt(bodyStmt); err != nil {
			return err
		}
	}
	c.emit(bytecode.OpJump, 0, 0, 0, int32(loopStart))
	loopEnd := len(c.proto.Code)
	c.patchJump(jumpFalse, loopEnd)
	c.popLoop(loopEnd, loop)
	return nil
}

func (c *funcCompiler) compileRepeatStmt(stmt *ir.RepeatStmt) error {
	loop := c.pushLoop()
	loopStart := len(c.proto.Code)
	for _, bodyStmt := range stmt.Body {
		if err := c.compileStmt(bodyStmt); err != nil {
			return err
		}
	}
	condReg := c.allocTemp()
	if err := c.compileExprTo(condReg, stmt.Cond); err != nil {
		return err
	}
	c.emit(bytecode.OpJumpIfFalse, uint16(condReg), 0, 0, int32(loopStart))
	c.popLoop(len(c.proto.Code), loop)
	return nil
}

func (c *funcCompiler) compileForNumericStmt(stmt *ir.ForNumericStmt) error {
	loop := c.pushLoop()
	if err := c.compileExprTo(stmt.Slot, stmt.Start); err != nil {
		return err
	}
	limitReg := c.allocTemp()
	if err := c.compileExprTo(limitReg, stmt.Limit); err != nil {
		return err
	}
	stepReg := c.allocTemp()
	if err := c.compileExprTo(stepReg, stmt.Step); err != nil {
		return err
	}
	zeroReg := c.allocTemp()
	c.proto.Emit(bytecode.OpLoadConst, uint16(zeroReg), 0, 0, int32(c.zeroConst))
	condReg := c.allocTemp()
	loopStart := len(c.proto.Code)
	c.proto.Emit(bytecode.OpLess, uint16(condReg), uint16(zeroReg), uint16(stepReg), 0)
	negativeBranch := c.emit(bytecode.OpJumpIfFalse, uint16(condReg), 0, 0, 0)
	c.proto.Emit(bytecode.OpLessEqual, uint16(condReg), uint16(stmt.Slot), uint16(limitReg), 0)
	exitPositive := c.emit(bytecode.OpJumpIfFalse, uint16(condReg), 0, 0, 0)
	afterCond := c.emit(bytecode.OpJump, 0, 0, 0, 0)
	c.patchJump(negativeBranch, len(c.proto.Code))
	c.proto.Emit(bytecode.OpLessEqual, uint16(condReg), uint16(limitReg), uint16(stmt.Slot), 0)
	exitNegative := c.emit(bytecode.OpJumpIfFalse, uint16(condReg), 0, 0, 0)
	c.patchJump(afterCond, len(c.proto.Code))
	for _, bodyStmt := range stmt.Body {
		if err := c.compileStmt(bodyStmt); err != nil {
			return err
		}
	}
	c.proto.Emit(bytecode.OpAdd, uint16(stmt.Slot), uint16(stmt.Slot), uint16(stepReg), 0)
	c.emit(bytecode.OpJump, 0, 0, 0, int32(loopStart))
	loopEnd := len(c.proto.Code)
	c.patchJump(exitPositive, loopEnd)
	c.patchJump(exitNegative, loopEnd)
	c.popLoop(loopEnd, loop)
	return nil
}

func (c *funcCompiler) compileForGenericStmt(stmt *ir.ForGenericStmt) error {
	if kind, stateExpr, ok := c.detectGenericIteratorIntrinsic(stmt.Exprs); ok {
		return c.compileIntrinsicForGenericStmt(stmt, kind, stateExpr)
	}
	if err := c.compileAssignSlots([]int{stmt.IteratorSlot, stmt.StateSlot, stmt.ControlSlot}, stmt.Exprs); err != nil {
		return err
	}
	loop := c.pushLoop()
	loopStart := len(c.proto.Code)
	c.proto.Emit(bytecode.OpCallMulti, uint16(stmt.ControlSlot), uint16(stmt.IteratorSlot), uint16(stmt.StateSlot), bytecode.PackCallCounts(2, len(stmt.VarSlots)+1))
	exitJump := c.emit(bytecode.OpJumpIfFalse, uint16(stmt.ControlSlot), 0, 0, 0)
	for i := len(stmt.VarSlots) - 1; i >= 0; i-- {
		slot := stmt.VarSlots[i]
		c.proto.Emit(bytecode.OpMove, uint16(slot), uint16(stmt.ControlSlot+i), 0, 0)
	}
	for _, bodyStmt := range stmt.Body {
		if err := c.compileStmt(bodyStmt); err != nil {
			return err
		}
	}
	c.emit(bytecode.OpJump, 0, 0, 0, int32(loopStart))
	loopEnd := len(c.proto.Code)
	c.patchJump(exitJump, loopEnd)
	c.popLoop(loopEnd, loop)
	return nil
}

func (c *funcCompiler) compileIntrinsicForGenericStmt(stmt *ir.ForGenericStmt, kind string, stateExpr ir.Expr) error {
	c.proto.Emit(bytecode.OpLoadConst, uint16(stmt.IteratorSlot), 0, 0, int32(c.nilConst))
	if err := c.compileExprTo(stmt.StateSlot, stateExpr); err != nil {
		return err
	}
	if kind == "ipairs" {
		c.proto.Emit(bytecode.OpLoadConst, uint16(stmt.ControlSlot), 0, 0, int32(c.zeroConst))
	} else {
		c.proto.Emit(bytecode.OpLoadConst, uint16(stmt.ControlSlot), 0, 0, int32(c.nilConst))
	}
	loop := c.pushLoop()
	loopStart := len(c.proto.Code)
	resultCount := len(stmt.VarSlots) + 1
	switch kind {
	case "pairs":
		c.proto.Emit(bytecode.OpIterPairs, uint16(stmt.ControlSlot), uint16(stmt.StateSlot), uint16(resultCount), 0)
	case "ipairs":
		c.proto.Emit(bytecode.OpIterIPairs, uint16(stmt.ControlSlot), uint16(stmt.StateSlot), uint16(resultCount), 0)
	default:
		return fmt.Errorf("unsupported iterator intrinsic %q", kind)
	}
	exitJump := c.emit(bytecode.OpJumpIfFalse, uint16(stmt.ControlSlot), 0, 0, 0)
	for i := len(stmt.VarSlots) - 1; i >= 0; i-- {
		slot := stmt.VarSlots[i]
		c.proto.Emit(bytecode.OpMove, uint16(slot), uint16(stmt.ControlSlot+i), 0, 0)
	}
	for _, bodyStmt := range stmt.Body {
		if err := c.compileStmt(bodyStmt); err != nil {
			return err
		}
	}
	c.emit(bytecode.OpJump, 0, 0, 0, int32(loopStart))
	loopEnd := len(c.proto.Code)
	c.patchJump(exitJump, loopEnd)
	c.popLoop(loopEnd, loop)
	return nil
}

func (c *funcCompiler) detectGenericIteratorIntrinsic(exprs []ir.Expr) (string, ir.Expr, bool) {
	if len(exprs) != 1 {
		return "", nil, false
	}
	call, ok := exprs[0].(*ir.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", nil, false
	}
	callee, ok := call.Callee.(*ir.VarExpr)
	if !ok || callee.Ref.Kind != ir.VarGlobal {
		return "", nil, false
	}
	switch callee.Ref.Name {
	case "pairs", "ipairs":
		return callee.Ref.Name, call.Args[0], true
	default:
		return "", nil, false
	}
}

func (c *funcCompiler) compileExprTo(target int, expr ir.Expr) error {
	switch e := expr.(type) {
	case *ir.VarExpr:
		switch e.Ref.Kind {
		case ir.VarLocal:
			if target != e.Ref.Index {
				c.proto.Emit(bytecode.OpMove, uint16(target), uint16(e.Ref.Index), 0, 0)
			}
		case ir.VarUpvalue:
			c.proto.Emit(bytecode.OpLoadUpvalue, uint16(target), uint16(e.Ref.Index), 0, 0)
		case ir.VarGlobal:
			sym := c.runtime.InternSymbol(e.Ref.Name)
			c.proto.Emit(bytecode.OpLoadGlobal, uint16(target), 0, 0, int32(sym))
		}
		return nil
	case *ir.LiteralExpr:
		value, err := c.literalValue(e.Value)
		if err != nil {
			return err
		}
		idx := c.proto.AddConstant(value)
		c.proto.Emit(bytecode.OpLoadConst, uint16(target), 0, 0, int32(idx))
		return nil
	case *ir.VarargExpr:
		c.proto.Emit(bytecode.OpVararg, uint16(target), 1, 0, 0)
		return nil
	case *ir.UnaryExpr:
		switch e.Op {
		case "MINUS":
			zeroReg := c.allocTemp()
			c.proto.Emit(bytecode.OpLoadConst, uint16(zeroReg), 0, 0, int32(c.zeroConst))
			if err := c.compileExprTo(target, e.Expr); err != nil {
				return err
			}
			c.proto.Emit(bytecode.OpSub, uint16(target), uint16(zeroReg), uint16(target), 0)
		case "NOT":
			if err := c.compileExprTo(target, e.Expr); err != nil {
				return err
			}
			c.proto.Emit(bytecode.OpNot, uint16(target), uint16(target), 0, 0)
		case "HASH":
			if err := c.compileExprTo(target, e.Expr); err != nil {
				return err
			}
			c.proto.Emit(bytecode.OpLen, uint16(target), uint16(target), 0, 0)
		default:
			return fmt.Errorf("unsupported unary operator %s", e.Op)
		}
		return nil
	case *ir.BinaryExpr:
		switch e.Op {
		case "AND":
			return c.compileAndExpr(target, e)
		case "OR":
			return c.compileOrExpr(target, e)
		}
		if err := c.compileExprTo(target, e.Left); err != nil {
			return err
		}
		right := c.allocTemp()
		if err := c.compileExprTo(right, e.Right); err != nil {
			return err
		}
		switch e.Op {
		case "PLUS":
			c.proto.Emit(bytecode.OpAdd, uint16(target), uint16(target), uint16(right), 0)
		case "MINUS":
			c.proto.Emit(bytecode.OpSub, uint16(target), uint16(target), uint16(right), 0)
		case "STAR":
			c.proto.Emit(bytecode.OpMul, uint16(target), uint16(target), uint16(right), 0)
		case "SLASH":
			c.proto.Emit(bytecode.OpDiv, uint16(target), uint16(target), uint16(right), 0)
		case "PERCENT":
			c.proto.Emit(bytecode.OpMod, uint16(target), uint16(target), uint16(right), 0)
		case "CARET":
			c.proto.Emit(bytecode.OpPow, uint16(target), uint16(target), uint16(right), 0)
		case "CONCAT":
			c.proto.Emit(bytecode.OpConcat, uint16(target), uint16(target), uint16(right), 0)
		case "EQ":
			c.proto.Emit(bytecode.OpEqual, uint16(target), uint16(target), uint16(right), 0)
		case "NE":
			c.proto.Emit(bytecode.OpEqual, uint16(target), uint16(target), uint16(right), 0)
			c.proto.Emit(bytecode.OpNot, uint16(target), uint16(target), 0, 0)
		case "LT":
			c.proto.Emit(bytecode.OpLess, uint16(target), uint16(target), uint16(right), 0)
		case "LE":
			c.proto.Emit(bytecode.OpLessEqual, uint16(target), uint16(target), uint16(right), 0)
		case "GT":
			c.proto.Emit(bytecode.OpLess, uint16(target), uint16(right), uint16(target), 0)
		case "GE":
			c.proto.Emit(bytecode.OpLessEqual, uint16(target), uint16(right), uint16(target), 0)
		default:
			return fmt.Errorf("unsupported binary operator %s", e.Op)
		}
		return nil
	case *ir.FieldExpr:
		objReg := c.allocTemp()
		if err := c.compileExprTo(objReg, e.Target); err != nil {
			return err
		}
		sym := c.runtime.InternSymbol(e.Name)
		ic := c.nextIC
		c.nextIC++
		c.proto.Emit(bytecode.OpGetField, uint16(target), uint16(objReg), uint16(ic), int32(sym))
		return nil
	case *ir.IndexExpr:
		objReg := c.allocTemp()
		if err := c.compileExprTo(objReg, e.Target); err != nil {
			return err
		}
		keyReg := c.allocTemp()
		if err := c.compileExprTo(keyReg, e.Key); err != nil {
			return err
		}
		c.proto.Emit(bytecode.OpGetTable, uint16(target), uint16(objReg), uint16(keyReg), 0)
		return nil
	case *ir.TableExpr:
		c.proto.Emit(bytecode.OpNewTable, uint16(target), 0, 0, int32(len(e.Fields)))
		arrayIndex := 1
		lastField := len(e.Fields) - 1
		for index, field := range e.Fields {
			switch field.Kind {
			case ir.TableFieldNamed:
				valueReg := c.allocTemp()
				if err := c.compileExprTo(valueReg, field.Value); err != nil {
					return err
				}
				sym := c.runtime.InternSymbol(field.Name)
				c.proto.Emit(bytecode.OpSetField, uint16(target), uint16(valueReg), 0, int32(sym))
			case ir.TableFieldExpr:
				keyReg := c.allocTemp()
				if err := c.compileExprTo(keyReg, field.Key); err != nil {
					return err
				}
				valueReg := c.allocTemp()
				if err := c.compileExprTo(valueReg, field.Value); err != nil {
					return err
				}
				c.proto.Emit(bytecode.OpSetTable, uint16(target), uint16(keyReg), uint16(valueReg), 0)
			case ir.TableFieldArray:
				if index == lastField && c.isMultiExpr(field.Value) {
					if err := c.compilePendingExpr(field.Value); err != nil {
						return err
					}
					c.proto.Emit(bytecode.OpAppendTable, uint16(target), uint16(arrayIndex), 0, 0)
					continue
				}
				keyReg := c.allocTemp()
				keyConst := c.proto.AddConstant(rt.NumberValue(float64(arrayIndex)))
				c.proto.Emit(bytecode.OpLoadConst, uint16(keyReg), 0, 0, int32(keyConst))
				valueReg := c.allocTemp()
				if err := c.compileExprTo(valueReg, field.Value); err != nil {
					return err
				}
				c.proto.Emit(bytecode.OpSetTable, uint16(target), uint16(keyReg), uint16(valueReg), 0)
				arrayIndex++
			}
		}
		return nil
	case *ir.CallExpr:
		if c.isCoroutineYield(e) {
			return c.compileYieldExpr(target, e, 1)
		}
		return c.compileCallExprSingle(target, e)
	case *ir.MethodCallExpr:
		return c.compileMethodCallExprSingle(target, e)
	case *ir.ClosureExpr:
		childIndex, err := c.childIndex(e.Fn)
		if err != nil {
			return err
		}
		c.proto.Emit(bytecode.OpClosure, uint16(target), 0, 0, int32(childIndex))
		return nil
	default:
		return fmt.Errorf("unsupported expression %T", expr)
	}
}

func (c *funcCompiler) compileAndExpr(target int, expr *ir.BinaryExpr) error {
	if err := c.compileExprTo(target, expr.Left); err != nil {
		return err
	}
	endJump := c.emit(bytecode.OpJumpIfFalse, uint16(target), 0, 0, 0)
	if err := c.compileExprTo(target, expr.Right); err != nil {
		return err
	}
	c.patchJump(endJump, len(c.proto.Code))
	return nil
}

func (c *funcCompiler) compileAssignSlots(slots []int, values []ir.Expr) error {
	resultStart := c.reserveTemps(len(slots))
	if err := c.compileExprListInto(resultStart, values, len(slots)); err != nil {
		return err
	}
	for i, slot := range slots {
		if slot != resultStart+i {
			c.proto.Emit(bytecode.OpMove, uint16(slot), uint16(resultStart+i), 0, 0)
		}
	}
	return nil
}

func (c *funcCompiler) compileAssignTargets(targets []ir.AssignTarget, values []ir.Expr) error {
	resultStart := c.reserveTemps(len(targets))
	if err := c.compileExprListInto(resultStart, values, len(targets)); err != nil {
		return err
	}
	for i, target := range targets {
		valueReg := resultStart + i
		switch target := target.(type) {
		case *ir.VarTarget:
			switch target.Ref.Kind {
			case ir.VarLocal:
				if target.Ref.Index != valueReg {
					c.proto.Emit(bytecode.OpMove, uint16(target.Ref.Index), uint16(valueReg), 0, 0)
				}
			case ir.VarUpvalue:
				c.proto.Emit(bytecode.OpStoreUpvalue, uint16(valueReg), uint16(target.Ref.Index), 0, 0)
			case ir.VarGlobal:
				sym := c.runtime.InternSymbol(target.Ref.Name)
				c.proto.Emit(bytecode.OpStoreGlobal, uint16(valueReg), 0, 0, int32(sym))
			}
		case *ir.FieldTarget:
			objReg := c.allocTemp()
			if err := c.compileExprTo(objReg, target.Target); err != nil {
				return err
			}
			sym := c.runtime.InternSymbol(target.Name)
			c.proto.Emit(bytecode.OpSetField, uint16(objReg), uint16(valueReg), 0, int32(sym))
		case *ir.IndexTarget:
			objReg := c.allocTemp()
			if err := c.compileExprTo(objReg, target.Target); err != nil {
				return err
			}
			keyReg := c.allocTemp()
			if err := c.compileExprTo(keyReg, target.Key); err != nil {
				return err
			}
			c.proto.Emit(bytecode.OpSetTable, uint16(objReg), uint16(keyReg), uint16(valueReg), 0)
		}
	}
	return nil
}

func (c *funcCompiler) compileReturnValues(values []ir.Expr) error {
	if len(values) == 0 {
		c.proto.Emit(bytecode.OpReturnMulti, 0, 0, 0, 0)
		return nil
	}
	prefixCount := len(values)
	if c.isMultiExpr(values[len(values)-1]) {
		prefixCount--
	}
	start := c.reserveTemps(prefixCount)
	for i := 0; i < prefixCount; i++ {
		if err := c.compileExprTo(start+i, values[i]); err != nil {
			return err
		}
	}
	if prefixCount < len(values) {
		last := values[len(values)-1]
		if err := c.compilePendingExpr(last); err != nil {
			return err
		}
		c.proto.Emit(bytecode.OpReturnAppendPending, uint16(start), uint16(prefixCount), 0, 0)
		return nil
	}
	c.proto.Emit(bytecode.OpReturnMulti, uint16(start), uint16(prefixCount), 0, 0)
	return nil
}

func (c *funcCompiler) compileExprListInto(start int, values []ir.Expr, want int) error {
	if want == 0 {
		return nil
	}
	if len(values) == 0 {
		for i := 0; i < want; i++ {
			c.proto.Emit(bytecode.OpLoadConst, uint16(start+i), 0, 0, int32(c.nilConst))
		}
		return nil
	}
	for i, expr := range values {
		remaining := want - i
		if remaining <= 0 {
			break
		}
		if i == len(values)-1 && c.isMultiExpr(expr) {
			return c.compileExprToCount(start+i, expr, remaining)
		}
		if err := c.compileExprTo(start+i, expr); err != nil {
			return err
		}
	}
	for i := len(values); i < want; i++ {
		c.proto.Emit(bytecode.OpLoadConst, uint16(start+i), 0, 0, int32(c.nilConst))
	}
	return nil
}

func (c *funcCompiler) compileExprToCount(start int, expr ir.Expr, count int) error {
	switch e := expr.(type) {
	case *ir.CallExpr:
		if c.isCoroutineYield(e) {
			return c.compileYieldExpr(start, e, count)
		}
		return c.compileCallExprCount(start, e, count)
	case *ir.MethodCallExpr:
		return c.compileMethodCallExprCount(start, e, count)
	case *ir.VarargExpr:
		c.proto.Emit(bytecode.OpVararg, uint16(start), uint16(count), 0, 0)
		return nil
	default:
		if err := c.compileExprTo(start, expr); err != nil {
			return err
		}
		for i := 1; i < count; i++ {
			c.proto.Emit(bytecode.OpLoadConst, uint16(start+i), 0, 0, int32(c.nilConst))
		}
		return nil
	}
}

func (c *funcCompiler) compilePendingExpr(expr ir.Expr) error {
	switch e := expr.(type) {
	case *ir.CallExpr:
		if c.isCoroutineYield(e) {
			return c.compileYieldExpr(0, e, 0)
		}
		return c.compileCallExprCount(0, e, 0)
	case *ir.MethodCallExpr:
		return c.compileMethodCallExprCount(0, e, 0)
	case *ir.VarargExpr:
		c.proto.Emit(bytecode.OpVararg, 0, 0, 0, 0)
		return nil
	default:
		return c.compileExprToCount(c.reserveTemps(1), expr, 1)
	}
}

func (c *funcCompiler) compileYieldExpr(resumeStart int, expr *ir.CallExpr, resumeCount int) error {
	yieldStart, yieldCount, appendPending, err := c.compileCallArgs(expr.Args)
	if err != nil {
		return err
	}
	c.proto.Emit(bytecode.OpYield, uint16(resumeStart), uint16(yieldStart), 0, bytecode.PackCallCountsWithPending(yieldCount, resumeCount, appendPending))
	return nil
}

func (c *funcCompiler) compileCallExprSingle(target int, expr *ir.CallExpr) error {
	return c.compileCallExprCount(target, expr, 1)
}

func (c *funcCompiler) compileCallExprCount(target int, expr *ir.CallExpr, count int) error {
	calleeReg := c.allocTemp()
	if err := c.compileExprTo(calleeReg, expr.Callee); err != nil {
		return err
	}
	argStart, argCount, appendPending, err := c.compileCallArgs(expr.Args)
	if err != nil {
		return err
	}
	if count == 1 && !appendPending {
		c.proto.Emit(bytecode.OpCall, uint16(target), uint16(calleeReg), uint16(argStart), int32(argCount))
		return nil
	}
	c.proto.Emit(bytecode.OpCallMulti, uint16(target), uint16(calleeReg), uint16(argStart), bytecode.PackCallCountsWithPending(argCount, count, appendPending))
	return nil
}

func (c *funcCompiler) compileMethodCallExprSingle(target int, expr *ir.MethodCallExpr) error {
	return c.compileMethodCallExprCount(target, expr, 1)
}

func (c *funcCompiler) compileMethodCallExprCount(target int, expr *ir.MethodCallExpr, count int) error {
	receiverReg := c.allocTemp()
	if err := c.compileExprTo(receiverReg, expr.Receiver); err != nil {
		return err
	}
	calleeReg, argStart, argCount, appendPending, err := c.compileMethodCallWindow(receiverReg, expr.Name, expr.Args)
	if err != nil {
		return err
	}
	if count == 1 && !appendPending {
		c.proto.Emit(bytecode.OpCall, uint16(target), uint16(calleeReg), uint16(argStart), int32(argCount))
		return nil
	}
	c.proto.Emit(bytecode.OpCallMulti, uint16(target), uint16(calleeReg), uint16(argStart), bytecode.PackCallCountsWithPending(argCount, count, appendPending))
	return nil
}

func (c *funcCompiler) compileCallArgs(args []ir.Expr) (int, int, bool, error) {
	appendPending := len(args) > 0 && c.isMultiExpr(args[len(args)-1])
	fixedArgs := len(args)
	if appendPending {
		fixedArgs--
	}
	argStart := c.reserveTemps(fixedArgs)
	for i := 0; i < fixedArgs; i++ {
		if err := c.compileExprTo(argStart+i, args[i]); err != nil {
			return 0, 0, false, err
		}
	}
	if appendPending {
		if err := c.compilePendingExpr(args[len(args)-1]); err != nil {
			return 0, 0, false, err
		}
	}
	return argStart, fixedArgs, appendPending, nil
}

func (c *funcCompiler) compileMethodCallWindow(receiverReg int, name string, args []ir.Expr) (int, int, int, bool, error) {
	appendPending := len(args) > 0 && c.isMultiExpr(args[len(args)-1])
	fixedArgs := len(args) + 1
	if appendPending {
		fixedArgs--
	}
	windowStart := c.reserveTemps(fixedArgs + 1)
	calleeReg := windowStart
	argStart := windowStart + 1
	sym := c.runtime.InternSymbol(name)
	ic := c.nextIC
	c.nextIC++
	c.proto.Emit(bytecode.OpSelf, uint16(calleeReg), uint16(receiverReg), uint16(ic), int32(sym))
	limit := len(args)
	if appendPending {
		limit--
	}
	for i := 0; i < limit; i++ {
		if err := c.compileExprTo(argStart+1+i, args[i]); err != nil {
			return 0, 0, 0, false, err
		}
	}
	if appendPending {
		if err := c.compilePendingExpr(args[len(args)-1]); err != nil {
			return 0, 0, 0, false, err
		}
	}
	return calleeReg, argStart, fixedArgs, appendPending, nil
}

func (c *funcCompiler) reserveTemps(count int) int {
	start := c.nextTemp
	for i := 0; i < count; i++ {
		c.allocTemp()
	}
	return start
}

func (c *funcCompiler) isMultiExpr(expr ir.Expr) bool {
	switch expr.(type) {
	case *ir.CallExpr, *ir.MethodCallExpr, *ir.VarargExpr:
		return true
	default:
		return false
	}
}

func (c *funcCompiler) compileOrExpr(target int, expr *ir.BinaryExpr) error {
	if err := c.compileExprTo(target, expr.Left); err != nil {
		return err
	}
	endJump := c.emit(bytecode.OpJumpIfTrue, uint16(target), 0, 0, 0)
	if err := c.compileExprTo(target, expr.Right); err != nil {
		return err
	}
	c.patchJump(endJump, len(c.proto.Code))
	return nil
}

func (c *funcCompiler) pushLoop() *loopScope {
	loop := &loopScope{breakJumps: make([]int, 0, 2)}
	c.loops = append(c.loops, loop)
	return loop
}

func (c *funcCompiler) popLoop(target int, loop *loopScope) {
	for _, jump := range loop.breakJumps {
		c.patchJump(jump, target)
	}
	c.loops = c.loops[:len(c.loops)-1]
}

func (c *funcCompiler) childIndex(fn *ir.Function) (int, error) {
	if idx, ok := c.childSlot[fn]; ok {
		return idx, nil
	}
	child, err := New(c.runtime).CompileFunction(fn)
	if err != nil {
		return 0, err
	}
	idx := c.proto.AddChild(child)
	c.childSlot[fn] = idx
	return idx, nil
}

func (c *funcCompiler) allocTemp() int {
	reg := c.nextTemp
	c.nextTemp++
	if c.nextTemp > c.maxStack {
		c.maxStack = c.nextTemp
	}
	return reg
}

func (c *funcCompiler) literalValue(v any) (rt.Value, error) {
	switch value := v.(type) {
	case nil:
		return rt.NilValue, nil
	case bool:
		return rt.BoolValue(value), nil
	case float64:
		return rt.NumberValue(value), nil
	case string:
		return c.runtime.StringValue(value), nil
	default:
		return rt.NilValue, fmt.Errorf("unsupported literal %T", v)
	}
}

func (c *funcCompiler) isCoroutineYield(call *ir.CallExpr) bool {
	field, ok := call.Callee.(*ir.FieldExpr)
	if !ok || field.Name != "yield" {
		return false
	}
	callee, ok := field.Target.(*ir.VarExpr)
	if !ok {
		return false
	}
	return callee.Ref.Kind == ir.VarGlobal && callee.Ref.Name == "coroutine"
}

func (c *funcCompiler) emit(op bytecode.Op, a, b, cArg uint16, d int32) int {
	idx := len(c.proto.Code)
	c.proto.Emit(op, a, b, cArg, d)
	return idx
}

func (c *funcCompiler) patchJump(index int, target int) {
	c.proto.Code[index].D = int32(target)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

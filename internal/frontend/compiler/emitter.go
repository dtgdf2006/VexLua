package compiler

import (
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/frontend/lexer"
)

const fieldsPerFlush = 50

// ChunkEmitter lowers Bound IR into bytecode.Proto trees.
type ChunkEmitter struct{}

// NewEmitter constructs the bytecode emission stage for the source frontend.
func NewEmitter() Emitter {
	return &ChunkEmitter{}
}

// EmitChunk emits one lowered bound chunk into a validated bytecode proto.
func EmitChunk(chunk *BoundChunk) (*bytecode.Proto, error) {
	return (&ChunkEmitter{}).EmitChunk(chunk)
}

// EmitChunk emits one lowered bound chunk into a validated bytecode proto.
func (emitterStage *ChunkEmitter) EmitChunk(chunk *BoundChunk) (*bytecode.Proto, error) {
	if chunk == nil {
		return nil, lexer.Errorf(lexer.PhaseEmit, lexer.Span{}, "bound chunk is nil")
	}
	if chunk.Func == nil {
		return nil, lexer.Errorf(lexer.PhaseEmit, chunk.SpanRange(), "bound chunk function is nil")
	}
	emitter := newFuncEmitter(chunk, chunk.Func, true)
	return emitter.emit()
}

type funcEmitter struct {
	chunk       *BoundChunk
	function    *BoundFunc
	root        bool
	builder     *ProtoBuilder
	localSlots  map[SymbolID]int
	locals      []SymbolID
	debugLocals map[SymbolID]int
	upvalues    map[SymbolID]int
	scopes      []scopeFrame
	loops       []loopFrame
	paramsCount int
	tempTop     int
	maxStack    int
	usesOpenTop bool
}

type scopeFrame struct {
	scopeID     ScopeID
	baseLocals  int
	hasUpvalues bool
}

type loopFrame struct {
	scopeDepth int
	closeSlot  int
	breaks     JumpList
}

type preparedTargetKind uint8

const (
	preparedTargetSymbol preparedTargetKind = iota
	preparedTargetTable
)

type preparedTarget struct {
	kind       preparedTargetKind
	ref        NameRef
	receiver   int
	keyOperand int
}

func newFuncEmitter(chunk *BoundChunk, function *BoundFunc, root bool) *funcEmitter {
	builder := NewProtoBuilder(chunk.Name)
	lineDefined := 0
	lastLineDefined := 0
	if !root {
		span := function.SpanRange()
		lineDefined = lineOf(span)
		lastLineDefined = span.End.Line
	}
	builder.SetSource(chunk.Name)
	builder.SetLines(lineDefined, lastLineDefined)
	builder.SetSignature(uint8(len(function.Params)), function.HasVararg, uint8(len(function.Captures)))
	for index, param := range function.Params {
		_ = index
		_ = param
	}
	upvalues := make(map[SymbolID]int, len(function.Captures))
	for index, capture := range function.Captures {
		upvalues[capture.Symbol] = index
		builder.AddUpvalueName(capture.Name)
	}
	localSlots := make(map[SymbolID]int, len(function.Params)+len(function.Locals))
	for index, param := range function.Params {
		localSlots[param] = index
	}
	emitter := &funcEmitter{
		chunk:       chunk,
		function:    function,
		root:        root,
		builder:     builder,
		localSlots:  localSlots,
		debugLocals: make(map[SymbolID]int, len(function.Params)+len(function.Locals)),
		upvalues:    upvalues,
		paramsCount: len(function.Params),
		tempTop:     len(function.Params),
		maxStack:    maxInt(2, len(function.Params)),
	}
	for _, param := range function.Params {
		emitter.declareDebugLocal(param)
		emitter.activateDebugLocal(param, 0)
	}
	return emitter
}

func (emitter *funcEmitter) emit() (*bytecode.Proto, error) {
	if emitter.function == nil || emitter.function.Body == nil {
		return nil, emitter.errorf(lexer.Span{}, "function body is nil")
	}
	frame, err := emitter.enterScope(emitter.function.Body.Scope, false)
	if err != nil {
		return nil, err
	}
	if err := emitter.emitStats(emitter.function.Body.Stats); err != nil {
		return nil, err
	}
	emitter.leaveScope(frame)
	for _, param := range emitter.function.Params {
		emitter.closeDebugLocal(param, emitter.currentPC())
	}
	returnLine := lineEndOf(emitter.function.SpanRange())
	emitter.builder.EmitABC(bytecode.OP_RETURN, 0, 1, 0, returnLine)
	if emitter.maxStack > bytecode.NoReg {
		return nil, emitter.errorf(emitter.function.SpanRange(), "function requires %d registers, maximum is %d", emitter.maxStack, bytecode.NoReg)
	}
	emitter.builder.SetMaxStackSize(uint8(maxInt(2, emitter.maxStack)))
	return emitter.builder.Finish()
}

func (emitter *funcEmitter) emitStats(stats []BoundStat) error {
	for _, stat := range stats {
		if err := emitter.emitStat(stat); err != nil {
			return err
		}
		emitter.releaseTemps(emitter.activeBase())
	}
	return nil
}

func (emitter *funcEmitter) emitStat(stat BoundStat) error {
	switch typed := stat.(type) {
	case nil:
		return nil
	case BoundLocalDeclStat:
		return emitter.emitLocalDeclStat(typed)
	case BoundAssignStat:
		return emitter.emitAssignStat(typed)
	case BoundCallStat:
		return emitter.emitCallStat(typed)
	case BoundIfStat:
		return emitter.emitIfStat(typed)
	case BoundWhileStat:
		return emitter.emitWhileStat(typed)
	case BoundRepeatStat:
		return emitter.emitRepeatStat(typed)
	case BoundNumericForStat:
		return emitter.emitNumericForStat(typed)
	case BoundGenericForStat:
		return emitter.emitGenericForStat(typed)
	case BoundDoStat:
		return emitter.emitDoStat(typed)
	case BoundBreakStat:
		return emitter.emitBreakStat(typed)
	case BoundReturnStat:
		return emitter.emitReturnStat(typed)
	default:
		return emitter.errorf(stat.SpanRange(), "unsupported statement %T", stat)
	}
}

func (emitter *funcEmitter) emitLocalDeclStat(stat BoundLocalDeclStat) error {
	if len(stat.Names) == 0 {
		for _, value := range stat.Values {
			if err := emitter.emitDiscardExpr(value); err != nil {
				return err
			}
		}
		return nil
	}
	valueStart := emitter.activeBase()
	if err := emitter.ensureRegister(valueStart + len(stat.Names)); err != nil {
		return err
	}
	mark := emitter.tempTop
	emitter.tempTop = valueStart
	declStartPC := emitter.currentPC()
	if err := emitter.emitAdjustedExprs(stat.Values, valueStart, len(stat.Names)); err != nil {
		return err
	}
	declEndPC := emitter.currentPC()
	if line, ok := emitter.localFunctionDeclEndLine(stat); ok {
		for pc := declStartPC; pc < declEndPC; pc++ {
			emitter.builder.lineInfo[pc] = line
		}
	}
	for index, symbolID := range stat.Names {
		slot, err := emitter.allocateLocal(symbolID)
		if err != nil {
			return err
		}
		if slot != valueStart+index {
			return emitter.errorf(stat.SpanRange(), "local %d assigned unexpected slot %d, want %d", symbolID, slot, valueStart+index)
		}
	}
	for _, symbolID := range stat.Names {
		emitter.activateDebugLocal(symbolID, emitter.currentPC())
	}
	emitter.releaseTemps(maxInt(mark, emitter.activeBase()))
	return nil
}

func (emitter *funcEmitter) emitAssignStat(stat BoundAssignStat) error {
	if len(stat.Targets) == 1 && len(stat.Values) == 1 {
		if target, ok := stat.Targets[0].(BoundSymbolTarget); ok {
			switch target.Ref.Kind {
			case SymbolParam, SymbolLocal:
				slot, err := emitter.localSlot(target.Ref.Symbol)
				if err != nil {
					return err
				}
				return emitter.emitExprResults(stat.Values[0], slot, 1)
			}
		}
	}
	mark := emitter.tempTop
	prepared := make([]preparedTarget, 0, len(stat.Targets))
	for _, target := range stat.Targets {
		item, err := emitter.prepareTarget(target)
		if err != nil {
			return err
		}
		prepared = append(prepared, item)
	}
	valueStart, err := emitter.reserveTemps(len(stat.Targets))
	if err != nil {
		return err
	}
	if err := emitter.emitAdjustedExprs(stat.Values, valueStart, len(stat.Targets)); err != nil {
		return err
	}
	for index := len(prepared) - 1; index >= 0; index-- {
		if err := emitter.storePreparedTarget(prepared[index], valueStart+index, lineOf(stat.SpanRange())); err != nil {
			return err
		}
	}
	emitter.releaseTemps(mark)
	return nil
}

func (emitter *funcEmitter) emitCallStat(stat BoundCallStat) error {
	mark := emitter.tempTop
	callBase, err := emitter.allocateTemp()
	if err != nil {
		return err
	}
	call, ok := stat.Call.(BoundCallExpr)
	if !ok {
		return emitter.errorf(stat.SpanRange(), "statement call must lower to BoundCallExpr")
	}
	if err := emitter.emitCall(call, callBase, 0, false); err != nil {
		return err
	}
	emitter.releaseTemps(mark)
	return nil
}

func (emitter *funcEmitter) emitIfStat(stat BoundIfStat) error {
	exits := JumpList{}
	for _, clause := range stat.Clauses {
		falseJump, err := emitter.emitConditionFalseJump(clause.Condition)
		if err != nil {
			return err
		}
		if err := emitter.emitScopedBlock(clause.Body); err != nil {
			return err
		}
		if clause.Body != nil && (stat.ElseBlock != nil || len(exits.Entries)+1 < len(stat.Clauses)) {
			exitJump := emitter.emitJump(lineOf(clause.Body.SpanRange()))
			exits.Entries = append(exits.Entries, exitJump)
		}
		if err := emitter.patchJump(falseJump, emitter.currentPC()); err != nil {
			return err
		}
	}
	if stat.ElseBlock != nil {
		if err := emitter.emitScopedBlock(stat.ElseBlock); err != nil {
			return err
		}
	}
	return emitter.patchJumpList(exits, emitter.currentPC())
}

func (emitter *funcEmitter) emitWhileStat(stat BoundWhileStat) error {
	loopStart := emitter.currentPC()
	falseJump, err := emitter.emitConditionFalseJump(stat.Condition)
	if err != nil {
		return err
	}
	bodyFrame, err := emitter.enterScope(stat.Body.Scope, false)
	if err != nil {
		return err
	}
	emitter.pushLoop(len(emitter.scopes)-1, emitter.paramsCount+bodyFrame.baseLocals)
	if err := emitter.emitStats(stat.Body.Stats); err != nil {
		return err
	}
	emitter.emitScopeClose(bodyFrame, lineOf(stat.Body.SpanRange()))
	backJump := emitter.emitJump(lineOf(stat.SpanRange()))
	if err := emitter.patchJump(backJump, loopStart); err != nil {
		return err
	}
	loop := emitter.popLoop()
	emitter.leaveScope(bodyFrame)
	exitPC := emitter.currentPC()
	if err := emitter.patchJump(falseJump, exitPC); err != nil {
		return err
	}
	return emitter.patchJumpList(loop.breaks, exitPC)
}

func (emitter *funcEmitter) emitRepeatStat(stat BoundRepeatStat) error {
	repeatStart := emitter.currentPC()
	bodyFrame, err := emitter.enterScope(stat.Body.Scope, false)
	if err != nil {
		return err
	}
	emitter.pushLoop(len(emitter.scopes)-1, emitter.paramsCount+bodyFrame.baseLocals)
	if err := emitter.emitStats(stat.Body.Stats); err != nil {
		return err
	}
	falseJump, err := emitter.emitConditionFalseJump(stat.Condition)
	if err != nil {
		return err
	}
	emitter.emitScopeClose(bodyFrame, lineOf(stat.Body.SpanRange()))
	exitJump := emitter.emitJump(lineOf(stat.SpanRange()))
	falsePC := emitter.currentPC()
	emitter.emitScopeClose(bodyFrame, lineOf(stat.Body.SpanRange()))
	backJump := emitter.emitJump(lineOf(stat.SpanRange()))
	if err := emitter.patchJump(backJump, repeatStart); err != nil {
		return err
	}
	loop := emitter.popLoop()
	emitter.leaveScope(bodyFrame)
	exitPC := emitter.currentPC()
	if err := emitter.patchJump(falseJump, falsePC); err != nil {
		return err
	}
	if err := emitter.patchJump(exitJump, exitPC); err != nil {
		return err
	}
	return emitter.patchJumpList(loop.breaks, exitPC)
}

func (emitter *funcEmitter) emitNumericForStat(stat BoundNumericForStat) error {
	loopFrame, err := emitter.enterScope(stat.Body.Scope, true)
	if err != nil {
		return err
	}
	if _, err := emitter.allocateLocal(stat.IndexSymbol); err != nil {
		return err
	}
	if _, err := emitter.allocateLocal(stat.LimitSymbol); err != nil {
		return err
	}
	if _, err := emitter.allocateLocal(stat.StepSymbol); err != nil {
		return err
	}
	base, err := emitter.localSlot(stat.IndexSymbol)
	if err != nil {
		return err
	}
	if err := emitter.emitExprResults(stat.Initial, base, 1); err != nil {
		return err
	}
	if err := emitter.emitExprResults(stat.Limit, base+1, 1); err != nil {
		return err
	}
	if stat.Step != nil {
		if err := emitter.emitExprResults(stat.Step, base+2, 1); err != nil {
			return err
		}
	} else {
		if err := emitter.emitNumber(base+2, 1, lineOf(stat.SpanRange())); err != nil {
			return err
		}
	}
	emitter.activateDebugLocal(stat.IndexSymbol, emitter.currentPC())
	emitter.activateDebugLocal(stat.LimitSymbol, emitter.currentPC())
	emitter.activateDebugLocal(stat.StepSymbol, emitter.currentPC())
	prepPC := emitter.builder.EmitAsBx(bytecode.OP_FORPREP, base, 0, lineOf(stat.SpanRange()))
	iterFrame, err := emitter.enterScope(stat.Body.Scope, false)
	if err != nil {
		return err
	}
	if _, err := emitter.allocateLocal(stat.Counter); err != nil {
		return err
	}
	emitter.activateDebugLocal(stat.Counter, emitter.currentPC())
	bodyStart := emitter.currentPC()
	emitter.pushLoop(len(emitter.scopes)-2, emitter.paramsCount+loopFrame.baseLocals)
	if err := emitter.emitStats(stat.Body.Stats); err != nil {
		return err
	}
	loop := emitter.popLoop()
	emitter.emitScopeClose(iterFrame, lineOf(stat.Body.SpanRange()))
	emitter.leaveScope(iterFrame)
	loopPC := emitter.builder.EmitAsBx(bytecode.OP_FORLOOP, base, 0, lineOf(stat.SpanRange()))
	if err := emitter.patchJump(prepPC, loopPC); err != nil {
		return err
	}
	if err := emitter.patchJump(loopPC, bodyStart); err != nil {
		return err
	}
	emitter.leaveScope(loopFrame)
	exitPC := emitter.currentPC()
	return emitter.patchJumpList(loop.breaks, exitPC)
}

func (emitter *funcEmitter) emitGenericForStat(stat BoundGenericForStat) error {
	loopFrame, err := emitter.enterScope(stat.Body.Scope, true)
	if err != nil {
		return err
	}
	if _, err := emitter.allocateLocal(stat.GeneratorSymbol); err != nil {
		return err
	}
	if _, err := emitter.allocateLocal(stat.StateSymbol); err != nil {
		return err
	}
	if _, err := emitter.allocateLocal(stat.ControlSymbol); err != nil {
		return err
	}
	base, err := emitter.localSlot(stat.GeneratorSymbol)
	if err != nil {
		return err
	}
	if err := emitter.emitAdjustedExprs(stat.Iterators, base, 3); err != nil {
		return err
	}
	emitter.activateDebugLocal(stat.GeneratorSymbol, emitter.currentPC())
	emitter.activateDebugLocal(stat.StateSymbol, emitter.currentPC())
	emitter.activateDebugLocal(stat.ControlSymbol, emitter.currentPC())
	iterFrame, err := emitter.enterScope(stat.Body.Scope, false)
	if err != nil {
		return err
	}
	for _, symbolID := range stat.Names {
		if _, err := emitter.allocateLocal(symbolID); err != nil {
			return err
		}
	}
	prepJump := emitter.emitJump(lineOf(stat.SpanRange()))
	for _, symbolID := range stat.Names {
		emitter.activateDebugLocal(symbolID, emitter.currentPC())
	}
	bodyStart := emitter.currentPC()
	emitter.pushLoop(len(emitter.scopes)-2, emitter.paramsCount+loopFrame.baseLocals)
	if err := emitter.emitStats(stat.Body.Stats); err != nil {
		return err
	}
	loop := emitter.popLoop()
	emitter.emitScopeClose(iterFrame, lineOf(stat.Body.SpanRange()))
	emitter.leaveScope(iterFrame)
	tforPC := emitter.currentPC()
	if err := emitter.patchJump(prepJump, tforPC); err != nil {
		return err
	}
	emitter.builder.EmitABC(bytecode.OP_TFORLOOP, base, 0, len(stat.Names), lineOf(stat.SpanRange()))
	bodyJump := emitter.emitJump(lineOf(stat.SpanRange()))
	if err := emitter.patchJump(bodyJump, bodyStart); err != nil {
		return err
	}
	emitter.leaveScope(loopFrame)
	exitPC := emitter.currentPC()
	return emitter.patchJumpList(loop.breaks, exitPC)
}

func (emitter *funcEmitter) emitDoStat(stat BoundDoStat) error {
	return emitter.emitScopedBlock(stat.Body)
}

func (emitter *funcEmitter) emitBreakStat(stat BoundBreakStat) error {
	loop := emitter.currentLoop()
	if loop == nil {
		return emitter.errorf(stat.SpanRange(), "break outside loop")
	}
	if emitter.breakNeedsClose(loop.scopeDepth) {
		emitter.builder.EmitABC(bytecode.OP_CLOSE, loop.closeSlot, 0, 0, lineOf(stat.SpanRange()))
	}
	loop.breaks.Entries = append(loop.breaks.Entries, emitter.emitJump(lineOf(stat.SpanRange())))
	return nil
}

func (emitter *funcEmitter) emitReturnStat(stat BoundReturnStat) error {
	line := lineEndOf(stat.SpanRange())
	if len(stat.Values) == 0 {
		emitter.builder.EmitABC(bytecode.OP_RETURN, 0, 1, 0, line)
		return nil
	}
	if len(stat.Values) == 1 {
		if slot, ok, err := emitter.directReturnSlot(stat.Values[0]); err != nil {
			return err
		} else if ok {
			emitter.builder.EmitABC(bytecode.OP_RETURN, slot, 2, 0, line)
			return nil
		}
		if call, ok := stat.Values[0].(BoundCallExpr); ok && call.ResultMode() == ResultTail {
			base, err := emitter.reserveTemps(1)
			if err != nil {
				return err
			}
			if err := emitter.emitCall(call, base, 0, true); err != nil {
				return err
			}
			emitter.builder.EmitABC(bytecode.OP_RETURN, base, 0, 0, line)
			return nil
		}
	}
	base, err := emitter.reserveTemps(len(stat.Values))
	if err != nil {
		return err
	}
	last := stat.Values[len(stat.Values)-1]
	openReturn := isMultiResultExpr(last)
	for index, value := range stat.Values {
		if openReturn && index == len(stat.Values)-1 {
			if err := emitter.emitExprResults(value, base+index, 0); err != nil {
				return err
			}
			break
		}
		if err := emitter.emitExprResults(value, base+index, 1); err != nil {
			return err
		}
	}
	if openReturn {
		emitter.builder.EmitABC(bytecode.OP_RETURN, base, 0, 0, line)
		return nil
	}
	emitter.builder.EmitABC(bytecode.OP_RETURN, base, len(stat.Values)+1, 0, line)
	return nil
}

func (emitter *funcEmitter) emitScopedBlock(block *BoundBlock) error {
	frame, err := emitter.enterScope(block.Scope, false)
	if err != nil {
		return err
	}
	if err := emitter.emitStats(block.Stats); err != nil {
		return err
	}
	emitter.emitScopeClose(frame, lineOf(block.SpanRange()))
	emitter.leaveScope(frame)
	return nil
}

func (emitter *funcEmitter) emitAdjustedExprs(exprs []BoundExpr, dest int, wanted int) error {
	used := 0
	for index, expr := range exprs {
		if index < wanted {
			if index == len(exprs)-1 && isMultiResultExpr(expr) {
				if err := emitter.emitExprResults(expr, dest+index, wanted-index); err != nil {
					return err
				}
				used = wanted
				continue
			}
			if err := emitter.emitExprResults(expr, dest+index, 1); err != nil {
				return err
			}
			used++
			continue
		}
		if err := emitter.emitDiscardExpr(expr); err != nil {
			return err
		}
	}
	if used < wanted {
		emitter.emitNilRange(dest+used, wanted-used, 0)
	}
	return nil
}

func (emitter *funcEmitter) emitDiscardExpr(expr BoundExpr) error {
	mark := emitter.tempTop
	reg, err := emitter.allocateTemp()
	if err != nil {
		return err
	}
	if err := emitter.emitExprResults(expr, reg, 1); err != nil {
		return err
	}
	emitter.releaseTemps(mark)
	return nil
}

func (emitter *funcEmitter) emitExprResults(expr BoundExpr, dest int, count int) error {
	if expr == nil {
		emitter.emitNilRange(dest, maxInt(1, count), 0)
		return nil
	}
	switch typed := expr.(type) {
	case BoundNilExpr:
		emitter.emitNilRange(dest, maxInt(1, count), lineOf(typed.SpanRange()))
		return nil
	case BoundBoolExpr:
		emitter.builder.EmitABC(bytecode.OP_LOADBOOL, dest, boolToInt(typed.Value), 0, lineOf(typed.SpanRange()))
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundNumberExpr:
		if err := emitter.emitNumber(dest, typed.Value, lineOf(typed.SpanRange())); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundStringExpr:
		if err := emitter.emitString(dest, typed.Value, lineOf(typed.SpanRange())); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundVarargExpr:
		if count == 0 {
			emitter.usesOpenTop = true
			emitter.builder.EmitABC(bytecode.OP_VARARG, dest, 0, 0, lineOf(typed.SpanRange()))
			return nil
		}
		emitter.builder.EmitABC(bytecode.OP_VARARG, dest, count+1, 0, lineOf(typed.SpanRange()))
		return nil
	case BoundSymbolExpr:
		if err := emitter.emitSymbolExpr(typed, dest); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundUnaryExpr:
		if err := emitter.emitUnaryExpr(typed, dest); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundBinaryExpr:
		if err := emitter.emitBinaryExpr(typed, dest); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundLogicalExpr:
		if err := emitter.emitLogicalExpr(typed, dest); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundCompareExpr:
		if err := emitter.emitCompareExpr(typed, dest); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundTableExpr:
		if err := emitter.emitTableExpr(typed, dest); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundFunctionExpr:
		if err := emitter.emitFunctionExpr(typed, dest); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundIndexExpr:
		if err := emitter.emitIndexExpr(typed, dest); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundFieldExpr:
		if err := emitter.emitFieldExpr(typed, dest); err != nil {
			return err
		}
		if count > 1 {
			emitter.emitNilRange(dest+1, count-1, lineOf(typed.SpanRange()))
		}
		return nil
	case BoundCallExpr:
		return emitter.emitCall(typed, dest, count, false)
	default:
		return emitter.errorf(expr.SpanRange(), "unsupported expression %T", expr)
	}
}

func (emitter *funcEmitter) emitSymbolExpr(expr BoundSymbolExpr, dest int) error {
	line := lineOf(expr.SpanRange())
	switch expr.Ref.Kind {
	case SymbolParam, SymbolLocal:
		slot, err := emitter.localSlot(expr.Ref.Symbol)
		if err != nil {
			return err
		}
		if slot != dest {
			emitter.builder.EmitABC(bytecode.OP_MOVE, dest, slot, 0, line)
		}
		return nil
	case SymbolUpvalue:
		index, err := emitter.upvalueIndex(expr.Ref.Symbol)
		if err != nil {
			return err
		}
		emitter.builder.EmitABC(bytecode.OP_GETUPVAL, dest, index, 0, line)
		return nil
	case SymbolGlobal:
		index, err := emitter.addStringConstant(expr.Ref.GlobalName)
		if err != nil {
			return err
		}
		emitter.builder.EmitABx(bytecode.OP_GETGLOBAL, dest, index, line)
		return nil
	default:
		return emitter.errorf(expr.SpanRange(), "unsupported name reference kind %s", expr.Ref.Kind)
	}
}

func (emitter *funcEmitter) emitUnaryExpr(expr BoundUnaryExpr, dest int) error {
	if err := emitter.emitExprResults(expr.Value, dest, 1); err != nil {
		return err
	}
	line := lineOf(expr.SpanRange())
	switch expr.Op {
	case lexer.TokenMinus:
		emitter.builder.EmitABC(bytecode.OP_UNM, dest, dest, 0, line)
	case lexer.TokenNot:
		emitter.builder.EmitABC(bytecode.OP_NOT, dest, dest, 0, line)
	case lexer.TokenHash:
		emitter.builder.EmitABC(bytecode.OP_LEN, dest, dest, 0, line)
	default:
		return emitter.errorf(expr.SpanRange(), "unsupported unary operator %s", expr.Op)
	}
	return nil
}

func (emitter *funcEmitter) emitBinaryExpr(expr BoundBinaryExpr, dest int) error {
	if expr.Op == lexer.TokenConcat {
		return emitter.emitConcatExpr(expr, dest)
	}
	line := lineOf(expr.SpanRange())
	opcode, ok := arithmeticOpcode(expr.Op)
	if !ok {
		return emitter.errorf(expr.SpanRange(), "unsupported binary operator %s", expr.Op)
	}
	mark := emitter.tempTop
	left, leftDirect, err := emitter.tryRKOperand(expr.Left)
	if err != nil {
		return err
	}
	right, rightDirect, err := emitter.tryRKOperand(expr.Right)
	if err != nil {
		return err
	}
	switch {
	case leftDirect && rightDirect:
		emitter.builder.EmitABC(opcode, dest, left, right, line)
	case rightDirect:
		if emitter.rkAliasesDest(right, dest) {
			left, err := emitter.emitRKOperand(expr.Left)
			if err != nil {
				return err
			}
			emitter.builder.EmitABC(opcode, dest, left, right, line)
			break
		}
		if err := emitter.emitExprResults(expr.Left, dest, 1); err != nil {
			return err
		}
		emitter.builder.EmitABC(opcode, dest, dest, right, line)
	case leftDirect:
		if emitter.rkAliasesDest(left, dest) {
			right, err := emitter.emitRKOperand(expr.Right)
			if err != nil {
				return err
			}
			emitter.builder.EmitABC(opcode, dest, left, right, line)
			break
		}
		if err := emitter.emitExprResults(expr.Right, dest, 1); err != nil {
			return err
		}
		emitter.builder.EmitABC(opcode, dest, left, dest, line)
	default:
		if err := emitter.emitExprResults(expr.Left, dest, 1); err != nil {
			return err
		}
		right, err := emitter.emitRKOperand(expr.Right)
		if err != nil {
			return err
		}
		emitter.builder.EmitABC(opcode, dest, dest, right, line)
	}
	emitter.releaseTempsPreserving(mark, dest, 1)
	return nil
}

func (emitter *funcEmitter) emitConcatExpr(expr BoundBinaryExpr, dest int) error {
	parts := make([]BoundExpr, 0, 2)
	collectConcatParts(expr, &parts)
	mark := emitter.tempTop
	start, err := emitter.reserveTemps(len(parts))
	if err != nil {
		return err
	}
	for index, part := range parts {
		if err := emitter.emitExprResults(part, start+index, 1); err != nil {
			return err
		}
	}
	emitter.builder.EmitABC(bytecode.OP_CONCAT, dest, start, start+len(parts)-1, lineOf(expr.SpanRange()))
	emitter.releaseTempsPreserving(mark, dest, 1)
	return nil
}

func (emitter *funcEmitter) emitLogicalExpr(expr BoundLogicalExpr, dest int) error {
	if err := emitter.emitExprResults(expr.Left, dest, 1); err != nil {
		return err
	}
	line := lineOf(expr.SpanRange())
	jumpFlag := 0
	if expr.Op == lexer.TokenOr {
		jumpFlag = 1
	}
	shortJump := emitter.emitTestJump(dest, jumpFlag, line)
	if err := emitter.emitExprResults(expr.Right, dest, 1); err != nil {
		return err
	}
	return emitter.patchJump(shortJump, emitter.currentPC())
}

func (emitter *funcEmitter) emitCompareExpr(expr BoundCompareExpr, dest int) error {
	falseJump, err := emitter.emitCompareJump(expr, true)
	if err != nil {
		return err
	}
	line := lineOf(expr.SpanRange())
	emitter.builder.EmitABC(bytecode.OP_LOADBOOL, dest, 1, 1, line)
	falsePC := emitter.currentPC()
	emitter.builder.EmitABC(bytecode.OP_LOADBOOL, dest, 0, 0, line)
	return emitter.patchJump(falseJump, falsePC)
}

func (emitter *funcEmitter) emitTableExpr(expr BoundTableExpr, dest int) error {
	mark := emitter.tempTop
	tableReg, err := emitter.allocateTemp()
	if err != nil {
		return err
	}
	line := lineOf(expr.SpanRange())
	emitter.builder.EmitABC(bytecode.OP_NEWTABLE, tableReg, int2fb(expr.ArrayCount), int2fb(expr.HashCount), line)
	pending := 0
	arrayTotal := 0
	flushArray := func(open bool) error {
		if pending == 0 {
			return nil
		}
		block := (arrayTotal-1)/fieldsPerFlush + 1
		if block <= bytecode.MaxArgC {
			b := pending
			if open {
				b = 0
				emitter.usesOpenTop = true
			}
			emitter.builder.EmitABC(bytecode.OP_SETLIST, tableReg, b, block, line)
		} else {
			b := pending
			if open {
				b = 0
				emitter.usesOpenTop = true
			}
			emitter.builder.EmitABC(bytecode.OP_SETLIST, tableReg, b, 0, line)
			emitter.builder.EmitInstruction(bytecode.Instruction(block), line)
		}
		pending = 0
		return nil
	}
	for index, field := range expr.Fields {
		if field.Kind != BoundTableFieldArray {
			if err := flushArray(false); err != nil {
				return err
			}
		}
		switch field.Kind {
		case BoundTableFieldArray:
			valueReg := tableReg + 1 + pending
			if err := emitter.ensureRegister(valueReg + 1); err != nil {
				return err
			}
			open := index == len(expr.Fields)-1 && isMultiResultExpr(field.Value)
			if open {
				if err := emitter.emitExprResults(field.Value, valueReg, 0); err != nil {
					return err
				}
			} else {
				if err := emitter.emitExprResults(field.Value, valueReg, 1); err != nil {
					return err
				}
			}
			pending++
			arrayTotal++
			if pending == fieldsPerFlush || open {
				if err := flushArray(open); err != nil {
					return err
				}
			}
		case BoundTableFieldNamed:
			key, err := emitter.emitStringOperand(field.Name, line)
			if err != nil {
				return err
			}
			value, err := emitter.emitRKOperand(field.Value)
			if err != nil {
				return err
			}
			emitter.builder.EmitABC(bytecode.OP_SETTABLE, tableReg, key, value, line)
		case BoundTableFieldIndexed:
			key, err := emitter.emitRKOperand(field.Key)
			if err != nil {
				return err
			}
			value, err := emitter.emitRKOperand(field.Value)
			if err != nil {
				return err
			}
			emitter.builder.EmitABC(bytecode.OP_SETTABLE, tableReg, key, value, line)
		}
	}
	if err := flushArray(false); err != nil {
		return err
	}
	if tableReg != dest {
		emitter.builder.EmitABC(bytecode.OP_MOVE, dest, tableReg, 0, line)
	}
	emitter.releaseTempsPreserving(mark, dest, 1)
	return nil
}

func (emitter *funcEmitter) emitFunctionExpr(expr BoundFunctionExpr, dest int) error {
	child := newFuncEmitter(emitter.chunk, expr.Func, false)
	proto, err := child.emit()
	if err != nil {
		return err
	}
	childIndex := emitter.builder.AddChildProto(proto)
	if childIndex > bytecode.MaxArgBx {
		return emitter.errorf(expr.SpanRange(), "child proto index %d exceeds maximum %d", childIndex, bytecode.MaxArgBx)
	}
	line := lineEndOf(expr.SpanRange())
	emitter.builder.EmitABx(bytecode.OP_CLOSURE, dest, childIndex, line)
	for _, capture := range expr.Func.Captures {
		switch capture.Source {
		case CaptureFromLocal:
			slot, err := emitter.localSlot(capture.Symbol)
			if err != nil {
				return err
			}
			emitter.builder.EmitABC(bytecode.OP_MOVE, 0, slot, 0, line)
		case CaptureFromUpvalue:
			index, err := emitter.upvalueIndex(capture.Symbol)
			if err != nil {
				return err
			}
			emitter.builder.EmitABC(bytecode.OP_GETUPVAL, 0, index, 0, line)
		default:
			return emitter.errorf(expr.SpanRange(), "unsupported capture source %d", capture.Source)
		}
	}
	return nil
}

func (emitter *funcEmitter) emitIndexExpr(expr BoundIndexExpr, dest int) error {
	receiver, direct, err := emitter.directRegisterOperand(expr.Receiver)
	if err != nil {
		return err
	}
	if !direct {
		receiver = dest
		if err := emitter.emitExprResults(expr.Receiver, receiver, 1); err != nil {
			return err
		}
	}
	mark := emitter.tempTop
	index, err := emitter.emitRKOperand(expr.Index)
	if err != nil {
		return err
	}
	emitter.builder.EmitABC(bytecode.OP_GETTABLE, dest, receiver, index, lineOf(expr.SpanRange()))
	emitter.releaseTempsPreserving(mark, dest, 1)
	return nil
}

func (emitter *funcEmitter) emitFieldExpr(expr BoundFieldExpr, dest int) error {
	receiver, direct, err := emitter.directRegisterOperand(expr.Receiver)
	if err != nil {
		return err
	}
	if !direct {
		receiver = dest
		if err := emitter.emitExprResults(expr.Receiver, receiver, 1); err != nil {
			return err
		}
	}
	mark := emitter.tempTop
	key, err := emitter.emitStringOperand(expr.Name, lineOf(expr.SpanRange()))
	if err != nil {
		return err
	}
	emitter.builder.EmitABC(bytecode.OP_GETTABLE, dest, receiver, key, lineOf(expr.SpanRange()))
	emitter.releaseTempsPreserving(mark, dest, 1)
	return nil
}

func (emitter *funcEmitter) emitCall(expr BoundCallExpr, dest int, count int, tail bool) error {
	line := lineOf(expr.SpanRange())
	mark := emitter.tempTop
	argStart := dest + 1
	baseCount := 1
	if expr.Receiver != nil {
		receiver, direct, err := emitter.directRegisterOperand(expr.Receiver)
		if err != nil {
			return err
		}
		if !direct {
			receiver = dest + 1
			if err := emitter.emitExprResults(expr.Receiver, receiver, 1); err != nil {
				return err
			}
		}
		key, err := emitter.emitStringOperand(expr.MethodName, line)
		if err != nil {
			return err
		}
		emitter.builder.EmitABC(bytecode.OP_SELF, dest, receiver, key, line)
		argStart = dest + 2
		baseCount = 2
	} else {
		if err := emitter.emitExprResults(expr.Callee, dest, 1); err != nil {
			return err
		}
	}
	callAreaTop := argStart + len(expr.Args)
	if err := emitter.ensureRegister(callAreaTop); err != nil {
		return err
	}
	emitter.tempTop = maxInt(emitter.tempTop, callAreaTop)
	openArgs := false
	for index, arg := range expr.Args {
		if index == len(expr.Args)-1 && isMultiResultExpr(arg) {
			if err := emitter.emitExprResults(arg, argStart+index, 0); err != nil {
				return err
			}
			openArgs = true
			break
		}
		if err := emitter.emitExprResults(arg, argStart+index, 1); err != nil {
			return err
		}
	}
	b := baseCount + len(expr.Args)
	if openArgs {
		b = 0
	}
	if tail {
		emitter.builder.EmitABC(bytecode.OP_TAILCALL, dest, b, 0, line)
		emitter.releaseTemps(mark)
		return nil
	}
	if count == 0 {
		emitter.usesOpenTop = true
		emitter.builder.EmitABC(bytecode.OP_CALL, dest, b, 0, line)
		emitter.releaseTempsPreserving(mark, dest, 1)
		return nil
	}
	emitter.builder.EmitABC(bytecode.OP_CALL, dest, b, count+1, line)
	emitter.releaseTempsPreserving(mark, dest, maxInt(1, count))
	return nil
}

func (emitter *funcEmitter) prepareTarget(target BoundTarget) (preparedTarget, error) {
	switch typed := target.(type) {
	case BoundSymbolTarget:
		return preparedTarget{kind: preparedTargetSymbol, ref: typed.Ref}, nil
	case BoundIndexTarget:
		receiver, direct, err := emitter.directRegisterOperand(typed.Receiver)
		if err != nil {
			return preparedTarget{}, err
		}
		if !direct {
			receiver, err = emitter.allocateTemp()
			if err != nil {
				return preparedTarget{}, err
			}
			if err := emitter.emitExprResults(typed.Receiver, receiver, 1); err != nil {
				return preparedTarget{}, err
			}
		}
		key, err := emitter.emitRKOperand(typed.Index)
		if err != nil {
			return preparedTarget{}, err
		}
		return preparedTarget{kind: preparedTargetTable, receiver: receiver, keyOperand: key}, nil
	case BoundFieldTarget:
		receiver, direct, err := emitter.directRegisterOperand(typed.Receiver)
		if err != nil {
			return preparedTarget{}, err
		}
		if !direct {
			receiver, err = emitter.allocateTemp()
			if err != nil {
				return preparedTarget{}, err
			}
			if err := emitter.emitExprResults(typed.Receiver, receiver, 1); err != nil {
				return preparedTarget{}, err
			}
		}
		key, err := emitter.emitStringOperand(typed.Name, lineOf(typed.SpanRange()))
		if err != nil {
			return preparedTarget{}, err
		}
		return preparedTarget{kind: preparedTargetTable, receiver: receiver, keyOperand: key}, nil
	default:
		return preparedTarget{}, emitter.errorf(target.SpanRange(), "unsupported assignment target %T", target)
	}
}

func (emitter *funcEmitter) storePreparedTarget(target preparedTarget, source int, line int) error {
	switch target.kind {
	case preparedTargetSymbol:
		switch target.ref.Kind {
		case SymbolParam, SymbolLocal:
			slot, err := emitter.localSlot(target.ref.Symbol)
			if err != nil {
				return err
			}
			if slot != source {
				emitter.builder.EmitABC(bytecode.OP_MOVE, slot, source, 0, line)
			}
			return nil
		case SymbolUpvalue:
			index, err := emitter.upvalueIndex(target.ref.Symbol)
			if err != nil {
				return err
			}
			emitter.builder.EmitABC(bytecode.OP_SETUPVAL, source, index, 0, line)
			return nil
		case SymbolGlobal:
			index, err := emitter.addStringConstant(target.ref.GlobalName)
			if err != nil {
				return err
			}
			emitter.builder.EmitABx(bytecode.OP_SETGLOBAL, source, index, line)
			return nil
		default:
			return emitter.errorf(lexer.Span{}, "unsupported assignment target kind %s", target.ref.Kind)
		}
	case preparedTargetTable:
		emitter.builder.EmitABC(bytecode.OP_SETTABLE, target.receiver, target.keyOperand, source, line)
		return nil
	default:
		return emitter.errorf(lexer.Span{}, "unsupported prepared target kind %d", target.kind)
	}
}

func (emitter *funcEmitter) emitConditionFalseJump(expr BoundExpr) (int, error) {
	if compare, ok := expr.(BoundCompareExpr); ok {
		return emitter.emitCompareJump(compare, true)
	}
	mark := emitter.tempTop
	reg, err := emitter.allocateTemp()
	if err != nil {
		return 0, err
	}
	if err := emitter.emitExprResults(expr, reg, 1); err != nil {
		return 0, err
	}
	jump := emitter.emitTestJump(reg, 0, lineOf(expr.SpanRange()))
	emitter.releaseTemps(mark)
	return jump, nil
}

func (emitter *funcEmitter) emitCompareJump(expr BoundCompareExpr, jumpOnFalse bool) (int, error) {
	line := lineOf(expr.SpanRange())
	mark := emitter.tempTop
	leftExpr := expr.Left
	rightExpr := expr.Right
	opcode := bytecode.OP_EQ
	negated := false
	switch expr.Op {
	case lexer.TokenEqual:
		opcode = bytecode.OP_EQ
	case lexer.TokenNotEqual:
		opcode = bytecode.OP_EQ
		negated = true
	case lexer.TokenLessThan:
		opcode = bytecode.OP_LT
	case lexer.TokenLessEqual:
		opcode = bytecode.OP_LE
	case lexer.TokenGreaterThan:
		opcode = bytecode.OP_LT
		leftExpr, rightExpr = expr.Right, expr.Left
	case lexer.TokenGreaterEqual:
		opcode = bytecode.OP_LE
		leftExpr, rightExpr = expr.Right, expr.Left
	default:
		return 0, emitter.errorf(expr.SpanRange(), "unsupported compare operator %s", expr.Op)
	}
	left, err := emitter.emitRKOperand(leftExpr)
	if err != nil {
		return 0, err
	}
	right, err := emitter.emitRKOperand(rightExpr)
	if err != nil {
		return 0, err
	}
	a := 0
	if jumpOnFalse == negated {
		a = 1
	}
	emitter.builder.EmitABC(opcode, a, left, right, line)
	jump := emitter.emitJump(line)
	emitter.releaseTemps(mark)
	return jump, nil
}

func (emitter *funcEmitter) emitRKOperand(expr BoundExpr) (int, error) {
	if operand, ok, err := emitter.tryRKOperand(expr); err != nil {
		return 0, err
	} else if ok {
		return operand, nil
	}
	reg, err := emitter.allocateTemp()
	if err != nil {
		return 0, err
	}
	if err := emitter.emitExprResults(expr, reg, 1); err != nil {
		return 0, err
	}
	return reg, nil
}

func (emitter *funcEmitter) tryRKOperand(expr BoundExpr) (int, bool, error) {
	if constant, ok := constantForExpr(expr); ok {
		index, err := emitter.addConstant(constant)
		if err != nil {
			return 0, false, err
		}
		if index <= bytecode.MaxIndexRK {
			return bytecode.RKAsk(index), true, nil
		}
	}
	if symbolExpr, ok := expr.(BoundSymbolExpr); ok {
		switch symbolExpr.Ref.Kind {
		case SymbolParam, SymbolLocal:
			slot, err := emitter.localSlot(symbolExpr.Ref.Symbol)
			if err != nil {
				return 0, false, err
			}
			return slot, true, nil
		}
	}
	return 0, false, nil
}

func (emitter *funcEmitter) emitStringOperand(text string, line int) (int, error) {
	index, err := emitter.addStringConstant(text)
	if err != nil {
		return 0, err
	}
	if index <= bytecode.MaxIndexRK {
		return bytecode.RKAsk(index), nil
	}
	reg, err := emitter.allocateTemp()
	if err != nil {
		return 0, err
	}
	emitter.builder.EmitABx(bytecode.OP_LOADK, reg, index, line)
	return reg, nil
}

func (emitter *funcEmitter) emitNumber(dest int, value float64, line int) error {
	index, err := emitter.addConstant(bytecode.NumberConstant(value))
	if err != nil {
		return err
	}
	emitter.builder.EmitABx(bytecode.OP_LOADK, dest, index, line)
	return nil
}

func (emitter *funcEmitter) emitString(dest int, value string, line int) error {
	index, err := emitter.addConstant(bytecode.StringConstant(value))
	if err != nil {
		return err
	}
	emitter.builder.EmitABx(bytecode.OP_LOADK, dest, index, line)
	return nil
}

func (emitter *funcEmitter) emitNilRange(start int, count int, line int) {
	if count <= 0 {
		return
	}
	emitter.builder.EmitABC(bytecode.OP_LOADNIL, start, start+count-1, 0, line)
}

func (emitter *funcEmitter) emitTestJump(register int, flag int, line int) int {
	emitter.builder.EmitABC(bytecode.OP_TEST, register, 0, flag, line)
	return emitter.emitJump(line)
}

func (emitter *funcEmitter) emitJump(line int) int {
	return emitter.builder.EmitAsBx(bytecode.OP_JMP, 0, 0, line)
}

func (emitter *funcEmitter) patchJumpList(list JumpList, target int) error {
	for _, entry := range list.Entries {
		if err := emitter.patchJump(entry, target); err != nil {
			return err
		}
	}
	return nil
}

func (emitter *funcEmitter) patchJump(pc int, target int) error {
	if pc < 0 || pc >= len(emitter.builder.code) {
		return emitter.errorf(lexer.Span{}, "jump pc %d is out of range", pc)
	}
	offset := target - (pc + 1)
	if offset < -bytecode.MaxArgSBx || offset > bytecode.MaxArgSBx {
		return emitter.errorf(lexer.Span{}, "jump target %d is out of range for pc %d", target, pc)
	}
	emitter.builder.code[pc] = bytecode.SetSBx(emitter.builder.code[pc], offset)
	return nil
}

func (emitter *funcEmitter) enterScope(scopeID ScopeID, persistent bool) (scopeFrame, error) {
	scope := emitter.scope(scopeID)
	if scope == nil {
		return scopeFrame{}, emitter.errorf(lexer.Span{}, "scope %d is not defined", scopeID)
	}
	frame := scopeFrame{scopeID: scopeID, baseLocals: len(emitter.locals), hasUpvalues: scope.HasUpvalues && !persistent}
	emitter.scopes = append(emitter.scopes, frame)
	return frame, nil
}

func (emitter *funcEmitter) leaveScope(frame scopeFrame) {
	endPC := emitter.currentPC()
	if len(emitter.scopes) != 0 {
		emitter.scopes = emitter.scopes[:len(emitter.scopes)-1]
	}
	for len(emitter.locals) > frame.baseLocals {
		symbolID := emitter.locals[len(emitter.locals)-1]
		emitter.closeDebugLocal(symbolID, endPC)
		delete(emitter.localSlots, symbolID)
		emitter.locals = emitter.locals[:len(emitter.locals)-1]
	}
	emitter.releaseTemps(emitter.activeBase())
}

func (emitter *funcEmitter) emitScopeClose(frame scopeFrame, line int) {
	if !frame.hasUpvalues {
		return
	}
	emitter.builder.EmitABC(bytecode.OP_CLOSE, emitter.paramsCount+frame.baseLocals, 0, 0, line)
}

func (emitter *funcEmitter) allocateLocal(symbolID SymbolID) (int, error) {
	if symbolID == InvalidSymbolID {
		return 0, emitter.errorf(lexer.Span{}, "cannot allocate invalid symbol")
	}
	if slot, ok := emitter.localSlots[symbolID]; ok {
		return slot, nil
	}
	slot := emitter.activeBase()
	if err := emitter.ensureRegister(slot + 1); err != nil {
		return 0, err
	}
	emitter.locals = append(emitter.locals, symbolID)
	emitter.localSlots[symbolID] = slot
	emitter.declareDebugLocal(symbolID)
	if emitter.tempTop < emitter.activeBase() {
		emitter.tempTop = emitter.activeBase()
	}
	return slot, nil
}

func (emitter *funcEmitter) declareDebugLocal(symbolID SymbolID) {
	if symbolID == InvalidSymbolID {
		return
	}
	if _, ok := emitter.debugLocals[symbolID]; ok {
		return
	}
	symbol := emitter.symbol(symbolID)
	if symbol == nil {
		return
	}
	emitter.debugLocals[symbolID] = emitter.builder.AddLocVar(symbol.Name, -1, -1)
}

func (emitter *funcEmitter) activateDebugLocal(symbolID SymbolID, startPC int) {
	index, ok := emitter.debugLocals[symbolID]
	if !ok {
		emitter.declareDebugLocal(symbolID)
		index, ok = emitter.debugLocals[symbolID]
		if !ok {
			return
		}
	}
	if emitter.builder.locvars[index].StartPC < 0 {
		emitter.builder.locvars[index].StartPC = startPC
	}
}

func (emitter *funcEmitter) closeDebugLocal(symbolID SymbolID, endPC int) {
	index, ok := emitter.debugLocals[symbolID]
	if !ok {
		return
	}
	if emitter.builder.locvars[index].StartPC < 0 {
		emitter.builder.locvars[index].StartPC = endPC
	}
	if emitter.builder.locvars[index].EndPC < 0 {
		emitter.builder.locvars[index].EndPC = endPC
	}
}

func (emitter *funcEmitter) localSlot(symbolID SymbolID) (int, error) {
	slot, ok := emitter.localSlots[symbolID]
	if !ok {
		return 0, emitter.errorf(emitter.symbolSpan(symbolID), "symbol %d is not active in the current frame", symbolID)
	}
	return slot, nil
}

func (emitter *funcEmitter) upvalueIndex(symbolID SymbolID) (int, error) {
	index, ok := emitter.upvalues[symbolID]
	if !ok {
		return 0, emitter.errorf(emitter.symbolSpan(symbolID), "symbol %d is not captured in the current function", symbolID)
	}
	return index, nil
}

func (emitter *funcEmitter) reserveTemps(count int) (int, error) {
	start := emitter.tempTop
	if err := emitter.ensureRegister(start + count); err != nil {
		return 0, err
	}
	emitter.tempTop += count
	return start, nil
}

func (emitter *funcEmitter) allocateTemp() (int, error) {
	reg := emitter.tempTop
	if err := emitter.ensureRegister(reg + 1); err != nil {
		return 0, err
	}
	emitter.tempTop++
	return reg, nil
}

func (emitter *funcEmitter) releaseTemps(mark int) {
	if mark < emitter.activeBase() {
		mark = emitter.activeBase()
	}
	emitter.tempTop = mark
}

func (emitter *funcEmitter) releaseTempsPreserving(mark int, start int, count int) {
	preserve := start + count
	if preserve < emitter.activeBase() {
		preserve = emitter.activeBase()
	}
	if mark > preserve {
		preserve = mark
	}
	emitter.tempTop = preserve
}

func (emitter *funcEmitter) ensureRegister(limit int) error {
	if limit > bytecode.NoReg {
		return emitter.errorf(emitter.function.SpanRange(), "function requires %d registers, maximum is %d", limit, bytecode.NoReg)
	}
	if limit > emitter.maxStack {
		emitter.maxStack = limit
	}
	return nil
}

func (emitter *funcEmitter) activeBase() int {
	return emitter.paramsCount + len(emitter.locals)
}

func (emitter *funcEmitter) currentPC() int {
	return len(emitter.builder.code)
}

func (emitter *funcEmitter) pushLoop(scopeDepth int, closeSlot int) {
	emitter.loops = append(emitter.loops, loopFrame{scopeDepth: scopeDepth, closeSlot: closeSlot})
}

func (emitter *funcEmitter) popLoop() loopFrame {
	loop := emitter.loops[len(emitter.loops)-1]
	emitter.loops = emitter.loops[:len(emitter.loops)-1]
	return loop
}

func (emitter *funcEmitter) currentLoop() *loopFrame {
	if len(emitter.loops) == 0 {
		return nil
	}
	return &emitter.loops[len(emitter.loops)-1]
}

func (emitter *funcEmitter) breakNeedsClose(scopeDepth int) bool {
	for index := len(emitter.scopes) - 1; index >= scopeDepth; index-- {
		if emitter.scopes[index].hasUpvalues {
			return true
		}
	}
	return false
}

func (emitter *funcEmitter) addConstant(constant bytecode.Constant) (int, error) {
	index := emitter.builder.AddConstant(constant)
	if index > bytecode.MaxArgBx {
		return 0, emitter.errorf(emitter.function.SpanRange(), "constant index %d exceeds maximum %d", index, bytecode.MaxArgBx)
	}
	return index, nil
}

func (emitter *funcEmitter) addStringConstant(value string) (int, error) {
	return emitter.addConstant(bytecode.StringConstant(value))
}

func (emitter *funcEmitter) scope(scopeID ScopeID) *Scope {
	if scopeID == InvalidScopeID {
		return nil
	}
	index := int(scopeID) - 1
	if index < 0 || index >= len(emitter.chunk.Scopes) {
		return nil
	}
	return &emitter.chunk.Scopes[index]
}

func (emitter *funcEmitter) symbol(symbolID SymbolID) *Symbol {
	if symbolID == InvalidSymbolID {
		return nil
	}
	index := int(symbolID) - 1
	if index < 0 || index >= len(emitter.chunk.Symbols) {
		return nil
	}
	return &emitter.chunk.Symbols[index]
}

func (emitter *funcEmitter) symbolSpan(symbolID SymbolID) lexer.Span {
	symbol := emitter.symbol(symbolID)
	if symbol == nil {
		return lexer.Span{}
	}
	return symbol.DeclSpan
}

func (emitter *funcEmitter) errorf(span lexer.Span, format string, args ...any) error {
	return lexer.Errorf(lexer.PhaseEmit, span, format, args...)
}

func (emitter *funcEmitter) directReturnSlot(expr BoundExpr) (int, bool, error) {
	return emitter.directRegisterOperand(expr)
}

func (emitter *funcEmitter) directRegisterOperand(expr BoundExpr) (int, bool, error) {
	symbolExpr, ok := expr.(BoundSymbolExpr)
	if !ok {
		return 0, false, nil
	}
	switch symbolExpr.Ref.Kind {
	case SymbolParam, SymbolLocal:
		slot, err := emitter.localSlot(symbolExpr.Ref.Symbol)
		if err != nil {
			return 0, false, err
		}
		return slot, true, nil
	default:
		return 0, false, nil
	}
}

func (emitter *funcEmitter) rkAliasesDest(operand int, dest int) bool {
	return !bytecode.IsConstantRK(operand) && operand == dest
}

func (emitter *funcEmitter) localFunctionDeclEndLine(stat BoundLocalDeclStat) (int, bool) {
	if len(stat.Names) != 1 || len(stat.Values) != 1 {
		return 0, false
	}
	functionExpr, ok := stat.Values[0].(BoundFunctionExpr)
	if !ok || functionExpr.Func == nil || functionExpr.Func.Name == "" {
		return 0, false
	}
	symbol := emitter.symbol(stat.Names[0])
	if symbol == nil || symbol.Name != functionExpr.Func.Name {
		return 0, false
	}
	span := stat.SpanRange()
	if !span.IsValid() {
		return 0, false
	}
	return span.End.Line, true
}

func constantForExpr(expr BoundExpr) (bytecode.Constant, bool) {
	switch typed := expr.(type) {
	case BoundNilExpr:
		return bytecode.NilConstant(), true
	case BoundBoolExpr:
		return bytecode.BooleanConstant(typed.Value), true
	case BoundNumberExpr:
		return bytecode.NumberConstant(typed.Value), true
	case BoundStringExpr:
		return bytecode.StringConstant(typed.Value), true
	default:
		return bytecode.Constant{}, false
	}
}

func isMultiResultExpr(expr BoundExpr) bool {
	if expr == nil {
		return false
	}
	if expr.ResultMode() == ResultSingle {
		return false
	}
	switch expr.(type) {
	case BoundCallExpr, BoundVarargExpr:
		return true
	default:
		return false
	}
}

func collectConcatParts(expr BoundExpr, parts *[]BoundExpr) {
	binary, ok := expr.(BoundBinaryExpr)
	if ok && binary.Op == lexer.TokenConcat {
		collectConcatParts(binary.Left, parts)
		collectConcatParts(binary.Right, parts)
		return
	}
	*parts = append(*parts, expr)
}

func arithmeticOpcode(kind lexer.TokenKind) (bytecode.Opcode, bool) {
	switch kind {
	case lexer.TokenPlus:
		return bytecode.OP_ADD, true
	case lexer.TokenMinus:
		return bytecode.OP_SUB, true
	case lexer.TokenStar:
		return bytecode.OP_MUL, true
	case lexer.TokenSlash:
		return bytecode.OP_DIV, true
	case lexer.TokenPercent:
		return bytecode.OP_MOD, true
	case lexer.TokenCaret:
		return bytecode.OP_POW, true
	default:
		return 0, false
	}
}

func lineOf(span lexer.Span) int {
	if !span.IsValid() {
		return 0
	}
	return span.Start.Line
}

func lineEndOf(span lexer.Span) int {
	if !span.IsValid() {
		return 0
	}
	return span.End.Line
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func int2fb(value int) int {
	exponent := 0
	for value >= 16 {
		value = (value + 1) >> 1
		exponent++
	}
	if value < 8 {
		return value
	}
	return ((exponent + 1) << 3) | (value - 8)
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func (frame scopeFrame) String() string {
	return fmt.Sprintf("scope=%d baseLocals=%d hasUpvalues=%v", frame.scopeID, frame.baseLocals, frame.hasUpvalues)
}

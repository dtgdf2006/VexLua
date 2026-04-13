package compiler

import (
	"vexlua/internal/frontend/lexer"
	"vexlua/internal/frontend/parser"
)

// ChunkBinder resolves names and scopes from parser AST into Bound IR.
type ChunkBinder struct{}

// NewBinder constructs the resolver stage for the source frontend.
func NewBinder() Binder {
	return &ChunkBinder{}
}

// BindChunk binds one parsed chunk into stable Bound IR.
func BindChunk(chunk *parser.Chunk) (*BoundChunk, error) {
	return (&ChunkBinder{}).BindChunk(chunk)
}

// BindChunk binds one parsed chunk into stable Bound IR.
func (binderStage *ChunkBinder) BindChunk(chunk *parser.Chunk) (*BoundChunk, error) {
	if chunk == nil {
		return nil, lexer.Errorf(lexer.PhaseBind, lexer.Span{}, "source frontend chunk is nil")
	}
	state := &bindState{}
	funcNode, err := state.bindRootChunk(chunk)
	if err != nil {
		return nil, err
	}
	bound := &BoundChunk{
		BoundInfo: BoundInfo{Span: chunk.SpanRange()},
		Name:      chunk.Name,
		Func:      funcNode,
		Symbols:   append([]Symbol(nil), state.symbols...),
		Scopes:    append([]Scope(nil), state.scopes...),
	}
	return LowerChunk(bound)
}

type bindState struct {
	symbols         []Symbol
	scopes          []Scope
	currentFunction *functionState
}

type functionState struct {
	state        *bindState
	parent       *functionState
	name         string
	hasVararg    bool
	currentScope *scopeState
	params       []SymbolID
	locals       []SymbolID
	slots        map[SymbolID]int
	captures     []CaptureDesc
	captureIndex map[SymbolID]int
}

type scopeState struct {
	id        ScopeID
	parent    *scopeState
	breakable bool
	bindings  map[string]SymbolID
}

func (state *bindState) bindRootChunk(chunk *parser.Chunk) (*BoundFunc, error) {
	if chunk.Block == nil {
		return nil, state.errorf(chunk.SpanRange(), "chunk block is nil")
	}
	function := state.pushFunction(chunk.Name, true)
	defer state.popFunction(function)
	scope := state.pushScope(false)
	defer state.popScope(scope)
	stats, err := state.bindStatList(chunk.Block.Stats)
	if err != nil {
		return nil, err
	}
	return &BoundFunc{
		BoundInfo: BoundInfo{Span: chunk.SpanRange()},
		Name:      chunk.Name,
		Params:    nil,
		Locals:    append([]SymbolID(nil), function.locals...),
		Captures:  nil,
		HasVararg: true,
		Body: &BoundBlock{
			BoundInfo: BoundInfo{Span: chunk.Block.SpanRange()},
			Scope:     scope.id,
			Breakable: false,
			Stats:     stats,
		},
	}, nil
}

func (state *bindState) bindFunctionBody(body *parser.FunctionBody, name string, includeSelf bool) (*BoundFunc, error) {
	if body == nil {
		return nil, state.errorf(lexer.Span{}, "function body is nil")
	}
	if body.Block == nil {
		return nil, state.errorf(body.SpanRange(), "function body block is nil")
	}
	function := state.pushFunction(name, body.HasVararg)
	defer state.popFunction(function)
	scope := state.pushScope(false)
	defer state.popScope(scope)
	if includeSelf {
		selfPos := body.SpanRange().Start
		if !selfPos.IsValid() {
			selfPos = lexer.StartPosition()
		}
		state.declareSymbol(function, scope, "self", lexer.Span{Start: selfPos, End: selfPos}, SymbolParam)
	}
	for _, param := range body.Params {
		state.declareSymbol(function, scope, param.Text, param.Span, SymbolParam)
	}
	stats, err := state.bindStatList(body.Block.Stats)
	if err != nil {
		return nil, err
	}
	return &BoundFunc{
		BoundInfo: BoundInfo{Span: body.SpanRange()},
		Name:      name,
		Params:    append([]SymbolID(nil), function.params...),
		Locals:    append([]SymbolID(nil), function.locals...),
		Captures:  append([]CaptureDesc(nil), function.captures...),
		HasVararg: body.HasVararg,
		Body: &BoundBlock{
			BoundInfo: BoundInfo{Span: body.Block.SpanRange()},
			Scope:     scope.id,
			Breakable: false,
			Stats:     stats,
		},
	}, nil
}

func (state *bindState) bindStatList(stats []parser.Stat) ([]BoundStat, error) {
	bound := make([]BoundStat, 0, len(stats))
	for _, stat := range stats {
		item, err := state.bindStat(stat)
		if err != nil {
			return nil, err
		}
		if item != nil {
			bound = append(bound, item)
		}
	}
	return bound, nil
}

func (state *bindState) bindStat(stat parser.Stat) (BoundStat, error) {
	switch typed := stat.(type) {
	case parser.EmptyStat:
		return nil, nil
	case parser.LocalDeclStat:
		values, err := state.bindExprList(typed.Values, ResultMulti)
		if err != nil {
			return nil, err
		}
		names := make([]SymbolID, 0, len(typed.Names))
		for _, name := range typed.Names {
			names = append(names, state.declareSymbol(state.currentFunction, state.currentFunction.currentScope, name.Text, name.Span, SymbolLocal))
		}
		return BoundLocalDeclStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Names: names, Values: values}, nil
	case parser.LocalFunctionStat:
		symbol := state.declareSymbol(state.currentFunction, state.currentFunction.currentScope, typed.Name.Text, typed.Name.Span, SymbolLocal)
		function, err := state.bindFunctionBody(typed.Body, typed.Name.Text, false)
		if err != nil {
			return nil, err
		}
		value := BoundFunctionExpr{
			BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.Body.SpanRange()}, Results: ResultSingle},
			Func:          function,
		}
		return BoundLocalDeclStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Names: []SymbolID{symbol}, Values: []BoundExpr{value}}, nil
	case parser.AssignmentStat:
		targets := make([]BoundTarget, 0, len(typed.Targets))
		for _, target := range typed.Targets {
			boundTarget, err := state.bindTarget(target)
			if err != nil {
				return nil, err
			}
			targets = append(targets, boundTarget)
		}
		values, err := state.bindExprList(typed.Values, ResultMulti)
		if err != nil {
			return nil, err
		}
		return BoundAssignStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Targets: targets, Values: values}, nil
	case parser.FunctionStat:
		target, err := state.bindNamedFunctionTarget(typed.Path, parser.Name{})
		if err != nil {
			return nil, err
		}
		functionName := ""
		if len(typed.Path) != 0 {
			functionName = typed.Path[len(typed.Path)-1].Text
		}
		function, err := state.bindFunctionBody(typed.Body, functionName, false)
		if err != nil {
			return nil, err
		}
		value := BoundFunctionExpr{
			BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.Body.SpanRange()}, Results: ResultSingle},
			Func:          function,
		}
		return BoundAssignStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Targets: []BoundTarget{target}, Values: []BoundExpr{value}}, nil
	case parser.MethodStat:
		target, err := state.bindNamedFunctionTarget(typed.Path, typed.Method)
		if err != nil {
			return nil, err
		}
		function, err := state.bindFunctionBody(typed.Body, typed.Method.Text, true)
		if err != nil {
			return nil, err
		}
		value := BoundFunctionExpr{
			BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.Body.SpanRange()}, Results: ResultSingle},
			Func:          function,
		}
		return BoundAssignStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Targets: []BoundTarget{target}, Values: []BoundExpr{value}}, nil
	case parser.CallStat:
		call, err := state.bindExpr(typed.Call, ResultSingle)
		if err != nil {
			return nil, err
		}
		callLike, ok := call.(BoundCallLike)
		if !ok {
			return nil, state.errorf(typed.SpanRange(), "statement must be assignment or function call")
		}
		return BoundCallStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Call: callLike}, nil
	case parser.IfStat:
		clauses := make([]BoundIfClause, 0, len(typed.Clauses))
		for _, clause := range typed.Clauses {
			condition, err := state.bindExpr(clause.Condition, ResultSingle)
			if err != nil {
				return nil, err
			}
			body, err := state.bindScopedBlock(clause.Body, false)
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, BoundIfClause{Span: clause.Span, Condition: condition, Body: body})
		}
		var elseBlock *BoundBlock
		if typed.ElseBlock != nil {
			body, err := state.bindScopedBlock(typed.ElseBlock, false)
			if err != nil {
				return nil, err
			}
			elseBlock = body
		}
		return BoundIfStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Clauses: clauses, ElseBlock: elseBlock}, nil
	case parser.WhileStat:
		condition, err := state.bindExpr(typed.Condition, ResultSingle)
		if err != nil {
			return nil, err
		}
		body, err := state.bindScopedBlock(typed.Body, true)
		if err != nil {
			return nil, err
		}
		return BoundWhileStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Condition: condition, Body: body}, nil
	case parser.RepeatUntilStat:
		body, condition, err := state.bindRepeatBlock(typed.Body, typed.Condition)
		if err != nil {
			return nil, err
		}
		return BoundRepeatStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Body: body, Condition: condition}, nil
	case parser.NumericForStat:
		initial, err := state.bindExpr(typed.Initial, ResultSingle)
		if err != nil {
			return nil, err
		}
		limit, err := state.bindExpr(typed.Limit, ResultSingle)
		if err != nil {
			return nil, err
		}
		var step BoundExpr
		if typed.Step != nil {
			step, err = state.bindExpr(typed.Step, ResultSingle)
			if err != nil {
				return nil, err
			}
		}
		body, scope, err := state.beginScopedBlock(typed.Body, true)
		if err != nil {
			return nil, err
		}
		counter := state.declareSymbol(state.currentFunction, scope, typed.Name.Text, typed.Name.Span, SymbolLocal)
		stats, err := state.bindStatList(typed.Body.Stats)
		state.endScopedBlock(scope)
		if err != nil {
			return nil, err
		}
		body.Stats = stats
		return BoundNumericForStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Counter: counter, Initial: initial, Limit: limit, Step: step, Body: body}, nil
	case parser.GenericForStat:
		iterators, err := state.bindExprList(typed.Iterators, ResultMulti)
		if err != nil {
			return nil, err
		}
		body, scope, err := state.beginScopedBlock(typed.Body, true)
		if err != nil {
			return nil, err
		}
		names := make([]SymbolID, 0, len(typed.Names))
		for _, name := range typed.Names {
			names = append(names, state.declareSymbol(state.currentFunction, scope, name.Text, name.Span, SymbolLocal))
		}
		stats, err := state.bindStatList(typed.Body.Stats)
		state.endScopedBlock(scope)
		if err != nil {
			return nil, err
		}
		body.Stats = stats
		return BoundGenericForStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Names: names, Iterators: iterators, Body: body}, nil
	case parser.DoStat:
		body, err := state.bindScopedBlock(typed.Body, false)
		if err != nil {
			return nil, err
		}
		return BoundDoStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Body: body}, nil
	case parser.BreakStat:
		if !state.inBreakableScope() {
			return nil, state.errorf(typed.SpanRange(), "no loop to break")
		}
		return BoundBreakStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}}, nil
	case parser.ReturnStat:
		if state.currentFunction == nil {
			return nil, state.errorf(typed.SpanRange(), "return outside function")
		}
		values, err := state.bindExprList(typed.Values, ResultTail)
		if err != nil {
			return nil, err
		}
		return BoundReturnStat{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Values: values}, nil
	default:
		return nil, state.errorf(stat.SpanRange(), "unsupported statement %T", stat)
	}
}

func (state *bindState) bindExprList(exprs []parser.Expr, tailMode ResultMode) ([]BoundExpr, error) {
	bound := make([]BoundExpr, 0, len(exprs))
	for index, expr := range exprs {
		mode := ResultSingle
		if index == len(exprs)-1 {
			mode = tailMode
		}
		item, err := state.bindExpr(expr, mode)
		if err != nil {
			return nil, err
		}
		bound = append(bound, item)
	}
	return bound, nil
}

func (state *bindState) bindExpr(expr parser.Expr, mode ResultMode) (BoundExpr, error) {
	switch typed := expr.(type) {
	case parser.NilExpr:
		return BoundNilExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}}, nil
	case parser.BoolExpr:
		return BoundBoolExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Value: typed.Value}, nil
	case parser.NumberExpr:
		return BoundNumberExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Raw: typed.Raw, Value: typed.Value}, nil
	case parser.StringExpr:
		return BoundStringExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Raw: typed.Raw, Value: typed.Value}, nil
	case parser.VarargExpr:
		if state.currentFunction == nil || !state.currentFunction.hasVararg {
			return nil, state.errorf(typed.SpanRange(), "cannot use '...' outside a vararg function")
		}
		return BoundVarargExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: mode}}, nil
	case parser.NameExpr:
		ref, err := state.resolveName(typed.Name)
		if err != nil {
			return nil, err
		}
		return BoundSymbolExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Ref: ref}, nil
	case parser.UnaryExpr:
		value, err := state.bindExpr(typed.Value, ResultSingle)
		if err != nil {
			return nil, err
		}
		return BoundUnaryExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Op: typed.Op, Value: value}, nil
	case parser.BinaryExpr:
		left, err := state.bindExpr(typed.Left, ResultSingle)
		if err != nil {
			return nil, err
		}
		right, err := state.bindExpr(typed.Right, ResultSingle)
		if err != nil {
			return nil, err
		}
		return BoundBinaryExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Op: typed.Op, Left: left, Right: right}, nil
	case parser.TableConstructorExpr:
		fields := make([]BoundTableField, 0, len(typed.Fields))
		for index, field := range typed.Fields {
			valueMode := ResultSingle
			if index == len(typed.Fields)-1 && field.Kind == parser.TableFieldArray {
				valueMode = ResultMulti
			}
			boundField := BoundTableField{Span: field.Span}
			switch field.Kind {
			case parser.TableFieldArray:
				value, err := state.bindExpr(field.Value, valueMode)
				if err != nil {
					return nil, err
				}
				boundField.Kind = BoundTableFieldArray
				boundField.Value = value
			case parser.TableFieldNamed:
				value, err := state.bindExpr(field.Value, ResultSingle)
				if err != nil {
					return nil, err
				}
				boundField.Kind = BoundTableFieldNamed
				boundField.Name = field.Name.Text
				boundField.Value = value
			case parser.TableFieldIndexed:
				key, err := state.bindExpr(field.Key, ResultSingle)
				if err != nil {
					return nil, err
				}
				value, err := state.bindExpr(field.Value, ResultSingle)
				if err != nil {
					return nil, err
				}
				boundField.Kind = BoundTableFieldIndexed
				boundField.Key = key
				boundField.Value = value
			default:
				return nil, state.errorf(field.Span, "unsupported table field kind %d", field.Kind)
			}
			fields = append(fields, boundField)
		}
		return BoundTableExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Fields: fields}, nil
	case parser.FunctionLiteralExpr:
		function, err := state.bindFunctionBody(typed.Body, "", false)
		if err != nil {
			return nil, err
		}
		return BoundFunctionExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Func: function}, nil
	case parser.IndexExpr:
		receiver, err := state.bindExpr(typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		index, err := state.bindExpr(typed.Index, ResultSingle)
		if err != nil {
			return nil, err
		}
		return BoundIndexExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Receiver: receiver, Index: index}, nil
	case parser.FieldExpr:
		receiver, err := state.bindExpr(typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		return BoundFieldExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Receiver: receiver, Name: typed.Name.Text}, nil
	case parser.MethodExpr:
		receiver, err := state.bindExpr(typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		return BoundFieldExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: ResultSingle}, Receiver: receiver, Name: typed.Name.Text}, nil
	case parser.CallExpr:
		callee, err := state.bindExpr(typed.Callee, ResultSingle)
		if err != nil {
			return nil, err
		}
		args, err := state.bindExprList(typed.Args, ResultMulti)
		if err != nil {
			return nil, err
		}
		return BoundCallExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: mode}, Callee: callee, Args: args}, nil
	case parser.MethodCallExpr:
		receiver, err := state.bindExpr(typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		args, err := state.bindExprList(typed.Args, ResultMulti)
		if err != nil {
			return nil, err
		}
		return BoundMethodCallExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Results: mode}, Receiver: receiver, Name: typed.Name.Text, Args: args}, nil
	case parser.ParenExpr:
		return state.bindExpr(typed.Inner, ResultSingle)
	default:
		return nil, state.errorf(expr.SpanRange(), "unsupported expression %T", expr)
	}
}

func (state *bindState) bindTarget(expr parser.AssignableExpr) (BoundTarget, error) {
	switch typed := expr.(type) {
	case parser.NameExpr:
		ref, err := state.resolveName(typed.Name)
		if err != nil {
			return nil, err
		}
		return BoundSymbolTarget{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Ref: ref}, nil
	case parser.IndexExpr:
		receiver, err := state.bindExpr(typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		index, err := state.bindExpr(typed.Index, ResultSingle)
		if err != nil {
			return nil, err
		}
		return BoundIndexTarget{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Receiver: receiver, Index: index}, nil
	case parser.FieldExpr:
		receiver, err := state.bindExpr(typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		return BoundFieldTarget{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Receiver: receiver, Name: typed.Name.Text}, nil
	default:
		return nil, state.errorf(expr.SpanRange(), "assignment target expected")
	}
}

func (state *bindState) bindNamedFunctionTarget(path []parser.Name, method parser.Name) (BoundTarget, error) {
	if len(path) == 0 {
		return nil, state.errorf(method.Span, "function name expected")
	}
	ref, err := state.resolveName(path[0])
	if err != nil {
		return nil, err
	}
	var current BoundExpr = BoundSymbolExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: path[0].Span}, Results: ResultSingle}, Ref: ref}
	for index := 1; index < len(path); index++ {
		current = BoundFieldExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: lexer.MergeSpans(current.SpanRange(), path[index].Span)}, Results: ResultSingle}, Receiver: current, Name: path[index].Text}
	}
	if method.Text != "" {
		return BoundFieldTarget{BoundInfo: BoundInfo{Span: lexer.MergeSpans(current.SpanRange(), method.Span)}, Receiver: current, Name: method.Text}, nil
	}
	switch typed := current.(type) {
	case BoundSymbolExpr:
		return BoundSymbolTarget{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Ref: typed.Ref}, nil
	case BoundFieldExpr:
		return BoundFieldTarget{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Receiver: typed.Receiver, Name: typed.Name}, nil
	case BoundIndexExpr:
		return BoundIndexTarget{BoundInfo: BoundInfo{Span: typed.SpanRange()}, Receiver: typed.Receiver, Index: typed.Index}, nil
	default:
		return nil, state.errorf(current.SpanRange(), "assignment target expected")
	}
}

func (state *bindState) bindScopedBlock(block *parser.Block, breakable bool) (*BoundBlock, error) {
	bound, scope, err := state.beginScopedBlock(block, breakable)
	if err != nil {
		return nil, err
	}
	stats, bindErr := state.bindStatList(block.Stats)
	state.endScopedBlock(scope)
	if bindErr != nil {
		return nil, bindErr
	}
	bound.Stats = stats
	return bound, nil
}

func (state *bindState) bindRepeatBlock(block *parser.Block, condition parser.Expr) (*BoundBlock, BoundExpr, error) {
	bound, scope, err := state.beginScopedBlock(block, true)
	if err != nil {
		return nil, nil, err
	}
	stats, bindErr := state.bindStatList(block.Stats)
	if bindErr != nil {
		state.endScopedBlock(scope)
		return nil, nil, bindErr
	}
	boundCondition, bindErr := state.bindExpr(condition, ResultSingle)
	state.endScopedBlock(scope)
	if bindErr != nil {
		return nil, nil, bindErr
	}
	bound.Stats = stats
	return bound, boundCondition, nil
}

func (state *bindState) beginScopedBlock(block *parser.Block, breakable bool) (*BoundBlock, *scopeState, error) {
	if block == nil {
		return nil, nil, state.errorf(lexer.Span{}, "block is nil")
	}
	scope := state.pushScope(breakable)
	return &BoundBlock{BoundInfo: BoundInfo{Span: block.SpanRange()}, Scope: scope.id, Breakable: breakable}, scope, nil
}

func (state *bindState) endScopedBlock(scope *scopeState) {
	if scope != nil {
		state.popScope(scope)
	}
}

func (state *bindState) resolveName(name parser.Name) (NameRef, error) {
	if state.currentFunction == nil {
		return NameRef{}, state.errorf(name.Span, "name resolution outside function")
	}
	return state.currentFunction.resolveName(name)
}

func (state *bindState) pushFunction(name string, hasVararg bool) *functionState {
	function := &functionState{
		state:        state,
		parent:       state.currentFunction,
		name:         name,
		hasVararg:    hasVararg,
		slots:        make(map[SymbolID]int),
		captureIndex: make(map[SymbolID]int),
	}
	state.currentFunction = function
	return function
}

func (state *bindState) popFunction(function *functionState) {
	if function != nil {
		state.currentFunction = function.parent
	}
}

func (state *bindState) pushScope(breakable bool) *scopeState {
	parent := InvalidScopeID
	var parentScope *scopeState
	if state.currentFunction != nil {
		parentScope = state.currentFunction.currentScope
		if parentScope != nil {
			parent = parentScope.id
		}
	}
	id := ScopeID(len(state.scopes) + 1)
	state.scopes = append(state.scopes, Scope{ID: id, Parent: parent})
	scope := &scopeState{id: id, parent: parentScope, breakable: breakable, bindings: make(map[string]SymbolID)}
	if state.currentFunction != nil {
		state.currentFunction.currentScope = scope
	}
	return scope
}

func (state *bindState) popScope(scope *scopeState) {
	if state.currentFunction == nil || scope == nil {
		return
	}
	state.currentFunction.currentScope = scope.parent
}

func (state *bindState) declareSymbol(function *functionState, scope *scopeState, name string, span lexer.Span, kind SymbolKind) SymbolID {
	id := SymbolID(len(state.symbols) + 1)
	state.symbols = append(state.symbols, Symbol{ID: id, Kind: kind, Name: name, DeclSpan: span, Scope: scope.id})
	scope.bindings[name] = id
	scopeRecord := state.scope(scope.id)
	scopeRecord.Symbols = append(scopeRecord.Symbols, id)
	slot := len(function.params) + len(function.locals)
	function.slots[id] = slot
	if kind == SymbolParam {
		function.params = append(function.params, id)
	} else {
		function.locals = append(function.locals, id)
	}
	return id
}

func (state *bindState) inBreakableScope() bool {
	if state.currentFunction == nil {
		return false
	}
	for scope := state.currentFunction.currentScope; scope != nil; scope = scope.parent {
		if scope.breakable {
			return true
		}
	}
	return false
}

func (state *bindState) markCaptured(symbolID SymbolID) {
	if symbolID == InvalidSymbolID {
		return
	}
	symbol := state.symbol(symbolID)
	if symbol.Kind != SymbolParam && symbol.Kind != SymbolLocal {
		return
	}
	scope := state.scope(symbol.Scope)
	scope.HasUpvalues = true
}

func (state *bindState) symbol(symbolID SymbolID) *Symbol {
	if symbolID == InvalidSymbolID {
		return nil
	}
	index := int(symbolID) - 1
	if index < 0 || index >= len(state.symbols) {
		return nil
	}
	return &state.symbols[index]
}

func (state *bindState) scope(scopeID ScopeID) *Scope {
	if scopeID == InvalidScopeID {
		return nil
	}
	index := int(scopeID) - 1
	if index < 0 || index >= len(state.scopes) {
		return nil
	}
	return &state.scopes[index]
}

func (state *bindState) errorf(span lexer.Span, format string, args ...any) error {
	return lexer.Errorf(lexer.PhaseBind, span, format, args...)
}

func (function *functionState) resolveName(name parser.Name) (NameRef, error) {
	if symbolID, kind, ok := function.lookupLocal(name.Text); ok {
		return NameRef{Kind: kind, Symbol: symbolID}, nil
	}
	if function.parent == nil {
		return NameRef{Kind: SymbolGlobal, GlobalName: name.Text}, nil
	}
	parentRef, err := function.parent.resolveForChild(name)
	if err != nil {
		return NameRef{}, err
	}
	if parentRef.Kind == SymbolGlobal {
		return parentRef, nil
	}
	if _, err := function.addCapture(name.Text, parentRef); err != nil {
		return NameRef{}, err
	}
	return NameRef{Kind: SymbolUpvalue, Symbol: parentRef.Symbol}, nil
}

func (function *functionState) resolveForChild(name parser.Name) (NameRef, error) {
	if symbolID, kind, ok := function.lookupLocal(name.Text); ok {
		function.state.markCaptured(symbolID)
		return NameRef{Kind: kind, Symbol: symbolID}, nil
	}
	if function.parent == nil {
		return NameRef{Kind: SymbolGlobal, GlobalName: name.Text}, nil
	}
	parentRef, err := function.parent.resolveForChild(name)
	if err != nil {
		return NameRef{}, err
	}
	if parentRef.Kind == SymbolGlobal {
		return parentRef, nil
	}
	if _, err := function.addCapture(name.Text, parentRef); err != nil {
		return NameRef{}, err
	}
	return NameRef{Kind: SymbolUpvalue, Symbol: parentRef.Symbol}, nil
}

func (function *functionState) lookupLocal(name string) (SymbolID, SymbolKind, bool) {
	for scope := function.currentScope; scope != nil; scope = scope.parent {
		if symbolID, ok := scope.bindings[name]; ok {
			symbol := function.state.symbol(symbolID)
			if symbol == nil {
				return InvalidSymbolID, SymbolInvalid, false
			}
			return symbolID, symbol.Kind, true
		}
	}
	return InvalidSymbolID, SymbolInvalid, false
}

func (function *functionState) addCapture(name string, parentRef NameRef) (int, error) {
	if index, ok := function.captureIndex[parentRef.Symbol]; ok {
		return index, nil
	}
	if function.parent == nil {
		return -1, function.state.errorf(lexer.Span{}, "upvalue capture without parent function")
	}
	capture := CaptureDesc{Name: name, Symbol: parentRef.Symbol}
	switch parentRef.Kind {
	case SymbolParam, SymbolLocal:
		index, ok := function.parent.slots[parentRef.Symbol]
		if !ok {
			return -1, function.state.errorf(lexer.Span{}, "captured local has no slot index")
		}
		capture.Source = CaptureFromLocal
		capture.Index = index
	case SymbolUpvalue:
		index, ok := function.parent.captureIndex[parentRef.Symbol]
		if !ok {
			return -1, function.state.errorf(lexer.Span{}, "captured upvalue has no parent capture index")
		}
		capture.Source = CaptureFromUpvalue
		capture.Index = index
	default:
		return -1, function.state.errorf(lexer.Span{}, "cannot capture symbol kind %s", parentRef.Kind)
	}
	index := len(function.captures)
	function.captures = append(function.captures, capture)
	function.captureIndex[parentRef.Symbol] = index
	return index, nil
}

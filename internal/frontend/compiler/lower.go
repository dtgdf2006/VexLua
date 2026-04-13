package compiler

import "vexlua/internal/frontend/lexer"

const (
	numericForIndexName   = "(for index)"
	numericForLimitName   = "(for limit)"
	numericForStepName    = "(for step)"
	genericForGenName     = "(for generator)"
	genericForStateName   = "(for state)"
	genericForControlName = "(for control)"
)

// LowerChunk normalizes bound syntax sugar into emitter-facing IR forms.
func LowerChunk(chunk *BoundChunk) (*BoundChunk, error) {
	if chunk == nil {
		return nil, lexer.Errorf(lexer.PhaseBind, lexer.Span{}, "bound chunk is nil")
	}
	if chunk.Func == nil {
		return nil, lexer.Errorf(lexer.PhaseBind, chunk.SpanRange(), "bound chunk function is nil")
	}
	state := &lowerState{chunk: chunk}
	if err := state.lowerFunc(chunk.Func); err != nil {
		return nil, err
	}
	return chunk, nil
}

type lowerState struct {
	chunk *BoundChunk
}

func (state *lowerState) lowerFunc(function *BoundFunc) error {
	if function == nil {
		return lexer.Errorf(lexer.PhaseBind, lexer.Span{}, "bound function is nil")
	}
	block, err := state.lowerBlock(function, function.Body)
	if err != nil {
		return err
	}
	function.Body = block
	return nil
}

func (state *lowerState) lowerBlock(function *BoundFunc, block *BoundBlock) (*BoundBlock, error) {
	if block == nil {
		return nil, lexer.Errorf(lexer.PhaseBind, lexer.Span{}, "bound block is nil")
	}
	stats := make([]BoundStat, 0, len(block.Stats))
	for _, stat := range block.Stats {
		lowered, err := state.lowerStat(function, stat)
		if err != nil {
			return nil, err
		}
		if lowered != nil {
			stats = append(stats, lowered)
		}
	}
	block.Stats = stats
	return block, nil
}

func (state *lowerState) lowerStat(function *BoundFunc, stat BoundStat) (BoundStat, error) {
	switch typed := stat.(type) {
	case nil:
		return nil, nil
	case BoundLocalDeclStat:
		values, err := state.lowerExprList(function, typed.Values, ResultMulti)
		if err != nil {
			return nil, err
		}
		typed.Values = values
		return typed, nil
	case BoundAssignStat:
		targets := make([]BoundTarget, 0, len(typed.Targets))
		for _, target := range typed.Targets {
			lowered, err := state.lowerTarget(function, target)
			if err != nil {
				return nil, err
			}
			targets = append(targets, lowered)
		}
		values, err := state.lowerExprList(function, typed.Values, ResultMulti)
		if err != nil {
			return nil, err
		}
		typed.Targets = targets
		typed.Values = values
		return typed, nil
	case BoundCallStat:
		call, err := state.lowerCallLike(function, typed.Call)
		if err != nil {
			return nil, err
		}
		typed.Call = call
		return typed, nil
	case BoundIfStat:
		clauses := make([]BoundIfClause, 0, len(typed.Clauses))
		for _, clause := range typed.Clauses {
			condition, err := state.lowerExpr(function, clause.Condition, ResultSingle)
			if err != nil {
				return nil, err
			}
			body, err := state.lowerBlock(function, clause.Body)
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, BoundIfClause{Span: clause.Span, Condition: condition, Body: body})
		}
		typed.Clauses = clauses
		if typed.ElseBlock != nil {
			body, err := state.lowerBlock(function, typed.ElseBlock)
			if err != nil {
				return nil, err
			}
			typed.ElseBlock = body
		}
		return typed, nil
	case BoundWhileStat:
		condition, err := state.lowerExpr(function, typed.Condition, ResultSingle)
		if err != nil {
			return nil, err
		}
		body, err := state.lowerBlock(function, typed.Body)
		if err != nil {
			return nil, err
		}
		typed.Condition = condition
		typed.Body = body
		return typed, nil
	case BoundRepeatStat:
		body, err := state.lowerBlock(function, typed.Body)
		if err != nil {
			return nil, err
		}
		condition, err := state.lowerExpr(function, typed.Condition, ResultSingle)
		if err != nil {
			return nil, err
		}
		typed.Body = body
		typed.Condition = condition
		return typed, nil
	case BoundNumericForStat:
		initial, err := state.lowerExpr(function, typed.Initial, ResultSingle)
		if err != nil {
			return nil, err
		}
		limit, err := state.lowerExpr(function, typed.Limit, ResultSingle)
		if err != nil {
			return nil, err
		}
		step, err := state.lowerExpr(function, typed.Step, ResultSingle)
		if err != nil {
			return nil, err
		}
		body, err := state.lowerBlock(function, typed.Body)
		if err != nil {
			return nil, err
		}
		if typed.IndexSymbol == InvalidSymbolID {
			typed.IndexSymbol = state.addHiddenLocal(function, typed.Body.Scope, numericForIndexName, typed.SpanRange())
		}
		if typed.LimitSymbol == InvalidSymbolID {
			typed.LimitSymbol = state.addHiddenLocal(function, typed.Body.Scope, numericForLimitName, typed.SpanRange())
		}
		if typed.StepSymbol == InvalidSymbolID {
			typed.StepSymbol = state.addHiddenLocal(function, typed.Body.Scope, numericForStepName, typed.SpanRange())
		}
		typed.Initial = initial
		typed.Limit = limit
		typed.Step = step
		typed.Body = body
		return typed, nil
	case BoundGenericForStat:
		iterators, err := state.lowerExprList(function, typed.Iterators, ResultMulti)
		if err != nil {
			return nil, err
		}
		body, err := state.lowerBlock(function, typed.Body)
		if err != nil {
			return nil, err
		}
		if typed.GeneratorSymbol == InvalidSymbolID {
			typed.GeneratorSymbol = state.addHiddenLocal(function, typed.Body.Scope, genericForGenName, typed.SpanRange())
		}
		if typed.StateSymbol == InvalidSymbolID {
			typed.StateSymbol = state.addHiddenLocal(function, typed.Body.Scope, genericForStateName, typed.SpanRange())
		}
		if typed.ControlSymbol == InvalidSymbolID {
			typed.ControlSymbol = state.addHiddenLocal(function, typed.Body.Scope, genericForControlName, typed.SpanRange())
		}
		typed.Iterators = iterators
		typed.Body = body
		return typed, nil
	case BoundDoStat:
		body, err := state.lowerBlock(function, typed.Body)
		if err != nil {
			return nil, err
		}
		typed.Body = body
		return typed, nil
	case BoundBreakStat:
		return typed, nil
	case BoundReturnStat:
		values, err := state.lowerExprList(function, typed.Values, ResultTail)
		if err != nil {
			return nil, err
		}
		typed.Values = values
		return typed, nil
	default:
		return stat, nil
	}
}

func (state *lowerState) lowerCallLike(function *BoundFunc, call BoundCallLike) (BoundCallLike, error) {
	if call == nil {
		return nil, lexer.Errorf(lexer.PhaseBind, lexer.Span{}, "bound call is nil")
	}
	lowered, err := state.lowerExpr(function, call, call.ResultMode())
	if err != nil {
		return nil, err
	}
	callLike, ok := lowered.(BoundCallLike)
	if !ok {
		return nil, lexer.Errorf(lexer.PhaseBind, lowered.SpanRange(), "lowered call is not call-like")
	}
	return callLike, nil
}

func (state *lowerState) lowerTarget(function *BoundFunc, target BoundTarget) (BoundTarget, error) {
	switch typed := target.(type) {
	case BoundSymbolTarget:
		return typed, nil
	case BoundIndexTarget:
		receiver, err := state.lowerExpr(function, typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		index, err := state.lowerExpr(function, typed.Index, ResultSingle)
		if err != nil {
			return nil, err
		}
		typed.Receiver = receiver
		typed.Index = index
		return typed, nil
	case BoundFieldTarget:
		receiver, err := state.lowerExpr(function, typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		typed.Receiver = receiver
		return typed, nil
	default:
		return target, nil
	}
}

func (state *lowerState) lowerExprList(function *BoundFunc, exprs []BoundExpr, tailMode ResultMode) ([]BoundExpr, error) {
	values := make([]BoundExpr, 0, len(exprs))
	for index, expr := range exprs {
		mode := ResultSingle
		if index == len(exprs)-1 {
			mode = tailMode
		}
		value, err := state.lowerExpr(function, expr, mode)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (state *lowerState) lowerExpr(function *BoundFunc, expr BoundExpr, mode ResultMode) (BoundExpr, error) {
	switch typed := expr.(type) {
	case nil:
		return nil, nil
	case BoundNilExpr:
		typed.Results = ResultSingle
		return typed, nil
	case BoundBoolExpr:
		typed.Results = ResultSingle
		return typed, nil
	case BoundNumberExpr:
		typed.Results = ResultSingle
		return typed, nil
	case BoundStringExpr:
		typed.Results = ResultSingle
		return typed, nil
	case BoundVarargExpr:
		typed.Results = mode
		return typed, nil
	case BoundSymbolExpr:
		typed.Results = ResultSingle
		return typed, nil
	case BoundUnaryExpr:
		value, err := state.lowerExpr(function, typed.Value, ResultSingle)
		if err != nil {
			return nil, err
		}
		typed.Results = ResultSingle
		typed.Value = value
		return typed, nil
	case BoundBinaryExpr:
		switch typed.Op {
		case lexer.TokenAnd, lexer.TokenOr:
			left, err := state.lowerExpr(function, typed.Left, ResultSingle)
			if err != nil {
				return nil, err
			}
			right, err := state.lowerExpr(function, typed.Right, ResultSingle)
			if err != nil {
				return nil, err
			}
			return BoundLogicalExpr{BoundExprInfo: BoundExprInfo{BoundInfo: typed.BoundInfo, Results: ResultSingle}, Op: typed.Op, Left: left, Right: right}, nil
		case lexer.TokenEqual, lexer.TokenNotEqual, lexer.TokenLessThan, lexer.TokenLessEqual, lexer.TokenGreaterThan, lexer.TokenGreaterEqual:
			left, err := state.lowerExpr(function, typed.Left, ResultSingle)
			if err != nil {
				return nil, err
			}
			right, err := state.lowerExpr(function, typed.Right, ResultSingle)
			if err != nil {
				return nil, err
			}
			return BoundCompareExpr{BoundExprInfo: BoundExprInfo{BoundInfo: typed.BoundInfo, Results: ResultSingle}, Op: typed.Op, Left: left, Right: right}, nil
		default:
			left, err := state.lowerExpr(function, typed.Left, ResultSingle)
			if err != nil {
				return nil, err
			}
			right, err := state.lowerExpr(function, typed.Right, ResultSingle)
			if err != nil {
				return nil, err
			}
			typed.Results = ResultSingle
			typed.Left = left
			typed.Right = right
			return typed, nil
		}
	case BoundLogicalExpr:
		left, err := state.lowerExpr(function, typed.Left, ResultSingle)
		if err != nil {
			return nil, err
		}
		right, err := state.lowerExpr(function, typed.Right, ResultSingle)
		if err != nil {
			return nil, err
		}
		typed.Results = ResultSingle
		typed.Left = left
		typed.Right = right
		return typed, nil
	case BoundCompareExpr:
		left, err := state.lowerExpr(function, typed.Left, ResultSingle)
		if err != nil {
			return nil, err
		}
		right, err := state.lowerExpr(function, typed.Right, ResultSingle)
		if err != nil {
			return nil, err
		}
		typed.Results = ResultSingle
		typed.Left = left
		typed.Right = right
		return typed, nil
	case BoundTableExpr:
		fields := make([]BoundTableField, 0, len(typed.Fields))
		arrayCount := 0
		hashCount := 0
		for index, field := range typed.Fields {
			lowered := BoundTableField{Span: field.Span, Kind: field.Kind, Name: field.Name}
			switch field.Kind {
			case BoundTableFieldArray:
				valueMode := ResultSingle
				if index == len(typed.Fields)-1 {
					valueMode = ResultMulti
				}
				value, err := state.lowerExpr(function, field.Value, valueMode)
				if err != nil {
					return nil, err
				}
				lowered.Value = value
				arrayCount++
			case BoundTableFieldNamed:
				value, err := state.lowerExpr(function, field.Value, ResultSingle)
				if err != nil {
					return nil, err
				}
				lowered.Value = value
				hashCount++
			case BoundTableFieldIndexed:
				key, err := state.lowerExpr(function, field.Key, ResultSingle)
				if err != nil {
					return nil, err
				}
				value, err := state.lowerExpr(function, field.Value, ResultSingle)
				if err != nil {
					return nil, err
				}
				lowered.Key = key
				lowered.Value = value
				hashCount++
			}
			fields = append(fields, lowered)
		}
		typed.Results = ResultSingle
		typed.Fields = fields
		typed.ArrayCount = arrayCount
		typed.HashCount = hashCount
		return typed, nil
	case BoundFunctionExpr:
		if err := state.lowerFunc(typed.Func); err != nil {
			return nil, err
		}
		typed.Results = ResultSingle
		return typed, nil
	case BoundIndexExpr:
		receiver, err := state.lowerExpr(function, typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		index, err := state.lowerExpr(function, typed.Index, ResultSingle)
		if err != nil {
			return nil, err
		}
		typed.Results = ResultSingle
		typed.Receiver = receiver
		typed.Index = index
		return typed, nil
	case BoundFieldExpr:
		receiver, err := state.lowerExpr(function, typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		typed.Results = ResultSingle
		typed.Receiver = receiver
		return typed, nil
	case BoundCallExpr:
		var callee BoundExpr
		var receiver BoundExpr
		var err error
		if typed.Callee != nil {
			callee, err = state.lowerExpr(function, typed.Callee, ResultSingle)
			if err != nil {
				return nil, err
			}
		}
		if typed.Receiver != nil {
			receiver, err = state.lowerExpr(function, typed.Receiver, ResultSingle)
			if err != nil {
				return nil, err
			}
		}
		args, err := state.lowerExprList(function, typed.Args, ResultMulti)
		if err != nil {
			return nil, err
		}
		typed.Results = mode
		typed.Callee = callee
		typed.Receiver = receiver
		typed.Args = args
		return typed, nil
	case BoundMethodCallExpr:
		receiver, err := state.lowerExpr(function, typed.Receiver, ResultSingle)
		if err != nil {
			return nil, err
		}
		args, err := state.lowerExprList(function, typed.Args, ResultMulti)
		if err != nil {
			return nil, err
		}
		return BoundCallExpr{BoundExprInfo: BoundExprInfo{BoundInfo: typed.BoundInfo, Results: mode}, Receiver: receiver, MethodName: typed.Name, Args: args}, nil
	default:
		return expr, nil
	}
}

func (state *lowerState) addHiddenLocal(function *BoundFunc, scopeID ScopeID, name string, span lexer.Span) SymbolID {
	if function == nil || scopeID == InvalidScopeID {
		return InvalidSymbolID
	}
	id := SymbolID(len(state.chunk.Symbols) + 1)
	state.chunk.Symbols = append(state.chunk.Symbols, Symbol{ID: id, Kind: SymbolLocal, Name: name, DeclSpan: span, Scope: scopeID})
	function.Locals = append(function.Locals, id)
	if scope := state.scope(scopeID); scope != nil {
		scope.Symbols = append(scope.Symbols, id)
	}
	return id
}

func (state *lowerState) scope(scopeID ScopeID) *Scope {
	if state.chunk == nil || scopeID == InvalidScopeID {
		return nil
	}
	index := int(scopeID) - 1
	if index < 0 || index >= len(state.chunk.Scopes) {
		return nil
	}
	return &state.chunk.Scopes[index]
}

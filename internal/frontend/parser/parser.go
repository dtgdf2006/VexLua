package parser

import (
	"errors"
	"fmt"

	"vexlua/internal/frontend/lexer"
)

// Parser is the Phase 2 recursive-descent parser stage.
// It mirrors Lua 5.1's explicit block/stat/expr split while keeping a
// Sparkplug-style explicit driver boundary.
type Parser struct{}

type parseState struct {
	scanner     *lexer.Scanner
	current     lexer.Token
	diagnostics []lexer.Diagnostic
	sourceName  string
	functionVararg []bool
	loopDepth   int
	initialized bool
	fatalError  error
}

type parseSyntaxError struct {
	diagnostic lexer.Diagnostic
}

func (err *parseSyntaxError) Error() string {
	if err == nil {
		return ""
	}
	return err.diagnostic.Error()
}

type binaryPriority struct {
	left  int
	right int
}

var binaryPriorities = map[lexer.TokenKind]binaryPriority{
	lexer.TokenPlus:         {left: 6, right: 6},
	lexer.TokenMinus:        {left: 6, right: 6},
	lexer.TokenStar:         {left: 7, right: 7},
	lexer.TokenSlash:        {left: 7, right: 7},
	lexer.TokenPercent:      {left: 7, right: 7},
	lexer.TokenCaret:        {left: 10, right: 9},
	lexer.TokenConcat:       {left: 5, right: 4},
	lexer.TokenNotEqual:     {left: 3, right: 3},
	lexer.TokenEqual:        {left: 3, right: 3},
	lexer.TokenLessThan:     {left: 3, right: 3},
	lexer.TokenLessEqual:    {left: 3, right: 3},
	lexer.TokenGreaterThan:  {left: 3, right: 3},
	lexer.TokenGreaterEqual: {left: 3, right: 3},
	lexer.TokenAnd:          {left: 2, right: 2},
	lexer.TokenOr:           {left: 1, right: 1},
}

const unaryPriority = 8

// NewParser constructs one parser stage instance.
func NewParser() *Parser {
	return &Parser{}
}

// ParseChunk parses one source buffer into a stable AST chunk.
func ParseChunk(name string, src []byte) (*Chunk, error) {
	return NewParser().ParseChunk(name, src)
}

// ParseChunk parses one source buffer into a stable AST chunk.
func (parserStage *Parser) ParseChunk(name string, src []byte) (*Chunk, error) {
	state := &parseState{scanner: lexer.NewScanner(name, src), sourceName: name, functionVararg: []bool{true}}
	if err := state.advance(); err != nil {
		return nil, err
	}
	chunk, err := state.parseChunk()
	if err != nil {
		return chunk, err
	}
	if state.fatalError != nil {
		return chunk, state.fatalError
	}
	if len(state.diagnostics) != 0 {
		return chunk, lexer.NewDiagnosticError(state.diagnostics...)
	}
	return chunk, nil
}

func (state *parseState) parseChunk() (*Chunk, error) {
	block, err := state.parseBlock()
	if err != nil {
		return nil, err
	}
	for state.current.Kind != lexer.TokenEOF {
		state.recordDiagnostic(state.syntaxDiagnostic(state.current.Span, "unexpected %s", state.describeToken(state.current)))
		if err := state.advance(); err != nil {
			return &Chunk{NodeInfo: NodeInfo{Span: block.SpanRange()}, Name: state.sourceName, Block: block}, err
		}
	}
	span := block.SpanRange()
	if !span.IsValid() {
		span = zeroSpan(state.current.Span.Start)
	}
	return &Chunk{NodeInfo: NodeInfo{Span: span}, Name: state.sourceName, Block: block}, nil
}

func (state *parseState) parseBlock() (*Block, error) {
	start := state.current.Span.Start
	if !start.IsValid() {
		start = lexer.StartPosition()
	}
	stats := make([]Stat, 0, 8)
	for !isBlockFollow(state.current.Kind) {
		stat, err := state.parseStatement()
		if err != nil {
			if !state.recordSyntaxError(err) {
				return nil, err
			}
			if err := state.synchronizeStatement(); err != nil {
				return nil, err
			}
			continue
		}
		if stat != nil {
			stats = append(stats, stat)
		}
		if statementTerminates(stat) {
			if state.current.Kind == lexer.TokenSemicolon {
				if err := state.advance(); err != nil {
					return nil, err
				}
			}
			break
		}
	}
	span := zeroSpan(start)
	if len(stats) != 0 {
		span = mergeStatSpans(stats)
	}
	return &Block{NodeInfo: NodeInfo{Span: span}, Stats: stats}, nil
}

func (state *parseState) parseStatement() (Stat, error) {
	switch state.current.Kind {
	case lexer.TokenSemicolon:
		token := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		return EmptyStat{NodeInfo: NodeInfo{Span: token.Span}}, nil
	case lexer.TokenIf:
		return state.parseIfStat()
	case lexer.TokenWhile:
		return state.parseWhileStat()
	case lexer.TokenDo:
		return state.parseDoStat()
	case lexer.TokenFor:
		return state.parseForStat()
	case lexer.TokenRepeat:
		return state.parseRepeatStat()
	case lexer.TokenFunction:
		return state.parseFunctionStat()
	case lexer.TokenLocal:
		return state.parseLocalStat()
	case lexer.TokenReturn:
		return state.parseReturnStat()
	case lexer.TokenBreak:
		token := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		if state.loopDepth == 0 {
			return nil, state.syntaxError(token.Span, "no loop to break")
		}
		return BreakStat{NodeInfo: NodeInfo{Span: token.Span}}, nil
	default:
		return state.parseAssignmentOrCallStat()
	}
}

func (state *parseState) parseIfStat() (Stat, error) {
	ifToken := state.current
	if err := state.advance(); err != nil {
		return nil, err
	}
	condition, err := state.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := state.expect(lexer.TokenThen, "%s expected", lexer.TokenThen); err != nil {
		return nil, err
	}
	body, err := state.parseBlock()
	if err != nil {
		return nil, err
	}
	clauses := []IfClause{{Span: lexer.MergeSpans(ifToken.Span, body.SpanRange()), Condition: condition, Body: body}}
	for state.current.Kind == lexer.TokenElseIf {
		elseifToken := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		condition, err = state.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := state.expect(lexer.TokenThen, "%s expected", lexer.TokenThen); err != nil {
			return nil, err
		}
		body, err = state.parseBlock()
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, IfClause{Span: lexer.MergeSpans(elseifToken.Span, body.SpanRange()), Condition: condition, Body: body})
	}
	var elseBlock *Block
	if state.current.Kind == lexer.TokenElse {
		if err := state.advance(); err != nil {
			return nil, err
		}
		elseBlock, err = state.parseBlock()
		if err != nil {
			return nil, err
		}
	}
	endToken, err := state.expect(lexer.TokenEnd, "%s expected", lexer.TokenEnd)
	if err != nil {
		return nil, err
	}
	return IfStat{NodeInfo: NodeInfo{Span: lexer.MergeSpans(ifToken.Span, endToken.Span)}, Clauses: clauses, ElseBlock: elseBlock}, nil
}

func (state *parseState) parseWhileStat() (Stat, error) {
	whileToken := state.current
	if err := state.advance(); err != nil {
		return nil, err
	}
	condition, err := state.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := state.expect(lexer.TokenDo, "%s expected", lexer.TokenDo); err != nil {
		return nil, err
	}
	body, err := state.parseLoopBlock()
	if err != nil {
		return nil, err
	}
	endToken, err := state.expect(lexer.TokenEnd, "%s expected", lexer.TokenEnd)
	if err != nil {
		return nil, err
	}
	return WhileStat{NodeInfo: NodeInfo{Span: lexer.MergeSpans(whileToken.Span, endToken.Span)}, Condition: condition, Body: body}, nil
}

func (state *parseState) parseDoStat() (Stat, error) {
	doToken := state.current
	if err := state.advance(); err != nil {
		return nil, err
	}
	body, err := state.parseBlock()
	if err != nil {
		return nil, err
	}
	endToken, err := state.expect(lexer.TokenEnd, "%s expected", lexer.TokenEnd)
	if err != nil {
		return nil, err
	}
	return DoStat{NodeInfo: NodeInfo{Span: lexer.MergeSpans(doToken.Span, endToken.Span)}, Body: body}, nil
}

func (state *parseState) parseForStat() (Stat, error) {
	forToken := state.current
	if err := state.advance(); err != nil {
		return nil, err
	}
	name, err := state.parseName()
	if err != nil {
		return nil, err
	}
	if state.current.Kind == lexer.TokenAssign {
		if err := state.advance(); err != nil {
			return nil, err
		}
		initial, err := state.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := state.expect(lexer.TokenComma, "%s expected", lexer.TokenComma); err != nil {
			return nil, err
		}
		limit, err := state.parseExpression()
		if err != nil {
			return nil, err
		}
		var step Expr
		if state.current.Kind == lexer.TokenComma {
			if err := state.advance(); err != nil {
				return nil, err
			}
			step, err = state.parseExpression()
			if err != nil {
				return nil, err
			}
		}
		if _, err := state.expect(lexer.TokenDo, "%s expected", lexer.TokenDo); err != nil {
			return nil, err
		}
		body, err := state.parseLoopBlock()
		if err != nil {
			return nil, err
		}
		endToken, err := state.expect(lexer.TokenEnd, "%s expected", lexer.TokenEnd)
		if err != nil {
			return nil, err
		}
		return NumericForStat{NodeInfo: NodeInfo{Span: lexer.MergeSpans(forToken.Span, endToken.Span)}, Name: name, Initial: initial, Limit: limit, Step: step, Body: body}, nil
	}
	names := []Name{name}
	for state.current.Kind == lexer.TokenComma {
		if err := state.advance(); err != nil {
			return nil, err
		}
		item, err := state.parseName()
		if err != nil {
			return nil, err
		}
		names = append(names, item)
	}
	if _, err := state.expect(lexer.TokenIn, "%s expected", lexer.TokenIn); err != nil {
		return nil, err
	}
	iterators, err := state.parseExpressionList()
	if err != nil {
		return nil, err
	}
	if _, err := state.expect(lexer.TokenDo, "%s expected", lexer.TokenDo); err != nil {
		return nil, err
	}
	body, err := state.parseLoopBlock()
	if err != nil {
		return nil, err
	}
	endToken, err := state.expect(lexer.TokenEnd, "%s expected", lexer.TokenEnd)
	if err != nil {
		return nil, err
	}
	return GenericForStat{NodeInfo: NodeInfo{Span: lexer.MergeSpans(forToken.Span, endToken.Span)}, Names: names, Iterators: iterators, Body: body}, nil
}

func (state *parseState) parseRepeatStat() (Stat, error) {
	repeatToken := state.current
	if err := state.advance(); err != nil {
		return nil, err
	}
	body, err := state.parseLoopBlock()
	if err != nil {
		return nil, err
	}
	if _, err := state.expect(lexer.TokenUntil, "%s expected", lexer.TokenUntil); err != nil {
		return nil, err
	}
	condition, err := state.parseExpression()
	if err != nil {
		return nil, err
	}
	return RepeatUntilStat{NodeInfo: NodeInfo{Span: lexer.MergeSpans(repeatToken.Span, condition.SpanRange())}, Body: body, Condition: condition}, nil
}

func (state *parseState) parseFunctionStat() (Stat, error) {
	functionToken := state.current
	if err := state.advance(); err != nil {
		return nil, err
	}
	path, method, err := state.parseFunctionName()
	if err != nil {
		return nil, err
	}
	body, err := state.parseFunctionBody()
	if err != nil {
		return nil, err
	}
	span := lexer.MergeSpans(functionToken.Span, body.SpanRange())
	if method.Text != "" {
		return MethodStat{NodeInfo: NodeInfo{Span: span}, Path: path, Method: method, Body: body}, nil
	}
	return FunctionStat{NodeInfo: NodeInfo{Span: span}, Path: path, Body: body}, nil
}

func (state *parseState) parseLocalStat() (Stat, error) {
	localToken := state.current
	if err := state.advance(); err != nil {
		return nil, err
	}
	if state.current.Kind == lexer.TokenFunction {
		functionToken := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		name, err := state.parseName()
		if err != nil {
			return nil, err
		}
		body, err := state.parseFunctionBody()
		if err != nil {
			return nil, err
		}
		span := lexer.MergeSpans(localToken.Span, body.SpanRange())
		if functionToken.Span.IsValid() {
			span = lexer.MergeSpans(localToken.Span, functionToken.Span)
			span = lexer.MergeSpans(span, body.SpanRange())
		}
		return LocalFunctionStat{NodeInfo: NodeInfo{Span: span}, Name: name, Body: body}, nil
	}
	names := make([]Name, 0, 4)
	name, err := state.parseName()
	if err != nil {
		return nil, err
	}
	names = append(names, name)
	for state.current.Kind == lexer.TokenComma {
		if err := state.advance(); err != nil {
			return nil, err
		}
		name, err = state.parseName()
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	values := make([]Expr, 0, 4)
	if state.current.Kind == lexer.TokenAssign {
		if err := state.advance(); err != nil {
			return nil, err
		}
		values, err = state.parseExpressionList()
		if err != nil {
			return nil, err
		}
	}
	span := lexer.MergeSpans(localToken.Span, names[len(names)-1].Span)
	if len(values) != 0 {
		span = lexer.MergeSpans(span, values[len(values)-1].SpanRange())
	}
	return LocalDeclStat{NodeInfo: NodeInfo{Span: span}, Names: names, Values: values}, nil
}

func (state *parseState) parseReturnStat() (Stat, error) {
	returnToken := state.current
	if err := state.advance(); err != nil {
		return nil, err
	}
	values := make([]Expr, 0, 2)
	if state.current.Kind != lexer.TokenSemicolon && !isBlockFollow(state.current.Kind) {
		exprs, err := state.parseExpressionList()
		if err != nil {
			return nil, err
		}
		values = exprs
	}
	span := returnToken.Span
	if len(values) != 0 {
		span = lexer.MergeSpans(span, values[len(values)-1].SpanRange())
	}
	return ReturnStat{NodeInfo: NodeInfo{Span: span}, Values: values}, nil
}

func (state *parseState) parseAssignmentOrCallStat() (Stat, error) {
	expr, err := state.parsePrefixExpr()
	if err != nil {
		return nil, err
	}
	if state.current.Kind == lexer.TokenAssign || state.current.Kind == lexer.TokenComma {
		target, ok := asAssignable(expr)
		if !ok {
			return nil, state.syntaxError(expr.SpanRange(), "assignment target expected")
		}
		targets := []AssignableExpr{target}
		for state.current.Kind == lexer.TokenComma {
			if err := state.advance(); err != nil {
				return nil, err
			}
			nextExpr, err := state.parsePrefixExpr()
			if err != nil {
				return nil, err
			}
			target, ok = asAssignable(nextExpr)
			if !ok {
				return nil, state.syntaxError(nextExpr.SpanRange(), "assignment target expected")
			}
			targets = append(targets, target)
		}
		if _, err := state.expect(lexer.TokenAssign, "%s expected", lexer.TokenAssign); err != nil {
			return nil, err
		}
		values, err := state.parseExpressionList()
		if err != nil {
			return nil, err
		}
		span := expr.SpanRange()
		if len(values) != 0 {
			span = lexer.MergeSpans(span, values[len(values)-1].SpanRange())
		}
		return AssignmentStat{NodeInfo: NodeInfo{Span: span}, Targets: targets, Values: values}, nil
	}
	switch typed := expr.(type) {
	case CallExpr:
		return CallStat{NodeInfo: NodeInfo{Span: typed.SpanRange()}, Call: typed}, nil
	case MethodCallExpr:
		return CallStat{NodeInfo: NodeInfo{Span: typed.SpanRange()}, Call: typed}, nil
	default:
		return nil, state.syntaxError(expr.SpanRange(), "statement must be assignment or function call")
	}
}

func (state *parseState) parseExpressionList() ([]Expr, error) {
	first, err := state.parseExpression()
	if err != nil {
		return nil, err
	}
	exprs := []Expr{first}
	for state.current.Kind == lexer.TokenComma {
		if err := state.advance(); err != nil {
			return nil, err
		}
		nextExpr, err := state.parseExpression()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, nextExpr)
	}
	return exprs, nil
}

func (state *parseState) parseExpression() (Expr, error) {
	return state.parseSubexpression(0)
}

func (state *parseState) parseSubexpression(limit int) (Expr, error) {
	var left Expr
	if isUnaryOperator(state.current.Kind) {
		opToken := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		value, err := state.parseSubexpression(unaryPriority)
		if err != nil {
			return nil, err
		}
		left = UnaryExpr{NodeInfo: NodeInfo{Span: lexer.MergeSpans(opToken.Span, value.SpanRange())}, Op: opToken.Kind, Value: value}
	} else {
		var err error
		left, err = state.parseSimpleExpr()
		if err != nil {
			return nil, err
		}
	}
	for {
		priority, ok := binaryPriorities[state.current.Kind]
		if !ok || priority.left <= limit {
			return left, nil
		}
		opToken := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		right, err := state.parseSubexpression(priority.right)
		if err != nil {
			return nil, err
		}
		left = BinaryExpr{NodeInfo: NodeInfo{Span: lexer.MergeSpans(left.SpanRange(), right.SpanRange())}, Op: opToken.Kind, Left: left, Right: right}
	}
}

func (state *parseState) parseSimpleExpr() (Expr, error) {
	switch state.current.Kind {
	case lexer.TokenNil:
		token := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		return NilExpr{NodeInfo: NodeInfo{Span: token.Span}}, nil
	case lexer.TokenTrue:
		token := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		return BoolExpr{NodeInfo: NodeInfo{Span: token.Span}, Value: true}, nil
	case lexer.TokenFalse:
		token := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		return BoolExpr{NodeInfo: NodeInfo{Span: token.Span}, Value: false}, nil
	case lexer.TokenNumber:
		token := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		return NumberExpr{NodeInfo: NodeInfo{Span: token.Span}, Raw: token.Lexeme, Value: token.NumberValue}, nil
	case lexer.TokenString:
		token := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		return StringExpr{NodeInfo: NodeInfo{Span: token.Span}, Raw: token.Lexeme, Value: token.StringValue}, nil
	case lexer.TokenDots:
		token := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		if !state.currentAllowsVararg() {
			return nil, state.syntaxError(token.Span, "cannot use '...' outside a vararg function")
		}
		return VarargExpr{NodeInfo: NodeInfo{Span: token.Span}}, nil
	case lexer.TokenLeftBrace:
		return state.parseTableConstructor()
	case lexer.TokenFunction:
		functionToken := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		body, err := state.parseFunctionBody()
		if err != nil {
			return nil, err
		}
		return FunctionLiteralExpr{NodeInfo: NodeInfo{Span: lexer.MergeSpans(functionToken.Span, body.SpanRange())}, Body: body}, nil
	default:
		return state.parsePrefixExpr()
	}
}

func (state *parseState) parsePrefixExpr() (Expr, error) {
	var expr Expr
	switch state.current.Kind {
	case lexer.TokenName:
		name, err := state.parseName()
		if err != nil {
			return nil, err
		}
		expr = NameExpr{NodeInfo: NodeInfo{Span: name.Span}, Name: name}
	case lexer.TokenLeftParen:
		openToken := state.current
		if err := state.advance(); err != nil {
			return nil, err
		}
		inner, err := state.parseExpression()
		if err != nil {
			return nil, err
		}
		closeToken, err := state.expect(lexer.TokenRightParen, "%s expected", lexer.TokenRightParen)
		if err != nil {
			return nil, err
		}
		expr = ParenExpr{NodeInfo: NodeInfo{Span: lexer.MergeSpans(openToken.Span, closeToken.Span)}, Inner: inner}
	default:
		return nil, state.syntaxError(state.current.Span, "expression expected")
	}
	for {
		switch state.current.Kind {
		case lexer.TokenDot:
			if err := state.advance(); err != nil {
				return nil, err
			}
			name, err := state.parseName()
			if err != nil {
				return nil, err
			}
			expr = FieldExpr{NodeInfo: NodeInfo{Span: lexer.MergeSpans(expr.SpanRange(), name.Span)}, Receiver: expr, Name: name}
		case lexer.TokenLeftBracket:
			openToken := state.current
			if err := state.advance(); err != nil {
				return nil, err
			}
			index, err := state.parseExpression()
			if err != nil {
				return nil, err
			}
			closeToken, err := state.expect(lexer.TokenRightBracket, "%s expected", lexer.TokenRightBracket)
			if err != nil {
				return nil, err
			}
			span := lexer.MergeSpans(expr.SpanRange(), openToken.Span)
			span = lexer.MergeSpans(span, index.SpanRange())
			span = lexer.MergeSpans(span, closeToken.Span)
			expr = IndexExpr{NodeInfo: NodeInfo{Span: span}, Receiver: expr, Index: index}
		case lexer.TokenColon:
			if err := state.advance(); err != nil {
				return nil, err
			}
			name, err := state.parseName()
			if err != nil {
				return nil, err
			}
			if !isFuncArgStart(state.current.Kind) {
				return nil, state.syntaxError(name.Span, "function arguments expected")
			}
			args, argSpan, err := state.parseFuncArgs(name.Span.End.Line)
			if err != nil {
				return nil, err
			}
			expr = MethodCallExpr{NodeInfo: NodeInfo{Span: lexer.MergeSpans(expr.SpanRange(), argSpan)}, Receiver: expr, Name: name, Args: args}
		case lexer.TokenLeftParen, lexer.TokenLeftBrace, lexer.TokenString:
			args, argSpan, err := state.parseFuncArgs(expr.SpanRange().End.Line)
			if err != nil {
				return nil, err
			}
			expr = CallExpr{NodeInfo: NodeInfo{Span: lexer.MergeSpans(expr.SpanRange(), argSpan)}, Callee: expr, Args: args}
		default:
			return expr, nil
		}
	}
}

func (state *parseState) parseFuncArgs(previousLine int) ([]Expr, lexer.Span, error) {
	switch state.current.Kind {
	case lexer.TokenLeftParen:
		openToken := state.current
		if previousLine != 0 && openToken.Span.Start.Line != previousLine {
			return nil, lexer.Span{}, state.syntaxError(openToken.Span, "ambiguous syntax (function call x new statement)")
		}
		if err := state.advance(); err != nil {
			return nil, lexer.Span{}, err
		}
		args := make([]Expr, 0, 4)
		if state.current.Kind != lexer.TokenRightParen {
			exprs, err := state.parseExpressionList()
			if err != nil {
				return nil, lexer.Span{}, err
			}
			args = exprs
		}
		closeToken, err := state.expect(lexer.TokenRightParen, "%s expected", lexer.TokenRightParen)
		if err != nil {
			return nil, lexer.Span{}, err
		}
		return args, lexer.MergeSpans(openToken.Span, closeToken.Span), nil
	case lexer.TokenLeftBrace:
		constructor, err := state.parseTableConstructor()
		if err != nil {
			return nil, lexer.Span{}, err
		}
		return []Expr{constructor}, constructor.SpanRange(), nil
	case lexer.TokenString:
		token := state.current
		if err := state.advance(); err != nil {
			return nil, lexer.Span{}, err
		}
		arg := StringExpr{NodeInfo: NodeInfo{Span: token.Span}, Raw: token.Lexeme, Value: token.StringValue}
		return []Expr{arg}, token.Span, nil
	default:
		return nil, lexer.Span{}, state.syntaxError(state.current.Span, "function arguments expected")
	}
}

func (state *parseState) parseTableConstructor() (Expr, error) {
	openToken, err := state.expect(lexer.TokenLeftBrace, "%s expected", lexer.TokenLeftBrace)
	if err != nil {
		return nil, err
	}
	fields := make([]TableField, 0, 4)
	for state.current.Kind != lexer.TokenRightBrace && state.current.Kind != lexer.TokenEOF {
		field, err := state.parseTableField()
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
		if state.current.Kind == lexer.TokenComma || state.current.Kind == lexer.TokenSemicolon {
			if err := state.advance(); err != nil {
				return nil, err
			}
			continue
		}
		if state.current.Kind != lexer.TokenRightBrace {
			return nil, state.syntaxError(state.current.Span, "%s, %s, or %s expected", lexer.TokenComma, lexer.TokenSemicolon, lexer.TokenRightBrace)
		}
	}
	closeToken, err := state.expect(lexer.TokenRightBrace, "%s expected", lexer.TokenRightBrace)
	if err != nil {
		return nil, err
	}
	return TableConstructorExpr{NodeInfo: NodeInfo{Span: lexer.MergeSpans(openToken.Span, closeToken.Span)}, Fields: fields}, nil
}

func (state *parseState) parseTableField() (TableField, error) {
	startSpan := state.current.Span
	if state.current.Kind == lexer.TokenLeftBracket {
		if err := state.advance(); err != nil {
			return TableField{}, err
		}
		key, err := state.parseExpression()
		if err != nil {
			return TableField{}, err
		}
		if _, err := state.expect(lexer.TokenRightBracket, "%s expected", lexer.TokenRightBracket); err != nil {
			return TableField{}, err
		}
		if _, err := state.expect(lexer.TokenAssign, "%s expected", lexer.TokenAssign); err != nil {
			return TableField{}, err
		}
		value, err := state.parseExpression()
		if err != nil {
			return TableField{}, err
		}
		return TableField{Span: lexer.MergeSpans(startSpan, value.SpanRange()), Kind: TableFieldIndexed, Key: key, Value: value}, nil
	}
	if state.current.Kind == lexer.TokenName {
		lookahead, err := state.lookahead()
		if err != nil {
			return TableField{}, err
		}
		if lookahead.Kind == lexer.TokenAssign {
			name, err := state.parseName()
			if err != nil {
				return TableField{}, err
			}
			if _, err := state.expect(lexer.TokenAssign, "%s expected", lexer.TokenAssign); err != nil {
				return TableField{}, err
			}
			value, err := state.parseExpression()
			if err != nil {
				return TableField{}, err
			}
			return TableField{Span: lexer.MergeSpans(name.Span, value.SpanRange()), Kind: TableFieldNamed, Name: name, Value: value}, nil
		}
	}
	value, err := state.parseExpression()
	if err != nil {
		return TableField{}, err
	}
	return TableField{Span: value.SpanRange(), Kind: TableFieldArray, Value: value}, nil
}

func (state *parseState) parseFunctionBody() (*FunctionBody, error) {
	openToken, err := state.expect(lexer.TokenLeftParen, "%s expected", lexer.TokenLeftParen)
	if err != nil {
		return nil, err
	}
	params := make([]Name, 0, 4)
	hasVararg := false
	if state.current.Kind != lexer.TokenRightParen {
		for {
			switch state.current.Kind {
			case lexer.TokenName:
				name, err := state.parseName()
				if err != nil {
					return nil, err
				}
				params = append(params, name)
			case lexer.TokenDots:
				hasVararg = true
				if err := state.advance(); err != nil {
					return nil, err
				}
			default:
				return nil, state.syntaxError(state.current.Span, "<name> or %s expected", lexer.TokenDots)
			}
			if state.current.Kind != lexer.TokenComma {
				break
			}
			if hasVararg {
				return nil, state.syntaxError(state.current.Span, "%s must be the last parameter", lexer.TokenDots)
			}
			if err := state.advance(); err != nil {
				return nil, err
			}
		}
	}
	if _, err := state.expect(lexer.TokenRightParen, "%s expected", lexer.TokenRightParen); err != nil {
		return nil, err
	}
	outerLoopDepth := state.loopDepth
	state.loopDepth = 0
	state.functionVararg = append(state.functionVararg, hasVararg)
	defer func() {
		state.loopDepth = outerLoopDepth
		state.functionVararg = state.functionVararg[:len(state.functionVararg)-1]
	}()
	body, err := state.parseBlock()
	if err != nil {
		return nil, err
	}
	endToken, err := state.expect(lexer.TokenEnd, "%s expected", lexer.TokenEnd)
	if err != nil {
		return nil, err
	}
	return &FunctionBody{NodeInfo: NodeInfo{Span: lexer.MergeSpans(openToken.Span, endToken.Span)}, Params: params, HasVararg: hasVararg, Block: body}, nil
}

func (state *parseState) parseFunctionName() ([]Name, Name, error) {
	path := make([]Name, 0, 2)
	name, err := state.parseName()
	if err != nil {
		return nil, Name{}, err
	}
	path = append(path, name)
	for state.current.Kind == lexer.TokenDot {
		if err := state.advance(); err != nil {
			return nil, Name{}, err
		}
		name, err = state.parseName()
		if err != nil {
			return nil, Name{}, err
		}
		path = append(path, name)
	}
	if state.current.Kind != lexer.TokenColon {
		return path, Name{}, nil
	}
	if err := state.advance(); err != nil {
		return nil, Name{}, err
	}
	method, err := state.parseName()
	if err != nil {
		return nil, Name{}, err
	}
	return path, method, nil
}

func (state *parseState) parseName() (Name, error) {
	if state.current.Kind != lexer.TokenName {
		return Name{}, state.syntaxError(state.current.Span, "%s expected", lexer.TokenName)
	}
	token := state.current
	if err := state.advance(); err != nil {
		return Name{}, err
	}
	return Name{Span: token.Span, Text: token.Lexeme, Token: token}, nil
}

func (state *parseState) advance() error {
	token, err := state.scanner.Next()
	if err != nil {
		state.fatalError = err
		return err
	}
	state.current = token
	state.initialized = true
	return nil
}

func (state *parseState) lookahead() (lexer.Token, error) {
	token, err := state.scanner.Lookahead()
	if err != nil {
		state.fatalError = err
		return lexer.Token{}, err
	}
	return token, nil
}

func (state *parseState) expect(kind lexer.TokenKind, format string, args ...any) (lexer.Token, error) {
	if state.current.Kind != kind {
		return lexer.Token{}, state.syntaxError(state.current.Span, format, args...)
	}
	token := state.current
	if err := state.advance(); err != nil {
		return lexer.Token{}, err
	}
	return token, nil
}

func (state *parseState) recordSyntaxError(err error) bool {
	var syntaxErr *parseSyntaxError
	if !errors.As(err, &syntaxErr) {
		return false
	}
	state.recordDiagnostic(syntaxErr.diagnostic)
	return true
}

func (state *parseState) recordDiagnostic(diagnostic lexer.Diagnostic) {
	state.diagnostics = append(state.diagnostics, diagnostic)
}

func (state *parseState) syntaxError(span lexer.Span, format string, args ...any) error {
	return &parseSyntaxError{diagnostic: state.syntaxDiagnostic(span, format, args...)}
}

func (state *parseState) syntaxDiagnostic(span lexer.Span, format string, args ...any) lexer.Diagnostic {
	if !span.IsValid() {
		span = zeroSpan(state.current.Span.Start)
	}
	return lexer.Diagnostic{Phase: lexer.PhaseParse, Severity: lexer.SeverityError, Message: formatMessage(format, args...), Span: span}
}

func (state *parseState) synchronizeStatement() error {
	if state.current.Kind == lexer.TokenSemicolon {
		return state.advance()
	}
	for state.current.Kind != lexer.TokenEOF {
		if isBlockFollow(state.current.Kind) || isStatementStart(state.current.Kind) {
			return nil
		}
		if err := state.advance(); err != nil {
			return err
		}
	}
	return nil
}

func (state *parseState) describeToken(token lexer.Token) string {
	if token.Kind == lexer.TokenEOF {
		return "end of file"
	}
	return token.Kind.String()
}

func asAssignable(expr Expr) (AssignableExpr, bool) {
	target, ok := expr.(AssignableExpr)
	return target, ok
}

func statementTerminates(stat Stat) bool {
	switch stat.(type) {
	case ReturnStat, BreakStat:
		return true
	default:
		return false
	}
}

func (state *parseState) parseLoopBlock() (*Block, error) {
	state.loopDepth++
	defer func() {
		state.loopDepth--
	}()
	return state.parseBlock()
}

func (state *parseState) currentAllowsVararg() bool {
	if state == nil || len(state.functionVararg) == 0 {
		return false
	}
	return state.functionVararg[len(state.functionVararg)-1]
}

func isUnaryOperator(kind lexer.TokenKind) bool {
	switch kind {
	case lexer.TokenNot, lexer.TokenMinus, lexer.TokenHash:
		return true
	default:
		return false
	}
}

func isBlockFollow(kind lexer.TokenKind) bool {
	switch kind {
	case lexer.TokenElse, lexer.TokenElseIf, lexer.TokenEnd, lexer.TokenUntil, lexer.TokenEOF:
		return true
	default:
		return false
	}
}

func isStatementStart(kind lexer.TokenKind) bool {
	switch kind {
	case lexer.TokenSemicolon, lexer.TokenIf, lexer.TokenWhile, lexer.TokenDo, lexer.TokenFor, lexer.TokenRepeat, lexer.TokenFunction, lexer.TokenLocal, lexer.TokenReturn, lexer.TokenBreak, lexer.TokenName, lexer.TokenLeftParen:
		return true
	default:
		return false
	}
}

func isFuncArgStart(kind lexer.TokenKind) bool {
	switch kind {
	case lexer.TokenLeftParen, lexer.TokenLeftBrace, lexer.TokenString:
		return true
	default:
		return false
	}
}

func zeroSpan(position lexer.Position) lexer.Span {
	if !position.IsValid() {
		position = lexer.StartPosition()
	}
	return lexer.Span{Start: position, End: position}
}

func mergeStatSpans(stats []Stat) lexer.Span {
	if len(stats) == 0 {
		return lexer.Span{}
	}
	span := stats[0].SpanRange()
	for index := 1; index < len(stats); index++ {
		span = lexer.MergeSpans(span, stats[index].SpanRange())
	}
	return span
}

func formatMessage(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

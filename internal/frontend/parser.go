package frontend

import (
	"fmt"
	"strconv"
)

type Parser struct {
	tokens []Token
	index  int
}

func Parse(source string) (*Chunk, error) {
	tokens, err := Lex(source)
	if err != nil {
		return nil, err
	}
	p := &Parser{tokens: tokens}
	return p.parseBlockUntil(TokenEOF)
}

func (p *Parser) parseBlockUntil(terminators ...TokenType) (*Chunk, error) {
	chunk := &Chunk{Statements: make([]Stmt, 0, 16)}
	for !p.isTerminator(terminators...) && p.current().Type != TokenEOF {
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		chunk.Statements = append(chunk.Statements, stmt)
		if p.match(TokenSemi) {
			p.advance()
		}
	}
	return chunk, nil
}

func (p *Parser) parseStmt() (Stmt, error) {
	switch p.current().Type {
	case TokenLocal:
		p.advance()
		if p.match(TokenFunction) {
			p.advance()
			return p.parseFunctionStmt(true)
		}
		names, err := p.parseNameList()
		if err != nil {
			return nil, err
		}
		values := make([]Expr, 0, len(names))
		if p.match(TokenAssign) {
			p.advance()
			values, err = p.parseExprList()
			if err != nil {
				return nil, err
			}
		}
		return &LocalAssignStmt{Names: names, Values: values}, nil
	case TokenFunction:
		p.advance()
		return p.parseFunctionStmt(false)
	case TokenBreak:
		p.advance()
		return &BreakStmt{}, nil
	case TokenIf:
		return p.parseIfStmt()
	case TokenWhile:
		return p.parseWhileStmt()
	case TokenRepeat:
		return p.parseRepeatStmt()
	case TokenFor:
		return p.parseForStmt()
	case TokenReturn:
		p.advance()
		if p.match(TokenEnd) || p.match(TokenElse) || p.match(TokenElseif) || p.match(TokenUntil) || p.match(TokenEOF) || p.match(TokenSemi) {
			return &ReturnStmt{Values: nil}, nil
		}
		values, err := p.parseExprList()
		if err != nil {
			return nil, err
		}
		return &ReturnStmt{Values: values}, nil
	default:
		expr, err := p.parsePrefixExpr()
		if err != nil {
			return nil, err
		}
		if p.match(TokenComma) || p.match(TokenAssign) {
			targets := []Expr{expr}
			for p.match(TokenComma) {
				p.advance()
				nextTarget, err := p.parsePrefixExpr()
				if err != nil {
					return nil, err
				}
				targets = append(targets, nextTarget)
			}
			if !p.match(TokenAssign) {
				return nil, fmt.Errorf("assignment statement missing '=' at offset %d", p.current().Offset)
			}
			p.advance()
			values, err := p.parseExprList()
			if err != nil {
				return nil, err
			}
			return &AssignStmt{Targets: targets, Values: values}, nil
		}
		switch expr.(type) {
		case *CallExpr, *MethodCallExpr:
			return &ExprStmt{Expr: expr}, nil
		default:
			return nil, fmt.Errorf("statement must be assignment or function call at offset %d", p.current().Offset)
		}
	}
}

func (p *Parser) parseIfStmt() (Stmt, error) {
	if _, err := p.expect(TokenIf); err != nil {
		return nil, err
	}
	clauses := make([]IfClause, 0, 2)
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenThen); err != nil {
		return nil, err
	}
	body, err := p.parseBlockUntil(TokenElseif, TokenElse, TokenEnd)
	if err != nil {
		return nil, err
	}
	clauses = append(clauses, IfClause{Cond: cond, Body: body.Statements})
	for p.match(TokenElseif) {
		p.advance()
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokenThen); err != nil {
			return nil, err
		}
		body, err := p.parseBlockUntil(TokenElseif, TokenElse, TokenEnd)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, IfClause{Cond: cond, Body: body.Statements})
	}
	var elseBody []Stmt
	if p.match(TokenElse) {
		p.advance()
		body, err := p.parseBlockUntil(TokenEnd)
		if err != nil {
			return nil, err
		}
		elseBody = body.Statements
	}
	if _, err := p.expect(TokenEnd); err != nil {
		return nil, err
	}
	return &IfStmt{Clauses: clauses, ElseBody: elseBody}, nil
}

func (p *Parser) parseWhileStmt() (Stmt, error) {
	if _, err := p.expect(TokenWhile); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenDo); err != nil {
		return nil, err
	}
	body, err := p.parseBlockUntil(TokenEnd)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenEnd); err != nil {
		return nil, err
	}
	return &WhileStmt{Cond: cond, Body: body.Statements}, nil
}

func (p *Parser) parseRepeatStmt() (Stmt, error) {
	if _, err := p.expect(TokenRepeat); err != nil {
		return nil, err
	}
	body, err := p.parseBlockUntil(TokenUntil)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenUntil); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &RepeatStmt{Body: body.Statements, Cond: cond}, nil
}

func (p *Parser) parseForStmt() (Stmt, error) {
	if _, err := p.expect(TokenFor); err != nil {
		return nil, err
	}
	name, err := p.expect(TokenName)
	if err != nil {
		return nil, err
	}
	if p.match(TokenAssign) {
		p.advance()
		start, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokenComma); err != nil {
			return nil, err
		}
		limit, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		step := Expr(&NumberExpr{Value: 1})
		if p.match(TokenComma) {
			p.advance()
			step, err = p.parseExpr()
			if err != nil {
				return nil, err
			}
		}
		if _, err := p.expect(TokenDo); err != nil {
			return nil, err
		}
		body, err := p.parseBlockUntil(TokenEnd)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokenEnd); err != nil {
			return nil, err
		}
		return &ForNumericStmt{Name: name.Literal, Start: start, Limit: limit, Step: step, Body: body.Statements}, nil
	}
	names := []string{name.Literal}
	for p.match(TokenComma) {
		p.advance()
		part, err := p.expect(TokenName)
		if err != nil {
			return nil, err
		}
		names = append(names, part.Literal)
	}
	if _, err := p.expect(TokenIn); err != nil {
		return nil, err
	}
	exprs, err := p.parseExprList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenDo); err != nil {
		return nil, err
	}
	body, err := p.parseBlockUntil(TokenEnd)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenEnd); err != nil {
		return nil, err
	}
	return &ForGenericStmt{Names: names, Exprs: exprs, Body: body.Statements}, nil
}

func (p *Parser) parseFunctionStmt(local bool) (Stmt, error) {
	if local {
		name, err := p.expect(TokenName)
		if err != nil {
			return nil, err
		}
		params, vararg, body, err := p.parseFunctionBody(false)
		if err != nil {
			return nil, err
		}
		return &FunctionStmt{Local: true, Name: name.Literal, Params: params, Vararg: vararg, Body: body}, nil
	}
	target, injectSelf, err := p.parseFunctionNameTarget()
	if err != nil {
		return nil, err
	}
	params, vararg, body, err := p.parseFunctionBody(injectSelf)
	if err != nil {
		return nil, err
	}
	return &FunctionStmt{Local: false, Target: target, Params: params, Vararg: vararg, Body: body}, nil
}

func (p *Parser) parseFunctionNameTarget() (Expr, bool, error) {
	name, err := p.expect(TokenName)
	if err != nil {
		return nil, false, err
	}
	var target Expr = &NameExpr{Name: name.Literal}
	injectSelf := false
	for p.match(TokenDot) {
		p.advance()
		part, err := p.expect(TokenName)
		if err != nil {
			return nil, false, err
		}
		target = &FieldExpr{Target: target, Name: part.Literal}
	}
	if p.match(TokenColon) {
		p.advance()
		part, err := p.expect(TokenName)
		if err != nil {
			return nil, false, err
		}
		target = &FieldExpr{Target: target, Name: part.Literal}
		injectSelf = true
	}
	return target, injectSelf, nil
}

func (p *Parser) parseFunctionBody(injectSelf bool) ([]string, bool, []Stmt, error) {
	if _, err := p.expect(TokenLParen); err != nil {
		return nil, false, nil, err
	}
	params := make([]string, 0, 4)
	if injectSelf {
		params = append(params, "self")
	}
	vararg := false
	if !p.match(TokenRParen) {
		for {
			if p.match(TokenEllipsis) {
				p.advance()
				vararg = true
				break
			}
			name, err := p.expect(TokenName)
			if err != nil {
				return nil, false, nil, err
			}
			params = append(params, name.Literal)
			if !p.match(TokenComma) {
				break
			}
			p.advance()
		}
	}
	if _, err := p.expect(TokenRParen); err != nil {
		return nil, false, nil, err
	}
	chunk, err := p.parseBlockUntil(TokenEnd)
	if err != nil {
		return nil, false, nil, err
	}
	if _, err := p.expect(TokenEnd); err != nil {
		return nil, false, nil, err
	}
	return params, vararg, chunk.Statements, nil
}

func (p *Parser) parseNameList() ([]string, error) {
	name, err := p.expect(TokenName)
	if err != nil {
		return nil, err
	}
	names := []string{name.Literal}
	for p.match(TokenComma) {
		p.advance()
		name, err := p.expect(TokenName)
		if err != nil {
			return nil, err
		}
		names = append(names, name.Literal)
	}
	return names, nil
}

func (p *Parser) parseExprList() ([]Expr, error) {
	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	exprs := []Expr{first}
	for p.match(TokenComma) {
		p.advance()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
	}
	return exprs, nil
}

func (p *Parser) parseExpr() (Expr, error) {
	return p.parseOr()
}

func (p *Parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.match(TokenOr) {
		op := p.current().Type
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseAnd() (Expr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.match(TokenAnd) {
		op := p.current().Type
		p.advance()
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseComparison() (Expr, error) {
	left, err := p.parseConcat()
	if err != nil {
		return nil, err
	}
	for p.match(TokenEqual) || p.match(TokenNotEqual) || p.match(TokenLess) || p.match(TokenLessEqual) || p.match(TokenGreater) || p.match(TokenGreaterEqual) {
		op := p.current().Type
		p.advance()
		right, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseConcat() (Expr, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	if p.match(TokenConcat) {
		op := p.current().Type
		p.advance()
		right, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: op, Left: left, Right: right}, nil
	}
	return left, nil
}

func (p *Parser) parseAdditive() (Expr, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for p.match(TokenPlus) || p.match(TokenMinus) {
		op := p.current().Type
		p.advance()
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseMultiplicative() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.match(TokenStar) || p.match(TokenSlash) || p.match(TokenPercent) {
		op := p.current().Type
		p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseUnary() (Expr, error) {
	if p.match(TokenMinus) || p.match(TokenNot) || p.match(TokenHash) {
		op := p.current().Type
		p.advance()
		expr, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: op, Expr: expr}, nil
	}
	return p.parsePower()
}

func (p *Parser) parsePower() (Expr, error) {
	left, err := p.parsePrefixExpr()
	if err != nil {
		return nil, err
	}
	if p.match(TokenCaret) {
		op := p.current().Type
		p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: op, Left: left, Right: right}, nil
	}
	return left, nil
}

func (p *Parser) parsePrefixExpr() (Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch p.current().Type {
		case TokenDot:
			p.advance()
			name, err := p.expect(TokenName)
			if err != nil {
				return nil, err
			}
			expr = &FieldExpr{Target: expr, Name: name.Literal}
		case TokenLBracket:
			p.advance()
			key, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(TokenRBracket); err != nil {
				return nil, err
			}
			expr = &IndexExpr{Target: expr, Key: key}
		case TokenLParen:
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			expr = &CallExpr{Callee: expr, Args: args}
		case TokenColon:
			p.advance()
			name, err := p.expect(TokenName)
			if err != nil {
				return nil, err
			}
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			expr = &MethodCallExpr{Receiver: expr, Name: name.Literal, Args: args}
		default:
			return expr, nil
		}
	}
}

func (p *Parser) parseArgs() ([]Expr, error) {
	if _, err := p.expect(TokenLParen); err != nil {
		return nil, err
	}
	args := make([]Expr, 0, 4)
	if !p.match(TokenRParen) {
		var err error
		args, err = p.parseExprList()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(TokenRParen); err != nil {
		return nil, err
	}
	return args, nil
}

func (p *Parser) parsePrimary() (Expr, error) {
	tok := p.current()
	switch tok.Type {
	case TokenName:
		p.advance()
		return &NameExpr{Name: tok.Literal}, nil
	case TokenNumber:
		p.advance()
		value, _ := strconv.ParseFloat(tok.Literal, 64)
		return &NumberExpr{Value: value}, nil
	case TokenString:
		p.advance()
		return &StringExpr{Value: tok.Literal}, nil
	case TokenTrue:
		p.advance()
		return &BoolExpr{Value: true}, nil
	case TokenFalse:
		p.advance()
		return &BoolExpr{Value: false}, nil
	case TokenNil:
		p.advance()
		return &NilExpr{}, nil
	case TokenEllipsis:
		p.advance()
		return &VarargExpr{}, nil
	case TokenFunction:
		p.advance()
		params, vararg, body, err := p.parseFunctionBody(false)
		if err != nil {
			return nil, err
		}
		return &FunctionExpr{Params: params, Vararg: vararg, Body: body}, nil
	case TokenLBrace:
		return p.parseTableExpr()
	case TokenLParen:
		p.advance()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokenRParen); err != nil {
			return nil, err
		}
		return expr, nil
	default:
		return nil, fmt.Errorf("unexpected token %q at offset %d", tok.Literal, tok.Offset)
	}
}

func (p *Parser) parseTableExpr() (Expr, error) {
	if _, err := p.expect(TokenLBrace); err != nil {
		return nil, err
	}
	fields := make([]TableField, 0, 4)
	for !p.match(TokenRBrace) {
		field, err := p.parseTableField()
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
		if p.match(TokenComma) || p.match(TokenSemi) {
			p.advance()
			if p.match(TokenRBrace) {
				break
			}
		}
	}
	if _, err := p.expect(TokenRBrace); err != nil {
		return nil, err
	}
	return &TableExpr{Fields: fields}, nil
}

func (p *Parser) parseTableField() (TableField, error) {
	if p.match(TokenLBracket) {
		p.advance()
		key, err := p.parseExpr()
		if err != nil {
			return TableField{}, err
		}
		if _, err := p.expect(TokenRBracket); err != nil {
			return TableField{}, err
		}
		if _, err := p.expect(TokenAssign); err != nil {
			return TableField{}, err
		}
		value, err := p.parseExpr()
		if err != nil {
			return TableField{}, err
		}
		return TableField{Kind: TableFieldExpr, Key: key, Value: value}, nil
	}
	if p.match(TokenName) && p.peek().Type == TokenAssign {
		name := p.current().Literal
		p.advance()
		p.advance()
		value, err := p.parseExpr()
		if err != nil {
			return TableField{}, err
		}
		return TableField{Kind: TableFieldNamed, Name: name, Value: value}, nil
	}
	value, err := p.parseExpr()
	if err != nil {
		return TableField{}, err
	}
	return TableField{Kind: TableFieldArray, Value: value}, nil
}

func (p *Parser) expect(kind TokenType) (Token, error) {
	if p.current().Type != kind {
		return Token{}, fmt.Errorf("expected %v at offset %d, got %v", kind, p.current().Offset, p.current().Type)
	}
	tok := p.current()
	p.advance()
	return tok, nil
}

func (p *Parser) current() Token {
	if p.index >= len(p.tokens) {
		return Token{Type: TokenEOF, Offset: len(p.tokens)}
	}
	return p.tokens[p.index]
}

func (p *Parser) peek() Token {
	if p.index+1 >= len(p.tokens) {
		return Token{Type: TokenEOF, Offset: p.current().Offset}
	}
	return p.tokens[p.index+1]
}

func (p *Parser) match(kind TokenType) bool {
	return p.current().Type == kind
}

func (p *Parser) advance() {
	if p.index < len(p.tokens) {
		p.index++
	}
}

func (p *Parser) isTerminator(terminators ...TokenType) bool {
	current := p.current().Type
	for _, term := range terminators {
		if current == term {
			return true
		}
	}
	return false
}

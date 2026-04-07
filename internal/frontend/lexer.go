package frontend

import (
	"fmt"
	"strconv"
	"unicode"
	"unicode/utf8"
)

type TokenType int

const (
	TokenEOF TokenType = iota
	TokenName
	TokenNumber
	TokenString
	TokenLocal
	TokenFunction
	TokenReturn
	TokenEnd
	TokenTrue
	TokenFalse
	TokenNil
	TokenIf
	TokenThen
	TokenElse
	TokenElseif
	TokenWhile
	TokenDo
	TokenRepeat
	TokenUntil
	TokenFor
	TokenIn
	TokenBreak
	TokenEllipsis
	TokenAnd
	TokenOr
	TokenNot
	TokenAssign
	TokenEqual
	TokenNotEqual
	TokenLess
	TokenLessEqual
	TokenGreater
	TokenGreaterEqual
	TokenComma
	TokenDot
	TokenConcat
	TokenColon
	TokenLParen
	TokenRParen
	TokenLBrace
	TokenRBrace
	TokenLBracket
	TokenRBracket
	TokenPlus
	TokenMinus
	TokenStar
	TokenSlash
	TokenPercent
	TokenCaret
	TokenHash
	TokenSemi
)

func (t TokenType) String() string {
	switch t {
	case TokenEOF:
		return "EOF"
	case TokenName:
		return "NAME"
	case TokenNumber:
		return "NUMBER"
	case TokenString:
		return "STRING"
	case TokenLocal:
		return "LOCAL"
	case TokenFunction:
		return "FUNCTION"
	case TokenReturn:
		return "RETURN"
	case TokenEnd:
		return "END"
	case TokenTrue:
		return "TRUE"
	case TokenFalse:
		return "FALSE"
	case TokenNil:
		return "NIL"
	case TokenIf:
		return "IF"
	case TokenThen:
		return "THEN"
	case TokenElse:
		return "ELSE"
	case TokenElseif:
		return "ELSEIF"
	case TokenWhile:
		return "WHILE"
	case TokenDo:
		return "DO"
	case TokenRepeat:
		return "REPEAT"
	case TokenUntil:
		return "UNTIL"
	case TokenFor:
		return "FOR"
	case TokenIn:
		return "IN"
	case TokenBreak:
		return "BREAK"
	case TokenEllipsis:
		return "ELLIPSIS"
	case TokenAnd:
		return "AND"
	case TokenOr:
		return "OR"
	case TokenNot:
		return "NOT"
	case TokenAssign:
		return "ASSIGN"
	case TokenEqual:
		return "EQ"
	case TokenNotEqual:
		return "NE"
	case TokenLess:
		return "LT"
	case TokenLessEqual:
		return "LE"
	case TokenGreater:
		return "GT"
	case TokenGreaterEqual:
		return "GE"
	case TokenComma:
		return "COMMA"
	case TokenDot:
		return "DOT"
	case TokenConcat:
		return "CONCAT"
	case TokenColon:
		return "COLON"
	case TokenLParen:
		return "LPAREN"
	case TokenRParen:
		return "RPAREN"
	case TokenLBrace:
		return "LBRACE"
	case TokenRBrace:
		return "RBRACE"
	case TokenLBracket:
		return "LBRACKET"
	case TokenRBracket:
		return "RBRACKET"
	case TokenPlus:
		return "PLUS"
	case TokenMinus:
		return "MINUS"
	case TokenStar:
		return "STAR"
	case TokenSlash:
		return "SLASH"
	case TokenPercent:
		return "PERCENT"
	case TokenCaret:
		return "CARET"
	case TokenHash:
		return "HASH"
	case TokenSemi:
		return "SEMI"
	default:
		return fmt.Sprintf("TOKEN_%d", t)
	}
}

type Token struct {
	Type    TokenType
	Literal string
	Offset  int
}

type Lexer struct {
	source string
	offset int
}

func Lex(source string) ([]Token, error) {
	lexer := &Lexer{source: source}
	tokens := make([]Token, 0, 64)
	for {
		tok, err := lexer.next()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		if tok.Type == TokenEOF {
			return tokens, nil
		}
	}
}

func (l *Lexer) next() (Token, error) {
	l.skipSpaceAndComments()
	if l.offset >= len(l.source) {
		return Token{Type: TokenEOF, Offset: l.offset}, nil
	}
	start := l.offset
	r, size := utf8.DecodeRuneInString(l.source[l.offset:])
	l.offset += size
	switch {
	case isIdentStart(r):
		for l.offset < len(l.source) {
			r, size = utf8.DecodeRuneInString(l.source[l.offset:])
			if !isIdentContinue(r) {
				break
			}
			l.offset += size
		}
		lit := l.source[start:l.offset]
		return Token{Type: keywordType(lit), Literal: lit, Offset: start}, nil
	case unicode.IsDigit(r):
		for l.offset < len(l.source) {
			r, size = utf8.DecodeRuneInString(l.source[l.offset:])
			if !unicode.IsDigit(r) && r != '.' {
				break
			}
			l.offset += size
		}
		lit := l.source[start:l.offset]
		if _, err := strconv.ParseFloat(lit, 64); err != nil {
			return Token{}, fmt.Errorf("invalid number %q", lit)
		}
		return Token{Type: TokenNumber, Literal: lit, Offset: start}, nil
	case r == '\'' || r == '"':
		quote := r
		buf := make([]rune, 0, 16)
		for l.offset < len(l.source) {
			r, size = utf8.DecodeRuneInString(l.source[l.offset:])
			l.offset += size
			if r == quote {
				return Token{Type: TokenString, Literal: string(buf), Offset: start}, nil
			}
			if r == '\\' {
				if l.offset >= len(l.source) {
					return Token{}, fmt.Errorf("unterminated string literal")
				}
				r, size = utf8.DecodeRuneInString(l.source[l.offset:])
				l.offset += size
				switch r {
				case 'n':
					buf = append(buf, '\n')
				case 't':
					buf = append(buf, '\t')
				case '\\', '\'', '"':
					buf = append(buf, r)
				default:
					return Token{}, fmt.Errorf("unsupported string escape \\%c", r)
				}
				continue
			}
			buf = append(buf, r)
		}
		return Token{}, fmt.Errorf("unterminated string literal")
	case r == '=':
		if l.match('=') {
			l.offset++
			return Token{Type: TokenEqual, Literal: "==", Offset: start}, nil
		}
		return Token{Type: TokenAssign, Literal: "=", Offset: start}, nil
	case r == '~':
		if l.match('=') {
			l.offset++
			return Token{Type: TokenNotEqual, Literal: "~=", Offset: start}, nil
		}
		return Token{}, fmt.Errorf("unexpected character %q", r)
	case r == '<':
		if l.match('=') {
			l.offset++
			return Token{Type: TokenLessEqual, Literal: "<=", Offset: start}, nil
		}
		return Token{Type: TokenLess, Literal: "<", Offset: start}, nil
	case r == '>':
		if l.match('=') {
			l.offset++
			return Token{Type: TokenGreaterEqual, Literal: ">=", Offset: start}, nil
		}
		return Token{Type: TokenGreater, Literal: ">", Offset: start}, nil
	case r == ',':
		return Token{Type: TokenComma, Literal: ",", Offset: start}, nil
	case r == '.':
		if l.offset+1 < len(l.source) && l.source[l.offset] == '.' && l.source[l.offset+1] == '.' {
			l.offset += 2
			return Token{Type: TokenEllipsis, Literal: "...", Offset: start}, nil
		}
		if l.match('.') {
			l.offset++
			return Token{Type: TokenConcat, Literal: "..", Offset: start}, nil
		}
		return Token{Type: TokenDot, Literal: ".", Offset: start}, nil
	case r == ':':
		return Token{Type: TokenColon, Literal: ":", Offset: start}, nil
	case r == '(':
		return Token{Type: TokenLParen, Literal: "(", Offset: start}, nil
	case r == ')':
		return Token{Type: TokenRParen, Literal: ")", Offset: start}, nil
	case r == '{':
		return Token{Type: TokenLBrace, Literal: "{", Offset: start}, nil
	case r == '}':
		return Token{Type: TokenRBrace, Literal: "}", Offset: start}, nil
	case r == '[':
		return Token{Type: TokenLBracket, Literal: "[", Offset: start}, nil
	case r == ']':
		return Token{Type: TokenRBracket, Literal: "]", Offset: start}, nil
	case r == '+':
		return Token{Type: TokenPlus, Literal: "+", Offset: start}, nil
	case r == '-':
		return Token{Type: TokenMinus, Literal: "-", Offset: start}, nil
	case r == '*':
		return Token{Type: TokenStar, Literal: "*", Offset: start}, nil
	case r == '/':
		return Token{Type: TokenSlash, Literal: "/", Offset: start}, nil
	case r == '%':
		return Token{Type: TokenPercent, Literal: "%", Offset: start}, nil
	case r == '^':
		return Token{Type: TokenCaret, Literal: "^", Offset: start}, nil
	case r == '#':
		return Token{Type: TokenHash, Literal: "#", Offset: start}, nil
	case r == ';':
		return Token{Type: TokenSemi, Literal: ";", Offset: start}, nil
	default:
		return Token{}, fmt.Errorf("unexpected character %q", r)
	}
}

func (l *Lexer) skipSpaceAndComments() {
	for l.offset < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.source[l.offset:])
		if unicode.IsSpace(r) {
			l.offset += size
			continue
		}
		if r == '-' && l.offset+1 < len(l.source) && l.source[l.offset+1] == '-' {
			l.offset += 2
			for l.offset < len(l.source) {
				r, size = utf8.DecodeRuneInString(l.source[l.offset:])
				l.offset += size
				if r == '\n' {
					break
				}
			}
			continue
		}
		break
	}
}

func (l *Lexer) match(expected byte) bool {
	return l.offset < len(l.source) && l.source[l.offset] == expected
}

func keywordType(lit string) TokenType {
	switch lit {
	case "local":
		return TokenLocal
	case "function":
		return TokenFunction
	case "return":
		return TokenReturn
	case "end":
		return TokenEnd
	case "true":
		return TokenTrue
	case "false":
		return TokenFalse
	case "nil":
		return TokenNil
	case "if":
		return TokenIf
	case "then":
		return TokenThen
	case "else":
		return TokenElse
	case "elseif":
		return TokenElseif
	case "while":
		return TokenWhile
	case "do":
		return TokenDo
	case "repeat":
		return TokenRepeat
	case "until":
		return TokenUntil
	case "for":
		return TokenFor
	case "in":
		return TokenIn
	case "break":
		return TokenBreak
	case "and":
		return TokenAnd
	case "or":
		return TokenOr
	case "not":
		return TokenNot
	default:
		return TokenName
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentContinue(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r)
}

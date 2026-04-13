package lexer

import "fmt"

// TokenKind is the stable token surface shared by the lexer and parser.
// Reserved words intentionally keep Lua 5.1's declaration order from llex.h.
type TokenKind uint16

const (
	TokenInvalid TokenKind = iota

	TokenAnd
	TokenBreak
	TokenDo
	TokenElse
	TokenElseIf
	TokenEnd
	TokenFalse
	TokenFor
	TokenFunction
	TokenIf
	TokenIn
	TokenLocal
	TokenNil
	TokenNot
	TokenOr
	TokenRepeat
	TokenReturn
	TokenThen
	TokenTrue
	TokenUntil
	TokenWhile

	TokenConcat
	TokenDots
	TokenEqual
	TokenGreaterEqual
	TokenLessEqual
	TokenNotEqual
	TokenNumber
	TokenName
	TokenString
	TokenEOF

	TokenAssign
	TokenLessThan
	TokenGreaterThan
	TokenLeftParen
	TokenRightParen
	TokenLeftBrace
	TokenRightBrace
	TokenLeftBracket
	TokenRightBracket
	TokenSemicolon
	TokenColon
	TokenComma
	TokenDot
	TokenPlus
	TokenMinus
	TokenStar
	TokenSlash
	TokenPercent
	TokenCaret
	TokenHash
)

const (
	firstReservedWord = TokenAnd
	lastReservedWord  = TokenWhile
)

var tokenNames = [...]string{
	TokenInvalid:      "<invalid>",
	TokenAnd:          "and",
	TokenBreak:        "break",
	TokenDo:           "do",
	TokenElse:         "else",
	TokenElseIf:       "elseif",
	TokenEnd:          "end",
	TokenFalse:        "false",
	TokenFor:          "for",
	TokenFunction:     "function",
	TokenIf:           "if",
	TokenIn:           "in",
	TokenLocal:        "local",
	TokenNil:          "nil",
	TokenNot:          "not",
	TokenOr:           "or",
	TokenRepeat:       "repeat",
	TokenReturn:       "return",
	TokenThen:         "then",
	TokenTrue:         "true",
	TokenUntil:        "until",
	TokenWhile:        "while",
	TokenConcat:       "..",
	TokenDots:         "...",
	TokenEqual:        "==",
	TokenGreaterEqual: ">=",
	TokenLessEqual:    "<=",
	TokenNotEqual:     "~=",
	TokenNumber:       "<number>",
	TokenName:         "<name>",
	TokenString:       "<string>",
	TokenEOF:          "<eof>",
	TokenAssign:       "=",
	TokenLessThan:     "<",
	TokenGreaterThan:  ">",
	TokenLeftParen:    "(",
	TokenRightParen:   ")",
	TokenLeftBrace:    "{",
	TokenRightBrace:   "}",
	TokenLeftBracket:  "[",
	TokenRightBracket: "]",
	TokenSemicolon:    ";",
	TokenColon:        ":",
	TokenComma:        ",",
	TokenDot:          ".",
	TokenPlus:         "+",
	TokenMinus:        "-",
	TokenStar:         "*",
	TokenSlash:        "/",
	TokenPercent:      "%",
	TokenCaret:        "^",
	TokenHash:         "#",
}

var keywordTable = map[string]TokenKind{
	"and":      TokenAnd,
	"break":    TokenBreak,
	"do":       TokenDo,
	"else":     TokenElse,
	"elseif":   TokenElseIf,
	"end":      TokenEnd,
	"false":    TokenFalse,
	"for":      TokenFor,
	"function": TokenFunction,
	"if":       TokenIf,
	"in":       TokenIn,
	"local":    TokenLocal,
	"nil":      TokenNil,
	"not":      TokenNot,
	"or":       TokenOr,
	"repeat":   TokenRepeat,
	"return":   TokenReturn,
	"then":     TokenThen,
	"true":     TokenTrue,
	"until":    TokenUntil,
	"while":    TokenWhile,
}

func (kind TokenKind) String() string {
	if int(kind) < len(tokenNames) && tokenNames[kind] != "" {
		return tokenNames[kind]
	}
	return fmt.Sprintf("TokenKind(%d)", kind)
}

// IsReservedWord reports whether the token is one of Lua 5.1's reserved words.
func (kind TokenKind) IsReservedWord() bool {
	return kind >= firstReservedWord && kind <= lastReservedWord
}

// ReservedWordsInLua51Order returns the reserved words in the exact order used
// by Lua 5.1's llex.h enum.
func ReservedWordsInLua51Order() []TokenKind {
	words := make([]TokenKind, 0, int(lastReservedWord-firstReservedWord)+1)
	for kind := firstReservedWord; kind <= lastReservedWord; kind++ {
		words = append(words, kind)
	}
	return words
}

// LookupKeyword resolves a source identifier to a reserved-word token kind.
func LookupKeyword(text string) (TokenKind, bool) {
	kind, ok := keywordTable[text]
	return kind, ok
}

// Token is the stable lexer output consumed by the parser.
type Token struct {
	Kind        TokenKind
	Span        Span
	Lexeme      string
	NumberValue float64
	StringValue string
}

// IsValid reports whether the token was populated with a concrete kind.
func (token Token) IsValid() bool {
	return token.Kind != TokenInvalid
}

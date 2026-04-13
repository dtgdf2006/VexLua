package lexer

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

var sourceNumberPattern = regexp.MustCompile(`^(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$`)

// Scanner is the Phase 1 explicit token iterator for Lua 5.1 source text.
// It mirrors luaX_next/luaX_lookahead with a Go-facing API.
type Scanner struct {
	name         string
	src          []byte
	offset       int
	line         int
	column       int
	hasLookahead bool
	lookahead    Token
}

// NewScanner constructs a scanner for one source buffer.
func NewScanner(name string, src []byte) *Scanner {
	return &Scanner{name: name, src: src, line: 1, column: 1}
}

// Name returns the logical source name attached to the scanner.
func (scanner *Scanner) Name() string {
	if scanner == nil {
		return ""
	}
	return scanner.name
}

// Next returns the next token, consuming it from the token stream.
func (scanner *Scanner) Next() (Token, error) {
	if scanner == nil {
		return Token{}, Errorf(PhaseLex, Span{}, "scanner is nil")
	}
	if scanner.hasLookahead {
		token := scanner.lookahead
		scanner.hasLookahead = false
		scanner.lookahead = Token{}
		return token, nil
	}
	return scanner.scanToken()
}

// Lookahead returns the next token without consuming it.
func (scanner *Scanner) Lookahead() (Token, error) {
	if scanner == nil {
		return Token{}, Errorf(PhaseLex, Span{}, "scanner is nil")
	}
	if scanner.hasLookahead {
		return scanner.lookahead, nil
	}
	token, err := scanner.scanToken()
	if err != nil {
		return Token{}, err
	}
	scanner.lookahead = token
	scanner.hasLookahead = true
	return token, nil
}

// ScanAll tokenizes the remaining source into a full token slice, including EOF.
func (scanner *Scanner) ScanAll() ([]Token, error) {
	if scanner == nil {
		return nil, Errorf(PhaseLex, Span{}, "scanner is nil")
	}
	tokens := make([]Token, 0, 32)
	for {
		token, err := scanner.Next()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
		if token.Kind == TokenEOF {
			return tokens, nil
		}
	}
}

func (scanner *Scanner) scanToken() (Token, error) {
	if err := scanner.skipTrivia(); err != nil {
		return Token{}, err
	}
	start := scanner.position()
	startOffset := scanner.offset
	current, ok := scanner.peekByte(0)
	if !ok {
		return Token{Kind: TokenEOF, Span: Span{Start: start, End: start}}, nil
	}

	switch current {
	case '[':
		sep, matched, invalid := scanner.longDelimiter('[')
		if matched {
			return scanner.readLongString(start, startOffset, sep)
		}
		if invalid {
			return Token{}, scanner.lexError(Span{Start: start, End: scanner.position()}, "invalid long string delimiter")
		}
		scanner.consumeByte()
		return scanner.simpleToken(TokenLeftBracket, start, startOffset), nil
	case ']':
		scanner.consumeByte()
		return scanner.simpleToken(TokenRightBracket, start, startOffset), nil
	case '=':
		scanner.consumeByte()
		if scanner.matchByte('=') {
			return scanner.compoundToken(TokenEqual, start, startOffset), nil
		}
		return scanner.simpleToken(TokenAssign, start, startOffset), nil
	case '<':
		scanner.consumeByte()
		if scanner.matchByte('=') {
			return scanner.compoundToken(TokenLessEqual, start, startOffset), nil
		}
		return scanner.simpleToken(TokenLessThan, start, startOffset), nil
	case '>':
		scanner.consumeByte()
		if scanner.matchByte('=') {
			return scanner.compoundToken(TokenGreaterEqual, start, startOffset), nil
		}
		return scanner.simpleToken(TokenGreaterThan, start, startOffset), nil
	case '~':
		scanner.consumeByte()
		if scanner.matchByte('=') {
			return scanner.compoundToken(TokenNotEqual, start, startOffset), nil
		}
		return Token{}, scanner.lexError(Span{Start: start, End: scanner.position()}, "unexpected character '~'")
	case '\'', '"':
		return scanner.readQuotedString(start, startOffset, current)
	case '.':
		scanner.consumeByte()
		if scanner.matchByte('.') {
			if scanner.matchByte('.') {
				return scanner.compoundToken(TokenDots, start, startOffset), nil
			}
			return scanner.compoundToken(TokenConcat, start, startOffset), nil
		}
		if next, ok := scanner.peekByte(0); ok && isDigit(next) {
			return scanner.readNumber(start, startOffset, true)
		}
		return scanner.simpleToken(TokenDot, start, startOffset), nil
	case '(':
		scanner.consumeByte()
		return scanner.simpleToken(TokenLeftParen, start, startOffset), nil
	case ')':
		scanner.consumeByte()
		return scanner.simpleToken(TokenRightParen, start, startOffset), nil
	case '{':
		scanner.consumeByte()
		return scanner.simpleToken(TokenLeftBrace, start, startOffset), nil
	case '}':
		scanner.consumeByte()
		return scanner.simpleToken(TokenRightBrace, start, startOffset), nil
	case ';':
		scanner.consumeByte()
		return scanner.simpleToken(TokenSemicolon, start, startOffset), nil
	case ':':
		scanner.consumeByte()
		return scanner.simpleToken(TokenColon, start, startOffset), nil
	case ',':
		scanner.consumeByte()
		return scanner.simpleToken(TokenComma, start, startOffset), nil
	case '+':
		scanner.consumeByte()
		return scanner.simpleToken(TokenPlus, start, startOffset), nil
	case '-':
		scanner.consumeByte()
		return scanner.simpleToken(TokenMinus, start, startOffset), nil
	case '*':
		scanner.consumeByte()
		return scanner.simpleToken(TokenStar, start, startOffset), nil
	case '/':
		scanner.consumeByte()
		return scanner.simpleToken(TokenSlash, start, startOffset), nil
	case '%':
		scanner.consumeByte()
		return scanner.simpleToken(TokenPercent, start, startOffset), nil
	case '^':
		scanner.consumeByte()
		return scanner.simpleToken(TokenCaret, start, startOffset), nil
	case '#':
		scanner.consumeByte()
		return scanner.simpleToken(TokenHash, start, startOffset), nil
	default:
		if isDigit(current) {
			return scanner.readNumber(start, startOffset, false)
		}
		if isIdentStart(current) {
			return scanner.readName(start, startOffset), nil
		}
		scanner.consumeByte()
		return Token{}, scanner.lexError(Span{Start: start, End: scanner.position()}, fmt.Sprintf("unexpected character %q", current))
	}
}

func (scanner *Scanner) skipTrivia() error {
	for {
		current, ok := scanner.peekByte(0)
		if !ok {
			return nil
		}
		switch current {
		case ' ', '\t', '\v', '\f':
			scanner.consumeByte()
			continue
		case '\n', '\r':
			scanner.consumeNewline()
			continue
		case '-':
			next, ok := scanner.peekByte(1)
			if !ok || next != '-' {
				return nil
			}
			scanner.consumeByte()
			scanner.consumeByte()
			if current, ok = scanner.peekByte(0); ok && current == '[' {
				sep, matched, _ := scanner.longDelimiter('[')
				if matched {
					if err := scanner.skipLongComment(sep); err != nil {
						return err
					}
					continue
				}
			}
			for {
				current, ok = scanner.peekByte(0)
				if !ok || current == '\n' || current == '\r' {
					break
				}
				scanner.consumeByte()
			}
			continue
		default:
			return nil
		}
	}
}

func (scanner *Scanner) readName(start Position, startOffset int) Token {
	for {
		current, ok := scanner.peekByte(0)
		if !ok || !isIdentContinue(current) {
			break
		}
		scanner.consumeByte()
	}
	lexeme := string(scanner.src[startOffset:scanner.offset])
	kind := TokenName
	if keyword, ok := LookupKeyword(lexeme); ok {
		kind = keyword
	}
	return Token{Kind: kind, Span: Span{Start: start, End: scanner.position()}, Lexeme: lexeme}
}

func (scanner *Scanner) readNumber(start Position, startOffset int, startedWithDot bool) (Token, error) {
	if !startedWithDot {
		scanner.consumeByte()
	}
	for {
		current, ok := scanner.peekByte(0)
		if !ok || (!isDigit(current) && current != '.') {
			break
		}
		scanner.consumeByte()
	}
	if current, ok := scanner.peekByte(0); ok && (current == 'e' || current == 'E') {
		scanner.consumeByte()
		if sign, ok := scanner.peekByte(0); ok && (sign == '+' || sign == '-') {
			scanner.consumeByte()
		}
	}
	for {
		current, ok := scanner.peekByte(0)
		if !ok || !isAlphaNumeric(current) && current != '_' {
			break
		}
		scanner.consumeByte()
	}
	lexeme := string(scanner.src[startOffset:scanner.offset])
	value, ok := parseSourceNumber(lexeme)
	if !ok {
		return Token{}, scanner.lexError(Span{Start: start, End: scanner.position()}, "malformed number")
	}
	return Token{Kind: TokenNumber, Span: Span{Start: start, End: scanner.position()}, Lexeme: lexeme, NumberValue: value}, nil
}

func (scanner *Scanner) readQuotedString(start Position, startOffset int, delimiter byte) (Token, error) {
	scanner.consumeByte()
	value := make([]byte, 0, 32)
	for {
		current, ok := scanner.peekByte(0)
		if !ok {
			return Token{}, scanner.lexError(Span{Start: start, End: scanner.position()}, "unfinished string")
		}
		if current == delimiter {
			scanner.consumeByte()
			return Token{
				Kind:        TokenString,
				Span:        Span{Start: start, End: scanner.position()},
				Lexeme:      string(scanner.src[startOffset:scanner.offset]),
				StringValue: string(value),
			}, nil
		}
		if current == '\n' || current == '\r' {
			return Token{}, scanner.lexError(Span{Start: start, End: scanner.position()}, "unfinished string")
		}
		if current != '\\' {
			value = append(value, scanner.consumeByte())
			continue
		}
		scanner.consumeByte()
		escaped, ok := scanner.peekByte(0)
		if !ok {
			return Token{}, scanner.lexError(Span{Start: start, End: scanner.position()}, "unfinished string")
		}
		switch escaped {
		case 'a':
			value = append(value, '\a')
			scanner.consumeByte()
		case 'b':
			value = append(value, '\b')
			scanner.consumeByte()
		case 'f':
			value = append(value, '\f')
			scanner.consumeByte()
		case 'n':
			value = append(value, '\n')
			scanner.consumeByte()
		case 'r':
			value = append(value, '\r')
			scanner.consumeByte()
		case 't':
			value = append(value, '\t')
			scanner.consumeByte()
		case 'v':
			value = append(value, '\v')
			scanner.consumeByte()
		case '\n', '\r':
			value = append(value, '\n')
			scanner.consumeNewline()
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			decoded := 0
			for digits := 0; digits < 3; digits++ {
				current, ok = scanner.peekByte(0)
				if !ok || !isDigit(current) {
					break
				}
				decoded = decoded*10 + int(current-'0')
				scanner.consumeByte()
			}
			if decoded > 255 {
				return Token{}, scanner.lexError(Span{Start: start, End: scanner.position()}, "escape sequence too large")
			}
			value = append(value, byte(decoded))
		default:
			value = append(value, scanner.consumeByte())
		}
	}
}

func (scanner *Scanner) readLongString(start Position, startOffset int, sep int) (Token, error) {
	value, endOffset, err := scanner.readLongBody(sep, true)
	if err != nil {
		return Token{}, scanner.lexError(Span{Start: start, End: scanner.position()}, err.Error())
	}
	return Token{
		Kind:        TokenString,
		Span:        Span{Start: start, End: scanner.position()},
		Lexeme:      string(scanner.src[startOffset:endOffset]),
		StringValue: string(value),
	}, nil
}

func (scanner *Scanner) skipLongComment(sep int) error {
	_, _, err := scanner.readLongBody(sep, false)
	if err != nil {
		return scanner.lexError(Span{Start: scanner.position(), End: scanner.position()}, err.Error())
	}
	return nil
}

func (scanner *Scanner) readLongBody(sep int, keep bool) ([]byte, int, error) {
	openingWidth := 2 + sep
	scanner.consumeN(openingWidth)
	if current, ok := scanner.peekByte(0); ok && (current == '\n' || current == '\r') {
		scanner.consumeNewline()
	}
	value := make([]byte, 0, 32)
	for {
		current, ok := scanner.peekByte(0)
		if !ok {
			if keep {
				return nil, scanner.offset, fmt.Errorf("unfinished long string")
			}
			return nil, scanner.offset, fmt.Errorf("unfinished long comment")
		}
		if current == ']' {
			closeSep, matched, _ := scanner.longDelimiter(']')
			if matched && closeSep == sep {
				scanner.consumeN(2 + sep)
				return value, scanner.offset, nil
			}
		}
		if current == '\n' || current == '\r' {
			if keep {
				value = append(value, '\n')
			}
			scanner.consumeNewline()
			continue
		}
		if keep {
			value = append(value, scanner.consumeByte())
		} else {
			scanner.consumeByte()
		}
	}
}

func (scanner *Scanner) simpleToken(kind TokenKind, start Position, startOffset int) Token {
	return Token{Kind: kind, Span: Span{Start: start, End: scanner.position()}, Lexeme: string(scanner.src[startOffset:scanner.offset])}
}

func (scanner *Scanner) compoundToken(kind TokenKind, start Position, startOffset int) Token {
	return Token{Kind: kind, Span: Span{Start: start, End: scanner.position()}, Lexeme: string(scanner.src[startOffset:scanner.offset])}
}

func (scanner *Scanner) lexError(span Span, message string) error {
	if span.End.Offset < span.Start.Offset {
		span.End = span.Start
	}
	return Errorf(PhaseLex, span, message)
}

func (scanner *Scanner) position() Position {
	if scanner == nil {
		return Position{}
	}
	return Position{Offset: scanner.offset, Line: scanner.line, Column: scanner.column}
}

func (scanner *Scanner) peekByte(delta int) (byte, bool) {
	if scanner == nil {
		return 0, false
	}
	index := scanner.offset + delta
	if index < 0 || index >= len(scanner.src) {
		return 0, false
	}
	return scanner.src[index], true
}

func (scanner *Scanner) consumeByte() byte {
	current := scanner.src[scanner.offset]
	scanner.offset++
	scanner.column++
	return current
}

func (scanner *Scanner) consumeN(count int) {
	for index := 0; index < count; index++ {
		scanner.consumeByte()
	}
}

func (scanner *Scanner) consumeNewline() {
	current := scanner.src[scanner.offset]
	scanner.offset++
	if scanner.offset < len(scanner.src) {
		next := scanner.src[scanner.offset]
		if (current == '\n' && next == '\r') || (current == '\r' && next == '\n') {
			scanner.offset++
		}
	}
	scanner.line++
	scanner.column = 1
}

func (scanner *Scanner) matchByte(want byte) bool {
	current, ok := scanner.peekByte(0)
	if !ok || current != want {
		return false
	}
	scanner.consumeByte()
	return true
}

func (scanner *Scanner) longDelimiter(bracket byte) (int, bool, bool) {
	current, ok := scanner.peekByte(0)
	if !ok || current != bracket {
		return 0, false, false
	}
	index := scanner.offset + 1
	count := 0
	for index < len(scanner.src) && scanner.src[index] == '=' {
		count++
		index++
	}
	if index < len(scanner.src) && scanner.src[index] == bracket {
		return count, true, false
	}
	if count > 0 {
		return count, false, true
	}
	return 0, false, false
}

func parseSourceNumber(text string) (float64, bool) {
	if !sourceNumberPattern.MatchString(text) {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		var numberError *strconv.NumError
		if !errors.As(err, &numberError) || numberError.Err != strconv.ErrRange {
			return 0, false
		}
	}
	return value, true
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isAlphaNumeric(value byte) bool {
	return isDigit(value) || isASCIIAlpha(value) || value >= 0x80
}

func isIdentStart(value byte) bool {
	return value == '_' || isASCIIAlpha(value) || value >= 0x80
}

func isIdentContinue(value byte) bool {
	return isIdentStart(value) || isDigit(value)
}

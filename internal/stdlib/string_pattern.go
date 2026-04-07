package stdlib

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	rt "vexlua/internal/runtime"
)

type stringPattern struct {
	plain    bool
	literal  string
	re       *regexp.Regexp
	captures int
	lua      *luaPatternProgram
}

type stringPatternMatch struct {
	start    int
	end      int
	captures []stringPatternCapture
}

type stringPatternCapture struct {
	text       string
	position   int
	isPosition bool
}

type luaPatternProgram struct {
	pattern string
	anchor  bool
}

type luaPatternCapture struct {
	start      int
	end        int
	position   int
	isPosition bool
}

func compileStringPattern(pattern string, plain bool) (*stringPattern, error) {
	if plain || !hasLuaPatternMagic(pattern) {
		return &stringPattern{plain: true, literal: pattern}, nil
	}
	if !needsLuaPatternVM(pattern) {
		re, captures, err := compileLuaPattern(pattern)
		if err == nil {
			return &stringPattern{re: re, captures: captures}, nil
		}
	}
	program, err := compileLuaSearchPattern(pattern)
	if err != nil {
		return nil, err
	}
	return &stringPattern{lua: program}, nil
}

func (pattern *stringPattern) find(subject string, start int) (*stringPatternMatch, error) {
	if start < 0 || start > len(subject) {
		return nil, nil
	}
	if pattern.plain {
		idx := strings.Index(subject[start:], pattern.literal)
		if idx < 0 {
			return nil, nil
		}
		matchStart := start + idx
		return &stringPatternMatch{start: matchStart, end: matchStart + len(pattern.literal)}, nil
	}
	if pattern.lua != nil {
		return pattern.lua.find(subject, start)
	}
	indices := pattern.re.FindStringSubmatchIndex(subject[start:])
	if indices == nil {
		return nil, nil
	}
	adjusted := make([]int, len(indices))
	for i, value := range indices {
		if value < 0 {
			adjusted[i] = value
			continue
		}
		adjusted[i] = start + value
	}
	return buildRegexStringPatternMatch(subject, adjusted, pattern.captures), nil
}

func buildRegexStringPatternMatch(subject string, indices []int, captures int) *stringPatternMatch {
	match := &stringPatternMatch{start: indices[0], end: indices[1]}
	if captures == 0 {
		return match
	}
	match.captures = make([]stringPatternCapture, 0, captures)
	for i := 0; i < captures; i++ {
		captureStart := indices[2+i*2]
		captureEnd := indices[3+i*2]
		if captureStart < 0 {
			match.captures = append(match.captures, stringPatternCapture{text: ""})
			continue
		}
		match.captures = append(match.captures, stringPatternCapture{text: subject[captureStart:captureEnd]})
	}
	return match
}

func stringCaptureToValue(runtime *rt.Runtime, capture stringPatternCapture) rt.Value {
	if capture.isPosition {
		return rt.NumberValue(float64(capture.position + 1))
	}
	return runtime.StringValue(capture.text)
}

func stringCaptureToString(capture stringPatternCapture) string {
	if capture.isPosition {
		return strconv.Itoa(capture.position + 1)
	}
	return capture.text
}

func stringMatchResults(runtime *rt.Runtime, full string, captures []stringPatternCapture) []rt.Value {
	if len(captures) == 0 {
		return []rt.Value{runtime.StringValue(full)}
	}
	results := make([]rt.Value, 0, len(captures))
	for _, capture := range captures {
		results = append(results, stringCaptureToValue(runtime, capture))
	}
	return results
}

func hasLuaPatternMagic(pattern string) bool {
	return strings.IndexAny(pattern, "^$()%.[]*+-?") >= 0
}

func needsLuaPatternVM(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '(':
			if i+1 < len(pattern) && pattern[i+1] == ')' {
				return true
			}
		case '%':
			if i+1 >= len(pattern) {
				break
			}
			next := pattern[i+1]
			if next == 'b' || next == 'f' || (next >= '1' && next <= '9') {
				return true
			}
			i++
		}
	}
	return false
}

func compileLuaSearchPattern(pattern string) (*luaPatternProgram, error) {
	program := &luaPatternProgram{pattern: pattern}
	if len(program.pattern) > 0 && program.pattern[0] == '^' {
		program.anchor = true
		program.pattern = program.pattern[1:]
	}
	if err := validateLuaPattern(program.pattern); err != nil {
		return nil, err
	}
	return program, nil
}

func validateLuaPattern(pattern string) error {
	depth := 0
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '(':
			if i+1 < len(pattern) && pattern[i+1] == ')' {
				i += 2
				continue
			}
			depth++
			i++
		case ')':
			if depth == 0 {
				return fmt.Errorf("invalid pattern capture")
			}
			depth--
			i++
		default:
			end, err := luaPatternItemEnd(pattern, i)
			if err != nil {
				return err
			}
			i = end
			if i < len(pattern) && isLuaPatternQuantifier(pattern[i]) {
				i++
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("unfinished capture")
	}
	return nil
}

func isLuaPatternQuantifier(ch byte) bool {
	return ch == '?' || ch == '*' || ch == '+' || ch == '-'
}

func luaPatternItemEnd(pattern string, start int) (int, error) {
	if start >= len(pattern) {
		return start, nil
	}
	switch pattern[start] {
	case '%':
		if start+1 >= len(pattern) {
			return 0, fmt.Errorf("unterminated string pattern escape")
		}
		switch pattern[start+1] {
		case 'b':
			if start+3 >= len(pattern) {
				return 0, fmt.Errorf("malformed pattern %%b")
			}
			return start + 4, nil
		case 'f':
			if start+2 >= len(pattern) || pattern[start+2] != '[' {
				return 0, fmt.Errorf("malformed pattern %%f")
			}
			return luaPatternClassEnd(pattern, start+2)
		default:
			return start + 2, nil
		}
	case '[':
		return luaPatternClassEnd(pattern, start)
	default:
		return start + 1, nil
	}
}

func luaPatternClassEnd(pattern string, start int) (int, error) {
	i := start + 1
	if i < len(pattern) && pattern[i] == '^' {
		i++
	}
	if i < len(pattern) && pattern[i] == ']' {
		i++
	}
	for ; i < len(pattern); i++ {
		if pattern[i] == '%' {
			i++
			continue
		}
		if pattern[i] == ']' {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("malformed pattern class")
}

func (program *luaPatternProgram) find(subject string, start int) (*stringPatternMatch, error) {
	if start < 0 || start > len(subject) {
		return nil, nil
	}
	if program.anchor {
		if start != 0 {
			return nil, nil
		}
		return program.matchFrom(subject, 0)
	}
	for pos := start; pos <= len(subject); pos++ {
		match, err := program.matchFrom(subject, pos)
		if err != nil {
			return nil, err
		}
		if match != nil {
			return match, nil
		}
	}
	return nil, nil
}

func (program *luaPatternProgram) matchFrom(subject string, start int) (*stringPatternMatch, error) {
	end, captures, ok, err := program.matchHere(subject, start, 0, nil)
	if err != nil || !ok {
		return nil, err
	}
	match := &stringPatternMatch{start: start, end: end, captures: make([]stringPatternCapture, 0, len(captures))}
	for _, capture := range captures {
		if capture.isPosition {
			match.captures = append(match.captures, stringPatternCapture{position: capture.position, isPosition: true})
			continue
		}
		match.captures = append(match.captures, stringPatternCapture{text: subject[capture.start:capture.end]})
	}
	return match, nil
}

func (program *luaPatternProgram) matchHere(subject string, subjectPos int, patternPos int, captures []luaPatternCapture) (int, []luaPatternCapture, bool, error) {
	pattern := program.pattern
	if patternPos == len(pattern) {
		return subjectPos, captures, true, nil
	}
	if pattern[patternPos] == '$' && patternPos+1 == len(pattern) {
		if subjectPos == len(subject) {
			return subjectPos, captures, true, nil
		}
		return 0, nil, false, nil
	}
	switch pattern[patternPos] {
	case '(':
		if patternPos+1 < len(pattern) && pattern[patternPos+1] == ')' {
			copied := cloneLuaPatternCaptures(captures)
			copied = append(copied, luaPatternCapture{position: subjectPos, isPosition: true})
			return program.matchHere(subject, subjectPos, patternPos+2, copied)
		}
		copied := cloneLuaPatternCaptures(captures)
		copied = append(copied, luaPatternCapture{start: subjectPos, end: -1})
		return program.matchHere(subject, subjectPos, patternPos+1, copied)
	case ')':
		index := lastOpenLuaPatternCapture(captures)
		if index < 0 {
			return 0, nil, false, fmt.Errorf("invalid pattern capture")
		}
		copied := cloneLuaPatternCaptures(captures)
		copied[index].end = subjectPos
		return program.matchHere(subject, subjectPos, patternPos+1, copied)
	}
	itemEnd, err := luaPatternItemEnd(pattern, patternPos)
	if err != nil {
		return 0, nil, false, err
	}
	nextPatternPos := itemEnd
	quantifier := byte(0)
	if nextPatternPos < len(pattern) && isLuaPatternQuantifier(pattern[nextPatternPos]) {
		quantifier = pattern[nextPatternPos]
		nextPatternPos++
	}
	switch quantifier {
	case 0:
		nextPos, ok, err := matchLuaPatternItem(subject, subjectPos, pattern, patternPos, itemEnd, captures)
		if err != nil || !ok {
			return 0, nil, false, err
		}
		return program.matchHere(subject, nextPos, nextPatternPos, captures)
	case '?':
		nextPos, ok, err := matchLuaPatternItem(subject, subjectPos, pattern, patternPos, itemEnd, captures)
		if err != nil {
			return 0, nil, false, err
		}
		if ok {
			if end, nextCaptures, matched, err := program.matchHere(subject, nextPos, nextPatternPos, captures); err != nil {
				return 0, nil, false, err
			} else if matched {
				return end, nextCaptures, true, nil
			}
		}
		return program.matchHere(subject, subjectPos, nextPatternPos, captures)
	case '*', '+', '-':
		positions, err := repeatLuaPatternItem(subject, subjectPos, pattern, patternPos, itemEnd, captures)
		if err != nil {
			return 0, nil, false, err
		}
		startIndex := 0
		step := 1
		if quantifier == '+' {
			if len(positions) < 2 {
				return 0, nil, false, nil
			}
			startIndex = len(positions) - 1
			step = -1
		} else if quantifier == '*' {
			startIndex = len(positions) - 1
			step = -1
		}
		for index := startIndex; index >= 0 && index < len(positions); index += step {
			if quantifier == '+' && index == 0 {
				break
			}
			end, nextCaptures, matched, err := program.matchHere(subject, positions[index], nextPatternPos, captures)
			if err != nil {
				return 0, nil, false, err
			}
			if matched {
				return end, nextCaptures, true, nil
			}
			if quantifier == '-' && index == len(positions)-1 {
				break
			}
		}
		return 0, nil, false, nil
	default:
		return 0, nil, false, fmt.Errorf("unsupported pattern quantifier %q", quantifier)
	}
}

func repeatLuaPatternItem(subject string, subjectPos int, pattern string, patternPos int, itemEnd int, captures []luaPatternCapture) ([]int, error) {
	positions := []int{subjectPos}
	current := subjectPos
	for {
		next, ok, err := matchLuaPatternItem(subject, current, pattern, patternPos, itemEnd, captures)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		positions = append(positions, next)
		if next == current {
			break
		}
		current = next
	}
	return positions, nil
}

func matchLuaPatternItem(subject string, subjectPos int, pattern string, patternPos int, itemEnd int, captures []luaPatternCapture) (int, bool, error) {
	if patternPos >= len(pattern) {
		return 0, false, nil
	}
	switch pattern[patternPos] {
	case '.':
		if subjectPos < len(subject) {
			return subjectPos + 1, true, nil
		}
		return 0, false, nil
	case '[':
		if subjectPos >= len(subject) {
			return 0, false, nil
		}
		matched, err := matchLuaBracketClass(subject[subjectPos], pattern, patternPos, itemEnd)
		if err != nil || !matched {
			return 0, false, err
		}
		return subjectPos + 1, true, nil
	case '%':
		next := pattern[patternPos+1]
		switch {
		case next == 'b':
			nextPos, ok := matchLuaBalanced(subject, subjectPos, pattern[patternPos+2], pattern[patternPos+3])
			return nextPos, ok, nil
		case next == 'f':
			prev := byte(0)
			if subjectPos > 0 {
				prev = subject[subjectPos-1]
			}
			curr := byte(0)
			if subjectPos < len(subject) {
				curr = subject[subjectPos]
			}
			classEnd, err := luaPatternClassEnd(pattern, patternPos+2)
			if err != nil {
				return 0, false, err
			}
			prevMatched, err := matchLuaBracketClass(prev, pattern, patternPos+2, classEnd)
			if err != nil {
				return 0, false, err
			}
			currMatched, err := matchLuaBracketClass(curr, pattern, patternPos+2, classEnd)
			if err != nil {
				return 0, false, err
			}
			if !prevMatched && currMatched {
				return subjectPos, true, nil
			}
			return 0, false, nil
		case next >= '1' && next <= '9':
			index := int(next - '1')
			if index >= len(captures) {
				return 0, false, fmt.Errorf("invalid pattern capture")
			}
			capture := captures[index]
			if capture.isPosition || capture.end < 0 {
				return 0, false, fmt.Errorf("invalid pattern capture")
			}
			text := subject[capture.start:capture.end]
			if len(subject)-subjectPos >= len(text) && subject[subjectPos:subjectPos+len(text)] == text {
				return subjectPos + len(text), true, nil
			}
			return 0, false, nil
		default:
			if subjectPos >= len(subject) {
				return 0, false, nil
			}
			if matched, known := matchLuaClassByte(subject[subjectPos], next); known {
				if matched {
					return subjectPos + 1, true, nil
				}
				return 0, false, nil
			}
			if subject[subjectPos] == next {
				return subjectPos + 1, true, nil
			}
			return 0, false, nil
		}
	default:
		if subjectPos < len(subject) && subject[subjectPos] == pattern[patternPos] {
			return subjectPos + 1, true, nil
		}
		return 0, false, nil
	}
}

func matchLuaBalanced(subject string, subjectPos int, open byte, close byte) (int, bool) {
	if subjectPos >= len(subject) || subject[subjectPos] != open {
		return 0, false
	}
	depth := 1
	for i := subjectPos + 1; i < len(subject); i++ {
		if subject[i] == close {
			depth--
			if depth == 0 {
				return i + 1, true
			}
		} else if subject[i] == open {
			depth++
		}
	}
	return 0, false
}

func cloneLuaPatternCaptures(captures []luaPatternCapture) []luaPatternCapture {
	if len(captures) == 0 {
		return nil
	}
	copied := make([]luaPatternCapture, len(captures))
	copy(copied, captures)
	return copied
}

func lastOpenLuaPatternCapture(captures []luaPatternCapture) int {
	for i := len(captures) - 1; i >= 0; i-- {
		if !captures[i].isPosition && captures[i].end < 0 {
			return i
		}
	}
	return -1
}

func matchLuaBracketClass(ch byte, pattern string, start int, end int) (bool, error) {
	i := start + 1
	negate := false
	if i < end-1 && pattern[i] == '^' {
		negate = true
		i++
	}
	matched := false
	for i < end-1 {
		if pattern[i] == '%' {
			if i+1 >= end-1 {
				return false, fmt.Errorf("malformed pattern class")
			}
			if classMatched, known := matchLuaClassByte(ch, pattern[i+1]); known {
				matched = matched || classMatched
				i += 2
				continue
			}
			matched = matched || ch == pattern[i+1]
			i += 2
			continue
		}
		if i+2 < end-1 && pattern[i+1] == '-' {
			left := pattern[i]
			right := pattern[i+2]
			if left <= right {
				matched = matched || (ch >= left && ch <= right)
			} else {
				matched = matched || (ch >= right && ch <= left)
			}
			i += 3
			continue
		}
		matched = matched || ch == pattern[i]
		i++
	}
	if negate {
		return !matched, nil
	}
	return matched, nil
}

func matchLuaClassByte(ch byte, code byte) (bool, bool) {
	switch code {
	case 'a':
		return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z'), true
	case 'A':
		matched, _ := matchLuaClassByte(ch, 'a')
		return !matched, true
	case 'd':
		return ch >= '0' && ch <= '9', true
	case 'D':
		matched, _ := matchLuaClassByte(ch, 'd')
		return !matched, true
	case 'l':
		return ch >= 'a' && ch <= 'z', true
	case 'L':
		matched, _ := matchLuaClassByte(ch, 'l')
		return !matched, true
	case 'u':
		return ch >= 'A' && ch <= 'Z', true
	case 'U':
		matched, _ := matchLuaClassByte(ch, 'u')
		return !matched, true
	case 'w':
		return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_', true
	case 'W':
		matched, _ := matchLuaClassByte(ch, 'w')
		return !matched, true
	case 's':
		return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '\f' || ch == '\v', true
	case 'S':
		matched, _ := matchLuaClassByte(ch, 's')
		return !matched, true
	case 'p':
		return (ch >= 33 && ch <= 47) || (ch >= 58 && ch <= 64) || (ch >= 91 && ch <= 96) || (ch >= 123 && ch <= 126), true
	case 'P':
		matched, _ := matchLuaClassByte(ch, 'p')
		return !matched, true
	case 'c':
		return ch < 32 || ch == 127, true
	case 'C':
		matched, _ := matchLuaClassByte(ch, 'c')
		return !matched, true
	case 'x':
		return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F'), true
	case 'X':
		matched, _ := matchLuaClassByte(ch, 'x')
		return !matched, true
	case 'z':
		return ch == 0, true
	case 'Z':
		return ch != 0, true
	default:
		return false, false
	}
}

func compileLuaPattern(pattern string) (*regexp.Regexp, int, error) {
	translated, captures, err := translateLuaPattern(pattern)
	if err != nil {
		return nil, 0, err
	}
	re, err := regexp.Compile("(?s)" + translated)
	if err != nil {
		return nil, 0, err
	}
	return re, captures, nil
}

func translateLuaPattern(pattern string) (string, int, error) {
	var builder strings.Builder
	captures := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '%':
			if i+1 >= len(pattern) {
				return "", 0, fmt.Errorf("unterminated string pattern escape")
			}
			i++
			next := pattern[i]
			if next >= '1' && next <= '9' {
				return "", 0, fmt.Errorf("unsupported string pattern back reference %%%c", next)
			}
			if next == 'b' || next == 'f' {
				return "", 0, fmt.Errorf("unsupported string pattern %%%c", next)
			}
			if class, ok, err := luaPatternClass(next, false); err != nil {
				return "", 0, err
			} else if ok {
				builder.WriteString(class)
				continue
			}
			builder.WriteString(regexp.QuoteMeta(string(next)))
		case '[':
			translated, nextIndex, err := translateLuaClass(pattern, i)
			if err != nil {
				return "", 0, err
			}
			builder.WriteString(translated)
			i = nextIndex - 1
		case '^':
			if i == 0 {
				builder.WriteByte('^')
			} else {
				builder.WriteString("\\^")
			}
		case '$':
			if i == len(pattern)-1 {
				builder.WriteByte('$')
			} else {
				builder.WriteString("\\$")
			}
		case '(':
			if i+1 < len(pattern) && pattern[i+1] == ')' {
				return "", 0, fmt.Errorf("unsupported empty string capture")
			}
			captures++
			builder.WriteByte('(')
		case ')':
			builder.WriteByte(')')
		case '.':
			builder.WriteByte('.')
		case '*':
			builder.WriteByte('*')
		case '+':
			builder.WriteByte('+')
		case '-':
			builder.WriteString("*?")
		case '?':
			builder.WriteByte('?')
		default:
			builder.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	return builder.String(), captures, nil
}

func translateLuaClass(pattern string, start int) (string, int, error) {
	var builder strings.Builder
	builder.WriteByte('[')
	i := start + 1
	if i < len(pattern) && pattern[i] == '^' {
		builder.WriteByte('^')
		i++
	}
	for ; i < len(pattern); i++ {
		switch pattern[i] {
		case ']':
			builder.WriteByte(']')
			return builder.String(), i + 1, nil
		case '%':
			if i+1 >= len(pattern) {
				return "", 0, fmt.Errorf("unterminated string pattern class")
			}
			i++
			next := pattern[i]
			if next >= '1' && next <= '9' {
				return "", 0, fmt.Errorf("unsupported string pattern back reference %%%c", next)
			}
			if next == 'b' || next == 'f' {
				return "", 0, fmt.Errorf("unsupported string pattern %%%c", next)
			}
			if class, ok, err := luaPatternClass(next, true); err != nil {
				return "", 0, err
			} else if ok {
				builder.WriteString(class)
				continue
			}
			if next == '\\' || next == ']' || next == '^' {
				builder.WriteByte('\\')
			}
			builder.WriteByte(next)
		case '\\':
			builder.WriteString("\\\\")
		default:
			builder.WriteByte(pattern[i])
		}
	}
	return "", 0, fmt.Errorf("unterminated string pattern class")
}

func luaPatternClass(code byte, inClass bool) (string, bool, error) {
	switch code {
	case 'a':
		if inClass {
			return "A-Za-z", true, nil
		}
		return "[A-Za-z]", true, nil
	case 'A':
		if inClass {
			return "", false, fmt.Errorf("unsupported negated string class %%A inside []")
		}
		return "[^A-Za-z]", true, nil
	case 'd':
		if inClass {
			return "0-9", true, nil
		}
		return "[0-9]", true, nil
	case 'D':
		if inClass {
			return "", false, fmt.Errorf("unsupported negated string class %%D inside []")
		}
		return "[^0-9]", true, nil
	case 'l':
		if inClass {
			return "a-z", true, nil
		}
		return "[a-z]", true, nil
	case 'L':
		if inClass {
			return "", false, fmt.Errorf("unsupported negated string class %%L inside []")
		}
		return "[^a-z]", true, nil
	case 'u':
		if inClass {
			return "A-Z", true, nil
		}
		return "[A-Z]", true, nil
	case 'U':
		if inClass {
			return "", false, fmt.Errorf("unsupported negated string class %%U inside []")
		}
		return "[^A-Z]", true, nil
	case 'w':
		if inClass {
			return "A-Za-z0-9_", true, nil
		}
		return "[A-Za-z0-9_]", true, nil
	case 'W':
		if inClass {
			return "", false, fmt.Errorf("unsupported negated string class %%W inside []")
		}
		return "[^A-Za-z0-9_]", true, nil
	case 's':
		if inClass {
			return " \t\r\n\f\v", true, nil
		}
		return "[ \t\r\n\f\v]", true, nil
	case 'S':
		if inClass {
			return "", false, fmt.Errorf("unsupported negated string class %%S inside []")
		}
		return "[^ \t\r\n\f\v]", true, nil
	case 'p':
		if inClass {
			return "[:punct:]", true, nil
		}
		return "[[:punct:]]", true, nil
	case 'P':
		if inClass {
			return "", false, fmt.Errorf("unsupported negated string class %%P inside []")
		}
		return "[^[:punct:]]", true, nil
	case 'c':
		if inClass {
			return "[:cntrl:]", true, nil
		}
		return "[[:cntrl:]]", true, nil
	case 'C':
		if inClass {
			return "", false, fmt.Errorf("unsupported negated string class %%C inside []")
		}
		return "[^[:cntrl:]]", true, nil
	case 'x':
		if inClass {
			return "A-Fa-f0-9", true, nil
		}
		return "[A-Fa-f0-9]", true, nil
	case 'X':
		if inClass {
			return "", false, fmt.Errorf("unsupported negated string class %%X inside []")
		}
		return "[^A-Fa-f0-9]", true, nil
	case 'z':
		if inClass {
			return "\\x00", true, nil
		}
		return "\\x00", true, nil
	case 'Z':
		if inClass {
			return "", false, fmt.Errorf("unsupported negated string class %%Z inside []")
		}
		return "[^\\x00]", true, nil
	default:
		return "", false, nil
	}
}

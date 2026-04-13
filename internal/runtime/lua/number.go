// Package lua centralizes Lua 5.1 coercion rules that are shared by the
// interpreter and baseline slow paths. The shape mirrors Sparkplug's approach:
// keep cheap type checks inline, and funnel generic conversion through a shared
// helper instead of duplicating it in each opcode path.
package lua

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"vexlua/internal/runtime/value"
)

var (
	decimalNumberPattern = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)
	hexIntegerPattern    = regexp.MustCompile(`^[+-]?0[xX][0-9a-fA-F]+$`)
)

type StringLookup func(value.HeapRef44) (string, error)

func ParseNumber(text string) (float64, bool) {
	trimmed := strings.TrimFunc(text, unicode.IsSpace)
	if trimmed == "" {
		return 0, false
	}
	if hexIntegerPattern.MatchString(trimmed) {
		parsed, ok := parseHexInteger(trimmed)
		if !ok {
			return 0, false
		}
		return value.CanonicalizeNumber(parsed), true
	}
	if !decimalNumberPattern.MatchString(trimmed) {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		var numErr *strconv.NumError
		if !errors.As(err, &numErr) || numErr.Err != strconv.ErrRange {
			return 0, false
		}
	}
	return value.CanonicalizeNumber(parsed), true
}

func FormatNumber(number float64) string {
	number = value.CanonicalizeNumber(number)
	if text, ok := formatSpecialNumber(number); ok {
		return text
	}
	text := strconv.FormatFloat(number, 'g', 14, 64)
	exponent := strings.IndexAny(text, "eE")
	if exponent < 0 || exponent+2 >= len(text) {
		return text
	}
	sign := text[exponent+1]
	if sign != '+' && sign != '-' {
		return text
	}
	digits := text[exponent+2:]
	if len(digits) >= 3 {
		return text
	}
	return text[:exponent+2] + strings.Repeat("0", 3-len(digits)) + digits
}

func formatSpecialNumber(number float64) (string, bool) {
	if !math.IsNaN(number) && !math.IsInf(number, 0) {
		return "", false
	}
	if runtime.GOOS == "windows" {
		switch {
		case math.IsNaN(number):
			return "-1.#IND", true
		case math.IsInf(number, 1):
			return "1.#INF", true
		default:
			return "-1.#INF", true
		}
	}
	return strconv.FormatFloat(number, 'g', 14, 64), true
}

func ToNumber(slotValue value.TValue, lookup StringLookup) (value.TValue, bool, error) {
	if number, ok := slotValue.Float64(); ok {
		return value.NumberValue(number), true, nil
	}
	if !slotValue.IsBoxedTag(value.TagStringRef) {
		return value.NilValue(), false, nil
	}
	if lookup == nil {
		return value.NilValue(), false, fmt.Errorf("string lookup is nil")
	}
	ref, _ := slotValue.HeapRef()
	text, err := lookup(ref)
	if err != nil {
		return value.NilValue(), false, err
	}
	parsed, ok := ParseNumber(text)
	if !ok {
		return value.NilValue(), false, nil
	}
	return value.NumberValue(parsed), true, nil
}

func ToStringText(slotValue value.TValue, lookup StringLookup) (string, bool, error) {
	if slotValue.IsBoxedTag(value.TagStringRef) {
		if lookup == nil {
			return "", false, fmt.Errorf("string lookup is nil")
		}
		ref, _ := slotValue.HeapRef()
		text, err := lookup(ref)
		if err != nil {
			return "", false, err
		}
		return text, true, nil
	}
	if number, ok := slotValue.Float64(); ok {
		return FormatNumber(number), true, nil
	}
	return "", false, nil
}

func parseHexInteger(text string) (float64, bool) {
	sign := 1.0
	trimmed := text
	if strings.HasPrefix(trimmed, "+") {
		trimmed = trimmed[1:]
	} else if strings.HasPrefix(trimmed, "-") {
		sign = -1
		trimmed = trimmed[1:]
	}
	if len(trimmed) < 3 || trimmed[0] != '0' || (trimmed[1] != 'x' && trimmed[1] != 'X') {
		return 0, false
	}
	parsed, err := strconv.ParseUint(trimmed[2:], 16, 64)
	if err != nil {
		return 0, false
	}
	return sign * float64(parsed), true
}

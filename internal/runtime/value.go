package runtime

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Value uint64

type ValueKind uint8

const (
	KindNumber ValueKind = iota
	KindNil
	KindBool
	KindHandle
)

const (
	boxBase      uint64 = 0x7ffc000000000000
	boxCheckMask uint64 = 0xffff000000000000
	tagShift            = 44
	payloadMask  uint64 = (uint64(1) << tagShift) - 1

	tagNumberNaN uint64 = 0
	tagNil       uint64 = 1
	tagBool      uint64 = 2
	tagHandle    uint64 = 3
)

var (
	NilValue   = boxed(tagNil, 0)
	FalseValue = boxed(tagBool, 0)
	TrueValue  = boxed(tagBool, 1)
	NaNValue   = boxed(tagNumberNaN, 1)
)

func boxed(tag, payload uint64) Value {
	return Value(boxBase | (tag << tagShift) | (payload & payloadMask))
}

func isBoxed(bits uint64) bool {
	return bits&boxCheckMask == boxBase
}

func NumberValue(v float64) Value {
	if math.IsNaN(v) {
		return NaNValue
	}
	bits := math.Float64bits(v)
	if isBoxed(bits) {
		return NaNValue
	}
	return Value(bits)
}

func BoolValue(v bool) Value {
	if v {
		return TrueValue
	}
	return FalseValue
}

func HandleValue(h Handle) Value {
	return boxed(tagHandle, uint64(h))
}

func (v Value) Kind() ValueKind {
	bits := uint64(v)
	if !isBoxed(bits) {
		return KindNumber
	}
	switch (bits >> tagShift) & 0xF {
	case tagNil:
		return KindNil
	case tagBool:
		return KindBool
	case tagHandle:
		return KindHandle
	default:
		return KindNumber
	}
}

func (v Value) IsNumber() bool {
	return v.Kind() == KindNumber
}

func (v Value) Number() float64 {
	if v == NaNValue {
		return math.NaN()
	}
	return math.Float64frombits(uint64(v))
}

func (v Value) Bool() bool {
	return uint64(v)&payloadMask == 1
}

func (v Value) Handle() (Handle, bool) {
	if v.Kind() != KindHandle {
		return 0, false
	}
	return Handle(uint64(v) & payloadMask), true
}

func FormatNumber(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return normalizeNumberExponent(fmt.Sprintf("%.14g", v))
	}
	if v == 0 {
		return "0"
	}
	sign := ""
	abs := v
	if v < 0 {
		sign = "-"
		abs = -v
	}
	scientific := strconv.FormatFloat(abs, 'e', -1, 64)
	eIndex := strings.IndexByte(scientific, 'e')
	if eIndex < 0 {
		return sign + scientific
	}
	exponent, err := strconv.Atoi(scientific[eIndex+1:])
	if err != nil {
		return sign + scientific
	}
	digits := strings.ReplaceAll(scientific[:eIndex], ".", "")
	digits, exponent = roundGeneralDigits(digits, exponent, 14)
	if exponent < -4 || exponent >= 14 {
		return sign + scientificDigitsString(digits, exponent)
	}
	return sign + fixedDigitsString(digits, exponent)
}

func (v Value) String() string {
	switch v.Kind() {
	case KindNil:
		return "nil"
	case KindBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case KindNumber:
		return FormatNumber(v.Number())
	case KindHandle:
		h, _ := v.Handle()
		return h.String()
	default:
		return "<unknown>"
	}
}

func normalizeNumberExponent(text string) string {
	index := strings.LastIndexAny(text, "eE")
	if index < 0 || index+2 >= len(text) {
		return text
	}
	sign := text[index+1]
	if sign != '+' && sign != '-' {
		return text
	}
	digits := text[index+2:]
	if digits == "" {
		return text
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return text
		}
	}
	if len(digits) >= 3 {
		return text
	}
	return text[:index+2] + strings.Repeat("0", 3-len(digits)) + digits
}

func roundGeneralDigits(digits string, exponent int, precision int) (string, int) {
	if len(digits) <= precision {
		return digits, exponent
	}
	rounded := []byte(digits[:precision])
	if digits[precision] >= '5' {
		for index := len(rounded) - 1; index >= 0; index-- {
			if rounded[index] < '9' {
				rounded[index]++
				goto done
			}
			rounded[index] = '0'
		}
		rounded = append([]byte{'1'}, rounded...)
		exponent++
	}
done:
	if len(rounded) > precision {
		rounded = rounded[:precision]
	}
	return string(rounded), exponent
}

func scientificDigitsString(digits string, exponent int) string {
	fraction := strings.TrimRight(digits[1:], "0")
	if fraction == "" {
		return digits[:1] + formatExponent(exponent)
	}
	return digits[:1] + "." + fraction + formatExponent(exponent)
}

func fixedDigitsString(digits string, exponent int) string {
	decimalPos := exponent + 1
	var text string
	switch {
	case decimalPos <= 0:
		text = "0." + strings.Repeat("0", -decimalPos) + digits
	case decimalPos >= len(digits):
		text = digits + strings.Repeat("0", decimalPos-len(digits))
	default:
		text = digits[:decimalPos] + "." + digits[decimalPos:]
	}
	if strings.Contains(text, ".") {
		text = strings.TrimRight(text, "0")
		text = strings.TrimRight(text, ".")
	}
	return text
}

func formatExponent(exponent int) string {
	if exponent < 0 {
		return fmt.Sprintf("e-%03d", -exponent)
	}
	return fmt.Sprintf("e+%03d", exponent)
}

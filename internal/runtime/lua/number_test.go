package lua_test

import (
	"math"
	"runtime"
	"testing"

	rtlua "vexlua/internal/runtime/lua"
	"vexlua/internal/runtime/value"
)

func TestParseNumberAcceptsLua51Forms(t *testing.T) {
	tests := map[string]float64{
		"42":       42,
		"  +3.5  ": 3.5,
		"-0.25":    -0.25,
		"1e2":      100,
		"1.":       1,
		".5":       0.5,
		"0x10":     16,
		"-0X1f":    -31,
		"1e5000":   math.Inf(1),
	}
	for text, want := range tests {
		got, ok := rtlua.ParseNumber(text)
		if !ok {
			t.Fatalf("ParseNumber(%q) unexpectedly failed", text)
		}
		if math.IsInf(want, 1) {
			if !math.IsInf(got, 1) {
				t.Fatalf("ParseNumber(%q) = %v, want +Inf", text, got)
			}
			continue
		}
		if got != want {
			t.Fatalf("ParseNumber(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestParseNumberRejectsInvalidLua51Forms(t *testing.T) {
	tests := []string{"", "   ", "foo", "1foo", "0x", "0x1p2", "1_2", "nan", "inf"}
	for _, text := range tests {
		if got, ok := rtlua.ParseNumber(text); ok {
			t.Fatalf("ParseNumber(%q) unexpectedly succeeded with %v", text, got)
		}
	}
}

func TestFormatNumberUsesLua51Precision(t *testing.T) {
	tests := map[float64]string{
		42:                 "42",
		12.5:               "12.5",
		1.2345678901234567: "1.2345678901235",
		1e14:               "1e+014",
		1e-5:               "1e-005",
	}
	for input, want := range tests {
		if got := rtlua.FormatNumber(input); got != want {
			t.Fatalf("FormatNumber(%v) = %q, want %q", input, got, want)
		}
	}
}

func TestFormatNumberSpecialValuesMatchWindowsLua51(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific Lua 5.1 CRT spelling")
	}
	tests := map[string]float64{
		"1.#INF":  math.Inf(1),
		"-1.#INF": math.Inf(-1),
		"-1.#IND": math.NaN(),
	}
	for want, input := range tests {
		if got := rtlua.FormatNumber(input); got != want {
			t.Fatalf("FormatNumber(%v) = %q, want %q", input, got, want)
		}
	}
}

func TestToNumberCoercesStringValues(t *testing.T) {
	lookup := func(ref value.HeapRef44) (string, error) {
		switch ref {
		case 1:
			return " 0x10 ", nil
		case 2:
			return "3.5", nil
		default:
			return "", nil
		}
	}

	tests := []struct {
		name  string
		input value.TValue
		want  float64
		ok    bool
	}{
		{name: "number", input: value.NumberValue(7), want: 7, ok: true},
		{name: "hex string", input: value.StringRefValue(1), want: 16, ok: true},
		{name: "decimal string", input: value.StringRefValue(2), want: 3.5, ok: true},
		{name: "bool", input: value.BoolValue(true), ok: false},
	}

	for _, test := range tests {
		got, ok, err := rtlua.ToNumber(test.input, lookup)
		if err != nil {
			t.Fatalf("ToNumber(%s) returned error: %v", test.name, err)
		}
		if ok != test.ok {
			t.Fatalf("ToNumber(%s) ok = %t, want %t", test.name, ok, test.ok)
		}
		if !ok {
			continue
		}
		number, _ := got.Float64()
		if number != test.want {
			t.Fatalf("ToNumber(%s) = %v, want %v", test.name, number, test.want)
		}
	}
}

func TestToStringTextFormatsNumbersAndStrings(t *testing.T) {
	lookup := func(ref value.HeapRef44) (string, error) {
		if ref == 7 {
			return "hello", nil
		}
		return "", nil
	}

	text, ok, err := rtlua.ToStringText(value.StringRefValue(7), lookup)
	if err != nil || !ok || text != "hello" {
		t.Fatalf("ToStringText(string) = %q, %t, %v", text, ok, err)
	}

	text, ok, err = rtlua.ToStringText(value.NumberValue(1e14), lookup)
	if err != nil || !ok || text != "1e+014" {
		t.Fatalf("ToStringText(number) = %q, %t, %v", text, ok, err)
	}

	if runtime.GOOS == "windows" {
		text, ok, err = rtlua.ToStringText(value.NumberValue(math.Inf(1)), lookup)
		if err != nil || !ok || text != "1.#INF" {
			t.Fatalf("ToStringText(+Inf) = %q, %t, %v", text, ok, err)
		}
	}

	if text, ok, err = rtlua.ToStringText(value.BoolValue(true), lookup); err != nil || ok || text != "" {
		t.Fatalf("ToStringText(bool) = %q, %t, %v", text, ok, err)
	}
}

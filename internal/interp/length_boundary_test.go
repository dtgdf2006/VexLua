package interp

import (
	"testing"

	"vexlua/internal/runtime/value"
)

func TestLengthBoundaryUsesLuaHGetnAndIgnoresTableStringMetamethods(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	lenKey, err := engine.InternString(metaLenName)
	if err != nil {
		t.Fatalf("intern __len key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	lenCalls := 0
	lenMeta, err := engine.RegisterHostFunction("len-meta", func(value.TValue) float64 {
		lenCalls++
		return 99
	}, env.Value)
	if err != nil {
		t.Fatalf("register __len host function: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, lenKey.Value, lenMeta.Value); err != nil {
		t.Fatalf("seed __len metamethod: %v", err)
	}
	textValue, err := engine.InternString("hello")
	if err != nil {
		t.Fatalf("intern string value: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(textValue.Value, metatable.Value); err != nil {
		t.Fatalf("set string metatable: %v", err)
	}
	tableValue, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new table value: %v", err)
	}
	for _, key := range []float64{1, 2, 4} {
		if err := engine.Tables.Set(tableValue.Ref, value.NumberValue(key), value.NumberValue(key*10)); err != nil {
			t.Fatalf("seed table key %v: %v", key, err)
		}
	}
	if err := engine.SetValueMetatableBoundary(tableValue.Value, metatable.Value); err != nil {
		t.Fatalf("set table metatable: %v", err)
	}
	textLen, err := engine.LengthBoundary(thread, textValue.Value)
	if err != nil {
		t.Fatalf("string length boundary: %v", err)
	}
	if textLen.Bits() != value.NumberValue(5).Bits() {
		t.Fatalf("string length = %s, want 5", textLen)
	}
	tableLen, err := engine.LengthBoundary(thread, tableValue.Value)
	if err != nil {
		t.Fatalf("table length boundary: %v", err)
	}
	if tableLen.Bits() != value.NumberValue(4).Bits() {
		t.Fatalf("table length = %s, want 4", tableLen)
	}
	if lenCalls != 0 {
		t.Fatalf("string/table __len call count = %d, want 0", lenCalls)
	}
}

func TestLengthBoundaryUsesTypeMetatableAndLua51Errors(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	lenKey, err := engine.InternString(metaLenName)
	if err != nil {
		t.Fatalf("intern __len key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	lenMeta, err := engine.RegisterHostFunction("number-len-meta", func(number float64) float64 {
		return number + 35
	}, env.Value)
	if err != nil {
		t.Fatalf("register number __len host function: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, lenKey.Value, lenMeta.Value); err != nil {
		t.Fatalf("seed number __len metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(value.NumberValue(7), metatable.Value); err != nil {
		t.Fatalf("set number metatable: %v", err)
	}
	result, err := engine.LengthBoundary(thread, value.NumberValue(7))
	if err != nil {
		t.Fatalf("number length boundary: %v", err)
	}
	if result.Bits() != value.NumberValue(42).Bits() {
		t.Fatalf("number length result = %s, want 42", result)
	}
	_, err = engine.LengthBoundary(thread, value.BoolValue(true))
	if err == nil || err.Error() != "attempt to get length of a boolean value" {
		t.Fatalf("bool length error = %v, want Lua 5.1 type error", err)
	}
}

package interp

import (
	"fmt"
	"testing"

	"vexlua/internal/runtime/value"
)

func TestConcatBoundaryUsesTMConcatAndLua51RightFold(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	concatKey, err := engine.InternString(metaConcatName)
	if err != nil {
		t.Fatalf("intern __concat key: %v", err)
	}
	labelKey, err := engine.InternString("label")
	if err != nil {
		t.Fatalf("intern label key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	concatCalls := 0
	concatMeta, err := engine.RegisterHostFunction("concat-meta", func(lhs value.TValue, rhs value.TValue) (string, error) {
		concatCalls++
		leftLabel, err := concatBoundaryLabel(engine, lhs, labelKey.Value)
		if err != nil {
			return "", err
		}
		rightLabel, err := concatBoundaryLabel(engine, rhs, labelKey.Value)
		if err != nil {
			return "", err
		}
		return leftLabel + rightLabel, nil
	}, env.Value)
	if err != nil {
		t.Fatalf("register __concat host function: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, concatKey.Value, concatMeta.Value); err != nil {
		t.Fatalf("seed __concat metamethod: %v", err)
	}
	leftTable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left table: %v", err)
	}
	rightTable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right table: %v", err)
	}
	for handle, label := range map[value.HeapRef44]string{leftTable.Ref: "B", rightTable.Ref: "C"} {
		textHandle, err := engine.InternString(label)
		if err != nil {
			t.Fatalf("intern label %q: %v", label, err)
		}
		if err := engine.Tables.Set(handle, labelKey.Value, textHandle.Value); err != nil {
			t.Fatalf("seed label %q: %v", label, err)
		}
	}
	if err := engine.SetValueMetatableBoundary(leftTable.Value, metatable.Value); err != nil {
		t.Fatalf("set left metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(rightTable.Value, metatable.Value); err != nil {
		t.Fatalf("set right metatable: %v", err)
	}
	prefix, err := engine.InternString("A")
	if err != nil {
		t.Fatalf("intern prefix: %v", err)
	}
	result, err := engine.ConcatBoundary(thread, []value.TValue{prefix.Value, leftTable.Value, rightTable.Value})
	if err != nil {
		t.Fatalf("concat boundary: %v", err)
	}
	want, err := engine.InternString("ABC")
	if err != nil {
		t.Fatalf("intern expected string: %v", err)
	}
	if result.Bits() != want.Value.Bits() {
		t.Fatalf("concat result = %s, want %s", result, want.Value)
	}
	if concatCalls != 1 {
		t.Fatalf("__concat call count = %d, want 1", concatCalls)
	}
}

func TestConcatBoundaryUsesLua51ConcatTypeError(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	prefix, err := engine.InternString("hello")
	if err != nil {
		t.Fatalf("intern prefix: %v", err)
	}
	_, err = engine.ConcatBoundary(thread, []value.TValue{prefix.Value, value.BoolValue(true)})
	if err == nil || err.Error() != "attempt to concatenate a boolean value" {
		t.Fatalf("concat error = %v, want Lua 5.1 type error", err)
	}
}

func concatBoundaryLabel(engine *Engine, candidate value.TValue, labelKey value.TValue) (string, error) {
	if candidate.IsBoxedTag(value.TagTableRef) {
		ref, _ := candidate.HeapRef()
		labelValue, found, err := engine.Tables.Get(ref, labelKey)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fmt.Errorf("missing label field")
		}
		labelRef, _ := labelValue.HeapRef()
		return engine.Strings.Text(labelRef)
	}
	if candidate.IsBoxedTag(value.TagStringRef) {
		ref, _ := candidate.HeapRef()
		return engine.Strings.Text(ref)
	}
	return "", fmt.Errorf("unexpected concat operand %s", candidate)
}

package interp

import (
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/value"
)

func TestReadIndexMetaBoundaryUsesStringTypeMetatable(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	stringValue, err := engine.InternString("hello")
	if err != nil {
		t.Fatalf("intern string value: %v", err)
	}
	answerKey, err := engine.InternString("answer")
	if err != nil {
		t.Fatalf("intern answer key: %v", err)
	}
	indexKey, err := engine.InternString("__index")
	if err != nil {
		t.Fatalf("intern __index key: %v", err)
	}
	fallback, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new fallback table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	if err := engine.Tables.Set(fallback.Ref, answerKey.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("seed fallback table: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, indexKey.Value, fallback.Value); err != nil {
		t.Fatalf("seed string __index: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(stringValue.Value, metatable.Value); err != nil {
		t.Fatalf("set string metatable: %v", err)
	}
	gotMetatable, found, err := engine.GetMetatableBoundary(stringValue.Value)
	if err != nil {
		t.Fatalf("get string metatable: %v", err)
	}
	if !found || gotMetatable.Bits() != metatable.Value.Bits() {
		t.Fatalf("string metatable = %s (found=%v), want %s", gotMetatable, found, metatable.Value)
	}
	result, found, err := engine.ReadIndexMetaBoundary(thread, stringValue.Value, answerKey.Value)
	if err != nil {
		t.Fatalf("read string meta boundary: %v", err)
	}
	if !found || result.Bits() != value.NumberValue(42).Bits() {
		t.Fatalf("string meta boundary result = %s (found=%v), want number(42)", result, found)
	}
}

func TestWriteIndexMetaBoundaryUsesNumberTypeMetatable(t *testing.T) {
	engine := New()
	answerKey, err := engine.InternString("answer")
	if err != nil {
		t.Fatalf("intern answer key: %v", err)
	}
	newIndexKey, err := engine.InternString("__newindex")
	if err != nil {
		t.Fatalf("intern __newindex key: %v", err)
	}
	sink, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new sink table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new metatable: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, newIndexKey.Value, sink.Value); err != nil {
		t.Fatalf("seed number __newindex: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(value.NumberValue(7), metatable.Value); err != nil {
		t.Fatalf("set number metatable: %v", err)
	}
	if err := engine.WriteIndexMetaBoundary(nil, value.NumberValue(7), answerKey.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("write number meta boundary: %v", err)
	}
	stored, found, err := engine.Tables.Get(sink.Ref, answerKey.Value)
	if err != nil {
		t.Fatalf("read sink table: %v", err)
	}
	if !found || stored.Bits() != value.NumberValue(42).Bits() {
		t.Fatalf("sink answer = %s (found=%v), want number(42)", stored, found)
	}
}

func TestHostObjectMetatableDoesNotShareLightHandleTypeMetatable(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	lightMetatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new light metatable: %v", err)
	}
	lightValue := value.LightHandleValue(1)
	if err := engine.SetValueMetatableBoundary(lightValue, lightMetatable.Value); err != nil {
		t.Fatalf("set light handle metatable: %v", err)
	}
	gotLightMetatable, found, err := engine.GetMetatableBoundary(lightValue)
	if err != nil {
		t.Fatalf("get light handle metatable: %v", err)
	}
	if !found || gotLightMetatable.Bits() != lightMetatable.Value.Bits() {
		t.Fatalf("light handle metatable = %s (found=%v), want %s", gotLightMetatable, found, lightMetatable.Value)
	}
	wrapper, err := engine.RegisterHostObject("bag", map[string]float64{"x": 1}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	hostMetatable, found, err := engine.GetMetatableBoundary(wrapper.Value)
	if err != nil {
		t.Fatalf("get host object metatable: %v", err)
	}
	if !found {
		t.Fatalf("host object metatable not found, want default bridge metatable")
	}
	if hostMetatable.Bits() == lightMetatable.Value.Bits() {
		t.Fatalf("host object metatable should not share light handle metatable")
	}
}

func TestCallValueBoundaryUsesTMCallMetamethod(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	biasKey, err := engine.InternString("bias")
	if err != nil {
		t.Fatalf("intern bias key: %v", err)
	}
	callKey, err := engine.InternString("__call")
	if err != nil {
		t.Fatalf("intern __call key: %v", err)
	}
	callProto := &bytecode.Proto{
		Source:       "@tmcall-boundary.lua",
		NumParams:    2,
		MaxStackSize: 3,
		Constants:    []bytecode.Constant{bytecode.StringConstant("bias")},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_GETTABLE, 2, 0, bytecode.RKAsk(0)),
			bytecode.CreateABC(bytecode.OP_ADD, 1, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 1, 2, 0),
		},
	}
	callClosure, err := engine.NewClosure(callProto, env.Value, nil)
	if err != nil {
		t.Fatalf("new __call closure: %v", err)
	}
	callable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable table: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new callable metatable: %v", err)
	}
	if err := engine.Tables.Set(callable.Ref, biasKey.Value, value.NumberValue(10)); err != nil {
		t.Fatalf("seed callable bias: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, callKey.Value, callClosure.Value); err != nil {
		t.Fatalf("seed __call metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(callable.Value, metatable.Value); err != nil {
		t.Fatalf("set callable metatable: %v", err)
	}
	results, err := engine.CallValueBoundary(thread, callable.Value, []value.TValue{value.NumberValue(32)}, 1)
	if err != nil {
		t.Fatalf("call value boundary: %v", err)
	}
	if len(results) != 1 || results[0].Bits() != value.NumberValue(42).Bits() {
		t.Fatalf("tm_call result = %v, want [42]", results)
	}
}

func TestCompareBoundaryUsesSharedMetamethodIdentityAndLEFallback(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	eqKey, err := engine.InternString("__eq")
	if err != nil {
		t.Fatalf("intern __eq key: %v", err)
	}
	ltKey, err := engine.InternString("__lt")
	if err != nil {
		t.Fatalf("intern __lt key: %v", err)
	}
	left, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left table: %v", err)
	}
	right, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right table: %v", err)
	}
	leftMeta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left metatable: %v", err)
	}
	rightMeta, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right metatable: %v", err)
	}
	eqCalls := 0
	ltCalls := 0
	eqMeta, err := engine.RegisterHostFunction("eq-meta", func(value.TValue, value.TValue) bool {
		eqCalls++
		return true
	}, env.Value)
	if err != nil {
		t.Fatalf("register __eq host function: %v", err)
	}
	ltMeta, err := engine.RegisterHostFunction("lt-meta", func(lhs value.TValue, rhs value.TValue) bool {
		ltCalls++
		return lhs.Bits() == left.Value.Bits() && rhs.Bits() == right.Value.Bits()
	}, env.Value)
	if err != nil {
		t.Fatalf("register __lt host function: %v", err)
	}
	for _, metatable := range []value.TValue{leftMeta.Value, rightMeta.Value} {
		metaRef, _ := metatable.HeapRef()
		if err := engine.Tables.Set(metaRef, eqKey.Value, eqMeta.Value); err != nil {
			t.Fatalf("seed __eq metamethod: %v", err)
		}
		if err := engine.Tables.Set(metaRef, ltKey.Value, ltMeta.Value); err != nil {
			t.Fatalf("seed __lt metamethod: %v", err)
		}
	}
	if err := engine.SetValueMetatableBoundary(left.Value, leftMeta.Value); err != nil {
		t.Fatalf("set left metatable: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(right.Value, rightMeta.Value); err != nil {
		t.Fatalf("set right metatable: %v", err)
	}
	equal, err := engine.CompareBoundary(thread, bytecode.OP_EQ, left.Value, right.Value)
	if err != nil {
		t.Fatalf("compare eq boundary: %v", err)
	}
	if !equal {
		t.Fatalf("tm_eq compare = false, want true")
	}
	less, err := engine.CompareBoundary(thread, bytecode.OP_LT, left.Value, right.Value)
	if err != nil {
		t.Fatalf("compare lt boundary: %v", err)
	}
	if !less {
		t.Fatalf("tm_lt compare = false, want true")
	}
	lessEqual, err := engine.CompareBoundary(thread, bytecode.OP_LE, left.Value, right.Value)
	if err != nil {
		t.Fatalf("compare le boundary: %v", err)
	}
	if !lessEqual {
		t.Fatalf("tm_le fallback compare = false, want true")
	}
	reverseLessEqual, err := engine.CompareBoundary(thread, bytecode.OP_LE, right.Value, left.Value)
	if err != nil {
		t.Fatalf("reverse compare le boundary: %v", err)
	}
	if reverseLessEqual {
		t.Fatalf("reverse tm_le fallback compare = true, want false")
	}
	if eqCalls != 1 {
		t.Fatalf("tm_eq call count = %d, want 1", eqCalls)
	}
	if ltCalls != 3 {
		t.Fatalf("tm_lt call count = %d, want 3", ltCalls)
	}
}

func TestCompareBoundaryIgnoresNumberTypeMetatableAndMatchesOrderErrors(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	eqKey, err := engine.InternString("__eq")
	if err != nil {
		t.Fatalf("intern __eq key: %v", err)
	}
	metatable, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new number metatable: %v", err)
	}
	eqCalls := 0
	eqMeta, err := engine.RegisterHostFunction("eq-number-meta", func(value.TValue, value.TValue) bool {
		eqCalls++
		return true
	}, env.Value)
	if err != nil {
		t.Fatalf("register number __eq host function: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, eqKey.Value, eqMeta.Value); err != nil {
		t.Fatalf("seed number __eq metamethod: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(value.NumberValue(1), metatable.Value); err != nil {
		t.Fatalf("set number metatable: %v", err)
	}
	equal, err := engine.CompareBoundary(nil, bytecode.OP_EQ, value.NumberValue(1), value.NumberValue(2))
	if err != nil {
		t.Fatalf("number compare eq boundary: %v", err)
	}
	if equal {
		t.Fatalf("number compare eq = true, want false")
	}
	if eqCalls != 0 {
		t.Fatalf("number __eq call count = %d, want 0", eqCalls)
	}
	left, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new left table: %v", err)
	}
	right, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new right table: %v", err)
	}
	_, err = engine.CompareBoundary(nil, bytecode.OP_LT, left.Value, right.Value)
	if err == nil || err.Error() != "attempt to compare two table values" {
		t.Fatalf("table compare error = %v, want attempt to compare two table values", err)
	}
	_, err = engine.CompareBoundary(nil, bytecode.OP_LT, left.Value, value.NumberValue(1))
	if err == nil || err.Error() != "attempt to compare table with number" {
		t.Fatalf("mixed compare error = %v, want attempt to compare table with number", err)
	}
}

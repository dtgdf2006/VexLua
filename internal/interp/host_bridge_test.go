package interp

import (
	"encoding/binary"
	"fmt"
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/host"
	"vexlua/internal/runtime/value"
)

type hostCounter struct {
	Value float64
	Name  string
}

type hostTaggedCounter struct {
	Value float64 `lua:"score"`
	Name  string  `lua:"display-name"`
}

type hostTaggedMethodCounter struct {
	Value float64
}

func (counter *hostTaggedMethodCounter) DoubleValue() float64 {
	return counter.Value * 2
}

func (counter *hostTaggedMethodCounter) LuaMethodMap() map[string]string {
	return map[string]string{"double-score": "DoubleValue"}
}

func TestHostObjectStructMapAndFunctionBridge(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	structObject, err := engine.RegisterHostObject("counter", &hostCounter{Name: "demo", Value: 41}, env.Value)
	if err != nil {
		t.Fatalf("register struct object: %v", err)
	}
	mapObject, err := engine.RegisterHostObject("bag", map[string]float64{"x": 5}, env.Value)
	if err != nil {
		t.Fatalf("register map object: %v", err)
	}
	hostFunc, err := engine.RegisterHostFunction("sum3", func(a float64, b float64, c float64) float64 {
		return a + b + c
	}, env.Value)
	if err != nil {
		t.Fatalf("register host function: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "counter", structObject.Value); err != nil {
		t.Fatalf("set global counter: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "bag", mapObject.Value); err != nil {
		t.Fatalf("set global bag: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "sum3", hostFunc.Value); err != nil {
		t.Fatalf("set global sum3: %v", err)
	}

	proto := &bytecode.Proto{
		Source:       "@host.lua",
		MaxStackSize: 10,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("counter"),
			bytecode.StringConstant("Value"),
			bytecode.NumberConstant(42),
			bytecode.StringConstant("bag"),
			bytecode.StringConstant("x"),
			bytecode.StringConstant("sum3"),
			bytecode.NumberConstant(1),
			bytecode.NumberConstant(2),
			bytecode.NumberConstant(3),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 2, 2),
			bytecode.CreateABC(bytecode.OP_SETTABLE, 0, 1, 2),
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 3, 3),
			bytecode.CreateABx(bytecode.OP_LOADK, 4, 4),
			bytecode.CreateABC(bytecode.OP_GETTABLE, 5, 3, 4),
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 6, 5),
			bytecode.CreateABx(bytecode.OP_LOADK, 7, 6),
			bytecode.CreateABx(bytecode.OP_LOADK, 8, 7),
			bytecode.CreateABx(bytecode.OP_LOADK, 9, 8),
			bytecode.CreateABC(bytecode.OP_CALL, 6, 4, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 5, 3, 0),
		},
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("create bridge closure: %v", err)
	}
	results, err := engine.Call(thread, closureHandle.Value, nil, -1)
	if err != nil {
		t.Fatalf("execute bridge closure: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	first, _ := results[0].Float64()
	second, _ := results[1].Float64()
	if first != 5 || second != 6 {
		t.Fatalf("unexpected bridge results: %v", results)
	}
	if structObjectHeader, storedTarget, _, err := engine.Hosts.ReadHostObject(structObject.Ref); err != nil {
		t.Fatalf("read host object wrapper: %v", err)
	} else {
		counter := storedTarget.(*hostCounter)
		if counter.Value != 42 {
			t.Fatalf("struct host object was not mutated, got %g", counter.Value)
		}
		if structObjectHeader.HostHandle == 0 {
			t.Fatalf("host object wrapper should store a non-zero handle")
		}
	}
}

func TestHostDescriptorVersionRefreshesOnBoundaryAccess(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	wrapper, err := engine.RegisterHostObject("counter", &hostCounter{Value: 1}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	header, _, _, err := engine.Hosts.ReadHostObject(wrapper.Ref)
	if err != nil {
		t.Fatalf("read host object: %v", err)
	}
	if err := engine.Hosts.BumpDescriptorVersion(host.Handle(header.HostHandle)); err != nil {
		t.Fatalf("bump descriptor version: %v", err)
	}
	key, err := engine.InternString("Value")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	result, found, err := engine.ReadIndexBoundary(wrapper.Value, key.Value)
	if err != nil {
		t.Fatalf("boundary get after descriptor bump: %v", err)
	}
	if !found || result.Bits() != value.NumberValue(1).Bits() {
		t.Fatalf("unexpected refreshed boundary result: %v found=%v", result, found)
	}
	updated, _, _, err := engine.Hosts.ReadHostObject(wrapper.Ref)
	if err != nil {
		t.Fatalf("read refreshed wrapper: %v", err)
	}
	if updated.DescriptorVersion != header.DescriptorVersion+1 {
		t.Fatalf("wrapper descriptor version = %d, want %d", updated.DescriptorVersion, header.DescriptorVersion+1)
	}
}

func TestHostObjectDefaultMetatableBridgeHandlesMetaBoundary(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	target := map[string]float64{"x": 5}
	wrapper, err := engine.RegisterHostObject("bag", target, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	metatable, found, err := engine.GetMetatableBoundary(wrapper.Value)
	if err != nil {
		t.Fatalf("get wrapper metatable: %v", err)
	}
	if !found || !metatable.IsBoxedTag(value.TagTableRef) {
		t.Fatalf("wrapper metatable = %s (found=%v), want bridge table", metatable, found)
	}
	xKey, err := engine.InternString("x")
	if err != nil {
		t.Fatalf("intern x key: %v", err)
	}
	result, found, err := engine.ReadIndexMetaBoundary(thread, wrapper.Value, xKey.Value)
	if err != nil {
		t.Fatalf("read meta boundary: %v", err)
	}
	if !found || result.Bits() != value.NumberValue(5).Bits() {
		t.Fatalf("meta boundary result = %s (found=%v), want number(5)", result, found)
	}
	if err := engine.WriteIndexMetaBoundary(thread, wrapper.Value, xKey.Value, value.NumberValue(9)); err != nil {
		t.Fatalf("write meta boundary: %v", err)
	}
	if got := target["x"]; got != 9 {
		t.Fatalf("host target x = %v, want 9", got)
	}
}

func TestHostObjectUsesPerObjectMetatableOnReadMetaBoundary(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	wrapper, err := engine.RegisterHostObject("counter", &hostCounter{Value: 41}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	valueKey, err := engine.InternString("Value")
	if err != nil {
		t.Fatalf("intern value key: %v", err)
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
	if err := engine.Tables.Set(fallback.Ref, valueKey.Value, value.NumberValue(99)); err != nil {
		t.Fatalf("seed fallback value: %v", err)
	}
	if err := engine.Tables.Set(metatable.Ref, indexKey.Value, fallback.Value); err != nil {
		t.Fatalf("seed __index: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(wrapper.Value, metatable.Value); err != nil {
		t.Fatalf("set wrapper metatable: %v", err)
	}
	got, found, err := engine.GetMetatableBoundary(wrapper.Value)
	if err != nil {
		t.Fatalf("get wrapper metatable: %v", err)
	}
	if !found || got.Bits() != metatable.Value.Bits() {
		t.Fatalf("wrapper metatable = %s (found=%v), want %s", got, found, metatable.Value)
	}
	result, found, err := engine.ReadIndexMetaBoundary(thread, wrapper.Value, valueKey.Value)
	if err != nil {
		t.Fatalf("read meta boundary: %v", err)
	}
	if !found || result.Bits() != value.NumberValue(99).Bits() {
		t.Fatalf("meta boundary result = %s (found=%v), want number(99)", result, found)
	}
	rawResult, found, err := engine.ReadIndexBoundary(wrapper.Value, valueKey.Value)
	if err != nil {
		t.Fatalf("read raw boundary: %v", err)
	}
	if !found || rawResult.Bits() != value.NumberValue(41).Bits() {
		t.Fatalf("raw boundary result = %s (found=%v), want number(41)", rawResult, found)
	}
}

func TestHostObjectUsesPerObjectMetatableOnWriteMetaBoundary(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	target := map[string]float64{"x": 1}
	wrapper, err := engine.RegisterHostObject("bag", target, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	xKey, err := engine.InternString("x")
	if err != nil {
		t.Fatalf("intern x key: %v", err)
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
		t.Fatalf("seed __newindex: %v", err)
	}
	if err := engine.SetValueMetatableBoundary(wrapper.Value, metatable.Value); err != nil {
		t.Fatalf("set wrapper metatable: %v", err)
	}
	if err := engine.WriteIndexMetaBoundary(thread, wrapper.Value, xKey.Value, value.NumberValue(77)); err != nil {
		t.Fatalf("write meta boundary: %v", err)
	}
	if got := target["x"]; got != 1 {
		t.Fatalf("host target x = %v, want 1", got)
	}
	stored, found, err := engine.Tables.Get(sink.Ref, xKey.Value)
	if err != nil {
		t.Fatalf("read sink table: %v", err)
	}
	if !found || stored.Bits() != value.NumberValue(77).Bits() {
		t.Fatalf("sink x = %s (found=%v), want number(77)", stored, found)
	}
	if err := engine.WriteIndexBoundary(wrapper.Value, xKey.Value, value.NumberValue(55)); err != nil {
		t.Fatalf("write raw boundary: %v", err)
	}
	if got := target["x"]; got != 55 {
		t.Fatalf("host target x after raw boundary = %v, want 55", got)
	}
}

func TestHostObjectNonStringKeyWithoutMetatableErrorsLikeUserdata(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	wrapper, err := engine.RegisterHostObject("bag", map[string]float64{"x": 1}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	_, _, err = engine.ReadIndexMetaBoundary(thread, wrapper.Value, value.NumberValue(1))
	if err == nil || err.Error() != "attempt to index a userdata value" {
		t.Fatalf("read non-string key error = %v, want userdata index error", err)
	}
	if err := engine.WriteIndexMetaBoundary(thread, wrapper.Value, value.NumberValue(1), value.NumberValue(2)); err == nil || err.Error() != "attempt to index a userdata value" {
		t.Fatalf("write non-string key error = %v, want userdata index error", err)
	}
}

func TestHostObjectMissingGetterSetterWithoutMetatableErrorsLikeUserdata(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	wrapper, err := engine.RegisterHostObject("bag", map[string]float64{"x": 1}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	key, err := engine.InternString("x")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	_, _, descriptor, err := engine.Hosts.ReadHostObject(wrapper.Ref)
	if err != nil {
		t.Fatalf("read host object: %v", err)
	}
	descriptor.Get = nil
	descriptor.Set = nil
	_, _, err = engine.ReadIndexMetaBoundary(thread, wrapper.Value, key.Value)
	if err == nil || err.Error() != "attempt to index a userdata value" {
		t.Fatalf("read missing getter error = %v, want userdata index error", err)
	}
	if err := engine.WriteIndexMetaBoundary(thread, wrapper.Value, key.Value, value.NumberValue(2)); err == nil || err.Error() != "attempt to index a userdata value" {
		t.Fatalf("write missing setter error = %v, want userdata index error", err)
	}
}

func TestHostObjectReflectBridgeInternalErrorsCollapseToUserdataError(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	var target *hostCounter
	wrapper, err := engine.RegisterHostObject("counter", target, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	valueKey, err := engine.InternString("Value")
	if err != nil {
		t.Fatalf("intern value key: %v", err)
	}
	_, _, err = engine.ReadIndexMetaBoundary(thread, wrapper.Value, valueKey.Value)
	if err == nil || err.Error() != "attempt to index a userdata value" {
		t.Fatalf("read nil-pointer bridge error = %v, want userdata index error", err)
	}

	typedTarget := map[string]float64{"x": 1}
	wrapper, err = engine.RegisterHostObject("bag", typedTarget, env.Value)
	if err != nil {
		t.Fatalf("register typed host object: %v", err)
	}
	xKey, err := engine.InternString("x")
	if err != nil {
		t.Fatalf("intern x key: %v", err)
	}
	badValue, err := engine.InternString("oops")
	if err != nil {
		t.Fatalf("intern bad value: %v", err)
	}
	if err := engine.WriteIndexMetaBoundary(thread, wrapper.Value, xKey.Value, badValue.Value); err == nil || err.Error() != "attempt to index a userdata value" {
		t.Fatalf("write type-mismatch bridge error = %v, want userdata index error", err)
	}
}

func TestHostObjectExplicitDescriptorErrorsCollapseToUserdataOnMetaBoundary(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	wrapper, err := engine.RegisterHostObject("bag", map[string]float64{"x": 1}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	key, err := engine.InternString("x")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	_, _, descriptor, err := engine.Hosts.ReadHostObject(wrapper.Ref)
	if err != nil {
		t.Fatalf("read host object: %v", err)
	}
	descriptor.Get = func(target any, key any) (any, bool, error) {
		return nil, false, fmt.Errorf("boom")
	}
	_, _, err = engine.ReadIndexMetaBoundary(thread, wrapper.Value, key.Value)
	if err == nil || err.Error() != "attempt to index a userdata value" {
		t.Fatalf("read explicit descriptor meta error = %v, want userdata index error", err)
	}
	descriptor.Set = func(target any, key any, newValue any) error {
		return fmt.Errorf("boom-set")
	}
	if err := engine.WriteIndexMetaBoundary(thread, wrapper.Value, key.Value, value.NumberValue(2)); err == nil || err.Error() != "attempt to index a userdata value" {
		t.Fatalf("write explicit descriptor meta error = %v, want userdata index error", err)
	}
}

func TestHostObjectRawBoundaryStillPropagatesExplicitDescriptorErrors(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	wrapper, err := engine.RegisterHostObject("bag", map[string]float64{"x": 1}, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	key, err := engine.InternString("x")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	_, _, descriptor, err := engine.Hosts.ReadHostObject(wrapper.Ref)
	if err != nil {
		t.Fatalf("read host object: %v", err)
	}
	descriptor.Get = func(target any, key any) (any, bool, error) {
		return nil, false, fmt.Errorf("boom")
	}
	_, _, err = engine.ReadIndexBoundary(wrapper.Value, key.Value)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("read raw descriptor error = %v, want boom", err)
	}
	descriptor.Set = func(target any, key any, newValue any) error {
		return fmt.Errorf("boom-set")
	}
	if err := engine.WriteIndexBoundary(wrapper.Value, key.Value, value.NumberValue(2)); err == nil || err.Error() != "boom-set" {
		t.Fatalf("write raw descriptor error = %v, want boom-set", err)
	}
}

func TestHostObjectBridgeSupportsNumericMapKeys(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	target := map[float64]float64{1: 5}
	wrapper, err := engine.RegisterHostObject("bag", target, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	result, found, err := engine.ReadIndexMetaBoundary(thread, wrapper.Value, value.NumberValue(1))
	if err != nil {
		t.Fatalf("read numeric key through meta boundary: %v", err)
	}
	if !found || result.Bits() != value.NumberValue(5).Bits() {
		t.Fatalf("numeric meta boundary result = %s (found=%v), want number(5)", result, found)
	}
	if err := engine.WriteIndexMetaBoundary(thread, wrapper.Value, value.NumberValue(2), value.NumberValue(9)); err != nil {
		t.Fatalf("write numeric key through meta boundary: %v", err)
	}
	if got := target[2]; got != 9 {
		t.Fatalf("numeric host target value = %v, want 9", got)
	}
	rawResult, found, err := engine.ReadIndexBoundary(wrapper.Value, value.NumberValue(2))
	if err != nil {
		t.Fatalf("read numeric key through raw boundary: %v", err)
	}
	if !found || rawResult.Bits() != value.NumberValue(9).Bits() {
		t.Fatalf("numeric raw boundary result = %s (found=%v), want number(9)", rawResult, found)
	}
}

func TestHostObjectStructBridgeSupportsTaggedLuaFields(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	target := &hostTaggedCounter{Value: 41, Name: "demo"}
	wrapper, err := engine.RegisterHostObject("counter", target, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	scoreKey, err := engine.InternString("score")
	if err != nil {
		t.Fatalf("intern score key: %v", err)
	}
	labelKey, err := engine.InternString("display-name")
	if err != nil {
		t.Fatalf("intern label key: %v", err)
	}
	result, found, err := engine.ReadIndexMetaBoundary(thread, wrapper.Value, scoreKey.Value)
	if err != nil {
		t.Fatalf("read tagged score: %v", err)
	}
	if !found || result.Bits() != value.NumberValue(41).Bits() {
		t.Fatalf("tagged score result = %s (found=%v), want number(41)", result, found)
	}
	label, found, err := engine.ReadIndexMetaBoundary(thread, wrapper.Value, labelKey.Value)
	if err != nil {
		t.Fatalf("read tagged label: %v", err)
	}
	if !found || !label.IsBoxedTag(value.TagStringRef) {
		t.Fatalf("tagged label result = %s (found=%v), want string", label, found)
	}
	labelRef, _ := label.HeapRef()
	labelText, err := engine.Strings.Text(labelRef)
	if err != nil {
		t.Fatalf("read tagged label text: %v", err)
	}
	if labelText != "demo" {
		t.Fatalf("tagged label text = %q, want demo", labelText)
	}
	if err := engine.WriteIndexMetaBoundary(thread, wrapper.Value, scoreKey.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("write tagged score: %v", err)
	}
	if target.Value != 42 {
		t.Fatalf("tagged score should update struct field, got %v", target.Value)
	}
	rawResult, found, err := engine.ReadIndexBoundary(wrapper.Value, scoreKey.Value)
	if err != nil {
		t.Fatalf("read tagged score through raw boundary: %v", err)
	}
	if !found || rawResult.Bits() != value.NumberValue(42).Bits() {
		t.Fatalf("raw tagged score result = %s (found=%v), want number(42)", rawResult, found)
	}
}

func TestHostObjectStructBridgeSupportsTaggedLuaMethods(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	target := &hostTaggedMethodCounter{Value: 21}
	wrapper, err := engine.RegisterHostObject("counter", target, env.Value)
	if err != nil {
		t.Fatalf("register host object: %v", err)
	}
	taggedKey, err := engine.InternString("double-score")
	if err != nil {
		t.Fatalf("intern tagged method key: %v", err)
	}
	leakedKey, err := engine.InternString("DoubleValue")
	if err != nil {
		t.Fatalf("intern leaked method key: %v", err)
	}
	result, found, err := engine.ReadIndexMetaBoundary(thread, wrapper.Value, taggedKey.Value)
	if err != nil {
		t.Fatalf("read tagged method through meta boundary: %v", err)
	}
	if !found || result.Bits() != value.NumberValue(42).Bits() {
		t.Fatalf("tagged method result = %s (found=%v), want number(42)", result, found)
	}
	rawResult, found, err := engine.ReadIndexBoundary(wrapper.Value, taggedKey.Value)
	if err != nil {
		t.Fatalf("read tagged method through raw boundary: %v", err)
	}
	if !found || rawResult.Bits() != value.NumberValue(42).Bits() {
		t.Fatalf("raw tagged method result = %s (found=%v), want number(42)", rawResult, found)
	}
	leakedResult, found, err := engine.ReadIndexBoundary(wrapper.Value, leakedKey.Value)
	if err != nil {
		t.Fatalf("read leaked Go method name through raw boundary: %v", err)
	}
	if found || !leakedResult.IsBoxedTag(value.TagNil) {
		t.Fatalf("raw leaked Go method name should be hidden, got %s (found=%v)", leakedResult, found)
	}
	leakedMeta, _, err := engine.ReadIndexMetaBoundary(thread, wrapper.Value, leakedKey.Value)
	if err != nil {
		t.Fatalf("read leaked Go method name through meta boundary: %v", err)
	}
	if !leakedMeta.IsBoxedTag(value.TagNil) {
		t.Fatalf("meta leaked Go method name should resolve to nil, got %s", leakedMeta)
	}
}

func TestActivationReadsEnvFromNativeClosureObject(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	env1, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env1: %v", err)
	}
	env2, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env2: %v", err)
	}
	if err := engine.SetGlobal(env1.Value, "x", value.NumberValue(1)); err != nil {
		t.Fatalf("set env1.x: %v", err)
	}
	if err := engine.SetGlobal(env2.Value, "x", value.NumberValue(2)); err != nil {
		t.Fatalf("set env2.x: %v", err)
	}
	var closureRef value.HeapRef44
	swap, err := engine.RegisterHostFunction("swap", func() {
		if err := engine.Closures.SetEnv(closureRef, env2.Value); err != nil {
			t.Fatalf("set closure env: %v", err)
		}
	}, env1.Value)
	if err != nil {
		t.Fatalf("register swap function: %v", err)
	}
	if err := engine.SetGlobal(env1.Value, "swap", swap.Value); err != nil {
		t.Fatalf("set global swap: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@swap-env.lua",
		MaxStackSize: 2,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("swap"),
			bytecode.StringConstant("x"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 1),
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closureHandle, err := engine.NewClosure(proto, env1.Value, nil)
	if err != nil {
		t.Fatalf("create closure: %v", err)
	}
	closureRef = closureHandle.Ref
	results, err := engine.Call(thread, closureHandle.Value, nil, -1)
	if err != nil {
		t.Fatalf("execute closure: %v", err)
	}
	if len(results) != 1 || results[0].Bits() != value.NumberValue(2).Bits() {
		t.Fatalf("unexpected env-derived result: %v", results)
	}
}

func TestLoadKReadsFrameConstBaseAfterNativeMutation(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	var proto *bytecode.Proto
	patcher, err := engine.RegisterHostFunction("patchConst", func() {
		base, err := engine.Protos.ConstantBase(proto, engine.Strings)
		if err != nil {
			t.Fatalf("constant base: %v", err)
		}
		offset, err := engine.Heap.OffsetForNativeAddress(base)
		if err != nil {
			t.Fatalf("constant base offset: %v", err)
		}
		second, err := engine.Heap.Resolve(offset+value.HeapOff64(value.TValueSize), value.TValueSize)
		if err != nil {
			t.Fatalf("resolve target constant bytes: %v", err)
		}
		binary.LittleEndian.PutUint64(second, uint64(value.NumberValue(99).Bits()))
	}, env.Value)
	if err != nil {
		t.Fatalf("register patchConst: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "patchConst", patcher.Value); err != nil {
		t.Fatalf("set global patchConst: %v", err)
	}
	proto = &bytecode.Proto{
		Source:       "@const-base.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("patchConst"),
			bytecode.NumberConstant(1),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 1),
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("create closure: %v", err)
	}
	results, err := engine.Call(thread, closureHandle.Value, nil, -1)
	if err != nil {
		t.Fatalf("execute closure: %v", err)
	}
	if len(results) != 1 || results[0].Bits() != value.NumberValue(99).Bits() {
		t.Fatalf("unexpected const-base result: %v", results)
	}
}

func TestHostRegistryReleaseDoesNotLeakReferences(t *testing.T) {
	engine := New()
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	target := map[string]float64{"x": 1}
	handle, err := engine.Hosts.RegisterObject("bag", target)
	if err != nil {
		t.Fatalf("register object: %v", err)
	}
	if _, err := engine.Hosts.WrapObject(handle, env.Value); err != nil {
		t.Fatalf("wrap object: %v", err)
	}
	if err := engine.Hosts.Release(handle); err != nil {
		t.Fatalf("release first ref: %v", err)
	}
	if err := engine.Hosts.Release(handle); err != nil {
		t.Fatalf("release wrapper ref: %v", err)
	}
	if _, err := engine.Hosts.DescriptorVersion(handle); err == nil {
		t.Fatalf("released host handle should be gone from registry")
	}
}

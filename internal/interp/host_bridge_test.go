package interp

import (
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/host"
)

type hostCounter struct {
	Value float64
	Name  string
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

func TestHostDescriptorVersionInvalidation(t *testing.T) {
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
	if _, _, err := engine.hostObjectGet(wrapper.Value, key.Value); err == nil {
		t.Fatalf("expected descriptor version mismatch after bump")
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

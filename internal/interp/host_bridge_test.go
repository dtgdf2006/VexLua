package interp

import (
	"encoding/binary"
	"testing"

	"vexlua/internal/bytecode"
	rclosure "vexlua/internal/runtime/closure"
	"vexlua/internal/runtime/host"
	"vexlua/internal/runtime/value"
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
		object, err := engine.Closures.Object(closureRef)
		if err != nil {
			t.Fatalf("read closure object: %v", err)
		}
		object.Env = env2.Value
		address, err := engine.Heap.DecodeHeapRef(closureRef)
		if err != nil {
			t.Fatalf("decode closure ref: %v", err)
		}
		offset, err := engine.Heap.OffsetForAddress(address)
		if err != nil {
			t.Fatalf("closure offset: %v", err)
		}
		bytes, err := engine.Heap.Resolve(offset, rclosure.ObjectSize)
		if err != nil {
			t.Fatalf("resolve closure bytes: %v", err)
		}
		if err := rclosure.WriteObject(bytes, object); err != nil {
			t.Fatalf("write closure bytes: %v", err)
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

func TestActivationReadsProtoFromCurrentFrame(t *testing.T) {
	engine := New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	childOne := &bytecode.Proto{
		Source:       "@child-one.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(10),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	childTwo := &bytecode.Proto{
		Source:       "@child-two.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(20),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	outerA := &bytecode.Proto{
		Source:       "@outer-a.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("swapProto"),
		},
		Protos: []*bytecode.Proto{childOne},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 1),
			bytecode.CreateABx(bytecode.OP_CLOSURE, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	outerB := &bytecode.Proto{
		Source:       "@outer-b.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("swapProto"),
		},
		Protos: []*bytecode.Proto{childTwo},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 1),
			bytecode.CreateABx(bytecode.OP_CLOSURE, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	outerBHandle, err := engine.Protos.Intern(outerB)
	if err != nil {
		t.Fatalf("intern alternate proto: %v", err)
	}
	swapProto, err := engine.RegisterHostFunction("swapProto", func() {
		frame := thread.CurrentFrame()
		if frame == nil {
			t.Fatalf("expected active Lua frame during host callback")
		}
		frame.Proto = outerBHandle.Value
	}, env.Value)
	if err != nil {
		t.Fatalf("register swapProto: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "swapProto", swapProto.Value); err != nil {
		t.Fatalf("set global swapProto: %v", err)
	}
	closureHandle, err := engine.NewClosure(outerA, env.Value, nil)
	if err != nil {
		t.Fatalf("create outer closure: %v", err)
	}
	results, err := engine.Call(thread, closureHandle.Value, nil, -1)
	if err != nil {
		t.Fatalf("execute outer closure: %v", err)
	}
	if len(results) != 1 || results[0].Bits() != value.NumberValue(20).Bits() {
		t.Fatalf("activation should pick child proto from current frame, got %v", results)
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

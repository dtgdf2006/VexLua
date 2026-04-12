package gc

import (
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/closure"
	"vexlua/internal/runtime/heap"
	rproto "vexlua/internal/runtime/proto"
	"vexlua/internal/runtime/state"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/upvalue"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/baseline"
)

func TestScannerWalksThreadFrameAndOpenUpvalueRoots(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0x12345678)
	protos := rproto.NewStore(runtimeHeap)
	vm := state.NewVMState(runtimeHeap)
	thread, err := vm.NewThread(32, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	upvalues := upvalue.NewManager(runtimeHeap, vm)
	closures := closure.NewStore(runtimeHeap, protos)

	protoObject := &bytecode.Proto{MaxStackSize: 3}
	protoHandle, err := protos.Intern(protoObject)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	closureHandle, err := closures.NewLuaClosure(protoObject, value.NilValue(), nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	registerRoot := mustIntern(t, strings, "register-root")
	openRegisterRoot := mustIntern(t, strings, "open-register-root")
	spillRoot := mustIntern(t, strings, "spill-root")
	varargRoot := mustIntern(t, strings, "vararg-root")
	hiddenRegister := mustIntern(t, strings, "hidden-register")
	deadSlot := mustIntern(t, strings, "dead-slot")

	varargBase, err := thread.SlotAddress(4)
	if err != nil {
		t.Fatalf("vararg base: %v", err)
	}
	frame, err := thread.PushFrame(state.FrameSpec{
		Closure:       closureHandle.Value,
		Proto:         protoHandle.Value,
		RegisterBase:  0,
		VarargBase:    varargBase,
		VarargCount:   1,
		RegisterCount: 3,
		SpillCount:    1,
		Top:           2,
	})
	if err != nil {
		t.Fatalf("push frame: %v", err)
	}
	if err := thread.SetRegister(frame, 0, registerRoot.Value); err != nil {
		t.Fatalf("set register 0: %v", err)
	}
	if err := thread.SetRegister(frame, 1, openRegisterRoot.Value); err != nil {
		t.Fatalf("set register 1: %v", err)
	}
	if err := thread.SetRegister(frame, 2, hiddenRegister.Value); err != nil {
		t.Fatalf("set register 2: %v", err)
	}
	if err := thread.SetSpill(frame, 0, spillRoot.Value); err != nil {
		t.Fatalf("set spill 0: %v", err)
	}
	if err := thread.SetValueAtAddress(varargBase, varargRoot.Value); err != nil {
		t.Fatalf("set vararg: %v", err)
	}
	deadAddress, err := thread.SlotAddress(12)
	if err != nil {
		t.Fatalf("dead slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(deadAddress, deadSlot.Value); err != nil {
		t.Fatalf("set dead slot: %v", err)
	}
	upvalueAddress, err := frame.RegisterAddress(1)
	if err != nil {
		t.Fatalf("register address: %v", err)
	}
	openHandle, err := upvalues.FindOrCreateOpen(thread, upvalueAddress)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}

	scanner := NewScanner(runtimeHeap)
	visited := collectRoots(t, func(visit VisitFunc) error {
		return scanner.WalkVMState(vm, visit)
	})

	for _, ref := range []value.HeapRef44{
		closureHandle.Ref,
		protoHandle.Ref,
		registerRoot.Ref,
		openRegisterRoot.Ref,
		spillRoot.Ref,
		varargRoot.Ref,
		openHandle.Ref,
	} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing root %#x", uint64(ref))
		}
	}
	for _, ref := range []value.HeapRef44{hiddenRegister.Ref, deadSlot.Ref} {
		if _, ok := visited[ref]; ok {
			t.Fatalf("unexpected root %#x", uint64(ref))
		}
	}
}

func TestScannerOpenUpvalueKeepsOutOfTopSlotAlive(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0xCAFEBABE)
	protos := rproto.NewStore(runtimeHeap)
	vm := state.NewVMState(runtimeHeap)
	thread, err := vm.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	upvalues := upvalue.NewManager(runtimeHeap, vm)
	closures := closure.NewStore(runtimeHeap, protos)

	protoObject := &bytecode.Proto{MaxStackSize: 2}
	protoHandle, err := protos.Intern(protoObject)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	closureHandle, err := closures.NewLuaClosure(protoObject, value.NilValue(), nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	capturedRoot := mustIntern(t, strings, "captured-out-of-top-root")
	hiddenRegister := mustIntern(t, strings, "uncaptured-out-of-top-root")
	frame, err := thread.PushFrame(state.FrameSpec{
		Closure:       closureHandle.Value,
		Proto:         protoHandle.Value,
		RegisterBase:  0,
		RegisterCount: 2,
		Top:           0,
	})
	if err != nil {
		t.Fatalf("push frame: %v", err)
	}
	if err := thread.SetRegister(frame, 0, hiddenRegister.Value); err != nil {
		t.Fatalf("set hidden register: %v", err)
	}
	if err := thread.SetRegister(frame, 1, capturedRoot.Value); err != nil {
		t.Fatalf("set captured register: %v", err)
	}
	upvalueAddress, err := frame.RegisterAddress(1)
	if err != nil {
		t.Fatalf("register address: %v", err)
	}
	openHandle, err := upvalues.FindOrCreateOpen(thread, upvalueAddress)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}

	scanner := NewScanner(runtimeHeap)
	visited := collectRoots(t, func(visit VisitFunc) error {
		return scanner.WalkVMState(vm, visit)
	})

	for _, ref := range []value.HeapRef44{closureHandle.Ref, protoHandle.Ref, openHandle.Ref, capturedRoot.Ref} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing root %#x", uint64(ref))
		}
	}
	if _, ok := visited[hiddenRegister.Ref]; ok {
		t.Fatalf("unexpected uncaptured root %#x", uint64(hiddenRegister.Ref))
	}
}

func TestScannerWalksStoreAndExternalRoots(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0x87654321)
	protos := rproto.NewStore(runtimeHeap)
	vm := state.NewVMState(runtimeHeap)
	if _, err := vm.NewThread(16, 2); err != nil {
		t.Fatalf("new thread: %v", err)
	}
	stringA := mustIntern(t, strings, "store-root-a")
	stringB := mustIntern(t, strings, "store-root-b")
	external := mustIntern(t, strings, "external-root")
	protoHandle, err := protos.Intern(&bytecode.Proto{Constants: []bytecode.Constant{bytecode.StringConstant("x")}})
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}

	scanner := NewScanner(runtimeHeap)
	visited := collectRoots(t, func(visit VisitFunc) error {
		return scanner.WalkRoots(vm, visit, StringTableRoots(strings), ProtoStoreRoots(protos), Values(external.Value))
	})

	for _, ref := range []value.HeapRef44{stringA.Ref, stringB.Ref, external.Ref, protoHandle.Ref} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing root %#x", uint64(ref))
		}
	}
	if len(visited) < 4 {
		t.Fatalf("visited %d roots, want at least 4", len(visited))
	}
}

func TestInterpreterActivationRootsFollowLiveTop(t *testing.T) {
	engine := interp.New()
	thread, err := engine.NewThread(32, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	liveRoot := mustIntern(t, engine.Strings, "activation-live-root")
	deadRoot := mustIntern(t, engine.Strings, "activation-dead-root")
	scanner := NewScanner(engine.Heap)
	visited := make(map[value.HeapRef44]struct{})
	var scanErr error
	hostFunc, err := engine.RegisterHostFunction("probe", func() {
		frame := thread.CurrentFrame()
		if frame == nil {
			scanErr = errString("missing current frame during host callback")
			return
		}
		if err := thread.SetRegister(frame, 2, deadRoot.Value); err != nil {
			scanErr = err
			return
		}
		scanErr = scanner.InterpreterActivationRoots(engine).WalkRoots(func(ref value.HeapRef44) error {
			visited[ref] = struct{}{}
			return nil
		})
	}, value.NilValue())
	if err != nil {
		t.Fatalf("register host function: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "probe", hostFunc.Value); err != nil {
		t.Fatalf("set global probe: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@activation-roots.lua",
		MaxStackSize: 3,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("probe"),
			bytecode.StringConstant("activation-live-root"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := engine.Call(thread, closureHandle.Value, nil, 0); err != nil {
		t.Fatalf("call closure: %v", err)
	}
	if scanErr != nil {
		t.Fatalf("scan activation roots: %v", scanErr)
	}
	for _, ref := range []value.HeapRef44{closureHandle.Ref, hostFunc.Ref, liveRoot.Ref} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing activation root %#x", uint64(ref))
		}
	}
	if _, ok := visited[deadRoot.Ref]; ok {
		t.Fatalf("unexpected activation root %#x", uint64(deadRoot.Ref))
	}
}

func TestScannerUsesCompiledSafepointLiveSlots(t *testing.T) {
	engine := interp.New()
	runtime := baseline.NewRuntime(engine)
	thread, err := engine.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@compiled-safepoint-live-slots.lua",
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("unused-0"),
			bytecode.StringConstant("unused-1"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABx(bytecode.OP_LOADK, 1, 1),
			bytecode.CreateABC(bytecode.OP_NEWTABLE, 2, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 3, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 3, 2, 0),
		},
	}
	protoHandle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	compiled, err := runtime.CompileRef(protoHandle.Ref)
	if err != nil {
		t.Fatalf("compile proto: %v", err)
	}
	liveSet, ok := compiled.Metadata.LiveSlotSetAtBytecode(2)
	if !ok {
		t.Fatalf("missing live slot set for bytecode 2")
	}
	if !liveSet.HasStaticRegister(0) || liveSet.HasStaticRegister(1) || liveSet.HasStaticRegister(2) {
		t.Fatalf("unexpected live slot set at pc 2: %+v", liveSet)
	}
	liveRoot := mustIntern(t, engine.Strings, "compiled-live-root")
	deadRoot1 := mustIntern(t, engine.Strings, "compiled-dead-root-1")
	deadRoot2 := mustIntern(t, engine.Strings, "compiled-dead-root-2")
	deadRoot3 := mustIntern(t, engine.Strings, "compiled-dead-root-3")
	deadSpill := mustIntern(t, engine.Strings, "compiled-dead-spill")
	frame, err := thread.PushFrame(state.FrameSpec{
		Closure:       closureHandle.Value,
		Proto:         protoHandle.Value,
		RegisterBase:  0,
		RegisterCount: 4,
		SpillCount:    1,
		Top:           4,
		SavedBCOff:    2,
		Flags:         state.FrameFlagCompiled,
	})
	if err != nil {
		t.Fatalf("push compiled frame: %v", err)
	}
	if err := thread.SetRegister(frame, 0, liveRoot.Value); err != nil {
		t.Fatalf("set live register: %v", err)
	}
	if err := thread.SetRegister(frame, 1, deadRoot1.Value); err != nil {
		t.Fatalf("set dead register 1: %v", err)
	}
	if err := thread.SetRegister(frame, 2, deadRoot2.Value); err != nil {
		t.Fatalf("set dead register 2: %v", err)
	}
	if err := thread.SetRegister(frame, 3, deadRoot3.Value); err != nil {
		t.Fatalf("set dead register 3: %v", err)
	}
	if err := thread.SetSpill(frame, 0, deadSpill.Value); err != nil {
		t.Fatalf("set dead spill: %v", err)
	}
	scanner := NewScanner(engine.Heap)
	scanner.BindCompiledRuntime(runtime)
	visited := collectRoots(t, func(visit VisitFunc) error {
		return scanner.WalkThread(thread, visit)
	})
	for _, ref := range []value.HeapRef44{closureHandle.Ref, protoHandle.Ref, liveRoot.Ref, deadSpill.Ref} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing compiled safepoint root %#x", uint64(ref))
		}
	}
	for _, ref := range []value.HeapRef44{deadRoot1.Ref, deadRoot2.Ref, deadRoot3.Ref} {
		if _, ok := visited[ref]; ok {
			t.Fatalf("unexpected compiled safepoint root %#x", uint64(ref))
		}
	}
}

func TestScannerUsesCompiledFrameSpillRoots(t *testing.T) {
	engine := interp.New()
	runtime := baseline.NewRuntime(engine)
	thread, err := engine.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@compiled-spill-root.lua",
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	protoHandle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	if _, err := runtime.CompileRef(protoHandle.Ref); err != nil {
		t.Fatalf("compile proto: %v", err)
	}
	spillRoot := mustIntern(t, engine.Strings, "compiled-spill-root")
	frame, err := thread.PushFrame(state.FrameSpec{
		Closure:       closureHandle.Value,
		Proto:         protoHandle.Value,
		RegisterBase:  0,
		RegisterCount: 1,
		SpillCount:    1,
		Top:           0,
		SavedBCOff:    0,
		Flags:         state.FrameFlagCompiled,
	})
	if err != nil {
		t.Fatalf("push compiled frame: %v", err)
	}
	if err := thread.SetSpill(frame, 0, spillRoot.Value); err != nil {
		t.Fatalf("set spill root: %v", err)
	}

	scanner := NewScanner(engine.Heap)
	scanner.BindCompiledRuntime(runtime)
	visited := collectRoots(t, func(visit VisitFunc) error {
		return scanner.WalkThread(thread, visit)
	})

	for _, ref := range []value.HeapRef44{closureHandle.Ref, protoHandle.Ref, spillRoot.Ref} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing compiled spill root %#x", uint64(ref))
		}
	}
}

func TestScannerUsesCompiledSafepointDynamicTopRange(t *testing.T) {
	engine := interp.New()
	runtime := baseline.NewRuntime(engine)
	thread, err := engine.NewThread(16, 4)
	if err != nil {
		t.Fatalf("new thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("new env: %v", err)
	}
	proto := &bytecode.Proto{
		Source:       "@compiled-safepoint-dynamic-top.lua",
		MaxStackSize: 4,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_CALL, 1, 0, 1),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	protoHandle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	closureHandle, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	compiled, err := runtime.CompileRef(protoHandle.Ref)
	if err != nil {
		t.Fatalf("compile proto: %v", err)
	}
	liveSet, ok := compiled.Metadata.LiveSlotSetAtBytecode(0)
	if !ok {
		t.Fatalf("missing live slot set for bytecode 0")
	}
	if !liveSet.HasStaticRegister(1) || !liveSet.HasDynamicRange() || liveSet.DynamicTopStart != 2 {
		t.Fatalf("unexpected dynamic live slot set at pc 0: %+v", liveSet)
	}
	deadRoot := mustIntern(t, engine.Strings, "compiled-dynamic-dead-root")
	calleeRoot := mustIntern(t, engine.Strings, "compiled-dynamic-callee-root")
	argRoot1 := mustIntern(t, engine.Strings, "compiled-dynamic-arg-root-1")
	argRoot2 := mustIntern(t, engine.Strings, "compiled-dynamic-arg-root-2")
	frame, err := thread.PushFrame(state.FrameSpec{
		Closure:       closureHandle.Value,
		Proto:         protoHandle.Value,
		RegisterBase:  0,
		RegisterCount: 4,
		Top:           4,
		SavedBCOff:    0,
		Flags:         state.FrameFlagCompiled,
	})
	if err != nil {
		t.Fatalf("push compiled frame: %v", err)
	}
	if err := thread.SetRegister(frame, 0, deadRoot.Value); err != nil {
		t.Fatalf("set dead register: %v", err)
	}
	if err := thread.SetRegister(frame, 1, calleeRoot.Value); err != nil {
		t.Fatalf("set callee register: %v", err)
	}
	if err := thread.SetRegister(frame, 2, argRoot1.Value); err != nil {
		t.Fatalf("set arg register 1: %v", err)
	}
	if err := thread.SetRegister(frame, 3, argRoot2.Value); err != nil {
		t.Fatalf("set arg register 2: %v", err)
	}
	scanner := NewScanner(engine.Heap)
	scanner.BindCompiledRuntime(runtime)
	visited := collectRoots(t, func(visit VisitFunc) error {
		return scanner.WalkThread(thread, visit)
	})
	for _, ref := range []value.HeapRef44{closureHandle.Ref, protoHandle.Ref, calleeRoot.Ref, argRoot1.Ref, argRoot2.Ref} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing dynamic-top safepoint root %#x", uint64(ref))
		}
	}
	if _, ok := visited[deadRoot.Ref]; ok {
		t.Fatalf("unexpected dynamic-top safepoint root %#x", uint64(deadRoot.Ref))
	}
}

func TestCompiledMetadataRootsVisitCompiledProtoRefs(t *testing.T) {
	engine := interp.New()
	runtime := baseline.NewRuntime(engine)
	proto := &bytecode.Proto{
		Source:       "@compiled-roots.lua",
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	handle, err := engine.Protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	compiled, err := runtime.CompileRef(handle.Ref)
	if err != nil {
		t.Fatalf("compile proto: %v", err)
	}
	extra, err := engine.Strings.Intern("compiled-root-extra")
	if err != nil {
		t.Fatalf("intern extra metadata root: %v", err)
	}
	compiled.Metadata.AddHeapRef(extra.Ref)
	visited := collectRoots(t, func(visit VisitFunc) error {
		return CompiledMetadataRoots(runtime).WalkRoots(visit)
	})
	for _, ref := range []value.HeapRef44{handle.Ref, extra.Ref} {
		if _, ok := visited[ref]; !ok {
			t.Fatalf("missing compiled metadata root %#x", uint64(ref))
		}
	}
	if compiled.ProtoRef != handle.Ref {
		t.Fatalf("compiled proto ref %#x, want %#x", uint64(compiled.ProtoRef), uint64(handle.Ref))
	}
}

func collectRoots(t *testing.T, walk func(VisitFunc) error) map[value.HeapRef44]struct{} {
	t.Helper()
	visited := make(map[value.HeapRef44]struct{})
	if err := walk(func(ref value.HeapRef44) error {
		visited[ref] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk roots: %v", err)
	}
	return visited
}

func mustIntern(t *testing.T, strings *rtstring.InternTable, text string) rtstring.Handle {
	t.Helper()
	handle, err := strings.Intern(text)
	if err != nil {
		t.Fatalf("intern %q: %v", text, err)
	}
	return handle
}

type errString string

func (err errString) Error() string {
	return string(err)
}

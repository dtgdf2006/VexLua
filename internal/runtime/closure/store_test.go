package closure

import (
	"encoding/binary"
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/heap"
	rproto "vexlua/internal/runtime/proto"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/upvalue"
	"vexlua/internal/runtime/value"
)

func TestClosureProtoBridgeAndUpvalueLifecycle(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	vm := state.NewVMState(runtimeHeap)
	thread, err := vm.NewThread(16, 4)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	protos := rproto.NewStore(runtimeHeap)
	closures := NewStore(runtimeHeap, protos)
	upvalues := upvalue.NewManager(runtimeHeap)

	slot, err := thread.SlotAddress(4)
	if err != nil {
		t.Fatalf("get slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(slot, value.NumberValue(123)); err != nil {
		t.Fatalf("seed stack slot: %v", err)
	}
	open, err := upvalues.FindOrCreateOpen(thread, slot)
	if err != nil {
		t.Fatalf("open upvalue: %v", err)
	}

	proto := &bytecode.Proto{
		Source:       "closure-test",
		NumUpvalues:  1,
		MaxStackSize: 2,
	}
	closureHandle, err := closures.NewLuaClosure(proto, value.NilValue(), []value.HeapRef44{open.Ref})
	if err != nil {
		t.Fatalf("create closure: %v", err)
	}
	resolvedProto, err := closures.Proto(closureHandle.Ref)
	if err != nil {
		t.Fatalf("resolve closure proto: %v", err)
	}
	if resolvedProto != proto {
		t.Fatalf("expected closure to resolve original proto pointer")
	}
	refs, err := closures.UpvalueRefs(closureHandle.Ref)
	if err != nil {
		t.Fatalf("read closure upvalues: %v", err)
	}
	if len(refs) != 1 || refs[0] != open.Ref {
		t.Fatalf("unexpected closure upvalue refs: %#v", refs)
	}

	current, err := upvalues.Get(open.Ref)
	if err != nil {
		t.Fatalf("read open upvalue: %v", err)
	}
	number, _ := current.Float64()
	if number != 123 {
		t.Fatalf("unexpected open upvalue value %g", number)
	}

	if err := thread.SetValueAtAddress(slot, value.NumberValue(456)); err != nil {
		t.Fatalf("mutate open slot: %v", err)
	}
	current, err = upvalues.Get(open.Ref)
	if err != nil {
		t.Fatalf("read updated open upvalue: %v", err)
	}
	number, _ = current.Float64()
	if number != 456 {
		t.Fatalf("unexpected updated open upvalue value %g", number)
	}

	closed, err := upvalues.CloseAtOrAbove(thread, slot)
	if err != nil {
		t.Fatalf("close upvalue: %v", err)
	}
	if len(closed) != 1 || closed[0].Ref != open.Ref {
		t.Fatalf("unexpected closed upvalue set")
	}
	if err := thread.SetValueAtAddress(slot, value.NumberValue(789)); err != nil {
		t.Fatalf("mutate stack after close: %v", err)
	}
	current, err = upvalues.Get(open.Ref)
	if err != nil {
		t.Fatalf("read closed upvalue: %v", err)
	}
	number, _ = current.Float64()
	if number != 456 {
		t.Fatalf("closed upvalue should retain captured value, got %g", number)
	}

	if err := upvalues.Set(open.Ref, value.BoolValue(true)); err != nil {
		t.Fatalf("write closed upvalue: %v", err)
	}
	current, err = upvalues.Get(open.Ref)
	if err != nil {
		t.Fatalf("read overwritten closed upvalue: %v", err)
	}
	boolean, ok := current.Bool()
	if !ok || !boolean {
		t.Fatalf("unexpected closed upvalue value %s", current)
	}
	stackValue, err := thread.ValueAtAddress(slot)
	if err != nil {
		t.Fatalf("read stack after closed write: %v", err)
	}
	number, _ = stackValue.Float64()
	if number != 789 {
		t.Fatalf("closed upvalue write should not mutate stack, got %g", number)
	}
}

func TestClosureExposesNativeEnvProtoAndUpvalueVector(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	vm := state.NewVMState(runtimeHeap)
	thread, err := vm.NewThread(16, 4)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	protos := rproto.NewStore(runtimeHeap)
	closures := NewStore(runtimeHeap, protos)
	upvalues := upvalue.NewManager(runtimeHeap)
	firstSlot, err := thread.SlotAddress(2)
	if err != nil {
		t.Fatalf("first slot address: %v", err)
	}
	secondSlot, err := thread.SlotAddress(3)
	if err != nil {
		t.Fatalf("second slot address: %v", err)
	}
	if err := thread.SetValueAtAddress(firstSlot, value.NumberValue(7)); err != nil {
		t.Fatalf("seed first slot: %v", err)
	}
	if err := thread.SetValueAtAddress(secondSlot, value.NumberValue(9)); err != nil {
		t.Fatalf("seed second slot: %v", err)
	}
	firstUpvalue, err := upvalues.FindOrCreateOpen(thread, firstSlot)
	if err != nil {
		t.Fatalf("open first upvalue: %v", err)
	}
	secondUpvalue, err := upvalues.FindOrCreateOpen(thread, secondSlot)
	if err != nil {
		t.Fatalf("open second upvalue: %v", err)
	}
	proto := &bytecode.Proto{Source: "native-closure", NumUpvalues: 2, MaxStackSize: 2}
	protoHandle, err := protos.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	closureHandle, err := closures.NewLuaClosure(proto, value.BoolValue(true), []value.HeapRef44{firstUpvalue.Ref, secondUpvalue.Ref})
	if err != nil {
		t.Fatalf("create closure: %v", err)
	}
	protoRef, err := closures.ProtoRef(closureHandle.Ref)
	if err != nil {
		t.Fatalf("read closure proto ref: %v", err)
	}
	if protoRef != protoHandle.Ref {
		t.Fatalf("closure proto ref = %#x, want %#x", uint64(protoRef), uint64(protoHandle.Ref))
	}
	env, err := closures.Env(closureHandle.Ref)
	if err != nil {
		t.Fatalf("read closure env: %v", err)
	}
	if boolean, ok := env.Bool(); !ok || !boolean {
		t.Fatalf("closure env = %s, want bool(true)", env)
	}
	base, err := closures.UpvalueBase(closureHandle.Ref)
	if err != nil {
		t.Fatalf("read upvalue base: %v", err)
	}
	object, err := closures.Object(closureHandle.Ref)
	if err != nil {
		t.Fatalf("read closure object: %v", err)
	}
	expectedBase, err := runtimeHeap.NativeAddressForOffset(object.UpvaluesData)
	if err != nil {
		t.Fatalf("resolve upvalue vector address: %v", err)
	}
	if base != expectedBase {
		t.Fatalf("upvalue base %#x, want %#x", base, expectedBase)
	}
	ref0, err := closures.UpvalueRefAt(closureHandle.Ref, 0)
	if err != nil {
		t.Fatalf("read upvalue 0: %v", err)
	}
	ref1, err := closures.UpvalueRefAt(closureHandle.Ref, 1)
	if err != nil {
		t.Fatalf("read upvalue 1: %v", err)
	}
	if ref0 != firstUpvalue.Ref || ref1 != secondUpvalue.Ref {
		t.Fatalf("unexpected upvalue refs: %#x %#x", uint64(ref0), uint64(ref1))
	}
	address, err := runtimeHeap.DecodeHeapRef(closureHandle.Ref)
	if err != nil {
		t.Fatalf("decode closure ref: %v", err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("closure offset: %v", err)
	}
	bytes, err := runtimeHeap.Resolve(offset, ObjectSize)
	if err != nil {
		t.Fatalf("resolve closure bytes: %v", err)
	}
	if got := value.Raw(binary.LittleEndian.Uint64(bytes[ProtoOffset : ProtoOffset+8])); got != protoHandle.Value.Bits() {
		t.Fatalf("closure proto bits = %#x, want %#x", uint64(got), uint64(protoHandle.Value.Bits()))
	}
	if got := value.Raw(binary.LittleEndian.Uint64(bytes[EnvOffset : EnvOffset+8])); got != env.Bits() {
		t.Fatalf("closure env bits = %#x, want %#x", uint64(got), uint64(env.Bits()))
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(bytes[UpvaluesDataOffset : UpvaluesDataOffset+8])); got != object.UpvaluesData {
		t.Fatalf("closure upvalue data = %#x, want %#x", uint64(got), uint64(object.UpvaluesData))
	}
}

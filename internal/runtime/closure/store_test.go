package closure

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/feedback"
	"vexlua/internal/runtime/heap"
	rproto "vexlua/internal/runtime/proto"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/upvalue"
	"vexlua/internal/runtime/value"
)

func TestClosureObjectLayoutContract(t *testing.T) {
	object := NewObject(value.BoolValue(true), value.NumberValue(3), 2, value.HeapOff64(0x1122334455667788))
	object.Flags = 0x55AA
	object.FeedbackData = value.HeapOff64(0x8877665544332211)
	object.FeedbackSize = 7
	buffer := make([]byte, ObjectSize)
	if err := WriteObject(buffer, object); err != nil {
		t.Fatalf("write closure object: %v", err)
	}
	if got := value.Raw(binary.LittleEndian.Uint64(buffer[ProtoOffset : ProtoOffset+8])); got != object.Proto.Bits() {
		t.Fatalf("proto bits = %#x, want %#x", uint64(got), uint64(object.Proto.Bits()))
	}
	if got := value.Raw(binary.LittleEndian.Uint64(buffer[EnvOffset : EnvOffset+8])); got != object.Env.Bits() {
		t.Fatalf("env bits = %#x, want %#x", uint64(got), uint64(object.Env.Bits()))
	}
	if got := binary.LittleEndian.Uint16(buffer[UpvalueCountOffset : UpvalueCountOffset+2]); got != object.UpvalueCount {
		t.Fatalf("upvalue count = %d, want %d", got, object.UpvalueCount)
	}
	if got := binary.LittleEndian.Uint16(buffer[FlagsOffset : FlagsOffset+2]); got != object.Flags {
		t.Fatalf("flags = %#x, want %#x", got, object.Flags)
	}
	if got := binary.LittleEndian.Uint32(buffer[Reserved0Offset : Reserved0Offset+4]); got != 0 {
		t.Fatalf("reserved0 = %#x, want 0", got)
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[UpvaluesDataOffset : UpvaluesDataOffset+8])); got != object.UpvaluesData {
		t.Fatalf("upvalues data = %#x, want %#x", uint64(got), uint64(object.UpvaluesData))
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[FeedbackVectorOff : FeedbackVectorOff+8])); got != object.FeedbackData {
		t.Fatalf("feedback data = %#x, want %#x", uint64(got), uint64(object.FeedbackData))
	}
	if got := binary.LittleEndian.Uint32(buffer[FeedbackSlotsOff : FeedbackSlotsOff+4]); got != object.FeedbackSize {
		t.Fatalf("feedback slots = %d, want %d", got, object.FeedbackSize)
	}
	if got := binary.LittleEndian.Uint32(buffer[Reserved2Offset : Reserved2Offset+4]); got != 0 {
		t.Fatalf("reserved2 = %#x, want 0", got)
	}
}

func TestClosureProtoBridgeAndUpvalueLifecycle(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	vm := state.NewVMState(runtimeHeap)
	thread, err := vm.NewThread(16, 4)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	protos := rproto.NewStore(runtimeHeap)
	closures := NewStore(runtimeHeap, protos)
	upvalues := upvalue.NewManager(runtimeHeap, vm)

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
	upvalues := upvalue.NewManager(runtimeHeap, vm)
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
	nativeAddress, err := runtimeHeap.NativeAddressForOffset(offset)
	if err != nil {
		t.Fatalf("resolve closure native address: %v", err)
	}
	if uintptr(unsafe.Pointer(&bytes[0])) != nativeAddress {
		t.Fatalf("closure object bytes base %#x, want %#x", uintptr(unsafe.Pointer(&bytes[0])), nativeAddress)
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
	upvalueBytes, err := runtimeHeap.Resolve(object.UpvaluesData, uint64(object.UpvalueCount)*8)
	if err != nil {
		t.Fatalf("resolve canonical upvalue vector: %v", err)
	}
	nativeUpvalueAddress, err := runtimeHeap.NativeAddressForOffset(object.UpvaluesData)
	if err != nil {
		t.Fatalf("resolve upvalue vector native address: %v", err)
	}
	if uintptr(unsafe.Pointer(&upvalueBytes[0])) != nativeUpvalueAddress {
		t.Fatalf("closure upvalue vector bytes base %#x, want %#x", uintptr(unsafe.Pointer(&upvalueBytes[0])), nativeUpvalueAddress)
	}
}

func TestClosureFeedbackVectorUsesNativeHeapLayout(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	protos := rproto.NewStore(runtimeHeap)
	closures := NewStore(runtimeHeap, protos)
	proto := &bytecode.Proto{
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("g"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	env := value.NilValue()
	closureHandle, err := closures.NewLuaClosure(proto, env, nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	layout := feedback.LayoutForProto(proto)
	base, err := closures.EnsureFeedbackVector(closureHandle.Ref, layout)
	if err != nil {
		t.Fatalf("ensure feedback vector: %v", err)
	}
	if base == 0 {
		t.Fatalf("feedback vector base should not be zero")
	}
	object, err := closures.Object(closureHandle.Ref)
	if err != nil {
		t.Fatalf("read closure object: %v", err)
	}
	if object.FeedbackData == 0 || object.FeedbackSize != layout.SlotCount() {
		t.Fatalf("unexpected closure feedback metadata: offset=%#x slots=%d", uint64(object.FeedbackData), object.FeedbackSize)
	}
	expectedBase, err := runtimeHeap.NativeAddressForOffset(object.FeedbackData)
	if err != nil {
		t.Fatalf("resolve feedback base: %v", err)
	}
	if base != expectedBase {
		t.Fatalf("feedback base %#x, want %#x", base, expectedBase)
	}
	header, err := closures.ReadFeedbackHeader(closureHandle.Ref)
	if err != nil {
		t.Fatalf("read feedback header: %v", err)
	}
	if header.SlotCount != layout.SlotCount() {
		t.Fatalf("feedback slot count = %d, want %d", header.SlotCount, layout.SlotCount())
	}
	cell, err := closures.ReadFeedbackCell(closureHandle.Ref, 0)
	if err != nil {
		t.Fatalf("read feedback cell: %v", err)
	}
	if cell.State != feedback.StateGeneric || cell.SlotKind != feedback.SlotGetGlobal {
		t.Fatalf("unexpected initial feedback cell: %+v", cell)
	}
	feedbackBytes, err := runtimeHeap.Resolve(object.FeedbackData, feedback.VectorSize(layout.SlotCount()))
	if err != nil {
		t.Fatalf("resolve canonical feedback vector: %v", err)
	}
	nativeFeedbackAddress, err := runtimeHeap.NativeAddressForOffset(object.FeedbackData)
	if err != nil {
		t.Fatalf("resolve feedback native address: %v", err)
	}
	if uintptr(unsafe.Pointer(&feedbackBytes[0])) != nativeFeedbackAddress {
		t.Fatalf("feedback vector bytes base %#x, want %#x", uintptr(unsafe.Pointer(&feedbackBytes[0])), nativeFeedbackAddress)
	}
	updated := feedback.NewMegamorphicCell(feedback.SlotGetGlobal)
	if err := closures.WriteFeedbackCell(closureHandle.Ref, 0, updated); err != nil {
		t.Fatalf("write feedback cell: %v", err)
	}
	cellBytes, err := runtimeHeap.Resolve(object.FeedbackData+value.HeapOff64(feedback.CellOffset(0)), feedback.CellSize)
	if err != nil {
		t.Fatalf("resolve feedback cell: %v", err)
	}
	decodedCell, err := feedback.ReadCell(cellBytes)
	if err != nil {
		t.Fatalf("decode feedback cell: %v", err)
	}
	if decodedCell != updated {
		t.Fatalf("feedback cell = %+v, want %+v", decodedCell, updated)
	}
}

func TestClosureFeedbackHeaderNativeStateStaysCanonical(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	protos := rproto.NewStore(runtimeHeap)
	closures := NewStore(runtimeHeap, protos)
	proto := &bytecode.Proto{
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("g"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	closureHandle, err := closures.NewLuaClosure(proto, value.NilValue(), nil)
	if err != nil {
		t.Fatalf("new closure: %v", err)
	}
	layout := feedback.LayoutForProto(proto)
	base, err := closures.EnsureFeedbackVector(closureHandle.Ref, layout)
	if err != nil {
		t.Fatalf("ensure feedback vector: %v", err)
	}
	object, err := closures.Object(closureHandle.Ref)
	if err != nil {
		t.Fatalf("read closure object: %v", err)
	}
	offset, err := runtimeHeap.OffsetForNativeAddress(base)
	if err != nil {
		t.Fatalf("feedback vector offset: %v", err)
	}
	if offset != object.FeedbackData {
		t.Fatalf("feedback offset %#x, want %#x", uint64(offset), uint64(object.FeedbackData))
	}
	updated := feedback.Header{SlotCount: layout.SlotCount(), InterruptBudget: 64, LoopHotness: 5, OSRState: 3, Flags: 0xA5}
	if err := closures.WriteFeedbackHeader(closureHandle.Ref, updated); err != nil {
		t.Fatalf("write feedback header: %v", err)
	}
	decoded, err := closures.ReadFeedbackHeader(closureHandle.Ref)
	if err != nil {
		t.Fatalf("read feedback header: %v", err)
	}
	if decoded != updated {
		t.Fatalf("decoded header = %+v, want %+v", decoded, updated)
	}
	bytes, err := runtimeHeap.Resolve(object.FeedbackData, feedback.HeaderSize)
	if err != nil {
		t.Fatalf("resolve native feedback header: %v", err)
	}
	nativeUpdated := feedback.Header{SlotCount: layout.SlotCount(), InterruptBudget: -7, LoopHotness: 11, OSRState: 9, Flags: 0x5A}
	if err := feedback.WriteHeader(bytes, nativeUpdated); err != nil {
		t.Fatalf("write canonical feedback bytes: %v", err)
	}
	decoded, err = closures.ReadFeedbackHeader(closureHandle.Ref)
	if err != nil {
		t.Fatalf("read mutated feedback header: %v", err)
	}
	if decoded != nativeUpdated {
		t.Fatalf("mutated header = %+v, want %+v", decoded, nativeUpdated)
	}
}

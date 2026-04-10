package proto

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/heap"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
)

func TestStoreBuildsPinnedNativeConstantBlock(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0x9E3779B9)
	store := NewStore(runtimeHeap)
	proto := &bytecode.Proto{
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(42),
			bytecode.StringConstant("hello"),
			bytecode.BooleanConstant(true),
		},
	}
	base, err := store.ConstantBase(proto, strings)
	if err != nil {
		t.Fatalf("constant base: %v", err)
	}
	if base == 0 {
		t.Fatalf("constant base should not be zero")
	}
	baseAgain, err := store.ConstantBase(proto, strings)
	if err != nil {
		t.Fatalf("constant base second lookup: %v", err)
	}
	if baseAgain != base {
		t.Fatalf("constant base should be stable, got %#x then %#x", base, baseAgain)
	}
	first, err := store.ConstantValue(proto, 0, strings)
	if err != nil {
		t.Fatalf("constant 0: %v", err)
	}
	if first.Bits() != value.NumberValue(42).Bits() {
		t.Fatalf("constant 0 = %s, want %s", first, value.NumberValue(42))
	}
	second, err := store.ConstantValue(proto, 1, strings)
	if err != nil {
		t.Fatalf("constant 1: %v", err)
	}
	if uintptr(unsafe.Pointer(&store.constByProto[proto].values[0])) != base {
		t.Fatalf("native constant base %#x does not match backing slice %#x", base, uintptr(unsafe.Pointer(&store.constByProto[proto].values[0])))
	}
	if !second.IsBoxedTag(value.TagStringRef) {
		t.Fatalf("constant 1 should be string ref, got %s", second)
	}
	third, err := store.ConstantValue(proto, 2, strings)
	if err != nil {
		t.Fatalf("constant 2: %v", err)
	}
	if boolean, ok := third.Bool(); !ok || !boolean {
		t.Fatalf("constant 2 should be true, got %s", third)
	}
}

func TestConstantBaseIsContiguousNativeTValueArray(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0x12345678)
	store := NewStore(runtimeHeap)
	proto := &bytecode.Proto{
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
			bytecode.StringConstant("two"),
			bytecode.BooleanConstant(false),
			bytecode.NumberConstant(4),
		},
	}
	base, err := store.ConstantBase(proto, strings)
	if err != nil {
		t.Fatalf("constant base: %v", err)
	}
	block := store.constByProto[proto]
	if block == nil {
		t.Fatalf("expected native constant block to be allocated")
	}
	if base%value.TValueSize != 0 {
		t.Fatalf("constant base %#x is not %d-byte aligned", base, value.TValueSize)
	}
	for index, want := range block.values {
		address := uintptr(unsafe.Pointer(&block.values[index]))
		expectedAddress := base + uintptr(index)*value.TValueSize
		if address != expectedAddress {
			t.Fatalf("constant %d address %#x, want %#x", index, address, expectedAddress)
		}
		got, err := store.ConstantValue(proto, index, strings)
		if err != nil {
			t.Fatalf("constant %d lookup: %v", index, err)
		}
		if got.Bits() != want.Bits() {
			t.Fatalf("constant %d bits %#x, want %#x", index, uint64(got.Bits()), uint64(want.Bits()))
		}
	}
}

func TestStoreSyncsNativeConstBaseAndCompiledEntry(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0x87654321)
	store := NewStore(runtimeHeap)
	proto := &bytecode.Proto{
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(42),
		},
	}
	handle, err := store.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	base, err := store.ConstantBase(proto, strings)
	if err != nil {
		t.Fatalf("constant base: %v", err)
	}
	if err := store.SyncCompiledMetadata(handle.Ref, 0x1122334455667788, ProtoCompiledFlagNoSuspend); err != nil {
		t.Fatalf("set compiled metadata: %v", err)
	}
	object, err := store.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read proto object: %v", err)
	}
	if uintptr(object.ConstBasePtr) != base {
		t.Fatalf("native const base %#x, want %#x", uintptr(object.ConstBasePtr), base)
	}
	if object.CompiledEntry != 0x1122334455667788 {
		t.Fatalf("compiled entry %#x, want %#x", object.CompiledEntry, uint64(0x1122334455667788))
	}
	if object.CompiledFlags != ProtoCompiledFlagNoSuspend {
		t.Fatalf("compiled flags %#x, want %#x", object.CompiledFlags, ProtoCompiledFlagNoSuspend)
	}
	storedBase, err := store.NativeConstBase(handle.Ref)
	if err != nil {
		t.Fatalf("native const base lookup: %v", err)
	}
	if storedBase != base {
		t.Fatalf("native const base lookup %#x, want %#x", storedBase, base)
	}
	entry, err := store.CompiledEntry(handle.Ref)
	if err != nil {
		t.Fatalf("compiled entry lookup: %v", err)
	}
	if entry != uintptr(0x1122334455667788) {
		t.Fatalf("compiled entry lookup %#x, want %#x", entry, uintptr(0x1122334455667788))
	}
	flags, err := store.CompiledFlags(handle.Ref)
	if err != nil {
		t.Fatalf("compiled flags lookup: %v", err)
	}
	if flags != ProtoCompiledFlagNoSuspend {
		t.Fatalf("compiled flags lookup %#x, want %#x", flags, ProtoCompiledFlagNoSuspend)
	}
}

func TestStoreBuildsNativeProtoPayload(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	store := NewStore(runtimeHeap)
	child := &bytecode.Proto{
		Source:       "child",
		NumUpvalues:  2,
		MaxStackSize: 2,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
		LineInfo: []int{21},
	}
	parent := &bytecode.Proto{
		Source:       "parent",
		NumUpvalues:  1,
		MaxStackSize: 3,
		Protos:       []*bytecode.Proto{child},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_CLOSURE, 0, 0),
			bytecode.CreateABC(bytecode.OP_MOVE, 0, 1, 0),
			bytecode.CreateABC(bytecode.OP_GETUPVAL, 0, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
		LineInfo: []int{10, 11, 12, 13},
	}
	handle, err := store.Intern(parent)
	if err != nil {
		t.Fatalf("intern parent proto: %v", err)
	}
	childHandle, err := store.Intern(child)
	if err != nil {
		t.Fatalf("intern child proto: %v", err)
	}
	object, err := store.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read proto object: %v", err)
	}
	if object.CodeData == 0 || object.ChildProtoData == 0 || object.ClosureSiteData == 0 || object.LineInfoData == 0 {
		t.Fatalf("expected proto native payload offsets, got code=%#x child=%#x closure=%#x line=%#x", uint64(object.CodeData), uint64(object.ChildProtoData), uint64(object.ClosureSiteData), uint64(object.LineInfoData))
	}
	if object.ClosureSiteCount != 1 {
		t.Fatalf("closure site count = %d, want 1", object.ClosureSiteCount)
	}
	address, err := runtimeHeap.DecodeHeapRef(handle.Ref)
	if err != nil {
		t.Fatalf("decode proto ref: %v", err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("proto offset: %v", err)
	}
	bytes, err := runtimeHeap.Resolve(offset, ObjectSize)
	if err != nil {
		t.Fatalf("resolve proto bytes: %v", err)
	}
	if got := binary.LittleEndian.Uint32(bytes[ClosureSiteCountOff : ClosureSiteCountOff+4]); got != object.ClosureSiteCount {
		t.Fatalf("closure site count bytes = %d, want %d", got, object.ClosureSiteCount)
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(bytes[CodeDataOff : CodeDataOff+8])); got != object.CodeData {
		t.Fatalf("code data offset = %#x, want %#x", uint64(got), uint64(object.CodeData))
	}
	instructions, err := store.Instructions(handle.Ref)
	if err != nil {
		t.Fatalf("read native instructions: %v", err)
	}
	if len(instructions) != len(parent.Code) {
		t.Fatalf("instruction count = %d, want %d", len(instructions), len(parent.Code))
	}
	for index, instruction := range parent.Code {
		if instructions[index] != instruction {
			t.Fatalf("instruction %d = %#x, want %#x", index, uint32(instructions[index]), uint32(instruction))
		}
	}
	childRefs, err := store.ChildProtoRefs(handle.Ref)
	if err != nil {
		t.Fatalf("read child proto refs: %v", err)
	}
	if len(childRefs) != 1 || childRefs[0] != childHandle.Ref {
		t.Fatalf("unexpected child proto refs: %#v", childRefs)
	}
	lines, err := store.LineInfo(handle.Ref)
	if err != nil {
		t.Fatalf("read line info: %v", err)
	}
	if len(lines) != len(parent.LineInfo) || lines[0] != 10 || lines[3] != 13 {
		t.Fatalf("unexpected native line info: %#v", lines)
	}
	site, captures, found, err := store.ClosureSite(handle.Ref, 0)
	if err != nil {
		t.Fatalf("read closure site: %v", err)
	}
	if !found {
		t.Fatalf("expected closure site at pc 0")
	}
	if site.ChildProtoIndex != 0 || site.UpvalueCount != 2 {
		t.Fatalf("unexpected closure site: %+v", site)
	}
	if len(captures) != 2 {
		t.Fatalf("capture count = %d, want 2", len(captures))
	}
	if captures[0].Kind != CaptureLocal || captures[0].Index != 1 {
		t.Fatalf("capture 0 = %+v, want local 1", captures[0])
	}
	if captures[1].Kind != CaptureUpvalue || captures[1].Index != 0 {
		t.Fatalf("capture 1 = %+v, want upvalue 0", captures[1])
	}
}

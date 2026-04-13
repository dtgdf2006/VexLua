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

func TestProtoObjectLayoutContract(t *testing.T) {
	object := Object{
		Common:           value.CommonHeader{Kind: value.KindProto, SizeBytes: ObjectSize, Version: 1},
		InstructionCount: 9,
		ConstantCount:    4,
		ProtoCount:       2,
		UpvalueCount:     3,
		MaxStackSize:     8,
		NumParams:        2,
		VarargFlags:      1,
		CompiledFlags:    ProtoCompiledFlagNoSuspend,
		SourceHash:       0x11223344,
		LineDefined:      12,
		LastLineDefined:  34,
		ClosureSiteCount: 5,
		CodeData:         value.HeapOff64(0x1000),
		ChildProtoData:   value.HeapOff64(0x2000),
		ClosureSiteData:  value.HeapOff64(0x3000),
		LineInfoData:     value.HeapOff64(0x4000),
		ConstBasePtr:     0x5566778899AABBCC,
		CompiledEntry:    0xCCDDEEFF00112233,
		LocVarCount:      7,
		UpvalueNameCount: 3,
		LocVarData:       value.HeapOff64(0x5000),
		UpvalueNameData:  value.HeapOff64(0x6000),
	}
	buffer := make([]byte, ObjectSize)
	if err := WriteObject(buffer, object); err != nil {
		t.Fatalf("write proto object: %v", err)
	}
	if got := binary.LittleEndian.Uint32(buffer[InstructionCountOff : InstructionCountOff+4]); got != object.InstructionCount {
		t.Fatalf("instruction count = %d, want %d", got, object.InstructionCount)
	}
	if got := binary.LittleEndian.Uint32(buffer[ConstantCountOff : ConstantCountOff+4]); got != object.ConstantCount {
		t.Fatalf("constant count = %d, want %d", got, object.ConstantCount)
	}
	if got := binary.LittleEndian.Uint16(buffer[ProtoCountOff : ProtoCountOff+2]); got != object.ProtoCount {
		t.Fatalf("proto count = %d, want %d", got, object.ProtoCount)
	}
	if got := binary.LittleEndian.Uint16(buffer[UpvalueCountOff : UpvalueCountOff+2]); got != object.UpvalueCount {
		t.Fatalf("upvalue count = %d, want %d", got, object.UpvalueCount)
	}
	if got := buffer[MaxStackSizeOff]; got != object.MaxStackSize {
		t.Fatalf("max stack size = %d, want %d", got, object.MaxStackSize)
	}
	if got := buffer[NumParamsOff]; got != object.NumParams {
		t.Fatalf("num params = %d, want %d", got, object.NumParams)
	}
	if got := buffer[VarargFlagsOff]; got != object.VarargFlags {
		t.Fatalf("vararg flags = %#x, want %#x", got, object.VarargFlags)
	}
	if got := buffer[CompiledFlagsOff]; got != object.CompiledFlags {
		t.Fatalf("compiled flags = %#x, want %#x", got, object.CompiledFlags)
	}
	if got := binary.LittleEndian.Uint32(buffer[SourceHashOff : SourceHashOff+4]); got != object.SourceHash {
		t.Fatalf("source hash = %#x, want %#x", got, object.SourceHash)
	}
	if got := binary.LittleEndian.Uint32(buffer[LineDefinedOff : LineDefinedOff+4]); got != object.LineDefined {
		t.Fatalf("line defined = %d, want %d", got, object.LineDefined)
	}
	if got := binary.LittleEndian.Uint32(buffer[LastLineDefinedOff : LastLineDefinedOff+4]); got != object.LastLineDefined {
		t.Fatalf("last line defined = %d, want %d", got, object.LastLineDefined)
	}
	if got := binary.LittleEndian.Uint32(buffer[ClosureSiteCountOff : ClosureSiteCountOff+4]); got != object.ClosureSiteCount {
		t.Fatalf("closure site count = %d, want %d", got, object.ClosureSiteCount)
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[CodeDataOff : CodeDataOff+8])); got != object.CodeData {
		t.Fatalf("code data = %#x, want %#x", uint64(got), uint64(object.CodeData))
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[ChildProtoDataOff : ChildProtoDataOff+8])); got != object.ChildProtoData {
		t.Fatalf("child proto data = %#x, want %#x", uint64(got), uint64(object.ChildProtoData))
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[ClosureSiteDataOff : ClosureSiteDataOff+8])); got != object.ClosureSiteData {
		t.Fatalf("closure site data = %#x, want %#x", uint64(got), uint64(object.ClosureSiteData))
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[LineInfoDataOff : LineInfoDataOff+8])); got != object.LineInfoData {
		t.Fatalf("line info data = %#x, want %#x", uint64(got), uint64(object.LineInfoData))
	}
	if got := binary.LittleEndian.Uint64(buffer[ConstBasePtrOff : ConstBasePtrOff+8]); got != object.ConstBasePtr {
		t.Fatalf("const base ptr = %#x, want %#x", got, object.ConstBasePtr)
	}
	if got := binary.LittleEndian.Uint64(buffer[CompiledEntryOff : CompiledEntryOff+8]); got != object.CompiledEntry {
		t.Fatalf("compiled entry = %#x, want %#x", got, object.CompiledEntry)
	}
	if got := binary.LittleEndian.Uint32(buffer[LocVarCountOff : LocVarCountOff+4]); got != object.LocVarCount {
		t.Fatalf("locvar count = %d, want %d", got, object.LocVarCount)
	}
	if got := binary.LittleEndian.Uint32(buffer[UpvalueNameCountOff : UpvalueNameCountOff+4]); got != object.UpvalueNameCount {
		t.Fatalf("upvalue name count = %d, want %d", got, object.UpvalueNameCount)
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[LocVarDataOff : LocVarDataOff+8])); got != object.LocVarData {
		t.Fatalf("locvar data = %#x, want %#x", uint64(got), uint64(object.LocVarData))
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[UpvalueNameDataOff : UpvalueNameDataOff+8])); got != object.UpvalueNameData {
		t.Fatalf("upvalue name data = %#x, want %#x", uint64(got), uint64(object.UpvalueNameData))
	}
}

func TestStoreBuildsHeapNativeConstantArea(t *testing.T) {
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
	handle, err := store.Intern(proto)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
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
	object, err := store.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read proto object: %v", err)
	}
	if uintptr(object.ConstBasePtr) != base {
		t.Fatalf("proto object const base %#x, want %#x", uintptr(object.ConstBasePtr), base)
	}
	offset := nativeConstOffset(t, runtimeHeap, base)
	bytes, err := runtimeHeap.Resolve(offset, uint64(len(proto.Constants))*value.TValueSize)
	if err != nil {
		t.Fatalf("resolve constant bytes: %v", err)
	}
	if uintptr(unsafe.Pointer(&bytes[0])) != base {
		t.Fatalf("constant area bytes base %#x, want %#x", uintptr(unsafe.Pointer(&bytes[0])), base)
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
	offset := nativeConstOffset(t, runtimeHeap, base)
	if base%value.TValueSize != 0 {
		t.Fatalf("constant base %#x is not %d-byte aligned", base, value.TValueSize)
	}
	for index, constant := range proto.Constants {
		want, err := constantToTValue(constant, strings)
		if err != nil {
			t.Fatalf("constant %d conversion: %v", index, err)
		}
		address, err := runtimeHeap.NativeAddressForOffset(offset + value.HeapOff64(index*value.TValueSize))
		if err != nil {
			t.Fatalf("constant %d native address: %v", index, err)
		}
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

func TestConstantValueReadsCanonicalHeapBytes(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0xBADC0DE)
	store := NewStore(runtimeHeap)
	proto := &bytecode.Proto{
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(1),
		},
	}
	base, err := store.ConstantBase(proto, strings)
	if err != nil {
		t.Fatalf("constant base: %v", err)
	}
	offset := nativeConstOffset(t, runtimeHeap, base)
	bytes, err := runtimeHeap.Resolve(offset, value.TValueSize)
	if err != nil {
		t.Fatalf("resolve constant bytes: %v", err)
	}
	if uintptr(unsafe.Pointer(&bytes[0])) != base {
		t.Fatalf("constant base %#x does not match canonical bytes %#x", base, uintptr(unsafe.Pointer(&bytes[0])))
	}
	binary.LittleEndian.PutUint64(bytes, uint64(value.NumberValue(99).Bits()))
	got, err := store.ConstantValue(proto, 0, strings)
	if err != nil {
		t.Fatalf("constant 0 after native mutation: %v", err)
	}
	if got.Bits() != value.NumberValue(99).Bits() {
		t.Fatalf("constant 0 = %s, want %s", got, value.NumberValue(99))
	}
}

func nativeConstOffset(t *testing.T, runtimeHeap *heap.Heap, base uintptr) value.HeapOff64 {
	t.Helper()
	nativeBase := runtimeHeap.NativeBase()
	if base == 0 {
		t.Fatalf("constant base should not be zero")
	}
	if base < nativeBase {
		t.Fatalf("constant base %#x precedes heap native base %#x", base, nativeBase)
	}
	return value.HeapOff64(uint64(base - nativeBase))
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
		LineInfo:     []int{21},
		LocVars:      []bytecode.LocVar{{Name: "arg", StartPC: 0, EndPC: 1}},
		UpvalueNames: []string{"left", "right"},
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
		LineInfo:     []int{10, 11, 12, 13},
		LocVars:      []bytecode.LocVar{{Name: "x", StartPC: 0, EndPC: 4}, {Name: "y", StartPC: 1, EndPC: 3}},
		UpvalueNames: []string{"env"},
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
	if object.CodeData == 0 || object.ChildProtoData == 0 || object.ClosureSiteData == 0 || object.LineInfoData == 0 || object.LocVarData == 0 || object.UpvalueNameData == 0 {
		t.Fatalf("expected proto native payload offsets, got code=%#x child=%#x closure=%#x line=%#x loc=%#x up=%#x", uint64(object.CodeData), uint64(object.ChildProtoData), uint64(object.ClosureSiteData), uint64(object.LineInfoData), uint64(object.LocVarData), uint64(object.UpvalueNameData))
	}
	if object.ClosureSiteCount != 1 {
		t.Fatalf("closure site count = %d, want 1", object.ClosureSiteCount)
	}
	if object.LocVarCount != uint32(len(parent.LocVars)) {
		t.Fatalf("locvar count = %d, want %d", object.LocVarCount, len(parent.LocVars))
	}
	if object.UpvalueNameCount != uint32(len(parent.UpvalueNames)) {
		t.Fatalf("upvalue name count = %d, want %d", object.UpvalueNameCount, len(parent.UpvalueNames))
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
	nativeAddress, err := runtimeHeap.NativeAddressForOffset(offset)
	if err != nil {
		t.Fatalf("resolve native proto address: %v", err)
	}
	if uintptr(unsafe.Pointer(&bytes[0])) != nativeAddress {
		t.Fatalf("proto object bytes base %#x, want %#x", uintptr(unsafe.Pointer(&bytes[0])), nativeAddress)
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
	codeBytes, err := runtimeHeap.Resolve(object.CodeData, uint64(object.InstructionCount)*4)
	if err != nil {
		t.Fatalf("resolve canonical code bytes: %v", err)
	}
	nativeCodeAddress, err := runtimeHeap.NativeAddressForOffset(object.CodeData)
	if err != nil {
		t.Fatalf("resolve native code address: %v", err)
	}
	if uintptr(unsafe.Pointer(&codeBytes[0])) != nativeCodeAddress {
		t.Fatalf("proto code bytes base %#x, want %#x", uintptr(unsafe.Pointer(&codeBytes[0])), nativeCodeAddress)
	}
	childRefs, err := store.ChildProtoRefs(handle.Ref)
	if err != nil {
		t.Fatalf("read child proto refs: %v", err)
	}
	if len(childRefs) != 1 || childRefs[0] != childHandle.Ref {
		t.Fatalf("unexpected child proto refs: %#v", childRefs)
	}
	childBytes, err := runtimeHeap.Resolve(object.ChildProtoData, uint64(object.ProtoCount)*8)
	if err != nil {
		t.Fatalf("resolve canonical child proto bytes: %v", err)
	}
	nativeChildAddress, err := runtimeHeap.NativeAddressForOffset(object.ChildProtoData)
	if err != nil {
		t.Fatalf("resolve native child proto address: %v", err)
	}
	if uintptr(unsafe.Pointer(&childBytes[0])) != nativeChildAddress {
		t.Fatalf("proto child-ref bytes base %#x, want %#x", uintptr(unsafe.Pointer(&childBytes[0])), nativeChildAddress)
	}
	lines, err := store.LineInfo(handle.Ref)
	if err != nil {
		t.Fatalf("read line info: %v", err)
	}
	if len(lines) != len(parent.LineInfo) || lines[0] != 10 || lines[3] != 13 {
		t.Fatalf("unexpected native line info: %#v", lines)
	}
	locals, err := store.LocVars(handle.Ref)
	if err != nil {
		t.Fatalf("read locvars: %v", err)
	}
	if len(locals) != len(parent.LocVars) || locals[0] != parent.LocVars[0] || locals[1] != parent.LocVars[1] {
		t.Fatalf("unexpected native locvars: %#v", locals)
	}
	upvalueNames, err := store.UpvalueNames(handle.Ref)
	if err != nil {
		t.Fatalf("read upvalue names: %v", err)
	}
	if len(upvalueNames) != len(parent.UpvalueNames) || upvalueNames[0] != parent.UpvalueNames[0] {
		t.Fatalf("unexpected native upvalue names: %#v", upvalueNames)
	}
	lineBytes, err := runtimeHeap.Resolve(object.LineInfoData, uint64(object.InstructionCount)*4)
	if err != nil {
		t.Fatalf("resolve canonical line info bytes: %v", err)
	}
	nativeLineAddress, err := runtimeHeap.NativeAddressForOffset(object.LineInfoData)
	if err != nil {
		t.Fatalf("resolve native line info address: %v", err)
	}
	if uintptr(unsafe.Pointer(&lineBytes[0])) != nativeLineAddress {
		t.Fatalf("proto line-info bytes base %#x, want %#x", uintptr(unsafe.Pointer(&lineBytes[0])), nativeLineAddress)
	}
	locVarBytes, err := runtimeHeap.Resolve(object.LocVarData, uint64(object.LocVarCount)*locVarRecordSize)
	if err != nil {
		t.Fatalf("resolve canonical locvar bytes: %v", err)
	}
	nativeLocVarAddress, err := runtimeHeap.NativeAddressForOffset(object.LocVarData)
	if err != nil {
		t.Fatalf("resolve native locvar address: %v", err)
	}
	if uintptr(unsafe.Pointer(&locVarBytes[0])) != nativeLocVarAddress {
		t.Fatalf("proto locvar bytes base %#x, want %#x", uintptr(unsafe.Pointer(&locVarBytes[0])), nativeLocVarAddress)
	}
	upvalueNameBytes, err := runtimeHeap.Resolve(object.UpvalueNameData, uint64(object.UpvalueNameCount)*debugNameRecordSize)
	if err != nil {
		t.Fatalf("resolve canonical upvalue-name bytes: %v", err)
	}
	nativeUpvalueNameAddress, err := runtimeHeap.NativeAddressForOffset(object.UpvalueNameData)
	if err != nil {
		t.Fatalf("resolve native upvalue-name address: %v", err)
	}
	if uintptr(unsafe.Pointer(&upvalueNameBytes[0])) != nativeUpvalueNameAddress {
		t.Fatalf("proto upvalue-name bytes base %#x, want %#x", uintptr(unsafe.Pointer(&upvalueNameBytes[0])), nativeUpvalueNameAddress)
	}
	childLocals, err := store.LocVars(childHandle.Ref)
	if err != nil {
		t.Fatalf("read child locvars: %v", err)
	}
	if len(childLocals) != len(child.LocVars) || childLocals[0] != child.LocVars[0] {
		t.Fatalf("unexpected child locvars: %#v", childLocals)
	}
	childUpvalueNames, err := store.UpvalueNames(childHandle.Ref)
	if err != nil {
		t.Fatalf("read child upvalue names: %v", err)
	}
	if len(childUpvalueNames) != len(child.UpvalueNames) || childUpvalueNames[0] != child.UpvalueNames[0] || childUpvalueNames[1] != child.UpvalueNames[1] {
		t.Fatalf("unexpected child upvalue names: %#v", childUpvalueNames)
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
	closureSiteBytes, err := runtimeHeap.Resolve(object.ClosureSiteData, uint64(object.ClosureSiteCount)*ClosureSiteSize)
	if err != nil {
		t.Fatalf("resolve canonical closure-site bytes: %v", err)
	}
	nativeClosureSiteAddress, err := runtimeHeap.NativeAddressForOffset(object.ClosureSiteData)
	if err != nil {
		t.Fatalf("resolve native closure-site address: %v", err)
	}
	if uintptr(unsafe.Pointer(&closureSiteBytes[0])) != nativeClosureSiteAddress {
		t.Fatalf("proto closure-site bytes base %#x, want %#x", uintptr(unsafe.Pointer(&closureSiteBytes[0])), nativeClosureSiteAddress)
	}
	captureBytes, err := runtimeHeap.Resolve(site.CaptureData, uint64(site.UpvalueCount)*CaptureDescriptorSize)
	if err != nil {
		t.Fatalf("resolve canonical capture bytes: %v", err)
	}
	nativeCaptureAddress, err := runtimeHeap.NativeAddressForOffset(site.CaptureData)
	if err != nil {
		t.Fatalf("resolve native capture address: %v", err)
	}
	if uintptr(unsafe.Pointer(&captureBytes[0])) != nativeCaptureAddress {
		t.Fatalf("proto capture-descriptor bytes base %#x, want %#x", uintptr(unsafe.Pointer(&captureBytes[0])), nativeCaptureAddress)
	}
}

func TestStoreRepairsStaleCachedProtoRefs(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	store := NewStore(runtimeHeap)
	protoValue := &bytecode.Proto{
		Source:       "stale-proto",
		MaxStackSize: 1,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0),
		},
	}
	handle, err := store.Intern(protoValue)
	if err != nil {
		t.Fatalf("intern proto: %v", err)
	}
	address, err := runtimeHeap.DecodeHeapRef(handle.Ref)
	if err != nil {
		t.Fatalf("decode proto ref: %v", err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("proto offset: %v", err)
	}
	if err := runtimeHeap.FreeSpan(offset); err != nil {
		t.Fatalf("free proto span: %v", err)
	}
	if _, err := store.Resolve(handle.Ref); err == nil {
		t.Fatalf("stale proto ref should not resolve after span free")
	}
	if _, ok := store.byProto[protoValue]; ok {
		t.Fatalf("stale byProto entry should be pruned")
	}
	if _, ok := store.byRef[handle.Ref]; ok {
		t.Fatalf("stale byRef entry should be pruned")
	}
	repaired, err := store.Intern(protoValue)
	if err != nil {
		t.Fatalf("re-intern proto after prune: %v", err)
	}
	resolved, err := store.Resolve(repaired.Ref)
	if err != nil {
		t.Fatalf("resolve repaired proto ref: %v", err)
	}
	if resolved != protoValue {
		t.Fatalf("resolved proto pointer %p, want %p", resolved, protoValue)
	}
	if _, err := runtimeHeap.DecodeHeapRef(repaired.Ref); err != nil {
		t.Fatalf("repaired proto ref should decode to a live object: %v", err)
	}
}

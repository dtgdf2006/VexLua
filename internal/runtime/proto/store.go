package proto

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/heap"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
)

const (
	ObjectSize            = 0x60
	InstructionCountOff   = 0x10
	ConstantCountOff      = 0x14
	ProtoCountOff         = 0x18
	UpvalueCountOff       = 0x1A
	MaxStackSizeOff       = 0x1C
	NumParamsOff          = 0x1D
	VarargFlagsOff        = 0x1E
	CompiledFlagsOff      = 0x1F
	SourceHashOff         = 0x20
	LineDefinedOff        = 0x24
	LastLineDefinedOff    = 0x28
	ClosureSiteCountOff   = 0x2C
	CodeDataOff           = 0x30
	ChildProtoDataOff     = 0x38
	ClosureSiteDataOff    = 0x40
	LineInfoDataOff       = 0x48
	ConstBasePtrOff       = 0x50
	CompiledEntryOff      = 0x58
	CaptureDescriptorSize = 0x04
	ClosureSiteSize       = 0x10
	CaptureKindLocal      = 1
	CaptureKindUpvalue    = 2
)

const (
	ProtoCompiledFlagNoSuspend uint8 = 1 << iota
)

type CaptureKind uint8

const (
	CaptureLocal   CaptureKind = CaptureKindLocal
	CaptureUpvalue CaptureKind = CaptureKindUpvalue
)

type Handle struct {
	Ref   value.HeapRef44
	Value value.TValue
}

type Object struct {
	Common           value.CommonHeader
	InstructionCount uint32
	ConstantCount    uint32
	ProtoCount       uint16
	UpvalueCount     uint16
	MaxStackSize     uint8
	NumParams        uint8
	VarargFlags      uint8
	CompiledFlags    uint8
	SourceHash       uint32
	LineDefined      uint32
	LastLineDefined  uint32
	ClosureSiteCount uint32
	CodeData         value.HeapOff64
	ChildProtoData   value.HeapOff64
	ClosureSiteData  value.HeapOff64
	LineInfoData     value.HeapOff64
	ConstBasePtr     uint64
	CompiledEntry    uint64
}

type CaptureDescriptor struct {
	Kind  CaptureKind
	Index uint16
}

type ClosureSite struct {
	PC              uint32
	ChildProtoIndex uint16
	UpvalueCount    uint16
	CaptureData     value.HeapOff64
}

type Store struct {
	heap    *heap.Heap
	byProto map[*bytecode.Proto]value.HeapRef44
	byRef   map[value.HeapRef44]*bytecode.Proto
}

func NewStore(runtimeHeap *heap.Heap) *Store {
	if runtimeHeap == nil {
		panic("proto store requires a heap")
	}
	return &Store{
		heap:    runtimeHeap,
		byProto: make(map[*bytecode.Proto]value.HeapRef44),
		byRef:   make(map[value.HeapRef44]*bytecode.Proto),
	}
}

func (store *Store) Intern(proto *bytecode.Proto) (Handle, error) {
	if proto == nil {
		return Handle{}, fmt.Errorf("proto cannot be nil")
	}
	if ref, ok := store.byProto[proto]; ok {
		return Handle{Ref: ref, Value: value.ProtoRefValue(ref)}, nil
	}
	object, err := store.buildObject(proto)
	if err != nil {
		return Handle{}, err
	}
	allocation, err := store.heap.AllocObject(object.Common)
	if err != nil {
		return Handle{}, err
	}
	if err := WriteObject(allocation.Bytes, object); err != nil {
		return Handle{}, err
	}
	ref, err := store.heap.EncodeHeapRef(allocation.Address)
	if err != nil {
		return Handle{}, err
	}
	store.byProto[proto] = ref
	store.byRef[ref] = proto
	return Handle{Ref: ref, Value: value.ProtoRefValue(ref)}, nil
}

func (store *Store) Resolve(ref value.HeapRef44) (*bytecode.Proto, error) {
	proto, ok := store.byRef[ref]
	if !ok {
		return nil, fmt.Errorf("unknown proto ref %#x", uint64(ref))
	}
	return proto, nil
}

func (store *Store) SetCompiledEntry(ref value.HeapRef44, entry uintptr) error {
	return store.SyncCompiledMetadata(ref, entry, 0)
}

func (store *Store) SyncCompiledMetadata(ref value.HeapRef44, entry uintptr, flags uint8) error {
	object, bytes, err := store.objectBytes(ref)
	if err != nil {
		return err
	}
	object.CompiledEntry = uint64(entry)
	object.CompiledFlags = flags
	return WriteObject(bytes, object)
}

func (store *Store) CompiledEntry(ref value.HeapRef44) (uintptr, error) {
	object, err := store.Object(ref)
	if err != nil {
		return 0, err
	}
	return uintptr(object.CompiledEntry), nil
}

func (store *Store) CompiledFlags(ref value.HeapRef44) (uint8, error) {
	object, err := store.Object(ref)
	if err != nil {
		return 0, err
	}
	return object.CompiledFlags, nil
}

func (store *Store) NativeConstBase(ref value.HeapRef44) (uintptr, error) {
	object, err := store.Object(ref)
	if err != nil {
		return 0, err
	}
	return uintptr(object.ConstBasePtr), nil
}

func (store *Store) ConstantBase(proto *bytecode.Proto, strings *rtstring.InternTable) (uintptr, error) {
	object, _, err := store.ensureConstantData(proto, strings)
	if err != nil {
		return 0, err
	}
	return uintptr(object.ConstBasePtr), nil
}

func (store *Store) ConstantValue(proto *bytecode.Proto, index int, strings *rtstring.InternTable) (value.TValue, error) {
	object, offset, err := store.ensureConstantData(proto, strings)
	if err != nil {
		return value.NilValue(), err
	}
	if offset == 0 || index < 0 || index >= int(object.ConstantCount) {
		return value.NilValue(), fmt.Errorf("constant %d is out of range", index)
	}
	bytes, err := store.heap.Resolve(offset+value.HeapOff64(index*value.TValueSize), value.TValueSize)
	if err != nil {
		return value.NilValue(), err
	}
	return value.FromRaw(value.Raw(binary.LittleEndian.Uint64(bytes))), nil
}

func (store *Store) Object(ref value.HeapRef44) (Object, error) {
	address, err := store.heap.DecodeHeapRef(ref)
	if err != nil {
		return Object{}, err
	}
	offset, err := store.heap.OffsetForAddress(address)
	if err != nil {
		return Object{}, err
	}
	bytes, err := store.heap.Resolve(offset, ObjectSize)
	if err != nil {
		return Object{}, err
	}
	return ReadObject(bytes)
}

func NewObject(proto *bytecode.Proto) Object {
	return Object{
		Common: value.CommonHeader{
			Kind:      value.KindProto,
			SizeBytes: ObjectSize,
			Version:   1,
		},
		InstructionCount: uint32(len(proto.Code)),
		ConstantCount:    uint32(len(proto.Constants)),
		ProtoCount:       uint16(len(proto.Protos)),
		UpvalueCount:     uint16(proto.NumUpvalues),
		MaxStackSize:     proto.MaxStackSize,
		NumParams:        proto.NumParams,
		VarargFlags:      proto.IsVararg,
		CompiledFlags:    0,
		SourceHash:       rtstring.HashString(proto.Source, 0),
		LineDefined:      uint32(proto.LineDefined),
		LastLineDefined:  uint32(proto.LastLineDef),
	}
}

func ReadObject(buffer []byte) (Object, error) {
	if len(buffer) < ObjectSize {
		return Object{}, fmt.Errorf("buffer too small for proto object: %d", len(buffer))
	}
	common, err := value.ReadCommonHeader(buffer)
	if err != nil {
		return Object{}, err
	}
	if common.Kind != value.KindProto {
		return Object{}, fmt.Errorf("expected %s object, got %s", value.KindProto, common.Kind)
	}
	return Object{
		Common:           common,
		InstructionCount: binary.LittleEndian.Uint32(buffer[InstructionCountOff : InstructionCountOff+4]),
		ConstantCount:    binary.LittleEndian.Uint32(buffer[ConstantCountOff : ConstantCountOff+4]),
		ProtoCount:       binary.LittleEndian.Uint16(buffer[ProtoCountOff : ProtoCountOff+2]),
		UpvalueCount:     binary.LittleEndian.Uint16(buffer[UpvalueCountOff : UpvalueCountOff+2]),
		MaxStackSize:     buffer[MaxStackSizeOff],
		NumParams:        buffer[NumParamsOff],
		VarargFlags:      buffer[VarargFlagsOff],
		CompiledFlags:    buffer[CompiledFlagsOff],
		SourceHash:       binary.LittleEndian.Uint32(buffer[SourceHashOff : SourceHashOff+4]),
		LineDefined:      binary.LittleEndian.Uint32(buffer[LineDefinedOff : LineDefinedOff+4]),
		LastLineDefined:  binary.LittleEndian.Uint32(buffer[LastLineDefinedOff : LastLineDefinedOff+4]),
		ClosureSiteCount: binary.LittleEndian.Uint32(buffer[ClosureSiteCountOff : ClosureSiteCountOff+4]),
		CodeData:         value.HeapOff64(binary.LittleEndian.Uint64(buffer[CodeDataOff : CodeDataOff+8])),
		ChildProtoData:   value.HeapOff64(binary.LittleEndian.Uint64(buffer[ChildProtoDataOff : ChildProtoDataOff+8])),
		ClosureSiteData:  value.HeapOff64(binary.LittleEndian.Uint64(buffer[ClosureSiteDataOff : ClosureSiteDataOff+8])),
		LineInfoData:     value.HeapOff64(binary.LittleEndian.Uint64(buffer[LineInfoDataOff : LineInfoDataOff+8])),
		ConstBasePtr:     binary.LittleEndian.Uint64(buffer[ConstBasePtrOff : ConstBasePtrOff+8]),
		CompiledEntry:    binary.LittleEndian.Uint64(buffer[CompiledEntryOff : CompiledEntryOff+8]),
	}, nil
}

func WriteObject(buffer []byte, object Object) error {
	if len(buffer) < ObjectSize {
		return fmt.Errorf("buffer too small for proto object: %d", len(buffer))
	}
	if err := value.WriteCommonHeader(buffer, object.Common); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(buffer[InstructionCountOff:InstructionCountOff+4], object.InstructionCount)
	binary.LittleEndian.PutUint32(buffer[ConstantCountOff:ConstantCountOff+4], object.ConstantCount)
	binary.LittleEndian.PutUint16(buffer[ProtoCountOff:ProtoCountOff+2], object.ProtoCount)
	binary.LittleEndian.PutUint16(buffer[UpvalueCountOff:UpvalueCountOff+2], object.UpvalueCount)
	buffer[MaxStackSizeOff] = object.MaxStackSize
	buffer[NumParamsOff] = object.NumParams
	buffer[VarargFlagsOff] = object.VarargFlags
	buffer[CompiledFlagsOff] = object.CompiledFlags
	binary.LittleEndian.PutUint32(buffer[SourceHashOff:SourceHashOff+4], object.SourceHash)
	binary.LittleEndian.PutUint32(buffer[LineDefinedOff:LineDefinedOff+4], object.LineDefined)
	binary.LittleEndian.PutUint32(buffer[LastLineDefinedOff:LastLineDefinedOff+4], object.LastLineDefined)
	binary.LittleEndian.PutUint32(buffer[ClosureSiteCountOff:ClosureSiteCountOff+4], object.ClosureSiteCount)
	binary.LittleEndian.PutUint64(buffer[CodeDataOff:CodeDataOff+8], uint64(object.CodeData))
	binary.LittleEndian.PutUint64(buffer[ChildProtoDataOff:ChildProtoDataOff+8], uint64(object.ChildProtoData))
	binary.LittleEndian.PutUint64(buffer[ClosureSiteDataOff:ClosureSiteDataOff+8], uint64(object.ClosureSiteData))
	binary.LittleEndian.PutUint64(buffer[LineInfoDataOff:LineInfoDataOff+8], uint64(object.LineInfoData))
	binary.LittleEndian.PutUint64(buffer[ConstBasePtrOff:ConstBasePtrOff+8], object.ConstBasePtr)
	binary.LittleEndian.PutUint64(buffer[CompiledEntryOff:CompiledEntryOff+8], object.CompiledEntry)
	return nil
}

func (store *Store) Instructions(ref value.HeapRef44) ([]bytecode.Instruction, error) {
	object, err := store.Object(ref)
	if err != nil {
		return nil, err
	}
	if object.InstructionCount == 0 || object.CodeData == 0 {
		return nil, nil
	}
	bytes, err := store.heap.Resolve(object.CodeData, uint64(object.InstructionCount)*4)
	if err != nil {
		return nil, err
	}
	instructions := make([]bytecode.Instruction, object.InstructionCount)
	for index := range instructions {
		instructions[index] = bytecode.Instruction(binary.LittleEndian.Uint32(bytes[index*4 : (index+1)*4]))
	}
	return instructions, nil
}

func (store *Store) ChildProtoRefs(ref value.HeapRef44) ([]value.HeapRef44, error) {
	object, err := store.Object(ref)
	if err != nil {
		return nil, err
	}
	if object.ProtoCount == 0 || object.ChildProtoData == 0 {
		return nil, nil
	}
	bytes, err := store.heap.Resolve(object.ChildProtoData, uint64(object.ProtoCount)*8)
	if err != nil {
		return nil, err
	}
	refs := make([]value.HeapRef44, object.ProtoCount)
	for index := range refs {
		refs[index] = value.HeapRef44(binary.LittleEndian.Uint64(bytes[index*8 : (index+1)*8]))
	}
	return refs, nil
}

func (store *Store) LineInfo(ref value.HeapRef44) ([]int, error) {
	object, err := store.Object(ref)
	if err != nil {
		return nil, err
	}
	if object.InstructionCount == 0 || object.LineInfoData == 0 {
		return nil, nil
	}
	bytes, err := store.heap.Resolve(object.LineInfoData, uint64(object.InstructionCount)*4)
	if err != nil {
		return nil, err
	}
	lines := make([]int, object.InstructionCount)
	for index := range lines {
		lines[index] = int(int32(binary.LittleEndian.Uint32(bytes[index*4 : (index+1)*4])))
	}
	return lines, nil
}

func (store *Store) ClosureSite(ref value.HeapRef44, pc int) (ClosureSite, []CaptureDescriptor, bool, error) {
	object, err := store.Object(ref)
	if err != nil {
		return ClosureSite{}, nil, false, err
	}
	if object.ClosureSiteCount == 0 || object.ClosureSiteData == 0 {
		return ClosureSite{}, nil, false, nil
	}
	bytes, err := store.heap.Resolve(object.ClosureSiteData, uint64(object.ClosureSiteCount)*ClosureSiteSize)
	if err != nil {
		return ClosureSite{}, nil, false, err
	}
	for index := uint32(0); index < object.ClosureSiteCount; index++ {
		start := int(index) * ClosureSiteSize
		site, err := readClosureSite(bytes[start : start+ClosureSiteSize])
		if err != nil {
			return ClosureSite{}, nil, false, err
		}
		if site.PC != uint32(pc) {
			continue
		}
		captures, err := store.readCaptureDescriptors(site.CaptureData, site.UpvalueCount)
		if err != nil {
			return ClosureSite{}, nil, false, err
		}
		return site, captures, true, nil
	}
	return ClosureSite{}, nil, false, nil
}

func (store *Store) buildObject(proto *bytecode.Proto) (Object, error) {
	object := NewObject(proto)
	codeData, err := store.allocInstructionData(proto.Code)
	if err != nil {
		return Object{}, err
	}
	childData, err := store.allocChildProtoData(proto.Protos)
	if err != nil {
		return Object{}, err
	}
	closureSites, err := buildClosureSites(proto)
	if err != nil {
		return Object{}, err
	}
	closureSiteData, err := store.allocClosureSiteData(closureSites)
	if err != nil {
		return Object{}, err
	}
	lineInfoData, err := store.allocLineInfoData(proto.LineInfo)
	if err != nil {
		return Object{}, err
	}
	object.ClosureSiteCount = uint32(len(closureSites))
	object.CodeData = codeData
	object.ChildProtoData = childData
	object.ClosureSiteData = closureSiteData
	object.LineInfoData = lineInfoData
	return object, nil
}

func (store *Store) objectBytes(ref value.HeapRef44) (Object, []byte, error) {
	address, err := store.heap.DecodeHeapRef(ref)
	if err != nil {
		return Object{}, nil, err
	}
	offset, err := store.heap.OffsetForAddress(address)
	if err != nil {
		return Object{}, nil, err
	}
	bytes, err := store.heap.Resolve(offset, ObjectSize)
	if err != nil {
		return Object{}, nil, err
	}
	object, err := ReadObject(bytes)
	if err != nil {
		return Object{}, nil, err
	}
	return object, bytes, nil
}

func (store *Store) ensureConstantData(proto *bytecode.Proto, strings *rtstring.InternTable) (Object, value.HeapOff64, error) {
	if proto == nil {
		return Object{}, 0, fmt.Errorf("proto cannot be nil")
	}
	handle, err := store.Intern(proto)
	if err != nil {
		return Object{}, 0, err
	}
	object, bytes, err := store.objectBytes(handle.Ref)
	if err != nil {
		return Object{}, 0, err
	}
	if object.ConstantCount == 0 {
		return object, 0, nil
	}
	if object.ConstBasePtr != 0 {
		offset, err := store.constantDataOffset(object.ConstBasePtr)
		if err != nil {
			return Object{}, 0, err
		}
		return object, offset, nil
	}
	offset, err := store.allocConstantData(proto.Constants, strings)
	if err != nil {
		return Object{}, 0, err
	}
	base, err := store.heap.NativeAddressForOffset(offset)
	if err != nil {
		return Object{}, 0, err
	}
	object.ConstBasePtr = uint64(base)
	if err := WriteObject(bytes, object); err != nil {
		return Object{}, 0, err
	}
	return object, offset, nil
}

func (store *Store) constantDataOffset(base uint64) (value.HeapOff64, error) {
	if base == 0 {
		return 0, nil
	}
	nativeBase := store.heap.NativeBase()
	if uintptr(base) < nativeBase {
		return 0, fmt.Errorf("constant base %#x precedes heap native base %#x", base, nativeBase)
	}
	offset := value.HeapOff64(uint64(uintptr(base) - nativeBase))
	if _, err := store.heap.Resolve(offset, value.TValueSize); err != nil {
		return 0, err
	}
	return offset, nil
}

func (store *Store) allocInstructionData(code []bytecode.Instruction) (value.HeapOff64, error) {
	if len(code) == 0 {
		return 0, nil
	}
	allocation, err := store.heap.Alloc(uint64(len(code)) * 4)
	if err != nil {
		return 0, err
	}
	for index, instruction := range code {
		binary.LittleEndian.PutUint32(allocation.Bytes[index*4:(index+1)*4], uint32(instruction))
	}
	return allocation.Offset, nil
}

func (store *Store) allocChildProtoData(children []*bytecode.Proto) (value.HeapOff64, error) {
	if len(children) == 0 {
		return 0, nil
	}
	allocation, err := store.heap.Alloc(uint64(len(children)) * 8)
	if err != nil {
		return 0, err
	}
	for index, child := range children {
		handle, err := store.Intern(child)
		if err != nil {
			return 0, err
		}
		binary.LittleEndian.PutUint64(allocation.Bytes[index*8:(index+1)*8], uint64(handle.Ref))
	}
	return allocation.Offset, nil
}

func (store *Store) allocLineInfoData(lines []int) (value.HeapOff64, error) {
	if len(lines) == 0 {
		return 0, nil
	}
	allocation, err := store.heap.Alloc(uint64(len(lines)) * 4)
	if err != nil {
		return 0, err
	}
	for index, line := range lines {
		binary.LittleEndian.PutUint32(allocation.Bytes[index*4:(index+1)*4], uint32(int32(line)))
	}
	return allocation.Offset, nil
}

func (store *Store) allocClosureSiteData(sites []closureSiteRecord) (value.HeapOff64, error) {
	if len(sites) == 0 {
		return 0, nil
	}
	allocation, err := store.heap.Alloc(uint64(len(sites)) * ClosureSiteSize)
	if err != nil {
		return 0, err
	}
	for index, site := range sites {
		captureData, err := store.allocCaptureDescriptors(site.Captures)
		if err != nil {
			return 0, err
		}
		encoded := ClosureSite{
			PC:              site.PC,
			ChildProtoIndex: site.ChildProtoIndex,
			UpvalueCount:    uint16(len(site.Captures)),
			CaptureData:     captureData,
		}
		start := int(index) * ClosureSiteSize
		if err := writeClosureSite(allocation.Bytes[start:start+ClosureSiteSize], encoded); err != nil {
			return 0, err
		}
	}
	return allocation.Offset, nil
}

func (store *Store) allocCaptureDescriptors(captures []CaptureDescriptor) (value.HeapOff64, error) {
	if len(captures) == 0 {
		return 0, nil
	}
	allocation, err := store.heap.Alloc(uint64(len(captures)) * CaptureDescriptorSize)
	if err != nil {
		return 0, err
	}
	for index, capture := range captures {
		start := int(index) * CaptureDescriptorSize
		writeCaptureDescriptor(allocation.Bytes[start:start+CaptureDescriptorSize], capture)
	}
	return allocation.Offset, nil
}

func (store *Store) readCaptureDescriptors(offset value.HeapOff64, count uint16) ([]CaptureDescriptor, error) {
	if count == 0 || offset == 0 {
		return nil, nil
	}
	bytes, err := store.heap.Resolve(offset, uint64(count)*CaptureDescriptorSize)
	if err != nil {
		return nil, err
	}
	descs := make([]CaptureDescriptor, count)
	for index := range descs {
		start := index * CaptureDescriptorSize
		descs[index] = readCaptureDescriptor(bytes[start : start+CaptureDescriptorSize])
	}
	return descs, nil
}

type closureSiteRecord struct {
	PC              uint32
	ChildProtoIndex uint16
	Captures        []CaptureDescriptor
}

func buildClosureSites(proto *bytecode.Proto) ([]closureSiteRecord, error) {
	if proto == nil {
		return nil, fmt.Errorf("proto cannot be nil")
	}
	sites := make([]closureSiteRecord, 0)
	for pc, instruction := range proto.Code {
		if instruction.Opcode() != bytecode.OP_CLOSURE {
			continue
		}
		childIndex := instruction.Bx()
		if childIndex < 0 || childIndex >= len(proto.Protos) {
			return nil, fmt.Errorf("closure pc %d child proto %d is out of range", pc, childIndex)
		}
		child := proto.Protos[childIndex]
		captures := make([]CaptureDescriptor, int(child.NumUpvalues))
		for index := range captures {
			capturePC := pc + 1 + index
			if capturePC >= len(proto.Code) {
				return nil, fmt.Errorf("closure pc %d is missing capture instruction %d", pc, index)
			}
			capture := proto.Code[capturePC]
			switch capture.Opcode() {
			case bytecode.OP_MOVE:
				captures[index] = CaptureDescriptor{Kind: CaptureLocal, Index: uint16(capture.B())}
			case bytecode.OP_GETUPVAL:
				captures[index] = CaptureDescriptor{Kind: CaptureUpvalue, Index: uint16(capture.B())}
			default:
				return nil, fmt.Errorf("closure pc %d capture %d uses unsupported opcode %s", pc, index, capture.Opcode())
			}
		}
		sites = append(sites, closureSiteRecord{
			PC:              uint32(pc),
			ChildProtoIndex: uint16(childIndex),
			Captures:        captures,
		})
	}
	return sites, nil
}

func readClosureSite(buffer []byte) (ClosureSite, error) {
	if len(buffer) < ClosureSiteSize {
		return ClosureSite{}, fmt.Errorf("buffer too small for closure site: %d", len(buffer))
	}
	return ClosureSite{
		PC:              binary.LittleEndian.Uint32(buffer[0:4]),
		ChildProtoIndex: binary.LittleEndian.Uint16(buffer[4:6]),
		UpvalueCount:    binary.LittleEndian.Uint16(buffer[6:8]),
		CaptureData:     value.HeapOff64(binary.LittleEndian.Uint64(buffer[8:16])),
	}, nil
}

func writeClosureSite(buffer []byte, site ClosureSite) error {
	if len(buffer) < ClosureSiteSize {
		return fmt.Errorf("buffer too small for closure site: %d", len(buffer))
	}
	binary.LittleEndian.PutUint32(buffer[0:4], site.PC)
	binary.LittleEndian.PutUint16(buffer[4:6], site.ChildProtoIndex)
	binary.LittleEndian.PutUint16(buffer[6:8], site.UpvalueCount)
	binary.LittleEndian.PutUint64(buffer[8:16], uint64(site.CaptureData))
	return nil
}

func readCaptureDescriptor(buffer []byte) CaptureDescriptor {
	return CaptureDescriptor{
		Kind:  CaptureKind(buffer[0]),
		Index: binary.LittleEndian.Uint16(buffer[2:4]),
	}
}

func writeCaptureDescriptor(buffer []byte, capture CaptureDescriptor) {
	buffer[0] = byte(capture.Kind)
	buffer[1] = 0
	binary.LittleEndian.PutUint16(buffer[2:4], capture.Index)
}

func (store *Store) allocConstantData(constants []bytecode.Constant, strings *rtstring.InternTable) (value.HeapOff64, error) {
	if len(constants) == 0 {
		return 0, nil
	}
	allocation, err := store.heap.Alloc(uint64(len(constants)) * value.TValueSize)
	if err != nil {
		return 0, err
	}
	for index, constant := range constants {
		converted, err := constantToTValue(constant, strings)
		if err != nil {
			return 0, fmt.Errorf("constant %d: %w", index, err)
		}
		start := index * value.TValueSize
		binary.LittleEndian.PutUint64(allocation.Bytes[start:start+value.TValueSize], uint64(converted.Bits()))
	}
	return allocation.Offset, nil
}

func constantToTValue(constant bytecode.Constant, strings *rtstring.InternTable) (value.TValue, error) {
	switch constant.Kind {
	case bytecode.ConstantNil:
		return value.NilValue(), nil
	case bytecode.ConstantBoolean:
		return value.BoolValue(constant.Boolean), nil
	case bytecode.ConstantNumber:
		return value.NumberValue(constant.Number), nil
	case bytecode.ConstantString:
		if strings == nil {
			return value.NilValue(), fmt.Errorf("string table is nil")
		}
		handle, err := strings.Intern(constant.Text)
		if err != nil {
			return value.NilValue(), err
		}
		return handle.Value, nil
	default:
		return value.NilValue(), fmt.Errorf("unsupported constant kind %s", constant.Kind)
	}
}

package table

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	"vexlua/internal/runtime/heap"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
)

func TestTableObjectLayoutContract(t *testing.T) {
	object := NewObject(0xCAFEBABE)
	object.Flags = FlagHasMetatable | FlagHasArrayPart | FlagHasHashPart
	object.ShapeID = 9
	object.TableVersion = 17
	object.Metatable = value.BoolValue(true)
	object.ArrayData = value.HeapOff64(0x1110)
	object.ArrayLenHint = 3
	object.ArrayCap = 4
	object.CtrlData = value.HeapOff64(0x2220)
	object.EntriesData = value.HeapOff64(0x3330)
	object.HashCount = 2
	object.HashCapacity = 16
	object.GrowthLeft = 11
	buffer := make([]byte, ObjectSize)
	if err := WriteObject(buffer, object); err != nil {
		t.Fatalf("write table object: %v", err)
	}
	if got := Flags(binary.LittleEndian.Uint32(buffer[FlagsOffset : FlagsOffset+4])); got != object.Flags {
		t.Fatalf("flags = %#x, want %#x", uint32(got), uint32(object.Flags))
	}
	if got := binary.LittleEndian.Uint32(buffer[ShapeIDOffset : ShapeIDOffset+4]); got != object.ShapeID {
		t.Fatalf("shape id = %d, want %d", got, object.ShapeID)
	}
	if got := binary.LittleEndian.Uint32(buffer[TableVersionOffset : TableVersionOffset+4]); got != object.TableVersion {
		t.Fatalf("table version = %d, want %d", got, object.TableVersion)
	}
	if got := binary.LittleEndian.Uint32(buffer[Reserved0Offset : Reserved0Offset+4]); got != 0 {
		t.Fatalf("reserved0 = %#x, want 0", got)
	}
	if got := value.Raw(binary.LittleEndian.Uint64(buffer[MetatableOffset : MetatableOffset+8])); got != object.Metatable.Bits() {
		t.Fatalf("metatable bits = %#x, want %#x", uint64(got), uint64(object.Metatable.Bits()))
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[ArrayDataOffset : ArrayDataOffset+8])); got != object.ArrayData {
		t.Fatalf("array data = %#x, want %#x", uint64(got), uint64(object.ArrayData))
	}
	if got := binary.LittleEndian.Uint32(buffer[ArrayLenHintOffset : ArrayLenHintOffset+4]); got != object.ArrayLenHint {
		t.Fatalf("array len hint = %d, want %d", got, object.ArrayLenHint)
	}
	if got := binary.LittleEndian.Uint32(buffer[ArrayCapOffset : ArrayCapOffset+4]); got != object.ArrayCap {
		t.Fatalf("array cap = %d, want %d", got, object.ArrayCap)
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[CtrlDataOffset : CtrlDataOffset+8])); got != object.CtrlData {
		t.Fatalf("ctrl data = %#x, want %#x", uint64(got), uint64(object.CtrlData))
	}
	if got := value.HeapOff64(binary.LittleEndian.Uint64(buffer[EntriesDataOffset : EntriesDataOffset+8])); got != object.EntriesData {
		t.Fatalf("entries data = %#x, want %#x", uint64(got), uint64(object.EntriesData))
	}
	if got := binary.LittleEndian.Uint32(buffer[HashCountOffset : HashCountOffset+4]); got != object.HashCount {
		t.Fatalf("hash count = %d, want %d", got, object.HashCount)
	}
	if got := binary.LittleEndian.Uint32(buffer[HashCapacityOffset : HashCapacityOffset+4]); got != object.HashCapacity {
		t.Fatalf("hash capacity = %d, want %d", got, object.HashCapacity)
	}
	if got := binary.LittleEndian.Uint32(buffer[GrowthLeftOffset : GrowthLeftOffset+4]); got != object.GrowthLeft {
		t.Fatalf("growth left = %d, want %d", got, object.GrowthLeft)
	}
	if got := binary.LittleEndian.Uint32(buffer[HashSeedOffset : HashSeedOffset+4]); got != object.HashSeed {
		t.Fatalf("hash seed = %#x, want %#x", got, object.HashSeed)
	}
	if got := binary.LittleEndian.Uint64(buffer[Reserved1Offset : Reserved1Offset+8]); got != 0 {
		t.Fatalf("reserved1 = %#x, want 0", got)
	}
}

func TestArrayPartReadWriteAndLenHint(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	tables := NewStore(runtimeHeap)
	handle, err := tables.New(0, 0)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	if err := tables.Set(handle.Ref, value.NumberValue(1), value.NumberValue(10)); err != nil {
		t.Fatalf("set key 1: %v", err)
	}
	if err := tables.Set(handle.Ref, value.NumberValue(2), value.NumberValue(20)); err != nil {
		t.Fatalf("set key 2: %v", err)
	}
	if err := tables.Set(handle.Ref, value.NumberValue(4), value.NumberValue(40)); err != nil {
		t.Fatalf("set key 4: %v", err)
	}

	object, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table object: %v", err)
	}
	if object.ArrayCap == 0 {
		t.Fatalf("expected array part to be allocated")
	}
	if object.ArrayLenHint != 2 {
		t.Fatalf("unexpected array len hint %d", object.ArrayLenHint)
	}

	if err := tables.Set(handle.Ref, value.NumberValue(3), value.NumberValue(30)); err != nil {
		t.Fatalf("set key 3: %v", err)
	}
	object, err = tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table object after key 3: %v", err)
	}
	if object.ArrayLenHint != 4 {
		t.Fatalf("expected contiguous prefix 4, got %d", object.ArrayLenHint)
	}

	result, found, err := tables.Get(handle.Ref, value.NumberValue(4))
	if err != nil {
		t.Fatalf("get key 4: %v", err)
	}
	if !found {
		t.Fatalf("expected key 4 to exist")
	}
	number, ok := result.Float64()
	if !ok || number != 40 {
		t.Fatalf("unexpected value for key 4: %s", result)
	}
}

func TestHashPartVersionAndMetatableBlockers(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0xCAFEBABE)
	tables := NewStore(runtimeHeap)
	handle, err := tables.New(0, 0)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	keyHandle, err := strings.Intern("answer")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}

	before, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read initial table object: %v", err)
	}
	if err := tables.Set(handle.Ref, keyHandle.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("insert hash key: %v", err)
	}
	afterInsert, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table after insert: %v", err)
	}
	if !afterInsert.Flags.Has(FlagHasHashPart) {
		t.Fatalf("expected hash-part flag after insert")
	}
	if afterInsert.TableVersion <= before.TableVersion {
		t.Fatalf("expected version bump on structural insert")
	}

	if err := tables.Set(handle.Ref, keyHandle.Value, value.NumberValue(84)); err != nil {
		t.Fatalf("overwrite same hash key: %v", err)
	}
	afterOverwrite, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table after overwrite: %v", err)
	}
	if afterOverwrite.TableVersion != afterInsert.TableVersion {
		t.Fatalf("expected overwrite to keep version stable, got %d -> %d", afterInsert.TableVersion, afterOverwrite.TableVersion)
	}

	metatableKey, err := strings.Intern("meta")
	if err != nil {
		t.Fatalf("intern metatable key: %v", err)
	}
	if err := tables.SetMetatable(handle.Ref, metatableKey.Value); err != nil {
		t.Fatalf("set metatable: %v", err)
	}
	withMetatable, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table after metatable: %v", err)
	}
	if !withMetatable.Flags.Has(FlagHasMetatable | FlagIndexFastPathBlocked | FlagNewIndexFastPathBlocked) {
		t.Fatalf("expected metatable blocker flags, got %#x", uint32(withMetatable.Flags))
	}
	if withMetatable.TableVersion <= afterOverwrite.TableVersion {
		t.Fatalf("expected metatable change to bump version")
	}
}

func TestSetListArrayPreallocatesAndRefreshesLenHint(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	tables := NewStore(runtimeHeap)
	handle, err := tables.New(0, 0)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	handled, err := tables.SetListArray(handle.Ref, 1, []value.TValue{
		value.NumberValue(11),
		value.NumberValue(22),
		value.NumberValue(33),
	})
	if err != nil {
		t.Fatalf("setlist array: %v", err)
	}
	if !handled {
		t.Fatalf("expected plain table setlist array to be handled")
	}
	object, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table object: %v", err)
	}
	if object.ArrayCap < 3 {
		t.Fatalf("expected array capacity >= 3, got %d", object.ArrayCap)
	}
	if object.ArrayLenHint != 3 {
		t.Fatalf("array len hint = %d, want 3", object.ArrayLenHint)
	}
	for index, want := range []value.TValue{value.NumberValue(11), value.NumberValue(22), value.NumberValue(33)} {
		got, found, err := tables.Get(handle.Ref, value.NumberValue(float64(index+1)))
		if err != nil {
			t.Fatalf("get key %d: %v", index+1, err)
		}
		if !found {
			t.Fatalf("expected key %d to exist", index+1)
		}
		if got.Bits() != want.Bits() {
			t.Fatalf("table[%d] = %s, want %s", index+1, got, want)
		}
	}

	meta, err := tables.New(0, 0)
	if err != nil {
		t.Fatalf("create metatable: %v", err)
	}
	if err := tables.SetMetatable(handle.Ref, meta.Value); err != nil {
		t.Fatalf("set metatable: %v", err)
	}
	handled, err = tables.SetListArray(handle.Ref, 4, []value.TValue{value.NumberValue(44)})
	if err != nil {
		t.Fatalf("setlist array with blocker: %v", err)
	}
	if handled {
		t.Fatalf("blocked table should not stay on setlist array fast path")
	}
}

func TestHashRehashAndDelete(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0x13572468)
	tables := NewStore(runtimeHeap)
	handle, err := tables.New(0, 0)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	keys := make([]value.TValue, 0, 20)
	for index := 0; index < 20; index++ {
		interned, err := strings.Intern(fmt.Sprintf("key-%02d", index))
		if err != nil {
			t.Fatalf("intern key %d: %v", index, err)
		}
		keys = append(keys, interned.Value)
		if err := tables.Set(handle.Ref, interned.Value, value.NumberValue(float64(index))); err != nil {
			t.Fatalf("insert key %d: %v", index, err)
		}
	}
	object, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table after rehash inserts: %v", err)
	}
	if object.HashCapacity < 32 {
		t.Fatalf("expected hash capacity growth, got %d", object.HashCapacity)
	}

	for index, key := range keys {
		result, found, err := tables.Get(handle.Ref, key)
		if err != nil {
			t.Fatalf("get key %d: %v", index, err)
		}
		if !found {
			t.Fatalf("expected key %d to exist", index)
		}
		number, _ := result.Float64()
		if number != float64(index) {
			t.Fatalf("unexpected value for key %d: %g", index, number)
		}
	}

	versionBeforeDelete := object.TableVersion
	if err := tables.Set(handle.Ref, keys[3], value.NilValue()); err != nil {
		t.Fatalf("delete key 3: %v", err)
	}
	afterDelete, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table after delete: %v", err)
	}
	if afterDelete.TableVersion <= versionBeforeDelete {
		t.Fatalf("expected delete to bump version")
	}
	if _, found, err := tables.Get(handle.Ref, keys[3]); err != nil {
		t.Fatalf("get deleted key: %v", err)
	} else if found {
		t.Fatalf("expected deleted key to be absent")
	}
}

func TestTableObjectExposesNativeArrayAndHashRegions(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0x2468ACE0)
	tables := NewStore(runtimeHeap)
	handle, err := tables.New(0, 0)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	keyHandle, err := strings.Intern("answer")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	if err := tables.Set(handle.Ref, value.NumberValue(1), value.NumberValue(10)); err != nil {
		t.Fatalf("set array key 1: %v", err)
	}
	if err := tables.Set(handle.Ref, value.NumberValue(2), value.NumberValue(20)); err != nil {
		t.Fatalf("set array key 2: %v", err)
	}
	if err := tables.Set(handle.Ref, keyHandle.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("set hash key: %v", err)
	}
	object, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table object: %v", err)
	}
	if !object.Flags.Has(FlagHasArrayPart | FlagHasHashPart) {
		t.Fatalf("expected table to expose array+hash flags, got %#x", uint32(object.Flags))
	}
	if object.ArrayData == 0 || object.CtrlData == 0 || object.EntriesData == 0 {
		t.Fatalf("expected native array/hash offsets, got array=%#x ctrl=%#x entries=%#x", uint64(object.ArrayData), uint64(object.CtrlData), uint64(object.EntriesData))
	}
	arrayBytes, err := runtimeHeap.Resolve(object.ArrayData, uint64(object.ArrayCap)*value.TValueSize)
	if err != nil {
		t.Fatalf("resolve array region: %v", err)
	}
	if got := nativeTValueAt(arrayBytes, 0); got.Bits() != value.NumberValue(10).Bits() {
		t.Fatalf("array slot 0 = %s, want %s", got, value.NumberValue(10))
	}
	if got := nativeTValueAt(arrayBytes, 1); got.Bits() != value.NumberValue(20).Bits() {
		t.Fatalf("array slot 1 = %s, want %s", got, value.NumberValue(20))
	}
	ctrlBytes, err := runtimeHeap.Resolve(object.CtrlData, uint64(object.HashCapacity)+1)
	if err != nil {
		t.Fatalf("resolve ctrl region: %v", err)
	}
	if ctrlBytes[object.HashCapacity] != CtrlSentinel {
		t.Fatalf("hash ctrl sentinel = %#x, want %#x", ctrlBytes[object.HashCapacity], CtrlSentinel)
	}
	entriesBytes, err := runtimeHeap.Resolve(object.EntriesData, uint64(object.HashCapacity)*EntrySize)
	if err != nil {
		t.Fatalf("resolve entries region: %v", err)
	}
	found := false
	for slot := uint32(0); slot < object.HashCapacity; slot++ {
		switch ctrlBytes[slot] {
		case CtrlEmpty, CtrlDeleted, CtrlSentinel:
			continue
		}
		start := int(slot) * EntrySize
		entry, err := ReadEntry(entriesBytes[start : start+EntrySize])
		if err != nil {
			t.Fatalf("read entry %d: %v", slot, err)
		}
		if entry.Key.Bits() != keyHandle.Value.Bits() {
			continue
		}
		found = true
		if entry.KeyClass != KeyClassInternedString {
			t.Fatalf("hash entry key class = %d, want %d", entry.KeyClass, KeyClassInternedString)
		}
		if entry.KeyAux != uint64(keyHandle.Ref) {
			t.Fatalf("hash entry key aux = %#x, want %#x", entry.KeyAux, uint64(keyHandle.Ref))
		}
		if entry.Value.Bits() != value.NumberValue(42).Bits() {
			t.Fatalf("hash entry value = %s, want %s", entry.Value, value.NumberValue(42))
		}
	}
	if !found {
		t.Fatalf("expected to find native hash entry for interned string key")
	}
}

func nativeTValueAt(buffer []byte, index int) value.TValue {
	offset := index * value.TValueSize
	return value.FromRaw(value.Raw(binary.LittleEndian.Uint64(buffer[offset : offset+value.TValueSize])))
}

func TestDescribeFastAccessForArrayAndHash(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0x12345678)
	tables := NewStore(runtimeHeap)
	handle, err := tables.New(0, 0)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	keyHandle, err := strings.Intern("answer")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	if err := tables.Set(handle.Ref, value.NumberValue(1), value.NumberValue(10)); err != nil {
		t.Fatalf("set array key: %v", err)
	}
	if err := tables.Set(handle.Ref, keyHandle.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("set hash key: %v", err)
	}
	arrayAccess, ok, err := tables.DescribeFastGet(handle.Ref, value.NumberValue(1))
	if err != nil {
		t.Fatalf("describe array get: %v", err)
	}
	if !ok || arrayAccess.Kind != FastAccessArray || arrayAccess.SlotIndex != 0 {
		t.Fatalf("unexpected array fast access: %+v ok=%t", arrayAccess, ok)
	}
	hashAccess, ok, err := tables.DescribeFastGet(handle.Ref, keyHandle.Value)
	if err != nil {
		t.Fatalf("describe hash get: %v", err)
	}
	if !ok || hashAccess.Kind != FastAccessHash {
		t.Fatalf("unexpected hash fast access: %+v ok=%t", hashAccess, ok)
	}
	storeAccess, ok, err := tables.DescribeFastSet(handle.Ref, keyHandle.Value, value.NumberValue(84))
	if err != nil {
		t.Fatalf("describe hash set: %v", err)
	}
	if !ok || storeAccess.Kind != FastAccessHash || storeAccess.SlotIndex != hashAccess.SlotIndex {
		t.Fatalf("unexpected hash set fast access: %+v ok=%t", storeAccess, ok)
	}
	blockedHandle, err := strings.Intern("meta")
	if err != nil {
		t.Fatalf("intern metatable key: %v", err)
	}
	if err := tables.SetMetatable(handle.Ref, blockedHandle.Value); err != nil {
		t.Fatalf("set metatable: %v", err)
	}
	if _, ok, err := tables.DescribeFastGet(handle.Ref, keyHandle.Value); err != nil {
		t.Fatalf("describe blocked hash get: %v", err)
	} else if ok {
		t.Fatalf("blocked table should not expose fast get access")
	}
}

func TestTableUsesSingleCanonicalBytes(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0x87654321)
	tables := NewStore(runtimeHeap)
	handle, err := tables.New(0, 0)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	keyHandle, err := strings.Intern("answer")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	if err := tables.Set(handle.Ref, value.NumberValue(1), value.NumberValue(10)); err != nil {
		t.Fatalf("set array value: %v", err)
	}
	if err := tables.Set(handle.Ref, keyHandle.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("set hash value: %v", err)
	}
	object, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table object: %v", err)
	}
	logicalObject, err := runtimeHeap.Resolve(mustOffsetForRef(t, runtimeHeap, handle.Ref), ObjectSize)
	if err != nil {
		t.Fatalf("resolve canonical object: %v", err)
	}
	nativeObjectAddress, err := runtimeHeap.NativeAddressForOffset(mustOffsetForRef(t, runtimeHeap, handle.Ref))
	if err != nil {
		t.Fatalf("resolve native object address: %v", err)
	}
	if uintptr(unsafe.Pointer(&logicalObject[0])) != nativeObjectAddress {
		t.Fatalf("table object bytes base %#x, want %#x", uintptr(unsafe.Pointer(&logicalObject[0])), nativeObjectAddress)
	}
	decodedObject, err := ReadObject(logicalObject)
	if err != nil {
		t.Fatalf("read canonical object: %v", err)
	}
	if decodedObject.ArrayData != object.ArrayData || decodedObject.EntriesData != object.EntriesData {
		t.Fatalf("native object layout mismatch: native=%+v logical=%+v", decodedObject, object)
	}
	arrayBytes, err := runtimeHeap.Resolve(object.ArrayData, uint64(object.ArrayCap)*value.TValueSize)
	if err != nil {
		t.Fatalf("resolve array region: %v", err)
	}
	arrayAddress, err := runtimeHeap.NativeAddressForOffset(object.ArrayData)
	if err != nil {
		t.Fatalf("resolve array native address: %v", err)
	}
	if uintptr(unsafe.Pointer(&arrayBytes[0])) != arrayAddress {
		t.Fatalf("table array bytes base %#x, want %#x", uintptr(unsafe.Pointer(&arrayBytes[0])), arrayAddress)
	}
	if got := nativeTValueAt(arrayBytes, 0); got.Bits() != value.NumberValue(10).Bits() {
		t.Fatalf("native array slot = %s, want %s", got, value.NumberValue(10))
	}
	entriesBytes, err := runtimeHeap.Resolve(object.EntriesData, uint64(object.HashCapacity)*EntrySize)
	if err != nil {
		t.Fatalf("resolve entries region: %v", err)
	}
	entriesAddress, err := runtimeHeap.NativeAddressForOffset(object.EntriesData)
	if err != nil {
		t.Fatalf("resolve entries native address: %v", err)
	}
	if uintptr(unsafe.Pointer(&entriesBytes[0])) != entriesAddress {
		t.Fatalf("table entries bytes base %#x, want %#x", uintptr(unsafe.Pointer(&entriesBytes[0])), entriesAddress)
	}
	ctrlBytes, err := runtimeHeap.Resolve(object.CtrlData, uint64(object.HashCapacity)+1)
	if err != nil {
		t.Fatalf("resolve ctrl region: %v", err)
	}
	ctrlAddress, err := runtimeHeap.NativeAddressForOffset(object.CtrlData)
	if err != nil {
		t.Fatalf("resolve ctrl native address: %v", err)
	}
	if uintptr(unsafe.Pointer(&ctrlBytes[0])) != ctrlAddress {
		t.Fatalf("table ctrl bytes base %#x, want %#x", uintptr(unsafe.Pointer(&ctrlBytes[0])), ctrlAddress)
	}
	if !nativeHashContains(t, entriesBytes, ctrlBytes, object.HashCapacity, keyHandle.Value, value.NumberValue(42)) {
		t.Fatalf("native hash region missing expected entry")
	}
	if err := tables.Set(handle.Ref, keyHandle.Value, value.NumberValue(84)); err != nil {
		t.Fatalf("overwrite hash value: %v", err)
	}
	entriesBytes, err = runtimeHeap.Resolve(object.EntriesData, uint64(object.HashCapacity)*EntrySize)
	if err != nil {
		t.Fatalf("resolve entries after overwrite: %v", err)
	}
	if !nativeHashContains(t, entriesBytes, ctrlBytes, object.HashCapacity, keyHandle.Value, value.NumberValue(84)) {
		t.Fatalf("native hash region did not reflect overwritten value")
	}
}

func TestTableNativeAddressesStayStableAcrossHeapGrowthAndGC(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	strings := rtstring.NewInternTable(runtimeHeap, 0xABCDEF01)
	tables := NewStore(runtimeHeap)
	handle, err := tables.New(0, 0)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	keyHandle, err := strings.Intern("answer")
	if err != nil {
		t.Fatalf("intern key: %v", err)
	}
	if err := tables.Set(handle.Ref, value.NumberValue(1), value.NumberValue(10)); err != nil {
		t.Fatalf("set array value: %v", err)
	}
	if err := tables.Set(handle.Ref, keyHandle.Value, value.NumberValue(42)); err != nil {
		t.Fatalf("set hash value: %v", err)
	}
	object, err := tables.Object(handle.Ref)
	if err != nil {
		t.Fatalf("read table object: %v", err)
	}
	arrayAddrBefore, err := runtimeHeap.NativeAddressForOffset(object.ArrayData)
	if err != nil {
		t.Fatalf("native array address: %v", err)
	}
	ctrlAddrBefore, err := runtimeHeap.NativeAddressForOffset(object.CtrlData)
	if err != nil {
		t.Fatalf("native ctrl address: %v", err)
	}
	entriesAddrBefore, err := runtimeHeap.NativeAddressForOffset(object.EntriesData)
	if err != nil {
		t.Fatalf("native entries address: %v", err)
	}
	for index := 0; index < 512; index++ {
		grown, err := tables.New(0, 0)
		if err != nil {
			t.Fatalf("grow table %d: %v", index, err)
		}
		key, err := strings.Intern(fmt.Sprintf("grow-%03d", index))
		if err != nil {
			t.Fatalf("intern grow key %d: %v", index, err)
		}
		if err := tables.Set(grown.Ref, value.NumberValue(1), value.NumberValue(float64(index))); err != nil {
			t.Fatalf("set grow array %d: %v", index, err)
		}
		if err := tables.Set(grown.Ref, key.Value, value.NumberValue(float64(index))); err != nil {
			t.Fatalf("set grow hash %d: %v", index, err)
		}
	}
	runtime.GC()
	arrayAddrAfter, err := runtimeHeap.NativeAddressForOffset(object.ArrayData)
	if err != nil {
		t.Fatalf("native array address after growth: %v", err)
	}
	ctrlAddrAfter, err := runtimeHeap.NativeAddressForOffset(object.CtrlData)
	if err != nil {
		t.Fatalf("native ctrl address after growth: %v", err)
	}
	entriesAddrAfter, err := runtimeHeap.NativeAddressForOffset(object.EntriesData)
	if err != nil {
		t.Fatalf("native entries address after growth: %v", err)
	}
	if arrayAddrBefore != arrayAddrAfter || ctrlAddrBefore != ctrlAddrAfter || entriesAddrBefore != entriesAddrAfter {
		t.Fatalf("native addresses changed across heap growth/GC: array %#x->%#x ctrl %#x->%#x entries %#x->%#x", arrayAddrBefore, arrayAddrAfter, ctrlAddrBefore, ctrlAddrAfter, entriesAddrBefore, entriesAddrAfter)
	}
	arrayBytes, err := runtimeHeap.Resolve(object.ArrayData, uint64(object.ArrayCap)*value.TValueSize)
	if err != nil {
		t.Fatalf("resolve array after growth: %v", err)
	}
	if got := nativeTValueAt(arrayBytes, 0); got.Bits() != value.NumberValue(10).Bits() {
		t.Fatalf("native array slot after growth = %s, want %s", got, value.NumberValue(10))
	}
}

func mustOffsetForRef(t *testing.T, runtimeHeap *heap.Heap, ref value.HeapRef44) value.HeapOff64 {
	t.Helper()
	address, err := runtimeHeap.DecodeHeapRef(ref)
	if err != nil {
		t.Fatalf("decode heap ref %#x: %v", uint64(ref), err)
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		t.Fatalf("offset for heap ref %#x: %v", uint64(ref), err)
	}
	return offset
}

func nativeHashContains(t *testing.T, entries []byte, ctrl []byte, capacity uint32, key value.TValue, want value.TValue) bool {
	t.Helper()
	for slot := uint32(0); slot < capacity; slot++ {
		switch ctrl[slot] {
		case CtrlEmpty, CtrlDeleted, CtrlSentinel:
			continue
		}
		start := int(slot) * EntrySize
		entry, err := ReadEntry(entries[start : start+EntrySize])
		if err != nil {
			t.Fatalf("read native entry %d: %v", slot, err)
		}
		if entry.Key.Bits() == key.Bits() && entry.Value.Bits() == want.Bits() {
			return true
		}
	}
	return false
}

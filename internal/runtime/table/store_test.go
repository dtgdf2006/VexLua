package table

import (
	"encoding/binary"
	"fmt"
	"testing"

	"vexlua/internal/runtime/heap"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
)

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

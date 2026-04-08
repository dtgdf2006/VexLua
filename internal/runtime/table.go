package runtime

import (
	"fmt"
	"math"
)

type FieldCache struct {
	Valid   bool
	Table   Handle
	Version uint32
	Slot    uint32
	Symbol  uint32
}

type Table struct {
	array   []Value
	hash    map[uint64]Value
	slots   map[uint32]uint32
	keys    []uint32
	fields  []Value
	meta    Value
	shape   uint32
	version uint32
}

func newTable(capacity int) Table {
	return Table{
		array:   make([]Value, 0, capacity),
		hash:    make(map[uint64]Value, capacity),
		slots:   make(map[uint32]uint32, capacity),
		keys:    make([]uint32, 0, capacity),
		fields:  make([]Value, 0, capacity),
		meta:    NilValue,
		shape:   1,
		version: 1,
	}
}

func (t *Table) Shape() uint32 {
	return t.shape
}

func (t *Table) Version() uint32 {
	return t.version
}

func (t *Table) Metatable() Value {
	return t.meta
}

func (t *Table) SetMetatable(meta Value) {
	t.meta = meta
	t.version++
}

func (t *Table) GetSlot(slot uint32) (Value, bool) {
	if int(slot) >= len(t.fields) {
		return NilValue, false
	}
	value := t.fields[slot]
	if value.Kind() == KindNil {
		return NilValue, false
	}
	return value, true
}

func (t *Table) GetSymbol(sym uint32) (Value, uint32, bool) {
	slot, ok := t.slots[sym]
	if !ok {
		return NilValue, 0, false
	}
	if int(slot) >= len(t.fields) {
		return NilValue, 0, false
	}
	value := t.fields[slot]
	if value.Kind() == KindNil {
		return NilValue, slot, false
	}
	return value, slot, true
}

func (t *Table) SetSymbol(sym uint32, v Value) uint32 {
	if slot, ok := t.slots[sym]; ok {
		if int(slot) >= len(t.fields) {
			grown := make([]Value, int(slot)+1)
			copy(grown, t.fields)
			fillNilValues(grown[len(t.fields):])
			t.fields = grown
		}
		t.fields[slot] = v
		t.version++
		return slot
	}
	slot := uint32(len(t.fields))
	t.slots[sym] = slot
	t.keys = append(t.keys, sym)
	t.fields = append(t.fields, v)
	t.shape++
	t.version++
	return slot
}

func growArrayCapacity(current int, required int) int {
	if required <= current {
		return current
	}
	if current < 1 {
		current = 1
	}
	newCap := current
	for newCap < required {
		if newCap < 1024 {
			newCap *= 2
		} else {
			newCap += newCap / 2
		}
	}
	return newCap
}

func fillNilValues(values []Value) {
	for i := range values {
		values[i] = NilValue
	}
}

func (t *Table) ensureArrayLength(index int) {
	if index <= len(t.array) {
		return
	}
	if index > cap(t.array) {
		grown := make([]Value, len(t.array), growArrayCapacity(cap(t.array), index))
		copy(grown, t.array)
		t.array = grown
	}
	oldLen := len(t.array)
	t.array = t.array[:index]
	fillNilValues(t.array[oldLen:index])
}

func (t *Table) trimArrayTail() {
	newLen := len(t.array)
	for newLen > 0 && t.array[newLen-1].Kind() == KindNil {
		newLen--
	}
	t.array = t.array[:newLen]
}

func (t *Table) SetIndex(index int, v Value) {
	if index <= 0 {
		t.hash[uint64(index)] = v
		t.version++
		return
	}
	if index > len(t.array) && v.Kind() == KindNil {
		return
	}
	t.ensureArrayLength(index)
	t.array[index-1] = v
	if v.Kind() == KindNil {
		t.trimArrayTail()
	}
	t.version++
}

func (t *Table) GetIndex(index int) (Value, bool) {
	if index > 0 && index <= len(t.array) {
		value := t.array[index-1]
		if value.Kind() == KindNil {
			return NilValue, false
		}
		return value, true
	}
	v, ok := t.hash[uint64(index)]
	if ok && v.Kind() == KindNil {
		return NilValue, false
	}
	return v, ok
}

func (t *Table) Length() int {
	length := 0
	for i := 0; i < len(t.array); i++ {
		if t.array[i].Kind() == KindNil {
			break
		}
		length = i + 1
	}
	return length
}

func (t *Table) MaxNumericKey() float64 {
	maxKey := 0.0
	for index, value := range t.array {
		if value.Kind() == KindNil {
			continue
		}
		maxKey = float64(index + 1)
	}
	for bits, value := range t.hash {
		if value.Kind() == KindNil {
			continue
		}
		key := Value(bits)
		if !key.IsNumber() {
			continue
		}
		number := key.Number()
		if number > maxKey {
			maxKey = number
		}
	}
	return maxKey
}

func (t *Table) RawGet(key Value) (Value, bool) {
	if key.IsNumber() {
		n := key.Number()
		if n > 0 && math.Trunc(n) == n {
			return t.GetIndex(int(n))
		}
	}
	v, ok := t.hash[uint64(key)]
	if ok && v.Kind() == KindNil {
		return NilValue, false
	}
	return v, ok
}

func (t *Table) RawSet(key Value, value Value) {
	if key.IsNumber() {
		n := key.Number()
		if n > 0 && math.Trunc(n) == n {
			t.SetIndex(int(n), value)
			return
		}
	}
	t.hash[uint64(key)] = value
	t.version++
}

func (t *Table) String() string {
	return fmt.Sprintf("table(shape=%d, version=%d)", t.shape, t.version)
}

func (t *Table) ApproxBytes() int64 {
	return int64(len(t.array))*8 + int64(len(t.hash))*24 + int64(len(t.slots))*16 + int64(len(t.keys))*4 + int64(len(t.fields))*8 + 64
}

func (t *Table) nextArrayEntry(after int) (int, Value, bool) {
	for index := after + 1; index <= len(t.array); index++ {
		value := t.array[index-1]
		if value.Kind() == KindNil {
			continue
		}
		return index, value, true
	}
	return 0, NilValue, false
}

func (t *Table) nextSymbolEntry(start int) (uint32, Value, bool) {
	for i := start; i < len(t.keys); i++ {
		sym := t.keys[i]
		value, _, found := t.GetSymbol(sym)
		if !found || value.Kind() == KindNil {
			continue
		}
		return sym, value, true
	}
	return 0, NilValue, false
}

func (t *Table) nextSymbolAfter(sym uint32) (uint32, Value, bool) {
	for i, key := range t.keys {
		if key != sym {
			continue
		}
		return t.nextSymbolEntry(i + 1)
	}
	return 0, NilValue, false
}

func (t *Table) hasSymbol(sym uint32) bool {
	_, ok := t.slots[sym]
	return ok
}

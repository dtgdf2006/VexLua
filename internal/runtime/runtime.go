package runtime

import (
	"fmt"
	"math"
)

type Runtime struct {
	heap        Heap
	symbolIDs   map[string]uint32
	symbolNames []string
	stringIDs   map[string]Handle
	globals     Handle
}

func NewRuntime() *Runtime {
	rt := &Runtime{
		symbolIDs:   make(map[string]uint32, 64),
		symbolNames: make([]string, 0, 64),
		stringIDs:   make(map[string]Handle, 64),
	}
	rt.globals = rt.heap.NewTable(16)
	return rt
}

func (rt *Runtime) Heap() *Heap {
	return &rt.heap
}

func (rt *Runtime) InternSymbol(name string) uint32 {
	if id, ok := rt.symbolIDs[name]; ok {
		return id
	}
	id := uint32(len(rt.symbolNames))
	rt.symbolIDs[name] = id
	rt.symbolNames = append(rt.symbolNames, name)
	return id
}

func (rt *Runtime) SymbolName(sym uint32) string {
	if int(sym) >= len(rt.symbolNames) {
		return fmt.Sprintf("sym#%d", sym)
	}
	return rt.symbolNames[sym]
}

func (rt *Runtime) GlobalsHandle() Handle {
	return rt.globals
}

func (rt *Runtime) Globals() *Table {
	return rt.heap.Table(rt.globals)
}

func (rt *Runtime) SetGlobal(name string, value Value) {
	rt.SetGlobalSymbol(rt.InternSymbol(name), value)
}

func (rt *Runtime) SetGlobalSymbol(sym uint32, value Value) {
	rt.Globals().SetSymbol(sym, value)
}

func (rt *Runtime) GetGlobalSymbol(sym uint32) (Value, bool) {
	value, _, ok := rt.Globals().GetSymbol(sym)
	return value, ok
}

func (rt *Runtime) StringValue(v string) Value {
	if handle, ok := rt.stringIDs[v]; ok {
		return HandleValue(handle)
	}
	handle := rt.heap.NewString(v)
	rt.stringIDs[v] = handle
	return HandleValue(handle)
}

func (rt *Runtime) NewTableValue(capacity int) Value {
	return HandleValue(rt.heap.NewTable(capacity))
}

func (rt *Runtime) ToString(v Value) (string, bool) {
	h, ok := v.Handle()
	if !ok || h.Kind() != ObjectString {
		return "", false
	}
	return rt.heap.String(h), true
}

func (rt *Runtime) GetMetatable(target Value) (Value, bool) {
	h, ok := target.Handle()
	if !ok {
		return NilValue, false
	}
	switch h.Kind() {
	case ObjectTable:
		meta := rt.heap.Table(h).Metatable()
		if meta.Kind() == KindNil {
			return NilValue, false
		}
		return meta, true
	default:
		return NilValue, false
	}
}

func (rt *Runtime) SetMetatable(target Value, meta Value) error {
	h, ok := target.Handle()
	if !ok || h.Kind() != ObjectTable {
		return fmt.Errorf("setmetatable expects table")
	}
	if meta.Kind() != KindNil {
		mh, ok := meta.Handle()
		if !ok || mh.Kind() != ObjectTable {
			return fmt.Errorf("setmetatable expects table or nil metatable")
		}
	}
	rt.heap.Table(h).SetMetatable(meta)
	return nil
}

func (rt *Runtime) GetMetafield(target Value, name string) (Value, bool) {
	meta, ok := rt.GetMetatable(target)
	if !ok {
		return NilValue, false
	}
	h, ok := meta.Handle()
	if !ok || h.Kind() != ObjectTable {
		return NilValue, false
	}
	value, _, found := rt.heap.Table(h).GetSymbol(rt.InternSymbol(name))
	return value, found
}

func (rt *Runtime) GetField(target Value, symbol uint32) (Value, uint32, bool, error) {
	h, ok := target.Handle()
	if !ok {
		return NilValue, 0, false, fmt.Errorf("attempt to index non-object value %s", target)
	}
	switch h.Kind() {
	case ObjectTable:
		value, slot, found := rt.heap.Table(h).GetSymbol(symbol)
		return value, slot, found, nil
	case ObjectHostProxy:
		proxy := rt.heap.HostProxy(h)
		value, found, err := proxy.Adapter.GetField(rt, proxy.Subject, rt.SymbolName(symbol))
		return value, 0, found, err
	default:
		return NilValue, 0, false, fmt.Errorf("attempt to index unsupported object kind %s", h.Kind())
	}
}

func (rt *Runtime) GetTable(target Value, key Value) (Value, bool, error) {
	h, ok := target.Handle()
	if !ok {
		return NilValue, false, fmt.Errorf("attempt to index non-object value %s", target)
	}
	switch h.Kind() {
	case ObjectTable:
		if name, ok := rt.ToString(key); ok {
			value, _, found := rt.heap.Table(h).GetSymbol(rt.InternSymbol(name))
			if found {
				return value, true, nil
			}
		}
		value, found := rt.heap.Table(h).RawGet(key)
		return value, found, nil
	case ObjectHostProxy:
		if name, ok := rt.ToString(key); ok {
			proxy := rt.heap.HostProxy(h)
			value, found, err := proxy.Adapter.GetField(rt, proxy.Subject, name)
			return value, found, err
		}
		return NilValue, false, fmt.Errorf("host proxy index expects string key")
	default:
		return NilValue, false, fmt.Errorf("attempt to index unsupported object kind %s", h.Kind())
	}
}

func (rt *Runtime) SetTable(target Value, key Value, value Value) error {
	h, ok := target.Handle()
	if !ok {
		return fmt.Errorf("attempt to assign table key on non-object value %s", target)
	}
	switch h.Kind() {
	case ObjectTable:
		if name, ok := rt.ToString(key); ok {
			rt.heap.Table(h).SetSymbol(rt.InternSymbol(name), value)
			return nil
		}
		rt.heap.Table(h).RawSet(key, value)
		return nil
	case ObjectHostProxy:
		if name, ok := rt.ToString(key); ok {
			proxy := rt.heap.HostProxy(h)
			return proxy.Adapter.SetField(rt, proxy.Subject, name, value)
		}
		return fmt.Errorf("host proxy assignment expects string key")
	default:
		return fmt.Errorf("attempt to assign table key on unsupported object kind %s", h.Kind())
	}
}

func (rt *Runtime) GetFieldCached(target Value, cache *FieldCache) (Value, bool, error) {
	h, ok := target.Handle()
	if !ok {
		return NilValue, false, fmt.Errorf("attempt to index non-object value %s", target)
	}
	if cache != nil && cache.Valid && h == cache.Table && h.Kind() == ObjectTable {
		table := rt.heap.Table(h)
		if table.Version() == cache.Version {
			value, found := table.GetSlot(cache.Slot)
			return value, found, nil
		}
	}
	value, slot, found, err := rt.GetField(target, cache.Symbol)
	if err != nil {
		return NilValue, false, err
	}
	if found && h.Kind() == ObjectTable && cache != nil {
		cache.Valid = true
		cache.Table = h
		cache.Version = rt.heap.Table(h).Version()
		cache.Slot = slot
	}
	return value, found, nil
}

func (rt *Runtime) SetField(target Value, symbol uint32, value Value) error {
	h, ok := target.Handle()
	if !ok {
		return fmt.Errorf("attempt to assign field on non-object value %s", target)
	}
	switch h.Kind() {
	case ObjectTable:
		rt.heap.Table(h).SetSymbol(symbol, value)
		return nil
	case ObjectHostProxy:
		proxy := rt.heap.HostProxy(h)
		return proxy.Adapter.SetField(rt, proxy.Subject, rt.SymbolName(symbol), value)
	default:
		return fmt.Errorf("attempt to assign field on unsupported object kind %s", h.Kind())
	}
}

func (rt *Runtime) Next(target Value, key Value) (Value, Value, bool, error) {
	h, ok := target.Handle()
	if !ok || h.Kind() != ObjectTable {
		return NilValue, NilValue, false, fmt.Errorf("next expects table")
	}
	table := rt.heap.Table(h)
	if nextKey, nextValue, found, handled, err := rt.nextFast(table, key); handled {
		return nextKey, nextValue, found, err
	}
	return rt.nextSlow(table, key)
}

func (rt *Runtime) nextFast(table *Table, key Value) (Value, Value, bool, bool, error) {
	hasHash := len(table.hash) > 0
	if key.Kind() == KindNil {
		if nextIndex, value, found := table.nextArrayEntry(0); found {
			return NumberValue(float64(nextIndex)), value, true, true, nil
		}
		if nextSym, value, found := table.nextSymbolEntry(0); found {
			return rt.StringValue(rt.SymbolName(nextSym)), value, true, true, nil
		}
		if hasHash {
			return rt.firstHashEntry(table)
		}
		return NilValue, NilValue, false, true, nil
	}
	if key.IsNumber() {
		n := key.Number()
		if n > 0 && math.Trunc(n) == n {
			if nextIndex, value, found := table.nextArrayEntry(int(n)); found {
				return NumberValue(float64(nextIndex)), value, true, true, nil
			}
			if nextSym, value, found := table.nextSymbolEntry(0); found {
				return rt.StringValue(rt.SymbolName(nextSym)), value, true, true, nil
			}
			if hasHash {
				return rt.firstHashEntry(table)
			}
			return NilValue, NilValue, false, true, nil
		}
	}
	if name, ok := rt.ToString(key); ok {
		sym := rt.InternSymbol(name)
		if nextSym, value, found := table.nextSymbolAfter(sym); found {
			return rt.StringValue(rt.SymbolName(nextSym)), value, true, true, nil
		}
		if hasHash {
			return rt.firstHashEntry(table)
		}
		if table.hasSymbol(sym) {
			return NilValue, NilValue, false, true, nil
		}
		return NilValue, NilValue, false, true, fmt.Errorf("invalid key to 'next'")
	}
	if !hasHash {
		return NilValue, NilValue, false, true, fmt.Errorf("invalid key to 'next'")
	}
	return NilValue, NilValue, false, false, nil
}

func (rt *Runtime) nextSlow(table *Table, key Value) (Value, Value, bool, error) {
	keys := make([]Value, 0, len(table.array)+len(table.keys)+len(table.hash))
	values := make([]Value, 0, cap(keys))
	for i, value := range table.array {
		if value.Kind() == KindNil {
			continue
		}
		keys = append(keys, NumberValue(float64(i+1)))
		values = append(values, value)
	}
	for _, sym := range table.keys {
		value, _, found := table.GetSymbol(sym)
		if !found || value.Kind() == KindNil {
			continue
		}
		keys = append(keys, rt.StringValue(rt.SymbolName(sym)))
		values = append(values, value)
	}
	for rawKey, value := range table.hash {
		if value.Kind() == KindNil {
			continue
		}
		keys = append(keys, Value(rawKey))
		values = append(values, value)
	}
	if key.Kind() == KindNil {
		if len(keys) == 0 {
			return NilValue, NilValue, false, nil
		}
		return keys[0], values[0], true, nil
	}
	for i, candidate := range keys {
		if candidate == key {
			if i+1 >= len(keys) {
				return NilValue, NilValue, false, nil
			}
			return keys[i+1], values[i+1], true, nil
		}
	}
	return NilValue, NilValue, false, fmt.Errorf("invalid key to 'next'")
}

func (rt *Runtime) firstHashEntry(table *Table) (Value, Value, bool, bool, error) {
	for rawKey, value := range table.hash {
		if value.Kind() == KindNil {
			continue
		}
		return Value(rawKey), value, true, true, nil
	}
	return NilValue, NilValue, false, true, nil
}

func (rt *Runtime) CallValue(callee Value, args []Value) (Value, error) {
	results, err := rt.CallValueMulti(callee, args)
	if err != nil {
		return NilValue, err
	}
	if len(results) == 0 {
		return NilValue, nil
	}
	return results[0], nil
}

func (rt *Runtime) CallValueMulti(callee Value, args []Value) ([]Value, error) {
	h, ok := callee.Handle()
	if !ok {
		return nil, fmt.Errorf("attempt to call non-callable value %s", callee)
	}
	switch h.Kind() {
	case ObjectHostFunction:
		host := rt.heap.HostFunction(h)
		if host.CallMulti != nil {
			return host.CallMulti(rt, args)
		}
		result, err := host.Call(rt, args)
		if err != nil {
			return nil, err
		}
		return []Value{result}, nil
	default:
		return nil, fmt.Errorf("attempt to call unsupported object kind %s", h.Kind())
	}
}

package runtime

import "fmt"

type ObjectKind uint8

const (
	ObjectString ObjectKind = iota + 1
	ObjectTable
	ObjectHostFunction
	ObjectHostProxy
	ObjectLuaClosure
)

type Handle uint64

const handleKindShift = 32

func makeHandle(kind ObjectKind, index uint32) Handle {
	return Handle(uint64(kind)<<handleKindShift | uint64(index))
}

func (h Handle) Kind() ObjectKind {
	return ObjectKind(uint64(h) >> handleKindShift)
}

func (h Handle) Index() uint32 {
	return uint32(h)
}

func (h Handle) String() string {
	return fmt.Sprintf("<%s:%d>", h.Kind(), h.Index())
}

func (k ObjectKind) String() string {
	switch k {
	case ObjectString:
		return "string"
	case ObjectTable:
		return "table"
	case ObjectHostFunction:
		return "host-func"
	case ObjectHostProxy:
		return "host-proxy"
	case ObjectLuaClosure:
		return "lua-closure"
	default:
		return "object"
	}
}

type Heap struct {
	strings       []string
	tables        []*Table
	hostFunctions []HostFunction
	hostProxies   []HostProxy
	luaClosures   []any
}

func (h *Heap) NewString(v string) Handle {
	idx := uint32(len(h.strings))
	h.strings = append(h.strings, v)
	return makeHandle(ObjectString, idx)
}

func (h *Heap) String(handle Handle) string {
	return h.strings[handle.Index()]
}

func (h *Heap) NewTable(capacity int) Handle {
	idx := uint32(len(h.tables))
	table := newTable(capacity)
	h.tables = append(h.tables, &table)
	return makeHandle(ObjectTable, idx)
}

func (h *Heap) Table(handle Handle) *Table {
	return h.tables[handle.Index()]
}

func (h *Heap) NewHostFunction(fn HostFunction) Handle {
	idx := uint32(len(h.hostFunctions))
	h.hostFunctions = append(h.hostFunctions, fn)
	return makeHandle(ObjectHostFunction, idx)
}

func (h *Heap) HostFunction(handle Handle) *HostFunction {
	return &h.hostFunctions[handle.Index()]
}

func (h *Heap) NewHostProxy(proxy HostProxy) Handle {
	idx := uint32(len(h.hostProxies))
	h.hostProxies = append(h.hostProxies, proxy)
	return makeHandle(ObjectHostProxy, idx)
}

func (h *Heap) HostProxy(handle Handle) *HostProxy {
	return &h.hostProxies[handle.Index()]
}

func (h *Heap) NewLuaClosure(closure any) Handle {
	idx := uint32(len(h.luaClosures))
	h.luaClosures = append(h.luaClosures, closure)
	return makeHandle(ObjectLuaClosure, idx)
}

func (h *Heap) LuaClosure(handle Handle) any {
	return h.luaClosures[handle.Index()]
}

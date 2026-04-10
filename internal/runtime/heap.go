package runtime

import (
	"fmt"
	"unsafe"
)

type ObjectKind uint8

const (
	ObjectString ObjectKind = iota + 1
	ObjectTable
	ObjectHostFunction
	ObjectHostProxy
	ObjectLuaClosure
	ObjectThread
	ObjectUserdata
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
	case ObjectThread:
		return "thread"
	case ObjectUserdata:
		return "userdata"
	default:
		return "object"
	}
}

type ThreadObject struct {
	State any
	Meta  Value
	Env   Value
}

type Userdata struct {
	Value     any
	Meta      Value
	Env       Value
	Finalized bool
}

type Heap struct {
	strings       []string
	tables        []*Table
	hostFunctions []HostFunction
	hostProxies   []*HostProxy
	luaClosures   []any
	threads       []*ThreadObject
	userdatas     []*Userdata
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

func (h *Heap) TablesBase() uintptr {
	if len(h.tables) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&h.tables[0]))
}

func (h *Heap) TablesLen() int {
	return len(h.tables)
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
	h.hostProxies = append(h.hostProxies, &proxy)
	return makeHandle(ObjectHostProxy, idx)
}

func (h *Heap) HostProxy(handle Handle) *HostProxy {
	return h.hostProxies[handle.Index()]
}

func (h *Heap) NewLuaClosure(closure any) Handle {
	idx := uint32(len(h.luaClosures))
	h.luaClosures = append(h.luaClosures, closure)
	return makeHandle(ObjectLuaClosure, idx)
}

func (h *Heap) LuaClosure(handle Handle) any {
	return h.luaClosures[handle.Index()]
}

func (h *Heap) NewThread(state any, env Value) Handle {
	idx := uint32(len(h.threads))
	h.threads = append(h.threads, &ThreadObject{State: state, Meta: NilValue, Env: env})
	return makeHandle(ObjectThread, idx)
}

func (h *Heap) Thread(handle Handle) *ThreadObject {
	return h.threads[handle.Index()]
}

func (h *Heap) NewUserdata(value any, meta Value, env Value) Handle {
	idx := uint32(len(h.userdatas))
	h.userdatas = append(h.userdatas, &Userdata{Value: value, Meta: meta, Env: env})
	return makeHandle(ObjectUserdata, idx)
}

func (h *Heap) Userdata(handle Handle) *Userdata {
	return h.userdatas[handle.Index()]
}

func (h *Heap) ApproxBytes() int64 {
	var total int64
	for _, s := range h.strings {
		total += int64(len(s))
	}
	total += int64(len(h.strings)) * 16
	for _, table := range h.tables {
		if table != nil {
			total += table.ApproxBytes()
		}
	}
	total += int64(len(h.hostFunctions)) * 64
	for _, proxy := range h.hostProxies {
		if proxy != nil {
			total += 96
		}
	}
	for _, closure := range h.luaClosures {
		if closure != nil {
			total += 96
		}
	}
	for _, thread := range h.threads {
		if thread != nil {
			total += 96
		}
	}
	for _, userdata := range h.userdatas {
		if userdata != nil {
			total += 96
		}
	}
	return total
}

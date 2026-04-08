package runtime

import "strings"

type GCRootSource interface {
	AppendGCRoots([]Value) []Value
}

type StaticGCRoots []Value

func (s StaticGCRoots) AppendGCRoots(dst []Value) []Value {
	return append(dst, s...)
}

type gcContext struct {
	rt               *Runtime
	marked           map[Handle]struct{}
	queue            []Handle
	weakTables       []Handle
	pendingFinalizer []Handle
	modeSymbol       uint32
	gcSymbol         uint32
}

func (rt *Runtime) CollectGarbage(extraRoots []Value) []Value {
	gc := &gcContext{
		rt:         rt,
		marked:     make(map[Handle]struct{}, 256),
		queue:      make([]Handle, 0, 256),
		weakTables: make([]Handle, 0, 32),
		modeSymbol: rt.InternSymbol("__mode"),
		gcSymbol:   rt.InternSymbol("__gc"),
	}
	gc.markValue(HandleValue(rt.globals))
	gc.markValue(HandleValue(rt.registry))
	gc.markValue(rt.stringMeta)
	gc.markValue(rt.numberMeta)
	gc.markValue(rt.boolMeta)
	gc.markValue(rt.functionMeta)
	gc.markValue(rt.threadMeta)
	gc.markValue(rt.userdataMeta)
	for _, root := range extraRoots {
		gc.markValue(root)
	}
	gc.processQueue()
	gc.identifyPendingFinalizers()
	gc.processQueue()
	gc.clearWeakTables()
	gc.sweep()
	values := make([]Value, 0, len(gc.pendingFinalizer))
	for _, handle := range gc.pendingFinalizer {
		values = append(values, HandleValue(handle))
	}
	return values
}

func (gc *gcContext) markValue(value Value) {
	handle, ok := value.Handle()
	if !ok || !gc.isLiveHandle(handle) {
		return
	}
	gc.markHandle(handle)
}

func (gc *gcContext) markHandle(handle Handle) {
	if _, seen := gc.marked[handle]; seen {
		return
	}
	if !gc.isLiveHandle(handle) {
		return
	}
	gc.marked[handle] = struct{}{}
	gc.queue = append(gc.queue, handle)
}

func (gc *gcContext) processQueue() {
	for len(gc.queue) > 0 {
		handle := gc.queue[len(gc.queue)-1]
		gc.queue = gc.queue[:len(gc.queue)-1]
		switch handle.Kind() {
		case ObjectTable:
			gc.markTable(handle)
		case ObjectLuaClosure:
			gc.markRootSource(gc.rt.heap.LuaClosure(handle))
		case ObjectThread:
			gc.markThread(handle)
		case ObjectUserdata:
			gc.markUserdata(handle)
		case ObjectHostProxy:
			gc.markHostProxy(handle)
		case ObjectHostFunction:
			gc.markHostFunction(handle)
		}
	}
}

func (gc *gcContext) markTable(handle Handle) {
	table := gc.rt.heap.Table(handle)
	if table == nil {
		return
	}
	gc.markValue(table.meta)
	weakKeys, weakValues := gc.tableWeakMode(table)
	if weakKeys || weakValues {
		gc.weakTables = append(gc.weakTables, handle)
	}
	if !weakValues {
		for _, value := range table.array {
			gc.markValue(value)
		}
		for _, value := range table.fields {
			gc.markValue(value)
		}
	}
	for rawKey, value := range table.hash {
		if !weakKeys {
			gc.markValue(Value(rawKey))
		}
		if !weakValues {
			gc.markValue(value)
		}
	}
}

func (gc *gcContext) markThread(handle Handle) {
	thread := gc.rt.heap.Thread(handle)
	if thread == nil {
		return
	}
	gc.markValue(thread.Meta)
	gc.markValue(thread.Env)
	gc.markRootSource(thread.State)
}

func (gc *gcContext) markUserdata(handle Handle) {
	userdata := gc.rt.heap.Userdata(handle)
	if userdata == nil {
		return
	}
	gc.markValue(userdata.Meta)
	gc.markValue(userdata.Env)
	gc.markRootSource(userdata.Value)
}

func (gc *gcContext) markHostProxy(handle Handle) {
	proxy := gc.rt.heap.HostProxy(handle)
	if proxy == nil {
		return
	}
	gc.markValue(proxy.Meta)
	gc.markValue(proxy.Env)
	gc.markRootSource(proxy.Subject)
}

func (gc *gcContext) markHostFunction(handle Handle) {
	function := gc.rt.heap.HostFunction(handle)
	if function == nil || function.Roots == nil {
		return
	}
	gc.markRootSource(function.Roots)
}

func (gc *gcContext) markRootSource(source any) {
	roots, ok := source.(GCRootSource)
	if !ok || roots == nil {
		return
	}
	buffer := roots.AppendGCRoots(nil)
	for _, value := range buffer {
		gc.markValue(value)
	}
}

func (gc *gcContext) identifyPendingFinalizers() {
	for index := len(gc.rt.heap.userdatas) - 1; index >= 0; index-- {
		userdata := gc.rt.heap.userdatas[index]
		if userdata == nil {
			continue
		}
		handle := makeHandle(ObjectUserdata, uint32(index))
		if gc.isMarked(handle) || userdata.Finalized || !gc.hasUserdataFinalizer(userdata) {
			continue
		}
		userdata.Finalized = true
		gc.pendingFinalizer = append(gc.pendingFinalizer, handle)
		gc.markHandle(handle)
	}
}

func (gc *gcContext) hasUserdataFinalizer(userdata *Userdata) bool {
	metaHandle, ok := userdata.Meta.Handle()
	if !ok || metaHandle.Kind() != ObjectTable {
		return false
	}
	table := gc.rt.heap.Table(metaHandle)
	if table == nil {
		return false
	}
	value, _, found := table.GetSymbol(gc.gcSymbol)
	return found && value.Kind() != KindNil
}

func (gc *gcContext) clearWeakTables() {
	for _, handle := range gc.weakTables {
		table := gc.rt.heap.Table(handle)
		if table == nil {
			continue
		}
		weakKeys, weakValues := gc.tableWeakMode(table)
		if !weakKeys && !weakValues {
			continue
		}
		if weakValues {
			for index, value := range table.array {
				if gc.shouldClearWeakValue(value) {
					table.array[index] = NilValue
					table.version++
				}
			}
			for index, value := range table.fields {
				if gc.shouldClearWeakValue(value) {
					table.fields[index] = NilValue
					table.version++
				}
			}
		}
		for rawKey, value := range table.hash {
			clearKey := weakKeys && gc.shouldClearWeakKey(Value(rawKey))
			clearValue := weakValues && gc.shouldClearWeakValue(value)
			if clearKey || clearValue {
				delete(table.hash, rawKey)
				table.version++
			}
		}
	}
}

func (gc *gcContext) sweep() {
	for index, table := range gc.rt.heap.tables {
		if table == nil {
			continue
		}
		handle := makeHandle(ObjectTable, uint32(index))
		if !gc.isMarked(handle) {
			gc.rt.heap.tables[index] = nil
		}
	}
	for index, closure := range gc.rt.heap.luaClosures {
		if closure == nil {
			continue
		}
		handle := makeHandle(ObjectLuaClosure, uint32(index))
		if !gc.isMarked(handle) {
			gc.rt.heap.luaClosures[index] = nil
		}
	}
	for index, thread := range gc.rt.heap.threads {
		if thread == nil {
			continue
		}
		handle := makeHandle(ObjectThread, uint32(index))
		if !gc.isMarked(handle) {
			gc.rt.heap.threads[index] = nil
		}
	}
	for index, proxy := range gc.rt.heap.hostProxies {
		if proxy == nil {
			continue
		}
		handle := makeHandle(ObjectHostProxy, uint32(index))
		if !gc.isMarked(handle) {
			gc.rt.heap.hostProxies[index] = nil
		}
	}
	for index, userdata := range gc.rt.heap.userdatas {
		if userdata == nil {
			continue
		}
		handle := makeHandle(ObjectUserdata, uint32(index))
		if !gc.isMarked(handle) {
			gc.rt.heap.userdatas[index] = nil
		}
	}
}

func (gc *gcContext) tableWeakMode(table *Table) (bool, bool) {
	metaHandle, ok := table.meta.Handle()
	if !ok || metaHandle.Kind() != ObjectTable {
		return false, false
	}
	meta := gc.rt.heap.Table(metaHandle)
	if meta == nil {
		return false, false
	}
	mode, _, found := meta.GetSymbol(gc.modeSymbol)
	if !found {
		return false, false
	}
	text, ok := gc.rt.ToString(mode)
	if !ok {
		return false, false
	}
	return strings.Contains(text, "k"), strings.Contains(text, "v")
}

func (gc *gcContext) shouldClearWeakKey(value Value) bool {
	return gc.shouldClearWeakValue(value)
}

func (gc *gcContext) shouldClearWeakValue(value Value) bool {
	handle, ok := value.Handle()
	if !ok {
		return false
	}
	if !gc.isWeakCollectable(handle) {
		return false
	}
	return !gc.isMarked(handle)
}

func (gc *gcContext) isMarked(handle Handle) bool {
	_, ok := gc.marked[handle]
	return ok
}

func (gc *gcContext) isWeakCollectable(handle Handle) bool {
	switch handle.Kind() {
	case ObjectTable, ObjectHostFunction, ObjectHostProxy, ObjectLuaClosure, ObjectThread, ObjectUserdata:
		return gc.isLiveHandle(handle)
	default:
		return false
	}
}

func (gc *gcContext) isLiveHandle(handle Handle) bool {
	index := int(handle.Index())
	switch handle.Kind() {
	case ObjectString:
		return index >= 0 && index < len(gc.rt.heap.strings)
	case ObjectTable:
		return index >= 0 && index < len(gc.rt.heap.tables) && gc.rt.heap.tables[index] != nil
	case ObjectHostFunction:
		return index >= 0 && index < len(gc.rt.heap.hostFunctions)
	case ObjectHostProxy:
		return index >= 0 && index < len(gc.rt.heap.hostProxies) && gc.rt.heap.hostProxies[index] != nil
	case ObjectLuaClosure:
		return index >= 0 && index < len(gc.rt.heap.luaClosures) && gc.rt.heap.luaClosures[index] != nil
	case ObjectThread:
		return index >= 0 && index < len(gc.rt.heap.threads) && gc.rt.heap.threads[index] != nil
	case ObjectUserdata:
		return index >= 0 && index < len(gc.rt.heap.userdatas) && gc.rt.heap.userdatas[index] != nil
	default:
		return false
	}
}

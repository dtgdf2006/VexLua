package host

import (
	"fmt"
	"reflect"
	"sync"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

type Handle uint64

type DescriptorKind uint8

const (
	DescriptorKindObject DescriptorKind = iota + 1
	DescriptorKindFunction
)

type Getter func(target any, key string) (any, bool, error)
type Setter func(target any, key string, newValue any) error
type Caller func(target any, args []any) ([]any, error)

type Descriptor struct {
	Name string
	Get  Getter
	Set  Setter
	Call Caller
}

type entry struct {
	handle     Handle
	descriptor *Descriptor
	target     any
	refCount   uint32
	nativeMeta value.HeapOff64
}

type Registry struct {
	heap          *heap.Heap
	mu            sync.RWMutex
	nextHandle    Handle
	nextDescID    uint32
	nextShapeID   uint32
	nextCacheSlot uint32
	entries       map[Handle]*entry
	byTarget      map[uintptr]Handle
	byDescName    map[string]*Descriptor
}

type HostObject struct {
	Ref   value.HeapRef44
	Value value.TValue
}

type HostFunction struct {
	Ref   value.HeapRef44
	Value value.TValue
}

func NewRegistry(runtimeHeap *heap.Heap) *Registry {
	if runtimeHeap == nil {
		panic("host registry requires a heap")
	}
	return &Registry{
		heap:          runtimeHeap,
		nextHandle:    1,
		nextDescID:    1,
		nextShapeID:   1,
		nextCacheSlot: 1,
		entries:       make(map[Handle]*entry),
		byTarget:      make(map[uintptr]Handle),
		byDescName:    make(map[string]*Descriptor),
	}
}

func (registry *Registry) MustRegisterObject(name string, target any) Handle {
	handle, err := registry.RegisterObject(name, target)
	if err != nil {
		panic(err)
	}
	return handle
}

func (registry *Registry) MustRegisterFunction(name string, function any) Handle {
	handle, err := registry.RegisterFunction(name, function)
	if err != nil {
		panic(err)
	}
	return handle
}

func (registry *Registry) RegisterObject(name string, target any) (Handle, error) {
	if target == nil {
		return 0, fmt.Errorf("host object target cannot be nil")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	address, reusable := hostIdentity(target)
	if reusable {
		if existing, ok := registry.byTarget[address]; ok {
			registry.entries[existing].refCount++
			return existing, nil
		}
	}
	descriptor := registry.getOrCreateDescriptorLocked(name, DescriptorKindObject, makeGetter(), makeSetter(), nil)
	handle, err := registry.allocEntryLocked(target, descriptor, DescriptorKindObject, 0, DescriptorFlagIndexable|DescriptorFlagWritable)
	if err != nil {
		return 0, err
	}
	if reusable {
		registry.byTarget[address] = handle
	}
	return handle, nil
}

func (registry *Registry) RegisterFunction(name string, function any) (Handle, error) {
	if function == nil {
		return 0, fmt.Errorf("host function cannot be nil")
	}
	callable := reflect.ValueOf(function)
	if callable.Kind() != reflect.Func {
		return 0, fmt.Errorf("host function must be a Go func, got %T", function)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	flags := DescriptorFlagCallable
	if callable.Type().IsVariadic() {
		flags |= DescriptorFlagVariadic
	}
	descriptor := registry.getOrCreateDescriptorLocked(name, DescriptorKindFunction, nil, nil, makeCaller())
	handle, err := registry.allocEntryLocked(function, descriptor, DescriptorKindFunction, uint16(callable.Type().NumIn()), flags)
	if err != nil {
		return 0, err
	}
	return handle, nil
}

func (registry *Registry) WrapObject(handle Handle, env value.TValue) (HostObject, error) {
	registry.mu.Lock()
	entry, ok := registry.entries[handle]
	if !ok {
		registry.mu.Unlock()
		return HostObject{}, fmt.Errorf("unknown host handle %d", handle)
	}
	native, err := registry.readEntryNativeDescriptorLocked(entry)
	if err != nil {
		registry.mu.Unlock()
		return HostObject{}, err
	}
	if native.Kind != DescriptorKindObject {
		registry.mu.Unlock()
		return HostObject{}, fmt.Errorf("host handle %d is not of expected kind %d", handle, DescriptorKindObject)
	}
	entry.refCount++
	header := newHostObjectHeader(uint64(handle), native.DescriptorVersion, env, entry.nativeMeta)
	registry.mu.Unlock()
	allocation, err := registry.heap.AllocObject(header.Common)
	if err != nil {
		_ = registry.Release(handle)
		return HostObject{}, err
	}
	if err := writeWrapperHeader(allocation.Bytes, header); err != nil {
		_ = registry.Release(handle)
		return HostObject{}, err
	}
	ref, err := registry.heap.EncodeHeapRef(allocation.Address)
	if err != nil {
		_ = registry.Release(handle)
		return HostObject{}, err
	}
	return HostObject{Ref: ref, Value: value.HostObjectRefValue(ref)}, nil
}

func (registry *Registry) WrapFunction(handle Handle, env value.TValue) (HostFunction, error) {
	registry.mu.Lock()
	entry, ok := registry.entries[handle]
	if !ok {
		registry.mu.Unlock()
		return HostFunction{}, fmt.Errorf("unknown host handle %d", handle)
	}
	native, err := registry.readEntryNativeDescriptorLocked(entry)
	if err != nil {
		registry.mu.Unlock()
		return HostFunction{}, err
	}
	if native.Kind != DescriptorKindFunction {
		registry.mu.Unlock()
		return HostFunction{}, fmt.Errorf("host handle %d is not of expected kind %d", handle, DescriptorKindFunction)
	}
	entry.refCount++
	header := newHostFunctionHeader(uint64(handle), native.DescriptorVersion, env, entry.nativeMeta)
	registry.mu.Unlock()
	allocation, err := registry.heap.AllocObject(header.Common)
	if err != nil {
		_ = registry.Release(handle)
		return HostFunction{}, err
	}
	if err := writeWrapperHeader(allocation.Bytes, header); err != nil {
		_ = registry.Release(handle)
		return HostFunction{}, err
	}
	ref, err := registry.heap.EncodeHeapRef(allocation.Address)
	if err != nil {
		_ = registry.Release(handle)
		return HostFunction{}, err
	}
	return HostFunction{Ref: ref, Value: value.HostFunctionRefValue(ref)}, nil
}

func (registry *Registry) ReadHostObject(ref value.HeapRef44) (WrapperHeader, any, *Descriptor, error) {
	header, err := registry.readWrapper(ref, value.KindHostObject, hostObjectSize)
	if err != nil {
		return WrapperHeader{}, nil, nil, err
	}
	entry, err := registry.lookupAny(Handle(header.HostHandle))
	if err != nil {
		return WrapperHeader{}, nil, nil, err
	}
	native, err := registry.readEntryNativeDescriptor(entry)
	if err != nil {
		return WrapperHeader{}, nil, nil, err
	}
	if native.Kind != DescriptorKindObject {
		return WrapperHeader{}, nil, nil, fmt.Errorf("host handle %d is not of expected kind %d", header.HostHandle, DescriptorKindObject)
	}
	return header, entry.target, entry.descriptor, nil
}

func (registry *Registry) ReadHostFunction(ref value.HeapRef44) (WrapperHeader, any, *Descriptor, error) {
	header, err := registry.readWrapper(ref, value.KindHostFunction, hostFunctionSize)
	if err != nil {
		return WrapperHeader{}, nil, nil, err
	}
	entry, err := registry.lookupAny(Handle(header.HostHandle))
	if err != nil {
		return WrapperHeader{}, nil, nil, err
	}
	native, err := registry.readEntryNativeDescriptor(entry)
	if err != nil {
		return WrapperHeader{}, nil, nil, err
	}
	if native.Kind != DescriptorKindFunction {
		return WrapperHeader{}, nil, nil, fmt.Errorf("host handle %d is not of expected kind %d", header.HostHandle, DescriptorKindFunction)
	}
	return header, entry.target, entry.descriptor, nil
}

func (registry *Registry) DescriptorVersion(handle Handle) (uint32, error) {
	entry, err := registry.lookupAny(handle)
	if err != nil {
		return 0, err
	}
	native, err := registry.readEntryNativeDescriptor(entry)
	if err != nil {
		return 0, err
	}
	return native.DescriptorVersion, nil
}

func (registry *Registry) BumpDescriptorVersion(handle Handle) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry, ok := registry.entries[handle]
	if !ok {
		return fmt.Errorf("unknown host handle %d", handle)
	}
	native, err := registry.readEntryNativeDescriptorLocked(entry)
	if err != nil {
		return err
	}
	native.DescriptorVersion++
	native.Common.Version = native.DescriptorVersion
	return registry.writeEntryNativeDescriptorLocked(entry, native)
}

func (registry *Registry) Release(handle Handle) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry, ok := registry.entries[handle]
	if !ok {
		return fmt.Errorf("unknown host handle %d", handle)
	}
	if entry.refCount > 0 {
		entry.refCount--
	}
	if entry.refCount > 0 {
		return nil
	}
	address, reusable := hostIdentity(entry.target)
	if reusable {
		delete(registry.byTarget, address)
	}
	delete(registry.entries, handle)
	return nil
}

func (registry *Registry) WalkDescriptorRefs(visit func(value.HeapRef44) error) error {
	if registry == nil || visit == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	for _, entry := range registry.entries {
		if entry == nil || entry.nativeMeta == 0 {
			continue
		}
		address, err := registry.heap.AddressForOffset(entry.nativeMeta)
		if err != nil {
			return err
		}
		ref, err := registry.heap.EncodeHeapRef(address)
		if err != nil {
			return err
		}
		if err := visit(ref); err != nil {
			return err
		}
	}
	return nil
}

func (registry *Registry) allocEntryLocked(target any, descriptor *Descriptor, kind DescriptorKind, arity uint16, flags DescriptorFlags) (Handle, error) {
	handle := registry.nextHandle
	registry.nextHandle++
	nativeMeta, err := registry.allocNativeDescriptorLocked(kind, arity, flags)
	if err != nil {
		return 0, err
	}
	registry.entries[handle] = &entry{
		handle:     handle,
		descriptor: descriptor,
		target:     target,
		refCount:   1,
		nativeMeta: nativeMeta,
	}
	return handle, nil
}

func (registry *Registry) getOrCreateDescriptorLocked(name string, kind DescriptorKind, getter Getter, setter Setter, caller Caller) *Descriptor {
	key := fmt.Sprintf("%d:%s", kind, name)
	if descriptor, ok := registry.byDescName[key]; ok {
		descriptor.Get = getter
		descriptor.Set = setter
		descriptor.Call = caller
		return descriptor
	}
	descriptor := &Descriptor{
		Name: name,
		Get:  getter,
		Set:  setter,
		Call: caller,
	}
	registry.byDescName[key] = descriptor
	return descriptor
}

func (registry *Registry) ReadNativeDescriptor(offset value.HeapOff64) (NativeDescriptor, error) {
	if offset == 0 {
		return NativeDescriptor{}, fmt.Errorf("native descriptor offset cannot be zero")
	}
	bytes, err := registry.heap.Resolve(offset, hostDescriptorSize)
	if err != nil {
		return NativeDescriptor{}, err
	}
	return readNativeDescriptor(bytes)
}

func (registry *Registry) RefreshWrapper(ref value.HeapRef44) (WrapperHeader, error) {
	header, offset, bytes, err := registry.readAnyWrapperBytes(ref)
	if err != nil {
		return WrapperHeader{}, err
	}
	entry, err := registry.lookupAny(Handle(header.HostHandle))
	if err != nil {
		return WrapperHeader{}, err
	}
	native, err := registry.readEntryNativeDescriptor(entry)
	if err != nil {
		return WrapperHeader{}, err
	}
	switch header.Common.Kind {
	case value.KindHostObject:
		if native.Kind != DescriptorKindObject {
			return WrapperHeader{}, fmt.Errorf("host handle %d is not of expected kind %d", header.HostHandle, DescriptorKindObject)
		}
	case value.KindHostFunction:
		if native.Kind != DescriptorKindFunction {
			return WrapperHeader{}, fmt.Errorf("host handle %d is not of expected kind %d", header.HostHandle, DescriptorKindFunction)
		}
	default:
		return WrapperHeader{}, fmt.Errorf("expected host wrapper, got %s", header.Common.Kind)
	}
	expectedFlags := WrapperFlagsForDescriptor(native.Kind, native.Flags)
	if header.DescriptorVersion == native.DescriptorVersion && header.Flags == expectedFlags {
		return header, nil
	}
	header.DescriptorVersion = native.DescriptorVersion
	header.Flags = expectedFlags
	if err := writeWrapperHeader(bytes, header); err != nil {
		return WrapperHeader{}, err
	}
	_ = offset
	return header, nil
}

func (registry *Registry) SetWrapperEnv(ref value.HeapRef44, env value.TValue) (WrapperHeader, error) {
	header, offset, bytes, err := registry.readAnyWrapperBytes(ref)
	if err != nil {
		return WrapperHeader{}, err
	}
	if header.Env.Bits() == env.Bits() {
		return header, nil
	}
	header.Env = env
	if err := writeWrapperHeader(bytes, header); err != nil {
		return WrapperHeader{}, err
	}
	if err := registry.heap.WriteBarrierValueByOffset(offset, env); err != nil {
		return WrapperHeader{}, err
	}
	return header, nil
}

func (registry *Registry) lookupAny(handle Handle) (*entry, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	entry, ok := registry.entries[handle]
	if !ok {
		return nil, fmt.Errorf("unknown host handle %d", handle)
	}
	return entry, nil
}

func (registry *Registry) allocNativeDescriptorLocked(kind DescriptorKind, arity uint16, flags DescriptorFlags) (value.HeapOff64, error) {
	descriptorID := registry.nextDescID
	shapeID := registry.nextShapeID
	cacheSlot := registry.nextCacheSlot
	registry.nextDescID++
	registry.nextShapeID++
	registry.nextCacheSlot++
	native := newNativeDescriptor(descriptorID, 1, shapeID, cacheSlot, arity, flags, kind)
	allocation, err := registry.heap.AllocObject(native.Common)
	if err != nil {
		return 0, err
	}
	if err := writeNativeDescriptor(allocation.Bytes, native); err != nil {
		return 0, err
	}
	return allocation.Offset, nil
}

func (registry *Registry) readEntryNativeDescriptor(entry *entry) (NativeDescriptor, error) {
	if entry == nil {
		return NativeDescriptor{}, fmt.Errorf("entry cannot be nil")
	}
	return registry.ReadNativeDescriptor(entry.nativeMeta)
}

func (registry *Registry) readEntryNativeDescriptorLocked(entry *entry) (NativeDescriptor, error) {
	if entry == nil {
		return NativeDescriptor{}, fmt.Errorf("entry cannot be nil")
	}
	bytes, err := registry.heap.Resolve(entry.nativeMeta, hostDescriptorSize)
	if err != nil {
		return NativeDescriptor{}, err
	}
	return readNativeDescriptor(bytes)
}

func (registry *Registry) writeEntryNativeDescriptorLocked(entry *entry, native NativeDescriptor) error {
	if entry == nil {
		return fmt.Errorf("entry cannot be nil")
	}
	bytes, err := registry.heap.Resolve(entry.nativeMeta, hostDescriptorSize)
	if err != nil {
		return err
	}
	return writeNativeDescriptor(bytes, native)
}

func (registry *Registry) readWrapper(ref value.HeapRef44, expectedKind value.ObjectKind, expectedSize uint32) (WrapperHeader, error) {
	address, err := registry.heap.DecodeHeapRef(ref)
	if err != nil {
		return WrapperHeader{}, err
	}
	offset, err := registry.heap.OffsetForAddress(address)
	if err != nil {
		return WrapperHeader{}, err
	}
	bytes, err := registry.heap.Resolve(offset, uint64(expectedSize))
	if err != nil {
		return WrapperHeader{}, err
	}
	return readWrapperHeader(bytes, expectedKind, expectedSize)
}

func (registry *Registry) readAnyWrapperBytes(ref value.HeapRef44) (WrapperHeader, value.HeapOff64, []byte, error) {
	address, err := registry.heap.DecodeHeapRef(ref)
	if err != nil {
		return WrapperHeader{}, 0, nil, err
	}
	offset, err := registry.heap.OffsetForAddress(address)
	if err != nil {
		return WrapperHeader{}, 0, nil, err
	}
	bytes, err := registry.heap.Resolve(offset, hostObjectSize)
	if err != nil {
		return WrapperHeader{}, 0, nil, err
	}
	common, err := value.ReadCommonHeader(bytes)
	if err != nil {
		return WrapperHeader{}, 0, nil, err
	}
	switch common.Kind {
	case value.KindHostObject:
		header, err := readWrapperHeader(bytes, value.KindHostObject, hostObjectSize)
		return header, offset, bytes, err
	case value.KindHostFunction:
		header, err := readWrapperHeader(bytes, value.KindHostFunction, hostFunctionSize)
		return header, offset, bytes, err
	default:
		return WrapperHeader{}, 0, nil, fmt.Errorf("expected host wrapper, got %s", common.Kind)
	}
}

func hostIdentity(target any) (uintptr, bool) {
	value := reflect.ValueOf(target)
	if !value.IsValid() {
		return 0, false
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		if value.IsNil() {
			return 0, false
		}
		return value.Pointer(), true
	default:
		return 0, false
	}
}

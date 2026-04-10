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
	ID        uint32
	Kind      DescriptorKind
	Version   uint32
	Name      string
	ShapeID   uint32
	CacheSlot uint32
	Arity     uint16
	Flags     DescriptorFlags
	Get       Getter
	Set       Setter
	Call      Caller
}

type entry struct {
	handle     Handle
	descriptor *Descriptor
	target     any
	refCount   uint32
	version    uint32
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
	descriptor := registry.getOrCreateDescriptorLocked(name, DescriptorKindObject, 0, DescriptorFlagIndexable|DescriptorFlagWritable, makeGetter(), makeSetter(), nil)
	handle, err := registry.allocEntryLocked(target, descriptor)
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
	descriptor := registry.getOrCreateDescriptorLocked(name, DescriptorKindFunction, uint16(callable.Type().NumIn()), flags, nil, nil, makeCaller())
	handle, err := registry.allocEntryLocked(function, descriptor)
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
	if entry.descriptor.Kind != DescriptorKindObject {
		registry.mu.Unlock()
		return HostObject{}, fmt.Errorf("host handle %d is not of expected kind %d", handle, DescriptorKindObject)
	}
	entry.refCount++
	header := newHostObjectHeader(uint64(handle), entry.descriptor.ID, entry.version, entry.descriptor.CacheSlot, env, entry.nativeMeta)
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
	if entry.descriptor.Kind != DescriptorKindFunction {
		registry.mu.Unlock()
		return HostFunction{}, fmt.Errorf("host handle %d is not of expected kind %d", handle, DescriptorKindFunction)
	}
	entry.refCount++
	header := newHostFunctionHeader(uint64(handle), entry.descriptor.ID, entry.version, entry.descriptor.CacheSlot, env, entry.nativeMeta)
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
	entry, err := registry.lookup(Handle(header.HostHandle), DescriptorKindObject)
	if err != nil {
		return WrapperHeader{}, nil, nil, err
	}
	return header, entry.target, entry.descriptor, nil
}

func (registry *Registry) ReadHostFunction(ref value.HeapRef44) (WrapperHeader, any, *Descriptor, error) {
	header, err := registry.readWrapper(ref, value.KindHostFunction, hostFunctionSize)
	if err != nil {
		return WrapperHeader{}, nil, nil, err
	}
	entry, err := registry.lookup(Handle(header.HostHandle), DescriptorKindFunction)
	if err != nil {
		return WrapperHeader{}, nil, nil, err
	}
	return header, entry.target, entry.descriptor, nil
}

func (registry *Registry) DescriptorVersion(handle Handle) (uint32, error) {
	entry, err := registry.lookupAny(handle)
	if err != nil {
		return 0, err
	}
	return entry.version, nil
}

func (registry *Registry) BumpDescriptorVersion(handle Handle) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry, ok := registry.entries[handle]
	if !ok {
		return fmt.Errorf("unknown host handle %d", handle)
	}
	entry.version++
	entry.descriptor.Version = entry.version
	return registry.syncEntryNativeDescriptorLocked(entry)
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

func (registry *Registry) allocEntryLocked(target any, descriptor *Descriptor) (Handle, error) {
	handle := registry.nextHandle
	registry.nextHandle++
	nativeMeta, err := registry.allocNativeDescriptorLocked(descriptor, descriptor.Version)
	if err != nil {
		return 0, err
	}
	registry.entries[handle] = &entry{
		handle:     handle,
		descriptor: descriptor,
		target:     target,
		refCount:   1,
		version:    descriptor.Version,
		nativeMeta: nativeMeta,
	}
	return handle, nil
}

func (registry *Registry) getOrCreateDescriptorLocked(name string, kind DescriptorKind, arity uint16, flags DescriptorFlags, getter Getter, setter Setter, caller Caller) *Descriptor {
	key := fmt.Sprintf("%d:%s", kind, name)
	if descriptor, ok := registry.byDescName[key]; ok {
		descriptor.Get = getter
		descriptor.Set = setter
		descriptor.Call = caller
		return descriptor
	}
	descriptor := &Descriptor{
		ID:        registry.nextDescID,
		Kind:      kind,
		Version:   1,
		Name:      name,
		ShapeID:   registry.nextShapeID,
		CacheSlot: registry.nextCacheSlot,
		Arity:     arity,
		Flags:     flags,
		Get:       getter,
		Set:       setter,
		Call:      caller,
	}
	registry.nextDescID++
	registry.nextShapeID++
	registry.nextCacheSlot++
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

func (registry *Registry) lookup(handle Handle, kind DescriptorKind) (*entry, error) {
	entry, err := registry.lookupAny(handle)
	if err != nil {
		return nil, err
	}
	if entry.descriptor.Kind != kind {
		return nil, fmt.Errorf("host handle %d is not of expected kind %d", handle, kind)
	}
	return entry, nil
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

func (registry *Registry) allocNativeDescriptorLocked(descriptor *Descriptor, version uint32) (value.HeapOff64, error) {
	allocation, err := registry.heap.AllocObject(newNativeDescriptor(descriptor.ID, version, descriptor.ShapeID, descriptor.CacheSlot, descriptor.Arity, descriptor.Flags, descriptor.Kind).Common)
	if err != nil {
		return 0, err
	}
	native := newNativeDescriptor(descriptor.ID, version, descriptor.ShapeID, descriptor.CacheSlot, descriptor.Arity, descriptor.Flags, descriptor.Kind)
	if err := writeNativeDescriptor(allocation.Bytes, native); err != nil {
		return 0, err
	}
	return allocation.Offset, nil
}

func (registry *Registry) syncEntryNativeDescriptorLocked(entry *entry) error {
	if entry == nil {
		return fmt.Errorf("entry cannot be nil")
	}
	bytes, err := registry.heap.Resolve(entry.nativeMeta, hostDescriptorSize)
	if err != nil {
		return err
	}
	native := newNativeDescriptor(entry.descriptor.ID, entry.version, entry.descriptor.ShapeID, entry.descriptor.CacheSlot, entry.descriptor.Arity, entry.descriptor.Flags, entry.descriptor.Kind)
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

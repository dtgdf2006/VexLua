package interp

import (
	"fmt"

	"vexlua/internal/runtime/host"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
)

func (engine *Engine) ReadIndexBoundary(targetValue value.TValue, key value.TValue) (value.TValue, bool, error) {
	if targetValue.IsBoxedTag(value.TagHostObjectRef) {
		return engine.readHostIndexBoundary(targetValue, key)
	}
	if !targetValue.IsBoxedTag(value.TagTableRef) {
		return value.NilValue(), false, fmt.Errorf("table operation requires table, got %s", targetValue)
	}
	ref, _ := targetValue.HeapRef()
	return engine.Tables.Get(ref, key)
}

func (engine *Engine) WriteIndexBoundary(targetValue value.TValue, key value.TValue, slotValue value.TValue) error {
	if targetValue.IsBoxedTag(value.TagHostObjectRef) {
		return engine.writeHostIndexBoundary(targetValue, key, slotValue)
	}
	if !targetValue.IsBoxedTag(value.TagTableRef) {
		return fmt.Errorf("table operation requires table, got %s", targetValue)
	}
	ref, _ := targetValue.HeapRef()
	return engine.Tables.Set(ref, key, slotValue)
}

func (engine *Engine) CallValueBoundary(thread *state.ThreadState, callee value.TValue, args []value.TValue, nresults int) ([]value.TValue, error) {
	if callee.IsBoxedTag(value.TagHostFunctionRef) {
		return engine.callHostBoundary(callee, args, nresults)
	}
	if !callee.IsBoxedTag(value.TagLuaClosureRef) {
		return nil, fmt.Errorf("interpreter only supports LuaClosure and HostFunction calls, got %s", callee)
	}
	ref, _ := callee.HeapRef()
	return engine.callLuaClosure(thread, ref, args, nresults)
}

func (engine *Engine) readHostIndexBoundary(objectValue value.TValue, key value.TValue) (value.TValue, bool, error) {
	keyText, err := engine.hostKeyString(key)
	if err != nil {
		return value.NilValue(), false, err
	}
	ref, _ := objectValue.HeapRef()
	header, target, descriptor, err := engine.Hosts.ReadHostObject(ref)
	if err != nil {
		return value.NilValue(), false, err
	}
	_, native, err := engine.resolveHostDescriptor(ref, header)
	if err != nil {
		return value.NilValue(), false, err
	}
	if native.Kind != host.DescriptorKindObject || native.Flags&host.DescriptorFlagIndexable == 0 {
		return value.NilValue(), false, fmt.Errorf("host object metadata is not indexable")
	}
	if descriptor.Get == nil {
		return value.NilValue(), false, fmt.Errorf("host object %q does not support property read", descriptor.Name)
	}
	result, found, err := descriptor.Get(target, keyText)
	if err != nil {
		return value.NilValue(), false, err
	}
	if !found {
		return value.NilValue(), false, nil
	}
	boxed, err := host.FromHostValue(engine.Strings, result)
	if err != nil {
		return value.NilValue(), false, err
	}
	return boxed, true, nil
}

func (engine *Engine) writeHostIndexBoundary(objectValue value.TValue, key value.TValue, slotValue value.TValue) error {
	keyText, err := engine.hostKeyString(key)
	if err != nil {
		return err
	}
	hostValue, err := host.ToHostValue(engine.Strings, slotValue)
	if err != nil {
		return err
	}
	ref, _ := objectValue.HeapRef()
	header, target, descriptor, err := engine.Hosts.ReadHostObject(ref)
	if err != nil {
		return err
	}
	_, native, err := engine.resolveHostDescriptor(ref, header)
	if err != nil {
		return err
	}
	if native.Kind != host.DescriptorKindObject || native.Flags&host.DescriptorFlagWritable == 0 {
		return fmt.Errorf("host object metadata is not writable")
	}
	if descriptor.Set == nil {
		return fmt.Errorf("host object %q does not support property write", descriptor.Name)
	}
	return descriptor.Set(target, keyText, hostValue)
}

func (engine *Engine) callHostBoundary(functionValue value.TValue, args []value.TValue, nresults int) ([]value.TValue, error) {
	ref, _ := functionValue.HeapRef()
	header, target, descriptor, err := engine.Hosts.ReadHostFunction(ref)
	if err != nil {
		return nil, err
	}
	_, native, err := engine.resolveHostDescriptor(ref, header)
	if err != nil {
		return nil, err
	}
	if native.Kind != host.DescriptorKindFunction || native.Flags&host.DescriptorFlagCallable == 0 {
		return nil, fmt.Errorf("host function metadata is not callable")
	}
	if native.Flags&host.DescriptorFlagVariadic == 0 && int(native.Arity) != len(args) {
		return nil, fmt.Errorf("host function expects %d args, got %d", native.Arity, len(args))
	}
	if descriptor.Call == nil {
		return nil, fmt.Errorf("host function %q does not support call", descriptor.Name)
	}
	hostArgs := make([]any, 0, len(args))
	for _, slotValue := range args {
		converted, err := host.ToHostValue(engine.Strings, slotValue)
		if err != nil {
			return nil, err
		}
		hostArgs = append(hostArgs, converted)
	}
	results, err := descriptor.Call(target, hostArgs)
	if err != nil {
		return nil, err
	}
	boxed := make([]value.TValue, 0, len(results))
	for _, result := range results {
		converted, err := host.FromHostValue(engine.Strings, result)
		if err != nil {
			return nil, err
		}
		boxed = append(boxed, converted)
	}
	return normalizeResults(boxed, nresults), nil
}

func (engine *Engine) resolveHostDescriptor(ref value.HeapRef44, header host.WrapperHeader) (host.WrapperHeader, host.NativeDescriptor, error) {
	native, err := engine.Hosts.ReadNativeDescriptor(header.NativeMeta)
	if err != nil {
		return host.WrapperHeader{}, host.NativeDescriptor{}, err
	}
	expectedFlags := host.WrapperFlagsForDescriptor(native.Kind, native.Flags)
	if header.DescriptorVersion == native.DescriptorVersion && header.Flags == expectedFlags {
		return header, native, nil
	}
	header, err = engine.Hosts.RefreshWrapper(ref)
	if err != nil {
		return host.WrapperHeader{}, host.NativeDescriptor{}, err
	}
	native, err = engine.Hosts.ReadNativeDescriptor(header.NativeMeta)
	if err != nil {
		return host.WrapperHeader{}, host.NativeDescriptor{}, err
	}
	return header, native, nil
}

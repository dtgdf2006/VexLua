package interp

import (
	"fmt"

	"vexlua/internal/runtime/host"
	rtmeta "vexlua/internal/runtime/meta"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
)

const exactTagLoopLimit = 100

const (
	metaIndexName    = "__index"
	metaNewIndexName = "__newindex"
	metaCallName     = "__call"
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

func (engine *Engine) ReadIndexMetaBoundary(thread *state.ThreadState, targetValue value.TValue, key value.TValue) (value.TValue, bool, error) {
	currentTarget := targetValue
	for loop := 0; loop < exactTagLoopLimit; loop++ {
		if currentTarget.IsBoxedTag(value.TagTableRef) {
			ref, _ := currentTarget.HeapRef()
			result, found, err := engine.Tables.Get(ref, key)
			if err != nil {
				return value.NilValue(), false, err
			}
			if found {
				return result, true, nil
			}
		}
		metamethod, ok, err := engine.valueMetamethod(currentTarget, metaIndexName)
		if err != nil {
			return value.NilValue(), false, err
		}
		if !ok {
			if currentTarget.IsBoxedTag(value.TagTableRef) {
				return value.NilValue(), false, nil
			}
			return value.NilValue(), false, indexBoundaryTypeError(currentTarget)
		}
		if isCallableBoundaryValue(metamethod) {
			if thread == nil {
				return value.NilValue(), false, fmt.Errorf("thread cannot be nil when calling %s", metaIndexName)
			}
			results, err := engine.CallValueBoundary(thread, metamethod, []value.TValue{currentTarget, key}, 1)
			if err != nil {
				return value.NilValue(), false, err
			}
			result := value.NilValue()
			if len(results) > 0 {
				result = results[0]
			}
			return result, true, nil
		}
		currentTarget = metamethod
	}
	return value.NilValue(), false, fmt.Errorf("loop in gettable")
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

func (engine *Engine) WriteIndexMetaBoundary(thread *state.ThreadState, targetValue value.TValue, key value.TValue, slotValue value.TValue) error {
	currentTarget := targetValue
	for loop := 0; loop < exactTagLoopLimit; loop++ {
		var tableRef value.HeapRef44
		if currentTarget.IsBoxedTag(value.TagTableRef) {
			ref, _ := currentTarget.HeapRef()
			tableRef = ref
			_, found, err := engine.Tables.Get(tableRef, key)
			if err != nil {
				return err
			}
			if found {
				return engine.Tables.Set(tableRef, key, slotValue)
			}
		}
		metamethod, ok, err := engine.valueMetamethod(currentTarget, metaNewIndexName)
		if err != nil {
			return err
		}
		if !ok {
			if tableRef != 0 {
				return engine.Tables.Set(tableRef, key, slotValue)
			}
			return newIndexBoundaryTypeError(currentTarget)
		}
		if isCallableBoundaryValue(metamethod) {
			if thread == nil {
				return fmt.Errorf("thread cannot be nil when calling %s", metaNewIndexName)
			}
			_, err := engine.CallValueBoundary(thread, metamethod, []value.TValue{currentTarget, key, slotValue}, 0)
			return err
		}
		currentTarget = metamethod
	}
	return fmt.Errorf("loop in settable")
}

func (engine *Engine) CallValueBoundary(thread *state.ThreadState, callee value.TValue, args []value.TValue, nresults int) ([]value.TValue, error) {
	resolvedCallee, resolvedArgs, err := engine.resolveCallBoundary(callee, args)
	if err != nil {
		return nil, err
	}
	return engine.CallResolvedBoundary(thread, resolvedCallee, resolvedArgs, nresults)
}

func (engine *Engine) CallResolvedBoundary(thread *state.ThreadState, resolvedCallee value.TValue, resolvedArgs []value.TValue, nresults int) ([]value.TValue, error) {
	if resolvedCallee.IsBoxedTag(value.TagHostFunctionRef) {
		return engine.callHostBoundary(resolvedCallee, resolvedArgs, nresults)
	}
	if !resolvedCallee.IsBoxedTag(value.TagLuaClosureRef) {
		return nil, callBoundaryTypeError(resolvedCallee)
	}
	ref, _ := resolvedCallee.HeapRef()
	return engine.callLuaClosure(thread, ref, resolvedArgs, nresults)
}

func (engine *Engine) ResolveCallBoundary(callee value.TValue, args []value.TValue) (value.TValue, []value.TValue, error) {
	return engine.resolveCallBoundary(callee, args)
}

func (engine *Engine) resolveCallBoundary(callee value.TValue, args []value.TValue) (value.TValue, []value.TValue, error) {
	if isCallableBoundaryValue(callee) {
		return callee, args, nil
	}
	metamethod, ok, err := engine.valueMetamethod(callee, metaCallName)
	if err != nil {
		return value.NilValue(), nil, err
	}
	if !ok || !isCallableBoundaryValue(metamethod) {
		return value.NilValue(), nil, callBoundaryTypeError(callee)
	}
	resolvedArgs := make([]value.TValue, 0, len(args)+1)
	resolvedArgs = append(resolvedArgs, callee)
	resolvedArgs = append(resolvedArgs, args...)
	return metamethod, resolvedArgs, nil
}

func (engine *Engine) tableMetamethodValue(tableRef value.HeapRef44, metamethodName string) (value.TValue, bool, error) {
	metatable, found, err := engine.tableMetatableValue(tableRef)
	if err != nil || !found {
		return value.NilValue(), false, err
	}
	return engine.metamethodFromMetatable(metatable, metamethodName)
}

func (engine *Engine) valueMetamethod(targetValue value.TValue, metamethodName string) (value.TValue, bool, error) {
	if targetValue.IsBoxedTag(value.TagTableRef) {
		ref, _ := targetValue.HeapRef()
		return engine.tableMetamethodValue(ref, metamethodName)
	}
	metatable, found, err := engine.GetMetatableBoundary(targetValue)
	if err != nil || !found {
		return value.NilValue(), false, err
	}
	return engine.metamethodFromMetatable(metatable, metamethodName)
}

func (engine *Engine) tableMetatableValue(tableRef value.HeapRef44) (value.TValue, bool, error) {
	object, err := engine.Tables.Object(tableRef)
	if err != nil {
		return value.NilValue(), false, err
	}
	if object.Metatable.IsBoxedTag(value.TagNil) {
		return value.NilValue(), false, nil
	}
	return object.Metatable, true, nil
}

func (engine *Engine) metamethodFromMetatable(metatable value.TValue, metamethodName string) (value.TValue, bool, error) {
	if !metatable.IsBoxedTag(value.TagTableRef) {
		return value.NilValue(), false, nil
	}
	metaRef, _ := metatable.HeapRef()
	key, err := engine.Strings.Intern(metamethodName)
	if err != nil {
		return value.NilValue(), false, err
	}
	metamethod, found, err := engine.Tables.Get(metaRef, key.Value)
	if err != nil {
		return value.NilValue(), false, err
	}
	if !found {
		return value.NilValue(), false, nil
	}
	return metamethod, true, nil
}

func isCallableBoundaryValue(candidate value.TValue) bool {
	return candidate.IsBoxedTag(value.TagHostFunctionRef) || candidate.IsBoxedTag(value.TagLuaClosureRef)
}

func indexBoundaryTypeError(targetValue value.TValue) error {
	return fmt.Errorf("attempt to index a %s value", rtmeta.TypeName(targetValue))
}

func newIndexBoundaryTypeError(targetValue value.TValue) error {
	return fmt.Errorf("attempt to index a %s value", rtmeta.TypeName(targetValue))
}

func callBoundaryTypeError(targetValue value.TValue) error {
	return fmt.Errorf("attempt to call a %s value", rtmeta.TypeName(targetValue))
}

func (engine *Engine) readHostIndexBoundary(objectValue value.TValue, key value.TValue) (value.TValue, bool, error) {
	keyValue, err := host.ToHostValue(engine.Strings, key)
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
	result, found, err := descriptor.Get(target, keyValue)
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

func (engine *Engine) readHostIndexFallback(objectValue value.TValue, key value.TValue) (value.TValue, bool, bool, error) {
	keyValue, err := host.ToHostValue(engine.Strings, key)
	if err != nil {
		return value.NilValue(), false, true, err
	}
	ref, _ := objectValue.HeapRef()
	header, target, descriptor, err := engine.Hosts.ReadHostObject(ref)
	if err != nil {
		return value.NilValue(), false, false, err
	}
	_, native, err := engine.resolveHostDescriptor(ref, header)
	if err != nil {
		return value.NilValue(), false, false, err
	}
	if native.Kind != host.DescriptorKindObject || native.Flags&host.DescriptorFlagIndexable == 0 || descriptor.Get == nil {
		return value.NilValue(), false, false, nil
	}
	result, found, err := descriptor.Get(target, keyValue)
	if err != nil {
		return value.NilValue(), false, false, nil
	}
	if !found {
		return value.NilValue(), false, true, nil
	}
	boxed, err := host.FromHostValue(engine.Strings, result)
	if err != nil {
		return value.NilValue(), false, false, nil
	}
	return boxed, true, true, nil
}

func (engine *Engine) writeHostIndexBoundary(objectValue value.TValue, key value.TValue, slotValue value.TValue) error {
	keyValue, err := host.ToHostValue(engine.Strings, key)
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
	return descriptor.Set(target, keyValue, hostValue)
}

func (engine *Engine) writeHostIndexFallback(objectValue value.TValue, key value.TValue, slotValue value.TValue) (bool, error) {
	keyValue, err := host.ToHostValue(engine.Strings, key)
	if err != nil {
		return true, err
	}
	hostValue, err := host.ToHostValue(engine.Strings, slotValue)
	if err != nil {
		return true, err
	}
	ref, _ := objectValue.HeapRef()
	header, target, descriptor, err := engine.Hosts.ReadHostObject(ref)
	if err != nil {
		return false, err
	}
	_, native, err := engine.resolveHostDescriptor(ref, header)
	if err != nil {
		return false, err
	}
	if native.Kind != host.DescriptorKindObject || native.Flags&host.DescriptorFlagWritable == 0 || descriptor.Set == nil {
		return false, nil
	}
	err = descriptor.Set(target, keyValue, hostValue)
	if err != nil {
		return false, nil
	}
	return true, nil
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

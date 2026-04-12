package baseline

import (
	"fmt"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/abi"
	"vexlua/internal/vexarc/codecache"
	"vexlua/internal/vexarc/stubs"
)

type Runtime struct {
	Engine      *interp.Engine
	Cache       *codecache.Cache
	compiled    map[value.HeapRef44]*CompiledCode
	sharedStubs *stubManager
	stubCounts  map[stubs.ID]uint64
	deoptCount  uint64
}

func NewRuntime(engine *interp.Engine) *Runtime {
	if engine == nil {
		panic("baseline runtime requires an interpreter engine")
	}
	return &Runtime{
		Engine:     engine,
		Cache:      codecache.New(),
		compiled:   make(map[value.HeapRef44]*CompiledCode),
		stubCounts: make(map[stubs.ID]uint64),
	}
}

func (runtime *Runtime) Close() error {
	var firstErr error
	for _, compiled := range runtime.compiled {
		if err := compiled.Release(runtime.Cache); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if runtime.sharedStubs != nil {
		if err := runtime.sharedStubs.Release(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (runtime *Runtime) Compile(proto *bytecode.Proto) (*CompiledCode, error) {
	if proto == nil {
		return nil, fmt.Errorf("proto cannot be nil")
	}
	handle, err := runtime.Engine.Protos.Intern(proto)
	if err != nil {
		return nil, err
	}
	return runtime.CompileRef(handle.Ref)
}

func (runtime *Runtime) CompileRef(protoRef value.HeapRef44) (*CompiledCode, error) {
	if protoRef == 0 {
		return nil, fmt.Errorf("proto ref cannot be zero")
	}
	if compiled, ok := runtime.compiled[protoRef]; ok {
		if compiled.Supported {
			if _, err := runtime.Engine.Protos.ConstantBase(compiled.Proto, runtime.Engine.Strings); err != nil {
				return nil, err
			}
			if err := runtime.syncCompiledMetadata(protoRef, compiled); err != nil {
				return nil, err
			}
		}
		return compiled, nil
	}
	proto, err := runtime.Engine.Protos.Resolve(protoRef)
	if err != nil {
		return nil, err
	}
	if err := runtime.ensureStubManager(); err != nil {
		return nil, err
	}
	compiled, err := NewCompiler(runtime.Engine, runtime.Cache, runtime.sharedStubs).Compile(proto)
	if err != nil {
		return nil, err
	}
	compiled.ProtoRef = protoRef
	if compiled.Supported {
		if _, err := runtime.Engine.Protos.ConstantBase(proto, runtime.Engine.Strings); err != nil {
			return nil, err
		}
		if err := runtime.syncCompiledMetadata(protoRef, compiled); err != nil {
			return nil, err
		}
	}
	runtime.compiled[protoRef] = compiled
	return compiled, nil
}

func (runtime *Runtime) DeoptCount() uint64 {
	if runtime == nil {
		return 0
	}
	return runtime.deoptCount
}

func (runtime *Runtime) SlowStubCount(id stubs.ID) uint64 {
	if runtime == nil {
		return 0
	}
	return runtime.stubCounts[id]
}

func (runtime *Runtime) ensureStubManager() error {
	if runtime.sharedStubs != nil {
		return nil
	}
	manager, err := newStubManager(runtime.Cache)
	if err != nil {
		return err
	}
	runtime.sharedStubs = manager
	return nil
}

func (runtime *Runtime) Call(thread *state.ThreadState, callee value.TValue, args []value.TValue, nresults int) ([]value.TValue, error) {
	if thread == nil {
		return nil, fmt.Errorf("thread cannot be nil")
	}
	if !callee.IsBoxedTag(value.TagLuaClosureRef) {
		return runtime.Engine.Call(thread, callee, args, nresults)
	}
	closureRef, _ := callee.HeapRef()
	closureObject, err := runtime.Engine.Closures.Object(closureRef)
	if err != nil {
		return nil, err
	}
	protoRef, ok := closureObject.ProtoRef()
	if !ok {
		return nil, fmt.Errorf("closure %#x has invalid proto reference %s", uint64(closureRef), closureObject.Proto)
	}
	compiled, err := runtime.CompileRef(protoRef)
	if err != nil {
		return nil, err
	}
	if !compiled.Supported {
		return runtime.Engine.Call(thread, callee, args, nresults)
	}
	if _, err := runtime.Engine.Closures.EnsureFeedbackVector(closureRef, compiled.FeedbackLayout); err != nil {
		return nil, err
	}
	return runtime.callCompiled(thread, closureRef, args, nresults, compiled)
}

func (runtime *Runtime) callCompiled(thread *state.ThreadState, closureRef value.HeapRef44, args []value.TValue, nresults int, compiled *CompiledCode) ([]value.TValue, error) {
	closureObject, err := runtime.Engine.Closures.Object(closureRef)
	if err != nil {
		return nil, err
	}
	registerCount := maxInt(1, int(compiled.Proto.MaxStackSize))
	resultSlots := registerCount
	if nresults > resultSlots {
		resultSlots = nresults
	}
	varargCount := 0
	if compiled.Proto.IsVararg != 0 && len(args) > int(compiled.Proto.NumParams) {
		varargCount = len(args) - int(compiled.Proto.NumParams)
	}
	registerBase, err := thread.NextRegisterBase()
	if err != nil {
		return nil, err
	}
	totalSlots := uint32(registerCount + resultSlots + varargCount)
	if registerBase+totalSlots > thread.StackSlots() {
		return nil, fmt.Errorf("thread stack exhausted: need %d slots, have %d", registerBase+totalSlots, thread.StackSlots())
	}
	constBase, err := runtime.Engine.Protos.ConstantBase(compiled.Proto, runtime.Engine.Strings)
	if err != nil {
		return nil, err
	}
	resultBase, err := thread.SlotAddress(registerBase + uint32(registerCount))
	if err != nil {
		return nil, err
	}
	var varargBase uintptr
	if varargCount > 0 {
		varargBase, err = thread.SlotAddress(registerBase + uint32(registerCount+resultSlots))
		if err != nil {
			return nil, err
		}
	}
	frame, err := thread.PushFrame(state.FrameSpec{
		Closure:       value.LuaClosureRefValue(closureRef),
		Proto:         closureObject.Proto,
		RegisterBase:  registerBase,
		ConstBase:     constBase,
		VarargBase:    varargBase,
		ResultBase:    resultBase,
		SavedBCOff:    0,
		NResults:      normalizeRequestedResults(nresults),
		VarargCount:   uint32(varargCount),
		RegisterCount: uint16(registerCount),
		SpillCount:    uint16(resultSlots + varargCount),
		Top:           uint16(minInt(len(args), registerCount)),
		ResultCap:     uint16(resultSlots),
	})
	if err != nil {
		return nil, err
	}
	clearThreadSlots(thread, registerBase, totalSlots)
	for index := 0; index < minInt(len(args), registerCount); index++ {
		if err := thread.SetRegister(frame, uint16(index), args[index]); err != nil {
			_, _ = thread.PopFrame()
			clearThreadSlots(thread, registerBase, totalSlots)
			return nil, err
		}
	}
	if varargCount > 0 {
		for index, slotValue := range args[int(compiled.Proto.NumParams):] {
			if err := thread.SetValueAtAddress(varargBase+uintptr(index)*value.TValueSize, slotValue); err != nil {
				_, _ = thread.PopFrame()
				clearThreadSlots(thread, registerBase, totalSlots)
				return nil, err
			}
		}
	}
	cleaned := false
	cleanup := func() {
		if cleaned {
			return
		}
		cleaned = true
		_, _ = thread.PopFrame()
		clearThreadSlots(thread, registerBase, totalSlots)
	}
	defer cleanup()
	regsBase := uintptr(frame.RegsBase)
	ctx := executionContext{}
	entry := compiled.Entry
	for {
		runtime.Engine.State.SyncActiveThread(thread)
		status, aux := abi.EnterCompiled(entry, runtime.Engine.Heap.NativeBase(), runtime.Engine.State.NativePointer(), unsafe.Pointer(frame), regsBase, unsafe.Pointer(&ctx))
		if err := thread.SyncCurrentFrameFromNative(); err != nil {
			return nil, err
		}
		switch status {
		case compiledStatusOK:
			results, err := collectThreadResults(thread, uintptr(frame.ResultBase), int(aux))
			if err != nil {
				return nil, err
			}
			return normalizeResults(results, nresults), nil
		case compiledStatusStub:
			nextEntry, finished, finalResults, err := runtime.handleStub(thread, frame, closureRef, compiled, &ctx, stubs.ID(aux), nresults)
			if err != nil {
				return nil, err
			}
			if finished {
				return finalResults, nil
			}
			if nextEntry < 0x10000 {
				return nil, fmt.Errorf("invalid continuation entry %#x for stub %d at site %d (compiled entry %#x)", nextEntry, aux, ctx.SiteID, compiled.Entry)
			}
			entry = nextEntry
		case compiledStatusDeopt:
			runtime.deoptCount++
			results, err := runtime.deoptFromContext(thread, frame, compiled, &ctx)
			if err != nil {
				return nil, err
			}
			return normalizeResults(results, nresults), nil
		case compiledStatusError:
			return nil, fmt.Errorf("compiled code returned error status")
		default:
			return nil, fmt.Errorf("unexpected compiled status %d", status)
		}
	}
}

func (runtime *Runtime) resumeCompiledFrame(thread *state.ThreadState, frame *state.CallFrameHeader, closureRef value.HeapRef44, compiled *CompiledCode, entry uintptr, nresults int) ([]value.TValue, error) {
	if thread == nil {
		return nil, fmt.Errorf("thread cannot be nil")
	}
	if frame == nil {
		return nil, fmt.Errorf("frame cannot be nil")
	}
	if compiled == nil || !compiled.Supported {
		return nil, fmt.Errorf("compiled frame requires supported compiled code")
	}
	ctx := executionContext{}
	regsBase := uintptr(frame.RegsBase)
	for {
		runtime.Engine.State.SyncActiveThread(thread)
		status, aux := abi.EnterCompiled(entry, runtime.Engine.Heap.NativeBase(), runtime.Engine.State.NativePointer(), unsafe.Pointer(frame), regsBase, unsafe.Pointer(&ctx))
		if err := thread.SyncCurrentFrameFromNative(); err != nil {
			return nil, err
		}
		switch status {
		case compiledStatusOK:
			results, err := collectThreadResults(thread, uintptr(frame.ResultBase), int(aux))
			if err != nil {
				return nil, err
			}
			return normalizeResults(results, nresults), nil
		case compiledStatusStub:
			nextEntry, finished, finalResults, err := runtime.handleStub(thread, frame, closureRef, compiled, &ctx, stubs.ID(aux), nresults)
			if err != nil {
				return nil, err
			}
			if finished {
				return normalizeResults(finalResults, nresults), nil
			}
			entry = nextEntry
		case compiledStatusDeopt:
			runtime.deoptCount++
			results, err := runtime.deoptFromContext(thread, frame, compiled, &ctx)
			if err != nil {
				return nil, err
			}
			return normalizeResults(results, nresults), nil
		case compiledStatusError:
			return nil, fmt.Errorf("compiled code returned error status")
		default:
			return nil, fmt.Errorf("unexpected compiled status %d", status)
		}
	}
}

func registerNumber(thread *state.ThreadState, frame *state.CallFrameHeader, index int) (float64, error) {
	registerValue, err := thread.Register(frame, uint16(index))
	if err != nil {
		return 0, err
	}
	number, ok := registerValue.Float64()
	if !ok {
		return 0, fmt.Errorf("register %d is not a number: %s", index, registerValue)
	}
	return number, nil
}

func storeFrameCallResults(thread *state.ThreadState, frame *state.CallFrameHeader, a int, c int, results []value.TValue) error {
	if c == 1 {
		if err := frame.SetTop(uint16(a)); err != nil {
			return err
		}
		return nil
	}
	wanted := c - 1
	if c == 0 {
		for index, slotValue := range results {
			if err := thread.SetRegister(frame, uint16(a+index), slotValue); err != nil {
				return err
			}
		}
		return frame.SetTop(uint16(a + len(results)))
	}
	for index := 0; index < wanted; index++ {
		slotValue := value.NilValue()
		if index < len(results) {
			slotValue = results[index]
		}
		if err := thread.SetRegister(frame, uint16(a+index), slotValue); err != nil {
			return err
		}
	}
	return frame.SetTop(uint16(a + wanted))
}

func normalizeResults(results []value.TValue, nresults int) []value.TValue {
	if nresults < 0 {
		return append([]value.TValue(nil), results...)
	}
	if nresults == 0 {
		return nil
	}
	out := make([]value.TValue, nresults)
	for index := 0; index < nresults; index++ {
		if index < len(results) {
			out[index] = results[index]
		} else {
			out[index] = value.NilValue()
		}
	}
	return out
}

func clearThreadSlots(thread *state.ThreadState, start uint32, count uint32) {
	for index := uint32(0); index < count; index++ {
		address, err := thread.SlotAddress(start + index)
		if err != nil {
			return
		}
		_ = thread.SetValueAtAddress(address, value.NilValue())
	}
}

func collectThreadResults(thread *state.ThreadState, base uintptr, count int) ([]value.TValue, error) {
	results := make([]value.TValue, 0, count)
	for index := 0; index < count; index++ {
		slotValue, err := thread.ValueAtAddress(base + uintptr(index)*value.TValueSize)
		if err != nil {
			return nil, err
		}
		results = append(results, slotValue)
	}
	return results, nil
}

func clearFrameSlots(thread *state.ThreadState, frame *state.CallFrameHeader) {
	if thread == nil || frame == nil {
		return
	}
	registerBase, err := thread.SlotIndexForAddress(uintptr(frame.RegsBase))
	if err != nil {
		return
	}
	clearThreadSlots(thread, registerBase, uint32(frame.RegisterCount)+uint32(frame.SpillCount))
}

func (runtime *Runtime) syncCompiledMetadata(protoRef value.HeapRef44, compiled *CompiledCode) error {
	return runtime.Engine.Protos.SyncCompiledMetadata(protoRef, compiled.Entry, 0)
}

package baseline

import (
	"fmt"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	rtproto "vexlua/internal/runtime/proto"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/abi"
	"vexlua/internal/vexarc/codecache"
)

type Runtime struct {
	Engine              *interp.Engine
	Cache               *codecache.Cache
	compiled            map[value.HeapRef44]*CompiledCode
	callSuspendCount    uint64
	forPrepSuspendCount uint64
	forLoopSuspendCount uint64
	deoptCount          uint64
}

func NewRuntime(engine *interp.Engine) *Runtime {
	if engine == nil {
		panic("baseline runtime requires an interpreter engine")
	}
	return &Runtime{
		Engine:   engine,
		Cache:    codecache.New(),
		compiled: make(map[value.HeapRef44]*CompiledCode),
	}
}

func (runtime *Runtime) Close() error {
	var firstErr error
	for _, compiled := range runtime.compiled {
		if err := compiled.Release(runtime.Cache); err != nil && firstErr == nil {
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
	compiled, err := NewCompiler(runtime.Engine, runtime.Cache).Compile(proto)
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

func (runtime *Runtime) CallSuspendCount() uint64 {
	if runtime == nil {
		return 0
	}
	return runtime.callSuspendCount
}

func (runtime *Runtime) ForPrepSuspendCount() uint64 {
	if runtime == nil {
		return 0
	}
	return runtime.forPrepSuspendCount
}

func (runtime *Runtime) ForLoopSuspendCount() uint64 {
	if runtime == nil {
		return 0
	}
	return runtime.forLoopSuspendCount
}

func (runtime *Runtime) DeoptCount() uint64 {
	if runtime == nil {
		return 0
	}
	return runtime.deoptCount
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
	registerBase, err := thread.NextRegisterBase()
	if err != nil {
		return nil, err
	}
	totalSlots := uint32(registerCount + resultSlots)
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
	frame, err := thread.PushFrame(state.FrameSpec{
		Closure:       value.LuaClosureRefValue(closureRef),
		Proto:         closureObject.Proto,
		RegisterBase:  registerBase,
		ConstBase:     constBase,
		ResultBase:    resultBase,
		SavedBCOff:    0,
		NResults:      normalizeRequestedResults(nresults),
		RegisterCount: uint16(registerCount),
		SpillCount:    uint16(resultSlots),
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
	currentPC := 0
	for {
		entry, err := compiled.EntryAtBytecode(currentPC)
		if err != nil {
			return nil, err
		}
		runtime.Engine.State.SyncActiveThread(thread)
		status, aux := abi.EnterCompiled(entry, runtime.Engine.Heap.NativeBase(), runtime.Engine.State.NativePointer(), unsafe.Pointer(frame), regsBase, unsafe.Pointer(&ctx))
		switch status {
		case compiledStatusOK:
			results, err := collectThreadResults(thread, uintptr(frame.ResultBase), int(aux))
			if err != nil {
				return nil, err
			}
			return normalizeResults(results, nresults), nil
		case compiledStatusSuspend:
			nextPC, finished, finalResults, err := runtime.handleSuspend(thread, frame, value.LuaClosureRefValue(closureRef), compiled, &ctx, nresults)
			if err != nil {
				return nil, err
			}
			if finished {
				return finalResults, nil
			}
			currentPC = nextPC
		case compiledStatusDeopt:
			runtime.deoptCount++
			cleanup()
			return runtime.Engine.Call(thread, value.LuaClosureRefValue(closureRef), args, nresults)
		case compiledStatusError:
			return nil, fmt.Errorf("compiled code returned error status")
		default:
			return nil, fmt.Errorf("unexpected compiled status %d", status)
		}
	}
}

func (runtime *Runtime) handleSuspend(thread *state.ThreadState, frame *state.CallFrameHeader, closure value.TValue, compiled *CompiledCode, ctx *executionContext, nresults int) (nextPC int, finished bool, finalResults []value.TValue, err error) {
	switch SuspendKind(ctx.SuspendKind) {
	case SuspendCall:
		return runtime.handleCallSuspend(thread, frame, ctx)
	case SuspendForPrep:
		nextPC, err = runtime.handleForPrep(thread, frame, ctx)
		return nextPC, false, nil, err
	case SuspendForLoop:
		nextPC, err = runtime.handleForLoop(thread, frame, ctx)
		return nextPC, false, nil, err
	default:
		return 0, false, nil, fmt.Errorf("unknown suspend kind %d", ctx.SuspendKind)
	}
}

func (runtime *Runtime) handleCallSuspend(thread *state.ThreadState, frame *state.CallFrameHeader, ctx *executionContext) (nextPC int, finished bool, finalResults []value.TValue, err error) {
	runtime.callSuspendCount++
	a := int(ctx.Arg0)
	b := int(ctx.Arg1)
	c := int(ctx.Arg2)
	callee, err := thread.Register(frame, uint16(a))
	if err != nil {
		return 0, false, nil, err
	}
	args := make([]value.TValue, 0, b-1)
	for index := 0; index < b-1; index++ {
		argument, err := thread.Register(frame, uint16(a+1+index))
		if err != nil {
			return 0, false, nil, err
		}
		args = append(args, argument)
	}
	results, err := runtime.Call(thread, callee, args, c-1)
	if err != nil {
		return 0, false, nil, err
	}
	if err := storeFrameCallResults(thread, frame, a, c, results); err != nil {
		return 0, false, nil, err
	}
	return int(ctx.ResumePC), false, nil, nil
}

func (runtime *Runtime) handleForPrep(thread *state.ThreadState, frame *state.CallFrameHeader, ctx *executionContext) (int, error) {
	runtime.forPrepSuspendCount++
	a := int(ctx.Arg0)
	index, err := registerNumber(thread, frame, a)
	if err != nil {
		return 0, err
	}
	step, err := registerNumber(thread, frame, a+2)
	if err != nil {
		return 0, err
	}
	if err := thread.SetRegister(frame, uint16(a), value.NumberValue(index-step)); err != nil {
		return 0, err
	}
	return int(ctx.Arg1), nil
}

func (runtime *Runtime) handleForLoop(thread *state.ThreadState, frame *state.CallFrameHeader, ctx *executionContext) (int, error) {
	runtime.forLoopSuspendCount++
	a := int(ctx.Arg0)
	index, err := registerNumber(thread, frame, a)
	if err != nil {
		return 0, err
	}
	limit, err := registerNumber(thread, frame, a+1)
	if err != nil {
		return 0, err
	}
	step, err := registerNumber(thread, frame, a+2)
	if err != nil {
		return 0, err
	}
	index += step
	if err := thread.SetRegister(frame, uint16(a), value.NumberValue(index)); err != nil {
		return 0, err
	}
	if (step > 0 && index <= limit) || (step <= 0 && index >= limit) {
		if err := thread.SetRegister(frame, uint16(a+3), value.NumberValue(index)); err != nil {
			return 0, err
		}
		return int(ctx.Arg1), nil
	}
	return int(ctx.ResumePC), nil
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
	if c <= 1 {
		return nil
	}
	wanted := c - 1
	for index := 0; index < wanted; index++ {
		slotValue := value.NilValue()
		if index < len(results) {
			slotValue = results[index]
		}
		if err := thread.SetRegister(frame, uint16(a+index), slotValue); err != nil {
			return err
		}
	}
	return nil
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

func (runtime *Runtime) syncCompiledMetadata(protoRef value.HeapRef44, compiled *CompiledCode) error {
	flags := uint8(0)
	if compiled != nil && compiled.Supported && compiledRunsWithoutSuspend(compiled.Proto) {
		flags |= rtproto.ProtoCompiledFlagNoSuspend
	}
	return runtime.Engine.Protos.SyncCompiledMetadata(protoRef, compiled.Entry, flags)
}

func compiledRunsWithoutSuspend(proto *bytecode.Proto) bool {
	if proto == nil {
		return false
	}
	for _, instruction := range proto.Code {
		switch instruction.Opcode() {
		case bytecode.OP_CALL, bytecode.OP_TAILCALL:
			return false
		}
	}
	return true
}

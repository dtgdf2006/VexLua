package baseline

import (
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/metadata"
	"vexlua/internal/vexarc/stubs"
)

func (runtime *Runtime) handleStub(thread *state.ThreadState, frame *state.CallFrameHeader, closureRef value.HeapRef44, compiled *CompiledCode, ctx *executionContext, stubID stubs.ID, nresults int) (uintptr, bool, []value.TValue, error) {
	site, err := compiled.ContinuationSite(ctx.SiteID)
	if err != nil {
		return 0, false, nil, err
	}
	if site.StubID != uint32(stubID) {
		return 0, false, nil, fmt.Errorf("stub/site mismatch: exit=%d metadata=%d", stubID, site.StubID)
	}
	countStub := true
	if (stubID == stubs.StubLuaCall || stubID == stubs.StubTailCall) && ctx.Flags&execCtxFlagNestedCallPending != 0 {
		countStub = false
	}
	if countStub {
		runtime.stubCounts[stubID]++
	}
	switch stubID {
	case stubs.StubGetGlobal:
		return runtime.handleGetGlobalStub(thread, frame, closureRef, compiled, site, nresults)
	case stubs.StubGetTable:
		return runtime.handleGetTableStub(thread, frame, closureRef, compiled, site, nresults)
	case stubs.StubSetGlobal:
		return runtime.handleSetGlobalStub(thread, frame, closureRef, compiled, site, nresults)
	case stubs.StubSetTable:
		return runtime.handleSetTableStub(thread, frame, closureRef, compiled, site, nresults)
	case stubs.StubLuaCall:
		return runtime.handleCallStub(thread, frame, compiled, site, ctx)
	case stubs.StubTailCall:
		return runtime.handleTailCallStub(thread, frame, compiled, site, ctx, nresults)
	default:
		return 0, false, nil, fmt.Errorf("unknown stub id %d", stubID)
	}
}

func (runtime *Runtime) deoptFromContext(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, ctx *executionContext) ([]value.TValue, error) {
	site, err := compiled.ContinuationSite(ctx.SiteID)
	if err != nil {
		return nil, err
	}
	return runtime.deoptFromSite(thread, frame, site)
}

func (runtime *Runtime) deoptFromSite(thread *state.ThreadState, frame *state.CallFrameHeader, site metadata.ContinuationSite) ([]value.TValue, error) {
	pc := int(site.DeoptPC)
	if site.DeoptPC == metadata.UnmappedOffset {
		pc = int(frame.SavedBCOff)
	}
	return runtime.Engine.ResumeLuaFrame(thread, frame, pc)
}

func (runtime *Runtime) deoptThroughSite(thread *state.ThreadState, frame *state.CallFrameHeader, site metadata.ContinuationSite, nresults int) (uintptr, bool, []value.TValue, error) {
	runtime.deoptCount++
	results, err := runtime.deoptFromSite(thread, frame, site)
	if err != nil {
		return 0, true, nil, err
	}
	return 0, true, normalizeResults(results, nresults), nil
}

func (runtime *Runtime) handleGetGlobalStub(thread *state.ThreadState, frame *state.CallFrameHeader, closureRef value.HeapRef44, compiled *CompiledCode, site metadata.ContinuationSite, nresults int) (uintptr, bool, []value.TValue, error) {
	key, err := runtime.constantOperandValue(compiled, int(site.Operand1))
	if err != nil {
		return 0, false, nil, err
	}
	env, err := runtime.Engine.Closures.Env(closureRef)
	if err != nil {
		return 0, false, nil, err
	}
	var result value.TValue
	if env.IsBoxedTag(value.TagHostObjectRef) {
		result, _, err = runtime.Engine.ReadIndexBoundary(env, key)
		if err != nil {
			return 0, false, nil, err
		}
	} else {
		return runtime.deoptThroughSite(thread, frame, site, nresults)
	}
	if err := thread.SetRegister(frame, uint16(site.Operand0), result); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleGetTableStub(thread *state.ThreadState, frame *state.CallFrameHeader, _ value.HeapRef44, compiled *CompiledCode, site metadata.ContinuationSite, nresults int) (uintptr, bool, []value.TValue, error) {
	tableValue, err := thread.Register(frame, uint16(site.Operand1))
	if err != nil {
		return 0, false, nil, err
	}
	key, err := runtime.frameRKValue(thread, frame, compiled, int(site.Operand2))
	if err != nil {
		return 0, false, nil, err
	}
	var result value.TValue
	if tableValue.IsBoxedTag(value.TagHostObjectRef) {
		result, _, err = runtime.Engine.ReadIndexBoundary(tableValue, key)
		if err != nil {
			return 0, false, nil, err
		}
	} else {
		return runtime.deoptThroughSite(thread, frame, site, nresults)
	}
	if err := thread.SetRegister(frame, uint16(site.Operand0), result); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleSetGlobalStub(thread *state.ThreadState, frame *state.CallFrameHeader, closureRef value.HeapRef44, compiled *CompiledCode, site metadata.ContinuationSite, nresults int) (uintptr, bool, []value.TValue, error) {
	key, err := runtime.constantOperandValue(compiled, int(site.Operand1))
	if err != nil {
		return 0, false, nil, err
	}
	env, err := runtime.Engine.Closures.Env(closureRef)
	if err != nil {
		return 0, false, nil, err
	}
	newValue, err := thread.Register(frame, uint16(site.Operand0))
	if err != nil {
		return 0, false, nil, err
	}
	if env.IsBoxedTag(value.TagHostObjectRef) {
		if err := runtime.Engine.WriteIndexBoundary(env, key, newValue); err != nil {
			return 0, false, nil, err
		}
	} else {
		return runtime.deoptThroughSite(thread, frame, site, nresults)
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleSetTableStub(thread *state.ThreadState, frame *state.CallFrameHeader, _ value.HeapRef44, compiled *CompiledCode, site metadata.ContinuationSite, nresults int) (uintptr, bool, []value.TValue, error) {
	tableValue, err := thread.Register(frame, uint16(site.Operand0))
	if err != nil {
		return 0, false, nil, err
	}
	key, err := runtime.frameRKValue(thread, frame, compiled, int(site.Operand1))
	if err != nil {
		return 0, false, nil, err
	}
	newValue, err := runtime.frameRKValue(thread, frame, compiled, int(site.Operand2))
	if err != nil {
		return 0, false, nil, err
	}
	if tableValue.IsBoxedTag(value.TagHostObjectRef) {
		if err := runtime.Engine.WriteIndexBoundary(tableValue, key, newValue); err != nil {
			return 0, false, nil, err
		}
	} else {
		return runtime.deoptThroughSite(thread, frame, site, nresults)
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleCallStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, ctx *executionContext) (uintptr, bool, []value.TValue, error) {
	a := int(site.Operand0)
	b := int(site.Operand1)
	c := int(site.Operand2)
	if ctx != nil && ctx.Flags&execCtxFlagNestedCallPending != 0 {
		results, err := runtime.finishNestedCompiledCall(thread, frame, ctx)
		if err != nil {
			return 0, false, nil, err
		}
		if err := storeFrameCallResults(thread, frame, a, c, results); err != nil {
			return 0, false, nil, err
		}
		entry, err := compiled.EntryAtSite(site, false)
		return entry, false, nil, err
	}
	callee, args, err := runtime.collectFrameCallArguments(thread, frame, a, b)
	if err != nil {
		return 0, false, nil, err
	}
	wantedResults := -1
	if c > 0 {
		wantedResults = c - 1
	}
	results, err := runtime.callValueBoundary(thread, callee, args, wantedResults)
	if err != nil {
		return 0, false, nil, err
	}
	if err := storeFrameCallResults(thread, frame, a, c, results); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleTailCallStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, ctx *executionContext, nresults int) (uintptr, bool, []value.TValue, error) {
	frame.SetFlag(state.FrameFlagIsTailcall, true)
	if ctx != nil && ctx.Flags&execCtxFlagNestedCallPending != 0 {
		results, err := runtime.finishNestedCompiledCall(thread, frame, ctx)
		if err != nil {
			return 0, false, nil, err
		}
		return 0, true, normalizeResults(results, nresults), nil
	}
	a := int(site.Operand0)
	b := int(site.Operand1)
	callee, args, err := runtime.collectFrameCallArguments(thread, frame, a, b)
	if err != nil {
		return 0, false, nil, err
	}
	results, err := runtime.callValueBoundary(thread, callee, args, -1)
	if err != nil {
		return 0, false, nil, err
	}
	return 0, true, normalizeResults(results, nresults), nil
}

func (runtime *Runtime) callValueBoundary(thread *state.ThreadState, callee value.TValue, args []value.TValue, nresults int) ([]value.TValue, error) {
	if callee.IsBoxedTag(value.TagLuaClosureRef) {
		return runtime.Call(thread, callee, args, nresults)
	}
	return runtime.Engine.CallValueBoundary(thread, callee, args, nresults)
}

func (runtime *Runtime) finishNestedCompiledCall(thread *state.ThreadState, callerFrame *state.CallFrameHeader, ctx *executionContext) ([]value.TValue, error) {
	if thread == nil {
		return nil, fmt.Errorf("thread cannot be nil")
	}
	if callerFrame == nil {
		return nil, fmt.Errorf("caller frame cannot be nil")
	}
	if ctx == nil || ctx.Flags&execCtxFlagNestedCallPending == 0 {
		return nil, fmt.Errorf("nested compiled call state is not pending")
	}
	activeFrame := thread.CurrentFrame()
	if activeFrame == nil || activeFrame == callerFrame {
		return nil, fmt.Errorf("nested compiled call did not publish an active callee frame")
	}
	frameCopy := *activeFrame
	cleanup := func() error {
		_, popErr := thread.PopFrame()
		clearFrameSlots(thread, &frameCopy)
		return popErr
	}
	closureRef, activeCompiled, err := runtime.compiledFrameState(activeFrame)
	if err != nil {
		ctx.Flags = 0
		ctx.Reserved0 = 0
		ctx.Reserved1 = 0
		ctx.Reserved2 = 0
		ctx.Reserved3 = 0
		if cleanupErr := cleanup(); cleanupErr != nil {
			return nil, cleanupErr
		}
		return nil, err
	}
	nestedCtx := executionContext{SiteID: ctx.Reserved0}
	nresults := int(activeFrame.NResults)
	var results []value.TValue
	if ctx.Flags&execCtxFlagNestedCallDeopt != 0 {
		runtime.deoptCount++
		results, err = runtime.deoptFromContext(thread, activeFrame, activeCompiled, &nestedCtx)
	} else if ctx.Flags&execCtxFlagNestedCallError != 0 {
		err = fmt.Errorf("nested compiled call returned error status")
	} else {
		nextEntry, finished, finalResults, handleErr := runtime.handleStub(thread, activeFrame, closureRef, activeCompiled, &nestedCtx, stubs.ID(ctx.Reserved1), nresults)
		if handleErr != nil {
			err = handleErr
		} else if finished {
			results = normalizeResults(finalResults, nresults)
		} else {
			results, err = runtime.resumeCompiledFrame(thread, activeFrame, closureRef, activeCompiled, nextEntry, nresults)
		}
	}
	ctx.Flags = 0
	ctx.Reserved0 = 0
	ctx.Reserved1 = 0
	ctx.Reserved2 = 0
	ctx.Reserved3 = 0
	if cleanupErr := cleanup(); err == nil && cleanupErr != nil {
		err = cleanupErr
	}
	if err != nil {
		return nil, err
	}
	return normalizeResults(results, nresults), nil
}

func (runtime *Runtime) compiledFrameState(frame *state.CallFrameHeader) (value.HeapRef44, *CompiledCode, error) {
	if frame == nil {
		return 0, nil, fmt.Errorf("frame cannot be nil")
	}
	closureRef, ok := frame.Closure.HeapRef()
	if !ok {
		return 0, nil, fmt.Errorf("frame closure is not a heap reference: %s", frame.Closure)
	}
	protoRef, ok := frame.Proto.HeapRef()
	if !ok {
		return 0, nil, fmt.Errorf("frame proto is not a heap reference: %s", frame.Proto)
	}
	compiled, err := runtime.CompileRef(protoRef)
	if err != nil {
		return 0, nil, err
	}
	if !compiled.Supported {
		return 0, nil, fmt.Errorf("compiled frame %#x lost compiled support", uint64(protoRef))
	}
	return closureRef, compiled, nil
}
func (runtime *Runtime) collectFrameCallArguments(thread *state.ThreadState, frame *state.CallFrameHeader, a int, b int) (value.TValue, []value.TValue, error) {
	callee, err := thread.Register(frame, uint16(a))
	if err != nil {
		return value.NilValue(), nil, err
	}
	argumentCount := 0
	if b == 0 {
		argumentCount = int(frame.RegisterCount) - a - 1
		if argumentCount < 0 {
			argumentCount = 0
		}
	} else {
		argumentCount = b - 1
	}
	args := make([]value.TValue, 0, argumentCount)
	for index := 0; index < argumentCount; index++ {
		argument, err := thread.Register(frame, uint16(a+1+index))
		if err != nil {
			return value.NilValue(), nil, err
		}
		args = append(args, argument)
	}
	return callee, args, nil
}

func (runtime *Runtime) constantOperandValue(compiled *CompiledCode, index int) (value.TValue, error) {
	if compiled == nil || compiled.Proto == nil {
		return value.NilValue(), fmt.Errorf("compiled proto is not available")
	}
	return runtime.Engine.Protos.ConstantValue(compiled.Proto, index, runtime.Engine.Strings)
}

func (runtime *Runtime) frameRKValue(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, operand int) (value.TValue, error) {
	if bytecode.IsConstantRK(operand) {
		return runtime.constantOperandValue(compiled, bytecode.IndexK(operand))
	}
	return thread.Register(frame, uint16(operand))
}

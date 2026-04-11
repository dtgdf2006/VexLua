package baseline

import (
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/feedback"
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
	runtime.stubCounts[stubID]++
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
		return runtime.handleCallStub(thread, frame, compiled, site)
	case stubs.StubTailCall:
		return runtime.handleTailCallStub(thread, frame, compiled, site, nresults)
	case stubs.StubForPrep:
		return runtime.handleForPrepStub(thread, frame, compiled, site)
	case stubs.StubForLoop:
		return runtime.handleForLoopStub(thread, frame, compiled, site)
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
		if !env.IsBoxedTag(value.TagTableRef) {
			return runtime.deoptThroughSite(thread, frame, site, nresults)
		}
		envRef, _ := env.HeapRef()
		result, _, err = runtime.Engine.Tables.Get(envRef, key)
		if err != nil {
			return 0, false, nil, err
		}
	}
	if err := thread.SetRegister(frame, uint16(site.Operand0), result); err != nil {
		return 0, false, nil, err
	}
	runtime.recordTableStubFeedback(closureRef, site, env, key, value.NilValue(), false)
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleGetTableStub(thread *state.ThreadState, frame *state.CallFrameHeader, closureRef value.HeapRef44, compiled *CompiledCode, site metadata.ContinuationSite, nresults int) (uintptr, bool, []value.TValue, error) {
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
		if !tableValue.IsBoxedTag(value.TagTableRef) {
			return runtime.deoptThroughSite(thread, frame, site, nresults)
		}
		tableRef, _ := tableValue.HeapRef()
		result, _, err = runtime.Engine.Tables.Get(tableRef, key)
		if err != nil {
			return 0, false, nil, err
		}
	}
	if err := thread.SetRegister(frame, uint16(site.Operand0), result); err != nil {
		return 0, false, nil, err
	}
	runtime.recordTableStubFeedback(closureRef, site, tableValue, key, value.NilValue(), false)
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
	if !env.IsBoxedTag(value.TagTableRef) {
		return runtime.deoptThroughSite(thread, frame, site, nresults)
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
		if !env.IsBoxedTag(value.TagTableRef) {
			return runtime.deoptThroughSite(thread, frame, site, nresults)
		}
		envRef, _ := env.HeapRef()
		if err := runtime.Engine.Tables.Set(envRef, key, newValue); err != nil {
			return 0, false, nil, err
		}
	}
	runtime.recordTableStubFeedback(closureRef, site, env, key, newValue, true)
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleSetTableStub(thread *state.ThreadState, frame *state.CallFrameHeader, closureRef value.HeapRef44, compiled *CompiledCode, site metadata.ContinuationSite, nresults int) (uintptr, bool, []value.TValue, error) {
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
		if !tableValue.IsBoxedTag(value.TagTableRef) {
			return runtime.deoptThroughSite(thread, frame, site, nresults)
		}
		tableRef, _ := tableValue.HeapRef()
		if err := runtime.Engine.Tables.Set(tableRef, key, newValue); err != nil {
			return 0, false, nil, err
		}
	}
	runtime.recordTableStubFeedback(closureRef, site, tableValue, key, newValue, true)
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleCallStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite) (uintptr, bool, []value.TValue, error) {
	a := int(site.Operand0)
	b := int(site.Operand1)
	c := int(site.Operand2)
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

func (runtime *Runtime) handleTailCallStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, nresults int) (uintptr, bool, []value.TValue, error) {
	frame.SetFlag(state.FrameFlagIsTailcall, true)
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

func (runtime *Runtime) handleForPrepStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite) (uintptr, bool, []value.TValue, error) {
	a := int(site.Operand0)
	index, err := registerNumber(thread, frame, a)
	if err != nil {
		return 0, false, nil, err
	}
	step, err := registerNumber(thread, frame, a+2)
	if err != nil {
		return 0, false, nil, err
	}
	if err := thread.SetRegister(frame, uint16(a), value.NumberValue(index-step)); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleForLoopStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite) (uintptr, bool, []value.TValue, error) {
	a := int(site.Operand0)
	index, err := registerNumber(thread, frame, a)
	if err != nil {
		return 0, false, nil, err
	}
	limit, err := registerNumber(thread, frame, a+1)
	if err != nil {
		return 0, false, nil, err
	}
	step, err := registerNumber(thread, frame, a+2)
	if err != nil {
		return 0, false, nil, err
	}
	index += step
	if err := thread.SetRegister(frame, uint16(a), value.NumberValue(index)); err != nil {
		return 0, false, nil, err
	}
	if (step > 0 && index <= limit) || (step <= 0 && index >= limit) {
		if err := thread.SetRegister(frame, uint16(a+3), value.NumberValue(index)); err != nil {
			return 0, false, nil, err
		}
		entry, err := compiled.EntryAtSite(site, true)
		return entry, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
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

func (runtime *Runtime) recordTableStubFeedback(closureRef value.HeapRef44, site metadata.ContinuationSite, tableValue value.TValue, key value.TValue, slotValue value.TValue, isStore bool) {
	kind, slotIndex, ok := feedbackSlotForSite(site)
	if !ok {
		return
	}
	runtime.Engine.UpdateTableFeedbackAtSlot(closureRef, kind, slotIndex, tableValue, key, slotValue, isStore)
}

func feedbackSlotForSite(site metadata.ContinuationSite) (feedback.SlotKind, uint32, bool) {
	switch site.Kind {
	case metadata.ContinuationGetGlobal:
		return feedback.SlotGetGlobal, site.Operand2, true
	case metadata.ContinuationGetTable:
		return feedback.SlotGetTable, site.Operand3, true
	case metadata.ContinuationSetGlobal:
		return feedback.SlotSetGlobal, site.Operand2, true
	case metadata.ContinuationSetTable:
		return feedback.SlotSetTable, site.Operand3, true
	default:
		return feedback.SlotInvalid, 0, false
	}
}

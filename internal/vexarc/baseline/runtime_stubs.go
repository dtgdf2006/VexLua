package baseline

import (
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/feedback"
	rtlua "vexlua/internal/runtime/lua"
	rtproto "vexlua/internal/runtime/proto"
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
	if (stubID == stubs.StubLuaCall || stubID == stubs.StubTailCall) && ctx.Flags&(execCtxFlagNestedCallPending|execCtxFlagResolvedHostCallBoundary|execCtxFlagMegamorphicCallBoundary) != 0 {
		countStub = false
	}
	if (stubID == stubs.StubLuaCall || stubID == stubs.StubTailCall) && ctx.Flags&execCtxFlagMegamorphicCallBoundary != 0 {
		runtime.megamorphicCallBoundaryCount++
	}
	if countStub {
		runtime.stubCounts[stubID]++
	}
	switch stubID {
	case stubs.StubGetGlobal:
		return runtime.handleGetGlobalStub(thread, frame, closureRef, compiled, site, nresults)
	case stubs.StubGetTable:
		return runtime.handleGetTableStub(thread, frame, closureRef, compiled, site, nresults)
	case stubs.StubForPrep:
		return runtime.handleForPrepStub(thread, frame, compiled, site, nresults)
	case stubs.StubSelf:
		return runtime.handleSelfStub(thread, frame, compiled, site, nresults)
	case stubs.StubArithmetic:
		return runtime.handleArithmeticStub(thread, frame, compiled, site, nresults)
	case stubs.StubLen:
		return runtime.handleLenStub(thread, frame, compiled, site, nresults)
	case stubs.StubCompare:
		return runtime.handleCompareStub(thread, frame, compiled, site, nresults)
	case stubs.StubSetGlobal:
		return runtime.handleSetGlobalStub(thread, frame, closureRef, compiled, site, nresults)
	case stubs.StubSetTable:
		return runtime.handleSetTableStub(thread, frame, closureRef, compiled, site, nresults)
	case stubs.StubSetList:
		return runtime.handleSetListStub(thread, frame, compiled, site, nresults)
	case stubs.StubNewTable:
		return runtime.handleNewTableStub(thread, frame, compiled, site, nresults)
	case stubs.StubConcat:
		return runtime.handleConcatStub(thread, frame, compiled, site, nresults)
	case stubs.StubClose:
		return runtime.handleCloseStub(thread, frame, compiled, site, nresults)
	case stubs.StubClosure:
		return runtime.handleClosureStub(thread, frame, closureRef, compiled, site, nresults)
	case stubs.StubLuaCall:
		return runtime.handleCallStub(thread, frame, closureRef, compiled, site, ctx)
	case stubs.StubTailCall:
		return runtime.handleTailCallStub(thread, frame, closureRef, compiled, site, ctx, nresults)
	default:
		return 0, false, nil, fmt.Errorf("unknown stub id %d", stubID)
	}
}

func (runtime *Runtime) handleForPrepStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, _ int) (uintptr, bool, []value.TValue, error) {
	a := int(site.Operand0)
	initNumber, err := runtime.coerceLoopRegisterNumber(thread, frame, a, "initial value")
	if err != nil {
		return 0, false, nil, err
	}
	_, err = runtime.coerceLoopRegisterNumber(thread, frame, a+1, "limit")
	if err != nil {
		return 0, false, nil, err
	}
	stepNumber, err := runtime.coerceLoopRegisterNumber(thread, frame, a+2, "step")
	if err != nil {
		return 0, false, nil, err
	}
	prepared, err := rtlua.ArithmeticNumberResult(bytecode.OP_SUB, initNumber, stepNumber)
	if err != nil {
		return 0, false, nil, err
	}
	if err := thread.SetRegister(frame, uint16(a), value.NumberValue(prepared)); err != nil {
		return 0, false, nil, err
	}
	if err := advanceFrameTopForSlot(frame, a); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleArithmeticStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, _ int) (uintptr, bool, []value.TValue, error) {
	opcode := bytecode.Opcode(site.Operand3)
	left, err := runtime.frameRKValue(thread, frame, compiled, int(site.Operand1))
	if err != nil {
		return 0, false, nil, err
	}
	right := left
	if opcode != bytecode.OP_UNM {
		right, err = runtime.frameRKValue(thread, frame, compiled, int(site.Operand2))
		if err != nil {
			return 0, false, nil, err
		}
	}
	result, err := runtime.Engine.ArithmeticBoundary(thread, opcode, left, right)
	if err != nil {
		return 0, false, nil, err
	}
	if err := thread.SetRegister(frame, uint16(site.Operand0), result); err != nil {
		return 0, false, nil, err
	}
	if err := advanceFrameTopForSlot(frame, int(site.Operand0)); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleLenStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, _ int) (uintptr, bool, []value.TValue, error) {
	slotValue, err := thread.Register(frame, uint16(site.Operand1))
	if err != nil {
		return 0, false, nil, err
	}
	result, err := runtime.Engine.LengthBoundary(thread, slotValue)
	if err != nil {
		return 0, false, nil, err
	}
	if err := thread.SetRegister(frame, uint16(site.Operand0), result); err != nil {
		return 0, false, nil, err
	}
	if err := advanceFrameTopForSlot(frame, int(site.Operand0)); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleCompareStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, _ int) (uintptr, bool, []value.TValue, error) {
	opcode := bytecode.Opcode(site.Operand3)
	left, err := runtime.frameRKValue(thread, frame, compiled, int(site.Operand1))
	if err != nil {
		return 0, false, nil, err
	}
	right, err := runtime.frameRKValue(thread, frame, compiled, int(site.Operand2))
	if err != nil {
		return 0, false, nil, err
	}
	compare, err := runtime.Engine.CompareBoundary(thread, opcode, left, right)
	if err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, compare == (site.Operand0 != 0))
	return entry, false, nil, err
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
	result, _, err := runtime.Engine.ReadIndexMetaBoundary(thread, env, key)
	if err != nil {
		return 0, false, nil, err
	}
	if err := thread.SetRegister(frame, uint16(site.Operand0), result); err != nil {
		return 0, false, nil, err
	}
	if err := advanceFrameTopForSlot(frame, int(site.Operand0)); err != nil {
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
	result, _, err := runtime.Engine.ReadIndexMetaBoundary(thread, tableValue, key)
	if err != nil {
		return 0, false, nil, err
	}
	if err := thread.SetRegister(frame, uint16(site.Operand0), result); err != nil {
		return 0, false, nil, err
	}
	if err := advanceFrameTopForSlot(frame, int(site.Operand0)); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleSelfStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, nresults int) (uintptr, bool, []value.TValue, error) {
	tableValue, err := thread.Register(frame, uint16(site.Operand1))
	if err != nil {
		return 0, false, nil, err
	}
	if err := thread.SetRegister(frame, uint16(site.Operand0)+1, tableValue); err != nil {
		return 0, false, nil, err
	}
	key, err := runtime.frameRKValue(thread, frame, compiled, int(site.Operand2))
	if err != nil {
		return 0, false, nil, err
	}
	result, _, err := runtime.Engine.ReadIndexMetaBoundary(thread, tableValue, key)
	if err != nil {
		return 0, false, nil, err
	}
	if err := thread.SetRegister(frame, uint16(site.Operand0), result); err != nil {
		return 0, false, nil, err
	}
	if err := advanceFrameTopForSlot(frame, int(site.Operand0)+1); err != nil {
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
	if err := runtime.Engine.WriteIndexMetaBoundary(thread, env, key, newValue); err != nil {
		return 0, false, nil, err
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
	if err := runtime.Engine.WriteIndexMetaBoundary(thread, tableValue, key, newValue); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleSetListStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, _ int) (uintptr, bool, []value.TValue, error) {
	tableValue, err := thread.Register(frame, uint16(site.Operand0))
	if err != nil {
		return 0, false, nil, err
	}
	if !tableValue.IsBoxedTag(value.TagTableRef) {
		return runtime.deoptThroughSite(thread, frame, site, -1)
	}
	count := int(site.Operand1)
	if count == 0 {
		top := int(frame.LogicalTop())
		if top <= int(site.Operand0)+1 {
			count = 0
		} else {
			count = top - int(site.Operand0) - 1
		}
	}
	if count == 0 {
		entry, err := compiled.EntryAtSite(site, false)
		return entry, false, nil, err
	}
	baseIndex := (int(site.Operand2) - 1) * setListFieldsPerFlush
	values := make([]value.TValue, count)
	for index := 0; index < count; index++ {
		slotValue, err := thread.Register(frame, uint16(int(site.Operand0)+index+1))
		if err != nil {
			return 0, false, nil, err
		}
		values[index] = slotValue
	}
	ref, _ := tableValue.HeapRef()
	handled, err := runtime.Engine.Tables.SetListArray(ref, uint32(baseIndex+1), values)
	if err != nil {
		return 0, false, nil, err
	}
	if !handled {
		return runtime.deoptThroughSite(thread, frame, site, -1)
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleNewTableStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, _ int) (uintptr, bool, []value.TValue, error) {
	frame.SavedBCOff = site.ResumePC
	if err := runtime.Engine.NewTableBoundary(site.Operand1, site.Operand2, func(tableValue value.TValue) error {
		if err := thread.SetRegister(frame, uint16(site.Operand0), tableValue); err != nil {
			return err
		}
		return advanceFrameTopForSlot(frame, int(site.Operand0))
	}); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleConcatStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, _ int) (uintptr, bool, []value.TValue, error) {
	count := int(site.Operand2) - int(site.Operand1) + 1
	values := make([]value.TValue, 0, count)
	for index := int(site.Operand1); index <= int(site.Operand2); index++ {
		slotValue, err := thread.Register(frame, uint16(index))
		if err != nil {
			return 0, false, nil, err
		}
		values = append(values, slotValue)
	}
	frame.SavedBCOff = site.ResumePC
	if err := runtime.Engine.ConcatValuesBoundary(thread, values, func(result value.TValue) error {
		if err := thread.SetRegister(frame, uint16(site.Operand0), result); err != nil {
			return err
		}
		return advanceFrameTopForSlot(frame, int(site.Operand0))
	}); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleCloseStub(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite, _ int) (uintptr, bool, []value.TValue, error) {
	registerBase, err := thread.SlotIndexForAddress(uintptr(frame.RegsBase))
	if err != nil {
		return 0, false, nil, err
	}
	address, err := thread.SlotAddress(registerBase + site.Operand0)
	if err != nil {
		return 0, false, nil, err
	}
	limit := uintptr(frame.RegsBase) + uintptr(frame.RegisterCount)*value.TValueSize
	frame.SavedBCOff = site.ResumePC
	if _, err := runtime.Engine.CloseUpvaluesInRangeBoundary(thread, address, limit); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleClosureStub(thread *state.ThreadState, frame *state.CallFrameHeader, closureRef value.HeapRef44, compiled *CompiledCode, site metadata.ContinuationSite, _ int) (uintptr, bool, []value.TValue, error) {
	if compiled.ProtoRef == 0 {
		return 0, false, nil, fmt.Errorf("closure continuation missing proto ref")
	}
	closureSite, captures, found, err := runtime.Engine.Protos.ClosureSite(compiled.ProtoRef, int(site.BytecodePC))
	if err != nil {
		return 0, false, nil, err
	}
	if !found {
		return 0, false, nil, fmt.Errorf("closure site pc %d is missing proto metadata", site.BytecodePC)
	}
	childIndex := int(site.Operand1)
	if int(closureSite.ChildProtoIndex) != childIndex {
		return 0, false, nil, fmt.Errorf("closure site child proto mismatch: metadata=%d site=%d", closureSite.ChildProtoIndex, childIndex)
	}
	if childIndex < 0 || childIndex >= len(compiled.Proto.Protos) {
		return 0, false, nil, fmt.Errorf("closure child proto %d is out of range", childIndex)
	}
	registerBase, err := thread.SlotIndexForAddress(uintptr(frame.RegsBase))
	if err != nil {
		return 0, false, nil, err
	}
	upvalueRefs := make([]value.HeapRef44, len(captures))
	for index, capture := range captures {
		switch capture.Kind {
		case rtproto.CaptureLocal:
			address, err := thread.SlotAddress(registerBase + uint32(capture.Index))
			if err != nil {
				return 0, false, nil, err
			}
			handle, err := runtime.Engine.Upvalues.FindOrCreateOpen(thread, address)
			if err != nil {
				return 0, false, nil, err
			}
			upvalueRefs[index] = handle.Ref
		case rtproto.CaptureUpvalue:
			upvalueRef, err := runtime.Engine.Closures.UpvalueRefAt(closureRef, int(capture.Index))
			if err != nil {
				return 0, false, nil, err
			}
			upvalueRefs[index] = upvalueRef
		default:
			return 0, false, nil, fmt.Errorf("unsupported closure capture kind %d", capture.Kind)
		}
	}
	env, err := runtime.Engine.Closures.Env(closureRef)
	if err != nil {
		return 0, false, nil, err
	}
	childProto := compiled.Proto.Protos[childIndex]
	frame.SavedBCOff = site.ResumePC
	if err := runtime.Engine.NewClosureBoundary(childProto, env, upvalueRefs, func(closureValue value.TValue) error {
		if err := thread.SetRegister(frame, uint16(site.Operand0), closureValue); err != nil {
			return err
		}
		return advanceFrameTopForSlot(frame, int(site.Operand0))
	}); err != nil {
		return 0, false, nil, err
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) handleCallStub(thread *state.ThreadState, frame *state.CallFrameHeader, closureRef value.HeapRef44, compiled *CompiledCode, site metadata.ContinuationSite, ctx *executionContext) (uintptr, bool, []value.TValue, error) {
	a := int(site.Operand0)
	b := int(site.Operand1)
	c := int(site.Operand2)
	resultBase := a
	previousTop := frame.LogicalTop()
	if site.HasAlternateResume() && site.Operand3 != 0 {
		resultBase = a + 3
	}
	if ctx != nil && ctx.Flags&execCtxFlagMegamorphicCallBoundary != 0 {
		ctx.Flags &^= execCtxFlagMegamorphicCallBoundary
	}
	if ctx != nil && ctx.Flags&execCtxFlagNestedCallPending != 0 {
		results, err := runtime.finishNestedCompiledCall(thread, frame, ctx)
		if err != nil {
			return 0, false, nil, err
		}
		if err := storeFrameCallResults(thread, frame, resultBase, c, results); err != nil {
			return 0, false, nil, err
		}
		if site.HasAlternateResume() && previousTop > frame.LogicalTop() {
			if err := frame.SetTop(previousTop); err != nil {
				return 0, false, nil, err
			}
		}
		return runtime.resumeCallContinuation(thread, frame, compiled, site)
	}
	if ctx != nil && ctx.Flags&execCtxFlagResolvedHostCallBoundary != 0 {
		wantedResults := -1
		if c > 0 {
			wantedResults = c - 1
		}
		results, err := runtime.finishResolvedHostCall(thread, frame, site, ctx, wantedResults)
		if err != nil {
			return 0, false, nil, err
		}
		if err := storeFrameCallResults(thread, frame, resultBase, c, results); err != nil {
			return 0, false, nil, err
		}
		if site.HasAlternateResume() && previousTop > frame.LogicalTop() {
			if err := frame.SetTop(previousTop); err != nil {
				return 0, false, nil, err
			}
		}
		return runtime.resumeCallContinuationAtSafepoint(thread, frame, compiled, site)
	}
	callee, args, err := runtime.collectFrameCallArguments(thread, frame, a, b)
	if err != nil {
		return 0, false, nil, err
	}
	resolvedCallee, resolvedArgs, matched, err := runtime.matchCallFeedbackCell(closureRef, compiled, int(site.BytecodePC), feedback.SlotCall, callee, args)
	if err != nil {
		return 0, false, nil, err
	}
	if !matched {
		resolvedCallee, resolvedArgs, err = runtime.Engine.ResolveCallBoundary(callee, args)
		if err != nil {
			return 0, false, nil, err
		}
		runtime.updateResolvedCallFeedback(closureRef, compiled, site, feedback.SlotCall, callee, resolvedCallee)
	}
	wantedResults := -1
	if c > 0 {
		wantedResults = c - 1
	}
	results, err := runtime.callResolvedBoundary(thread, resolvedCallee, resolvedArgs, wantedResults)
	if err != nil {
		return 0, false, nil, err
	}
	if err := storeFrameCallResults(thread, frame, resultBase, c, results); err != nil {
		return 0, false, nil, err
	}
	if site.HasAlternateResume() && previousTop > frame.LogicalTop() {
		if err := frame.SetTop(previousTop); err != nil {
			return 0, false, nil, err
		}
	}
	return runtime.resumeCallContinuationAtSafepoint(thread, frame, compiled, site)
}

func (runtime *Runtime) resumeCallContinuation(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite) (uintptr, bool, []value.TValue, error) {
	if site.HasAlternateResume() && site.Operand3 != 0 {
		firstResult, err := thread.Register(frame, uint16(site.Operand0+3))
		if err != nil {
			return 0, false, nil, err
		}
		if !firstResult.IsBoxedTag(value.TagNil) {
			if err := thread.SetRegister(frame, uint16(site.Operand3), firstResult); err != nil {
				return 0, false, nil, err
			}
			entry, err := compiled.EntryAtSite(site, true)
			return entry, false, nil, err
		}
	}
	entry, err := compiled.EntryAtSite(site, false)
	return entry, false, nil, err
}

func (runtime *Runtime) resumeCallContinuationAtSafepoint(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite) (uintptr, bool, []value.TValue, error) {
	entry, resumePC, err := runtime.callContinuationEntry(thread, frame, compiled, site)
	if err != nil {
		return 0, false, nil, err
	}
	if resumePC != metadata.UnmappedOffset {
		frame.SavedBCOff = resumePC
	}
	if err := runtime.Engine.AdvanceGCSafepoint(); err != nil {
		return 0, false, nil, err
	}
	return entry, false, nil, nil
}

func (runtime *Runtime) handleTailCallStub(thread *state.ThreadState, frame *state.CallFrameHeader, closureRef value.HeapRef44, compiled *CompiledCode, site metadata.ContinuationSite, ctx *executionContext, nresults int) (uintptr, bool, []value.TValue, error) {
	frame.SetFlag(state.FrameFlagIsTailcall, true)
	if ctx != nil && ctx.Flags&execCtxFlagMegamorphicCallBoundary != 0 {
		ctx.Flags &^= execCtxFlagMegamorphicCallBoundary
	}
	if ctx != nil && ctx.Flags&execCtxFlagNestedCallPending != 0 {
		results, err := runtime.finishNestedCompiledCall(thread, frame, ctx)
		if err != nil {
			return 0, false, nil, err
		}
		return 0, true, normalizeResults(results, nresults), nil
	}
	if ctx != nil && ctx.Flags&execCtxFlagResolvedHostCallBoundary != 0 {
		results, err := runtime.finishResolvedHostCall(thread, frame, site, ctx, -1)
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
	resolvedCallee, resolvedArgs, matched, err := runtime.matchCallFeedbackCell(closureRef, compiled, int(site.BytecodePC), feedback.SlotTailCall, callee, args)
	if err != nil {
		return 0, false, nil, err
	}
	if !matched {
		resolvedCallee, resolvedArgs, err = runtime.Engine.ResolveCallBoundary(callee, args)
		if err != nil {
			return 0, false, nil, err
		}
		runtime.updateResolvedCallFeedback(closureRef, compiled, site, feedback.SlotTailCall, callee, resolvedCallee)
	}
	results, err := runtime.callResolvedBoundary(thread, resolvedCallee, resolvedArgs, -1)
	if err != nil {
		return 0, false, nil, err
	}
	return 0, true, normalizeResults(results, nresults), nil
}

func (runtime *Runtime) matchCallFeedbackCell(closureRef value.HeapRef44, compiled *CompiledCode, bytecodePC int, kind feedback.SlotKind, callee value.TValue, args []value.TValue) (value.TValue, []value.TValue, bool, error) {
	slot, slotIndex, ok := compiled.FeedbackLayout.SlotAtPC(bytecodePC)
	if !ok || slot.Kind != kind {
		return value.NilValue(), nil, false, nil
	}
	cell, err := runtime.Engine.Closures.ReadFeedbackCell(closureRef, slotIndex)
	if err != nil {
		return value.NilValue(), nil, false, err
	}
	resolvedCallee, matched, err := runtime.Engine.MatchCallFeedbackCell(callee, cell)
	if err != nil || !matched {
		return value.NilValue(), nil, false, err
	}
	resolvedArgs := args
	if feedback.IsResolvedCallAccessKind(cell.AccessKind) {
		resolvedArgs = make([]value.TValue, 0, len(args)+1)
		resolvedArgs = append(resolvedArgs, callee)
		resolvedArgs = append(resolvedArgs, args...)
	}
	return resolvedCallee, resolvedArgs, true, nil
}

func (runtime *Runtime) callValueBoundary(thread *state.ThreadState, callee value.TValue, args []value.TValue, nresults int) ([]value.TValue, error) {
	resolvedCallee, resolvedArgs, err := runtime.Engine.ResolveCallBoundary(callee, args)
	if err != nil {
		return nil, err
	}
	return runtime.callResolvedBoundary(thread, resolvedCallee, resolvedArgs, nresults)
}

func (runtime *Runtime) callResolvedBoundary(thread *state.ThreadState, resolvedCallee value.TValue, resolvedArgs []value.TValue, nresults int) ([]value.TValue, error) {
	if resolvedCallee.IsBoxedTag(value.TagLuaClosureRef) {
		return runtime.Call(thread, resolvedCallee, resolvedArgs, nresults)
	}
	return runtime.Engine.CallResolvedBoundary(thread, resolvedCallee, resolvedArgs, nresults)
}

func (runtime *Runtime) updateResolvedCallFeedback(closureRef value.HeapRef44, compiled *CompiledCode, site metadata.ContinuationSite, kind feedback.SlotKind, callee value.TValue, resolvedCallee value.TValue) {
	runtime.Engine.UpdateCallFeedbackAtPC(closureRef, compiled.Proto, int(site.BytecodePC), kind, callee, resolvedCallee)
}

func (runtime *Runtime) callContinuationEntry(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, site metadata.ContinuationSite) (uintptr, uint32, error) {
	alternateResume := false
	resumePC := site.ResumePC
	if site.HasAlternateResume() && site.Operand3 != 0 {
		firstResult, err := thread.Register(frame, uint16(site.Operand0+3))
		if err != nil {
			return 0, metadata.UnmappedOffset, err
		}
		if !firstResult.IsBoxedTag(value.TagNil) {
			if err := thread.SetRegister(frame, uint16(site.Operand3), firstResult); err != nil {
				return 0, metadata.UnmappedOffset, err
			}
			alternateResume = true
			resumePC = site.AltResumePC
		}
	}
	entry, err := compiled.EntryAtSite(site, alternateResume)
	if err != nil {
		return 0, metadata.UnmappedOffset, err
	}
	return entry, resumePC, nil
}

func (runtime *Runtime) loopContinuationEntry(compiled *CompiledCode, site metadata.ContinuationSite, alternateResume bool) (uintptr, uint32, error) {
	resumePC := site.ResumePC
	if alternateResume {
		resumePC = site.AltResumePC
	}
	entry, err := compiled.EntryAtSite(site, alternateResume)
	if err != nil {
		return 0, metadata.UnmappedOffset, err
	}
	return entry, resumePC, nil
}

func (runtime *Runtime) finishNestedCompiledCall(thread *state.ThreadState, callerFrame *state.CallFrameHeader, ctx *executionContext) ([]value.TValue, error) {
	if ctx.Flags&execCtxFlagNestedCallPending == 0 {
		return nil, fmt.Errorf("nested compiled call state is not pending")
	}
	activeFrame := thread.CurrentFrame()
	if activeFrame == nil || activeFrame == callerFrame {
		return nil, fmt.Errorf("nested compiled call did not publish an active callee frame")
	}
	frameCopy := *activeFrame
	cleanup := func() error {
		closeLimit := uintptr(frameCopy.RegsBase) + uintptr(frameCopy.RegisterCount)*value.TValueSize
		_, closeErr := runtime.Engine.Upvalues.CloseInRange(thread, uintptr(frameCopy.RegsBase), closeLimit)
		_, popErr := thread.PopFrame()
		clearFrameSlots(thread, &frameCopy)
		if closeErr != nil {
			return closeErr
		}
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
	if _, err := runtime.Engine.Closures.EnsureFeedbackVector(closureRef, activeCompiled.FeedbackLayout); err != nil {
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

func (runtime *Runtime) finishResolvedHostCall(thread *state.ThreadState, frame *state.CallFrameHeader, site metadata.ContinuationSite, ctx *executionContext, nresults int) ([]value.TValue, error) {
	resolvedCallee, prependOriginalCallee, err := takeResolvedHostCallFromContext(ctx)
	if err != nil {
		return nil, err
	}
	callee, args, err := runtime.collectFrameCallArguments(thread, frame, int(site.Operand0), int(site.Operand1))
	if err != nil {
		return nil, err
	}
	resolvedArgs := args
	if prependOriginalCallee {
		resolvedArgs = make([]value.TValue, 0, len(args)+1)
		resolvedArgs = append(resolvedArgs, callee)
		resolvedArgs = append(resolvedArgs, args...)
	}
	return runtime.callResolvedBoundary(thread, resolvedCallee, resolvedArgs, nresults)
}

func takeResolvedHostCallFromContext(ctx *executionContext) (value.TValue, bool, error) {
	if ctx.Flags&execCtxFlagResolvedHostCallBoundary == 0 {
		return value.NilValue(), false, fmt.Errorf("resolved host call boundary is not pending")
	}
	raw := value.Raw(uint64(ctx.Reserved0) | uint64(ctx.Reserved1)<<32)
	prependOriginalCallee := ctx.Reserved2 == hostCallProtocolResolvedPrependCallee
	ctx.Flags &^= execCtxFlagResolvedHostCallBoundary
	ctx.Reserved0 = 0
	ctx.Reserved1 = 0
	ctx.Reserved2 = 0
	ctx.Reserved3 = 0
	resolvedCallee := value.FromRaw(raw)
	if !resolvedCallee.IsBoxedTag(value.TagHostFunctionRef) {
		return value.NilValue(), false, fmt.Errorf("resolved host call boundary requires host function, got %s", resolvedCallee)
	}
	return resolvedCallee, prependOriginalCallee, nil
}

func (runtime *Runtime) compiledFrameState(frame *state.CallFrameHeader) (value.HeapRef44, *CompiledCode, error) {
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

func advanceFrameTopForSlot(frame *state.CallFrameHeader, slot int) error {
	if slot < 0 {
		return fmt.Errorf("slot %d is invalid", slot)
	}
	if slot+1 <= int(frame.LogicalTop()) {
		return nil
	}
	return frame.SetTop(uint16(slot + 1))
}

func (runtime *Runtime) collectFrameCallArguments(thread *state.ThreadState, frame *state.CallFrameHeader, a int, b int) (value.TValue, []value.TValue, error) {
	callee, err := thread.Register(frame, uint16(a))
	if err != nil {
		return value.NilValue(), nil, err
	}
	argumentCount := 0
	if b == 0 {
		argumentCount = int(frame.LogicalTop()) - a - 1
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
	return runtime.Engine.Protos.ConstantValue(compiled.Proto, index, runtime.Engine.Strings)
}

func (runtime *Runtime) frameRKValue(thread *state.ThreadState, frame *state.CallFrameHeader, compiled *CompiledCode, operand int) (value.TValue, error) {
	if bytecode.IsConstantRK(operand) {
		return runtime.constantOperandValue(compiled, bytecode.IndexK(operand))
	}
	return thread.Register(frame, uint16(operand))
}

func (runtime *Runtime) coerceLoopRegisterNumber(thread *state.ThreadState, frame *state.CallFrameHeader, index int, role string) (float64, error) {
	registerValue, err := thread.Register(frame, uint16(index))
	if err != nil {
		return 0, err
	}
	numberValue, ok, err := rtlua.ToNumber(registerValue, runtime.Engine.Strings.Text)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("for %s must be a number", role)
	}
	if err := thread.SetRegister(frame, uint16(index), numberValue); err != nil {
		return 0, err
	}
	number, _ := numberValue.Float64()
	return number, nil
}

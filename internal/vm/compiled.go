package vm

import (
	"errors"
	"fmt"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/jit"
	rt "vexlua/internal/runtime"
)

func (m *VM) runCompiledOrInterpret(proto *bytecode.Proto, state *protoState, regs []rt.Value) (rt.Value, error) {
	if err := m.maybeCompile(state, proto); err != nil {
		return rt.NilValue, err
	}
	if state.compiled == nil {
		return m.interpretFromPC(proto, state, regs, 0)
	}
	pc := 0
	for {
		exit, err := m.enterTopCompiled(state, regs, pc)
		if err != nil {
			return rt.NilValue, err
		}
		switch exit.Reason {
		case jit.ExitReturn:
			state.compiledReturns++
			return exit.ReturnValue, nil
		case jit.ExitCallHelper:
			nextPC, err := m.executeTopLevelCompiledHelper(proto, state, regs, exit)
			if err != nil {
				return rt.NilValue, err
			}
			pc = nextPC
			continue
		case jit.ExitInterpret, jit.ExitSideExit, jit.ExitTailCall, jit.ExitYield, jit.ExitHook, jit.ExitGC:
			state.compiledFallbacks++
			return m.interpretFromPC(proto, state, regs, int(exit.ResumePC))
		default:
			return rt.NilValue, fmt.Errorf("compiled unit %q exited with unsupported reason %s", state.compiled.Name(), exit.Reason)
		}
	}
}

func (m *VM) maybeCompile(state *protoState, proto *bytecode.Proto) error {
	if state.compileFailed || m.compiler == nil || state.runs < m.hotThreshold {
		return nil
	}
	if state.compiled != nil {
		if state.quickenedOps <= state.compiledQuickenedAt {
			return nil
		}
	}
	compiled, err := m.compiler.Compile(jit.CompileRequest{Proto: proto, Mode: jit.CompileWholeProto})
	if err == nil {
		if proto.Scripted && compiled.Meta().Mode == jit.CompileRegion && state.compiled == nil && state.runs == m.hotThreshold {
			return nil
		}
		m.ensureCompiledFieldCaches(state, compiled.Meta())
		m.ensureCompiledCallCaches(state, compiled.Meta())
		state.compiled = compiled
		state.compiledQuickenedAt = state.quickenedOps
		return nil
	}
	if errors.Is(err, jit.ErrRetryLater) {
		return nil
	}
	if errors.Is(err, jit.ErrUnsupported) {
		state.compileFailed = true
		return nil
	}
	return err
}

func (m *VM) maybeRunCompiledFrame(co *Coroutine, frame *callFrame) (bool, error) {
	if co.hook.enabled() {
		return false, nil
	}
	state := frame.state
	proto := frame.closure.Proto
	unit := compiledUnitForFrame(frame)
	if frame.pc == 0 && frame.compiledUnit == nil {
		if err := m.maybeCompile(state, proto); err != nil {
			return false, err
		}
		unit = compiledUnitForFrame(frame)
	}
	if unit == nil {
		return false, nil
	}
	pc := frame.pc
	for {
		frame.pc = pc
		exit, err := m.enterCompiledFrameUnit(state, frame, unit)
		if err != nil {
			return false, err
		}
		switch exit.Reason {
		case jit.ExitReturn:
			frame.state.compiledReturns++
			return true, m.returnFromFrame(co, frame.singleResult(exit.ReturnValue))
		case jit.ExitNestedCall:
			if exit.Flags&jit.ExitFlagTailReplace != 0 {
				if err := m.materializePendingTailCallFrame(co, frame); err != nil {
					return false, err
				}
				return true, nil
			}
			child, childExit, err := m.materializePendingDirectCallFrame(co, frame, exit)
			if err != nil {
				return false, err
			}
			switch childExit.Reason {
			case jit.ExitCallHelper:
				outcome, err := m.executeFrameCompiledHelper(co, child, childExit)
				if err != nil {
					return false, err
				}
				if outcome.reenter {
					child.pc = outcome.nextPC
				}
				return true, nil
			case jit.ExitInterpret, jit.ExitSideExit, jit.ExitTailCall, jit.ExitYield, jit.ExitHook, jit.ExitGC:
				child.state.compiledFallbacks++
				child.pc = int(childExit.ResumePC)
				return true, nil
			default:
				return false, fmt.Errorf("nested compiled call for %q exited with unsupported reason %s", child.closure.Proto.Name, childExit.Reason)
			}
		case jit.ExitCallHelper:
			outcome, err := m.executeFrameCompiledHelper(co, frame, exit)
			if err != nil {
				return false, err
			}
			if outcome.reenter {
				pc = outcome.nextPC
				continue
			}
			if outcome.handled {
				return true, nil
			}
			return false, nil
		case jit.ExitInterpret, jit.ExitSideExit, jit.ExitTailCall, jit.ExitYield, jit.ExitHook, jit.ExitGC:
			frame.state.compiledFallbacks++
			frame.pc = int(exit.ResumePC)
			return false, nil
		default:
			return false, fmt.Errorf("compiled unit %q exited with unsupported reason %s", frame.closure.Proto.Name, exit.Reason)
		}
	}
}

func (m *VM) enterTopCompiled(state *protoState, regs []rt.Value, pc int) (jit.NativeExitRecord, error) {
	m.prepareNativeThread(state, 0, len(regs), cap(regs), m.globalEnv())
	state.nativeFrame.Reset()
	state.nativeFrame.PC = uint32(pc)
	state.nativeFrame.MaxStack = uint32(len(regs))
	state.nativeFrame.SlotsBase = slotsBase(regs)
	return m.callCompiledUnit(state, state.compiled, &state.nativeFrame)
}

func (m *VM) enterCompiledFrameUnit(state *protoState, frame *callFrame, unit jit.CompiledUnit) (jit.NativeExitRecord, error) {
	m.prepareNativeThread(state, frame.base, frame.stackSize, frame.base+cap(frame.regs), m.envOf(frame.closure))
	m.prepareNativeUpvalues(state, frame)
	frame.nativeFrame.Reset()
	frame.nativeFrame.Base = uint32(frame.base)
	frame.nativeFrame.PC = uint32(frame.pc)
	frame.nativeFrame.MaxStack = uint32(frame.stackSize)
	frame.nativeFrame.SlotsBase = slotsBase(frame.regs)
	frame.nativeFrame.VarargCount = uint32(len(frame.varargs))
	if frame.returnReg >= 0 {
		frame.nativeFrame.ResultReg = uint32(frame.returnReg)
	}
	if frame.returnCount >= 0 {
		frame.nativeFrame.ResultCount = uint32(frame.returnCount)
	}
	frame.syncNativeFrameBuffers()
	return m.callCompiledUnit(state, unit, &frame.nativeFrame)
}

func (m *VM) prepareNativeThread(state *protoState, base int, stackSize int, stackCapacity int, env rt.Value) {
	state.nativeThread.Reset()
	state.nativeThread.FrameDepth = 1
	if top := base + stackSize; top > 0 {
		state.nativeThread.StackTop = uint32(top)
	}
	if stackCapacity > 0 {
		state.nativeThread.StackCapacity = uint32(stackCapacity)
	}
	heap := m.runtime.Heap()
	state.nativeThread.HeapTablesBase = heap.TablesBase()
	state.nativeThread.HeapTablesLen = uintptr(heap.TablesLen())
	if len(state.fieldCaches) != 0 {
		state.nativeThread.FieldCachesBase = uintptr(unsafe.Pointer(&state.fieldCaches[0]))
		state.nativeThread.FieldCachesLen = uintptr(len(state.fieldCaches))
	}
	if len(state.callCaches) != 0 {
		state.nativeThread.CallCachesBase = uintptr(unsafe.Pointer(&state.callCaches[0]))
		state.nativeThread.CallCachesLen = uintptr(len(state.callCaches))
	}
	if handle, ok := env.Handle(); ok && handle.Kind() == rt.ObjectTable {
		state.nativeThread.CurrentEnvHandle = handle
	}
}

func (m *VM) prepareNativeUpvalues(state *protoState, frame *callFrame) {
	if state == nil || frame == nil || frame.closure == nil || len(frame.closure.Upvalues) == 0 {
		return
	}
	count := len(frame.closure.Upvalues)
	if count <= len(frame.nativeUpInline) {
		frame.nativeUpvalues = frame.nativeUpInline[:count]
	} else {
		if cap(frame.nativeUpvalues) < count {
			frame.nativeUpvalues = make([]jit.NativeUpvalue, count)
		} else {
			frame.nativeUpvalues = frame.nativeUpvalues[:count]
		}
	}
	for i, uv := range frame.closure.Upvalues {
		frame.nativeUpvalues[i].Cell = nativeUpvalueCell(uv)
	}
	state.nativeThread.UpvaluesBase = uintptr(unsafe.Pointer(&frame.nativeUpvalues[0]))
	state.nativeThread.UpvaluesLen = uintptr(count)
}

func (m *VM) ensureCompiledFieldCaches(state *protoState, meta *jit.CompiledUnitMeta) {
	if state == nil || meta == nil || len(meta.InlineCacheSlots) == 0 {
		return
	}
	maxSlot := len(state.fieldCaches) - 1
	for _, slot := range meta.InlineCacheSlots {
		if slot > maxSlot {
			maxSlot = slot
		}
	}
	if maxSlot < len(state.fieldCaches) {
		return
	}
	grown := make([]rt.FieldCache, maxSlot+1)
	copy(grown, state.fieldCaches)
	for index := len(state.fieldCaches); index < len(grown); index++ {
		grown[index].Symbol = ^uint32(0)
	}
	state.fieldCaches = grown
}

func (m *VM) ensureCompiledCallCaches(state *protoState, meta *jit.CompiledUnitMeta) {
	if state == nil || meta == nil || len(meta.CallCacheSlots) == 0 {
		return
	}
	maxSlot := len(state.callCaches) - 1
	for _, slot := range meta.CallCacheSlots {
		if slot > maxSlot {
			maxSlot = slot
		}
	}
	if maxSlot < len(state.callCaches) {
		return
	}
	grownCaches := make([]jit.DirectCallCache, maxSlot+1)
	copy(grownCaches, state.callCaches)
	state.callCaches = grownCaches
	grownUnits := make([]jit.CompiledUnit, maxSlot+1)
	copy(grownUnits, state.directCallUnits)
	state.directCallUnits = grownUnits
}

func (m *VM) callCompiledUnit(state *protoState, unit jit.CompiledUnit, frame *jit.NativeFrameState) (jit.NativeExitRecord, error) {
	exit, err := unit.Enter(&state.nativeThread, frame)
	if err != nil {
		return jit.NativeExitRecord{}, err
	}
	state.compiledEnters++
	state.compiledDirectCalls += state.nativeThread.DirectCallCount
	return exit, nil
}

func compiledUnitForFrame(frame *callFrame) jit.CompiledUnit {
	if frame == nil {
		return nil
	}
	if frame.compiledUnit != nil {
		return frame.compiledUnit
	}
	if frame.state == nil {
		return nil
	}
	return frame.state.compiled
}

func compiledMetaForFrame(frame *callFrame) *jit.CompiledUnitMeta {
	unit := compiledUnitForFrame(frame)
	if unit == nil {
		return nil
	}
	return unit.Meta()
}

func (m *VM) materializePendingDirectCallFrame(co *Coroutine, caller *callFrame, callerExit jit.NativeExitRecord) (*callFrame, jit.NativeExitRecord, error) {
	state := caller.state
	slot := int(state.nativeThread.PendingCallCache)
	if slot < 0 || slot >= len(state.directCallUnits) {
		return nil, jit.NativeExitRecord{}, fmt.Errorf("compiled unit %q reported invalid direct-call slot %d", compiledUnitForFrame(caller).Name(), slot)
	}
	unit := state.directCallUnits[slot]
	if unit == nil {
		return nil, jit.NativeExitRecord{}, fmt.Errorf("compiled unit %q has no direct-call target for slot %d", compiledUnitForFrame(caller).Name(), slot)
	}
	callee := state.nativeThread.PendingCallee
	h, ok := callee.Handle()
	if !ok || h.Kind() != rt.ObjectLuaClosure {
		return nil, jit.NativeExitRecord{}, fmt.Errorf("compiled unit %q reported invalid direct-call callee %s", compiledUnitForFrame(caller).Name(), callee)
	}
	closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
	pendingFrame := state.nativeThread.PendingFrame
	pendingExit := state.nativeThread.PendingCallExit
	child := m.acquireMaterializedFrame(co, closure, unit, pendingFrame)
	caller.pc = int(callerExit.ResumePC)
	co.frames = append(co.frames, child)
	co.stackTop = child.base + child.stackSize
	return child, pendingExit, nil
}

func (m *VM) materializePendingTailCallFrame(co *Coroutine, caller *callFrame) error {
	state := caller.state
	slot := int(state.nativeThread.PendingCallCache)
	if slot < 0 || slot >= len(state.directCallUnits) {
		return fmt.Errorf("compiled unit %q reported invalid tailcall slot %d", compiledUnitForFrame(caller).Name(), slot)
	}
	unit := state.directCallUnits[slot]
	if unit == nil {
		return fmt.Errorf("compiled unit %q has no tailcall target for slot %d", compiledUnitForFrame(caller).Name(), slot)
	}
	callee := state.nativeThread.PendingCallee
	h, ok := callee.Handle()
	if !ok || h.Kind() != rt.ObjectLuaClosure {
		return fmt.Errorf("compiled unit %q reported invalid tailcall callee %s", compiledUnitForFrame(caller).Name(), callee)
	}
	closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
	pending := state.nativeThread.PendingFrame
	if len(co.frames) > 1 {
		co.frames[len(co.frames)-2].tailCalls++
		caller.tailLoss++
	}
	caller.closeUpvalues()
	caller.state = m.stateFor(closure.Proto)
	caller.closure = closure
	caller.compiledUnit = unit
	caller.base = int(pending.Base)
	caller.stackSize = int(pending.MaxStack)
	need := caller.base + caller.stackSize
	if len(co.stack) < need {
		co.stack = co.stack[:need]
	}
	caller.regs = materializedFrameValues(co, pending)
	caller.pc = int(pending.PC)
	caller.tailCalls = 0
	caller.lastHookLine = -1
	caller.lastHookPC = -1
	caller.returnReg = int(pending.ResultReg)
	caller.returnCount = int(pending.ResultCount)
	caller.openCount = 0
	caller.clearVarargs()
	caller.clearPendingResults()
	caller.argScratch = caller.argScratch[:0]
	caller.resultScratch = caller.resultScratch[:0]
	caller.gcRoots = caller.gcRoots[:0]
	caller.gcOverwriteReg = -1
	caller.gcOverwriteCnt = 0
	caller.gcOverwritePend = false
	caller.gcFrameDead = false
	caller.tailPending = false
	caller.tailHookEvent = ""
	caller.skipTailUnwind = false
	caller.openUpvalues = resizeOpenUpvalues(caller.openUpvalues, closure.Proto.MaxStack)
	caller.regs = materializedFrameValues(co, pending)
	co.stackTop = caller.base + caller.stackSize
	return nil
}

func (m *VM) acquireMaterializedFrame(co *Coroutine, closure *LuaClosure, unit jit.CompiledUnit, pending jit.NativeFrameState) *callFrame {
	state := m.stateFor(closure.Proto)
	var frame *callFrame
	last := len(state.framePool) - 1
	if last >= 0 {
		frame = state.framePool[last]
		state.framePool = state.framePool[:last]
	} else {
		frame = &callFrame{}
	}
	frame.state = state
	frame.closure = closure
	frame.compiledUnit = unit
	frame.base = int(pending.Base)
	frame.stackSize = int(pending.MaxStack)
	frame.regs = materializedFrameValues(co, pending)
	frame.pc = int(pending.PC)
	frame.tailCalls = 0
	frame.tailLoss = 0
	frame.lastHookLine = -1
	frame.lastHookPC = -1
	frame.returnReg = int(pending.ResultReg)
	frame.returnCount = int(pending.ResultCount)
	frame.openCount = 0
	frame.clearVarargs()
	frame.clearPendingResults()
	frame.argScratch = frame.argScratch[:0]
	frame.resultScratch = frame.resultScratch[:0]
	frame.gcRoots = frame.gcRoots[:0]
	frame.gcOverwriteReg = -1
	frame.gcOverwriteCnt = 0
	frame.gcOverwritePend = false
	frame.gcFrameDead = false
	frame.tailPending = false
	frame.tailHookEvent = ""
	frame.skipTailUnwind = false
	frame.openUpvalues = resizeOpenUpvalues(frame.openUpvalues, closure.Proto.MaxStack)
	return frame
}

func materializedFrameValues(co *Coroutine, pending jit.NativeFrameState) []rt.Value {
	base := int(pending.Base)
	count := int(pending.MaxStack)
	if count <= 0 {
		return nil
	}
	need := base + count
	if co != nil && need <= cap(co.stack) {
		if len(co.stack) < need {
			co.stack = co.stack[:need]
		}
		return co.stack[base:need]
	}
	if pending.SlotsBase == 0 {
		return nil
	}
	return unsafe.Slice((*rt.Value)(unsafe.Pointer(pending.SlotsBase)), count)
}

func syncNativeMultiResultBuffer(buf *jit.NativeMultiResultBuffer, values []rt.Value) {
	buf.Reset()
	if len(values) == 0 {
		return
	}
	buf.Count = uint32(len(values))
	inlineCount := len(values)
	if inlineCount > len(buf.Inline) {
		inlineCount = len(buf.Inline)
	}
	copy(buf.Inline[:inlineCount], values[:inlineCount])
	if len(values) > inlineCount {
		spill := values[inlineCount:]
		buf.SpillCount = uint32(len(spill))
		buf.SpillBase = slotsBase(spill)
	}
}

func appendNativeMultiResultBuffer(dst []rt.Value, buf *jit.NativeMultiResultBuffer) []rt.Value {
	if buf == nil || buf.Count == 0 {
		return dst
	}
	inlineCount := int(buf.Count)
	if inlineCount > len(buf.Inline) {
		inlineCount = len(buf.Inline)
	}
	dst = append(dst, buf.Inline[:inlineCount]...)
	if buf.SpillCount == 0 || buf.SpillBase == 0 {
		return dst
	}
	spill := unsafe.Slice((*rt.Value)(unsafe.Pointer(buf.SpillBase)), int(buf.SpillCount))
	return append(dst, spill...)
}

func slotsBase(regs []rt.Value) uintptr {
	if len(regs) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&regs[0]))
}

func nativeUpvalueCell(uv *upvalue) uintptr {
	if uv == nil {
		return 0
	}
	if uv.isOpen {
		if uv.index < 0 || uv.index >= len(uv.stack) {
			return 0
		}
		uv.cell.value = uv.stack[uv.index]
	}
	return uintptr(unsafe.Pointer(&uv.cell.value))
}

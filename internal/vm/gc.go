package vm

import rt "vexlua/internal/runtime"

func (closure *LuaClosure) AppendGCRoots(dst []rt.Value) []rt.Value {
	if closure == nil {
		return dst
	}
	dst = append(dst, closure.Env)
	for _, uv := range closure.Upvalues {
		if uv == nil {
			continue
		}
		dst = append(dst, uv.Get())
	}
	return dst
}

func (co *Coroutine) AppendGCRoots(dst []rt.Value) []rt.Value {
	if co == nil {
		return dst
	}
	dst = append(dst, co.entry, co.proxy, co.lastResult, co.yielded)
	dst = append(dst, co.lastResults...)
	dst = append(dst, co.yieldedResult...)
	if co.machine == nil {
		return dst
	}
	for index, frame := range co.frames {
		if frame == nil {
			continue
		}
		overwriteReg := frame.gcOverwriteReg
		overwriteCount := frame.gcOverwriteCnt
		overwritePending := frame.gcOverwritePend
		frameDead := frame.gcFrameDead
		if index+1 < len(co.frames) {
			child := co.frames[index+1]
			if child != nil {
				overwriteReg = child.returnReg
				overwriteCount = child.returnCount
				overwritePending = child.returnCount == 0
				frameDead = false
			}
		} else if co.status == CoroutineSuspended {
			frameDead = false
			if co.resumeCount == 0 {
				overwriteReg = -1
				overwriteCount = 0
				overwritePending = true
			} else if co.resumeCount > 0 {
				overwriteReg = co.resumeReg
				overwriteCount = co.resumeCount
				overwritePending = false
			}
		}
		dst = co.machine.appendFrameRoots(dst, frame, overwriteReg, overwriteCount, overwritePending, frameDead)
	}
	return dst
}

func (m *VM) CollectGarbage() error {
	roots := make([]rt.Value, 0, 128)
	for _, state := range m.states {
		if state.rootClosure != 0 {
			roots = append(roots, state.rootClosure)
		}
		for _, singleton := range state.singletons {
			roots = append(roots, singleton)
		}
	}
	for _, frame := range m.activeFrames {
		if frame == nil {
			continue
		}
		roots = m.appendFrameRoots(roots, frame, frame.gcOverwriteReg, frame.gcOverwriteCnt, frame.gcOverwritePend, frame.gcFrameDead)
	}
	for _, co := range m.activeCoros {
		roots = co.AppendGCRoots(roots)
	}
	for _, userdata := range m.runtime.CollectGarbage(roots) {
		meta, ok := m.runtime.GetMetafield(userdata, "__gc")
		if !ok {
			continue
		}
		_, _ = m.callValue(meta, []rt.Value{userdata})
	}
	return nil
}

func (m *VM) appendFrameRoots(dst []rt.Value, frame *callFrame, overwriteReg int, overwriteCount int, overwritePending bool, frameDead bool) []rt.Value {
	if frame == nil {
		return dst
	}
	if value, ok := m.runtime.FindLuaClosureValue(frame.closure); ok {
		dst = append(dst, value)
	}
	dst = append(dst, frame.gcRoots...)
	if frameDead {
		return dst
	}
	dst = append(dst, frame.varargs...)
	if !overwritePending {
		dst = append(dst, frame.pendingResults...)
	}
	live := m.frameLiveRegs(frame)
	if len(live) == 0 {
		return dst
	}
	for reg := range frame.regs {
		if !liveRegisterContains(live, reg) {
			continue
		}
		if overwriteCount > 0 && overwriteReg >= 0 && reg >= overwriteReg && reg < overwriteReg+overwriteCount {
			continue
		}
		dst = append(dst, frame.regs[reg])
	}
	return dst
}

func (m *VM) frameLiveRegs(frame *callFrame) []uint64 {
	if frame == nil || frame.closure == nil || frame.closure.Proto == nil {
		return nil
	}
	state := m.stateFor(frame.closure.Proto)
	if frame.pc < 0 || frame.pc >= len(state.liveRegs) {
		return nil
	}
	return state.liveRegs[frame.pc]
}

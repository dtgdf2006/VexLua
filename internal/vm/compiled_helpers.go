package vm

import (
	"fmt"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/jit"
	rt "vexlua/internal/runtime"
)

type compiledFrameHelperOutcome struct {
	reenter bool
	nextPC  int
	handled bool
}

func resumeCompiledFrame(pc int) compiledFrameHelperOutcome {
	return compiledFrameHelperOutcome{reenter: true, nextPC: pc}
}

func continueCoroutineLoop() compiledFrameHelperOutcome {
	return compiledFrameHelperOutcome{handled: true}
}

func (m *VM) runLuaClosureHelperCall(co *Coroutine, caller *callFrame, closure *LuaClosure, args []rt.Value, resultReg int, resultCount int, resumePC int, clearPending bool) (compiledFrameHelperOutcome, error) {
	if co == nil {
		return compiledFrameHelperOutcome{}, fmt.Errorf("lua closure helper call requires active coroutine")
	}
	targetDepth := len(co.frames)
	caller.pc = resumePC
	m.pushFrame(co, closure, args, resultReg, resultCount)
	if err := m.dispatchCallHookForFrames(co, co.frames[len(co.frames)-1], caller); err != nil {
		return compiledFrameHelperOutcome{}, err
	}
	if clearPending {
		caller.clearPendingResults()
	}
	if _, err := m.executeCoroutineUntil(co, targetDepth); err != nil {
		return compiledFrameHelperOutcome{}, err
	}
	if co.status != CoroutineRunning || len(co.frames) != targetDepth {
		return continueCoroutineLoop(), nil
	}
	return resumeCompiledFrame(resumePC), nil
}

func invalidateCompiledFieldCache(caches []rt.FieldCache, slot int, symbol uint32) {
	if slot < 0 || slot >= len(caches) {
		return
	}
	caches[slot] = rt.FieldCache{Symbol: symbol}
}

func (m *VM) primeCompiledFieldCache(caches []rt.FieldCache, slot int, target rt.Value, symbol uint32) {
	if slot < 0 || slot >= len(caches) {
		return
	}
	invalidateCompiledFieldCache(caches, slot, symbol)
	h, ok := target.Handle()
	if !ok || h.Kind() != rt.ObjectTable {
		return
	}
	value, fieldSlot, found, err := m.runtime.GetField(target, symbol)
	if err != nil || !found || value.Kind() == rt.KindNil {
		return
	}
	caches[slot] = rt.FieldCache{
		Valid:   true,
		Table:   h,
		Version: m.runtime.Heap().Table(h).Version(),
		Slot:    fieldSlot,
		Symbol:  symbol,
	}
}

func (m *VM) executeCompiledGetField(state *protoState, proto *bytecode.Proto, pc int, regs []rt.Value, instr bytecode.Instr) error {
	if state != nil && proto != nil && pc >= 0 && pc < len(proto.Code) && proto.Code[pc].Op == bytecode.OpGetFieldIC && int(instr.C) < len(state.fieldCaches) {
		cache := &state.fieldCaches[instr.C]
		value, found, err := m.runtime.GetFieldCached(regs[instr.B], cache)
		if err != nil {
			return err
		}
		if found {
			regs[instr.A] = value
			return nil
		}
	}
	value, _, found, err := m.runtime.GetField(regs[instr.B], uint32(instr.D))
	if err != nil {
		value, handled, metaErr := m.resolveFieldIndex(regs[instr.B], uint32(instr.D))
		if metaErr != nil {
			return metaErr
		}
		if !handled {
			return err
		}
		if state != nil && int(instr.C) < len(state.fieldCaches) {
			invalidateCompiledFieldCache(state.fieldCaches, int(instr.C), uint32(instr.D))
		}
		regs[instr.A] = value
		return nil
	}
	if found {
		regs[instr.A] = value
		if state != nil && int(instr.C) < len(state.fieldCaches) {
			m.primeCompiledFieldCache(state.fieldCaches, int(instr.C), regs[instr.B], uint32(instr.D))
			if proto != nil && pc >= 0 && pc < len(proto.Code) && proto.Code[pc].Op != bytecode.OpGetFieldIC {
				proto.Code[pc].Op = bytecode.OpGetFieldIC
				state.quickenedOps++
			}
		}
		return nil
	}
	value, handled, err := m.resolveFieldIndex(regs[instr.B], uint32(instr.D))
	if err != nil {
		return err
	}
	if !handled {
		value = rt.NilValue
	}
	if state != nil && int(instr.C) < len(state.fieldCaches) {
		invalidateCompiledFieldCache(state.fieldCaches, int(instr.C), uint32(instr.D))
	}
	regs[instr.A] = value
	return nil
}

func (m *VM) executeCompiledSelf(state *protoState, proto *bytecode.Proto, pc int, regs []rt.Value, instr bytecode.Instr) error {
	receiver := regs[instr.B]
	if state != nil && proto != nil && pc >= 0 && pc < len(proto.Code) && proto.Code[pc].Op == bytecode.OpSelfIC && int(instr.C) < len(state.fieldCaches) {
		cache := &state.fieldCaches[instr.C]
		value, found, err := m.runtime.GetFieldCached(receiver, cache)
		if err != nil {
			return err
		}
		if found {
			regs[instr.A] = value
			regs[instr.A+1] = receiver
			return nil
		}
	}
	value, _, found, err := m.runtime.GetField(receiver, uint32(instr.D))
	if err != nil {
		value, handled, metaErr := m.resolveFieldIndex(receiver, uint32(instr.D))
		if metaErr != nil {
			return metaErr
		}
		if !handled {
			return err
		}
		if state != nil && int(instr.C) < len(state.fieldCaches) {
			invalidateCompiledFieldCache(state.fieldCaches, int(instr.C), uint32(instr.D))
		}
		regs[instr.A] = value
		regs[instr.A+1] = receiver
		return nil
	}
	if found {
		regs[instr.A] = value
		regs[instr.A+1] = receiver
		if state != nil && int(instr.C) < len(state.fieldCaches) {
			m.primeCompiledFieldCache(state.fieldCaches, int(instr.C), receiver, uint32(instr.D))
			if proto != nil && pc >= 0 && pc < len(proto.Code) && proto.Code[pc].Op != bytecode.OpSelfIC {
				proto.Code[pc].Op = bytecode.OpSelfIC
				state.quickenedOps++
			}
		}
		return nil
	}
	value, handled, err := m.resolveFieldIndex(receiver, uint32(instr.D))
	if err != nil {
		return err
	}
	if !handled {
		value = rt.NilValue
	}
	if state != nil && int(instr.C) < len(state.fieldCaches) {
		invalidateCompiledFieldCache(state.fieldCaches, int(instr.C), uint32(instr.D))
	}
	regs[instr.A] = value
	regs[instr.A+1] = receiver
	return nil
}

func (m *VM) executeCompiledGetTable(state *protoState, proto *bytecode.Proto, pc int, regs []rt.Value, instr bytecode.Instr) error {
	if table, index, ok := m.fastPlainArrayTarget(regs[instr.B], regs[instr.C]); ok {
		value, found := table.GetIndex(index)
		if !found {
			value = rt.NilValue
		}
		regs[instr.A] = value
		if state != nil && proto != nil && pc >= 0 && pc < len(proto.Code) && proto.Code[pc].Op != bytecode.OpGetTableArray {
			proto.Code[pc].Op = bytecode.OpGetTableArray
			state.quickenedOps++
		}
		return nil
	}
	value, err := m.getTableValue(regs[instr.B], regs[instr.C])
	if err != nil {
		return err
	}
	regs[instr.A] = value
	return nil
}

func (m *VM) executeCompiledSetTable(state *protoState, proto *bytecode.Proto, pc int, regs []rt.Value, instr bytecode.Instr) error {
	if table, index, ok := m.fastPlainArrayTarget(regs[instr.A], regs[instr.B]); ok {
		table.SetIndex(index, regs[instr.C])
		if state != nil && proto != nil && pc >= 0 && pc < len(proto.Code) && proto.Code[pc].Op != bytecode.OpSetTableArray {
			proto.Code[pc].Op = bytecode.OpSetTableArray
			state.quickenedOps++
		}
		return nil
	}
	return m.setTableValue(regs[instr.A], regs[instr.B], regs[instr.C])
}

func (m *VM) executeCompiledLen(state *protoState, proto *bytecode.Proto, pc int, regs []rt.Value, instr bytecode.Instr) error {
	if table, ok := m.fastPlainTable(regs[instr.B]); ok {
		regs[instr.A] = rt.NumberValue(float64(table.Length()))
		if state != nil && proto != nil && pc >= 0 && pc < len(proto.Code) && proto.Code[pc].Op != bytecode.OpLenTable {
			proto.Code[pc].Op = bytecode.OpLenTable
			state.quickenedOps++
		}
		return nil
	}
	result, err := m.lenValue(regs[instr.B])
	if err != nil {
		return err
	}
	regs[instr.A] = result
	return nil
}

func invalidateCompiledDirectCallCache(state *protoState, slot int) {
	if state == nil || slot < 0 || slot >= len(state.callCaches) {
		return
	}
	state.callCaches[slot] = jit.DirectCallCache{}
	if slot < len(state.directCallUnits) {
		state.directCallUnits[slot] = nil
	}
}

func compiledUnitCanDirectEnter(unit jit.CompiledUnit) bool {
	if unit == nil || unit.Entry() == 0 {
		return false
	}
	meta := unit.Meta()
	if meta == nil {
		return false
	}
	return meta.CodeSize > 1 && meta.Region.StartPC == 0
}

func compiledUnitTailReturnSafe(unit jit.CompiledUnit) bool {
	if !compiledUnitCanDirectEnter(unit) {
		return false
	}
	meta := unit.Meta()
	return len(meta.HelperCalls) == 0 && len(meta.SideExits) == 0
}

func (m *VM) primeCompiledDirectCallCache(state *protoState, slot int, callee rt.Value, closure *LuaClosure) {
	if state == nil || slot < 0 || slot >= len(state.callCaches) {
		return
	}
	invalidateCompiledDirectCallCache(state, slot)
	if closure == nil {
		return
	}
	calleeState := m.stateFor(closure.Proto)
	if calleeState.quickenedOps > calleeState.compiledQuickenedAt {
		return
	}
	unit := calleeState.compiled
	if !compiledUnitCanDirectEnter(unit) {
		return
	}
	cache := jit.DirectCallCache{
		Callee:   callee,
		Entry:    unit.Entry(),
		MaxStack: uint32(closure.Proto.MaxStack),
	}
	if !closure.Proto.Vararg {
		cache.Flags |= jit.DirectCallNoVararg
	}
	if compiledUnitTailReturnSafe(unit) {
		cache.Flags |= jit.DirectCallTailReturnSafe
	}
	if len(calleeState.fieldCaches) != 0 {
		cache.FieldCachesBase = uintptr(unsafe.Pointer(&calleeState.fieldCaches[0]))
		cache.FieldCachesLen = uintptr(len(calleeState.fieldCaches))
	}
	if len(calleeState.callCaches) != 0 {
		cache.CallCachesBase = uintptr(unsafe.Pointer(&calleeState.callCaches[0]))
		cache.CallCachesLen = uintptr(len(calleeState.callCaches))
	}
	if envHandle, ok := m.envOf(closure).Handle(); ok && envHandle.Kind() == rt.ObjectTable {
		cache.EnvHandle = envHandle
	}
	state.callCaches[slot] = cache
	if slot < len(state.directCallUnits) {
		state.directCallUnits[slot] = unit
	}
}

func (m *VM) executeTopLevelCompiledHelper(proto *bytecode.Proto, state *protoState, regs []rt.Value, exit jit.NativeExitRecord) (int, error) {
	desc, ok := state.compiled.Meta().HelperCallForID(exit.HelperID)
	if !ok {
		return 0, fmt.Errorf("compiled unit %q reported unknown helper id %d", state.compiled.Name(), exit.HelperID)
	}
	state.compiledHelperCalls++
	instr := proto.Code[desc.PC]
	switch desc.Kind {
	case jit.HelperGetField:
		if err := m.executeCompiledGetField(state, proto, desc.PC, regs, instr); err != nil {
			return 0, err
		}
	case jit.HelperAdd:
		value, err := m.addValues(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = value
		if regs[instr.B].IsNumber() && regs[instr.C].IsNumber() && proto.Code[desc.PC].Op != bytecode.OpAddNum {
			proto.Code[desc.PC].Op = bytecode.OpAddNum
			state.quickenedOps++
		}
	case jit.HelperSelf:
		if err := m.executeCompiledSelf(state, proto, desc.PC, regs, instr); err != nil {
			return 0, err
		}
	case jit.HelperGetTable:
		if err := m.executeCompiledGetTable(state, proto, desc.PC, regs, instr); err != nil {
			return 0, err
		}
	case jit.HelperSetTable:
		if err := m.executeCompiledSetTable(state, proto, desc.PC, regs, instr); err != nil {
			return 0, err
		}
	case jit.HelperLen:
		if err := m.executeCompiledLen(state, proto, desc.PC, regs, instr); err != nil {
			return 0, err
		}
	case jit.HelperGetFieldIC:
		cache := &state.fieldCaches[instr.C]
		value, found, err := m.runtime.GetFieldCached(regs[instr.B], cache)
		if err != nil {
			return 0, err
		}
		if !found {
			var handled bool
			value, handled, err = m.resolveFieldIndex(regs[instr.B], uint32(instr.D))
			if err != nil {
				return 0, err
			}
			if !handled {
				value = rt.NilValue
			}
		}
		regs[instr.A] = value
	case jit.HelperSelfIC:
		receiver := regs[instr.B]
		cache := &state.fieldCaches[instr.C]
		value, found, err := m.runtime.GetFieldCached(receiver, cache)
		if err != nil {
			return 0, err
		}
		if !found {
			var handled bool
			value, handled, err = m.resolveFieldIndex(receiver, uint32(instr.D))
			if err != nil {
				return 0, err
			}
			if !handled {
				value = rt.NilValue
			}
		}
		regs[instr.A] = value
		regs[instr.A+1] = receiver
	case jit.HelperGetTableArray:
		if table, index, ok := m.fastPlainArrayTarget(regs[instr.B], regs[instr.C]); ok {
			value, found := table.GetIndex(index)
			if !found {
				value = rt.NilValue
			}
			regs[instr.A] = value
			break
		}
		value, err := m.getTableValue(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = value
	case jit.HelperSetTableArray:
		if table, index, ok := m.fastPlainArrayTarget(regs[instr.A], regs[instr.B]); ok {
			table.SetIndex(index, regs[instr.C])
			break
		}
		if err := m.setTableValue(regs[instr.A], regs[instr.B], regs[instr.C]); err != nil {
			return 0, err
		}
	case jit.HelperLenTable:
		if table, ok := m.fastPlainTable(regs[instr.B]); ok {
			regs[instr.A] = rt.NumberValue(float64(table.Length()))
			break
		}
		value, err := m.lenValue(regs[instr.B])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = value
	case jit.HelperEqual:
		value, err := m.equalValues(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = rt.BoolValue(value)
	case jit.HelperLess:
		value, err := m.lessValues(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = rt.BoolValue(value)
	case jit.HelperLessEqual:
		value, err := m.lessEqualValues(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = rt.BoolValue(value)
	case jit.HelperNot:
		regs[instr.A] = rt.BoolValue(!isTruthy(regs[instr.B]))
	case jit.HelperUnm:
		value := regs[instr.B]
		if value.IsNumber() {
			regs[instr.A] = rt.NumberValue(-value.Number())
			break
		}
		result, err := m.callUnaryMetamethod("__unm", value)
		if err != nil {
			return 0, err
		}
		regs[instr.A] = result
	case jit.HelperConcat:
		value, err := m.concatValues(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = value
	case jit.HelperSub:
		value, err := m.subValues(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = value
	case jit.HelperMul:
		value, err := m.mulValues(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = value
	case jit.HelperDiv:
		value, err := m.divValues(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = value
	case jit.HelperMod:
		value, err := m.modValues(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = value
	case jit.HelperPow:
		value, err := m.powValues(regs[instr.B], regs[instr.C])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = value
	case jit.HelperLoadGlobal:
		value, ok := m.runtime.GetGlobalSymbol(uint32(instr.D))
		if !ok {
			value = rt.NilValue
		}
		regs[instr.A] = value
		m.primeCompiledFieldCache(state.fieldCaches, desc.InlineCacheSlot, m.globalEnv(), uint32(instr.D))
	case jit.HelperStoreGlobal:
		m.runtime.SetGlobalSymbol(uint32(instr.D), regs[instr.A])
	case jit.HelperSetField:
		if err := m.runtime.SetField(regs[instr.A], uint32(instr.D), regs[instr.B]); err != nil {
			return 0, err
		}
		m.primeCompiledFieldCache(state.fieldCaches, desc.InlineCacheSlot, regs[instr.A], uint32(instr.D))
	case jit.HelperNewTable:
		regs[instr.A] = m.runtime.NewTableValue(int(instr.D))
	case jit.HelperCallHostFunction:
		argCount := int(instr.D)
		result, err := m.runtime.CallValue(regs[instr.B], regs[int(instr.C):int(instr.C)+argCount])
		if err != nil {
			return 0, err
		}
		regs[instr.A] = result
	case jit.HelperCall:
		argCount := int(instr.D)
		args := make([]rt.Value, argCount)
		copy(args, regs[int(instr.C):int(instr.C)+argCount])
		result, err := m.runtime.CallValue(regs[instr.B], args)
		if err != nil {
			return 0, err
		}
		regs[instr.A] = result
	default:
		return 0, fmt.Errorf("top-level compiled helper %s is not supported", desc.Kind)
	}
	return desc.ResumePC, nil
}

func (m *VM) executeFrameCompiledHelper(co *Coroutine, frame *callFrame, exit jit.NativeExitRecord) (compiledFrameHelperOutcome, error) {
	meta := compiledMetaForFrame(frame)
	if meta == nil {
		return compiledFrameHelperOutcome{}, fmt.Errorf("compiled frame for %q has no metadata", frame.closure.Proto.Name)
	}
	desc, ok := meta.HelperCallForID(exit.HelperID)
	if !ok {
		return compiledFrameHelperOutcome{}, fmt.Errorf("compiled unit %q reported unknown helper id %d", compiledUnitForFrame(frame).Name(), exit.HelperID)
	}
	frame.state.compiledHelperCalls++
	instr := frame.closure.Proto.Code[desc.PC]
	switch desc.Kind {
	case jit.HelperGetField:
		if err := m.executeCompiledGetField(frame.state, frame.closure.Proto, desc.PC, frame.regs, instr); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
	case jit.HelperAdd:
		value, err := m.addValues(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = value
		if frame.regs[instr.B].IsNumber() && frame.regs[instr.C].IsNumber() && frame.closure.Proto.Code[desc.PC].Op != bytecode.OpAddNum {
			frame.closure.Proto.Code[desc.PC].Op = bytecode.OpAddNum
			frame.state.quickenedOps++
		}
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperSelf:
		if err := m.executeCompiledSelf(frame.state, frame.closure.Proto, desc.PC, frame.regs, instr); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
	case jit.HelperGetTable:
		if err := m.executeCompiledGetTable(frame.state, frame.closure.Proto, desc.PC, frame.regs, instr); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
	case jit.HelperSetTable:
		if err := m.executeCompiledSetTable(frame.state, frame.closure.Proto, desc.PC, frame.regs, instr); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
	case jit.HelperLen:
		if err := m.executeCompiledLen(frame.state, frame.closure.Proto, desc.PC, frame.regs, instr); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
	case jit.HelperLoadUpvalue:
		frame.regs[instr.A] = frame.closure.Upvalues[instr.B].Get()
	case jit.HelperStoreUpvalue:
		frame.closure.Upvalues[instr.B].Set(frame.regs[instr.A])
	case jit.HelperClosure:
		child := frame.closure.Proto.Children[instr.D]
		frame.regs[instr.A] = m.makeClosure(frame, child)
	case jit.HelperNewTable:
		frame.regs[instr.A] = m.runtime.NewTableValue(int(instr.D))
	case jit.HelperLoadGlobal:
		value, err := m.loadGlobal(frame.closure, uint32(instr.D))
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = value
		m.primeCompiledFieldCache(frame.state.fieldCaches, desc.InlineCacheSlot, m.envOf(frame.closure), uint32(instr.D))
	case jit.HelperStoreGlobal:
		if err := m.storeGlobal(frame.closure, uint32(instr.D), frame.regs[instr.A]); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
	case jit.HelperSetField:
		if err := m.setFieldValue(frame.regs[instr.A], uint32(instr.D), frame.regs[instr.B]); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		m.primeCompiledFieldCache(frame.state.fieldCaches, desc.InlineCacheSlot, frame.regs[instr.A], uint32(instr.D))
	case jit.HelperGetFieldIC:
		cache := &frame.state.fieldCaches[instr.C]
		value, found, err := m.runtime.GetFieldCached(frame.regs[instr.B], cache)
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		if !found {
			var handled bool
			value, handled, err = m.resolveFieldIndex(frame.regs[instr.B], uint32(instr.D))
			if err != nil {
				return compiledFrameHelperOutcome{}, err
			}
			if !handled {
				value = rt.NilValue
			}
		}
		frame.regs[instr.A] = value
	case jit.HelperSelfIC:
		receiver := frame.regs[instr.B]
		cache := &frame.state.fieldCaches[instr.C]
		value, found, err := m.runtime.GetFieldCached(receiver, cache)
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		if !found {
			var handled bool
			value, handled, err = m.resolveFieldIndex(receiver, uint32(instr.D))
			if err != nil {
				return compiledFrameHelperOutcome{}, err
			}
			if !handled {
				value = rt.NilValue
			}
		}
		frame.regs[instr.A] = value
		frame.regs[instr.A+1] = receiver
	case jit.HelperGetTableArray:
		if table, index, ok := m.fastPlainArrayTarget(frame.regs[instr.B], frame.regs[instr.C]); ok {
			value, found := table.GetIndex(index)
			if !found {
				value = rt.NilValue
			}
			frame.regs[instr.A] = value
			break
		}
		value, err := m.getTableValue(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = value
	case jit.HelperSetTableArray:
		if table, index, ok := m.fastPlainArrayTarget(frame.regs[instr.A], frame.regs[instr.B]); ok {
			table.SetIndex(index, frame.regs[instr.C])
			break
		}
		if err := m.setTableValue(frame.regs[instr.A], frame.regs[instr.B], frame.regs[instr.C]); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
	case jit.HelperLenTable:
		if table, ok := m.fastPlainTable(frame.regs[instr.B]); ok {
			frame.regs[instr.A] = rt.NumberValue(float64(table.Length()))
			break
		}
		result, err := m.lenValue(frame.regs[instr.B])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = result
	case jit.HelperEqual:
		value, err := m.equalValues(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = rt.BoolValue(value)
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperLess:
		value, err := m.lessValues(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = rt.BoolValue(value)
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperLessEqual:
		value, err := m.lessEqualValues(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = rt.BoolValue(value)
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperNot:
		frame.regs[instr.A] = rt.BoolValue(!isTruthy(frame.regs[instr.B]))
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperUnm:
		value := frame.regs[instr.B]
		if value.IsNumber() {
			frame.regs[instr.A] = rt.NumberValue(-value.Number())
			return resumeCompiledFrame(desc.ResumePC), nil
		}
		result, err := m.callUnaryMetamethod("__unm", value)
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = result
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperConcat:
		value, err := m.concatValues(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = value
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperSub:
		value, err := m.subValues(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = value
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperMul:
		value, err := m.mulValues(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = value
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperDiv:
		value, err := m.divValues(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = value
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperMod:
		value, err := m.modValues(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = value
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperPow:
		value, err := m.powValues(frame.regs[instr.B], frame.regs[instr.C])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = value
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperCallHostFunction:
		argCount := int(instr.D)
		result, err := m.runtime.CallValue(frame.regs[instr.B], frame.regs[int(instr.C):int(instr.C)+argCount])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = result
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperCall:
		argCount := int(instr.D)
		callee := frame.regs[instr.B]
		args := frame.regs[int(instr.C) : int(instr.C)+argCount]
		if h, ok := callee.Handle(); ok && h.Kind() == rt.ObjectLuaClosure {
			closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
			frame.pc = desc.ResumePC
			m.pushFrame(co, closure, args, int(instr.A), 1)
			if err := m.dispatchCallHookForFrames(co, co.frames[len(co.frames)-1], frame); err != nil {
				return compiledFrameHelperOutcome{}, err
			}
			return continueCoroutineLoop(), nil
		}
		frame.beginGCCall(callee, args, int(instr.A), 1, false, false)
		restore := m.pushActiveFrame(frame)
		result, err := m.callValue(callee, args)
		restore()
		frame.endGCCall()
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.regs[instr.A] = result
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperCallLuaClosure:
		callee := frame.regs[instr.B]
		h, ok := callee.Handle()
		if !ok || h.Kind() != rt.ObjectLuaClosure {
			invalidateCompiledDirectCallCache(frame.state, desc.CallCacheSlot)
			return compiledFrameHelperOutcome{}, fmt.Errorf("expected lua closure for fast call helper, got %s", callee)
		}
		closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
		argCount := int(instr.D)
		args := frame.regs[int(instr.C) : int(instr.C)+argCount]
		outcome, err := m.runLuaClosureHelperCall(co, frame, closure, args, int(instr.A), 1, desc.ResumePC, false)
		if err != nil {
			invalidateCompiledDirectCallCache(frame.state, desc.CallCacheSlot)
			return compiledFrameHelperOutcome{}, err
		}
		if outcome.reenter {
			m.primeCompiledDirectCallCache(frame.state, desc.CallCacheSlot, callee, closure)
		} else {
			invalidateCompiledDirectCallCache(frame.state, desc.CallCacheSlot)
		}
		return outcome, nil
	case jit.HelperCallMulti:
		callee := frame.regs[instr.B]
		argCount, resultCount, appendPending := bytecode.UnpackCallSpec(instr.D)
		args := frame.callArgs(int(instr.C), argCount, appendPending)
		if h, ok := callee.Handle(); ok && h.Kind() == rt.ObjectLuaClosure {
			closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
			outcome, err := m.runLuaClosureHelperCall(co, frame, closure, args, int(instr.A), resultCount, desc.ResumePC, appendPending)
			if err != nil {
				invalidateCompiledDirectCallCache(frame.state, desc.CallCacheSlot)
				return compiledFrameHelperOutcome{}, err
			}
			if outcome.reenter {
				m.primeCompiledDirectCallCache(frame.state, desc.CallCacheSlot, callee, closure)
			} else {
				invalidateCompiledDirectCallCache(frame.state, desc.CallCacheSlot)
			}
			return outcome, nil
		}
		invalidateCompiledDirectCallCache(frame.state, desc.CallCacheSlot)
		frame.beginGCCall(callee, args, int(instr.A), resultCount, resultCount == 0, false)
		restore := m.pushActiveFrame(frame)
		results, err := m.callValueMulti(callee, args)
		restore()
		frame.endGCCall()
		if appendPending {
			frame.clearPendingResults()
		}
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		frame.storeResults(int(instr.A), resultCount, results)
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperTailCall:
		argCount, _, appendPending := bytecode.UnpackCallSpec(instr.D)
		callee := frame.regs[instr.B]
		args := frame.callArgs(int(instr.C), argCount, appendPending)
		if appendPending {
			frame.clearPendingResults()
		}
		resolvedCallee, resolvedArgs, err := m.resolveCallTarget(callee, args)
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		if h, ok := resolvedCallee.Handle(); ok && h.Kind() == rt.ObjectLuaClosure {
			closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
			m.primeCompiledDirectCallCache(frame.state, desc.CallCacheSlot, resolvedCallee, closure)
			if co != nil && co.hook.function.Kind() != rt.KindNil && !co.hook.running {
				frame.tailPending = true
				frame.tailHookEvent = "tail return"
				m.pushFrame(co, closure, resolvedArgs, -1, 0)
				if err := m.dispatchCallHookForFrames(co, co.frames[len(co.frames)-1], frame); err != nil {
					return compiledFrameHelperOutcome{}, err
				}
				return continueCoroutineLoop(), nil
			}
			m.tailCallFrame(co, frame, closure, resolvedArgs)
			return continueCoroutineLoop(), nil
		}
		invalidateCompiledDirectCallCache(frame.state, desc.CallCacheSlot)
		if co != nil && co.hook.function.Kind() != rt.KindNil && !co.hook.running {
			frame.tailPending = true
			frame.tailHookEvent = "return"
		}
		frame.beginGCCall(resolvedCallee, resolvedArgs, -1, 0, false, true)
		restore := m.pushActiveFrame(frame)
		results, err := m.callValueMulti(resolvedCallee, resolvedArgs)
		restore()
		frame.endGCCall()
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		if frame.tailPending {
			if err := m.unwindTailFrames(co, results, frame.returnReg, frame.returnCount, nil); err != nil {
				return compiledFrameHelperOutcome{}, err
			}
			return continueCoroutineLoop(), nil
		}
		if err := m.returnFromFrame(co, results); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		return continueCoroutineLoop(), nil
	case jit.HelperAppendTable:
		h, ok := frame.regs[instr.A].Handle()
		if !ok || h.Kind() != rt.ObjectTable {
			return compiledFrameHelperOutcome{}, fmt.Errorf("table append expects table")
		}
		table := m.runtime.Heap().Table(h)
		start := int(instr.B)
		prefix := int(instr.C)
		for i := 0; i < prefix; i++ {
			table.SetIndex(start+i, frame.regs[int(instr.A)+1+i])
		}
		for i, value := range frame.pendingValues() {
			table.SetIndex(start+prefix+i, value)
		}
		frame.clearPendingResults()
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperReturnAppendPending:
		results := frame.returnResults(int(instr.A), int(instr.B))
		if err := m.returnFromFrame(co, results); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		return continueCoroutineLoop(), nil
	case jit.HelperVararg:
		count := int(instr.B)
		frame.storeResults(int(instr.A), count, frame.varargValues())
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperYield:
		yieldCount, resumeCount, appendPending := bytecode.UnpackCallSpec(instr.D)
		frame.pc = desc.ResumePC
		co.status = CoroutineSuspended
		co.resumeReg = int(instr.A)
		co.resumeCount = resumeCount
		co.setYieldedResults(frame.callArgs(int(instr.B), yieldCount, appendPending))
		if appendPending {
			frame.clearPendingResults()
		}
		return continueCoroutineLoop(), nil
	case jit.HelperIterPairs:
		nextKey, nextValue, found, err := m.runtime.Next(frame.regs[instr.B], frame.regs[instr.A])
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		if !found {
			frame.clearRange(int(instr.A), int(instr.C))
			return resumeCompiledFrame(desc.ResumePC), nil
		}
		frame.regs[instr.A] = nextKey
		if int(instr.C) > 1 {
			frame.regs[instr.A+1] = nextValue
		}
		if int(instr.C) > 2 {
			frame.clearRange(int(instr.A)+2, int(instr.C)-2)
		}
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperIterIPairs:
		index := 0
		if frame.regs[instr.A].IsNumber() {
			index = int(frame.regs[instr.A].Number())
		}
		index++
		value, found, err := m.runtime.GetTable(frame.regs[instr.B], rt.NumberValue(float64(index)))
		if err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		if !found || value.Kind() == rt.KindNil {
			frame.clearRange(int(instr.A), int(instr.C))
			return resumeCompiledFrame(desc.ResumePC), nil
		}
		frame.regs[instr.A] = rt.NumberValue(float64(index))
		if int(instr.C) > 1 {
			frame.regs[instr.A+1] = value
		}
		if int(instr.C) > 2 {
			frame.clearRange(int(instr.A)+2, int(instr.C)-2)
		}
		return resumeCompiledFrame(desc.ResumePC), nil
	case jit.HelperReturnMulti:
		if err := m.returnFromFrame(co, frame.regs[int(instr.A):int(instr.A)+int(instr.B)]); err != nil {
			return compiledFrameHelperOutcome{}, err
		}
		return continueCoroutineLoop(), nil
	case jit.HelperClose:
		frame.closeUpvaluesFrom(int(instr.A))
		return resumeCompiledFrame(desc.ResumePC), nil
	default:
		return compiledFrameHelperOutcome{}, fmt.Errorf("scripted compiled helper %s is not supported", desc.Kind)
	}
	_ = co
	return resumeCompiledFrame(desc.ResumePC), nil
}

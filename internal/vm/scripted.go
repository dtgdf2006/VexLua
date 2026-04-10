package vm

import (
	"fmt"
	"math"

	"vexlua/internal/bytecode"
	"vexlua/internal/jit"
	rt "vexlua/internal/runtime"
)

type LuaClosure struct {
	Proto    *bytecode.Proto
	Upvalues []*upvalue
	Env      rt.Value
	inline   [2]*upvalue
}

type CoroutineStatus string

const (
	CoroutineNew         CoroutineStatus = "suspended"
	CoroutineRunning     CoroutineStatus = "running"
	CoroutineSuspended   CoroutineStatus = "suspended"
	CoroutineDead        CoroutineStatus = "dead"
	smallInlineValueCap                  = 4
	smallScratchValueCap                 = 8
	minCoroutineStackCap                 = 32
)

type Coroutine struct {
	machine       *VM
	entry         rt.Value
	proxy         rt.Value
	visible       bool
	helper        bool
	hook          hookState
	hookTouched   bool
	frames        []*callFrame
	status        CoroutineStatus
	started       bool
	resumeReg     int
	resumeCount   int
	lastResult    rt.Value
	lastResults   []rt.Value
	lastInline    [smallInlineValueCap]rt.Value
	yielded       rt.Value
	yieldedResult []rt.Value
	yieldedInline [smallInlineValueCap]rt.Value
	argBuf        [1]rt.Value
	stack         []rt.Value
	stackTop      int
}

type callFrame struct {
	state           *protoState
	closure         *LuaClosure
	compiledUnit    jit.CompiledUnit
	nativeFrame     jit.NativeFrameState
	nativeUpvalues  []jit.NativeUpvalue
	regs            []rt.Value
	base            int
	stackSize       int
	pc              int
	tailCalls       int
	tailLoss        int
	lastHookLine    int
	lastHookPC      int
	returnReg       int
	returnCount     int
	openCount       int
	varargs         []rt.Value
	varargsInline   [smallInlineValueCap]rt.Value
	pendingResults  []rt.Value
	pendingInline   [smallInlineValueCap]rt.Value
	argScratch      []rt.Value
	argInline       [smallScratchValueCap]rt.Value
	resultScratch   []rt.Value
	resultInline    [smallScratchValueCap]rt.Value
	gcRoots         []rt.Value
	gcOverwriteReg  int
	gcOverwriteCnt  int
	gcOverwritePend bool
	gcFrameDead     bool
	tailPending     bool
	tailHookEvent   string
	skipTailUnwind  bool
	openUpvalues    []*upvalue
	nativeUpInline  [2]jit.NativeUpvalue
}

type upvalueCell struct {
	value rt.Value
}

type upvalue struct {
	cell   upvalueCell
	stack  []rt.Value
	index  int
	isOpen bool
}

func resizeOpenUpvalues(slots []*upvalue, size int) []*upvalue {
	if cap(slots) < size {
		return make([]*upvalue, size)
	}
	slots = slots[:size]
	clear(slots)
	return slots
}

func (u *upvalue) Get() rt.Value {
	if u.isOpen {
		return u.stack[u.index]
	}
	return u.cell.value
}

func (u *upvalue) Set(v rt.Value) {
	if u.isOpen {
		u.stack[u.index] = v
	}
	u.cell.value = v
}

func newLuaClosure(proto *bytecode.Proto) *LuaClosure {
	closure := &LuaClosure{Proto: proto}
	if count := len(proto.Upvalues); count > 0 {
		if count <= len(closure.inline) {
			closure.Upvalues = closure.inline[:count]
		} else {
			closure.Upvalues = make([]*upvalue, count)
		}
	}
	return closure
}

func newCoroutine(machine *VM, entry rt.Value) Coroutine {
	return Coroutine{machine: machine, entry: entry, proxy: rt.NilValue, status: CoroutineNew, resumeReg: -1}
}

func (m *VM) NewClosureValue(proto *bytecode.Proto) rt.Value {
	closure := newLuaClosure(proto)
	closure.Env = m.globalEnv()
	return rt.HandleValue(m.runtime.Heap().NewLuaClosure(closure))
}

func (m *VM) NewCoroutine(entry rt.Value) (*Coroutine, error) {
	if !m.isCallable(entry) {
		return nil, fmt.Errorf("attempt to create coroutine from non-callable value %s", entry)
	}
	co := newCoroutine(m, entry)
	co.visible = true
	return &co, nil
}

func (m *VM) ResumeCoroutine(co *Coroutine, arg rt.Value) (rt.Value, error) {
	var args []rt.Value
	if arg.Kind() != rt.KindNil {
		co.argBuf[0] = arg
		args = co.argBuf[:1]
	}
	results, err := m.ResumeCoroutineMulti(co, args)
	if err != nil {
		return rt.NilValue, err
	}
	if len(results) == 0 {
		return rt.NilValue, nil
	}
	return results[0], nil
}

func (m *VM) ResumeCoroutineMulti(co *Coroutine, args []rt.Value) ([]rt.Value, error) {
	if co == nil {
		return nil, fmt.Errorf("coroutine is nil")
	}
	if co.status == CoroutineDead {
		return nil, fmt.Errorf("cannot resume dead coroutine")
	}
	restoreCo := m.pushActiveCoroutine(co)
	defer restoreCo()
	if !co.started {
		co.started = true
		h, ok := co.entry.Handle()
		if ok && h.Kind() == rt.ObjectLuaClosure {
			closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
			m.pushFrame(co, closure, args, -1, 0)
			if err := m.dispatchCallHookForFrames(co, co.frames[len(co.frames)-1], nil); err != nil {
				return nil, err
			}
		} else {
			result, err := m.runtime.CallValueMulti(co.entry, args)
			co.status = CoroutineDead
			co.setResults(result)
			return result, err
		}
	} else if co.status == CoroutineSuspended {
		if len(co.frames) == 0 {
			return nil, fmt.Errorf("coroutine suspended without frame")
		}
		frame := co.frames[len(co.frames)-1]
		if co.resumeReg >= 0 {
			frame.storeResults(co.resumeReg, co.resumeCount, args)
		}
		co.resumeReg = -1
		co.resumeCount = 0
	}
	return m.executeCoroutine(co)
}

func (m *VM) CoroutineStatus(co *Coroutine) string {
	if co == nil {
		return string(CoroutineDead)
	}
	return string(co.status)
}

func (m *VM) runScripted(proto *bytecode.Proto) (rt.Value, error) {
	state := m.stateFor(proto)
	if state.rootClosure == 0 {
		state.rootClosure = m.NewClosureValue(proto)
	}
	co := newCoroutine(m, state.rootClosure)
	results, err := m.ResumeCoroutineMulti(&co, nil)
	if err != nil {
		return rt.NilValue, err
	}
	if len(results) == 0 {
		return rt.NilValue, nil
	}
	return results[0], nil
}

func (m *VM) executeCoroutine(co *Coroutine) ([]rt.Value, error) {
	return m.executeCoroutineUntil(co, 0)
}

func (m *VM) executeCoroutineUntil(co *Coroutine, targetDepth int) ([]rt.Value, error) {
	co.status = CoroutineRunning
	for len(co.frames) > targetDepth {
		frame := co.frames[len(co.frames)-1]
		proto := frame.closure.Proto
		state := frame.state
		if state == nil {
			state = m.stateFor(proto)
			frame.state = state
		}
		if err := m.maybeDispatchStepHooks(co, frame); err != nil {
			return nil, err
		}
		if frame.pc == 0 {
			state.runs++
		}
		handled, err := m.maybeRunCompiledFrame(co, frame)
		if err != nil {
			return nil, err
		}
		if handled {
			if co.status == CoroutineSuspended {
				return append([]rt.Value(nil), co.yieldedResult...), nil
			}
			continue
		}
		if frame.pc >= len(proto.Code) {
			if err := m.returnFromFrame(co, frame.singleResult(rt.NilValue)); err != nil {
				return nil, err
			}
			continue
		}
		instr := &proto.Code[frame.pc]
		frame.pc++
		switch instr.Op {
		case bytecode.OpNoop:
		case bytecode.OpLoadConst:
			frame.regs[instr.A] = proto.Constants[instr.D]
		case bytecode.OpMove:
			frame.regs[instr.A] = frame.regs[instr.B]
		case bytecode.OpLoadUpvalue:
			frame.regs[instr.A] = frame.closure.Upvalues[instr.B].Get()
		case bytecode.OpStoreUpvalue:
			frame.closure.Upvalues[instr.B].Set(frame.regs[instr.A])
		case bytecode.OpClosure:
			child := proto.Children[instr.D]
			frame.regs[instr.A] = m.makeClosure(frame, child)
		case bytecode.OpNewTable:
			frame.regs[instr.A] = m.runtime.NewTableValue(int(instr.D))
		case bytecode.OpLoadGlobal:
			value, err := m.loadGlobal(frame.closure, uint32(instr.D))
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = value
		case bytecode.OpStoreGlobal:
			if err := m.storeGlobal(frame.closure, uint32(instr.D), frame.regs[instr.A]); err != nil {
				return nil, err
			}
		case bytecode.OpGetField:
			value, slot, found, err := m.runtime.GetField(frame.regs[instr.B], uint32(instr.D))
			if err != nil {
				value, handled, metaErr := m.resolveFieldIndex(frame.regs[instr.B], uint32(instr.D))
				if metaErr != nil {
					return nil, metaErr
				}
				if !handled {
					return nil, err
				}
				frame.regs[instr.A] = value
				continue
			}
			if found {
				frame.regs[instr.A] = value
				if int(instr.C) < len(state.fieldCaches) {
					target, ok := frame.regs[instr.B].Handle()
					if ok && target.Kind() == rt.ObjectTable {
						cache := &state.fieldCaches[instr.C]
						cache.Valid = true
						cache.Table = target
						cache.Version = m.runtime.Heap().Table(target).Version()
						cache.Slot = slot
						cache.Symbol = uint32(instr.D)
						instr.Op = bytecode.OpGetFieldIC
						state.quickenedOps++
					}
				}
				continue
			}
			value, handled, err := m.resolveFieldIndex(frame.regs[instr.B], uint32(instr.D))
			if err != nil {
				return nil, err
			}
			if !handled {
				value = rt.NilValue
			}
			frame.regs[instr.A] = value
		case bytecode.OpSelf:
			receiver := frame.regs[instr.B]
			value, slot, found, err := m.runtime.GetField(receiver, uint32(instr.D))
			if err != nil {
				value, handled, metaErr := m.resolveFieldIndex(receiver, uint32(instr.D))
				if metaErr != nil {
					return nil, metaErr
				}
				if !handled {
					return nil, err
				}
				frame.regs[instr.A] = value
				frame.regs[instr.A+1] = receiver
				continue
			}
			if found {
				frame.regs[instr.A] = value
				frame.regs[instr.A+1] = receiver
				if int(instr.C) < len(state.fieldCaches) {
					target, ok := receiver.Handle()
					if ok && target.Kind() == rt.ObjectTable {
						cache := &state.fieldCaches[instr.C]
						cache.Valid = true
						cache.Table = target
						cache.Version = m.runtime.Heap().Table(target).Version()
						cache.Slot = slot
						cache.Symbol = uint32(instr.D)
						instr.Op = bytecode.OpSelfIC
						state.quickenedOps++
					}
				}
				continue
			}
			value, handled, err := m.resolveFieldIndex(receiver, uint32(instr.D))
			if err != nil {
				return nil, err
			}
			if !handled {
				value = rt.NilValue
			}
			frame.regs[instr.A] = value
			frame.regs[instr.A+1] = receiver
		case bytecode.OpGetFieldIC:
			cache := &state.fieldCaches[instr.C]
			value, found, err := m.runtime.GetFieldCached(frame.regs[instr.B], cache)
			if err != nil {
				return nil, err
			}
			if found {
				frame.regs[instr.A] = value
				continue
			}
			value, handled, err := m.resolveFieldIndex(frame.regs[instr.B], uint32(instr.D))
			if err != nil {
				return nil, err
			}
			if !handled {
				value = rt.NilValue
			}
			frame.regs[instr.A] = value
		case bytecode.OpSelfIC:
			receiver := frame.regs[instr.B]
			cache := &state.fieldCaches[instr.C]
			value, found, err := m.runtime.GetFieldCached(receiver, cache)
			if err != nil {
				return nil, err
			}
			if found {
				frame.regs[instr.A] = value
				frame.regs[instr.A+1] = receiver
				continue
			}
			value, handled, err := m.resolveFieldIndex(receiver, uint32(instr.D))
			if err != nil {
				return nil, err
			}
			if !handled {
				value = rt.NilValue
			}
			frame.regs[instr.A] = value
			frame.regs[instr.A+1] = receiver
		case bytecode.OpSetField:
			if err := m.setFieldValue(frame.regs[instr.A], uint32(instr.D), frame.regs[instr.B]); err != nil {
				return nil, err
			}
		case bytecode.OpGetTable:
			if table, index, ok := m.fastPlainArrayTarget(frame.regs[instr.B], frame.regs[instr.C]); ok {
				value, found := table.GetIndex(index)
				if !found {
					value = rt.NilValue
				}
				frame.regs[instr.A] = value
				instr.Op = bytecode.OpGetTableArray
				state.quickenedOps++
				continue
			}
			value, err := m.getTableValue(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = value
		case bytecode.OpGetTableArray:
			if table, index, ok := m.fastPlainArrayTarget(frame.regs[instr.B], frame.regs[instr.C]); ok {
				value, found := table.GetIndex(index)
				if !found {
					value = rt.NilValue
				}
				frame.regs[instr.A] = value
				continue
			}
			value, err := m.getTableValue(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = value
		case bytecode.OpSetTable:
			if table, index, ok := m.fastPlainArrayTarget(frame.regs[instr.A], frame.regs[instr.B]); ok {
				table.SetIndex(index, frame.regs[instr.C])
				instr.Op = bytecode.OpSetTableArray
				state.quickenedOps++
				continue
			}
			if err := m.setTableValue(frame.regs[instr.A], frame.regs[instr.B], frame.regs[instr.C]); err != nil {
				return nil, err
			}
		case bytecode.OpSetTableArray:
			if table, index, ok := m.fastPlainArrayTarget(frame.regs[instr.A], frame.regs[instr.B]); ok {
				table.SetIndex(index, frame.regs[instr.C])
				continue
			}
			if err := m.setTableValue(frame.regs[instr.A], frame.regs[instr.B], frame.regs[instr.C]); err != nil {
				return nil, err
			}
		case bytecode.OpAppendTable:
			h, ok := frame.regs[instr.A].Handle()
			if !ok || h.Kind() != rt.ObjectTable {
				return nil, fmt.Errorf("table append expects table")
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
		case bytecode.OpAdd:
			result, err := m.addValues(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
			if frame.regs[instr.B].IsNumber() && frame.regs[instr.C].IsNumber() {
				instr.Op = bytecode.OpAddNum
				state.quickenedOps++
			}
		case bytecode.OpAddNum:
			frame.regs[instr.A] = rt.NumberValue(frame.regs[instr.B].Number() + frame.regs[instr.C].Number())
		case bytecode.OpAddConst:
			frame.regs[instr.A] = rt.NumberValue(frame.regs[instr.B].Number() + proto.Constants[instr.D].Number())
		case bytecode.OpUnm:
			value := frame.regs[instr.B]
			if value.IsNumber() {
				frame.regs[instr.A] = rt.NumberValue(-value.Number())
				continue
			}
			result, err := m.callUnaryMetamethod("__unm", value)
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpSub:
			result, err := m.subValues(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpMul:
			result, err := m.mulValues(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpDiv:
			result, err := m.divValues(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpMod:
			result, err := m.modValues(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpPow:
			result, err := m.powValues(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpLen:
			if table, ok := m.fastPlainTable(frame.regs[instr.B]); ok {
				frame.regs[instr.A] = rt.NumberValue(float64(table.Length()))
				instr.Op = bytecode.OpLenTable
				state.quickenedOps++
				continue
			}
			result, err := m.lenValue(frame.regs[instr.B])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpLenTable:
			if table, ok := m.fastPlainTable(frame.regs[instr.B]); ok {
				frame.regs[instr.A] = rt.NumberValue(float64(table.Length()))
				continue
			}
			result, err := m.lenValue(frame.regs[instr.B])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpConcat:
			result, err := m.concatValues(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpEqual:
			value, err := m.equalValues(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = rt.BoolValue(value)
		case bytecode.OpLess:
			value, err := m.lessValues(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = rt.BoolValue(value)
		case bytecode.OpLessEqual:
			value, err := m.lessEqualValues(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = rt.BoolValue(value)
		case bytecode.OpNot:
			frame.regs[instr.A] = rt.BoolValue(!isTruthy(frame.regs[instr.B]))
		case bytecode.OpIterPairs:
			nextKey, nextValue, found, err := m.runtime.Next(frame.regs[instr.B], frame.regs[instr.A])
			if err != nil {
				return nil, err
			}
			if !found {
				frame.clearRange(int(instr.A), int(instr.C))
				continue
			}
			frame.regs[instr.A] = nextKey
			if int(instr.C) > 1 {
				frame.regs[instr.A+1] = nextValue
			}
			if int(instr.C) > 2 {
				frame.clearRange(int(instr.A)+2, int(instr.C)-2)
			}
		case bytecode.OpIterIPairs:
			index := 0
			if frame.regs[instr.A].IsNumber() {
				index = int(frame.regs[instr.A].Number())
			}
			index++
			value, found, err := m.runtime.GetTable(frame.regs[instr.B], rt.NumberValue(float64(index)))
			if err != nil {
				return nil, err
			}
			if !found || value.Kind() == rt.KindNil {
				frame.clearRange(int(instr.A), int(instr.C))
				continue
			}
			frame.regs[instr.A] = rt.NumberValue(float64(index))
			if int(instr.C) > 1 {
				frame.regs[instr.A+1] = value
			}
			if int(instr.C) > 2 {
				frame.clearRange(int(instr.A)+2, int(instr.C)-2)
			}
		case bytecode.OpCall:
			callee := frame.regs[instr.B]
			argCount := int(instr.D)
			args := frame.regs[int(instr.C) : int(instr.C)+argCount]
			if h, ok := callee.Handle(); ok && h.Kind() == rt.ObjectLuaClosure {
				closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
				m.pushFrame(co, closure, args, int(instr.A), 1)
				if err := m.dispatchCallHookForFrames(co, co.frames[len(co.frames)-1], frame); err != nil {
					return nil, err
				}
				continue
			}
			frame.beginGCCall(callee, args, int(instr.A), 1, false, false)
			restore := m.pushActiveFrame(frame)
			result, err := m.callValue(callee, args)
			restore()
			frame.endGCCall()
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpTailCall:
			argCount, _, appendPending := bytecode.UnpackCallSpec(instr.D)
			callee := frame.regs[instr.B]
			args := frame.callArgs(int(instr.C), argCount, appendPending)
			if appendPending {
				frame.clearPendingResults()
			}
			resolvedCallee, resolvedArgs, err := m.resolveCallTarget(callee, args)
			if err != nil {
				return nil, err
			}
			if h, ok := resolvedCallee.Handle(); ok && h.Kind() == rt.ObjectLuaClosure {
				closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
				if co != nil && co.hook.function.Kind() != rt.KindNil && !co.hook.running {
					frame.tailPending = true
					frame.tailHookEvent = "tail return"
					m.pushFrame(co, closure, resolvedArgs, -1, 0)
					if err := m.dispatchCallHookForFrames(co, co.frames[len(co.frames)-1], frame); err != nil {
						return nil, err
					}
					continue
				}
				m.tailCallFrame(co, frame, closure, resolvedArgs)
				continue
			}
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
				return nil, err
			}
			if frame.tailPending {
				if err := m.unwindTailFrames(co, results, frame.returnReg, frame.returnCount, nil); err != nil {
					return nil, err
				}
				continue
			}
			if err := m.returnFromFrame(co, results); err != nil {
				return nil, err
			}
		case bytecode.OpCallMulti:
			callee := frame.regs[instr.B]
			argCount, resultCount, appendPending := bytecode.UnpackCallSpec(instr.D)
			args := frame.callArgs(int(instr.C), argCount, appendPending)
			if h, ok := callee.Handle(); ok && h.Kind() == rt.ObjectLuaClosure {
				closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
				m.pushFrame(co, closure, args, int(instr.A), resultCount)
				if err := m.dispatchCallHookForFrames(co, co.frames[len(co.frames)-1], frame); err != nil {
					return nil, err
				}
				if appendPending {
					frame.clearPendingResults()
				}
				continue
			}
			frame.beginGCCall(callee, args, int(instr.A), resultCount, resultCount == 0, false)
			restore := m.pushActiveFrame(frame)
			results, err := m.callValueMulti(callee, args)
			restore()
			frame.endGCCall()
			if appendPending {
				frame.clearPendingResults()
			}
			if err != nil {
				return nil, err
			}
			frame.storeResults(int(instr.A), resultCount, results)
		case bytecode.OpVararg:
			count := int(instr.B)
			frame.storeResults(int(instr.A), count, frame.varargValues())
		case bytecode.OpYield:
			yieldCount, resumeCount, appendPending := bytecode.UnpackCallSpec(instr.D)
			co.status = CoroutineSuspended
			co.resumeReg = int(instr.A)
			co.resumeCount = resumeCount
			co.setYieldedResults(frame.callArgs(int(instr.B), yieldCount, appendPending))
			if appendPending {
				frame.clearPendingResults()
			}
			return append([]rt.Value(nil), co.yieldedResult...), nil
		case bytecode.OpJump:
			frame.pc = int(instr.D)
		case bytecode.OpJumpIfFalse:
			if !isTruthy(frame.regs[instr.A]) {
				frame.pc = int(instr.D)
			}
		case bytecode.OpJumpIfTrue:
			if isTruthy(frame.regs[instr.A]) {
				frame.pc = int(instr.D)
			}
		case bytecode.OpLessEqualJump:
			lhs := frame.regs[instr.A]
			rhs := frame.regs[instr.B]
			if !lhs.IsNumber() || !rhs.IsNumber() {
				return nil, fmt.Errorf("LE_JUMP expects numbers, got %s and %s", lhs, rhs)
			}
			if lhs.Number() <= rhs.Number() {
				frame.pc = int(instr.D)
			}
		case bytecode.OpClose:
			frame.closeUpvaluesFrom(int(instr.A))
		case bytecode.OpReturn:
			if err := m.returnFromFrame(co, frame.regs[instr.A:int(instr.A)+1]); err != nil {
				return nil, err
			}
		case bytecode.OpReturnMulti:
			if err := m.returnFromFrame(co, frame.regs[int(instr.A):int(instr.A)+int(instr.B)]); err != nil {
				return nil, err
			}
		case bytecode.OpReturnAppendPending:
			results := frame.returnResults(int(instr.A), int(instr.B))
			if err := m.returnFromFrame(co, results); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unknown scripted opcode %s", instr.Op)
		}
	}
	if targetDepth == 0 {
		return append([]rt.Value(nil), co.lastResults...), nil
	}
	return nil, nil
}

func (m *VM) pushFrame(co *Coroutine, closure *LuaClosure, args []rt.Value, returnReg int, returnCount int) {
	frame := m.acquireFrame(co, closure, returnReg, returnCount)
	m.bindFrameArgs(frame, args)
	co.frames = append(co.frames, frame)
}

func (m *VM) tailCallFrame(co *Coroutine, frame *callFrame, closure *LuaClosure, args []rt.Value) {
	boundArgs := frame.copyArgs(args)
	if len(co.frames) > 1 {
		co.frames[len(co.frames)-2].tailCalls++
		frame.tailLoss++
	}
	state := m.stateFor(closure.Proto)
	frame.closeUpvalues()
	frame.state = state
	co.stackTop = frame.base
	frame.closure = closure
	frame.compiledUnit = nil
	frame.stackSize = closure.Proto.MaxStack
	frame.regs = m.reserveRegisterWindow(co, frame.base, frame.stackSize)
	co.stackTop = frame.base + frame.stackSize
	frame.pc = 0
	frame.openCount = 0
	frame.nativeFrame.Reset()
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
	frame.tailCalls = 0
	frame.lastHookLine = -1
	frame.lastHookPC = -1
	frame.openUpvalues = resizeOpenUpvalues(frame.openUpvalues, closure.Proto.MaxStack)
	m.bindFrameArgs(frame, boundArgs)
}

func (m *VM) bindFrameArgs(frame *callFrame, args []rt.Value) {
	paramCount := frame.closure.Proto.NumParams
	if paramCount > len(args) {
		paramCount = len(args)
	}
	switch paramCount {
	case 0:
	case 1:
		frame.regs[0] = args[0]
	case 2:
		frame.regs[0] = args[0]
		frame.regs[1] = args[1]
	case 3:
		frame.regs[0] = args[0]
		frame.regs[1] = args[1]
		frame.regs[2] = args[2]
	case 4:
		frame.regs[0] = args[0]
		frame.regs[1] = args[1]
		frame.regs[2] = args[2]
		frame.regs[3] = args[3]
	default:
		copy(frame.regs[:paramCount], args[:paramCount])
	}
	if frame.closure.Proto.Vararg && len(args) > frame.closure.Proto.NumParams {
		frame.setVarargs(args[frame.closure.Proto.NumParams:])
	}
}

func (m *VM) returnFromFrame(co *Coroutine, results []rt.Value) error {
	frame := co.frames[len(co.frames)-1]
	returnReg := frame.returnReg
	returnCount := frame.returnCount
	skipTailUnwind := frame.skipTailUnwind
	tailLoss := frame.tailLoss
	var tailSourceInfo *DebugInfo
	if m.hookReturnEnabled(co) {
		frameInfo := m.debugInfoForFrameWithOptions(frame, debugInfoOptions{includeActiveLines: true})
		callerInfo := (*DebugInfo)(nil)
		if len(co.frames) > 1 {
			callerInfo = m.debugInfoPtrForFrameWithOptions(co.frames[len(co.frames)-2], debugInfoOptions{includeActiveLines: true})
			if co.frames[len(co.frames)-2].tailPending && frameInfo.What == "Lua" {
				frameInfo.Name = ""
				callerInfo = nil
			}
		}
		if err := m.dispatchReturnHookWithInfo(co, "return", frameInfo, callerInfo); err != nil {
			return err
		}
		if len(co.frames) == 1 && frameInfo.What == "main" && !co.helper {
			if err := m.dispatchReturnHookWithInfo(co, "return", cReturnDebugInfo(), nil); err != nil {
				return err
			}
		}
		tailSourceInfo = &frameInfo
	}
	frame.closeUpvalues()
	if len(co.frames) > 1 && tailLoss > 0 {
		caller := co.frames[len(co.frames)-2]
		caller.tailCalls -= tailLoss
		if caller.tailCalls < 0 {
			caller.tailCalls = 0
		}
	}
	co.frames = co.frames[:len(co.frames)-1]
	co.stackTop = frame.base
	if len(co.frames) == 0 {
		co.setResults(results)
		co.status = CoroutineDead
		m.releaseFrame(frame)
		return nil
	}
	m.releaseFrame(frame)
	if skipTailUnwind {
		caller := co.frames[len(co.frames)-1]
		if returnCount == 0 {
			caller.setPendingResults(results)
		} else if returnReg >= 0 && returnReg < len(caller.regs) {
			caller.storeResults(returnReg, returnCount, results)
		}
		return nil
	}
	return m.unwindTailFrames(co, results, returnReg, returnCount, tailSourceInfo)
}

func (m *VM) makeClosure(frame *callFrame, proto *bytecode.Proto) rt.Value {
	if len(proto.Upvalues) == 0 {
		state := m.stateFor(proto)
		if frame.closure.Env == m.globalEnv() {
			if singleton, ok := state.singletons[proto]; ok {
				return singleton
			}
		}
		closure := newLuaClosure(proto)
		closure.Env = frame.closure.Env
		value := rt.HandleValue(m.runtime.Heap().NewLuaClosure(closure))
		if frame.closure.Env == m.globalEnv() {
			state.singletons[proto] = value
		}
		return value
	}
	closure := newLuaClosure(proto)
	closure.Env = frame.closure.Env
	for i, desc := range proto.Upvalues {
		if desc.InParentLocal {
			closure.Upvalues[i] = frame.captureUpvalue(int(desc.Index))
			continue
		}
		closure.Upvalues[i] = frame.closure.Upvalues[desc.Index]
	}
	return rt.HandleValue(m.runtime.Heap().NewLuaClosure(closure))
}

func (f *callFrame) captureUpvalue(slot int) *upvalue {
	if uv := f.openUpvalues[slot]; uv != nil {
		return uv
	}
	uv := &upvalue{cell: upvalueCell{value: f.regs[slot]}, stack: f.regs, index: slot, isOpen: true}
	f.openUpvalues[slot] = uv
	f.openCount++
	return uv
}

func (f *callFrame) closeUpvalues() {
	f.closeUpvaluesFrom(0)
}

func (f *callFrame) closeUpvaluesFrom(start int) {
	if f.openCount == 0 {
		return
	}
	if start < 0 {
		start = 0
	}
	if start >= len(f.openUpvalues) {
		return
	}
	for i := start; i < len(f.openUpvalues); i++ {
		uv := f.openUpvalues[i]
		if uv != nil && uv.isOpen {
			uv.cell.value = uv.stack[uv.index]
			uv.stack = nil
			uv.isOpen = false
			f.openCount--
		}
		f.openUpvalues[i] = nil
	}
	if f.openCount < 0 {
		f.openCount = 0
	}
}

func (m *VM) isCallable(value rt.Value) bool {
	h, ok := value.Handle()
	if !ok {
		return false
	}
	return h.Kind() == rt.ObjectHostFunction || h.Kind() == rt.ObjectLuaClosure
}

func (m *VM) acquireFrame(co *Coroutine, closure *LuaClosure, returnReg int, returnCount int) *callFrame {
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
	frame.compiledUnit = nil
	frame.base = co.stackTop
	frame.stackSize = closure.Proto.MaxStack
	frame.regs = m.reserveRegisterWindow(co, frame.base, frame.stackSize)
	frame.pc = 0
	frame.tailCalls = 0
	frame.tailLoss = 0
	frame.lastHookLine = -1
	frame.lastHookPC = -1
	frame.returnReg = returnReg
	frame.returnCount = returnCount
	frame.openCount = 0
	frame.nativeFrame.Reset()
	frame.clearVarargs()
	frame.clearPendingResults()
	frame.gcRoots = frame.gcRoots[:0]
	frame.gcOverwriteReg = -1
	frame.gcOverwriteCnt = 0
	frame.gcOverwritePend = false
	frame.gcFrameDead = false
	frame.tailPending = false
	frame.skipTailUnwind = false
	frame.openUpvalues = resizeOpenUpvalues(frame.openUpvalues, closure.Proto.MaxStack)
	co.stackTop += frame.stackSize
	return frame
}

func (m *VM) releaseFrame(frame *callFrame) {
	state := frame.state
	if state == nil && frame.closure != nil {
		state = m.stateFor(frame.closure.Proto)
	}
	frame.regs = nil
	frame.base = 0
	frame.stackSize = 0
	frame.state = nil
	frame.closure = nil
	frame.compiledUnit = nil
	frame.pc = 0
	frame.tailCalls = 0
	frame.tailLoss = 0
	frame.lastHookLine = -1
	frame.lastHookPC = -1
	frame.returnReg = 0
	frame.returnCount = 0
	frame.openCount = 0
	frame.nativeFrame.Reset()
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
	if state != nil {
		state.framePool = append(state.framePool, frame)
	}
}

func (m *VM) globalEnv() rt.Value {
	return rt.HandleValue(m.runtime.GlobalsHandle())
}

func (m *VM) envOf(closure *LuaClosure) rt.Value {
	if closure != nil && closure.Env.Kind() != rt.KindNil {
		return closure.Env
	}
	return m.globalEnv()
}

func (m *VM) loadGlobal(closure *LuaClosure, sym uint32) (rt.Value, error) {
	env := m.envOf(closure)
	value, _, found, err := m.runtime.GetField(env, sym)
	if err != nil {
		return rt.NilValue, err
	}
	if found {
		return value, nil
	}
	value, handled, err := m.resolveFieldIndex(env, sym)
	if err != nil {
		return rt.NilValue, err
	}
	if !handled {
		return rt.NilValue, nil
	}
	return value, nil
}

func (m *VM) storeGlobal(closure *LuaClosure, sym uint32, value rt.Value) error {
	return m.setFieldValue(m.envOf(closure), sym, value)
}

func (co *Coroutine) setResults(results []rt.Value) {
	co.lastResults = storeOwnedValues(co.lastResults, co.lastInline[:], results)
	if len(results) == 0 {
		co.lastResult = rt.NilValue
		return
	}
	co.lastResult = results[0]
}

func (co *Coroutine) setYieldedResults(results []rt.Value) {
	co.yieldedResult = storeOwnedValues(co.yieldedResult, co.yieldedInline[:], results)
	if len(results) == 0 {
		co.yielded = rt.NilValue
		return
	}
	co.yielded = results[0]
}

func storeOwnedValues(dst []rt.Value, inline []rt.Value, src []rt.Value) []rt.Value {
	if len(src) <= len(inline) {
		copy(inline[:len(src)], src)
		return inline[:len(src)]
	}
	if cap(dst) < len(src) {
		dst = make([]rt.Value, len(src))
	} else {
		dst = dst[:len(src)]
	}
	copy(dst, src)
	return dst
}

func (f *callFrame) storeResults(start int, count int, results []rt.Value) {
	if count == 0 {
		f.setPendingResults(results)
		return
	}
	f.clearPendingResults()
	for i := 0; i < count; i++ {
		value := rt.NilValue
		if i < len(results) {
			value = results[i]
		}
		if start+i >= 0 && start+i < len(f.regs) {
			f.regs[start+i] = value
		}
	}
}

func (f *callFrame) callArgs(start int, count int, appendPending bool) []rt.Value {
	if !appendPending {
		return f.regs[start : start+count]
	}
	pendingCount := f.pendingResultCount()
	if pendingCount == 0 {
		return f.regs[start : start+count]
	}
	if count == 0 {
		return f.pendingValues()
	}
	args := f.borrowArgScratch(count + pendingCount)
	args = append(args, f.regs[start:start+count]...)
	args = f.appendPendingResults(args)
	return args
}

func (f *callFrame) pendingResultCount() int {
	if len(f.pendingResults) != 0 || f.nativeFrame.Pending.Count == 0 {
		return len(f.pendingResults)
	}
	return int(f.nativeFrame.Pending.Count)
}

func (f *callFrame) appendPendingResults(dst []rt.Value) []rt.Value {
	if len(f.pendingResults) != 0 {
		return append(dst, f.pendingResults...)
	}
	return appendNativeMultiResultBuffer(dst, &f.nativeFrame.Pending)
}

func (f *callFrame) pendingValues() []rt.Value {
	count := f.pendingResultCount()
	if count == 0 {
		return nil
	}
	values := f.borrowResultScratch(count)
	return f.appendPendingResults(values)
}

func (f *callFrame) firstPendingResult() (rt.Value, bool) {
	if len(f.pendingResults) != 0 {
		return f.pendingResults[0], true
	}
	if f.nativeFrame.Pending.Count == 0 {
		return rt.NilValue, false
	}
	return f.nativeFrame.Pending.Inline[0], true
}

func (f *callFrame) clearPendingResults() {
	f.pendingResults = f.pendingResults[:0]
	f.nativeFrame.Pending.Reset()
}

func (f *callFrame) setPendingResults(results []rt.Value) {
	f.pendingResults = storeOwnedValues(f.pendingResults, f.pendingInline[:], results)
	syncNativeMultiResultBuffer(&f.nativeFrame.Pending, f.pendingResults)
}

func (f *callFrame) setVarargs(values []rt.Value) {
	f.varargs = storeOwnedValues(f.varargs, f.varargsInline[:], values)
	syncNativeMultiResultBuffer(&f.nativeFrame.Varargs, f.varargs)
	f.nativeFrame.VarargCount = uint32(len(f.varargs))
}

func (f *callFrame) varargCount() int {
	if len(f.varargs) != 0 || f.nativeFrame.Varargs.Count == 0 {
		return len(f.varargs)
	}
	return int(f.nativeFrame.Varargs.Count)
}

func (f *callFrame) appendVarargs(dst []rt.Value) []rt.Value {
	if len(f.varargs) != 0 {
		return append(dst, f.varargs...)
	}
	return appendNativeMultiResultBuffer(dst, &f.nativeFrame.Varargs)
}

func (f *callFrame) varargValues() []rt.Value {
	count := f.varargCount()
	if count == 0 {
		return nil
	}
	values := f.borrowArgScratch(count)
	return f.appendVarargs(values)
}

func (f *callFrame) clearVarargs() {
	f.varargs = f.varargs[:0]
	f.nativeFrame.VarargCount = 0
	f.nativeFrame.Varargs.Reset()
}

func (f *callFrame) syncNativeFrameBuffers() {
	syncNativeMultiResultBuffer(&f.nativeFrame.Pending, f.pendingResults)
	syncNativeMultiResultBuffer(&f.nativeFrame.Varargs, f.varargs)
	f.nativeFrame.VarargCount = uint32(len(f.varargs))
}

func (f *callFrame) borrowArgScratch(size int) []rt.Value {
	if size <= len(f.argInline) {
		return f.argInline[:0]
	}
	if cap(f.argScratch) < size {
		f.argScratch = make([]rt.Value, 0, size)
	}
	return f.argScratch[:0]
}

func (f *callFrame) borrowResultScratch(size int) []rt.Value {
	if size <= len(f.resultInline) {
		return f.resultInline[:0]
	}
	if cap(f.resultScratch) < size {
		f.resultScratch = make([]rt.Value, 0, size)
	}
	return f.resultScratch[:0]
}

func (f *callFrame) copyArgs(args []rt.Value) []rt.Value {
	if len(args) == 0 {
		return nil
	}
	bound := f.borrowResultScratch(len(args))
	bound = append(bound, args...)
	return bound
}

func (f *callFrame) returnResults(start int, count int) []rt.Value {
	pendingCount := f.pendingResultCount()
	if pendingCount == 0 {
		return f.regs[start : start+count]
	}
	if count == 0 {
		return f.pendingValues()
	}
	results := f.borrowResultScratch(count + pendingCount)
	results = append(results, f.regs[start:start+count]...)
	results = f.appendPendingResults(results)
	return results
}

func (f *callFrame) singleResult(value rt.Value) []rt.Value {
	results := f.borrowResultScratch(1)
	results = append(results, value)
	return results
}

func (f *callFrame) clearRange(start int, count int) {
	for i := 0; i < count; i++ {
		f.regs[start+i] = rt.NilValue
	}
}

func (m *VM) reserveRegisterWindow(co *Coroutine, base int, size int) []rt.Value {
	need := base + size
	if cap(co.stack) < need {
		newCap := cap(co.stack) * 2
		if newCap < minCoroutineStackCap {
			newCap = minCoroutineStackCap
		}
		if newCap < need {
			newCap = need
		}
		newStack := make([]rt.Value, len(co.stack), newCap)
		copy(newStack, co.stack)
		co.stack = newStack
		m.rebindCoroutineFrames(co)
	}
	if len(co.stack) < need {
		oldLen := len(co.stack)
		co.stack = co.stack[:need]
		clear(co.stack[oldLen:need])
	}
	window := co.stack[base:need]
	clear(window)
	return window
}

func (m *VM) rebindCoroutineFrames(co *Coroutine) {
	for _, frame := range co.frames {
		if frame.stackSize == 0 {
			continue
		}
		frame.regs = co.stack[frame.base : frame.base+frame.stackSize]
		for _, uv := range frame.openUpvalues {
			if uv != nil && uv.isOpen {
				uv.stack = frame.regs
			}
		}
	}
}

func (m *VM) resolveFieldIndex(target rt.Value, symbol uint32) (rt.Value, bool, error) {
	meta, ok := m.runtime.GetMetafield(target, "__index")
	if !ok {
		return rt.NilValue, false, nil
	}
	key := m.runtime.StringValue(m.runtime.SymbolName(symbol))
	return m.resolveIndexMetamethod(meta, target, key)
}

func (m *VM) getTableValue(target rt.Value, key rt.Value) (rt.Value, error) {
	if value, handled := m.fastTableArrayGet(target, key); handled {
		return value, nil
	}
	value, found, err := m.runtime.GetTable(target, key)
	if err != nil {
		meta, ok := m.runtime.GetMetafield(target, "__index")
		if !ok {
			return rt.NilValue, err
		}
		value, handled, err := m.resolveIndexMetamethod(meta, target, key)
		if err != nil {
			return rt.NilValue, err
		}
		if !handled {
			return rt.NilValue, err
		}
		return value, nil
	}
	if found {
		return value, nil
	}
	meta, ok := m.runtime.GetMetafield(target, "__index")
	if !ok {
		return rt.NilValue, nil
	}
	value, handled, err := m.resolveIndexMetamethod(meta, target, key)
	if err != nil {
		return rt.NilValue, err
	}
	if !handled {
		return rt.NilValue, nil
	}
	return value, nil
}

func (m *VM) setFieldValue(target rt.Value, symbol uint32, value rt.Value) error {
	_, _, found, err := m.runtime.GetField(target, symbol)
	if err != nil {
		meta, ok := m.runtime.GetMetafield(target, "__newindex")
		if !ok {
			return err
		}
		key := m.runtime.StringValue(m.runtime.SymbolName(symbol))
		return m.resolveNewIndexMetamethod(meta, target, key, value)
	}
	if found {
		return m.runtime.SetField(target, symbol, value)
	}
	meta, ok := m.runtime.GetMetafield(target, "__newindex")
	if !ok {
		return m.runtime.SetField(target, symbol, value)
	}
	key := m.runtime.StringValue(m.runtime.SymbolName(symbol))
	return m.resolveNewIndexMetamethod(meta, target, key, value)
}

func (m *VM) setTableValue(target rt.Value, key rt.Value, value rt.Value) error {
	if handled, err := m.fastTableArraySet(target, key, value); handled {
		return err
	}
	_, found, err := m.runtime.GetTable(target, key)
	if err != nil {
		meta, ok := m.runtime.GetMetafield(target, "__newindex")
		if !ok {
			return err
		}
		return m.resolveNewIndexMetamethod(meta, target, key, value)
	}
	if found {
		return m.runtime.SetTable(target, key, value)
	}
	meta, ok := m.runtime.GetMetafield(target, "__newindex")
	if !ok {
		return m.runtime.SetTable(target, key, value)
	}
	return m.resolveNewIndexMetamethod(meta, target, key, value)
}

func positiveIntegerIndex(value rt.Value) (int, bool) {
	if !value.IsNumber() {
		return 0, false
	}
	number := value.Number()
	if number <= 0 || math.Trunc(number) != number {
		return 0, false
	}
	return int(number), true
}

func (m *VM) fastPlainTable(target rt.Value) (*rt.Table, bool) {
	h, ok := target.Handle()
	if !ok || h.Kind() != rt.ObjectTable {
		return nil, false
	}
	table := m.runtime.Heap().Table(h)
	if table.Metatable().Kind() != rt.KindNil {
		return nil, false
	}
	return table, true
}

func (m *VM) fastTableArrayTarget(target rt.Value, key rt.Value) (*rt.Table, int, bool) {
	h, ok := target.Handle()
	if !ok || h.Kind() != rt.ObjectTable {
		return nil, 0, false
	}
	index, ok := positiveIntegerIndex(key)
	if !ok {
		return nil, 0, false
	}
	return m.runtime.Heap().Table(h), index, true
}

func (m *VM) fastPlainArrayTarget(target rt.Value, key rt.Value) (*rt.Table, int, bool) {
	table, index, ok := m.fastTableArrayTarget(target, key)
	if !ok || table.Metatable().Kind() != rt.KindNil {
		return nil, 0, false
	}
	return table, index, true
}

func (m *VM) fastTableArrayGet(target rt.Value, key rt.Value) (rt.Value, bool) {
	table, index, ok := m.fastTableArrayTarget(target, key)
	if !ok {
		return rt.NilValue, false
	}
	if value, found := table.GetIndex(index); found {
		return value, true
	}
	if table.Metatable().Kind() == rt.KindNil {
		return rt.NilValue, true
	}
	return rt.NilValue, false
}

func (m *VM) fastTableArraySet(target rt.Value, key rt.Value, value rt.Value) (bool, error) {
	table, index, ok := m.fastTableArrayTarget(target, key)
	if !ok {
		return false, nil
	}
	if _, found := table.GetIndex(index); found || table.Metatable().Kind() == rt.KindNil {
		table.SetIndex(index, value)
		return true, nil
	}
	return false, nil
}

func (m *VM) resolveIndexMetamethod(meta rt.Value, target rt.Value, key rt.Value) (rt.Value, bool, error) {
	if h, ok := meta.Handle(); ok && h.Kind() == rt.ObjectTable {
		value, err := m.getTableValue(meta, key)
		return value, true, err
	}
	if !m.isCallable(meta) {
		return rt.NilValue, false, fmt.Errorf("__index must be table or function")
	}
	args := [2]rt.Value{target, key}
	value, err := m.callValue(meta, args[:])
	return value, true, err
}

func (m *VM) resolveNewIndexMetamethod(meta rt.Value, target rt.Value, key rt.Value, value rt.Value) error {
	if h, ok := meta.Handle(); ok && h.Kind() == rt.ObjectTable {
		return m.setTableValue(meta, key, value)
	}
	if !m.isCallable(meta) {
		return fmt.Errorf("__newindex must be table or function")
	}
	args := [3]rt.Value{target, key, value}
	_, err := m.callValue(meta, args[:])
	return err
}

func (m *VM) addValues(left rt.Value, right rt.Value) (rt.Value, error) {
	if left.IsNumber() && right.IsNumber() {
		return rt.NumberValue(left.Number() + right.Number()), nil
	}
	return m.callBinaryMetamethod("__add", left, right)
}

func (m *VM) subValues(left rt.Value, right rt.Value) (rt.Value, error) {
	if left.IsNumber() && right.IsNumber() {
		return rt.NumberValue(left.Number() - right.Number()), nil
	}
	return m.callBinaryMetamethod("__sub", left, right)
}

func (m *VM) mulValues(left rt.Value, right rt.Value) (rt.Value, error) {
	if left.IsNumber() && right.IsNumber() {
		return rt.NumberValue(left.Number() * right.Number()), nil
	}
	return m.callBinaryMetamethod("__mul", left, right)
}

func (m *VM) divValues(left rt.Value, right rt.Value) (rt.Value, error) {
	if left.IsNumber() && right.IsNumber() {
		return rt.NumberValue(left.Number() / right.Number()), nil
	}
	return m.callBinaryMetamethod("__div", left, right)
}

func (m *VM) modValues(left rt.Value, right rt.Value) (rt.Value, error) {
	if left.IsNumber() && right.IsNumber() {
		lhs := left.Number()
		rhs := right.Number()
		return rt.NumberValue(lhs - math.Floor(lhs/rhs)*rhs), nil
	}
	return m.callBinaryMetamethod("__mod", left, right)
}

func (m *VM) powValues(left rt.Value, right rt.Value) (rt.Value, error) {
	if left.IsNumber() && right.IsNumber() {
		return rt.NumberValue(math.Pow(left.Number(), right.Number())), nil
	}
	return m.callBinaryMetamethod("__pow", left, right)
}

func (m *VM) lenValue(value rt.Value) (rt.Value, error) {
	if s, ok := m.runtime.ToString(value); ok {
		return rt.NumberValue(float64(len(s))), nil
	}
	if h, ok := value.Handle(); ok && h.Kind() == rt.ObjectTable {
		return rt.NumberValue(float64(m.runtime.Heap().Table(h).Length())), nil
	}
	return m.callUnaryMetamethod("__len", value)
}

func (m *VM) concatValues(left rt.Value, right rt.Value) (rt.Value, error) {
	ls, lok := m.coerceConcatString(left)
	rs, rok := m.coerceConcatString(right)
	if lok && rok {
		return m.runtime.StringValue(ls + rs), nil
	}
	return m.callBinaryMetamethod("__concat", left, right)
}

func isTruthy(value rt.Value) bool {
	if value.Kind() == rt.KindNil {
		return false
	}
	if value.Kind() == rt.KindBool {
		return value.Bool()
	}
	return true
}

func (m *VM) equalValues(left rt.Value, right rt.Value) (bool, error) {
	if left.Kind() == right.Kind() {
		switch left.Kind() {
		case rt.KindNil:
			return true, nil
		case rt.KindBool:
			return left.Bool() == right.Bool(), nil
		case rt.KindNumber:
			return left.Number() == right.Number(), nil
		case rt.KindHandle:
			lh, _ := left.Handle()
			rh, _ := right.Handle()
			if lh.Kind() == rh.Kind() && lh.Kind() == rt.ObjectString {
				ls, _ := m.runtime.ToString(left)
				rs, _ := m.runtime.ToString(right)
				return ls == rs, nil
			}
			if lh == rh {
				return true, nil
			}
		}
	}
	value, handled, err := m.callEqualMetamethod(left, right)
	if err != nil {
		return false, err
	}
	if handled {
		return value, nil
	}
	return false, nil
}

func (m *VM) lessValues(left rt.Value, right rt.Value) (bool, error) {
	if left.IsNumber() && right.IsNumber() {
		return left.Number() < right.Number(), nil
	}
	ls, lok := m.runtime.ToString(left)
	rs, rok := m.runtime.ToString(right)
	if lok && rok {
		return ls < rs, nil
	}
	value, handled, err := m.callBinaryMetamethodBool("__lt", left, right)
	if err != nil {
		return false, err
	}
	if handled {
		return value, nil
	}
	return false, fmt.Errorf("attempt to compare %s and %s", left, right)
}

func (m *VM) lessEqualValues(left rt.Value, right rt.Value) (bool, error) {
	if left.IsNumber() && right.IsNumber() {
		return left.Number() <= right.Number(), nil
	}
	ls, lok := m.runtime.ToString(left)
	rs, rok := m.runtime.ToString(right)
	if lok && rok {
		return ls <= rs, nil
	}
	value, handled, err := m.callBinaryMetamethodBool("__le", left, right)
	if err != nil {
		return false, err
	}
	if handled {
		return value, nil
	}
	value, handled, err = m.callLessEqualFallback(left, right)
	if err != nil {
		return false, err
	}
	if handled {
		return value, nil
	}
	return false, fmt.Errorf("attempt to compare %s and %s", left, right)
}

func (m *VM) callEqualMetamethod(left rt.Value, right rt.Value) (bool, bool, error) {
	leftMeta, leftOK := m.runtime.GetMetafield(left, "__eq")
	rightMeta, rightOK := m.runtime.GetMetafield(right, "__eq")
	if !leftOK || !rightOK || leftMeta != rightMeta {
		return false, false, nil
	}
	args := [2]rt.Value{left, right}
	value, err := m.callValue(leftMeta, args[:])
	if err != nil {
		return false, true, err
	}
	return isTruthy(value), true, nil
}

func (m *VM) callLessEqualFallback(left rt.Value, right rt.Value) (bool, bool, error) {
	meta, handled := m.lookupBinaryMetamethod("__lt", left, right)
	if !handled {
		return false, false, nil
	}
	args := [2]rt.Value{right, left}
	value, err := m.callValue(meta, args[:])
	if err != nil {
		return false, true, err
	}
	return !isTruthy(value), true, nil
}

func (m *VM) callBinaryMetamethod(name string, left rt.Value, right rt.Value) (rt.Value, error) {
	meta, handled := m.lookupBinaryMetamethod(name, left, right)
	if !handled {
		return rt.NilValue, fmt.Errorf("attempt to apply %s on %s and %s", name, left, right)
	}
	args := [2]rt.Value{left, right}
	return m.callValue(meta, args[:])
}

func (m *VM) callBinaryMetamethodBool(name string, left rt.Value, right rt.Value) (bool, bool, error) {
	meta, handled := m.lookupBinaryMetamethod(name, left, right)
	if !handled {
		return false, false, nil
	}
	args := [2]rt.Value{left, right}
	value, err := m.callValue(meta, args[:])
	if err != nil {
		return false, true, err
	}
	return isTruthy(value), true, nil
}

func (m *VM) callUnaryMetamethod(name string, value rt.Value) (rt.Value, error) {
	meta, ok := m.runtime.GetMetafield(value, name)
	if !ok {
		return rt.NilValue, fmt.Errorf("attempt to apply %s on %s", name, value)
	}
	args := [1]rt.Value{value}
	return m.callValue(meta, args[:])
}

func (m *VM) lookupBinaryMetamethod(name string, left rt.Value, right rt.Value) (rt.Value, bool) {
	if meta, ok := m.runtime.GetMetafield(left, name); ok {
		return meta, true
	}
	return m.runtime.GetMetafield(right, name)
}

func (m *VM) callValue(callee rt.Value, args []rt.Value) (rt.Value, error) {
	resolvedCallee, resolvedArgs, err := m.resolveCallTarget(callee, args)
	if err != nil {
		return rt.NilValue, err
	}
	h, ok := resolvedCallee.Handle()
	if ok {
		switch h.Kind() {
		case rt.ObjectHostFunction:
			co := m.currentCoroutine()
			var (
				info       DebugInfo
				callerInfo *DebugInfo
				haveInfo   bool
			)
			if m.hookCallEnabled(co) {
				info, err = m.DebugInfoForFunctionWithOptions(resolvedCallee, true)
				if err != nil {
					return rt.NilValue, err
				}
				callerInfo = m.debugInfoPtrForFrameWithOptions(m.currentFrame(), debugInfoOptions{includeActiveLines: true})
				haveInfo = true
				if err := m.dispatchCallHookWithInfo(co, info, callerInfo); err != nil {
					return rt.NilValue, err
				}
			}
			result, err := m.runtime.CallValue(resolvedCallee, resolvedArgs)
			if err != nil {
				return rt.NilValue, err
			}
			if m.hookReturnEnabled(co) {
				if !haveInfo {
					info, err = m.DebugInfoForFunctionWithOptions(resolvedCallee, true)
					if err != nil {
						return rt.NilValue, err
					}
					callerInfo = m.debugInfoPtrForFrameWithOptions(m.currentFrame(), debugInfoOptions{includeActiveLines: true})
				}
				if err := m.dispatchReturnHookWithInfo(co, "return", info, callerInfo); err != nil {
					return rt.NilValue, err
				}
			}
			return result, nil
		case rt.ObjectLuaClosure:
			closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
			parentCo := m.currentCoroutine()
			if parentCo != nil && len(parentCo.frames) > 0 && (!parentCo.frames[len(parentCo.frames)-1].tailPending || parentCo.hook.running) {
				co := parentCo
				targetDepth := len(co.frames)
				m.pushFrame(co, closure, resolvedArgs, -1, 0)
				if co.hook.running {
					co.frames[len(co.frames)-1].skipTailUnwind = true
				}
				if err := m.dispatchCallHookForFrames(co, co.frames[len(co.frames)-1], co.frames[targetDepth-1]); err != nil {
					return rt.NilValue, err
				}
				if _, err := m.executeCoroutineUntil(co, targetDepth); err != nil {
					return rt.NilValue, err
				}
				if len(co.frames) < targetDepth {
					if len(co.lastResults) == 0 {
						return rt.NilValue, nil
					}
					return co.lastResults[0], nil
				}
				caller := co.frames[targetDepth-1]
				result := rt.NilValue
				if pending, ok := caller.firstPendingResult(); ok {
					result = pending
				}
				caller.clearPendingResults()
				return result, nil
			}
			co := newCoroutine(m, resolvedCallee)
			co.helper = true
			if parentCo != nil && parentCo.hook.function.Kind() != rt.KindNil {
				co.hook = parentCo.hook
				co.hook.running = false
				co.hook.context = nil
			}
			m.pushFrame(&co, closure, resolvedArgs, -1, 0)
			restoreCo := m.pushActiveCoroutine(&co)
			if err := m.dispatchCallHookForFrames(&co, co.frames[len(co.frames)-1], nil); err != nil {
				restoreCo()
				return rt.NilValue, err
			}
			results, err := m.executeCoroutine(&co)
			restoreCo()
			if err != nil {
				return rt.NilValue, err
			}
			if parentCo != nil && co.hookTouched {
				parentCo.hook = co.hook
				parentCo.hookTouched = true
			}
			if co.status != CoroutineDead {
				return rt.NilValue, fmt.Errorf("metamethod yielded")
			}
			if len(results) == 0 {
				return rt.NilValue, nil
			}
			return results[0], nil
		}
	}
	return rt.NilValue, fmt.Errorf("attempt to call unsupported object kind %s", h.Kind())
}

func (m *VM) resolveCallTarget(callee rt.Value, args []rt.Value) (rt.Value, []rt.Value, error) {
	if h, ok := callee.Handle(); ok {
		switch h.Kind() {
		case rt.ObjectHostFunction, rt.ObjectLuaClosure:
			return callee, args, nil
		}
	}
	if meta, handled := m.runtime.GetMetafield(callee, "__call"); handled {
		callArgs := make([]rt.Value, 0, len(args)+1)
		callArgs = append(callArgs, callee)
		callArgs = append(callArgs, args...)
		return meta, callArgs, nil
	}
	return rt.NilValue, nil, fmt.Errorf("attempt to call non-callable value %s", callee)
}

func (m *VM) callValueMulti(callee rt.Value, args []rt.Value) ([]rt.Value, error) {
	resolvedCallee, resolvedArgs, err := m.resolveCallTarget(callee, args)
	if err != nil {
		return nil, err
	}
	h, ok := resolvedCallee.Handle()
	if ok {
		switch h.Kind() {
		case rt.ObjectHostFunction:
			co := m.currentCoroutine()
			var (
				info       DebugInfo
				callerInfo *DebugInfo
				haveInfo   bool
			)
			if m.hookCallEnabled(co) {
				info, err = m.DebugInfoForFunctionWithOptions(resolvedCallee, true)
				if err != nil {
					return nil, err
				}
				callerInfo = m.debugInfoPtrForFrameWithOptions(m.currentFrame(), debugInfoOptions{includeActiveLines: true})
				haveInfo = true
				if err := m.dispatchCallHookWithInfo(co, info, callerInfo); err != nil {
					return nil, err
				}
			}
			results, err := m.runtime.CallValueMulti(resolvedCallee, resolvedArgs)
			if err != nil {
				return nil, err
			}
			if m.hookReturnEnabled(co) {
				if !haveInfo {
					info, err = m.DebugInfoForFunctionWithOptions(resolvedCallee, true)
					if err != nil {
						return nil, err
					}
					callerInfo = m.debugInfoPtrForFrameWithOptions(m.currentFrame(), debugInfoOptions{includeActiveLines: true})
					haveInfo = true
				}
				if err := m.dispatchReturnHookWithInfo(co, "return", info, callerInfo); err != nil {
					return nil, err
				}
			}
			return results, nil
		case rt.ObjectLuaClosure:
			closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
			parentCo := m.currentCoroutine()
			if parentCo != nil && len(parentCo.frames) > 0 && (!parentCo.frames[len(parentCo.frames)-1].tailPending || parentCo.hook.running) {
				co := parentCo
				targetDepth := len(co.frames)
				m.pushFrame(co, closure, resolvedArgs, -1, 0)
				if co.hook.running {
					co.frames[len(co.frames)-1].skipTailUnwind = true
				}
				if err := m.dispatchCallHookForFrames(co, co.frames[len(co.frames)-1], co.frames[targetDepth-1]); err != nil {
					return nil, err
				}
				if _, err := m.executeCoroutineUntil(co, targetDepth); err != nil {
					return nil, err
				}
				if len(co.frames) < targetDepth {
					return append([]rt.Value(nil), co.lastResults...), nil
				}
				caller := co.frames[targetDepth-1]
				results := append([]rt.Value(nil), caller.pendingValues()...)
				caller.clearPendingResults()
				return results, nil
			}
			co := newCoroutine(m, resolvedCallee)
			co.helper = true
			if parentCo != nil && parentCo.hook.function.Kind() != rt.KindNil {
				co.hook = parentCo.hook
				co.hook.running = false
				co.hook.context = nil
			}
			m.pushFrame(&co, closure, resolvedArgs, -1, 0)
			restoreCo := m.pushActiveCoroutine(&co)
			if err := m.dispatchCallHookForFrames(&co, co.frames[len(co.frames)-1], nil); err != nil {
				restoreCo()
				return nil, err
			}
			result, err := m.executeCoroutine(&co)
			restoreCo()
			if err != nil {
				return nil, err
			}
			if parentCo != nil && co.hookTouched {
				parentCo.hook = co.hook
				parentCo.hookTouched = true
			}
			if co.status != CoroutineDead {
				return nil, fmt.Errorf("metamethod yielded")
			}
			return result, nil
		}
	}
	return nil, fmt.Errorf("attempt to call unsupported object kind %s", h.Kind())
}

func (m *VM) unwindTailFrames(co *Coroutine, results []rt.Value, returnReg int, returnCount int, tailSourceInfo *DebugInfo) error {
	for len(co.frames) > 0 {
		frame := co.frames[len(co.frames)-1]
		if !frame.tailPending {
			break
		}
		returnReg = frame.returnReg
		returnCount = frame.returnCount
		event := frame.tailHookEvent
		if event == "" {
			event = "tail return"
		}
		if m.hookReturnEnabled(co) {
			callerInfo := (*DebugInfo)(nil)
			if len(co.frames) > 1 {
				callerInfo = m.debugInfoPtrForFrameWithOptions(co.frames[len(co.frames)-2], debugInfoOptions{includeActiveLines: true})
			}
			eventInfo := m.debugInfoForFrameWithOptions(frame, debugInfoOptions{includeActiveLines: true})
			if event == "tail return" && tailSourceInfo != nil {
				eventInfo = *tailSourceInfo
				eventInfo.Name = frame.closure.Proto.Name
			}
			if err := m.dispatchReturnHookWithInfo(co, event, eventInfo, callerInfo); err != nil {
				return err
			}
		}
		frame.closeUpvalues()
		co.frames = co.frames[:len(co.frames)-1]
		co.stackTop = frame.base
		m.releaseFrame(frame)
	}
	if len(co.frames) == 0 {
		co.setResults(results)
		co.status = CoroutineDead
		return nil
	}
	caller := co.frames[len(co.frames)-1]
	if returnCount == 0 {
		caller.setPendingResults(results)
	} else if returnReg >= 0 && returnReg < len(caller.regs) {
		caller.storeResults(returnReg, returnCount, results)
	}
	return nil
}

func (m *VM) coerceConcatString(value rt.Value) (string, bool) {
	if s, ok := m.runtime.ToString(value); ok {
		return s, true
	}
	if value.IsNumber() {
		return rt.FormatNumber(value.Number()), true
	}
	return "", false
}

func (f *callFrame) beginGCCall(callee rt.Value, args []rt.Value, overwriteReg int, overwriteCount int, overwritePending bool, frameDead bool) {
	f.gcRoots = append(f.gcRoots[:0], callee)
	f.gcRoots = append(f.gcRoots, args...)
	f.gcOverwriteReg = overwriteReg
	f.gcOverwriteCnt = overwriteCount
	f.gcOverwritePend = overwritePending
	f.gcFrameDead = frameDead
}

func (f *callFrame) endGCCall() {
	f.gcRoots = f.gcRoots[:0]
	f.gcOverwriteReg = -1
	f.gcOverwriteCnt = 0
	f.gcOverwritePend = false
	f.gcFrameDead = false
}

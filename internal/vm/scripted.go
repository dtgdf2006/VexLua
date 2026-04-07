package vm

import (
	"fmt"
	"math"

	"vexlua/internal/bytecode"
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
	CoroutineNew       CoroutineStatus = "suspended"
	CoroutineRunning   CoroutineStatus = "running"
	CoroutineSuspended CoroutineStatus = "suspended"
	CoroutineDead      CoroutineStatus = "dead"
)

type Coroutine struct {
	entry         rt.Value
	proxy         rt.Value
	visible       bool
	frames        []*callFrame
	status        CoroutineStatus
	started       bool
	resumeReg     int
	resumeCount   int
	lastResult    rt.Value
	lastResults   []rt.Value
	yielded       rt.Value
	yieldedResult []rt.Value
	argBuf        [1]rt.Value
	stack         []rt.Value
	stackTop      int
}

type callFrame struct {
	closure        *LuaClosure
	regs           []rt.Value
	base           int
	stackSize      int
	pc             int
	returnReg      int
	returnCount    int
	openCount      int
	varargs        []rt.Value
	pendingResults []rt.Value
	openUpvalues   []*upvalue
}

type upvalue struct {
	stack  []rt.Value
	index  int
	closed rt.Value
	isOpen bool
}

func (u *upvalue) Get() rt.Value {
	if u.isOpen {
		return u.stack[u.index]
	}
	return u.closed
}

func (u *upvalue) Set(v rt.Value) {
	if u.isOpen {
		u.stack[u.index] = v
		return
	}
	u.closed = v
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

func newCoroutine(entry rt.Value) Coroutine {
	return Coroutine{entry: entry, proxy: rt.NilValue, status: CoroutineNew, resumeReg: -1, stack: make([]rt.Value, 0, 256)}
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
	co := newCoroutine(entry)
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
	co := newCoroutine(state.rootClosure)
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
	co.status = CoroutineRunning
	for len(co.frames) > 0 {
		frame := co.frames[len(co.frames)-1]
		proto := frame.closure.Proto
		state := m.stateFor(proto)
		if frame.pc == 0 {
			state.runs++
			if err := m.maybeCompile(state, proto); err != nil {
				return nil, err
			}
			if state.compiled != nil {
				result, err := state.compiled.Run(frame.regs)
				if err != nil {
					return nil, err
				}
				if err := m.returnFromFrame(co, []rt.Value{result}); err != nil {
					return nil, err
				}
				continue
			}
		}
		if frame.pc >= len(proto.Code) {
			if err := m.returnFromFrame(co, []rt.Value{rt.NilValue}); err != nil {
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
				return nil, err
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
				return nil, err
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
			value, err := m.getTableValue(frame.regs[instr.B], frame.regs[instr.C])
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = value
		case bytecode.OpSetTable:
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
			for i, value := range frame.pendingResults {
				table.SetIndex(start+i, value)
			}
			frame.pendingResults = frame.pendingResults[:0]
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
				continue
			}
			restore := m.pushActiveFrame(frame)
			result, err := m.callValue(callee, args)
			restore()
			if err != nil {
				return nil, err
			}
			frame.regs[instr.A] = result
		case bytecode.OpCallMulti:
			callee := frame.regs[instr.B]
			argCount, resultCount, appendPending := bytecode.UnpackCallSpec(instr.D)
			args := frame.callArgs(int(instr.C), argCount, appendPending)
			if h, ok := callee.Handle(); ok && h.Kind() == rt.ObjectLuaClosure {
				closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
				m.pushFrame(co, closure, args, int(instr.A), resultCount)
				if appendPending {
					frame.pendingResults = frame.pendingResults[:0]
				}
				continue
			}
			restore := m.pushActiveFrame(frame)
			results, err := m.callValueMulti(callee, args)
			restore()
			if appendPending {
				frame.pendingResults = frame.pendingResults[:0]
			}
			if err != nil {
				return nil, err
			}
			frame.storeResults(int(instr.A), resultCount, results)
		case bytecode.OpVararg:
			count := int(instr.B)
			frame.storeResults(int(instr.A), count, frame.varargs)
		case bytecode.OpYield:
			yieldCount, resumeCount, appendPending := bytecode.UnpackCallSpec(instr.D)
			co.status = CoroutineSuspended
			co.resumeReg = int(instr.A)
			co.resumeCount = resumeCount
			co.yieldedResult = co.yieldedResult[:0]
			co.yieldedResult = append(co.yieldedResult, frame.callArgs(int(instr.B), yieldCount, appendPending)...)
			if appendPending {
				frame.pendingResults = frame.pendingResults[:0]
			}
			if len(co.yieldedResult) > 0 {
				co.yielded = co.yieldedResult[0]
			} else {
				co.yielded = rt.NilValue
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
		case bytecode.OpReturn:
			if err := m.returnFromFrame(co, []rt.Value{frame.regs[instr.A]}); err != nil {
				return nil, err
			}
		case bytecode.OpReturnMulti:
			if err := m.returnFromFrame(co, append([]rt.Value(nil), frame.regs[int(instr.A):int(instr.A)+int(instr.B)]...)); err != nil {
				return nil, err
			}
		case bytecode.OpReturnAppendPending:
			results := make([]rt.Value, 0, int(instr.B)+len(frame.pendingResults))
			results = append(results, frame.regs[int(instr.A):int(instr.A)+int(instr.B)]...)
			results = append(results, frame.pendingResults...)
			if err := m.returnFromFrame(co, results); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unknown scripted opcode %s", instr.Op)
		}
	}
	co.status = CoroutineDead
	return append([]rt.Value(nil), co.lastResults...), nil
}

func (m *VM) pushFrame(co *Coroutine, closure *LuaClosure, args []rt.Value, returnReg int, returnCount int) {
	frame := m.acquireFrame(co, closure, returnReg, returnCount)
	paramCount := closure.Proto.NumParams
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
	if closure.Proto.Vararg && len(args) > closure.Proto.NumParams {
		frame.varargs = append(frame.varargs[:0], args[closure.Proto.NumParams:]...)
	}
	co.frames = append(co.frames, frame)
}

func (m *VM) returnFromFrame(co *Coroutine, results []rt.Value) error {
	frame := co.frames[len(co.frames)-1]
	frame.closeUpvalues()
	co.frames = co.frames[:len(co.frames)-1]
	co.stackTop = frame.base
	if len(co.frames) == 0 {
		co.setResults(results)
		co.status = CoroutineDead
		m.releaseFrame(frame)
		return nil
	}
	caller := co.frames[len(co.frames)-1]
	if frame.returnCount == 0 {
		caller.pendingResults = append(caller.pendingResults[:0], results...)
	} else if frame.returnReg >= 0 && frame.returnReg < len(caller.regs) {
		caller.storeResults(frame.returnReg, frame.returnCount, results)
	}
	m.releaseFrame(frame)
	return nil
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
	uv := &upvalue{stack: f.regs, index: slot, isOpen: true}
	f.openUpvalues[slot] = uv
	f.openCount++
	return uv
}

func (f *callFrame) closeUpvalues() {
	if f.openCount == 0 {
		return
	}
	for i, uv := range f.openUpvalues {
		if uv != nil && uv.isOpen {
			uv.closed = uv.stack[uv.index]
			uv.stack = nil
			uv.isOpen = false
		}
		f.openUpvalues[i] = nil
	}
	f.openCount = 0
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
	frame.closure = closure
	frame.base = co.stackTop
	frame.stackSize = closure.Proto.MaxStack
	frame.regs = m.reserveRegisterWindow(co, frame.base, frame.stackSize)
	frame.pc = 0
	frame.returnReg = returnReg
	frame.returnCount = returnCount
	frame.openCount = 0
	frame.varargs = frame.varargs[:0]
	frame.pendingResults = frame.pendingResults[:0]
	if len(frame.openUpvalues) != closure.Proto.MaxStack {
		frame.openUpvalues = make([]*upvalue, closure.Proto.MaxStack)
	}
	co.stackTop += frame.stackSize
	return frame
}

func (m *VM) releaseFrame(frame *callFrame) {
	state := m.stateFor(frame.closure.Proto)
	frame.regs = nil
	frame.base = 0
	frame.stackSize = 0
	frame.closure = nil
	frame.pc = 0
	frame.returnReg = 0
	frame.returnCount = 0
	frame.openCount = 0
	frame.varargs = frame.varargs[:0]
	frame.pendingResults = frame.pendingResults[:0]
	state.framePool = append(state.framePool, frame)
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
	co.lastResults = append(co.lastResults[:0], results...)
	if len(results) == 0 {
		co.lastResult = rt.NilValue
		return
	}
	co.lastResult = results[0]
}

func (f *callFrame) storeResults(start int, count int, results []rt.Value) {
	if count == 0 {
		f.pendingResults = append(f.pendingResults[:0], results...)
		return
	}
	f.pendingResults = f.pendingResults[:0]
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
	args := make([]rt.Value, 0, count+len(f.pendingResults))
	args = append(args, f.regs[start:start+count]...)
	args = append(args, f.pendingResults...)
	return args
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
		if newCap < 256 {
			newCap = 256
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
	value, found, err := m.runtime.GetTable(target, key)
	if err != nil {
		return rt.NilValue, err
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
		return err
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
	_, found, err := m.runtime.GetTable(target, key)
	if err != nil {
		return err
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
	results, err := m.callValueMulti(callee, args)
	if err != nil {
		return rt.NilValue, err
	}
	if len(results) == 0 {
		return rt.NilValue, nil
	}
	return results[0], nil
}

func (m *VM) callValueMulti(callee rt.Value, args []rt.Value) ([]rt.Value, error) {
	h, ok := callee.Handle()
	if ok {
		switch h.Kind() {
		case rt.ObjectHostFunction:
			return m.runtime.CallValueMulti(callee, args)
		case rt.ObjectLuaClosure:
			closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
			co := newCoroutine(callee)
			m.pushFrame(&co, closure, args, -1, 0)
			restoreCo := m.pushActiveCoroutine(&co)
			result, err := m.executeCoroutine(&co)
			restoreCo()
			if err != nil {
				return nil, err
			}
			if co.status != CoroutineDead {
				return nil, fmt.Errorf("metamethod yielded")
			}
			return result, nil
		}
	}
	if meta, handled := m.runtime.GetMetafield(callee, "__call"); handled {
		callArgs := make([]rt.Value, 0, len(args)+1)
		callArgs = append(callArgs, callee)
		callArgs = append(callArgs, args...)
		return m.callValueMulti(meta, callArgs)
	}
	if !ok {
		return nil, fmt.Errorf("attempt to call non-callable value %s", callee)
	}
	return nil, fmt.Errorf("attempt to call unsupported object kind %s", h.Kind())
}

func (m *VM) coerceConcatString(value rt.Value) (string, bool) {
	if s, ok := m.runtime.ToString(value); ok {
		return s, true
	}
	if value.IsNumber() {
		return fmt.Sprintf("%g", value.Number()), true
	}
	return "", false
}

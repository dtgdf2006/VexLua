package vm

import (
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/jit"
	rt "vexlua/internal/runtime"
)

type Stats struct {
	Runs         uint32
	JITCompiled  bool
	QuickenedOps int
}

type protoState struct {
	runs         uint32
	quickenedOps int
	fieldCaches  []rt.FieldCache
	compiled     jit.Program
	jitFailed    bool
	activeLines  []int
	liveRegs     [][]uint64
	regs         []rt.Value
	rootClosure  rt.Value
	framePool    []*callFrame
	singletons   map[*bytecode.Proto]rt.Value
}

type VM struct {
	runtime      *rt.Runtime
	compiler     jit.Compiler
	hotThreshold uint32
	states       map[*bytecode.Proto]*protoState
	activeFrames []*callFrame
	activeCoros  []*Coroutine
}

func New(runtime *rt.Runtime, compiler jit.Compiler, hotThreshold uint32) *VM {
	if hotThreshold == 0 {
		hotThreshold = 8
	}
	return &VM{
		runtime:      runtime,
		compiler:     compiler,
		hotThreshold: hotThreshold,
		states:       make(map[*bytecode.Proto]*protoState),
	}
}

func (m *VM) Stats(proto *bytecode.Proto) Stats {
	state := m.stateFor(proto)
	return Stats{
		Runs:         state.runs,
		JITCompiled:  state.compiled != nil,
		QuickenedOps: state.quickenedOps,
	}
}

func (m *VM) Run(proto *bytecode.Proto) (rt.Value, error) {
	if proto.Scripted {
		return m.runScripted(proto)
	}
	state := m.stateFor(proto)
	state.runs++
	if len(state.regs) != proto.MaxStack {
		state.regs = make([]rt.Value, proto.MaxStack)
	} else {
		clear(state.regs)
	}
	regs := state.regs
	if err := m.maybeCompile(state, proto); err != nil {
		return rt.NilValue, err
	}
	if state.compiled != nil {
		return state.compiled.Run(regs)
	}
	return m.interpret(proto, state, regs)
}

func (m *VM) maybeCompile(state *protoState, proto *bytecode.Proto) error {
	if state.compiled != nil || state.jitFailed || m.compiler == nil || state.runs < m.hotThreshold {
		return nil
	}
	compiled, err := m.compiler.Compile(proto)
	if err == nil {
		state.compiled = compiled
		return nil
	}
	if err == jit.ErrUnsupported {
		state.jitFailed = true
		return nil
	}
	return err
}

func (m *VM) interpret(proto *bytecode.Proto, state *protoState, regs []rt.Value) (rt.Value, error) {
	for pc := 0; pc < len(proto.Code); pc++ {
		instr := &proto.Code[pc]
		switch instr.Op {
		case bytecode.OpNoop:
		case bytecode.OpLoadConst:
			regs[instr.A] = proto.Constants[instr.D]
		case bytecode.OpMove:
			regs[instr.A] = regs[instr.B]
		case bytecode.OpLoadGlobal:
			value, ok := m.runtime.GetGlobalSymbol(uint32(instr.D))
			if !ok {
				regs[instr.A] = rt.NilValue
				continue
			}
			regs[instr.A] = value
		case bytecode.OpStoreGlobal:
			m.runtime.SetGlobalSymbol(uint32(instr.D), regs[instr.A])
		case bytecode.OpGetField:
			value, slot, found, err := m.runtime.GetField(regs[instr.B], uint32(instr.D))
			if err != nil {
				return rt.NilValue, err
			}
			if !found {
				value = rt.NilValue
			}
			regs[instr.A] = value
			if int(instr.C) < len(state.fieldCaches) {
				target, ok := regs[instr.B].Handle()
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
		case bytecode.OpGetFieldIC:
			cache := &state.fieldCaches[instr.C]
			value, found, err := m.runtime.GetFieldCached(regs[instr.B], cache)
			if err != nil {
				return rt.NilValue, err
			}
			if !found {
				regs[instr.A] = rt.NilValue
				continue
			}
			regs[instr.A] = value
		case bytecode.OpSetField:
			if err := m.runtime.SetField(regs[instr.A], uint32(instr.D), regs[instr.B]); err != nil {
				return rt.NilValue, err
			}
		case bytecode.OpAdd:
			lhs := regs[instr.B]
			rhs := regs[instr.C]
			if !lhs.IsNumber() || !rhs.IsNumber() {
				return rt.NilValue, fmt.Errorf("ADD expects numbers, got %s and %s", lhs, rhs)
			}
			regs[instr.A] = rt.NumberValue(lhs.Number() + rhs.Number())
			instr.Op = bytecode.OpAddNum
			state.quickenedOps++
		case bytecode.OpAddNum:
			regs[instr.A] = rt.NumberValue(regs[instr.B].Number() + regs[instr.C].Number())
		case bytecode.OpAddConst:
			lhs := regs[instr.B]
			if !lhs.IsNumber() {
				return rt.NilValue, fmt.Errorf("ADD_CONST expects numeric lhs, got %s", lhs)
			}
			constant := proto.Constants[instr.D]
			regs[instr.A] = rt.NumberValue(lhs.Number() + constant.Number())
		case bytecode.OpUnm:
			value := regs[instr.B]
			if !value.IsNumber() {
				return rt.NilValue, fmt.Errorf("UNM expects numeric operand, got %s", value)
			}
			regs[instr.A] = rt.NumberValue(-value.Number())
		case bytecode.OpCall:
			argCount := int(instr.D)
			args := make([]rt.Value, argCount)
			copy(args, regs[instr.C:uint16(int(instr.C)+argCount)])
			result, err := m.runtime.CallValue(regs[instr.B], args)
			if err != nil {
				return rt.NilValue, err
			}
			regs[instr.A] = result
		case bytecode.OpJump:
			pc = int(instr.D) - 1
		case bytecode.OpJumpIfFalse:
			if !isTruthy(regs[instr.A]) {
				pc = int(instr.D) - 1
			}
		case bytecode.OpJumpIfTrue:
			if isTruthy(regs[instr.A]) {
				pc = int(instr.D) - 1
			}
		case bytecode.OpLessEqualJump:
			lhs := regs[instr.A]
			rhs := regs[instr.B]
			if !lhs.IsNumber() || !rhs.IsNumber() {
				return rt.NilValue, fmt.Errorf("LE_JUMP expects numbers, got %s and %s", lhs, rhs)
			}
			if lhs.Number() <= rhs.Number() {
				pc = int(instr.D) - 1
			}
		case bytecode.OpClose:
		case bytecode.OpReturn:
			return regs[instr.A], nil
		default:
			return rt.NilValue, fmt.Errorf("unknown opcode %s", instr.Op)
		}
	}
	return rt.NilValue, nil
}

func (m *VM) stateFor(proto *bytecode.Proto) *protoState {
	state, ok := m.states[proto]
	if ok {
		return state
	}
	state = &protoState{
		fieldCaches: make([]rt.FieldCache, proto.InlineCaches),
		liveRegs:    analyzeLiveRegisters(proto),
		singletons:  make(map[*bytecode.Proto]rt.Value),
	}
	for i := range state.fieldCaches {
		state.fieldCaches[i].Symbol = ^uint32(0)
	}
	m.states[proto] = state
	return state
}

package interp

import (
	"fmt"
	"runtime/debug"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/closure"
	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/host"
	rproto "vexlua/internal/runtime/proto"
	"vexlua/internal/runtime/state"
	rtstring "vexlua/internal/runtime/string"
	rttable "vexlua/internal/runtime/table"
	"vexlua/internal/runtime/upvalue"
	"vexlua/internal/runtime/value"
)

type StatusCode uint32

const (
	StatusOK StatusCode = iota
	StatusYield
	StatusError
	StatusDeopt
)

type Outcome struct {
	Status StatusCode
	Values []value.TValue
	Err    error
}

type RuntimeError struct {
	Proto  string
	PC     int
	Opcode bytecode.Opcode
	Reason string
}

func (err *RuntimeError) Error() string {
	if err == nil {
		return ""
	}
	if err.Proto != "" {
		return fmt.Sprintf("%s: pc %d (%s): %s", err.Proto, err.PC, err.Opcode, err.Reason)
	}
	return fmt.Sprintf("pc %d (%s): %s", err.PC, err.Opcode, err.Reason)
}

type Engine struct {
	Heap     *heap.Heap
	Strings  *rtstring.InternTable
	Tables   *rttable.Store
	Hosts    *host.Registry
	Protos   *rproto.Store
	Closures *closure.Store
	Upvalues *upvalue.Manager
	State    *state.VMState

	threads map[uint64]*threadContext
}

type threadContext struct {
	activations []*activation
}

type activation struct {
	thread *state.ThreadState
	frame  *state.CallFrameHeader
	top    uint32
	pc     int
}

type threadSnapshot struct {
	activations int
}

func New() *Engine {
	runtimeHeap := heap.MustNew(0, 0)
	protos := rproto.NewStore(runtimeHeap)
	vmState := state.NewVMState(runtimeHeap)
	return &Engine{
		Heap:     runtimeHeap,
		Strings:  rtstring.NewInternTable(runtimeHeap, 0x9E3779B9),
		Tables:   rttable.NewStore(runtimeHeap),
		Hosts:    host.NewRegistry(runtimeHeap),
		Protos:   protos,
		Closures: closure.NewStore(runtimeHeap, protos),
		Upvalues: upvalue.NewManager(runtimeHeap, vmState),
		State:    vmState,
		threads:  make(map[uint64]*threadContext),
	}
}

func (engine *Engine) NewThread(stackSlots uint32, frameCapacity uint32) (*state.ThreadState, error) {
	return engine.State.NewThread(stackSlots, frameCapacity)
}

func (engine *Engine) InternString(text string) (rtstring.Handle, error) {
	return engine.Strings.Intern(text)
}

func (engine *Engine) NewTable(arrayCap uint32, hashCap uint32) (rttable.Handle, error) {
	return engine.Tables.New(arrayCap, hashCap)
}

func (engine *Engine) NewClosure(proto *bytecode.Proto, env value.TValue, upvalues []value.HeapRef44) (closure.Handle, error) {
	return engine.Closures.NewLuaClosure(proto, env, upvalues)
}

func (engine *Engine) RegisterHostObject(name string, target any, env value.TValue) (host.HostObject, error) {
	handle, err := engine.Hosts.RegisterObject(name, target)
	if err != nil {
		return host.HostObject{}, err
	}
	wrapped, err := engine.Hosts.WrapObject(handle, env)
	if err != nil {
		_ = engine.Hosts.Release(handle)
		return host.HostObject{}, err
	}
	if err := engine.Hosts.Release(handle); err != nil {
		return host.HostObject{}, err
	}
	return wrapped, nil
}

func (engine *Engine) RegisterHostFunction(name string, function any, env value.TValue) (host.HostFunction, error) {
	handle, err := engine.Hosts.RegisterFunction(name, function)
	if err != nil {
		return host.HostFunction{}, err
	}
	wrapped, err := engine.Hosts.WrapFunction(handle, env)
	if err != nil {
		_ = engine.Hosts.Release(handle)
		return host.HostFunction{}, err
	}
	if err := engine.Hosts.Release(handle); err != nil {
		return host.HostFunction{}, err
	}
	return wrapped, nil
}

func (engine *Engine) SetGlobal(env value.TValue, name string, slotValue value.TValue) error {
	key, err := engine.InternString(name)
	if err != nil {
		return err
	}
	return engine.WriteIndexBoundary(env, key.Value, slotValue)
}

func (engine *Engine) GetGlobal(env value.TValue, name string) (value.TValue, bool, error) {
	key, err := engine.InternString(name)
	if err != nil {
		return value.NilValue(), false, err
	}
	return engine.ReadIndexBoundary(env, key.Value)
}

func (engine *Engine) Call(thread *state.ThreadState, callee value.TValue, args []value.TValue, nresults int) ([]value.TValue, error) {
	if thread == nil {
		return nil, fmt.Errorf("thread cannot be nil")
	}
	snapshot := engine.snapshot(thread)
	results, err := engine.callValue(thread, callee, args, nresults)
	if err != nil {
		engine.restoreSnapshot(thread, snapshot)
		return nil, err
	}
	return results, nil
}

func (engine *Engine) ProtectedCall(thread *state.ThreadState, callee value.TValue, args []value.TValue, nresults int) (outcome Outcome) {
	if thread == nil {
		return Outcome{Status: StatusError, Err: fmt.Errorf("thread cannot be nil")}
	}
	snapshot := engine.snapshot(thread)
	defer func() {
		if recovered := recover(); recovered != nil {
			engine.restoreSnapshot(thread, snapshot)
			outcome = Outcome{
				Status: StatusError,
				Err:    fmt.Errorf("panic: %v\n%s", recovered, debug.Stack()),
			}
		}
	}()
	values, err := engine.callValue(thread, callee, args, nresults)
	if err != nil {
		engine.restoreSnapshot(thread, snapshot)
		return Outcome{Status: StatusError, Err: err}
	}
	return Outcome{Status: StatusOK, Values: values}
}

func (engine *Engine) callValue(thread *state.ThreadState, callee value.TValue, args []value.TValue, nresults int) ([]value.TValue, error) {
	return engine.CallValueBoundary(thread, callee, args, nresults)
}

func (engine *Engine) threadState(thread *state.ThreadState) *threadContext {
	ctx, ok := engine.threads[thread.ID]
	if ok {
		return ctx
	}
	ctx = &threadContext{}
	engine.threads[thread.ID] = ctx
	return ctx
}

func (engine *Engine) snapshot(thread *state.ThreadState) threadSnapshot {
	ctx := engine.threadState(thread)
	return threadSnapshot{
		activations: len(ctx.activations),
	}
}

func (engine *Engine) restoreSnapshot(thread *state.ThreadState, snapshot threadSnapshot) {
	ctx := engine.threadState(thread)
	for len(ctx.activations) > snapshot.activations {
		act := ctx.activations[len(ctx.activations)-1]
		_, _ = engine.Upvalues.CloseAtOrAbove(thread, activationBaseAddress(act))
		_, _ = thread.PopFrame()
		ctx.activations = ctx.activations[:len(ctx.activations)-1]
		if registerBase, err := thread.SlotIndexForAddress(activationBaseAddress(act)); err == nil {
			engine.clearSlots(thread, registerBase, activationReservedSlots(act))
		}
	}
}

func activationBaseAddress(act *activation) uintptr {
	if act == nil || act.frame == nil {
		return 0
	}
	return uintptr(act.frame.RegsBase)
}

func activationReservedSlots(act *activation) uint32 {
	if act == nil || act.frame == nil || act.frame.RegisterCount == 0 {
		return 1
	}
	return uint32(act.frame.RegisterCount)
}

func (engine *Engine) clearSlots(thread *state.ThreadState, start uint32, count uint32) {
	for index := uint32(0); index < count; index++ {
		address, err := thread.SlotAddress(start + index)
		if err != nil {
			return
		}
		_ = thread.SetValueAtAddress(address, value.NilValue())
	}
}

func runtimeError(proto *bytecode.Proto, pc int, opcode bytecode.Opcode, reason string) error {
	if proto == nil {
		return &RuntimeError{PC: pc, Opcode: opcode, Reason: reason}
	}
	return &RuntimeError{Proto: proto.Source, PC: pc, Opcode: opcode, Reason: reason}
}

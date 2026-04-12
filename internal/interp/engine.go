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
	assist   AllocationAssistant

	threads map[uint64]*threadContext
}

type AllocationAssistant interface {
	AssistAllocation(bytes uint64) error
	AssistSafepoint() error
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
	strings := rtstring.NewInternTable(runtimeHeap, 0x9E3779B9)
	hosts := host.NewRegistry(runtimeHeap)
	return &Engine{
		Heap:     runtimeHeap,
		Strings:  strings,
		Tables:   rttable.NewStore(runtimeHeap),
		Hosts:    hosts,
		Protos:   protos,
		Closures: closure.NewStore(runtimeHeap, protos),
		Upvalues: upvalue.NewManager(runtimeHeap, vmState),
		State:    vmState,
		threads:  make(map[uint64]*threadContext),
	}
}

func (engine *Engine) SetAllocationAssistant(assist AllocationAssistant) {
	if engine == nil {
		return
	}
	engine.assist = assist
}

func (engine *Engine) AdvanceGCSafepoint() error {
	if engine == nil || engine.assist == nil {
		return nil
	}
	return engine.assist.AssistSafepoint()
}

func (engine *Engine) Close() error {
	if engine == nil {
		return nil
	}
	if engine.State != nil {
		engine.State.Close()
		engine.State = nil
	}
	engine.threads = nil
	return nil
}

func (engine *Engine) NewThread(stackSlots uint32, frameCapacity uint32) (*state.ThreadState, error) {
	return engine.State.NewThread(stackSlots, frameCapacity)
}

func (engine *Engine) InternString(text string) (rtstring.Handle, error) {
	before := engine.liveBytes()
	handle, err := engine.Strings.Intern(text)
	if err != nil {
		return rtstring.Handle{}, err
	}
	engine.retainRef(handle.Ref)
	if err := engine.advanceGCAfterBoundary(before); err != nil {
		return rtstring.Handle{}, err
	}
	return handle, nil
}

func (engine *Engine) NewTable(arrayCap uint32, hashCap uint32) (rttable.Handle, error) {
	before := engine.liveBytes()
	handle, err := engine.Tables.New(arrayCap, hashCap)
	if err != nil {
		return rttable.Handle{}, err
	}
	engine.retainRef(handle.Ref)
	if err := engine.advanceGCAfterBoundary(before); err != nil {
		return rttable.Handle{}, err
	}
	return handle, nil
}

func (engine *Engine) NewClosure(proto *bytecode.Proto, env value.TValue, upvalues []value.HeapRef44) (closure.Handle, error) {
	before := engine.liveBytes()
	handle, err := engine.Closures.NewLuaClosure(proto, env, upvalues)
	if err != nil {
		return closure.Handle{}, err
	}
	engine.retainRef(handle.Ref)
	if err := engine.advanceGCAfterBoundary(before); err != nil {
		return closure.Handle{}, err
	}
	return handle, nil
}

func (engine *Engine) RegisterHostObject(name string, target any, env value.TValue) (host.HostObject, error) {
	before := engine.liveBytes()
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
	engine.retainRef(wrapped.Ref)
	if err := engine.advanceGCAfterBoundary(before); err != nil {
		return host.HostObject{}, err
	}
	return wrapped, nil
}

func (engine *Engine) RegisterHostFunction(name string, function any, env value.TValue) (host.HostFunction, error) {
	before := engine.liveBytes()
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
	engine.retainRef(wrapped.Ref)
	if err := engine.advanceGCAfterBoundary(before); err != nil {
		return host.HostFunction{}, err
	}
	return wrapped, nil
}

func (engine *Engine) SetGlobal(env value.TValue, name string, slotValue value.TValue) error {
	before := engine.liveBytes()
	key, err := engine.Strings.Intern(name)
	if err != nil {
		return err
	}
	if err := engine.WriteIndexBoundary(env, key.Value, slotValue); err != nil {
		return err
	}
	return engine.advanceGCAfterBoundary(before)
}

func (engine *Engine) GetGlobal(env value.TValue, name string) (value.TValue, bool, error) {
	before := engine.liveBytes()
	key, err := engine.Strings.Intern(name)
	if err != nil {
		return value.NilValue(), false, err
	}
	result, found, err := engine.ReadIndexBoundary(env, key.Value)
	if err != nil {
		return value.NilValue(), false, err
	}
	if found {
		engine.retainValue(result)
	}
	if err := engine.advanceGCAfterBoundary(before); err != nil {
		return value.NilValue(), false, err
	}
	return result, found, nil
}

func (engine *Engine) Call(thread *state.ThreadState, callee value.TValue, args []value.TValue, nresults int) ([]value.TValue, error) {
	if thread == nil {
		return nil, fmt.Errorf("thread cannot be nil")
	}
	before := engine.liveBytes()
	snapshot := engine.snapshot(thread)
	results, err := engine.callValue(thread, callee, args, nresults)
	if err != nil {
		engine.restoreSnapshot(thread, snapshot)
		return nil, err
	}
	engine.retainValues(results)
	if err := engine.advanceGCAfterBoundary(before); err != nil {
		return nil, err
	}
	return results, nil
}

func (engine *Engine) ProtectedCall(thread *state.ThreadState, callee value.TValue, args []value.TValue, nresults int) (outcome Outcome) {
	if thread == nil {
		return Outcome{Status: StatusError, Err: fmt.Errorf("thread cannot be nil")}
	}
	before := engine.liveBytes()
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
	engine.retainValues(values)
	if err := engine.advanceGCAfterBoundary(before); err != nil {
		return Outcome{Status: StatusError, Err: err}
	}
	return Outcome{Status: StatusOK, Values: values}
}

func (engine *Engine) RetainValue(slotValue value.TValue) {
	engine.retainValue(slotValue)
}

func (engine *Engine) ReleaseValue(slotValue value.TValue) error {
	if engine == nil || engine.State == nil || engine.State.ExternalRoots() == nil {
		return nil
	}
	return engine.State.ExternalRoots().ReleaseValue(slotValue)
}

func (engine *Engine) ReleaseValues(values []value.TValue) error {
	for _, slotValue := range values {
		if err := engine.ReleaseValue(slotValue); err != nil {
			return err
		}
	}
	return nil
}

func (engine *Engine) ReleaseRef(ref value.HeapRef44) error {
	if engine == nil || engine.State == nil || engine.State.ExternalRoots() == nil {
		return nil
	}
	return engine.State.ExternalRoots().ReleaseRef(ref)
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
		if act != nil && act.frame != nil {
			closeLimit := activationBaseAddress(act) + uintptr(act.frame.RegisterCount)*value.TValueSize
			_, _ = engine.Upvalues.CloseInRange(thread, activationBaseAddress(act), closeLimit)
		}
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

func (engine *Engine) retainRef(ref value.HeapRef44) {
	if engine == nil || engine.State == nil || engine.State.ExternalRoots() == nil {
		return
	}
	engine.State.ExternalRoots().RetainRef(ref)
}

func (engine *Engine) retainValue(slotValue value.TValue) {
	if engine == nil || engine.State == nil || engine.State.ExternalRoots() == nil {
		return
	}
	engine.State.ExternalRoots().RetainValue(slotValue)
}

func (engine *Engine) retainValues(values []value.TValue) {
	for _, slotValue := range values {
		engine.retainValue(slotValue)
	}
}

func (engine *Engine) liveBytes() uint64 {
	if engine == nil || engine.Heap == nil {
		return 0
	}
	return engine.Heap.LiveBytes()
}

func (engine *Engine) advanceGCAfterBoundary(before uint64) error {
	if engine == nil || engine.assist == nil || engine.Heap == nil {
		return nil
	}
	after := engine.Heap.LiveBytes()
	if after <= before {
		return nil
	}
	return engine.assist.AssistAllocation(after - before)
}

func runtimeError(proto *bytecode.Proto, pc int, opcode bytecode.Opcode, reason string) error {
	if proto == nil {
		return &RuntimeError{PC: pc, Opcode: opcode, Reason: reason}
	}
	return &RuntimeError{Proto: proto.Source, PC: pc, Opcode: opcode, Reason: reason}
}

package gc

import (
	"fmt"

	"vexlua/internal/interp"
	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/host"
	rproto "vexlua/internal/runtime/proto"
	"vexlua/internal/runtime/state"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/upvalue"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/baseline"
)

type VisitFunc func(value.HeapRef44) error

type RootSource interface {
	WalkRoots(visit VisitFunc) error
}

type RootSourceFunc func(visit VisitFunc) error

func (fn RootSourceFunc) WalkRoots(visit VisitFunc) error {
	if fn == nil {
		return nil
	}
	return fn(visit)
}

type Scanner struct {
	heap     *heap.Heap
	compiled *baseline.Runtime
}

func NewScanner(runtimeHeap *heap.Heap) *Scanner {
	if runtimeHeap == nil {
		panic("gc scanner requires a heap")
	}
	return &Scanner{heap: runtimeHeap}
}

func (scanner *Scanner) BindCompiledRuntime(runtime *baseline.Runtime) {
	if scanner == nil {
		return
	}
	scanner.compiled = runtime
}

func (scanner *Scanner) WalkRoots(vm *state.VMState, visit VisitFunc, extra ...RootSource) error {
	if err := scanner.WalkVMState(vm, visit); err != nil {
		return err
	}
	for _, source := range extra {
		if source == nil {
			continue
		}
		if err := source.WalkRoots(visit); err != nil {
			return err
		}
	}
	return nil
}

func (scanner *Scanner) WalkVMState(vm *state.VMState, visit VisitFunc) error {
	if vm == nil {
		return fmt.Errorf("vm state cannot be nil")
	}
	if roots := vm.ExternalRoots(); roots != nil {
		if err := roots.WalkRefs(visit); err != nil {
			return err
		}
	}
	for _, thread := range vm.Threads() {
		if err := scanner.WalkThread(thread, visit); err != nil {
			return err
		}
	}
	return nil
}

func (scanner *Scanner) WalkThread(thread *state.ThreadState, visit VisitFunc) error {
	if thread == nil {
		return fmt.Errorf("thread cannot be nil")
	}
	if visit == nil {
		return nil
	}
	for frame := thread.CurrentFrame(); frame != nil; {
		if err := scanner.WalkFrame(thread, frame, visit); err != nil {
			return err
		}
		if frame.PrevFrame == 0 {
			break
		}
		previous, err := thread.FrameAtAddress(uintptr(frame.PrevFrame))
		if err != nil {
			return err
		}
		frame = previous
	}
	return scanner.walkOpenUpvalues(thread, visit)
}

func (scanner *Scanner) WalkFrame(thread *state.ThreadState, frame *state.CallFrameHeader, visit VisitFunc) error {
	return scanner.walkFrame(thread, frame, uint32(frame.Top), visit)
}

func (scanner *Scanner) WalkActivationFrame(thread *state.ThreadState, frame *state.CallFrameHeader, top uint32, visit VisitFunc) error {
	return scanner.walkFrame(thread, frame, top, visit)
}

func (scanner *Scanner) walkFrame(thread *state.ThreadState, frame *state.CallFrameHeader, top uint32, visit VisitFunc) error {
	if thread == nil {
		return fmt.Errorf("thread cannot be nil")
	}
	if frame == nil {
		return fmt.Errorf("frame cannot be nil")
	}
	if visit == nil {
		return nil
	}
	if scanner.compiled != nil {
		handled, err := scanner.compiled.WalkFrameRoots(thread, frame, visit)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
	}
	if err := visitTValue(frame.Closure, visit); err != nil {
		return err
	}
	if err := visitTValue(frame.Proto, visit); err != nil {
		return err
	}
	capacity := uint32(frame.RegisterCount) + uint32(frame.SpillCount)
	if top > capacity {
		return fmt.Errorf("frame top %d exceeds slot capacity %d", top, capacity)
	}
	registerTop := top
	if registerTop > uint32(frame.RegisterCount) {
		registerTop = uint32(frame.RegisterCount)
	}
	for index := uint32(0); index < registerTop; index++ {
		slotValue, err := thread.Register(frame, uint16(index))
		if err != nil {
			return err
		}
		if err := visitTValue(slotValue, visit); err != nil {
			return err
		}
	}
	for index := uint16(0); index < frame.SpillCount; index++ {
		slotValue, err := thread.Spill(frame, index)
		if err != nil {
			return err
		}
		if err := visitTValue(slotValue, visit); err != nil {
			return err
		}
	}
	if frame.VarargCount == 0 {
		return nil
	}
	if frame.VarargBase == 0 {
		return fmt.Errorf("frame has %d varargs but no vararg base", frame.VarargCount)
	}
	for index := uint32(0); index < frame.VarargCount; index++ {
		address := uintptr(frame.VarargBase) + uintptr(index)*value.TValueSize
		slotValue, err := thread.ValueAtAddress(address)
		if err != nil {
			return err
		}
		if err := visitTValue(slotValue, visit); err != nil {
			return err
		}
	}
	return nil
}

func (scanner *Scanner) InterpreterActivationRoots(engine *interp.Engine) RootSource {
	return RootSourceFunc(func(visit VisitFunc) error {
		if engine == nil {
			return nil
		}
		return engine.WalkActivationFrames(func(thread *state.ThreadState, frame *state.CallFrameHeader, top uint32) error {
			return scanner.WalkActivationFrame(thread, frame, top, visit)
		})
	})
}

func StringTableRoots(table *rtstring.InternTable) RootSource {
	return RootSourceFunc(func(visit VisitFunc) error {
		if table == nil {
			return nil
		}
		return table.WalkRefs(visit)
	})
}

func ProtoStoreRoots(store *rproto.Store) RootSource {
	return RootSourceFunc(func(visit VisitFunc) error {
		if store == nil {
			return nil
		}
		return store.WalkRefs(visit)
	})
}

func HostRegistryRoots(registry *host.Registry) RootSource {
	return RootSourceFunc(func(visit VisitFunc) error {
		if registry == nil {
			return nil
		}
		return registry.WalkDescriptorRefs(visit)
	})
}

func CompiledMetadataRoots(runtime *baseline.Runtime) RootSource {
	return RootSourceFunc(func(visit VisitFunc) error {
		if runtime == nil {
			return nil
		}
		return runtime.WalkCompiledRoots(visit)
	})
}

func Values(values ...value.TValue) RootSource {
	return RootSourceFunc(func(visit VisitFunc) error {
		for _, slotValue := range values {
			if err := visitTValue(slotValue, visit); err != nil {
				return err
			}
		}
		return nil
	})
}

func visitTValue(slotValue value.TValue, visit VisitFunc) error {
	if visit == nil {
		return nil
	}
	ref, ok := slotValue.HeapRef()
	if !ok || ref == 0 {
		return nil
	}
	return visit(ref)
}

func (scanner *Scanner) walkOpenUpvalues(thread *state.ThreadState, visit VisitFunc) error {
	if visit == nil {
		return nil
	}
	seen := make(map[value.HeapRef44]struct{})
	for current := thread.OpenUpvalueHead(); current != 0; {
		if _, ok := seen[current]; ok {
			return fmt.Errorf("open upvalue list cycle at %#x", uint64(current))
		}
		seen[current] = struct{}{}
		if err := visit(current); err != nil {
			return err
		}
		object, err := scanner.readUpvalueObject(current)
		if err != nil {
			return err
		}
		if object.State != upvalue.StateOpen {
			return fmt.Errorf("thread open upvalue list contains state %d", object.State)
		}
		slotValue, err := thread.ValueAtAddress(uintptr(object.SlotAddress))
		if err != nil {
			return err
		}
		if err := visitTValue(slotValue, visit); err != nil {
			return err
		}
		current = object.NextOpen
	}
	return nil
}

func (scanner *Scanner) readUpvalueObject(ref value.HeapRef44) (upvalue.Object, error) {
	address, err := scanner.heap.DecodeHeapRef(ref)
	if err != nil {
		return upvalue.Object{}, err
	}
	offset, err := scanner.heap.OffsetForAddress(address)
	if err != nil {
		return upvalue.Object{}, err
	}
	bytes, err := scanner.heap.Resolve(offset, upvalue.ObjectSize)
	if err != nil {
		return upvalue.Object{}, err
	}
	return upvalue.ReadObject(bytes)
}

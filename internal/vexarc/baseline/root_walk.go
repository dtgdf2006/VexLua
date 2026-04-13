package baseline

import (
	"fmt"

	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
)

func (runtime *Runtime) WalkCompiledRoots(visit func(value.HeapRef44) error) error {
	for protoRef, compiled := range runtime.compiled {
		if compiled.ProtoRef == 0 {
			compiled.ProtoRef = protoRef
		}
		if err := compiled.WalkRoots(visit); err != nil {
			return err
		}
	}
	return nil
}

func (runtime *Runtime) WalkFrameRoots(thread *state.ThreadState, frame *state.CallFrameHeader, visit func(value.HeapRef44) error) (bool, error) {
	if !frame.Flags.Has(state.FrameFlagCompiled) {
		return false, nil
	}
	_, compiled, err := runtime.compiledFrameState(frame)
	if err != nil {
		return false, err
	}
	if err := visitFrameTValue(frame.Closure, visit); err != nil {
		return true, err
	}
	if err := visitFrameTValue(frame.Proto, visit); err != nil {
		return true, err
	}
	if liveSet, ok := compiled.Metadata.LiveSlotSetAtBytecode(int(frame.SavedBCOff)); ok {
		if err := liveSet.WalkRegisters(uint32(frame.LogicalTop()), func(slot uint32) error {
			if slot >= uint32(frame.RegisterCount) {
				return fmt.Errorf("compiled live slot %d exceeds register_count %d at pc %d", slot, frame.RegisterCount, frame.SavedBCOff)
			}
			slotValue, err := thread.Register(frame, uint16(slot))
			if err != nil {
				return err
			}
			return visitFrameTValue(slotValue, visit)
		}); err != nil {
			return true, err
		}
	} else {
		top := uint32(frame.LogicalTop())
		if top > uint32(frame.RegisterCount) {
			return true, fmt.Errorf("compiled frame top %d exceeds register_count %d", top, frame.RegisterCount)
		}
		for slot := uint32(0); slot < top; slot++ {
			slotValue, err := thread.Register(frame, uint16(slot))
			if err != nil {
				return true, err
			}
			if err := visitFrameTValue(slotValue, visit); err != nil {
				return true, err
			}
		}
	}
	for slot := uint16(0); slot < frame.SpillCount; slot++ {
		slotValue, err := thread.Spill(frame, slot)
		if err != nil {
			return true, err
		}
		if err := visitFrameTValue(slotValue, visit); err != nil {
			return true, err
		}
	}
	if frame.VarargCount == 0 {
		return true, nil
	}
	if frame.VarargBase == 0 {
		return true, fmt.Errorf("compiled frame has %d varargs but no vararg base", frame.VarargCount)
	}
	for index := uint32(0); index < frame.VarargCount; index++ {
		address := uintptr(frame.VarargBase) + uintptr(index)*value.TValueSize
		slotValue, err := thread.ValueAtAddress(address)
		if err != nil {
			return true, err
		}
		if err := visitFrameTValue(slotValue, visit); err != nil {
			return true, err
		}
	}
	return true, nil
}

func visitFrameTValue(slotValue value.TValue, visit func(value.HeapRef44) error) error {
	ref, ok := slotValue.HeapRef()
	if !ok || ref == 0 {
		return nil
	}
	return visit(ref)
}

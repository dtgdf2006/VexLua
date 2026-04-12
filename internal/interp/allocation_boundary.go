package interp

import (
	"fmt"
	"strings"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/upvalue"
	"vexlua/internal/runtime/value"
)

func (engine *Engine) NewTableBoundary(arrayCap uint32, hashCap uint32, publish func(value.TValue) error) error {
	if publish == nil {
		return fmt.Errorf("newtable boundary requires publish callback")
	}
	before := engine.liveBytes()
	handle, err := engine.Tables.New(arrayCap, hashCap)
	if err != nil {
		return err
	}
	if err := publish(handle.Value); err != nil {
		return err
	}
	return engine.advanceGCAfterBoundary(before)
}

func (engine *Engine) ConcatValuesBoundary(values []value.TValue, publish func(value.TValue) error) error {
	if publish == nil {
		return fmt.Errorf("concat boundary requires publish callback")
	}
	before := engine.liveBytes()
	var builder strings.Builder
	for _, slotValue := range values {
		part, err := engine.concatOperandText(slotValue)
		if err != nil {
			return err
		}
		builder.WriteString(part)
	}
	handle, err := engine.Strings.Intern(builder.String())
	if err != nil {
		return err
	}
	if err := publish(handle.Value); err != nil {
		return err
	}
	return engine.advanceGCAfterBoundary(before)
}

func (engine *Engine) CloseUpvaluesBoundary(thread *state.ThreadState, level uintptr) ([]upvalue.Handle, error) {
	before := engine.liveBytes()
	closed, err := engine.Upvalues.CloseAtOrAbove(thread, level)
	if err != nil {
		return nil, err
	}
	if err := engine.advanceGCAfterBoundary(before); err != nil {
		return nil, err
	}
	return closed, nil
}

func (engine *Engine) CloseUpvaluesInRangeBoundary(thread *state.ThreadState, lower uintptr, upper uintptr) ([]upvalue.Handle, error) {
	before := engine.liveBytes()
	closed, err := engine.Upvalues.CloseInRange(thread, lower, upper)
	if err != nil {
		return nil, err
	}
	if err := engine.advanceGCAfterBoundary(before); err != nil {
		return nil, err
	}
	return closed, nil
}

func (engine *Engine) NewClosureBoundary(proto *bytecode.Proto, env value.TValue, upvalues []value.HeapRef44, publish func(value.TValue) error) error {
	if proto == nil {
		return fmt.Errorf("closure boundary requires child proto")
	}
	if publish == nil {
		return fmt.Errorf("closure boundary requires publish callback")
	}
	before := engine.liveBytes()
	handle, err := engine.Closures.NewLuaClosure(proto, env, upvalues)
	if err != nil {
		return err
	}
	if err := publish(handle.Value); err != nil {
		return err
	}
	return engine.advanceGCAfterBoundary(before)
}

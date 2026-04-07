package vm

import (
	"fmt"

	rt "vexlua/internal/runtime"
)

func (m *VM) GlobalEnv() rt.Value {
	return m.globalEnv()
}

func (m *VM) CurrentEnv() rt.Value {
	frame := m.currentFrame()
	if frame == nil {
		return m.globalEnv()
	}
	return m.envOf(frame.closure)
}

func (m *VM) SetCurrentEnv(env rt.Value) error {
	if err := m.validateEnv(env); err != nil {
		return err
	}
	frame := m.currentFrame()
	if frame == nil {
		return fmt.Errorf("setfenv requires an active Lua frame")
	}
	frame.closure.Env = env
	return nil
}

func (m *VM) GetFunctionEnv(value rt.Value) (rt.Value, error) {
	h, ok := value.Handle()
	if !ok {
		return rt.NilValue, fmt.Errorf("getfenv expects function")
	}
	switch h.Kind() {
	case rt.ObjectLuaClosure:
		closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
		return m.envOf(closure), nil
	case rt.ObjectHostFunction:
		return m.globalEnv(), nil
	default:
		return rt.NilValue, fmt.Errorf("getfenv expects function")
	}
}

func (m *VM) SetFunctionEnv(value rt.Value, env rt.Value) error {
	if err := m.validateEnv(env); err != nil {
		return err
	}
	h, ok := value.Handle()
	if !ok {
		return fmt.Errorf("setfenv expects function")
	}
	switch h.Kind() {
	case rt.ObjectLuaClosure:
		closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
		closure.Env = env
		return nil
	case rt.ObjectHostFunction:
		return fmt.Errorf("setfenv does not support host functions")
	default:
		return fmt.Errorf("setfenv expects function")
	}
}

func (m *VM) CallValue(callee rt.Value, args []rt.Value) ([]rt.Value, error) {
	return m.callValueMulti(callee, args)
}

func (m *VM) Less(left rt.Value, right rt.Value) (bool, error) {
	return m.lessValues(left, right)
}

func (m *VM) RunningCoroutine() *Coroutine {
	for i := len(m.activeCoros) - 1; i >= 0; i-- {
		if m.activeCoros[i] != nil && m.activeCoros[i].visible {
			return m.activeCoros[i]
		}
	}
	return nil
}

func (co *Coroutine) Proxy() rt.Value {
	if co == nil {
		return rt.NilValue
	}
	return co.proxy
}

func (co *Coroutine) SetProxy(value rt.Value) {
	if co == nil {
		return
	}
	co.proxy = value
}

func (m *VM) currentFrame() *callFrame {
	if len(m.activeFrames) == 0 {
		return nil
	}
	return m.activeFrames[len(m.activeFrames)-1]
}

func (m *VM) pushActiveFrame(frame *callFrame) func() {
	m.activeFrames = append(m.activeFrames, frame)
	return func() {
		m.activeFrames = m.activeFrames[:len(m.activeFrames)-1]
	}
}

func (m *VM) pushActiveCoroutine(co *Coroutine) func() {
	m.activeCoros = append(m.activeCoros, co)
	return func() {
		m.activeCoros = m.activeCoros[:len(m.activeCoros)-1]
	}
}

func (m *VM) validateEnv(env rt.Value) error {
	h, ok := env.Handle()
	if !ok || h.Kind() != rt.ObjectTable {
		return fmt.Errorf("environment must be a table")
	}
	return nil
}

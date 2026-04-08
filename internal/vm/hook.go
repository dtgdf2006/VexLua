package vm

import (
	"fmt"

	rt "vexlua/internal/runtime"
)

type hookState struct {
	function  rt.Value
	call      bool
	ret       bool
	line      bool
	count     int
	remaining int
	running   bool
	context   *hookContext
}

type hookContext struct {
	target DebugInfo
	caller *DebugInfo
}

func (m *VM) SetHook(co *Coroutine, function rt.Value, mask string, count int) error {
	if co == nil {
		co = m.currentCoroutine()
		if co == nil {
			return fmt.Errorf("debug.sethook requires an active coroutine")
		}
	}
	if function.Kind() == rt.KindNil {
		co.hookTouched = true
		co.hook = hookState{function: rt.NilValue}
		return nil
	}
	if !m.isCallable(function) {
		return fmt.Errorf("debug.sethook expects function or nil")
	}
	if count < 0 {
		count = 0
	}
	hook := hookState{function: function, count: count, remaining: count}
	for _, option := range mask {
		switch option {
		case 'c':
			hook.call = true
		case 'r':
			hook.ret = true
		case 'l':
			hook.line = true
		}
	}
	co.hookTouched = true
	co.hook = hook
	return nil
}

func (m *VM) GetHook(co *Coroutine) (rt.Value, string, int) {
	if co == nil {
		co = m.currentCoroutine()
	}
	if co == nil {
		return rt.NilValue, "", 0
	}
	hook := co.hook.function
	if hook.IsNumber() && hook.Number() == 0 {
		hook = rt.NilValue
	}
	var mask []byte
	if co.hook.call {
		mask = append(mask, 'c')
	}
	if co.hook.ret {
		mask = append(mask, 'r')
	}
	if co.hook.line {
		mask = append(mask, 'l')
	}
	return hook, string(mask), co.hook.count
}

func (h hookState) enabled() bool {
	return h.function.Kind() != rt.KindNil && (h.call || h.ret || h.line || h.count > 0)
}

func (m *VM) dispatchCallHook(co *Coroutine, target DebugInfo, caller *DebugInfo) error {
	return m.dispatchHook(co, true, false, "call", nil, target, caller)
}

func (m *VM) dispatchReturnHook(co *Coroutine, event string, target DebugInfo, caller *DebugInfo) error {
	return m.dispatchHook(co, false, true, event, nil, target, caller)
}

func (m *VM) dispatchLineHook(co *Coroutine, frame *callFrame, line int) error {
	target := m.debugInfoForFrame(frame)
	target.CurrentLine = line
	return m.dispatchHook(co, false, false, "line", &line, target, m.debugCallerInfo(co, frame))
}

func (m *VM) dispatchCountHook(co *Coroutine, frame *callFrame) error {
	target := m.debugInfoForFrame(frame)
	if line := nextLineForFrame(frame); line >= 0 {
		target.CurrentLine = line
	}
	return m.dispatchHook(co, false, false, "count", nil, target, m.debugCallerInfo(co, frame))
}

func (m *VM) maybeDispatchStepHooks(co *Coroutine, frame *callFrame) error {
	if co == nil || frame == nil || co.hook.running || !co.hook.enabled() {
		return nil
	}
	if co.hook.line {
		line := nextLineForFrame(frame)
		if line >= 0 && (line != frame.lastHookLine || frame.pc <= frame.lastHookPC) {
			if err := m.dispatchLineHook(co, frame, line); err != nil {
				return err
			}
			frame.lastHookLine = line
			frame.lastHookPC = frame.pc
		}
	}
	if co.hook.count > 0 {
		co.hook.remaining--
		if co.hook.remaining <= 0 {
			if err := m.dispatchCountHook(co, frame); err != nil {
				return err
			}
			co.hook.remaining = co.hook.count
		}
	}
	return nil
}

func (m *VM) dispatchHook(co *Coroutine, wantCall bool, wantReturn bool, event string, line *int, target DebugInfo, caller *DebugInfo) error {
	if co == nil || co.hook.running || co.hook.function.Kind() == rt.KindNil {
		return nil
	}
	if wantCall && !co.hook.call {
		return nil
	}
	if wantReturn && !co.hook.ret {
		return nil
	}
	if !wantCall && !wantReturn {
		switch event {
		case "line":
			if !co.hook.line {
				return nil
			}
		case "count":
			if co.hook.count <= 0 {
				return nil
			}
		}
	}
	prevContext := co.hook.context
	co.hook.running = true
	co.hook.context = &hookContext{target: target, caller: caller}
	defer func() {
		co.hook.context = prevContext
		co.hook.running = false
	}()
	lineValue := rt.NilValue
	if line != nil {
		lineValue = rt.NumberValue(float64(*line))
	}
	_, err := m.callValueMulti(co.hook.function, []rt.Value{m.runtime.StringValue(event), lineValue})
	if err != nil {
		return err
	}
	if co.status == CoroutineSuspended {
		return fmt.Errorf("hook yielded")
	}
	return nil
}

func (m *VM) debugInfoPtrForFrame(frame *callFrame) *DebugInfo {
	if frame == nil {
		return nil
	}
	info := m.debugInfoForFrame(frame)
	return &info
}

func (m *VM) debugCallerInfo(co *Coroutine, frame *callFrame) *DebugInfo {
	if co == nil || frame == nil {
		return nil
	}
	for index := len(co.frames) - 1; index >= 0; index-- {
		if co.frames[index] != frame {
			continue
		}
		if index == 0 {
			return nil
		}
		return m.debugInfoPtrForFrame(co.frames[index-1])
	}
	return nil
}

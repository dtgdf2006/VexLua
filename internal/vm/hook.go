package vm

import (
	"fmt"

	rt "vexlua/internal/runtime"
)

type hookState struct {
	function rt.Value
	call     bool
	ret      bool
	line     bool
	count    int
	running  bool
	context  *hookContext
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
		co.hook = hookState{}
		return nil
	}
	if !m.isCallable(function) {
		return fmt.Errorf("debug.sethook expects function or nil")
	}
	hook := hookState{function: function, count: count}
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

func (m *VM) dispatchCallHook(co *Coroutine, target DebugInfo, caller *DebugInfo) error {
	return m.dispatchHook(co, true, false, "call", target, caller)
}

func (m *VM) dispatchReturnHook(co *Coroutine, event string, target DebugInfo, caller *DebugInfo) error {
	return m.dispatchHook(co, false, true, event, target, caller)
}

func (m *VM) dispatchHook(co *Coroutine, wantCall bool, wantReturn bool, event string, target DebugInfo, caller *DebugInfo) error {
	if co == nil || co.hook.running || co.hook.function.Kind() == rt.KindNil {
		return nil
	}
	if wantCall && !co.hook.call {
		return nil
	}
	if wantReturn && !co.hook.ret {
		return nil
	}
	prevContext := co.hook.context
	co.hook.running = true
	co.hook.context = &hookContext{target: target, caller: caller}
	defer func() {
		co.hook.context = prevContext
		co.hook.running = false
	}()
	_, err := m.callValueMulti(co.hook.function, []rt.Value{m.runtime.StringValue(event)})
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

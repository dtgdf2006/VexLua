package vm

import (
	"fmt"
	"strings"

	rt "vexlua/internal/runtime"
)

type DebugInfo struct {
	Function        rt.Value
	Name            string
	NameWhat        string
	Source          string
	ShortSource     string
	What            string
	CurrentLine     int
	LineDefined     int
	LastLineDefined int
	NumUpvalues     int
}

func (m *VM) DebugInfoForFunction(value rt.Value) (DebugInfo, error) {
	h, ok := value.Handle()
	if !ok {
		return DebugInfo{}, fmt.Errorf("debug.getinfo expects function")
	}
	switch h.Kind() {
	case rt.ObjectLuaClosure:
		closure := m.runtime.Heap().LuaClosure(h).(*LuaClosure)
		info := m.debugInfoForClosure(closure)
		info.Function = value
		return info, nil
	case rt.ObjectHostFunction:
		host := m.runtime.Heap().HostFunction(h)
		return DebugInfo{
			Function:        value,
			Name:            host.Name,
			NameWhat:        "C",
			Source:          "=[C]",
			ShortSource:     "[C]",
			What:            "C",
			CurrentLine:     -1,
			LineDefined:     -1,
			LastLineDefined: -1,
			NumUpvalues:     0,
		}, nil
	default:
		return DebugInfo{}, fmt.Errorf("debug.getinfo expects function")
	}
}

func (m *VM) DebugInfoForLevel(co *Coroutine, level int) (DebugInfo, bool) {
	if level < 1 {
		return DebugInfo{}, false
	}
	if co == nil {
		co = m.currentCoroutine()
	}
	if co != nil && co.hook.context != nil {
		switch level {
		case 2:
			return co.hook.context.target, true
		case 3:
			if co.hook.context.caller != nil {
				return *co.hook.context.caller, true
			}
			return DebugInfo{}, false
		}
	}
	if co == nil || level > len(co.frames) {
		return DebugInfo{}, false
	}
	frame := co.frames[len(co.frames)-level]
	info := m.debugInfoForFrame(frame)
	if value, ok := m.runtime.FindLuaClosureValue(frame.closure); ok {
		info.Function = value
	}
	return info, true
}

func (m *VM) DebugTraceback(co *Coroutine, message string, level int) string {
	if level < 1 {
		level = 1
	}
	if co == nil {
		co = m.currentCoroutine()
	}
	var builder strings.Builder
	if message != "" {
		builder.WriteString(message)
		builder.WriteByte('\n')
	}
	builder.WriteString("stack traceback:")
	if co == nil {
		return builder.String()
	}
	for depth := level; depth <= len(co.frames); depth++ {
		frame := co.frames[len(co.frames)-depth]
		builder.WriteString("\n\t")
		info := m.debugInfoForFrame(frame)
		builder.WriteString(info.ShortSource)
		if info.CurrentLine >= 0 {
			builder.WriteString(":")
			builder.WriteString(fmt.Sprintf("%d", info.CurrentLine))
		}
		builder.WriteString(": in function '")
		if info.Name != "" {
			builder.WriteString(info.Name)
		} else {
			builder.WriteString("?")
		}
		builder.WriteString("'")
	}
	return builder.String()
}

func (m *VM) GetUpvalue(value rt.Value, index int) (string, rt.Value, bool, error) {
	closure, err := m.luaClosureForDebug(value)
	if err != nil {
		if err == errDebugUnsupportedFunction {
			return "", rt.NilValue, false, nil
		}
		return "", rt.NilValue, false, err
	}
	if index < 1 || index > len(closure.Upvalues) {
		return "", rt.NilValue, false, nil
	}
	name := closure.Proto.Upvalues[index-1].Name
	if name == "" {
		name = fmt.Sprintf("upvalue%d", index)
	}
	return name, closure.Upvalues[index-1].Get(), true, nil
}

func (m *VM) SetUpvalue(value rt.Value, index int, newValue rt.Value) (string, bool, error) {
	closure, err := m.luaClosureForDebug(value)
	if err != nil {
		if err == errDebugUnsupportedFunction {
			return "", false, nil
		}
		return "", false, err
	}
	if index < 1 || index > len(closure.Upvalues) {
		return "", false, nil
	}
	closure.Upvalues[index-1].Set(newValue)
	name := closure.Proto.Upvalues[index-1].Name
	if name == "" {
		name = fmt.Sprintf("upvalue%d", index)
	}
	return name, true, nil
}

var errDebugUnsupportedFunction = fmt.Errorf("unsupported debug function")

func (m *VM) luaClosureForDebug(value rt.Value) (*LuaClosure, error) {
	h, ok := value.Handle()
	if !ok {
		return nil, fmt.Errorf("function expected")
	}
	if h.Kind() == rt.ObjectHostFunction {
		return nil, errDebugUnsupportedFunction
	}
	if h.Kind() != rt.ObjectLuaClosure {
		return nil, fmt.Errorf("function expected")
	}
	return m.runtime.Heap().LuaClosure(h).(*LuaClosure), nil
}

func (m *VM) debugInfoForClosure(closure *LuaClosure) DebugInfo {
	name := closure.Proto.Name
	proto := closure.Proto
	what := "Lua"
	if proto.LineDefined == 0 {
		what = "main"
		name = ""
	}
	source := proto.Source
	if source == "" {
		switch {
		case proto.Name != "":
			source = "=" + proto.Name
		default:
			source = "=(string)"
		}
	}
	return DebugInfo{
		Name:            name,
		NameWhat:        "",
		Source:          source,
		ShortSource:     shortSource(source),
		What:            what,
		CurrentLine:     -1,
		LineDefined:     proto.LineDefined,
		LastLineDefined: proto.LastLineDefined,
		NumUpvalues:     len(closure.Upvalues),
	}
}

func (m *VM) debugInfoForFrame(frame *callFrame) DebugInfo {
	info := m.debugInfoForClosure(frame.closure)
	info.CurrentLine = currentLineForFrame(frame)
	return info
}

func currentLineForFrame(frame *callFrame) int {
	if frame == nil || frame.closure == nil || frame.closure.Proto == nil {
		return -1
	}
	pc := frame.pc - 1
	if pc < 0 {
		if frame.closure.Proto.LineDefined > 0 {
			return frame.closure.Proto.LineDefined
		}
		return -1
	}
	return frame.closure.Proto.CurrentLine(pc)
}

func shortSource(source string) string {
	if source == "" {
		return "?"
	}
	const idSize = 55
	switch source[0] {
	case '@', '=':
		text := source[1:]
		if source[0] == '@' && len(text) > idSize {
			keep := idSize - 3
			if keep < 0 {
				keep = 0
			}
			return "..." + text[len(text)-keep:]
		}
		if len(text) > idSize {
			return text[:idSize]
		}
		return text
	default:
		return source
	}
}

func cReturnDebugInfo() DebugInfo {
	return DebugInfo{
		Source:          "=[C]",
		ShortSource:     "[C]",
		What:            "C",
		CurrentLine:     -1,
		LineDefined:     -1,
		LastLineDefined: -1,
	}
}

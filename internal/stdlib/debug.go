package stdlib

import (
	"fmt"
	"strings"

	rt "vexlua/internal/runtime"
	"vexlua/internal/vm"
)

func registerDebug(runtime *rt.Runtime, machine *vm.VM) error {
	_, debugTable, err := ensureGlobalTable(runtime, "debug")
	if err != nil {
		return err
	}
	debugTable.SetSymbol(runtime.InternSymbol("getfenv"), runtime.NewHostFunction("debug.getfenv", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("debug.getfenv expects 1 argument")
		}
		return machine.GetEnv(args[0])
	}))
	debugTable.SetSymbol(runtime.InternSymbol("setfenv"), runtime.NewHostFunction("debug.setfenv", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 2 {
			return rt.NilValue, fmt.Errorf("debug.setfenv expects 2 arguments")
		}
		if err := machine.SetEnv(args[0], args[1]); err != nil {
			return rt.NilValue, err
		}
		return args[0], nil
	}))
	debugTable.SetSymbol(runtime.InternSymbol("getregistry"), runtime.NewHostFunction("debug.getregistry", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 0 {
			return rt.NilValue, fmt.Errorf("debug.getregistry expects no arguments")
		}
		return rt.HandleValue(runtime.RegistryHandle()), nil
	}))
	debugTable.SetSymbol(runtime.InternSymbol("sethook"), runtime.NewHostFunction("sethook", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		co, nextArg, err := debugCoroutineArg(runtime, args)
		if err != nil {
			return rt.NilValue, err
		}
		if nextArg >= len(args) {
			if err := machine.SetHook(co, rt.NilValue, "", 0); err != nil {
				return rt.NilValue, err
			}
			return rt.NilValue, nil
		}
		hook := args[nextArg]
		nextArg++
		mask := ""
		if nextArg < len(args) && args[nextArg].Kind() != rt.KindNil {
			text, ok := runtime.ToString(args[nextArg])
			if !ok {
				return rt.NilValue, fmt.Errorf("debug.sethook mask expects string")
			}
			mask = text
			nextArg++
		}
		count := 0
		if nextArg < len(args) && args[nextArg].Kind() != rt.KindNil {
			if !args[nextArg].IsNumber() {
				return rt.NilValue, fmt.Errorf("debug.sethook count expects number")
			}
			count = int(args[nextArg].Number())
		}
		if err := machine.SetHook(co, hook, mask, count); err != nil {
			return rt.NilValue, err
		}
		return rt.NilValue, nil
	}))
	debugTable.SetSymbol(runtime.InternSymbol("gethook"), runtime.NewHostFunctionMulti("debug.gethook", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		co, nextArg, err := debugCoroutineArg(runtime, args)
		if err != nil {
			return nil, err
		}
		if nextArg != len(args) {
			return nil, fmt.Errorf("debug.gethook expects no arguments")
		}
		hook, mask, count := machine.GetHook(co)
		return []rt.Value{hook, runtime.StringValue(mask), rt.NumberValue(float64(count))}, nil
	}))
	debugTable.SetSymbol(runtime.InternSymbol("traceback"), runtime.NewHostFunction("debug.traceback", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		co, nextArg, err := debugCoroutineArg(runtime, args)
		if err != nil {
			return rt.NilValue, err
		}
		message := ""
		if nextArg < len(args) && args[nextArg].Kind() != rt.KindNil {
			message, err = plainString(runtime, args[nextArg])
			if err != nil {
				return rt.NilValue, err
			}
			nextArg++
		}
		level := 1
		if nextArg < len(args) && args[nextArg].Kind() != rt.KindNil {
			if !args[nextArg].IsNumber() {
				return rt.NilValue, fmt.Errorf("debug.traceback level expects number")
			}
			level = int(args[nextArg].Number())
		}
		return runtime.StringValue(machine.DebugTraceback(co, message, level)), nil
	}))
	debugTable.SetSymbol(runtime.InternSymbol("getinfo"), runtime.NewHostFunction("debug.getinfo", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) == 0 {
			return rt.NilValue, fmt.Errorf("debug.getinfo expects function or level")
		}
		co, nextArg, err := debugCoroutineArg(runtime, args)
		if err != nil {
			return rt.NilValue, err
		}
		if nextArg >= len(args) {
			return rt.NilValue, fmt.Errorf("debug.getinfo expects function or level")
		}
		what := "flnSu"
		if nextArg+1 < len(args) && args[nextArg+1].Kind() != rt.KindNil {
			text, ok := runtime.ToString(args[nextArg+1])
			if !ok {
				return rt.NilValue, fmt.Errorf("debug.getinfo options expects string")
			}
			what = text
		}
		if err := validateDebugInfoOptions(what); err != nil {
			return rt.NilValue, err
		}
		includeActiveLines := strings.ContainsRune(what, 'L')
		var info vm.DebugInfo
		if args[nextArg].IsNumber() {
			level := int(args[nextArg].Number())
			stackInfo, ok := machine.DebugInfoForLevelWithOptions(co, level, includeActiveLines)
			if !ok {
				return rt.NilValue, nil
			}
			info = stackInfo
		} else {
			functionInfo, err := machine.DebugInfoForFunctionWithOptions(args[nextArg], includeActiveLines)
			if err != nil {
				return rt.NilValue, err
			}
			info = functionInfo
		}
		return debugInfoTable(runtime, info, what), nil
	}))
	debugTable.SetSymbol(runtime.InternSymbol("getlocal"), runtime.NewHostFunctionMulti("debug.getlocal", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		co, nextArg, err := debugCoroutineArg(runtime, args)
		if err != nil {
			return nil, err
		}
		if len(args)-nextArg != 2 {
			return nil, fmt.Errorf("debug.getlocal expects level and index")
		}
		if !args[nextArg].IsNumber() || !args[nextArg+1].IsNumber() {
			return nil, fmt.Errorf("debug.getlocal expects level and index numbers")
		}
		name, value, ok, err := machine.GetLocal(co, int(args[nextArg].Number()), int(args[nextArg+1].Number()))
		if err != nil {
			return nil, err
		}
		if !ok {
			return []rt.Value{rt.NilValue}, nil
		}
		return []rt.Value{runtime.StringValue(name), value}, nil
	}))
	debugTable.SetSymbol(runtime.InternSymbol("setlocal"), runtime.NewHostFunction("debug.setlocal", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		co, nextArg, err := debugCoroutineArg(runtime, args)
		if err != nil {
			return rt.NilValue, err
		}
		if len(args)-nextArg != 3 {
			return rt.NilValue, fmt.Errorf("debug.setlocal expects level, index, and value")
		}
		if !args[nextArg].IsNumber() || !args[nextArg+1].IsNumber() {
			return rt.NilValue, fmt.Errorf("debug.setlocal expects level and index numbers")
		}
		name, ok, err := machine.SetLocal(co, int(args[nextArg].Number()), int(args[nextArg+1].Number()), args[nextArg+2])
		if err != nil {
			return rt.NilValue, err
		}
		if !ok {
			return rt.NilValue, nil
		}
		return runtime.StringValue(name), nil
	}))
	debugTable.SetSymbol(runtime.InternSymbol("getupvalue"), runtime.NewHostFunctionMulti("debug.getupvalue", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("debug.getupvalue expects 2 arguments")
		}
		if !args[1].IsNumber() {
			return nil, fmt.Errorf("debug.getupvalue index expects number")
		}
		name, value, ok, err := machine.GetUpvalue(args[0], int(args[1].Number()))
		if err != nil {
			return nil, err
		}
		if !ok {
			return []rt.Value{rt.NilValue}, nil
		}
		return []rt.Value{runtime.StringValue(name), value}, nil
	}))
	debugTable.SetSymbol(runtime.InternSymbol("setupvalue"), runtime.NewHostFunction("debug.setupvalue", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 3 {
			return rt.NilValue, fmt.Errorf("debug.setupvalue expects 3 arguments")
		}
		if !args[1].IsNumber() {
			return rt.NilValue, fmt.Errorf("debug.setupvalue index expects number")
		}
		name, ok, err := machine.SetUpvalue(args[0], int(args[1].Number()), args[2])
		if err != nil {
			return rt.NilValue, err
		}
		if !ok {
			return rt.NilValue, nil
		}
		return runtime.StringValue(name), nil
	}))
	return nil
}

func debugCoroutineArg(runtime *rt.Runtime, args []rt.Value) (*vm.Coroutine, int, error) {
	if len(args) == 0 {
		return nil, 0, nil
	}
	co, err := asCoroutine(runtime, args[0])
	if err == nil {
		return co, 1, nil
	}
	return nil, 0, nil
}

func debugInfoTable(runtime *rt.Runtime, info vm.DebugInfo, what string) rt.Value {
	handle := runtime.Heap().NewTable(8)
	table := runtime.Heap().Table(handle)
	for _, option := range what {
		switch option {
		case 'L':
			if len(info.ActiveLines) == 0 {
				continue
			}
			activelines := runtime.Heap().NewTable(len(info.ActiveLines))
			activeTable := runtime.Heap().Table(activelines)
			for _, line := range info.ActiveLines {
				activeTable.RawSet(rt.NumberValue(float64(line)), rt.TrueValue)
			}
			table.SetSymbol(runtime.InternSymbol("activelines"), rt.HandleValue(activelines))
		case 'f':
			if info.Function.Kind() != rt.KindNil {
				table.SetSymbol(runtime.InternSymbol("func"), info.Function)
			}
		case 'l':
			table.SetSymbol(runtime.InternSymbol("currentline"), rt.NumberValue(float64(info.CurrentLine)))
		case 'n':
			if info.Name != "" {
				table.SetSymbol(runtime.InternSymbol("name"), runtime.StringValue(info.Name))
			}
			if info.NameWhat != "" {
				table.SetSymbol(runtime.InternSymbol("namewhat"), runtime.StringValue(info.NameWhat))
			}
		case 'S':
			table.SetSymbol(runtime.InternSymbol("source"), runtime.StringValue(info.Source))
			table.SetSymbol(runtime.InternSymbol("short_src"), runtime.StringValue(info.ShortSource))
			table.SetSymbol(runtime.InternSymbol("linedefined"), rt.NumberValue(float64(info.LineDefined)))
			table.SetSymbol(runtime.InternSymbol("lastlinedefined"), rt.NumberValue(float64(info.LastLineDefined)))
			table.SetSymbol(runtime.InternSymbol("what"), runtime.StringValue(info.What))
		case 'u':
			table.SetSymbol(runtime.InternSymbol("nups"), rt.NumberValue(float64(info.NumUpvalues)))
		}
	}
	return rt.HandleValue(handle)
}

func validateDebugInfoOptions(what string) error {
	for _, option := range what {
		switch option {
		case 'f', 'l', 'n', 'S', 'u', 'L':
		default:
			return fmt.Errorf("invalid option")
		}
	}
	return nil
}

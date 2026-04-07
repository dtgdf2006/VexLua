package stdlib

import (
	"fmt"
	"strconv"
	"strings"

	"vexlua/internal/bytecode"
	"vexlua/internal/chunk51"
	rt "vexlua/internal/runtime"
	"vexlua/internal/vm"
)

func registerBase(runtime *rt.Runtime, machine *vm.VM, compiler SourceCompiler) error {
	runtime.SetGlobal("print", runtime.NewHostFunction("print", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		parts := make([]string, 0, len(args))
		for _, arg := range args {
			text, err := luaToString(runtime, machine, arg)
			if err != nil {
				return rt.NilValue, err
			}
			parts = append(parts, text)
		}
		fmt.Println(strings.Join(parts, "\t"))
		return rt.NilValue, nil
	}))
	runtime.SetGlobal("assert", runtime.NewHostFunctionMulti("assert", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 || !isTruthy(args[0]) {
			message := runtime.StringValue("assertion failed!")
			if len(args) > 1 {
				message = args[1]
			}
			return nil, raiseValueError(runtime, message)
		}
		return append([]rt.Value(nil), args...), nil
	}))
	if err := bind(runtime, "type", func(value rt.Value) string {
		return typeName(runtime, value)
	}); err != nil {
		return err
	}
	runtime.SetGlobal("tostring", runtime.NewHostFunction("tostring", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("tostring expects 1 argument")
		}
		text, err := luaToString(runtime, machine, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		return runtime.StringValue(text), nil
	}))
	if err := bind(runtime, "tonumber", func(value rt.Value) (float64, error) {
		if value.IsNumber() {
			return value.Number(), nil
		}
		s, ok := runtime.ToString(value)
		if !ok {
			return 0, fmt.Errorf("tonumber expects string or number")
		}
		parsed, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	}); err != nil {
		return err
	}
	runtime.SetGlobal("getmetatable", runtime.NewHostFunction("getmetatable", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("getmetatable expects 1 argument")
		}
		meta, ok := runtime.GetMetatable(args[0])
		if !ok {
			return rt.NilValue, nil
		}
		if protected, found := rawMetafield(runtime, meta, "__metatable"); found {
			return protected, nil
		}
		return meta, nil
	}))
	runtime.SetGlobal("setmetatable", runtime.NewHostFunction("setmetatable", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 2 {
			return rt.NilValue, fmt.Errorf("setmetatable expects 2 arguments")
		}
		if meta, ok := runtime.GetMetatable(args[0]); ok {
			if _, protected := rawMetafield(runtime, meta, "__metatable"); protected {
				return rt.NilValue, fmt.Errorf("cannot change a protected metatable")
			}
		}
		if err := runtime.SetMetatable(args[0], args[1]); err != nil {
			return rt.NilValue, err
		}
		return args[0], nil
	}))
	runtime.SetGlobal("error", runtime.NewHostFunction("error", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		message := runtime.StringValue("error")
		if len(args) > 0 {
			message = args[0]
		}
		return rt.NilValue, raiseValueError(runtime, message)
	}))
	runtime.SetGlobal("pcall", runtime.NewHostFunctionMulti("pcall", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("pcall expects function")
		}
		results, err := machine.CallValue(args[0], args[1:])
		if err != nil {
			return []rt.Value{rt.FalseValue, errorToValue(runtime, err)}, nil
		}
		return append([]rt.Value{rt.TrueValue}, results...), nil
	}))
	runtime.SetGlobal("xpcall", runtime.NewHostFunctionMulti("xpcall", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("xpcall expects function and error handler")
		}
		results, err := machine.CallValue(args[0], nil)
		if err == nil {
			return append([]rt.Value{rt.TrueValue}, results...), nil
		}
		message := errorToValue(runtime, err)
		handled, handlerErr := machine.CallValue(args[1], []rt.Value{message})
		if handlerErr != nil {
			return []rt.Value{rt.FalseValue, errorToValue(runtime, handlerErr)}, nil
		}
		if len(handled) == 0 {
			return []rt.Value{rt.FalseValue, rt.NilValue}, nil
		}
		return []rt.Value{rt.FalseValue, handled[0]}, nil
	}))
	if err := bind(runtime, "rawget", func(target rt.Value, key rt.Value) (rt.Value, error) {
		value, found, err := rawTableGet(runtime, target, key)
		if err != nil {
			return rt.NilValue, err
		}
		if !found {
			return rt.NilValue, nil
		}
		return value, nil
	}); err != nil {
		return err
	}
	if err := bind(runtime, "rawset", func(target rt.Value, key rt.Value, value rt.Value) (rt.Value, error) {
		if err := rawTableSet(runtime, target, key, value); err != nil {
			return rt.NilValue, err
		}
		return target, nil
	}); err != nil {
		return err
	}
	if err := bind(runtime, "rawequal", func(lhs rt.Value, rhs rt.Value) bool {
		return lhs == rhs
	}); err != nil {
		return err
	}
	runtime.SetGlobal("select", runtime.NewHostFunctionMulti("select", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("select expects index")
		}
		if marker, ok := runtime.ToString(args[0]); ok {
			if marker != "#" {
				return nil, fmt.Errorf("select expects number or '#'")
			}
			return []rt.Value{rt.NumberValue(float64(len(args) - 1))}, nil
		}
		if !args[0].IsNumber() {
			return nil, fmt.Errorf("select expects number or '#'")
		}
		total := len(args) - 1
		index := int(args[0].Number())
		if index < 0 {
			index = total + index + 1
		}
		if index <= 0 {
			return nil, fmt.Errorf("select index out of range")
		}
		if index > total {
			return nil, nil
		}
		return append([]rt.Value(nil), args[index:]...), nil
	}))
	runtime.SetGlobal("unpack", runtime.NewHostFunctionMulti("unpack", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("unpack expects table")
		}
		table, err := asTable(runtime, args[0])
		if err != nil {
			return nil, err
		}
		start := 1
		if len(args) > 1 {
			if !args[1].IsNumber() {
				return nil, fmt.Errorf("unpack start expects number")
			}
			start = int(args[1].Number())
		}
		finish := table.Length()
		if len(args) > 2 {
			if !args[2].IsNumber() {
				return nil, fmt.Errorf("unpack end expects number")
			}
			finish = int(args[2].Number())
		}
		if start > finish {
			return nil, nil
		}
		results := make([]rt.Value, 0, finish-start+1)
		for index := start; index <= finish; index++ {
			value, found := table.RawGet(rt.NumberValue(float64(index)))
			if !found {
				value = rt.NilValue
			}
			results = append(results, value)
		}
		return results, nil
	}))
	loadString := runtime.NewHostFunction("loadstring", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("loadstring expects 1 argument")
		}
		source, ok := runtime.ToString(args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("loadstring expects string")
		}
		var (
			proto *bytecode.Proto
			err   error
		)
		if looksLikeChunkData(source) {
			proto, err = chunk51.Load(runtime, []byte(source))
		} else {
			proto, err = compiler.CompileSource(source)
		}
		if err != nil {
			return rt.NilValue, err
		}
		return machine.NewClosureValue(proto), nil
	})
	runtime.SetGlobal("loadstring", loadString)
	nextFunc := runtime.NewHostFunctionMulti("next", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("next expects table")
		}
		key := rt.NilValue
		if len(args) > 1 {
			key = args[1]
		}
		nextKey, nextValue, found, err := runtime.Next(args[0], key)
		if err != nil {
			return nil, err
		}
		if !found {
			return []rt.Value{rt.NilValue}, nil
		}
		return []rt.Value{nextKey, nextValue}, nil
	})
	runtime.SetGlobal("next", nextFunc)
	ipairsIter := runtime.NewHostFunctionMulti("ipairsaux", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("ipairs iterator expects 2 arguments")
		}
		index := 0
		if args[1].IsNumber() {
			index = int(args[1].Number())
		}
		index++
		value, found, err := runtime.GetTable(args[0], rt.NumberValue(float64(index)))
		if err != nil {
			return nil, err
		}
		if !found || value.Kind() == rt.KindNil {
			return []rt.Value{rt.NilValue}, nil
		}
		return []rt.Value{rt.NumberValue(float64(index)), value}, nil
	})
	runtime.SetGlobal("ipairs", runtime.NewHostFunctionMulti("ipairs", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("ipairs expects 1 argument")
		}
		return []rt.Value{ipairsIter, args[0], rt.NumberValue(0)}, nil
	}))
	runtime.SetGlobal("pairs", runtime.NewHostFunctionMulti("pairs", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("pairs expects 1 argument")
		}
		return []rt.Value{nextFunc, args[0], rt.NilValue}, nil
	}))
	getfenv := runtime.NewHostFunction("getfenv", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) == 0 {
			return machine.CurrentEnv(), nil
		}
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("getfenv expects 0 or 1 argument")
		}
		if args[0].IsNumber() {
			return machine.CurrentEnv(), nil
		}
		return machine.GetFunctionEnv(args[0])
	})
	runtime.SetGlobal("getfenv", getfenv)
	setfenv := runtime.NewHostFunction("setfenv", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 2 {
			return rt.NilValue, fmt.Errorf("setfenv expects 2 arguments")
		}
		if args[0].IsNumber() {
			if err := machine.SetCurrentEnv(args[1]); err != nil {
				return rt.NilValue, err
			}
			return args[0], nil
		}
		if err := machine.SetFunctionEnv(args[0], args[1]); err != nil {
			return rt.NilValue, err
		}
		return args[0], nil
	})
	runtime.SetGlobal("setfenv", setfenv)
	packageValue, packageTable, loadedTable, err := ensurePackageTables(runtime)
	if err != nil {
		return err
	}
	_, preloadTable, err := ensureSubtable(runtime, packageTable, "preload")
	if err != nil {
		return err
	}
	_, loadersTable, err := ensureSubtable(runtime, packageTable, "loaders")
	if err != nil {
		return err
	}
	preloadSearcher := runtime.NewHostFunctionMulti("package.preload_searcher", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("preload searcher expects module name")
		}
		name, ok := runtime.ToString(args[0])
		if !ok {
			return nil, fmt.Errorf("preload searcher expects string name")
		}
		sym := runtime.InternSymbol(name)
		if loader, _, found := preloadTable.GetSymbol(sym); found {
			return []rt.Value{loader}, nil
		}
		return []rt.Value{runtime.StringValue("\n\tno field package.preload['" + name + "']")}, nil
	})
	loadersTable.SetIndex(1, preloadSearcher)
	seeAll := runtime.NewHostFunction("package.seeall", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("package.seeall expects 1 argument")
		}
		meta := runtime.Heap().NewTable(1)
		runtime.Heap().Table(meta).SetSymbol(runtime.InternSymbol("__index"), machine.GlobalEnv())
		if err := runtime.SetMetatable(args[0], rt.HandleValue(meta)); err != nil {
			return rt.NilValue, err
		}
		return args[0], nil
	})
	packageTable.SetSymbol(runtime.InternSymbol("seeall"), seeAll)
	runtime.SetGlobal("package", packageValue)
	runtime.SetGlobal("require", runtime.NewHostFunction("require", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("require expects module name")
		}
		name, ok := runtime.ToString(args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("require expects string name")
		}
		sym := runtime.InternSymbol(name)
		if loaded, _, found := loadedTable.GetSymbol(sym); found && isTruthy(loaded) {
			return loaded, nil
		}
		messages := strings.Builder{}
		for index := 1; index <= loadersTable.Length(); index++ {
			searcher, found := loadersTable.GetIndex(index)
			if !found || searcher.Kind() == rt.KindNil {
				continue
			}
			results, err := machine.CallValue(searcher, []rt.Value{args[0]})
			if err != nil {
				return rt.NilValue, err
			}
			if len(results) == 0 || results[0].Kind() == rt.KindNil {
				continue
			}
			if text, ok := runtime.ToString(results[0]); ok {
				messages.WriteString(text)
				continue
			}
			loader := results[0]
			loadedResults, err := machine.CallValue(loader, []rt.Value{args[0]})
			if err != nil {
				return rt.NilValue, err
			}
			if len(loadedResults) > 0 && loadedResults[0].Kind() != rt.KindNil {
				loadedTable.SetSymbol(sym, loadedResults[0])
				return loadedResults[0], nil
			}
			if loaded, _, found := loadedTable.GetSymbol(sym); found && loaded.Kind() != rt.KindNil {
				return loaded, nil
			}
			loadedTable.SetSymbol(sym, rt.TrueValue)
			return rt.TrueValue, nil
		}
		return rt.NilValue, fmt.Errorf("module %s not found%s", name, messages.String())
	}))
	moduleFunc := runtime.NewHostFunction("module", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) == 0 {
			return rt.NilValue, fmt.Errorf("module expects name")
		}
		name, ok := runtime.ToString(args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("module expects string name")
		}
		moduleValue, moduleTable, err := ensureModuleTable(runtime, loadedTable, name)
		if err != nil {
			return rt.NilValue, err
		}
		moduleTable.SetSymbol(runtime.InternSymbol("_M"), moduleValue)
		moduleTable.SetSymbol(runtime.InternSymbol("_NAME"), runtime.StringValue(name))
		moduleTable.SetSymbol(runtime.InternSymbol("_PACKAGE"), runtime.StringValue(modulePackageName(name)))
		if err := machine.SetCurrentEnv(moduleValue); err != nil {
			return rt.NilValue, err
		}
		for _, option := range args[1:] {
			if _, err := machine.CallValue(option, []rt.Value{moduleValue}); err != nil {
				return rt.NilValue, err
			}
		}
		return moduleValue, nil
	})
	runtime.SetGlobal("module", moduleFunc)
	debugHandle := runtime.Heap().NewTable(4)
	debugTable := runtime.Heap().Table(debugHandle)
	debugTable.SetSymbol(runtime.InternSymbol("getfenv"), getfenv)
	debugTable.SetSymbol(runtime.InternSymbol("setfenv"), setfenv)
	runtime.SetGlobal("debug", rt.HandleValue(debugHandle))
	return nil
}

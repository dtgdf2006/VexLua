package stdlib

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"vexlua/internal/bytecode"
	"vexlua/internal/chunk51"
	rt "vexlua/internal/runtime"
	"vexlua/internal/vm"
)

func registerBase(runtime *rt.Runtime, machine *vm.VM, compiler SourceCompiler) error {
	runtime.SetGlobal("_G", rt.HandleValue(runtime.GlobalsHandle()))
	runtime.SetGlobal("_VERSION", runtime.StringValue("Lua 5.1"))
	runtime.SetGlobal("print", runtime.NewHostFunction("print", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		toString, _ := runtime.GetGlobalSymbol(runtime.InternSymbol("tostring"))
		parts := make([]string, 0, len(args))
		for _, arg := range args {
			results, err := machine.CallValue(toString, []rt.Value{arg})
			if err != nil {
				return rt.NilValue, err
			}
			if len(results) == 0 {
				return rt.NilValue, fmt.Errorf("'tostring' must return a string to 'print'")
			}
			text, ok := runtime.ToString(results[0])
			if !ok {
				return rt.NilValue, fmt.Errorf("'tostring' must return a string to 'print'")
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
	standardTonumber := func(value rt.Value) rt.Value {
		if value.IsNumber() {
			return value
		}
		s, ok := runtime.ToString(value)
		if !ok {
			return rt.NilValue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return rt.NilValue
		}
		return rt.NumberValue(parsed)
	}
	runtime.SetGlobal("tonumber", runtime.NewHostFunction("tonumber", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) == 0 || len(args) > 2 {
			return rt.NilValue, fmt.Errorf("tonumber expects 1 or 2 arguments")
		}
		if len(args) == 1 {
			return standardTonumber(args[0]), nil
		}
		if !args[1].IsNumber() {
			return rt.NilValue, fmt.Errorf("tonumber base expects number")
		}
		base := int(args[1].Number())
		if base == 10 {
			return standardTonumber(args[0]), nil
		}
		if base < 2 || base > 36 {
			return rt.NilValue, fmt.Errorf("base out of range")
		}
		s, ok := runtime.ToString(args[0])
		if !ok {
			return rt.NilValue, nil
		}
		parsed, err := strconv.ParseInt(strings.TrimSpace(s), base, 64)
		if err != nil {
			return rt.NilValue, nil
		}
		return rt.NumberValue(float64(parsed)), nil
	}))
	getmetatableFunc := runtime.NewHostFunction("getmetatable", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
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
	})
	runtime.SetGlobal("getmetatable", getmetatableFunc)
	setmetatableFunc := runtime.NewHostFunction("setmetatable", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
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
	})
	runtime.SetGlobal("setmetatable", setmetatableFunc)
	runtime.SetGlobal("error", runtime.NewHostFunction("error", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		message := runtime.StringValue("error")
		level := 1
		if len(args) > 0 {
			message = args[0]
		}
		if len(args) > 1 {
			if !args[1].IsNumber() {
				return rt.NilValue, fmt.Errorf("error level expects number")
			}
			level = int(args[1].Number())
		}
		return rt.NilValue, raiseBaseError(runtime, machine, message, level)
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
	validProxyMetas := make(map[rt.Handle]struct{})
	compileLoadedChunk := func(source string, name string) (rt.Value, rt.Value) {
		var (
			proto *bytecode.Proto
			err   error
		)
		if looksLikeChunkData(source) {
			proto, err = chunk51.Load(runtime, []byte(source))
		} else {
			proto, err = compiler.CompileSource(source)
			if err == nil && name != "" {
				proto.Name = name
				proto.SetSourceRecursive(name)
			}
		}
		if err != nil {
			return rt.NilValue, runtime.StringValue(err.Error())
		}
		return machine.NewClosureValue(proto), rt.NilValue
	}
	readLoadedChunk := func(filename *string) ([]byte, string, rt.Value) {
		if filename == nil {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, "", runtime.StringValue(err.Error())
			}
			return data, "=stdin", rt.NilValue
		}
		data, err := os.ReadFile(*filename)
		if err != nil {
			return nil, "", runtime.StringValue(err.Error())
		}
		sourceName := *filename
		if sourceName != "" && sourceName[0] != '@' && sourceName[0] != '=' {
			sourceName = "@" + sourceName
		}
		return data, sourceName, rt.NilValue
	}
	gcPause := 200
	gcStepMul := 200
	gcRunning := true
	runtime.SetGlobal("newproxy", runtime.NewHostFunction("newproxy", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) > 1 {
			return rt.NilValue, fmt.Errorf("newproxy expects 0 or 1 argument")
		}
		arg := rt.NilValue
		if len(args) == 1 {
			arg = args[0]
		}
		if arg.Kind() == rt.KindNil || (arg.Kind() == rt.KindBool && !arg.Bool()) {
			return runtime.NewUserdataValueWithEnv(nil, rt.NilValue, machine.CurrentEnv()), nil
		}
		if arg.Kind() == rt.KindBool {
			meta := runtime.Heap().NewTable(0)
			validProxyMetas[meta] = struct{}{}
			return runtime.NewUserdataValueWithEnv(nil, rt.HandleValue(meta), machine.CurrentEnv()), nil
		}
		h, ok := arg.Handle()
		if !ok || h.Kind() != rt.ObjectUserdata {
			return rt.NilValue, fmt.Errorf("boolean or proxy expected")
		}
		meta, hasMeta := runtime.GetMetatable(arg)
		if !hasMeta || meta.Kind() == rt.KindNil {
			return rt.NilValue, fmt.Errorf("boolean or proxy expected")
		}
		mh, ok := meta.Handle()
		if !ok || mh.Kind() != rt.ObjectTable {
			return rt.NilValue, fmt.Errorf("boolean or proxy expected")
		}
		if _, valid := validProxyMetas[mh]; !valid {
			return rt.NilValue, fmt.Errorf("boolean or proxy expected")
		}
		return runtime.NewUserdataValueWithEnv(nil, meta, machine.CurrentEnv()), nil
	}))
	runtime.SetGlobal("load", runtime.NewHostFunctionMulti("load", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 || len(args) > 2 {
			return nil, fmt.Errorf("load expects 1 or 2 arguments")
		}
		if typeName(runtime, args[0]) != "function" {
			return nil, fmt.Errorf("load expects reader function")
		}
		name := "=(load)"
		if len(args) == 2 && args[1].Kind() != rt.KindNil {
			text, ok := runtime.ToString(args[1])
			if !ok {
				return nil, fmt.Errorf("load chunk name expects string")
			}
			name = text
		}
		var builder strings.Builder
		for {
			results, err := machine.CallValue(args[0], nil)
			if err != nil {
				return []rt.Value{rt.NilValue, errorToValue(runtime, err)}, nil
			}
			if len(results) == 0 || results[0].Kind() == rt.KindNil {
				break
			}
			text, ok := concatString(runtime, results[0])
			if !ok {
				return []rt.Value{rt.NilValue, runtime.StringValue("reader function must return a string")}, nil
			}
			builder.WriteString(text)
		}
		chunk, errValue := compileLoadedChunk(builder.String(), name)
		if errValue.Kind() != rt.KindNil {
			return []rt.Value{rt.NilValue, errValue}, nil
		}
		return []rt.Value{chunk}, nil
	}))
	loadString := runtime.NewHostFunctionMulti("loadstring", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 || len(args) > 2 {
			return nil, fmt.Errorf("loadstring expects 1 or 2 arguments")
		}
		source, ok := runtime.ToString(args[0])
		if !ok {
			return nil, fmt.Errorf("loadstring expects string")
		}
		name := ""
		if len(args) == 2 && args[1].Kind() != rt.KindNil {
			text, ok := runtime.ToString(args[1])
			if !ok {
				return nil, fmt.Errorf("loadstring chunk name expects string")
			}
			name = text
		}
		chunk, errValue := compileLoadedChunk(source, name)
		if errValue.Kind() != rt.KindNil {
			return []rt.Value{rt.NilValue, errValue}, nil
		}
		return []rt.Value{chunk}, nil
	})
	runtime.SetGlobal("loadstring", loadString)
	runtime.SetGlobal("loadfile", runtime.NewHostFunctionMulti("loadfile", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) > 1 {
			return nil, fmt.Errorf("loadfile expects 0 or 1 argument")
		}
		var filename *string
		if len(args) == 1 && args[0].Kind() != rt.KindNil {
			text, ok := runtime.ToString(args[0])
			if !ok {
				return nil, fmt.Errorf("loadfile expects string filename")
			}
			filename = &text
		}
		data, sourceName, errValue := readLoadedChunk(filename)
		if errValue.Kind() != rt.KindNil {
			return []rt.Value{rt.NilValue, errValue}, nil
		}
		chunk, errValue := compileLoadedChunk(string(data), sourceName)
		if errValue.Kind() != rt.KindNil {
			return []rt.Value{rt.NilValue, errValue}, nil
		}
		return []rt.Value{chunk}, nil
	}))
	runtime.SetGlobal("dofile", runtime.NewHostFunctionMulti("dofile", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) > 1 {
			return nil, fmt.Errorf("dofile expects 0 or 1 argument")
		}
		var filename *string
		if len(args) == 1 && args[0].Kind() != rt.KindNil {
			text, ok := runtime.ToString(args[0])
			if !ok {
				return nil, fmt.Errorf("dofile expects string filename")
			}
			filename = &text
		}
		data, sourceName, errValue := readLoadedChunk(filename)
		if errValue.Kind() != rt.KindNil {
			return nil, raiseValueError(runtime, errValue)
		}
		chunk, errValue := compileLoadedChunk(string(data), sourceName)
		if errValue.Kind() != rt.KindNil {
			return nil, raiseValueError(runtime, errValue)
		}
		return machine.CallValue(chunk, nil)
	}))
	runtime.SetGlobal("collectgarbage", runtime.NewHostFunctionMulti("collectgarbage", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		option := "collect"
		if len(args) > 0 {
			text, ok := runtime.ToString(args[0])
			if !ok {
				return nil, fmt.Errorf("collectgarbage expects string option")
			}
			option = text
		}
		count := func() rt.Value {
			return rt.NumberValue(float64(runtime.ApproxMemoryBytes()) / 1024.0)
		}
		switch option {
		case "collect":
			if gcRunning {
				if err := machine.CollectGarbage(); err != nil {
					return nil, err
				}
			}
			return []rt.Value{rt.NumberValue(0)}, nil
		case "count":
			return []rt.Value{count()}, nil
		case "step":
			if !gcRunning {
				return []rt.Value{rt.FalseValue}, nil
			}
			if err := machine.CollectGarbage(); err != nil {
				return nil, err
			}
			return []rt.Value{rt.TrueValue}, nil
		case "stop":
			gcRunning = false
			return []rt.Value{rt.NumberValue(0)}, nil
		case "restart":
			gcRunning = true
			return []rt.Value{rt.NumberValue(0)}, nil
		case "setpause":
			previous := gcPause
			if len(args) > 1 {
				if !args[1].IsNumber() {
					return nil, fmt.Errorf("collectgarbage setpause expects number")
				}
				gcPause = int(args[1].Number())
			}
			return []rt.Value{rt.NumberValue(float64(previous))}, nil
		case "setstepmul":
			previous := gcStepMul
			if len(args) > 1 {
				if !args[1].IsNumber() {
					return nil, fmt.Errorf("collectgarbage setstepmul expects number")
				}
				gcStepMul = int(args[1].Number())
			}
			return []rt.Value{rt.NumberValue(float64(previous))}, nil
		default:
			return nil, fmt.Errorf("invalid collectgarbage option %q", option)
		}
	}))
	runtime.SetGlobal("gcinfo", runtime.NewHostFunction("gcinfo", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 0 {
			return rt.NilValue, fmt.Errorf("gcinfo expects no arguments")
		}
		return rt.NumberValue(float64(runtime.ApproxMemoryBytes()) / 1024.0), nil
	}))
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
		target, threadEnv, err := resolveBaseFenvTarget(machine, args, true)
		if err != nil {
			return rt.NilValue, err
		}
		if threadEnv {
			return machine.CurrentThreadEnv(), nil
		}
		return machine.GetFunctionEnv(target)
	})
	runtime.SetGlobal("getfenv", getfenv)
	setfenv := runtime.NewHostFunctionMulti("setfenv", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("setfenv expects 2 arguments")
		}
		target, threadEnv, err := resolveBaseFenvTarget(machine, args[:1], false)
		if err != nil {
			return nil, err
		}
		if threadEnv {
			if err := machine.SetCurrentThreadEnv(args[1]); err != nil {
				return nil, err
			}
			return nil, nil
		}
		if h, ok := target.Handle(); ok && h.Kind() == rt.ObjectHostFunction {
			return nil, fmt.Errorf("setfenv cannot change environment of given object")
		}
		if err := machine.SetFunctionEnv(target, args[1]); err != nil {
			return nil, err
		}
		return []rt.Value{target}, nil
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
	packageTable.SetSymbol(runtime.InternSymbol("path"), runtime.StringValue(configuredPackagePath("LUA_PATH", defaultLuaPackagePath())))
	packageTable.SetSymbol(runtime.InternSymbol("cpath"), runtime.StringValue(configuredPackagePath("LUA_CPATH", defaultCPackagePath())))
	packageTable.SetSymbol(runtime.InternSymbol("config"), runtime.StringValue(packageConfigString()))
	loadingSentinel := runtime.NewTableValue(0)
	packageLoadlib := runtime.NewHostFunctionMulti("package.loadlib", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("package.loadlib expects library name and init function")
		}
		libname, ok := runtime.ToString(args[0])
		if !ok {
			return nil, fmt.Errorf("package.loadlib library name expects string")
		}
		if _, ok := runtime.ToString(args[1]); !ok {
			return nil, fmt.Errorf("package.loadlib init function expects string")
		}
		if _, err := os.Stat(libname); err != nil {
			return []rt.Value{rt.NilValue, runtime.StringValue(err.Error()), runtime.StringValue("open")}, nil
		}
		return []rt.Value{rt.NilValue, runtime.StringValue("native Lua C modules are not supported in vexlua"), runtime.StringValue("open")}, nil
	})
	packageTable.SetSymbol(runtime.InternSymbol("loadlib"), packageLoadlib)
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
	luaSearcher := runtime.NewHostFunctionMulti("package.lua_searcher", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("lua searcher expects module name")
		}
		name, ok := runtime.ToString(args[0])
		if !ok {
			return nil, fmt.Errorf("lua searcher expects string name")
		}
		pathValue, _, found := packageTable.GetSymbol(runtime.InternSymbol("path"))
		if !found {
			return nil, fmt.Errorf("package.path must be a string")
		}
		pathText, ok := runtime.ToString(pathValue)
		if !ok {
			return nil, fmt.Errorf("package.path must be a string")
		}
		filename, messages := packageSearchPath(pathText, name)
		if filename == "" {
			return []rt.Value{runtime.StringValue(messages)}, nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return []rt.Value{runtime.StringValue("\n\tno file '" + filename + "'")}, nil
		}
		chunk, errValue := compileLoadedChunk(string(data), "@"+filename)
		if errValue.Kind() != rt.KindNil {
			text, err := plainString(runtime, errValue)
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("error loading module %q from file %q:\n\t%s", name, filename, text)
		}
		return []rt.Value{chunk}, nil
	})
	cSearcher := runtime.NewHostFunctionMulti("package.c_searcher", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("c searcher expects module name")
		}
		name, ok := runtime.ToString(args[0])
		if !ok {
			return nil, fmt.Errorf("c searcher expects string name")
		}
		cpathValue, _, found := packageTable.GetSymbol(runtime.InternSymbol("cpath"))
		if !found {
			return nil, fmt.Errorf("package.cpath must be a string")
		}
		cpathText, ok := runtime.ToString(cpathValue)
		if !ok {
			return nil, fmt.Errorf("package.cpath must be a string")
		}
		filename, messages := packageSearchPath(cpathText, name)
		if filename == "" {
			return []rt.Value{runtime.StringValue(messages)}, nil
		}
		results, err := machine.CallValue(packageLoadlib, []rt.Value{runtime.StringValue(filename), runtime.StringValue(packageLoaderFuncName(name))})
		if err != nil {
			return nil, err
		}
		if len(results) > 0 && results[0].Kind() != rt.KindNil {
			return []rt.Value{results[0]}, nil
		}
		message := "native Lua C modules are not supported in vexlua"
		if len(results) > 1 && results[1].Kind() != rt.KindNil {
			text, err := plainString(runtime, results[1])
			if err != nil {
				return nil, err
			}
			message = text
		}
		return nil, fmt.Errorf("error loading module %q from file %q:\n\t%s", name, filename, message)
	})
	cRootSearcher := runtime.NewHostFunctionMulti("package.croot_searcher", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("c root searcher expects module name")
		}
		name, ok := runtime.ToString(args[0])
		if !ok {
			return nil, fmt.Errorf("c root searcher expects string name")
		}
		dot := strings.Index(name, ".")
		if dot < 0 {
			return nil, nil
		}
		root := name[:dot]
		cpathValue, _, found := packageTable.GetSymbol(runtime.InternSymbol("cpath"))
		if !found {
			return nil, fmt.Errorf("package.cpath must be a string")
		}
		cpathText, ok := runtime.ToString(cpathValue)
		if !ok {
			return nil, fmt.Errorf("package.cpath must be a string")
		}
		filename, messages := packageSearchPath(cpathText, root)
		if filename == "" {
			return []rt.Value{runtime.StringValue(messages)}, nil
		}
		results, err := machine.CallValue(packageLoadlib, []rt.Value{runtime.StringValue(filename), runtime.StringValue(packageLoaderFuncName(name))})
		if err != nil {
			return nil, err
		}
		if len(results) > 0 && results[0].Kind() != rt.KindNil {
			return []rt.Value{results[0]}, nil
		}
		message := "native Lua C modules are not supported in vexlua"
		if len(results) > 1 && results[1].Kind() != rt.KindNil {
			text, err := plainString(runtime, results[1])
			if err != nil {
				return nil, err
			}
			message = text
		}
		return nil, fmt.Errorf("error loading module %q from file %q:\n\t%s", name, filename, message)
	})
	loadersTable.SetIndex(1, preloadSearcher)
	loadersTable.SetIndex(2, luaSearcher)
	loadersTable.SetIndex(3, cSearcher)
	loadersTable.SetIndex(4, cRootSearcher)
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
	loadedTable.SetSymbol(runtime.InternSymbol("package"), packageValue)
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
			if loaded == loadingSentinel {
				return rt.NilValue, fmt.Errorf("loop or previous error loading module %q", name)
			}
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
			loadedTable.SetSymbol(sym, loadingSentinel)
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
	debugTable.SetSymbol(runtime.InternSymbol("getmetatable"), runtime.NewHostFunction("debug.getmetatable", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("debug.getmetatable expects 1 argument")
		}
		meta, ok := runtime.GetMetatable(args[0])
		if !ok {
			return rt.NilValue, nil
		}
		return meta, nil
	}))
	debugTable.SetSymbol(runtime.InternSymbol("setmetatable"), runtime.NewHostFunction("debug.setmetatable", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 2 {
			return rt.NilValue, fmt.Errorf("debug.setmetatable expects 2 arguments")
		}
		if err := runtime.SetAnyMetatable(args[0], args[1]); err != nil {
			return rt.NilValue, err
		}
		return args[0], nil
	}))
	runtime.SetGlobal("debug", rt.HandleValue(debugHandle))
	return nil
}

func raiseBaseError(runtime *rt.Runtime, machine *vm.VM, message rt.Value, level int) error {
	if level > 0 {
		if text, ok := concatString(runtime, message); ok {
			if prefix := whereString(machine, level); prefix != "" {
				message = runtime.StringValue(prefix + text)
			}
		}
	}
	return raiseValueError(runtime, message)
}

func whereString(machine *vm.VM, level int) string {
	info, ok := machine.DebugInfoForLevel(nil, level)
	if !ok || info.CurrentLine <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d: ", info.ShortSource, info.CurrentLine)
}

func resolveBaseFenvTarget(machine *vm.VM, args []rt.Value, optional bool) (rt.Value, bool, error) {
	if len(args) == 0 {
		if !optional {
			return rt.NilValue, false, fmt.Errorf("function or level expected")
		}
		value, tailCall, err := machine.FunctionValueForLevel(nil, 1)
		if err != nil {
			return rt.NilValue, false, err
		}
		if tailCall {
			return rt.NilValue, false, fmt.Errorf("no function environment for tail call at level %d", 1)
		}
		return value, false, nil
	}
	if len(args) != 1 {
		return rt.NilValue, false, fmt.Errorf("function or level expected")
	}
	arg := args[0]
	if arg.IsNumber() {
		level := int(arg.Number())
		if level < 0 {
			return rt.NilValue, false, fmt.Errorf("level must be non-negative")
		}
		if level == 0 {
			return rt.NilValue, true, nil
		}
		value, tailCall, err := machine.FunctionValueForLevel(nil, level)
		if err != nil {
			return rt.NilValue, false, err
		}
		if tailCall {
			return rt.NilValue, false, fmt.Errorf("no function environment for tail call at level %d", level)
		}
		return value, false, nil
	}
	h, ok := arg.Handle()
	if !ok || (h.Kind() != rt.ObjectLuaClosure && h.Kind() != rt.ObjectHostFunction) {
		return rt.NilValue, false, fmt.Errorf("function or level expected")
	}
	return arg, false, nil
}

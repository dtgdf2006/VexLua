package stdlib

import (
	"fmt"

	rt "vexlua/internal/runtime"
	"vexlua/internal/vm"
)

func registerCoroutine(runtime *rt.Runtime, machine *vm.VM) error {
	handle := runtime.Heap().NewTable(8)
	table := runtime.Heap().Table(handle)
	createFunc := runtime.NewHostFunction("coroutine.create", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("coroutine.create expects 1 argument")
		}
		co, err := machine.NewCoroutine(args[0])
		if err != nil {
			return rt.NilValue, err
		}
		return coroutineValue(runtime, co), nil
	})
	table.SetSymbol(runtime.InternSymbol("create"), createFunc)
	runningFunc := runtime.NewHostFunction("coroutine.running", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 0 {
			return rt.NilValue, fmt.Errorf("coroutine.running expects no arguments")
		}
		co := machine.RunningCoroutine()
		if co == nil {
			return rt.NilValue, nil
		}
		return coroutineValue(runtime, co), nil
	})
	table.SetSymbol(runtime.InternSymbol("running"), runningFunc)
	resumeFunc := runtime.NewHostFunctionMulti("coroutine.resume", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("coroutine.resume expects coroutine")
		}
		co, err := asCoroutine(runtime, args[0])
		if err != nil {
			return nil, err
		}
		results, err := machine.ResumeCoroutineMulti(co, args[1:])
		if err != nil {
			return []rt.Value{rt.FalseValue, errorToValue(runtime, err)}, nil
		}
		return append([]rt.Value{rt.TrueValue}, results...), nil
	})
	table.SetSymbol(runtime.InternSymbol("resume"), resumeFunc)
	statusFunc := runtime.NewHostFunction("coroutine.status", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("coroutine.status expects 1 argument")
		}
		co, err := asCoroutine(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		return runtime.StringValue(machine.CoroutineStatus(co)), nil
	})
	table.SetSymbol(runtime.InternSymbol("status"), statusFunc)
	wrapFunc := runtime.NewHostFunction("coroutine.wrap", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("coroutine.wrap expects 1 argument")
		}
		co, err := machine.NewCoroutine(args[0])
		if err != nil {
			return rt.NilValue, err
		}
		return runtime.NewHostFunctionMulti("coroutine.wrap.fn", func(runtime *rt.Runtime, innerArgs []rt.Value) ([]rt.Value, error) {
			return machine.ResumeCoroutineMulti(co, innerArgs)
		}), nil
	})
	table.SetSymbol(runtime.InternSymbol("wrap"), wrapFunc)
	yieldFunc := runtime.NewHostFunctionMulti("coroutine.yield", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		return append([]rt.Value(nil), args...), nil
	})
	table.SetSymbol(runtime.InternSymbol("yield"), yieldFunc)
	runtime.SetGlobal("coroutine", rt.HandleValue(handle))
	return nil
}

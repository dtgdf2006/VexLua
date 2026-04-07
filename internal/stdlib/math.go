package stdlib

import rt "vexlua/internal/runtime"

func registerMath(runtime *rt.Runtime) error {
	handle := runtime.Heap().NewTable(8)
	table := runtime.Heap().Table(handle)
	if err := setTableFunc(runtime, table, "abs", func(v float64) float64 { return abs(v) }); err != nil {
		return err
	}
	if err := setTableFunc(runtime, table, "max", func(a, b float64) float64 {
		if a > b {
			return a
		}
		return b
	}); err != nil {
		return err
	}
	if err := setTableFunc(runtime, table, "min", func(a, b float64) float64 {
		if a < b {
			return a
		}
		return b
	}); err != nil {
		return err
	}
	runtime.SetGlobal("math", rt.HandleValue(handle))
	return nil
}

package stdlib

import (
	"fmt"
	"strings"
	"sync"

	rt "vexlua/internal/runtime"
	"vexlua/internal/vm"
)

var tableSortValuePool sync.Pool

func borrowTableSortValues(size int) []rt.Value {
	if cached := tableSortValuePool.Get(); cached != nil {
		values := cached.([]rt.Value)
		if cap(values) >= size {
			return values[:size]
		}
	}
	return make([]rt.Value, size)
}

func releaseTableSortValues(values []rt.Value) {
	clear(values)
	tableSortValuePool.Put(values[:0])
}

func registerTable(runtime *rt.Runtime, machine *vm.VM) error {
	handle := runtime.Heap().NewTable(8)
	table := runtime.Heap().Table(handle)
	newFunc := runtime.NewHostFunction("table.new", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		return rt.HandleValue(runtime.Heap().NewTable(4)), nil
	})
	table.SetSymbol(runtime.InternSymbol("new"), newFunc)
	getFunc := runtime.NewHostFunction("table.get", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 2 {
			return rt.NilValue, fmt.Errorf("table.get expects 2 arguments")
		}
		value, found, err := rawTableGet(runtime, args[0], args[1])
		if err != nil {
			return rt.NilValue, err
		}
		if !found {
			return rt.NilValue, nil
		}
		return value, nil
	})
	table.SetSymbol(runtime.InternSymbol("get"), getFunc)
	setFunc := runtime.NewHostFunction("table.set", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 3 {
			return rt.NilValue, fmt.Errorf("table.set expects 3 arguments")
		}
		if err := rawTableSet(runtime, args[0], args[1], args[2]); err != nil {
			return rt.NilValue, err
		}
		return args[2], nil
	})
	table.SetSymbol(runtime.InternSymbol("set"), setFunc)
	insertFunc := runtime.NewHostFunction("table.insert", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return rt.NilValue, fmt.Errorf("table.insert expects 2 or 3 arguments")
		}
		tbl, err := asTable(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		pos := tbl.Length() + 1
		value := args[1]
		if len(args) == 3 {
			if !args[1].IsNumber() {
				return rt.NilValue, fmt.Errorf("table.insert position expects number")
			}
			pos = int(args[1].Number())
			value = args[2]
		}
		length := tbl.Length()
		if pos < 1 || pos > length+1 {
			return rt.NilValue, fmt.Errorf("table.insert position out of bounds")
		}
		for index := length; index >= pos; index-- {
			current, _ := tbl.GetIndex(index)
			tbl.SetIndex(index+1, current)
		}
		tbl.SetIndex(pos, value)
		return rt.NilValue, nil
	})
	table.SetSymbol(runtime.InternSymbol("insert"), insertFunc)
	removeFunc := runtime.NewHostFunction("table.remove", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) < 1 || len(args) > 2 {
			return rt.NilValue, fmt.Errorf("table.remove expects 1 or 2 arguments")
		}
		tbl, err := asTable(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		length := tbl.Length()
		if length == 0 {
			return rt.NilValue, nil
		}
		pos := length
		if len(args) == 2 {
			if !args[1].IsNumber() {
				return rt.NilValue, fmt.Errorf("table.remove position expects number")
			}
			pos = int(args[1].Number())
		}
		if pos < 1 || pos > length {
			return rt.NilValue, nil
		}
		removed, _ := tbl.GetIndex(pos)
		for index := pos; index < length; index++ {
			current, _ := tbl.GetIndex(index + 1)
			tbl.SetIndex(index, current)
		}
		tbl.SetIndex(length, rt.NilValue)
		return removed, nil
	})
	table.SetSymbol(runtime.InternSymbol("remove"), removeFunc)
	getnFunc := runtime.NewHostFunction("table.getn", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("table.getn expects 1 argument")
		}
		tbl, err := asTable(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		return rt.NumberValue(float64(tbl.Length())), nil
	})
	table.SetSymbol(runtime.InternSymbol("getn"), getnFunc)
	setnFunc := runtime.NewHostFunction("table.setn", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		return rt.NilValue, fmt.Errorf("'setn' is obsolete")
	})
	table.SetSymbol(runtime.InternSymbol("setn"), setnFunc)
	concatFunc := runtime.NewHostFunction("table.concat", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) < 1 || len(args) > 4 {
			return rt.NilValue, fmt.Errorf("table.concat expects 1 to 4 arguments")
		}
		tbl, err := asTable(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		sep := ""
		if len(args) >= 2 {
			parsedSep, ok := runtime.ToString(args[1])
			if !ok {
				return rt.NilValue, fmt.Errorf("table.concat separator expects string")
			}
			sep = parsedSep
		}
		start := 1
		finish := tbl.Length()
		if len(args) >= 3 {
			if !args[2].IsNumber() {
				return rt.NilValue, fmt.Errorf("table.concat start expects number")
			}
			start = int(args[2].Number())
		}
		if len(args) == 4 {
			if !args[3].IsNumber() {
				return rt.NilValue, fmt.Errorf("table.concat end expects number")
			}
			finish = int(args[3].Number())
		}
		if start > finish {
			return runtime.StringValue(""), nil
		}
		parts := make([]string, 0, finish-start+1)
		for index := start; index <= finish; index++ {
			value, found := tbl.RawGet(rt.NumberValue(float64(index)))
			if !found || value.Kind() == rt.KindNil {
				return rt.NilValue, fmt.Errorf("table.concat encountered nil")
			}
			part, ok := concatString(runtime, value)
			if !ok {
				return rt.NilValue, fmt.Errorf("table.concat expects strings or numbers")
			}
			parts = append(parts, part)
		}
		return runtime.StringValue(strings.Join(parts, sep)), nil
	})
	table.SetSymbol(runtime.InternSymbol("concat"), concatFunc)
	maxnFunc := runtime.NewHostFunction("table.maxn", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("table.maxn expects 1 argument")
		}
		tbl, err := asTable(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		return rt.NumberValue(tbl.MaxNumericKey()), nil
	})
	table.SetSymbol(runtime.InternSymbol("maxn"), maxnFunc)
	sortFunc := runtime.NewHostFunction("table.sort", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) < 1 || len(args) > 2 {
			return rt.NilValue, fmt.Errorf("table.sort expects 1 or 2 arguments")
		}
		tbl, err := asTable(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		length := tbl.Length()
		if length < 2 {
			return rt.NilValue, nil
		}
		values := borrowTableSortValues(length)
		defer releaseTableSortValues(values)
		for index := 1; index <= length; index++ {
			value, _ := tbl.GetIndex(index)
			values[index-1] = value
		}
		var less func(rt.Value, rt.Value) (bool, error)
		if len(args) == 2 {
			cmp := args[1]
			var cmpArgs [2]rt.Value
			less = func(left rt.Value, right rt.Value) (bool, error) {
				cmpArgs[0] = left
				cmpArgs[1] = right
				results, err := machine.CallValue(cmp, cmpArgs[:])
				if err != nil {
					return false, err
				}
				if len(results) == 0 {
					return false, nil
				}
				return isTruthy(results[0]), nil
			}
		} else {
			less = machine.Less
		}
		for i := 1; i < len(values); i++ {
			current := values[i]
			j := i
			for j > 0 {
				ordered, err := less(current, values[j-1])
				if err != nil {
					return rt.NilValue, err
				}
				if !ordered {
					break
				}
				values[j] = values[j-1]
				j--
			}
			values[j] = current
		}
		for index, value := range values {
			tbl.SetIndex(index+1, value)
		}
		return rt.NilValue, nil
	})
	table.SetSymbol(runtime.InternSymbol("sort"), sortFunc)
	foreachFunc := runtime.NewHostFunction("table.foreach", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 2 {
			return rt.NilValue, fmt.Errorf("table.foreach expects 2 arguments")
		}
		if _, err := asTable(runtime, args[0]); err != nil {
			return rt.NilValue, err
		}
		key := rt.NilValue
		var callArgs [2]rt.Value
		for {
			nextKey, nextValue, found, err := runtime.Next(args[0], key)
			if err != nil {
				return rt.NilValue, err
			}
			if !found {
				return rt.NilValue, nil
			}
			callArgs[0] = nextKey
			callArgs[1] = nextValue
			results, err := machine.CallValue(args[1], callArgs[:])
			if err != nil {
				return rt.NilValue, err
			}
			if len(results) > 0 && results[0].Kind() != rt.KindNil {
				return results[0], nil
			}
			key = nextKey
		}
	})
	table.SetSymbol(runtime.InternSymbol("foreach"), foreachFunc)
	foreachiFunc := runtime.NewHostFunction("table.foreachi", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 2 {
			return rt.NilValue, fmt.Errorf("table.foreachi expects 2 arguments")
		}
		tbl, err := asTable(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		var callArgs [2]rt.Value
		for index := 1; index <= tbl.Length(); index++ {
			value, _ := tbl.GetIndex(index)
			callArgs[0] = rt.NumberValue(float64(index))
			callArgs[1] = value
			results, err := machine.CallValue(args[1], callArgs[:])
			if err != nil {
				return rt.NilValue, err
			}
			if len(results) > 0 && results[0].Kind() != rt.KindNil {
				return results[0], nil
			}
		}
		return rt.NilValue, nil
	})
	table.SetSymbol(runtime.InternSymbol("foreachi"), foreachiFunc)
	if unpack, ok := runtime.GetGlobalSymbol(runtime.InternSymbol("unpack")); ok {
		table.SetSymbol(runtime.InternSymbol("unpack"), unpack)
	}
	runtime.SetGlobal("table", rt.HandleValue(handle))
	return nil
}

package stdlib

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	rt "vexlua/internal/runtime"
)

func registerMath(runtime *rt.Runtime) error {
	handle := runtime.Heap().NewTable(32)
	table := runtime.Heap().Table(handle)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	checkNumber := func(args []rt.Value, index int, name string) (float64, error) {
		if index >= len(args) || !args[index].IsNumber() {
			return 0, fmt.Errorf("%s expects number", name)
		}
		return args[index].Number(), nil
	}
	setUnary := func(name string, fn func(float64) float64) {
		table.SetSymbol(runtime.InternSymbol(name), runtime.NewHostFunction("math."+name, func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
			if len(args) != 1 {
				return rt.NilValue, fmt.Errorf("math.%s expects 1 argument", name)
			}
			value, err := checkNumber(args, 0, "math."+name)
			if err != nil {
				return rt.NilValue, err
			}
			return rt.NumberValue(fn(value)), nil
		}))
	}
	setBinary := func(name string, fn func(float64, float64) float64) {
		table.SetSymbol(runtime.InternSymbol(name), runtime.NewHostFunction("math."+name, func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
			if len(args) != 2 {
				return rt.NilValue, fmt.Errorf("math.%s expects 2 arguments", name)
			}
			left, err := checkNumber(args, 0, "math."+name)
			if err != nil {
				return rt.NilValue, err
			}
			right, err := checkNumber(args, 1, "math."+name)
			if err != nil {
				return rt.NilValue, err
			}
			return rt.NumberValue(fn(left, right)), nil
		}))
	}
	setUnary("abs", math.Abs)
	setUnary("acos", math.Acos)
	setUnary("asin", math.Asin)
	setUnary("atan", math.Atan)
	setUnary("ceil", math.Ceil)
	setUnary("cos", math.Cos)
	setUnary("cosh", math.Cosh)
	setUnary("exp", math.Exp)
	setUnary("floor", math.Floor)
	setUnary("log", math.Log)
	setUnary("log10", math.Log10)
	setUnary("sin", math.Sin)
	setUnary("sinh", math.Sinh)
	setUnary("sqrt", math.Sqrt)
	setUnary("tan", math.Tan)
	setUnary("tanh", math.Tanh)
	setUnary("deg", func(v float64) float64 { return v * 180 / math.Pi })
	setUnary("rad", func(v float64) float64 { return v * math.Pi / 180 })
	setBinary("atan2", math.Atan2)
	setBinary("fmod", math.Mod)
	setBinary("ldexp", func(a, b float64) float64 { return math.Ldexp(a, int(b)) })
	setBinary("pow", math.Pow)
	table.SetSymbol(runtime.InternSymbol("frexp"), runtime.NewHostFunctionMulti("math.frexp", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("math.frexp expects 1 argument")
		}
		value, err := checkNumber(args, 0, "math.frexp")
		if err != nil {
			return nil, err
		}
		frac, exp := math.Frexp(value)
		return []rt.Value{rt.NumberValue(frac), rt.NumberValue(float64(exp))}, nil
	}))
	table.SetSymbol(runtime.InternSymbol("modf"), runtime.NewHostFunctionMulti("math.modf", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("math.modf expects 1 argument")
		}
		value, err := checkNumber(args, 0, "math.modf")
		if err != nil {
			return nil, err
		}
		intPart, frac := math.Modf(value)
		return []rt.Value{rt.NumberValue(intPart), rt.NumberValue(frac)}, nil
	}))
	table.SetSymbol(runtime.InternSymbol("max"), runtime.NewHostFunction("math.max", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) == 0 {
			return rt.NilValue, fmt.Errorf("math.max expects at least 1 argument")
		}
		best, err := checkNumber(args, 0, "math.max")
		if err != nil {
			return rt.NilValue, err
		}
		for index := 1; index < len(args); index++ {
			candidate, err := checkNumber(args, index, "math.max")
			if err != nil {
				return rt.NilValue, err
			}
			if candidate > best {
				best = candidate
			}
		}
		return rt.NumberValue(best), nil
	}))
	table.SetSymbol(runtime.InternSymbol("min"), runtime.NewHostFunction("math.min", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) == 0 {
			return rt.NilValue, fmt.Errorf("math.min expects at least 1 argument")
		}
		best, err := checkNumber(args, 0, "math.min")
		if err != nil {
			return rt.NilValue, err
		}
		for index := 1; index < len(args); index++ {
			candidate, err := checkNumber(args, index, "math.min")
			if err != nil {
				return rt.NilValue, err
			}
			if candidate < best {
				best = candidate
			}
		}
		return rt.NumberValue(best), nil
	}))
	table.SetSymbol(runtime.InternSymbol("random"), runtime.NewHostFunction("math.random", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		switch len(args) {
		case 0:
			return rt.NumberValue(rng.Float64()), nil
		case 1:
			upper, err := checkNumber(args, 0, "math.random")
			if err != nil {
				return rt.NilValue, err
			}
			u := int(upper)
			if u < 1 {
				return rt.NilValue, fmt.Errorf("interval is empty")
			}
			return rt.NumberValue(float64(rng.Intn(u) + 1)), nil
		case 2:
			lower, err := checkNumber(args, 0, "math.random")
			if err != nil {
				return rt.NilValue, err
			}
			upper, err := checkNumber(args, 1, "math.random")
			if err != nil {
				return rt.NilValue, err
			}
			l := int(lower)
			u := int(upper)
			if l > u {
				return rt.NilValue, fmt.Errorf("interval is empty")
			}
			return rt.NumberValue(float64(rng.Intn(u-l+1) + l)), nil
		default:
			return rt.NilValue, fmt.Errorf("wrong number of arguments")
		}
	}))
	table.SetSymbol(runtime.InternSymbol("randomseed"), runtime.NewHostFunction("math.randomseed", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("math.randomseed expects 1 argument")
		}
		seed, err := checkNumber(args, 0, "math.randomseed")
		if err != nil {
			return rt.NilValue, err
		}
		rng.Seed(int64(seed))
		return rt.NilValue, nil
	}))
	table.SetSymbol(runtime.InternSymbol("pi"), rt.NumberValue(math.Pi))
	table.SetSymbol(runtime.InternSymbol("huge"), rt.NumberValue(math.Inf(1)))
	fmodValue, _, _ := table.GetSymbol(runtime.InternSymbol("fmod"))
	table.SetSymbol(runtime.InternSymbol("mod"), fmodValue)
	runtime.SetGlobal("math", rt.HandleValue(handle))
	return nil
}

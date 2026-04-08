package runtime

import (
	"fmt"
	"reflect"
)

type HostCall func(*Runtime, []Value) (Value, error)

type HostCallMulti func(*Runtime, []Value) ([]Value, error)

type HostFunction struct {
	Name      string
	Call      HostCall
	CallMulti HostCallMulti
	Roots     GCRootSource
}

type HostAdapter interface {
	GetField(rt *Runtime, subject any, name string) (Value, bool, error)
	SetField(rt *Runtime, subject any, name string, value Value) error
}

type HostProxy struct {
	Name    string
	Subject any
	Adapter HostAdapter
	Meta    Value
	Env     Value
}

func (rt *Runtime) NewHostFunction(name string, fn HostCall) Value {
	return rt.NewHostFunctionWithRoots(name, nil, fn)
}

func (rt *Runtime) NewHostFunctionWithRoots(name string, roots GCRootSource, fn HostCall) Value {
	return HandleValue(rt.heap.NewHostFunction(HostFunction{
		Name:  name,
		Roots: roots,
		Call:  fn,
		CallMulti: func(runtime *Runtime, args []Value) ([]Value, error) {
			result, err := fn(runtime, args)
			if err != nil {
				return nil, err
			}
			return []Value{result}, nil
		},
	}))
}

func (rt *Runtime) NewHostFunctionMulti(name string, fn HostCallMulti) Value {
	return rt.NewHostFunctionMultiWithRoots(name, nil, fn)
}

func (rt *Runtime) NewHostFunctionMultiWithRoots(name string, roots GCRootSource, fn HostCallMulti) Value {
	return HandleValue(rt.heap.NewHostFunction(HostFunction{
		Name:  name,
		Roots: roots,
		Call: func(runtime *Runtime, args []Value) (Value, error) {
			results, err := fn(runtime, args)
			if err != nil {
				return NilValue, err
			}
			if len(results) == 0 {
				return NilValue, nil
			}
			return results[0], nil
		},
		CallMulti: fn,
	}))
}

func (rt *Runtime) NewHostProxy(name string, subject any, adapter HostAdapter) Value {
	return HandleValue(rt.heap.NewHostProxy(HostProxy{Name: name, Subject: subject, Adapter: adapter, Meta: NilValue, Env: HandleValue(rt.globals)}))
}

func WrapFunction(rt *Runtime, name string, fn any) (Value, error) {
	rv := reflect.ValueOf(fn)
	if !rv.IsValid() || rv.Kind() != reflect.Func {
		return NilValue, fmt.Errorf("%s is not a function", name)
	}
	return rt.NewHostFunctionMulti(name, func(rt *Runtime, args []Value) ([]Value, error) {
		callArgs, err := convertArgs(rt, rv.Type(), args)
		if err != nil {
			return nil, err
		}
		results := rv.Call(callArgs)
		return packCallResultsMulti(rt, results)
	}), nil
}

func WrapObject(rt *Runtime, name string, obj any) (Value, error) {
	if obj == nil {
		return NilValue, fmt.Errorf("%s is nil", name)
	}
	return rt.NewHostProxy(name, obj, newReflectAdapter()), nil
}

func BoxValue(rt *Runtime, value any) (Value, error) {
	return goToValue(rt, value)
}

type reflectAdapter struct{}

func newReflectAdapter() *reflectAdapter {
	return &reflectAdapter{}
}

func (a *reflectAdapter) GetField(rt *Runtime, subject any, name string) (Value, bool, error) {
	rv := reflect.ValueOf(subject)
	base := derefValue(rv)
	if base.IsValid() && base.Kind() == reflect.Struct {
		field := base.FieldByName(name)
		if field.IsValid() && field.CanInterface() {
			value, err := goToValue(rt, field.Interface())
			return value, true, err
		}
	}
	method := rv.MethodByName(name)
	if !method.IsValid() && base.IsValid() {
		method = base.MethodByName(name)
	}
	if method.IsValid() {
		bound, err := WrapFunction(rt, name, method.Interface())
		return bound, err == nil, err
	}
	return NilValue, false, nil
}

func (a *reflectAdapter) SetField(rt *Runtime, subject any, name string, value Value) error {
	rv := reflect.ValueOf(subject)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("host object %T is not settable", subject)
	}
	base := derefValue(rv)
	if base.Kind() != reflect.Struct {
		return fmt.Errorf("host object %T is not a struct", subject)
	}
	field := base.FieldByName(name)
	if !field.IsValid() || !field.CanSet() {
		return fmt.Errorf("field %s is not settable on %T", name, subject)
	}
	converted, err := valueToType(rt, value, field.Type())
	if err != nil {
		return err
	}
	field.Set(converted)
	return nil
}

func convertArgs(rt *Runtime, fnType reflect.Type, args []Value) ([]reflect.Value, error) {
	if !fnType.IsVariadic() && len(args) != fnType.NumIn() {
		return nil, fmt.Errorf("expected %d args, got %d", fnType.NumIn(), len(args))
	}
	if fnType.IsVariadic() && len(args) < fnType.NumIn()-1 {
		return nil, fmt.Errorf("expected at least %d args, got %d", fnType.NumIn()-1, len(args))
	}
	callArgs := make([]reflect.Value, 0, len(args))
	for i := 0; i < len(args); i++ {
		var target reflect.Type
		switch {
		case fnType.IsVariadic() && i >= fnType.NumIn()-1:
			target = fnType.In(fnType.NumIn() - 1).Elem()
		default:
			target = fnType.In(i)
		}
		converted, err := valueToType(rt, args[i], target)
		if err != nil {
			return nil, err
		}
		callArgs = append(callArgs, converted)
	}
	return callArgs, nil
}

func packCallResultsMulti(rt *Runtime, results []reflect.Value) ([]Value, error) {
	if len(results) == 0 {
		return nil, nil
	}
	last := results[len(results)-1]
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if last.IsValid() && last.Type().Implements(errorType) {
		if !last.IsNil() {
			return nil, last.Interface().(error)
		}
		results = results[:len(results)-1]
	}
	if len(results) == 0 {
		return nil, nil
	}
	packed := make([]Value, 0, len(results))
	for _, result := range results {
		value, err := goToValue(rt, result.Interface())
		if err != nil {
			return nil, err
		}
		packed = append(packed, value)
	}
	return packed, nil
}

func goToValue(rt *Runtime, value any) (Value, error) {
	if value == nil {
		return NilValue, nil
	}
	switch v := value.(type) {
	case Value:
		return v, nil
	case bool:
		return BoolValue(v), nil
	case int:
		return NumberValue(float64(v)), nil
	case int8:
		return NumberValue(float64(v)), nil
	case int16:
		return NumberValue(float64(v)), nil
	case int32:
		return NumberValue(float64(v)), nil
	case int64:
		return NumberValue(float64(v)), nil
	case uint:
		return NumberValue(float64(v)), nil
	case uint8:
		return NumberValue(float64(v)), nil
	case uint16:
		return NumberValue(float64(v)), nil
	case uint32:
		return NumberValue(float64(v)), nil
	case uint64:
		return NumberValue(float64(v)), nil
	case float32:
		return NumberValue(float64(v)), nil
	case float64:
		return NumberValue(v), nil
	case string:
		return rt.StringValue(v), nil
	default:
		return WrapObject(rt, reflect.TypeOf(value).String(), value)
	}
}

func valueToType(rt *Runtime, value Value, target reflect.Type) (reflect.Value, error) {
	if target == reflect.TypeOf(Value(0)) {
		return reflect.ValueOf(value), nil
	}
	if target.Kind() == reflect.Interface && target.NumMethod() == 0 {
		return reflect.ValueOf(value), nil
	}
	if value.Kind() == KindNil {
		return reflect.Zero(target), nil
	}
	switch target.Kind() {
	case reflect.Bool:
		if value.Kind() != KindBool {
			return reflect.Value{}, fmt.Errorf("expected bool, got %s", value)
		}
		return reflect.ValueOf(value.Bool()).Convert(target), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if !value.IsNumber() {
			return reflect.Value{}, fmt.Errorf("expected number, got %s", value)
		}
		return reflect.ValueOf(int64(value.Number())).Convert(target), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if !value.IsNumber() {
			return reflect.Value{}, fmt.Errorf("expected number, got %s", value)
		}
		return reflect.ValueOf(uint64(value.Number())).Convert(target), nil
	case reflect.Float32, reflect.Float64:
		if !value.IsNumber() {
			return reflect.Value{}, fmt.Errorf("expected number, got %s", value)
		}
		return reflect.ValueOf(value.Number()).Convert(target), nil
	case reflect.String:
		s, ok := rt.ToString(value)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected string, got %s", value)
		}
		return reflect.ValueOf(s), nil
	case reflect.Pointer, reflect.Struct:
		h, ok := value.Handle()
		if !ok || h.Kind() != ObjectHostProxy {
			return reflect.Value{}, fmt.Errorf("expected host proxy, got %s", value)
		}
		subject := rt.heap.HostProxy(h).Subject
		rv := reflect.ValueOf(subject)
		if rv.Type().AssignableTo(target) {
			return rv, nil
		}
		if rv.Type().ConvertibleTo(target) {
			return rv.Convert(target), nil
		}
		if rv.Kind() == reflect.Pointer && rv.Elem().Type().AssignableTo(target) {
			return rv.Elem(), nil
		}
		return reflect.Value{}, fmt.Errorf("cannot convert host proxy %T to %s", subject, target)
	default:
		return reflect.Value{}, fmt.Errorf("unsupported Go target type %s", target)
	}
}

func derefValue(v reflect.Value) reflect.Value {
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

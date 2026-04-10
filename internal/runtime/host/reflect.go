package host

import (
	"fmt"
	"reflect"
	"strings"
)

func makeGetter() Getter {
	return func(current any, key string) (any, bool, error) {
		value := reflect.ValueOf(current)
		if !value.IsValid() {
			return nil, false, fmt.Errorf("host target is invalid")
		}
		for value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return nil, false, fmt.Errorf("host target pointer is nil")
			}
			value = value.Elem()
		}
		switch value.Kind() {
		case reflect.Map:
			if value.Type().Key().Kind() != reflect.String {
				return nil, false, fmt.Errorf("host map key type must be string")
			}
			result := value.MapIndex(reflect.ValueOf(key))
			if !result.IsValid() {
				return nil, false, nil
			}
			return result.Interface(), true, nil
		case reflect.Struct:
			field, ok := lookupStructField(value, key)
			if !ok {
				method := reflect.ValueOf(current).MethodByName(exportName(key))
				if method.IsValid() && method.Type().NumIn() == 0 && method.Type().NumOut() >= 1 {
					results := method.Call(nil)
					return results[0].Interface(), true, nil
				}
				return nil, false, nil
			}
			return field.Interface(), true, nil
		default:
			return nil, false, fmt.Errorf("unsupported host object kind %s", value.Kind())
		}
	}
}

func makeSetter() Setter {
	return func(current any, key string, newValue any) error {
		value := reflect.ValueOf(current)
		if !value.IsValid() {
			return fmt.Errorf("host target is invalid")
		}
		for value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return fmt.Errorf("host target pointer is nil")
			}
			value = value.Elem()
		}
		switch value.Kind() {
		case reflect.Map:
			if value.Type().Key().Kind() != reflect.String {
				return fmt.Errorf("host map key type must be string")
			}
			converted, err := assignableValue(reflect.TypeOf(newValue), value.Type().Elem(), newValue)
			if err != nil {
				return err
			}
			value.SetMapIndex(reflect.ValueOf(key), converted)
			return nil
		case reflect.Struct:
			field, ok := lookupStructField(value, key)
			if !ok {
				return fmt.Errorf("host field %q not found", key)
			}
			if !field.CanSet() {
				return fmt.Errorf("host field %q is not settable", key)
			}
			converted, err := assignableValue(reflect.TypeOf(newValue), field.Type(), newValue)
			if err != nil {
				return err
			}
			field.Set(converted)
			return nil
		default:
			return fmt.Errorf("unsupported host object kind %s", value.Kind())
		}
	}
}

func makeCaller() Caller {
	return func(target any, args []any) ([]any, error) {
		function := reflect.ValueOf(target)
		if !function.IsValid() || function.Kind() != reflect.Func {
			return nil, fmt.Errorf("host target is not callable: %T", target)
		}
		typeInfo := function.Type()
		if !typeInfo.IsVariadic() && len(args) != typeInfo.NumIn() {
			return nil, fmt.Errorf("host function expects %d args, got %d", typeInfo.NumIn(), len(args))
		}
		callArgs := make([]reflect.Value, 0, len(args))
		for index, arg := range args {
			var targetType reflect.Type
			if typeInfo.IsVariadic() && index >= typeInfo.NumIn()-1 {
				targetType = typeInfo.In(typeInfo.NumIn() - 1).Elem()
			} else {
				targetType = typeInfo.In(index)
			}
			converted, err := assignableValue(reflect.TypeOf(arg), targetType, arg)
			if err != nil {
				return nil, err
			}
			callArgs = append(callArgs, converted)
		}
		results := function.Call(callArgs)
		if len(results) > 0 {
			last := results[len(results)-1]
			if last.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) && !last.IsNil() {
				return nil, last.Interface().(error)
			}
			if last.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
				results = results[:len(results)-1]
			}
		}
		convertedResults := make([]any, 0, len(results))
		for _, result := range results {
			convertedResults = append(convertedResults, result.Interface())
		}
		return convertedResults, nil
	}
}

func lookupStructField(value reflect.Value, key string) (reflect.Value, bool) {
	typeInfo := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := typeInfo.Field(index)
		if !field.IsExported() {
			continue
		}
		if strings.EqualFold(field.Name, key) {
			return value.Field(index), true
		}
	}
	return reflect.Value{}, false
}

func exportName(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func assignableValue(sourceType reflect.Type, targetType reflect.Type, candidate any) (reflect.Value, error) {
	if candidate == nil {
		return reflect.Zero(targetType), nil
	}
	value := reflect.ValueOf(candidate)
	if sourceType == nil {
		return reflect.Zero(targetType), nil
	}
	if value.Type().AssignableTo(targetType) {
		return value, nil
	}
	if value.Type().ConvertibleTo(targetType) {
		return value.Convert(targetType), nil
	}
	if targetType.Kind() == reflect.Interface && value.Type().Implements(targetType) {
		return value, nil
	}
	return reflect.Value{}, fmt.Errorf("cannot assign host value of type %s to %s", value.Type(), targetType)
}

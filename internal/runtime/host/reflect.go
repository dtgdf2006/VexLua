package host

import (
	"fmt"
	"reflect"
	"strings"
)

const luaStructFieldTag = "lua"

type LuaMethodMapper interface {
	LuaMethodMap() map[string]string
}

func makeGetter() Getter {
	return func(current any, key any) (any, bool, error) {
		value := reflect.ValueOf(current)
		if !value.IsValid() {
			return nil, false, newBridgeImplementationError("host target is invalid")
		}
		for value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return nil, false, newBridgeImplementationError("host target pointer is nil")
			}
			value = value.Elem()
		}
		switch value.Kind() {
		case reflect.Map:
			if key == nil {
				return nil, false, newBridgeImplementationError("host map key cannot be nil")
			}
			convertedKey, err := assignableValue(reflect.TypeOf(key), value.Type().Key(), key)
			if err != nil {
				return nil, false, newBridgeImplementationError(err.Error())
			}
			result := value.MapIndex(convertedKey)
			if !result.IsValid() {
				return nil, false, nil
			}
			return result.Interface(), true, nil
		case reflect.Struct:
			keyText, ok := key.(string)
			if !ok {
				return nil, false, newBridgeImplementationError("host struct property key must be string")
			}
			field, ok := lookupStructField(value, keyText)
			if !ok {
				method, found, err := lookupStructMethod(current, keyText)
				if err != nil {
					return nil, false, err
				}
				if !found {
					return nil, false, nil
				}
				results := method.Call(nil)
				return results[0].Interface(), true, nil
			}
			return field.Interface(), true, nil
		default:
			return nil, false, newBridgeImplementationError("unsupported host object kind %s", value.Kind())
		}
	}
}

func makeSetter() Setter {
	return func(current any, key any, newValue any) error {
		value := reflect.ValueOf(current)
		if !value.IsValid() {
			return newBridgeImplementationError("host target is invalid")
		}
		for value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return newBridgeImplementationError("host target pointer is nil")
			}
			value = value.Elem()
		}
		switch value.Kind() {
		case reflect.Map:
			if key == nil {
				return newBridgeImplementationError("host map key cannot be nil")
			}
			convertedKey, err := assignableValue(reflect.TypeOf(key), value.Type().Key(), key)
			if err != nil {
				return newBridgeImplementationError(err.Error())
			}
			converted, err := assignableValue(reflect.TypeOf(newValue), value.Type().Elem(), newValue)
			if err != nil {
				return newBridgeImplementationError(err.Error())
			}
			value.SetMapIndex(convertedKey, converted)
			return nil
		case reflect.Struct:
			keyText, ok := key.(string)
			if !ok {
				return newBridgeImplementationError("host struct property key must be string")
			}
			field, ok := lookupStructField(value, keyText)
			if !ok {
				return newBridgeImplementationError("host field %q not found", keyText)
			}
			if !field.CanSet() {
				return newBridgeImplementationError("host field %q is not settable", keyText)
			}
			converted, err := assignableValue(reflect.TypeOf(newValue), field.Type(), newValue)
			if err != nil {
				return newBridgeImplementationError(err.Error())
			}
			field.Set(converted)
			return nil
		default:
			return newBridgeImplementationError("unsupported host object kind %s", value.Kind())
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
		if !structFieldMatchesKey(field, key) {
			continue
		}
		return value.Field(index), true
	}
	return reflect.Value{}, false
}

func structFieldMatchesKey(field reflect.StructField, key string) bool {
	if tagName, ok := luaStructFieldName(field); ok {
		return tagName == key
	}
	return strings.EqualFold(field.Name, key)
}

func luaStructFieldName(field reflect.StructField) (string, bool) {
	rawTag := field.Tag.Get(luaStructFieldTag)
	if rawTag == "" {
		return "", false
	}
	tagName, _, _ := strings.Cut(rawTag, ",")
	if tagName == "" {
		return "", false
	}
	if tagName == "-" {
		return "", false
	}
	return tagName, true
}

func lookupStructMethod(current any, key string) (reflect.Value, bool, error) {
	methodName, ok := luaStructMethodName(current, key)
	if !ok {
		return reflect.Value{}, false, nil
	}
	method := reflect.ValueOf(current).MethodByName(methodName)
	if !method.IsValid() {
		return reflect.Value{}, false, newBridgeImplementationError("host lua method %q maps to missing Go method %q", key, methodName)
	}
	if method.Type().NumIn() != 0 || method.Type().NumOut() < 1 {
		return reflect.Value{}, false, newBridgeImplementationError("host lua method %q has unsupported signature", key)
	}
	return method, true, nil
}

func luaStructMethodName(current any, key string) (string, bool) {
	mapper, ok := current.(LuaMethodMapper)
	if !ok || mapper == nil {
		return "", false
	}
	methodMap := mapper.LuaMethodMap()
	if len(methodMap) == 0 {
		return "", false
	}
	methodName, ok := methodMap[key]
	if !ok || methodName == "" {
		return "", false
	}
	return methodName, true
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

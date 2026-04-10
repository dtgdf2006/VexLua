package value

import "fmt"

func (tag Tag) String() string {
	switch tag {
	case TagInvalid:
		return "Invalid"
	case TagNil:
		return "Nil"
	case TagBool:
		return "Bool"
	case TagI32:
		return "I32"
	case TagStringRef:
		return "StringRef"
	case TagTableRef:
		return "TableRef"
	case TagLuaClosureRef:
		return "LuaClosureRef"
	case TagProtoRef:
		return "ProtoRef"
	case TagUpValueRef:
		return "UpValueRef"
	case TagThreadRef:
		return "ThreadRef"
	case TagHostObjectRef:
		return "HostObjectRef"
	case TagHostFunctionRef:
		return "HostFunctionRef"
	case TagNativeClosureRef:
		return "NativeClosureRef"
	case TagLightHandle:
		return "LightHandle"
	case TagReserved1:
		return "Reserved1"
	case TagReserved2:
		return "Reserved2"
	case TagReserved3:
		return "Reserved3"
	default:
		return fmt.Sprintf("Tag(%d)", tag)
	}
}

func (kind ObjectKind) String() string {
	switch kind {
	case KindString:
		return "String"
	case KindTable:
		return "Table"
	case KindLuaClosure:
		return "LuaClosure"
	case KindProto:
		return "Proto"
	case KindUpValue:
		return "UpValue"
	case KindThread:
		return "Thread"
	case KindHostObject:
		return "HostObject"
	case KindHostFunction:
		return "HostFunction"
	case KindHostDescriptor:
		return "HostDescriptor"
	case KindNativeClosure:
		return "NativeClosure"
	case KindCodeMetadata:
		return "CodeMetadata"
	default:
		return fmt.Sprintf("ObjectKind(%d)", kind)
	}
}

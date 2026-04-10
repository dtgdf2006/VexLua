package interp

import (
	"fmt"
	"math"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/host"
	"vexlua/internal/runtime/state"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
)

func (engine *Engine) callLuaClosure(thread *state.ThreadState, closureRef value.HeapRef44, args []value.TValue, nresults int) ([]value.TValue, error) {
	closureObject, err := engine.Closures.Object(closureRef)
	if err != nil {
		return nil, err
	}
	protoRef, ok := closureObject.ProtoRef()
	if !ok {
		return nil, fmt.Errorf("closure %#x has invalid proto reference %s", uint64(closureRef), closureObject.Proto)
	}
	proto, err := engine.Protos.Resolve(protoRef)
	if err != nil {
		return nil, err
	}
	upvalueRefs, err := engine.Closures.UpvalueRefs(closureRef)
	if err != nil {
		return nil, err
	}
	if err := bytecode.ValidateProto(proto); err != nil {
		return nil, err
	}

	ctx := engine.threadState(thread)
	registerCount := uint32(proto.MaxStackSize)
	reservedSlots := registerCount
	if reservedSlots == 0 {
		reservedSlots = 1
	}
	registerBase, err := thread.NextRegisterBase()
	if err != nil {
		return nil, err
	}
	if registerBase+reservedSlots > thread.StackSlots() {
		return nil, fmt.Errorf("thread stack exhausted: need %d slots, have %d", registerBase+reservedSlots, thread.StackSlots())
	}
	baseAddress, err := thread.SlotAddress(registerBase)
	if err != nil {
		return nil, err
	}
	constBase, err := engine.Protos.ConstantBase(proto, engine.Strings)
	if err != nil {
		return nil, err
	}
	varargCount := 0
	if int(proto.NumParams) < len(args) {
		varargCount = len(args) - int(proto.NumParams)
	}
	frame, err := thread.PushFrame(state.FrameSpec{
		Closure:       value.LuaClosureRefValue(closureRef),
		Proto:         closureObject.Proto,
		RegisterBase:  registerBase,
		ConstBase:     constBase,
		SavedBCOff:    0,
		NResults:      normalizeNResults(nresults),
		VarargCount:   uint32(varargCount),
		RegisterCount: uint16(registerCount),
		SpillCount:    0,
	})
	if err != nil {
		return nil, err
	}
	engine.clearSlots(thread, registerBase, reservedSlots)

	activation := &activation{
		thread:        thread,
		frame:         frame,
		closureRef:    closureRef,
		closureObject: closureObject,
		proto:         proto,
		upvalueRefs:   upvalueRefs,
		env:           closureObject.Env,
		registerBase:  registerBase,
		baseAddress:   baseAddress,
		reservedSlots: reservedSlots,
		top:           uint32(minInt(len(args), int(registerCount))),
		pc:            0,
	}
	ctx.activations = append(ctx.activations, activation)

	for index := 0; index < int(proto.NumParams) && index < len(args); index++ {
		if err := engine.setRegister(activation, index, args[index]); err != nil {
			return nil, err
		}
	}
	var varargs []value.TValue
	if proto.IsVararg != 0 && len(args) > int(proto.NumParams) {
		varargs = append(varargs, args[int(proto.NumParams):]...)
	}
	if int(proto.NumParams) > len(args) {
		activation.top = uint32(minInt(int(proto.NumParams), int(registerCount)))
	}

	results, execErr := engine.executeActivation(activation, varargs)
	_, closeErr := engine.Upvalues.CloseAtOrAbove(thread, activation.baseAddress)
	_, popErr := thread.PopFrame()
	ctx.activations = ctx.activations[:len(ctx.activations)-1]
	engine.clearSlots(thread, registerBase, reservedSlots)

	if execErr != nil {
		return nil, execErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if popErr != nil {
		return nil, popErr
	}
	return normalizeResults(results, nresults), nil
}

func (engine *Engine) executeActivation(act *activation, varargs []value.TValue) ([]value.TValue, error) {
	for act.pc < len(act.proto.Code) {
		pc := act.pc
		instruction := act.proto.Code[pc]
		opcode := instruction.Opcode()
		act.frame.SavedBCOff = uint32(pc)
		act.pc++

		switch opcode {
		case bytecode.OP_MOVE:
			valueToMove, err := engine.registerValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A(), valueToMove); err != nil {
				return nil, err
			}
		case bytecode.OP_LOADK:
			constantValue, err := engine.constantValue(act, instruction.Bx())
			if err != nil {
				return nil, runtimeError(act.proto, pc, opcode, err.Error())
			}
			if err := engine.setRegister(act, instruction.A(), constantValue); err != nil {
				return nil, err
			}
		case bytecode.OP_LOADBOOL:
			if err := engine.setRegister(act, instruction.A(), value.BoolValue(instruction.B() != 0)); err != nil {
				return nil, err
			}
			if instruction.C() != 0 {
				act.pc++
			}
		case bytecode.OP_LOADNIL:
			for register := instruction.A(); register <= instruction.B(); register++ {
				if err := engine.setRegister(act, register, value.NilValue()); err != nil {
					return nil, err
				}
			}
		case bytecode.OP_GETUPVAL:
			if instruction.B() >= len(act.upvalueRefs) {
				return nil, runtimeError(act.proto, pc, opcode, fmt.Sprintf("upvalue %d is out of range", instruction.B()))
			}
			upvalueValue, err := engine.Upvalues.Get(act.upvalueRefs[instruction.B()])
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A(), upvalueValue); err != nil {
				return nil, err
			}
		case bytecode.OP_GETGLOBAL:
			key, err := engine.constantValue(act, instruction.Bx())
			if err != nil {
				return nil, runtimeError(act.proto, pc, opcode, err.Error())
			}
			globalValue, _, err := engine.getTable(act.env, key)
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A(), globalValue); err != nil {
				return nil, err
			}
		case bytecode.OP_GETTABLE:
			tableValue, err := engine.registerValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			key, err := engine.rkValue(act, instruction.C())
			if err != nil {
				return nil, err
			}
			result, _, err := engine.getTable(tableValue, key)
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A(), result); err != nil {
				return nil, err
			}
		case bytecode.OP_SETGLOBAL:
			key, err := engine.constantValue(act, instruction.Bx())
			if err != nil {
				return nil, runtimeError(act.proto, pc, opcode, err.Error())
			}
			registerValue, err := engine.registerValue(act, instruction.A())
			if err != nil {
				return nil, err
			}
			if err := engine.setTable(act.env, key, registerValue); err != nil {
				return nil, err
			}
		case bytecode.OP_SETUPVAL:
			if instruction.B() >= len(act.upvalueRefs) {
				return nil, runtimeError(act.proto, pc, opcode, fmt.Sprintf("upvalue %d is out of range", instruction.B()))
			}
			registerValue, err := engine.registerValue(act, instruction.A())
			if err != nil {
				return nil, err
			}
			if err := engine.Upvalues.Set(act.upvalueRefs[instruction.B()], registerValue); err != nil {
				return nil, err
			}
		case bytecode.OP_SETTABLE:
			tableValue, err := engine.registerValue(act, instruction.A())
			if err != nil {
				return nil, err
			}
			key, err := engine.rkValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			slotValue, err := engine.rkValue(act, instruction.C())
			if err != nil {
				return nil, err
			}
			if err := engine.setTable(tableValue, key, slotValue); err != nil {
				return nil, err
			}
		case bytecode.OP_NEWTABLE:
			handle, err := engine.Tables.New(uint32(fb2int(instruction.B())), uint32(fb2int(instruction.C())))
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A(), handle.Value); err != nil {
				return nil, err
			}
		case bytecode.OP_ADD, bytecode.OP_SUB, bytecode.OP_MUL, bytecode.OP_DIV, bytecode.OP_MOD, bytecode.OP_POW:
			left, err := engine.rkNumber(act, instruction.B(), pc, opcode)
			if err != nil {
				return nil, err
			}
			right, err := engine.rkNumber(act, instruction.C(), pc, opcode)
			if err != nil {
				return nil, err
			}
			var result float64
			switch opcode {
			case bytecode.OP_ADD:
				result = left + right
			case bytecode.OP_SUB:
				result = left - right
			case bytecode.OP_MUL:
				result = left * right
			case bytecode.OP_DIV:
				result = left / right
			case bytecode.OP_MOD:
				result = math.Mod(left, right)
			case bytecode.OP_POW:
				result = math.Pow(left, right)
			}
			if err := engine.setRegister(act, instruction.A(), value.NumberValue(result)); err != nil {
				return nil, err
			}
		case bytecode.OP_UNM:
			number, err := engine.registerNumber(act, instruction.B(), pc, opcode)
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A(), value.NumberValue(-number)); err != nil {
				return nil, err
			}
		case bytecode.OP_JMP:
			act.pc += instruction.SBx()
		case bytecode.OP_EQ:
			left, err := engine.rkValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			right, err := engine.rkValue(act, instruction.C())
			if err != nil {
				return nil, err
			}
			equal, err := engine.valuesEqual(left, right)
			if err != nil {
				return nil, runtimeError(act.proto, pc, opcode, err.Error())
			}
			if equal == (instruction.A() != 0) {
				if err := engine.takeTestJump(act, pc, opcode); err != nil {
					return nil, err
				}
			} else {
				act.pc++
			}
		case bytecode.OP_LT:
			left, err := engine.rkValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			right, err := engine.rkValue(act, instruction.C())
			if err != nil {
				return nil, err
			}
			less, err := engine.valuesLess(left, right)
			if err != nil {
				return nil, runtimeError(act.proto, pc, opcode, err.Error())
			}
			if less == (instruction.A() != 0) {
				if err := engine.takeTestJump(act, pc, opcode); err != nil {
					return nil, err
				}
			} else {
				act.pc++
			}
		case bytecode.OP_LE:
			left, err := engine.rkValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			right, err := engine.rkValue(act, instruction.C())
			if err != nil {
				return nil, err
			}
			lessEqual, err := engine.valuesLessEqual(left, right)
			if err != nil {
				return nil, runtimeError(act.proto, pc, opcode, err.Error())
			}
			if lessEqual == (instruction.A() != 0) {
				if err := engine.takeTestJump(act, pc, opcode); err != nil {
					return nil, err
				}
			} else {
				act.pc++
			}
		case bytecode.OP_TEST:
			registerValue, err := engine.registerValue(act, instruction.A())
			if err != nil {
				return nil, err
			}
			if isFalse(registerValue) != (instruction.C() != 0) {
				if err := engine.takeTestJump(act, pc, opcode); err != nil {
					return nil, err
				}
			} else {
				act.pc++
			}
		case bytecode.OP_TESTSET:
			registerValue, err := engine.registerValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			if isFalse(registerValue) != (instruction.C() != 0) {
				if err := engine.setRegister(act, instruction.A(), registerValue); err != nil {
					return nil, err
				}
				if err := engine.takeTestJump(act, pc, opcode); err != nil {
					return nil, err
				}
			} else {
				act.pc++
			}
		case bytecode.OP_CALL:
			callee, callArgs, err := engine.collectCallArguments(act, instruction.A(), instruction.B())
			if err != nil {
				return nil, err
			}
			wantedResults := -1
			if instruction.C() > 0 {
				wantedResults = instruction.C() - 1
			}
			results, err := engine.callValue(act.thread, callee, callArgs, wantedResults)
			if err != nil {
				return nil, err
			}
			if err := engine.storeCallResults(act, instruction.A(), instruction.C(), results); err != nil {
				return nil, err
			}
		case bytecode.OP_TAILCALL:
			act.frame.SetFlag(state.FrameFlagIsTailcall, true)
			callee, callArgs, err := engine.collectCallArguments(act, instruction.A(), instruction.B())
			if err != nil {
				return nil, err
			}
			return engine.callValue(act.thread, callee, callArgs, -1)
		case bytecode.OP_RETURN:
			return engine.collectReturnValues(act, instruction.A(), instruction.B())
		case bytecode.OP_CLOSE:
			address, err := act.thread.SlotAddress(act.registerBase + uint32(instruction.A()))
			if err != nil {
				return nil, err
			}
			if _, err := engine.Upvalues.CloseAtOrAbove(act.thread, address); err != nil {
				return nil, err
			}
		case bytecode.OP_CLOSURE:
			childProtoIndex := instruction.Bx()
			if childProtoIndex < 0 || childProtoIndex >= len(act.proto.Protos) {
				return nil, runtimeError(act.proto, pc, opcode, fmt.Sprintf("child proto %d is out of range", childProtoIndex))
			}
			childProto := act.proto.Protos[childProtoIndex]
			capturedRefs, err := engine.captureUpvalues(act, childProto, pc, opcode)
			if err != nil {
				return nil, err
			}
			closureHandle, err := engine.Closures.NewLuaClosure(childProto, act.env, capturedRefs)
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A(), closureHandle.Value); err != nil {
				return nil, err
			}
		case bytecode.OP_VARARG:
			if err := engine.storeVarargs(act, instruction.A(), instruction.B(), varargs); err != nil {
				return nil, err
			}
		default:
			return nil, runtimeError(act.proto, pc, opcode, "opcode not implemented in Stage 4 interpreter")
		}
	}
	return nil, runtimeError(act.proto, len(act.proto.Code), bytecode.OP_RETURN, "function fell off the end without RETURN")
}

func (engine *Engine) registerValue(act *activation, index int) (value.TValue, error) {
	if index < 0 || index >= int(act.frame.RegisterCount) {
		return value.NilValue(), runtimeError(act.proto, act.pc-1, bytecode.OP_MOVE, fmt.Sprintf("register %d is out of range", index))
	}
	return act.thread.Register(act.frame, uint16(index))
}

func (engine *Engine) setRegister(act *activation, index int, slotValue value.TValue) error {
	if index < 0 || index >= int(act.frame.RegisterCount) {
		return runtimeError(act.proto, act.pc-1, bytecode.OP_MOVE, fmt.Sprintf("register %d is out of range", index))
	}
	if uint32(index+1) > act.top {
		act.top = uint32(index + 1)
	}
	return act.thread.SetRegister(act.frame, uint16(index), slotValue)
}

func (engine *Engine) constantValue(act *activation, index int) (value.TValue, error) {
	if act == nil || act.proto == nil || act.frame == nil || index < 0 || index >= len(act.proto.Constants) {
		return value.NilValue(), fmt.Errorf("constant %d is out of range", index)
	}
	if act.frame.ConstBase == 0 {
		return value.NilValue(), fmt.Errorf("frame const base is not set")
	}
	return engine.Protos.ConstantValue(act.proto, index, engine.Strings)
}

func (engine *Engine) rkValue(act *activation, operand int) (value.TValue, error) {
	if bytecode.IsConstantRK(operand) {
		return engine.constantValue(act, bytecode.IndexK(operand))
	}
	return engine.registerValue(act, operand)
}

func (engine *Engine) registerNumber(act *activation, index int, pc int, opcode bytecode.Opcode) (float64, error) {
	registerValue, err := engine.registerValue(act, index)
	if err != nil {
		return 0, err
	}
	number, ok := registerValue.Float64()
	if !ok {
		return 0, runtimeError(act.proto, pc, opcode, fmt.Sprintf("register %d is not a number: %s", index, registerValue))
	}
	return number, nil
}

func (engine *Engine) rkNumber(act *activation, operand int, pc int, opcode bytecode.Opcode) (float64, error) {
	v, err := engine.rkValue(act, operand)
	if err != nil {
		return 0, err
	}
	number, ok := v.Float64()
	if !ok {
		return 0, runtimeError(act.proto, pc, opcode, fmt.Sprintf("operand %d is not a number: %s", operand, v))
	}
	return number, nil
}

func (engine *Engine) collectCallArguments(act *activation, a int, b int) (value.TValue, []value.TValue, error) {
	callee, err := engine.registerValue(act, a)
	if err != nil {
		return value.NilValue(), nil, err
	}
	argumentCount := 0
	if b == 0 {
		if act.top <= uint32(a+1) {
			argumentCount = 0
		} else {
			argumentCount = int(act.top) - a - 1
		}
	} else {
		argumentCount = b - 1
	}
	arguments := make([]value.TValue, 0, argumentCount)
	for index := 0; index < argumentCount; index++ {
		argument, err := engine.registerValue(act, a+1+index)
		if err != nil {
			return value.NilValue(), nil, err
		}
		arguments = append(arguments, argument)
	}
	return callee, arguments, nil
}

func (engine *Engine) storeCallResults(act *activation, a int, c int, results []value.TValue) error {
	if c == 1 {
		act.top = uint32(a)
		return nil
	}
	if c == 0 {
		for index, result := range results {
			if err := engine.setRegister(act, a+index, result); err != nil {
				return err
			}
		}
		act.top = uint32(a + len(results))
		return nil
	}
	wanted := c - 1
	for index := 0; index < wanted; index++ {
		slotValue := value.NilValue()
		if index < len(results) {
			slotValue = results[index]
		}
		if err := engine.setRegister(act, a+index, slotValue); err != nil {
			return err
		}
	}
	if uint32(a+wanted) > act.top {
		act.top = uint32(a + wanted)
	}
	return nil
}

func (engine *Engine) collectReturnValues(act *activation, a int, b int) ([]value.TValue, error) {
	if b == 1 {
		return nil, nil
	}
	count := 0
	if b == 0 {
		if act.top <= uint32(a) {
			count = 0
		} else {
			count = int(act.top) - a
		}
	} else {
		count = b - 1
	}
	results := make([]value.TValue, 0, count)
	for index := 0; index < count; index++ {
		slotValue, err := engine.registerValue(act, a+index)
		if err != nil {
			return nil, err
		}
		results = append(results, slotValue)
	}
	return results, nil
}

func (engine *Engine) storeVarargs(act *activation, a int, b int, varargs []value.TValue) error {
	if b == 0 {
		for index, slotValue := range varargs {
			if err := engine.setRegister(act, a+index, slotValue); err != nil {
				return err
			}
		}
		act.top = uint32(a + len(varargs))
		return nil
	}
	wanted := b - 1
	for index := 0; index < wanted; index++ {
		slotValue := value.NilValue()
		if index < len(varargs) {
			slotValue = varargs[index]
		}
		if err := engine.setRegister(act, a+index, slotValue); err != nil {
			return err
		}
	}
	if uint32(a+wanted) > act.top {
		act.top = uint32(a + wanted)
	}
	return nil
}

func (engine *Engine) captureUpvalues(act *activation, childProto *bytecode.Proto, pc int, opcode bytecode.Opcode) ([]value.HeapRef44, error) {
	captured := make([]value.HeapRef44, int(childProto.NumUpvalues))
	for index := range captured {
		if act.pc >= len(act.proto.Code) {
			return nil, runtimeError(act.proto, pc, opcode, "missing capture instruction after CLOSURE")
		}
		capture := act.proto.Code[act.pc]
		act.pc++
		switch capture.Opcode() {
		case bytecode.OP_MOVE:
			address, err := act.thread.SlotAddress(act.registerBase + uint32(capture.B()))
			if err != nil {
				return nil, err
			}
			handle, err := engine.Upvalues.FindOrCreateOpen(act.thread, address)
			if err != nil {
				return nil, err
			}
			captured[index] = handle.Ref
		case bytecode.OP_GETUPVAL:
			if capture.B() >= len(act.upvalueRefs) {
				return nil, runtimeError(act.proto, pc, opcode, fmt.Sprintf("parent upvalue %d is out of range", capture.B()))
			}
			captured[index] = act.upvalueRefs[capture.B()]
		default:
			return nil, runtimeError(act.proto, pc, opcode, fmt.Sprintf("invalid upvalue capture opcode %s", capture.Opcode()))
		}
	}
	return captured, nil
}

func (engine *Engine) getTable(tableValue value.TValue, key value.TValue) (value.TValue, bool, error) {
	if tableValue.IsBoxedTag(value.TagHostObjectRef) {
		return engine.hostObjectGet(tableValue, key)
	}
	if !tableValue.IsBoxedTag(value.TagTableRef) {
		return value.NilValue(), false, fmt.Errorf("table operation requires table, got %s", tableValue)
	}
	ref, _ := tableValue.HeapRef()
	return engine.Tables.Get(ref, key)
}

func (engine *Engine) setTable(tableValue value.TValue, key value.TValue, slotValue value.TValue) error {
	if tableValue.IsBoxedTag(value.TagHostObjectRef) {
		return engine.hostObjectSet(tableValue, key, slotValue)
	}
	if !tableValue.IsBoxedTag(value.TagTableRef) {
		return fmt.Errorf("table operation requires table, got %s", tableValue)
	}
	ref, _ := tableValue.HeapRef()
	return engine.Tables.Set(ref, key, slotValue)
}

func (engine *Engine) hostObjectGet(objectValue value.TValue, key value.TValue) (value.TValue, bool, error) {
	keyText, err := engine.hostKeyString(key)
	if err != nil {
		return value.NilValue(), false, err
	}
	ref, _ := objectValue.HeapRef()
	header, target, descriptor, err := engine.Hosts.ReadHostObject(ref)
	if err != nil {
		return value.NilValue(), false, err
	}
	native, err := engine.Hosts.ReadNativeDescriptor(header.NativeMeta)
	if err != nil {
		return value.NilValue(), false, err
	}
	if native.DescriptorVersion != header.DescriptorVersion {
		return value.NilValue(), false, fmt.Errorf("host object descriptor version mismatch: wrapper=%d current=%d", header.DescriptorVersion, native.DescriptorVersion)
	}
	if native.Kind != host.DescriptorKindObject || native.Flags&host.DescriptorFlagIndexable == 0 {
		return value.NilValue(), false, fmt.Errorf("host object metadata is not indexable")
	}
	if descriptor.Get == nil {
		return value.NilValue(), false, fmt.Errorf("host object %q does not support property read", descriptor.Name)
	}
	result, found, err := descriptor.Get(target, keyText)
	if err != nil {
		return value.NilValue(), false, err
	}
	if !found {
		return value.NilValue(), false, nil
	}
	boxed, err := host.FromHostValue(engine.Strings, result)
	if err != nil {
		return value.NilValue(), false, err
	}
	return boxed, true, nil
}

func (engine *Engine) hostObjectSet(objectValue value.TValue, key value.TValue, slotValue value.TValue) error {
	keyText, err := engine.hostKeyString(key)
	if err != nil {
		return err
	}
	hostValue, err := host.ToHostValue(engine.Strings, slotValue)
	if err != nil {
		return err
	}
	ref, _ := objectValue.HeapRef()
	header, target, descriptor, err := engine.Hosts.ReadHostObject(ref)
	if err != nil {
		return err
	}
	native, err := engine.Hosts.ReadNativeDescriptor(header.NativeMeta)
	if err != nil {
		return err
	}
	if native.DescriptorVersion != header.DescriptorVersion {
		return fmt.Errorf("host object descriptor version mismatch: wrapper=%d current=%d", header.DescriptorVersion, native.DescriptorVersion)
	}
	if native.Kind != host.DescriptorKindObject || native.Flags&host.DescriptorFlagWritable == 0 {
		return fmt.Errorf("host object metadata is not writable")
	}
	if descriptor.Set == nil {
		return fmt.Errorf("host object %q does not support property write", descriptor.Name)
	}
	return descriptor.Set(target, keyText, hostValue)
}

func (engine *Engine) callHostFunction(functionValue value.TValue, args []value.TValue, nresults int) ([]value.TValue, error) {
	ref, _ := functionValue.HeapRef()
	header, target, descriptor, err := engine.Hosts.ReadHostFunction(ref)
	if err != nil {
		return nil, err
	}
	native, err := engine.Hosts.ReadNativeDescriptor(header.NativeMeta)
	if err != nil {
		return nil, err
	}
	if native.DescriptorVersion != header.DescriptorVersion {
		return nil, fmt.Errorf("host function descriptor version mismatch: wrapper=%d current=%d", header.DescriptorVersion, native.DescriptorVersion)
	}
	if native.Kind != host.DescriptorKindFunction || native.Flags&host.DescriptorFlagCallable == 0 {
		return nil, fmt.Errorf("host function metadata is not callable")
	}
	if native.Flags&host.DescriptorFlagVariadic == 0 && int(native.Arity) != len(args) {
		return nil, fmt.Errorf("host function expects %d args, got %d", native.Arity, len(args))
	}
	if descriptor.Call == nil {
		return nil, fmt.Errorf("host function %q does not support call", descriptor.Name)
	}
	hostArgs := make([]any, 0, len(args))
	for _, slotValue := range args {
		converted, err := host.ToHostValue(engine.Strings, slotValue)
		if err != nil {
			return nil, err
		}
		hostArgs = append(hostArgs, converted)
	}
	results, err := descriptor.Call(target, hostArgs)
	if err != nil {
		return nil, err
	}
	boxed := make([]value.TValue, 0, len(results))
	for _, result := range results {
		converted, err := host.FromHostValue(engine.Strings, result)
		if err != nil {
			return nil, err
		}
		boxed = append(boxed, converted)
	}
	return normalizeResults(boxed, nresults), nil
}

func (engine *Engine) hostKeyString(key value.TValue) (string, error) {
	if !key.IsBoxedTag(value.TagStringRef) {
		return "", fmt.Errorf("host bridge currently only supports string property keys, got %s", key)
	}
	ref, _ := key.HeapRef()
	return engine.Strings.Text(ref)
}

func (engine *Engine) valuesEqual(left value.TValue, right value.TValue) (bool, error) {
	if left.IsNumber() && right.IsNumber() {
		leftNumber, _ := left.Float64()
		rightNumber, _ := right.Float64()
		return leftNumber == rightNumber, nil
	}
	if left.IsBoxedTag(value.TagStringRef) && right.IsBoxedTag(value.TagStringRef) {
		return left.Payload() == right.Payload(), nil
	}
	return left.Bits() == right.Bits(), nil
}

func (engine *Engine) valuesLess(left value.TValue, right value.TValue) (bool, error) {
	if left.IsNumber() && right.IsNumber() {
		leftNumber, _ := left.Float64()
		rightNumber, _ := right.Float64()
		return leftNumber < rightNumber, nil
	}
	if left.IsBoxedTag(value.TagStringRef) && right.IsBoxedTag(value.TagStringRef) {
		leftRef, _ := left.HeapRef()
		rightRef, _ := right.HeapRef()
		_, leftText, err := rtstring.StringAt(engine.Heap, leftRef)
		if err != nil {
			return false, err
		}
		_, rightText, err := rtstring.StringAt(engine.Heap, rightRef)
		if err != nil {
			return false, err
		}
		return leftText < rightText, nil
	}
	return false, fmt.Errorf("comparison requires two numbers or two strings")
}

func (engine *Engine) valuesLessEqual(left value.TValue, right value.TValue) (bool, error) {
	if left.IsNumber() && right.IsNumber() {
		leftNumber, _ := left.Float64()
		rightNumber, _ := right.Float64()
		return leftNumber <= rightNumber, nil
	}
	if left.IsBoxedTag(value.TagStringRef) && right.IsBoxedTag(value.TagStringRef) {
		leftRef, _ := left.HeapRef()
		rightRef, _ := right.HeapRef()
		_, leftText, err := rtstring.StringAt(engine.Heap, leftRef)
		if err != nil {
			return false, err
		}
		_, rightText, err := rtstring.StringAt(engine.Heap, rightRef)
		if err != nil {
			return false, err
		}
		return leftText <= rightText, nil
	}
	return false, fmt.Errorf("comparison requires two numbers or two strings")
}

func (engine *Engine) takeTestJump(act *activation, pc int, opcode bytecode.Opcode) error {
	if act.pc >= len(act.proto.Code) {
		return runtimeError(act.proto, pc, opcode, "test opcode is missing trailing JMP")
	}
	jump := act.proto.Code[act.pc]
	if jump.Opcode() != bytecode.OP_JMP {
		return runtimeError(act.proto, pc, opcode, fmt.Sprintf("expected trailing JMP after test, got %s", jump.Opcode()))
	}
	act.pc += 1 + jump.SBx()
	return nil
}

func normalizeResults(results []value.TValue, nresults int) []value.TValue {
	if nresults < 0 {
		copied := make([]value.TValue, len(results))
		copy(copied, results)
		return copied
	}
	normalized := make([]value.TValue, nresults)
	for index := 0; index < nresults; index++ {
		normalized[index] = value.NilValue()
		if index < len(results) {
			normalized[index] = results[index]
		}
	}
	return normalized
}

func normalizeNResults(nresults int) int16 {
	if nresults < 0 {
		return -1
	}
	if nresults > math.MaxInt16 {
		return math.MaxInt16
	}
	return int16(nresults)
}

func fb2int(value int) int {
	if value < 8 {
		return value
	}
	e := (value >> 3) & 0x1F
	m := value & 7
	return (m + 8) << (e - 1)
}

func isFalse(slotValue value.TValue) bool {
	if slotValue.IsBoxedTag(value.TagNil) {
		return true
	}
	if boolean, ok := slotValue.Bool(); ok {
		return !boolean
	}
	return false
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

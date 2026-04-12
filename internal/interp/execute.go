package interp

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/feedback"
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
	if err := bytecode.ValidateProto(proto); err != nil {
		return nil, err
	}

	ctx := engine.threadState(thread)
	registerCount := uint32(proto.MaxStackSize)
	registerBase, err := thread.NextRegisterBase()
	if err != nil {
		return nil, err
	}
	constBase, err := engine.Protos.ConstantBase(proto, engine.Strings)
	if err != nil {
		return nil, err
	}
	varargCount := 0
	if proto.IsVararg != 0 && int(proto.NumParams) < len(args) {
		varargCount = len(args) - int(proto.NumParams)
	}
	reservedSlots := registerCount + uint32(varargCount)
	if reservedSlots == 0 {
		reservedSlots = 1
	}
	if registerBase+reservedSlots > thread.StackSlots() {
		return nil, fmt.Errorf("thread stack exhausted: need %d slots, have %d", registerBase+reservedSlots, thread.StackSlots())
	}
	var varargBase uintptr
	if varargCount > 0 {
		varargBase, err = thread.SlotAddress(registerBase + registerCount)
		if err != nil {
			return nil, err
		}
	}
	frame, err := thread.PushFrame(state.FrameSpec{
		Closure:       value.LuaClosureRefValue(closureRef),
		Proto:         closureObject.Proto,
		RegisterBase:  registerBase,
		ConstBase:     constBase,
		VarargBase:    varargBase,
		SavedBCOff:    0,
		NResults:      normalizeNResults(nresults),
		VarargCount:   uint32(varargCount),
		RegisterCount: uint16(registerCount),
		SpillCount:    uint16(varargCount),
		Top:           uint16(minInt(len(args), int(registerCount))),
	})
	if err != nil {
		return nil, err
	}
	engine.clearSlots(thread, registerBase, reservedSlots)

	activation := &activation{
		thread: thread,
		frame:  frame,
		top:    uint32(frame.LogicalTop()),
		pc:     0,
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
		for index, slotValue := range varargs {
			if err := thread.SetValueAtAddress(varargBase+uintptr(index)*value.TValueSize, slotValue); err != nil {
				return nil, err
			}
		}
	}
	if int(proto.NumParams) > len(args) {
		if err := engine.setActivationTop(activation, uint32(minInt(int(proto.NumParams), int(registerCount)))); err != nil {
			return nil, err
		}
	}

	results, execErr := engine.executeActivation(activation, varargs)
	closeLimit := uintptr(frame.RegsBase) + uintptr(frame.RegisterCount)*value.TValueSize
	_, closeErr := engine.Upvalues.CloseInRange(thread, uintptr(frame.RegsBase), closeLimit)
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

func (engine *Engine) ResumeLuaFrame(thread *state.ThreadState, frame *state.CallFrameHeader, startPC int) ([]value.TValue, error) {
	if thread == nil {
		return nil, fmt.Errorf("thread cannot be nil")
	}
	if frame == nil {
		return nil, fmt.Errorf("frame cannot be nil")
	}
	if thread.CurrentFrame() != frame {
		return nil, fmt.Errorf("resume target must be the active frame")
	}
	protoRef, ok := frame.Proto.HeapRef()
	if !ok {
		return nil, fmt.Errorf("frame proto is not a proto reference: %s", frame.Proto)
	}
	proto, err := engine.Protos.Resolve(protoRef)
	if err != nil {
		return nil, err
	}
	if err := bytecode.ValidateProto(proto); err != nil {
		return nil, err
	}
	varargs, err := engine.frameVarargs(thread, frame)
	if err != nil {
		return nil, err
	}
	activation := &activation{thread: thread, frame: frame, top: uint32(frame.LogicalTop()), pc: startPC}
	ctx := engine.threadState(thread)
	ctx.activations = append(ctx.activations, activation)
	defer func() {
		ctx.activations = ctx.activations[:len(ctx.activations)-1]
	}()
	return engine.executeActivation(activation, varargs)
}

func (engine *Engine) executeActivation(act *activation, varargs []value.TValue) ([]value.TValue, error) {
	for {
		proto, err := engine.activationProto(act)
		if err != nil {
			return nil, err
		}
		if act.pc >= len(proto.Code) {
			break
		}
		pc := act.pc
		instruction := proto.Code[pc]
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
				return nil, runtimeError(proto, pc, opcode, err.Error())
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
			upvalueRef, err := engine.activationUpvalueRef(act, instruction.B())
			if err != nil {
				return nil, runtimeError(proto, pc, opcode, err.Error())
			}
			upvalueValue, err := engine.Upvalues.Get(upvalueRef)
			if err != nil {
				return nil, err
			}
			engine.recordUpvalueFeedback(act, pc, feedback.SlotGetUpvalue, upvalueRef, upvalueValue)
			if err := engine.setRegister(act, instruction.A(), upvalueValue); err != nil {
				return nil, err
			}
		case bytecode.OP_GETGLOBAL:
			key, err := engine.constantValue(act, instruction.Bx())
			if err != nil {
				return nil, runtimeError(proto, pc, opcode, err.Error())
			}
			env, err := engine.activationEnv(act)
			if err != nil {
				return nil, err
			}
			globalValue, _, err := engine.getTable(env, key)
			if err != nil {
				return nil, err
			}
			engine.recordGetFeedback(act, pc, feedback.SlotGetGlobal, env, key)
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
			engine.recordGetFeedback(act, pc, feedback.SlotGetTable, tableValue, key)
			if err := engine.setRegister(act, instruction.A(), result); err != nil {
				return nil, err
			}
		case bytecode.OP_SETGLOBAL:
			key, err := engine.constantValue(act, instruction.Bx())
			if err != nil {
				return nil, runtimeError(proto, pc, opcode, err.Error())
			}
			registerValue, err := engine.registerValue(act, instruction.A())
			if err != nil {
				return nil, err
			}
			env, err := engine.activationEnv(act)
			if err != nil {
				return nil, err
			}
			if err := engine.setTable(env, key, registerValue); err != nil {
				return nil, err
			}
			engine.recordSetFeedback(act, pc, feedback.SlotSetGlobal, env, key, registerValue)
		case bytecode.OP_SETUPVAL:
			registerValue, err := engine.registerValue(act, instruction.A())
			if err != nil {
				return nil, err
			}
			upvalueRef, err := engine.activationUpvalueRef(act, instruction.B())
			if err != nil {
				return nil, runtimeError(proto, pc, opcode, err.Error())
			}
			if err := engine.Upvalues.Set(upvalueRef, registerValue); err != nil {
				return nil, err
			}
			engine.recordUpvalueFeedback(act, pc, feedback.SlotSetUpvalue, upvalueRef, registerValue)
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
			engine.recordSetFeedback(act, pc, feedback.SlotSetTable, tableValue, key, slotValue)
		case bytecode.OP_NEWTABLE:
			if err := engine.NewTableBoundary(uint32(fb2int(instruction.B())), uint32(fb2int(instruction.C())), func(tableValue value.TValue) error {
				return engine.setRegister(act, instruction.A(), tableValue)
			}); err != nil {
				return nil, err
			}
		case bytecode.OP_SELF:
			tableValue, err := engine.registerValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A()+1, tableValue); err != nil {
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
		case bytecode.OP_NOT:
			registerValue, err := engine.registerValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A(), value.BoolValue(isFalse(registerValue))); err != nil {
				return nil, err
			}
		case bytecode.OP_LEN:
			registerValue, err := engine.registerValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			lengthValue, err := engine.lengthValue(registerValue)
			if err != nil {
				return nil, runtimeError(proto, pc, opcode, err.Error())
			}
			if err := engine.setRegister(act, instruction.A(), lengthValue); err != nil {
				return nil, err
			}
		case bytecode.OP_CONCAT:
			values := make([]value.TValue, 0, instruction.C()-instruction.B()+1)
			for index := instruction.B(); index <= instruction.C(); index++ {
				slotValue, err := engine.registerValue(act, index)
				if err != nil {
					return nil, err
				}
				values = append(values, slotValue)
			}
			if err := engine.ConcatValuesBoundary(values, func(result value.TValue) error {
				return engine.setRegister(act, instruction.A(), result)
			}); err != nil {
				return nil, runtimeError(proto, pc, opcode, err.Error())
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
				return nil, runtimeError(proto, pc, opcode, err.Error())
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
				return nil, runtimeError(proto, pc, opcode, err.Error())
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
				return nil, runtimeError(proto, pc, opcode, err.Error())
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
			if isFalse(registerValue) == (instruction.C() != 0) {
				act.pc++
			} else {
				if err := engine.takeTestJump(act, pc, opcode); err != nil {
					return nil, err
				}
			}
		case bytecode.OP_TESTSET:
			registerValue, err := engine.registerValue(act, instruction.B())
			if err != nil {
				return nil, err
			}
			if isFalse(registerValue) == (instruction.C() != 0) {
				act.pc++
			} else {
				if err := engine.setRegister(act, instruction.A(), registerValue); err != nil {
					return nil, err
				}
				if err := engine.takeTestJump(act, pc, opcode); err != nil {
					return nil, err
				}
			}
		case bytecode.OP_CALL:
			callee, callArgs, err := engine.collectCallArguments(act, instruction.A(), instruction.B())
			if err != nil {
				return nil, err
			}
			engine.recordCallFeedback(act, pc, feedback.SlotCall, callee)
			results, err := engine.callValue(act.thread, callee, callArgs, instruction.C()-1)
			if err != nil {
				return nil, err
			}
			if err := engine.storeCallResults(act, instruction.A(), instruction.C(), results); err != nil {
				return nil, err
			}
		case bytecode.OP_TAILCALL:
			callee, callArgs, err := engine.collectCallArguments(act, instruction.A(), instruction.B())
			if err != nil {
				return nil, err
			}
			engine.recordCallFeedback(act, pc, feedback.SlotTailCall, callee)
			return engine.callValue(act.thread, callee, callArgs, -1)
		case bytecode.OP_RETURN:
			return engine.collectReturnValues(act, instruction.A(), instruction.B())
		case bytecode.OP_FORLOOP:
			step, err := engine.loopNumberValue(act, instruction.A()+2, pc, opcode, "step")
			if err != nil {
				return nil, err
			}
			index, err := engine.loopNumberValue(act, instruction.A(), pc, opcode, "index")
			if err != nil {
				return nil, err
			}
			limit, err := engine.loopNumberValue(act, instruction.A()+1, pc, opcode, "limit")
			if err != nil {
				return nil, err
			}
			index += step
			continueLoop := (step > 0 && index <= limit) || (step <= 0 && limit <= index)
			if continueLoop {
				indexValue := value.NumberValue(index)
				if err := engine.setRegister(act, instruction.A(), indexValue); err != nil {
					return nil, err
				}
				if err := engine.setRegister(act, instruction.A()+3, indexValue); err != nil {
					return nil, err
				}
				act.pc += instruction.SBx()
			}
		case bytecode.OP_FORPREP:
			init, err := engine.loopNumberValue(act, instruction.A(), pc, opcode, "initial value")
			if err != nil {
				return nil, err
			}
			limit, err := engine.loopNumberValue(act, instruction.A()+1, pc, opcode, "limit")
			if err != nil {
				return nil, err
			}
			step, err := engine.loopNumberValue(act, instruction.A()+2, pc, opcode, "step")
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A()+1, value.NumberValue(limit)); err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A()+2, value.NumberValue(step)); err != nil {
				return nil, err
			}
			if err := engine.setRegister(act, instruction.A(), value.NumberValue(init-step)); err != nil {
				return nil, err
			}
			act.pc += instruction.SBx()
		case bytecode.OP_TFORLOOP:
			callee, callArgs, err := engine.collectCallArguments(act, instruction.A(), 3)
			if err != nil {
				return nil, err
			}
			results, err := engine.callValue(act.thread, callee, callArgs, instruction.C())
			if err != nil {
				return nil, err
			}
			for index := 0; index < instruction.C(); index++ {
				slotValue := value.NilValue()
				if index < len(results) {
					slotValue = results[index]
				}
				if err := engine.setRegister(act, instruction.A()+3+index, slotValue); err != nil {
					return nil, err
				}
			}
			firstResult, err := engine.registerValue(act, instruction.A()+3)
			if err != nil {
				return nil, err
			}
			if !firstResult.IsBoxedTag(value.TagNil) {
				if err := engine.setRegister(act, instruction.A()+2, firstResult); err != nil {
					return nil, err
				}
				if err := engine.takeTestJump(act, pc, opcode); err != nil {
					return nil, err
				}
			} else {
				act.pc++
			}
		case bytecode.OP_SETLIST:
			if err := engine.executeSetList(act, proto, instruction); err != nil {
				return nil, runtimeError(proto, pc, opcode, err.Error())
			}
		case bytecode.OP_CLOSE:
			registerBase, err := engine.activationRegisterBase(act)
			if err != nil {
				return nil, err
			}
			address, err := act.thread.SlotAddress(registerBase + uint32(instruction.A()))
			if err != nil {
				return nil, err
			}
			limit := uintptr(act.frame.RegsBase) + uintptr(act.frame.RegisterCount)*value.TValueSize
			if _, err := engine.CloseUpvaluesInRangeBoundary(act.thread, address, limit); err != nil {
				return nil, err
			}
		case bytecode.OP_CLOSURE:
			childProtoIndex := instruction.Bx()
			if childProtoIndex < 0 || childProtoIndex >= len(proto.Protos) {
				return nil, runtimeError(proto, pc, opcode, fmt.Sprintf("child proto %d is out of range", childProtoIndex))
			}
			childProto := proto.Protos[childProtoIndex]
			capturedRefs, err := engine.captureUpvalues(act, childProto, pc, opcode)
			if err != nil {
				return nil, err
			}
			env, err := engine.activationEnv(act)
			if err != nil {
				return nil, err
			}
			if err := engine.NewClosureBoundary(childProto, env, capturedRefs, func(closureValue value.TValue) error {
				return engine.setRegister(act, instruction.A(), closureValue)
			}); err != nil {
				return nil, err
			}
		case bytecode.OP_VARARG:
			if err := engine.storeVarargs(act, instruction.A(), instruction.B(), varargs); err != nil {
				return nil, err
			}
		default:
			return nil, runtimeError(proto, pc, opcode, "opcode not implemented in Stage 4 interpreter")
		}
	}
	proto, err := engine.activationProto(act)
	if err != nil {
		return nil, err
	}
	return nil, runtimeError(proto, len(proto.Code), bytecode.OP_RETURN, "function fell off the end without RETURN")
}

func (engine *Engine) registerValue(act *activation, index int) (value.TValue, error) {
	if index < 0 || index >= int(act.frame.RegisterCount) {
		return value.NilValue(), engine.activationRuntimeError(act, act.pc-1, bytecode.OP_MOVE, fmt.Sprintf("register %d is out of range", index))
	}
	return act.thread.Register(act.frame, uint16(index))
}

func (engine *Engine) setRegister(act *activation, index int, slotValue value.TValue) error {
	if index < 0 || index >= int(act.frame.RegisterCount) {
		return engine.activationRuntimeError(act, act.pc-1, bytecode.OP_MOVE, fmt.Sprintf("register %d is out of range", index))
	}
	if uint32(index+1) > act.top {
		if err := engine.setActivationTop(act, uint32(index+1)); err != nil {
			return err
		}
	}
	return act.thread.SetRegister(act.frame, uint16(index), slotValue)
}

func (engine *Engine) setActivationTop(act *activation, top uint32) error {
	if act == nil || act.frame == nil {
		return fmt.Errorf("activation frame is not set")
	}
	if top > uint32(act.frame.RegisterCount) {
		return fmt.Errorf("activation top %d exceeds register capacity %d", top, act.frame.RegisterCount)
	}
	act.top = top
	return act.frame.SetTop(uint16(top))
}

func (engine *Engine) activationClosureRef(act *activation) (value.HeapRef44, error) {
	if act == nil || act.frame == nil {
		return 0, fmt.Errorf("activation frame is not set")
	}
	ref, ok := act.frame.Closure.HeapRef()
	if !ok {
		return 0, fmt.Errorf("frame closure is not a closure reference: %s", act.frame.Closure)
	}
	return ref, nil
}

func (engine *Engine) activationEnv(act *activation) (value.TValue, error) {
	closureRef, err := engine.activationClosureRef(act)
	if err != nil {
		return value.NilValue(), err
	}
	return engine.Closures.Env(closureRef)
}

func (engine *Engine) activationUpvalueRef(act *activation, index int) (value.HeapRef44, error) {
	closureRef, err := engine.activationClosureRef(act)
	if err != nil {
		return 0, err
	}
	return engine.Closures.UpvalueRefAt(closureRef, index)
}

func (engine *Engine) activationProto(act *activation) (*bytecode.Proto, error) {
	if act == nil || act.frame == nil {
		return nil, fmt.Errorf("activation frame is not set")
	}
	protoRef, ok := act.frame.Proto.HeapRef()
	if !ok {
		return nil, fmt.Errorf("frame proto is not a proto reference: %s", act.frame.Proto)
	}
	return engine.Protos.Resolve(protoRef)
}

func (engine *Engine) activationRegisterBase(act *activation) (uint32, error) {
	if act == nil || act.thread == nil || act.frame == nil {
		return 0, fmt.Errorf("activation frame is not set")
	}
	return act.thread.SlotIndexForAddress(uintptr(act.frame.RegsBase))
}

func (engine *Engine) activationRuntimeError(act *activation, pc int, opcode bytecode.Opcode, reason string) error {
	proto, err := engine.activationProto(act)
	if err != nil {
		return fmt.Errorf("%s: %w", reason, err)
	}
	return runtimeError(proto, pc, opcode, reason)
}

func (engine *Engine) constantValue(act *activation, index int) (value.TValue, error) {
	proto, err := engine.activationProto(act)
	if err != nil {
		return value.NilValue(), err
	}
	if act == nil || act.frame == nil || index < 0 || index >= len(proto.Constants) {
		return value.NilValue(), fmt.Errorf("constant %d is out of range", index)
	}
	if act.frame.ConstBase == 0 {
		return value.NilValue(), fmt.Errorf("frame const base is not set")
	}
	baseOffset, err := engine.Heap.OffsetForNativeAddress(uintptr(act.frame.ConstBase))
	if err != nil {
		return value.NilValue(), fmt.Errorf("frame const base %#x is not heap-backed: %w", act.frame.ConstBase, err)
	}
	bytes, err := engine.Heap.Resolve(baseOffset+value.HeapOff64(index*value.TValueSize), value.TValueSize)
	if err != nil {
		return value.NilValue(), err
	}
	return value.FromRaw(value.Raw(binary.LittleEndian.Uint64(bytes))), nil
}

func (engine *Engine) lengthValue(slotValue value.TValue) (value.TValue, error) {
	if slotValue.IsBoxedTag(value.TagStringRef) {
		ref, _ := slotValue.HeapRef()
		header, err := engine.Strings.Header(ref)
		if err != nil {
			return value.NilValue(), err
		}
		return value.NumberValue(float64(header.Length)), nil
	}
	if slotValue.IsBoxedTag(value.TagTableRef) {
		ref, _ := slotValue.HeapRef()
		object, err := engine.Tables.Object(ref)
		if err != nil {
			return value.NilValue(), err
		}
		return value.NumberValue(float64(object.ArrayLenHint)), nil
	}
	return value.NilValue(), fmt.Errorf("length requires table or string, got %s", slotValue)
}

func (engine *Engine) concatOperandText(slotValue value.TValue) (string, error) {
	if slotValue.IsBoxedTag(value.TagStringRef) {
		ref, _ := slotValue.HeapRef()
		return engine.Strings.Text(ref)
	}
	if number, ok := slotValue.Float64(); ok {
		return strconv.FormatFloat(number, 'g', -1, 64), nil
	}
	return "", fmt.Errorf("concat requires strings or numbers, got %s", slotValue)
}

func (engine *Engine) loopNumberValue(act *activation, index int, pc int, opcode bytecode.Opcode, role string) (float64, error) {
	slotValue, err := engine.registerValue(act, index)
	if err != nil {
		return 0, err
	}
	number, ok := slotValue.Float64()
	if !ok {
		return 0, engine.activationRuntimeError(act, pc, opcode, fmt.Sprintf("for %s must be a number", role))
	}
	if err := engine.setRegister(act, index, value.NumberValue(number)); err != nil {
		return 0, err
	}
	return number, nil
}

func (engine *Engine) executeSetList(act *activation, proto *bytecode.Proto, instruction bytecode.Instruction) error {
	tableValue, err := engine.registerValue(act, instruction.A())
	if err != nil {
		return err
	}
	n := instruction.B()
	if n == 0 {
		if act.top <= uint32(instruction.A()+1) {
			n = 0
		} else {
			n = int(act.top) - instruction.A() - 1
		}
	}
	block := instruction.C()
	if block == 0 {
		if act.pc >= len(proto.Code) {
			return fmt.Errorf("SETLIST expects trailing extra argument instruction")
		}
		block = int(proto.Code[act.pc])
		act.pc++
	}
	const fieldsPerFlush = 50
	baseIndex := (block - 1) * fieldsPerFlush
	for index := 1; index <= n; index++ {
		slotValue, err := engine.registerValue(act, instruction.A()+index)
		if err != nil {
			return err
		}
		key := value.NumberValue(float64(baseIndex + index))
		if err := engine.setTable(tableValue, key, slotValue); err != nil {
			return err
		}
	}
	return nil
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
		return 0, engine.activationRuntimeError(act, pc, opcode, fmt.Sprintf("register %d is not a number: %s", index, registerValue))
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
		return 0, engine.activationRuntimeError(act, pc, opcode, fmt.Sprintf("operand %d is not a number: %s", operand, v))
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
		if err := engine.setActivationTop(act, uint32(a)); err != nil {
			return err
		}
		return nil
	}
	if c == 0 {
		for index, result := range results {
			if err := engine.setRegister(act, a+index, result); err != nil {
				return err
			}
		}
		if err := engine.setActivationTop(act, uint32(a+len(results))); err != nil {
			return err
		}
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
	return engine.setActivationTop(act, uint32(a+wanted))
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
		if err := engine.setActivationTop(act, uint32(a+len(varargs))); err != nil {
			return err
		}
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
		if err := engine.setActivationTop(act, uint32(a+wanted)); err != nil {
			return err
		}
	}
	return nil
}

func (engine *Engine) captureUpvalues(act *activation, childProto *bytecode.Proto, pc int, opcode bytecode.Opcode) ([]value.HeapRef44, error) {
	proto, err := engine.activationProto(act)
	if err != nil {
		return nil, err
	}
	registerBase, err := engine.activationRegisterBase(act)
	if err != nil {
		return nil, err
	}
	captured := make([]value.HeapRef44, int(childProto.NumUpvalues))
	for index := range captured {
		if act.pc >= len(proto.Code) {
			return nil, runtimeError(proto, pc, opcode, "missing capture instruction after CLOSURE")
		}
		capture := proto.Code[act.pc]
		act.pc++
		switch capture.Opcode() {
		case bytecode.OP_MOVE:
			address, err := act.thread.SlotAddress(registerBase + uint32(capture.B()))
			if err != nil {
				return nil, err
			}
			handle, err := engine.Upvalues.FindOrCreateOpen(act.thread, address)
			if err != nil {
				return nil, err
			}
			captured[index] = handle.Ref
		case bytecode.OP_GETUPVAL:
			upvalueRef, err := engine.activationUpvalueRef(act, capture.B())
			if err != nil {
				return nil, runtimeError(proto, pc, opcode, err.Error())
			}
			captured[index] = upvalueRef
		default:
			return nil, runtimeError(proto, pc, opcode, fmt.Sprintf("invalid upvalue capture opcode %s", capture.Opcode()))
		}
	}
	return captured, nil
}

func (engine *Engine) getTable(tableValue value.TValue, key value.TValue) (value.TValue, bool, error) {
	return engine.ReadIndexBoundary(tableValue, key)
}

func (engine *Engine) setTable(tableValue value.TValue, key value.TValue, slotValue value.TValue) error {
	return engine.WriteIndexBoundary(tableValue, key, slotValue)
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
	proto, err := engine.activationProto(act)
	if err != nil {
		return err
	}
	if act.pc >= len(proto.Code) {
		return runtimeError(proto, pc, opcode, "test opcode is missing trailing JMP")
	}
	jump := proto.Code[act.pc]
	if jump.Opcode() != bytecode.OP_JMP {
		return runtimeError(proto, pc, opcode, fmt.Sprintf("expected trailing JMP after test, got %s", jump.Opcode()))
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

func maxUint32(left uint32, right uint32) uint32 {
	if left > right {
		return left
	}
	return right
}

func (engine *Engine) frameVarargs(thread *state.ThreadState, frame *state.CallFrameHeader) ([]value.TValue, error) {
	if thread == nil || frame == nil || frame.VarargCount == 0 || frame.VarargBase == 0 {
		return nil, nil
	}
	varargs := make([]value.TValue, 0, frame.VarargCount)
	for index := uint32(0); index < frame.VarargCount; index++ {
		slotValue, err := thread.ValueAtAddress(uintptr(frame.VarargBase) + uintptr(index)*value.TValueSize)
		if err != nil {
			return nil, err
		}
		varargs = append(varargs, slotValue)
	}
	return varargs, nil
}

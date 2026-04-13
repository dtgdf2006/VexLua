package interp

import (
	"fmt"
	"math"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/feedback"
	rtlua "vexlua/internal/runtime/lua"
	"vexlua/internal/runtime/state"
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
	slots, err := thread.FrameWindow(frame)
	if err != nil {
		_, _ = thread.PopFrame()
		return nil, err
	}
	feedbackLayout := feedback.LayoutForProto(proto)
	if err := engine.validateFrameConstBase(frame, proto); err != nil {
		_, _ = thread.PopFrame()
		return nil, err
	}
	top := uint32(frame.LogicalTop())

	for index := 0; index < int(proto.NumParams) && index < len(args); index++ {
		if err := engine.setRegister(proto, frame, slots, &top, -1, index, args[index]); err != nil {
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
		if err := engine.setFrameTop(frame, &top, uint32(minInt(int(proto.NumParams), int(registerCount)))); err != nil {
			return nil, err
		}
	}

	results, execErr := engine.executeLuaFrame(thread, frame, proto, closureRef, feedbackLayout, slots, 0, varargs)
	reservedSlots = uint32(frame.RegisterCount) + uint32(frame.SpillCount)
	closeLimit := uintptr(frame.RegsBase) + uintptr(frame.RegisterCount)*value.TValueSize
	_, closeErr := engine.Upvalues.CloseInRange(thread, uintptr(frame.RegsBase), closeLimit)
	_, popErr := thread.PopFrame()
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
	closureRef, ok := frame.Closure.HeapRef()
	if !ok {
		return nil, fmt.Errorf("frame closure is not a closure reference: %s", frame.Closure)
	}
	varargs, err := engine.frameVarargs(thread, frame)
	if err != nil {
		return nil, err
	}
	slots, err := thread.FrameWindow(frame)
	if err != nil {
		return nil, err
	}
	feedbackLayout := feedback.LayoutForProto(proto)
	if err := engine.validateFrameConstBase(frame, proto); err != nil {
		return nil, err
	}
	return engine.executeLuaFrame(thread, frame, proto, closureRef, feedbackLayout, slots, startPC, varargs)
}

func (engine *Engine) executeLuaFrame(thread *state.ThreadState, frame *state.CallFrameHeader, proto *bytecode.Proto, closureRef value.HeapRef44, feedbackLayout *feedback.Layout, slots []value.TValue, startPC int, varargs []value.TValue) ([]value.TValue, error) {
	top := uint32(frame.LogicalTop())
	pc := startPC
	code := proto.Code
	for {
		if pc >= len(code) {
			break
		}
		currentPC := pc
		instruction := code[currentPC]
		opcode := instruction.Opcode()
		frame.SavedBCOff = uint32(currentPC)
		pc++

		switch opcode {
		case bytecode.OP_MOVE:
			valueToMove, err := engine.registerValue(proto, frame, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), valueToMove); err != nil {
				return nil, err
			}
		case bytecode.OP_LOADK:
			constantValue, err := engine.constantValue(frame, proto, instruction.Bx())
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), constantValue); err != nil {
				return nil, err
			}
		case bytecode.OP_LOADBOOL:
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), value.BoolValue(instruction.B() != 0)); err != nil {
				return nil, err
			}
			if instruction.C() != 0 {
				pc++
			}
		case bytecode.OP_LOADNIL:
			for register := instruction.A(); register <= instruction.B(); register++ {
				if err := engine.setRegister(proto, frame, slots, &top, currentPC, register, value.NilValue()); err != nil {
					return nil, err
				}
			}
		case bytecode.OP_GETUPVAL:
			upvalueRef, err := engine.closureUpvalueRef(closureRef, instruction.B())
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			upvalueValue, err := engine.Upvalues.Get(upvalueRef)
			if err != nil {
				return nil, err
			}
			engine.recordUpvalueFeedback(feedbackLayout, closureRef, currentPC, feedback.SlotGetUpvalue, upvalueRef, upvalueValue)
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), upvalueValue); err != nil {
				return nil, err
			}
		case bytecode.OP_GETGLOBAL:
			key, err := engine.constantValue(frame, proto, instruction.Bx())
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			env, err := engine.closureEnv(closureRef)
			if err != nil {
				return nil, err
			}
			globalValue, _, err := engine.getTable(thread, env, key)
			if err != nil {
				return nil, err
			}
			engine.recordGetFeedback(feedbackLayout, closureRef, currentPC, feedback.SlotGetGlobal, env, key)
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), globalValue); err != nil {
				return nil, err
			}
		case bytecode.OP_GETTABLE:
			tableValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			key, err := engine.rkValue(frame, proto, slots, currentPC, instruction.C())
			if err != nil {
				return nil, err
			}
			result, _, err := engine.getTable(thread, tableValue, key)
			if err != nil {
				return nil, err
			}
			engine.recordGetFeedback(feedbackLayout, closureRef, currentPC, feedback.SlotGetTable, tableValue, key)
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), result); err != nil {
				return nil, err
			}
		case bytecode.OP_SETGLOBAL:
			key, err := engine.constantValue(frame, proto, instruction.Bx())
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			registerValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.A())
			if err != nil {
				return nil, err
			}
			env, err := engine.closureEnv(closureRef)
			if err != nil {
				return nil, err
			}
			if err := engine.setTable(thread, env, key, registerValue); err != nil {
				return nil, err
			}
			engine.recordSetFeedback(feedbackLayout, closureRef, currentPC, feedback.SlotSetGlobal, env, key, registerValue)
		case bytecode.OP_SETUPVAL:
			registerValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.A())
			if err != nil {
				return nil, err
			}
			upvalueRef, err := engine.closureUpvalueRef(closureRef, instruction.B())
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			if err := engine.Upvalues.Set(upvalueRef, registerValue); err != nil {
				return nil, err
			}
			engine.recordUpvalueFeedback(feedbackLayout, closureRef, currentPC, feedback.SlotSetUpvalue, upvalueRef, registerValue)
		case bytecode.OP_SETTABLE:
			tableValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.A())
			if err != nil {
				return nil, err
			}
			key, err := engine.rkValue(frame, proto, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			slotValue, err := engine.rkValue(frame, proto, slots, currentPC, instruction.C())
			if err != nil {
				return nil, err
			}
			if err := engine.setTable(thread, tableValue, key, slotValue); err != nil {
				return nil, err
			}
			engine.recordSetFeedback(feedbackLayout, closureRef, currentPC, feedback.SlotSetTable, tableValue, key, slotValue)
		case bytecode.OP_NEWTABLE:
			if err := engine.NewTableBoundary(uint32(fb2int(instruction.B())), uint32(fb2int(instruction.C())), func(tableValue value.TValue) error {
				return engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), tableValue)
			}); err != nil {
				return nil, err
			}
		case bytecode.OP_SELF:
			tableValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A()+1, tableValue); err != nil {
				return nil, err
			}
			key, err := engine.rkValue(frame, proto, slots, currentPC, instruction.C())
			if err != nil {
				return nil, err
			}
			result, _, err := engine.getTable(thread, tableValue, key)
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), result); err != nil {
				return nil, err
			}
		case bytecode.OP_ADD, bytecode.OP_SUB, bytecode.OP_MUL, bytecode.OP_DIV, bytecode.OP_MOD, bytecode.OP_POW:
			left, err := engine.rkValue(frame, proto, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			right, err := engine.rkValue(frame, proto, slots, currentPC, instruction.C())
			if err != nil {
				return nil, err
			}
			result, err := engine.ArithmeticBoundary(thread, opcode, left, right)
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), result); err != nil {
				return nil, err
			}
		case bytecode.OP_UNM:
			operand, err := engine.registerValue(proto, frame, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			result, err := engine.ArithmeticBoundary(thread, opcode, operand, operand)
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), result); err != nil {
				return nil, err
			}
		case bytecode.OP_NOT:
			registerValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), value.BoolValue(isFalse(registerValue))); err != nil {
				return nil, err
			}
		case bytecode.OP_LEN:
			registerValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			lengthValue, err := engine.LengthBoundary(thread, registerValue)
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), lengthValue); err != nil {
				return nil, err
			}
		case bytecode.OP_CONCAT:
			values := make([]value.TValue, 0, instruction.C()-instruction.B()+1)
			for index := instruction.B(); index <= instruction.C(); index++ {
				slotValue, err := engine.registerValue(proto, frame, slots, currentPC, index)
				if err != nil {
					return nil, err
				}
				values = append(values, slotValue)
			}
			if err := engine.ConcatValuesBoundary(thread, values, func(result value.TValue) error {
				return engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), result)
			}); err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
		case bytecode.OP_JMP:
			pc += instruction.SBx()
		case bytecode.OP_EQ:
			left, err := engine.rkValue(frame, proto, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			right, err := engine.rkValue(frame, proto, slots, currentPC, instruction.C())
			if err != nil {
				return nil, err
			}
			equal, err := engine.CompareBoundary(thread, opcode, left, right)
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			if equal == (instruction.A() != 0) {
				if err := engine.takeTestJump(proto, code, &pc, currentPC, opcode); err != nil {
					return nil, err
				}
			} else {
				pc++
			}
		case bytecode.OP_LT:
			left, err := engine.rkValue(frame, proto, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			right, err := engine.rkValue(frame, proto, slots, currentPC, instruction.C())
			if err != nil {
				return nil, err
			}
			less, err := engine.CompareBoundary(thread, opcode, left, right)
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			if less == (instruction.A() != 0) {
				if err := engine.takeTestJump(proto, code, &pc, currentPC, opcode); err != nil {
					return nil, err
				}
			} else {
				pc++
			}
		case bytecode.OP_LE:
			left, err := engine.rkValue(frame, proto, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			right, err := engine.rkValue(frame, proto, slots, currentPC, instruction.C())
			if err != nil {
				return nil, err
			}
			lessEqual, err := engine.CompareBoundary(thread, opcode, left, right)
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			if lessEqual == (instruction.A() != 0) {
				if err := engine.takeTestJump(proto, code, &pc, currentPC, opcode); err != nil {
					return nil, err
				}
			} else {
				pc++
			}
		case bytecode.OP_TEST:
			registerValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.A())
			if err != nil {
				return nil, err
			}
			if isFalse(registerValue) == (instruction.C() != 0) {
				pc++
			} else {
				if err := engine.takeTestJump(proto, code, &pc, currentPC, opcode); err != nil {
					return nil, err
				}
			}
		case bytecode.OP_TESTSET:
			registerValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.B())
			if err != nil {
				return nil, err
			}
			if isFalse(registerValue) == (instruction.C() != 0) {
				pc++
			} else {
				if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), registerValue); err != nil {
					return nil, err
				}
				if err := engine.takeTestJump(proto, code, &pc, currentPC, opcode); err != nil {
					return nil, err
				}
			}
		case bytecode.OP_CALL:
			callee, callArgs, err := engine.collectCallArguments(proto, frame, slots, top, currentPC, instruction.A(), instruction.B())
			if err != nil {
				return nil, err
			}
			engine.recordCallFeedback(feedbackLayout, closureRef, currentPC, feedback.SlotCall, callee)
			results, err := engine.callValue(thread, callee, callArgs, instruction.C()-1)
			if err != nil {
				return nil, err
			}
			if err := engine.storeCallResults(proto, frame, slots, &top, currentPC, instruction.A(), instruction.C(), results); err != nil {
				return nil, err
			}
		case bytecode.OP_TAILCALL:
			callee, callArgs, err := engine.collectCallArguments(proto, frame, slots, top, currentPC, instruction.A(), instruction.B())
			if err != nil {
				return nil, err
			}
			engine.recordCallFeedback(feedbackLayout, closureRef, currentPC, feedback.SlotTailCall, callee)
			return engine.callValue(thread, callee, callArgs, -1)
		case bytecode.OP_RETURN:
			return engine.collectReturnValues(proto, frame, slots, top, currentPC, instruction.A(), instruction.B())
		case bytecode.OP_FORLOOP:
			step, err := engine.loopNumberValue(proto, frame, slots, &top, currentPC, opcode, instruction.A()+2, "step")
			if err != nil {
				return nil, err
			}
			index, err := engine.loopNumberValue(proto, frame, slots, &top, currentPC, opcode, instruction.A(), "index")
			if err != nil {
				return nil, err
			}
			limit, err := engine.loopNumberValue(proto, frame, slots, &top, currentPC, opcode, instruction.A()+1, "limit")
			if err != nil {
				return nil, err
			}
			index += step
			continueLoop := (step > 0 && index <= limit) || (step <= 0 && limit <= index)
			if continueLoop {
				indexValue := value.NumberValue(index)
				if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), indexValue); err != nil {
					return nil, err
				}
				if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A()+3, indexValue); err != nil {
					return nil, err
				}
				pc += instruction.SBx()
			}
		case bytecode.OP_FORPREP:
			init, err := engine.loopNumberValue(proto, frame, slots, &top, currentPC, opcode, instruction.A(), "initial value")
			if err != nil {
				return nil, err
			}
			limit, err := engine.loopNumberValue(proto, frame, slots, &top, currentPC, opcode, instruction.A()+1, "limit")
			if err != nil {
				return nil, err
			}
			step, err := engine.loopNumberValue(proto, frame, slots, &top, currentPC, opcode, instruction.A()+2, "step")
			if err != nil {
				return nil, err
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A()+1, value.NumberValue(limit)); err != nil {
				return nil, err
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A()+2, value.NumberValue(step)); err != nil {
				return nil, err
			}
			if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), value.NumberValue(init-step)); err != nil {
				return nil, err
			}
			pc += instruction.SBx()
		case bytecode.OP_TFORLOOP:
			callee, callArgs, err := engine.collectCallArguments(proto, frame, slots, top, currentPC, instruction.A(), 3)
			if err != nil {
				return nil, err
			}
			engine.recordCallFeedback(feedbackLayout, closureRef, currentPC, feedback.SlotCall, callee)
			results, err := engine.callValue(thread, callee, callArgs, instruction.C())
			if err != nil {
				return nil, err
			}
			for index := 0; index < instruction.C(); index++ {
				slotValue := value.NilValue()
				if index < len(results) {
					slotValue = results[index]
				}
				if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A()+3+index, slotValue); err != nil {
					return nil, err
				}
			}
			firstResult, err := engine.registerValue(proto, frame, slots, currentPC, instruction.A()+3)
			if err != nil {
				return nil, err
			}
			if !firstResult.IsBoxedTag(value.TagNil) {
				if err := engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A()+2, firstResult); err != nil {
					return nil, err
				}
				if err := engine.takeTestJump(proto, code, &pc, currentPC, opcode); err != nil {
					return nil, err
				}
			} else {
				pc++
			}
		case bytecode.OP_SETLIST:
			if err := engine.executeSetList(thread, frame, proto, slots, top, &pc, currentPC, instruction); err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
		case bytecode.OP_CLOSE:
			registerBase, err := thread.SlotIndexForAddress(uintptr(frame.RegsBase))
			if err != nil {
				return nil, err
			}
			address, err := thread.SlotAddress(registerBase + uint32(instruction.A()))
			if err != nil {
				return nil, err
			}
			limit := uintptr(frame.RegsBase) + uintptr(frame.RegisterCount)*value.TValueSize
			if _, err := engine.CloseUpvaluesInRangeBoundary(thread, address, limit); err != nil {
				return nil, err
			}
		case bytecode.OP_CLOSURE:
			childProtoIndex := instruction.Bx()
			if childProtoIndex < 0 || childProtoIndex >= len(proto.Protos) {
				return nil, runtimeError(proto, currentPC, opcode, fmt.Sprintf("child proto %d is out of range", childProtoIndex))
			}
			childProto := proto.Protos[childProtoIndex]
			capturedRefs, err := engine.captureUpvalues(thread, frame, proto, closureRef, code, &pc, currentPC, opcode, childProto)
			if err != nil {
				return nil, err
			}
			env, err := engine.closureEnv(closureRef)
			if err != nil {
				return nil, err
			}
			if err := engine.NewClosureBoundary(childProto, env, capturedRefs, func(closureValue value.TValue) error {
				return engine.setRegister(proto, frame, slots, &top, currentPC, instruction.A(), closureValue)
			}); err != nil {
				return nil, err
			}
		case bytecode.OP_VARARG:
			if err := engine.storeVarargs(proto, frame, slots, &top, currentPC, instruction.A(), instruction.B(), varargs); err != nil {
				return nil, err
			}
		default:
			return nil, runtimeError(proto, currentPC, opcode, "opcode not implemented in Stage 4 interpreter")
		}
	}
	return nil, runtimeError(proto, len(code), bytecode.OP_RETURN, "function fell off the end without RETURN")
}

func (engine *Engine) registerValue(proto *bytecode.Proto, frame *state.CallFrameHeader, slots []value.TValue, pc int, index int) (value.TValue, error) {
	if index < 0 {
		return value.NilValue(), runtimeError(proto, pc, bytecode.OP_MOVE, fmt.Sprintf("register %d is out of range", index))
	}
	if index >= len(slots) {
		return value.NilValue(), runtimeError(proto, pc, bytecode.OP_MOVE, fmt.Sprintf("register %d is out of range", index))
	}
	return slots[index], nil
}

func (engine *Engine) setRegister(proto *bytecode.Proto, frame *state.CallFrameHeader, slots []value.TValue, top *uint32, pc int, index int, slotValue value.TValue) error {
	if index < 0 {
		return runtimeError(proto, pc, bytecode.OP_MOVE, fmt.Sprintf("register %d is out of range", index))
	}
	if index >= len(slots) {
		return runtimeError(proto, pc, bytecode.OP_MOVE, fmt.Sprintf("register %d is out of range", index))
	}
	slots[index] = slotValue
	if uint32(index+1) > *top {
		if err := engine.setFrameTop(frame, top, uint32(index+1)); err != nil {
			return err
		}
	}
	return nil
}

func (engine *Engine) setFrameTop(frame *state.CallFrameHeader, top *uint32, newTop uint32) error {
	capacity := uint32(frame.RegisterCount) + uint32(frame.SpillCount)
	if newTop > capacity {
		return fmt.Errorf("frame top %d exceeds slot capacity %d", newTop, capacity)
	}
	*top = newTop
	return frame.SetTop(uint16(newTop))
}

func (engine *Engine) closureEnv(closureRef value.HeapRef44) (value.TValue, error) {
	return engine.Closures.Env(closureRef)
}

func (engine *Engine) closureUpvalueRef(closureRef value.HeapRef44, index int) (value.HeapRef44, error) {
	return engine.Closures.UpvalueRefAt(closureRef, index)
}

func (engine *Engine) validateFrameConstBase(frame *state.CallFrameHeader, proto *bytecode.Proto) error {
	if len(proto.Constants) == 0 {
		return nil
	}
	if frame.ConstBase == 0 {
		return fmt.Errorf("frame const base is not set")
	}
	baseOffset, err := engine.Heap.OffsetForNativeAddress(uintptr(frame.ConstBase))
	if err != nil {
		return fmt.Errorf("frame const base %#x is not heap-backed: %w", frame.ConstBase, err)
	}
	if _, err := engine.Heap.Resolve(baseOffset, uint64(len(proto.Constants))*value.TValueSize); err != nil {
		return err
	}
	return nil
}

func (engine *Engine) constantValue(frame *state.CallFrameHeader, proto *bytecode.Proto, index int) (value.TValue, error) {
	if index < 0 || index >= len(proto.Constants) {
		return value.NilValue(), fmt.Errorf("constant %d is out of range", index)
	}
	address := uintptr(frame.ConstBase) + uintptr(index)*value.TValueSize
	return *(*value.TValue)(unsafe.Pointer(address)), nil
}

func (engine *Engine) loopNumberValue(proto *bytecode.Proto, frame *state.CallFrameHeader, slots []value.TValue, top *uint32, pc int, opcode bytecode.Opcode, index int, role string) (float64, error) {
	slotValue, err := engine.registerValue(proto, frame, slots, pc, index)
	if err != nil {
		return 0, err
	}
	numberValue, ok, err := rtlua.ToNumber(slotValue, engine.Strings.Text)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, runtimeError(proto, pc, opcode, fmt.Sprintf("for %s must be a number", role))
	}
	number, _ := numberValue.Float64()
	if err := engine.setRegister(proto, frame, slots, top, pc, index, numberValue); err != nil {
		return 0, err
	}
	return number, nil
}

func (engine *Engine) executeSetList(thread *state.ThreadState, frame *state.CallFrameHeader, proto *bytecode.Proto, slots []value.TValue, top uint32, pc *int, currentPC int, instruction bytecode.Instruction) error {
	tableValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.A())
	if err != nil {
		return err
	}
	n := instruction.B()
	if n == 0 {
		if top <= uint32(instruction.A()+1) {
			n = 0
		} else {
			n = int(top) - instruction.A() - 1
		}
	}
	block := instruction.C()
	if block == 0 {
		if *pc >= len(proto.Code) {
			return fmt.Errorf("SETLIST expects trailing extra argument instruction")
		}
		block = int(proto.Code[*pc])
		*pc++
	}
	const fieldsPerFlush = 50
	baseIndex := (block - 1) * fieldsPerFlush
	for index := 1; index <= n; index++ {
		slotValue, err := engine.registerValue(proto, frame, slots, currentPC, instruction.A()+index)
		if err != nil {
			return err
		}
		key := value.NumberValue(float64(baseIndex + index))
		if err := engine.setTable(thread, tableValue, key, slotValue); err != nil {
			return err
		}
	}
	return nil
}

func (engine *Engine) rkValue(frame *state.CallFrameHeader, proto *bytecode.Proto, slots []value.TValue, pc int, operand int) (value.TValue, error) {
	if bytecode.IsConstantRK(operand) {
		return engine.constantValue(frame, proto, bytecode.IndexK(operand))
	}
	return engine.registerValue(proto, frame, slots, pc, operand)
}

func (engine *Engine) registerNumber(proto *bytecode.Proto, frame *state.CallFrameHeader, slots []value.TValue, pc int, opcode bytecode.Opcode, index int) (float64, error) {
	registerValue, err := engine.registerValue(proto, frame, slots, pc, index)
	if err != nil {
		return 0, err
	}
	numberValue, ok, err := rtlua.ToNumber(registerValue, engine.Strings.Text)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, runtimeError(proto, pc, opcode, fmt.Sprintf("register %d is not a number: %s", index, registerValue))
	}
	number, _ := numberValue.Float64()
	return number, nil
}

func (engine *Engine) rkNumber(frame *state.CallFrameHeader, proto *bytecode.Proto, slots []value.TValue, pc int, opcode bytecode.Opcode, operand int) (float64, error) {
	v, err := engine.rkValue(frame, proto, slots, pc, operand)
	if err != nil {
		return 0, err
	}
	numberValue, ok, err := rtlua.ToNumber(v, engine.Strings.Text)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, runtimeError(proto, pc, opcode, fmt.Sprintf("operand %d is not a number: %s", operand, v))
	}
	number, _ := numberValue.Float64()
	return number, nil
}

func (engine *Engine) collectCallArguments(proto *bytecode.Proto, frame *state.CallFrameHeader, slots []value.TValue, top uint32, pc int, a int, b int) (value.TValue, []value.TValue, error) {
	callee, err := engine.registerValue(proto, frame, slots, pc, a)
	if err != nil {
		return value.NilValue(), nil, err
	}
	argumentCount := 0
	if b == 0 {
		if top <= uint32(a+1) {
			argumentCount = 0
		} else {
			argumentCount = int(top) - a - 1
		}
	} else {
		argumentCount = b - 1
	}
	arguments := make([]value.TValue, 0, argumentCount)
	for index := 0; index < argumentCount; index++ {
		argument, err := engine.registerValue(proto, frame, slots, pc, a+1+index)
		if err != nil {
			return value.NilValue(), nil, err
		}
		arguments = append(arguments, argument)
	}
	return callee, arguments, nil
}

func (engine *Engine) storeCallResults(proto *bytecode.Proto, frame *state.CallFrameHeader, slots []value.TValue, top *uint32, pc int, a int, c int, results []value.TValue) error {
	if c == 1 {
		if err := engine.setFrameTop(frame, top, uint32(a)); err != nil {
			return err
		}
		return nil
	}
	if c == 0 {
		for index, result := range results {
			if err := engine.setRegister(proto, frame, slots, top, pc, a+index, result); err != nil {
				return err
			}
		}
		if err := engine.setFrameTop(frame, top, uint32(a+len(results))); err != nil {
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
		if err := engine.setRegister(proto, frame, slots, top, pc, a+index, slotValue); err != nil {
			return err
		}
	}
	return engine.setFrameTop(frame, top, uint32(a+wanted))
}

func (engine *Engine) collectReturnValues(proto *bytecode.Proto, frame *state.CallFrameHeader, slots []value.TValue, top uint32, pc int, a int, b int) ([]value.TValue, error) {
	if b == 1 {
		return nil, nil
	}
	count := 0
	if b == 0 {
		if top <= uint32(a) {
			count = 0
		} else {
			count = int(top) - a
		}
	} else {
		count = b - 1
	}
	results := make([]value.TValue, 0, count)
	for index := 0; index < count; index++ {
		slotValue, err := engine.registerValue(proto, frame, slots, pc, a+index)
		if err != nil {
			return nil, err
		}
		results = append(results, slotValue)
	}
	return results, nil
}

func (engine *Engine) storeVarargs(proto *bytecode.Proto, frame *state.CallFrameHeader, slots []value.TValue, top *uint32, pc int, a int, b int, varargs []value.TValue) error {
	if b == 0 {
		for index, slotValue := range varargs {
			if err := engine.setRegister(proto, frame, slots, top, pc, a+index, slotValue); err != nil {
				return err
			}
		}
		if err := engine.setFrameTop(frame, top, uint32(a+len(varargs))); err != nil {
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
		if err := engine.setRegister(proto, frame, slots, top, pc, a+index, slotValue); err != nil {
			return err
		}
	}
	if uint32(a+wanted) > *top {
		if err := engine.setFrameTop(frame, top, uint32(a+wanted)); err != nil {
			return err
		}
	}
	return nil
}

func (engine *Engine) captureUpvalues(thread *state.ThreadState, frame *state.CallFrameHeader, proto *bytecode.Proto, closureRef value.HeapRef44, code []bytecode.Instruction, pc *int, currentPC int, opcode bytecode.Opcode, childProto *bytecode.Proto) ([]value.HeapRef44, error) {
	registerBase, err := thread.SlotIndexForAddress(uintptr(frame.RegsBase))
	if err != nil {
		return nil, err
	}
	captured := make([]value.HeapRef44, int(childProto.NumUpvalues))
	for index := range captured {
		if *pc >= len(code) {
			return nil, runtimeError(proto, currentPC, opcode, "missing capture instruction after CLOSURE")
		}
		capture := code[*pc]
		*pc++
		switch capture.Opcode() {
		case bytecode.OP_MOVE:
			address, err := thread.SlotAddress(registerBase + uint32(capture.B()))
			if err != nil {
				return nil, err
			}
			handle, err := engine.Upvalues.FindOrCreateOpen(thread, address)
			if err != nil {
				return nil, err
			}
			captured[index] = handle.Ref
		case bytecode.OP_GETUPVAL:
			upvalueRef, err := engine.closureUpvalueRef(closureRef, capture.B())
			if err != nil {
				return nil, runtimeError(proto, currentPC, opcode, err.Error())
			}
			captured[index] = upvalueRef
		default:
			return nil, runtimeError(proto, currentPC, opcode, fmt.Sprintf("invalid upvalue capture opcode %s", capture.Opcode()))
		}
	}
	return captured, nil
}

func (engine *Engine) getTable(thread *state.ThreadState, tableValue value.TValue, key value.TValue) (value.TValue, bool, error) {
	return engine.ReadIndexMetaBoundary(thread, tableValue, key)
}

func (engine *Engine) setTable(thread *state.ThreadState, tableValue value.TValue, key value.TValue, slotValue value.TValue) error {
	return engine.WriteIndexMetaBoundary(thread, tableValue, key, slotValue)
}

func (engine *Engine) hostKeyString(key value.TValue) (string, error) {
	if !key.IsBoxedTag(value.TagStringRef) {
		return "", fmt.Errorf("host bridge currently only supports string property keys, got %s", key)
	}
	ref, _ := key.HeapRef()
	return engine.Strings.Text(ref)
}

func (engine *Engine) hostPropertyKey(key value.TValue) (string, bool) {
	if !key.IsBoxedTag(value.TagStringRef) {
		return "", false
	}
	ref, _ := key.HeapRef()
	text, err := engine.Strings.Text(ref)
	if err != nil {
		return "", false
	}
	return text, true
}

func (engine *Engine) takeTestJump(proto *bytecode.Proto, code []bytecode.Instruction, pc *int, currentPC int, opcode bytecode.Opcode) error {
	if *pc >= len(code) {
		return runtimeError(proto, currentPC, opcode, "test opcode is missing trailing JMP")
	}
	jump := code[*pc]
	if jump.Opcode() != bytecode.OP_JMP {
		return runtimeError(proto, currentPC, opcode, fmt.Sprintf("expected trailing JMP after test, got %s", jump.Opcode()))
	}
	*pc += 1 + jump.SBx()
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
	if frame.VarargCount == 0 {
		return nil, nil
	}
	if frame.VarargBase == 0 {
		return nil, fmt.Errorf("frame has %d varargs but no vararg base", frame.VarargCount)
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

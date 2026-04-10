package baseline

import (
	"fmt"
	"math"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	rtclosure "vexlua/internal/runtime/closure"
	rtproto "vexlua/internal/runtime/proto"
	rtstate "vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/amd64"
	"vexlua/internal/vexarc/codecache"
	"vexlua/internal/vexarc/metadata"
)

type Compiler struct {
	engine *interp.Engine
	cache  *codecache.Cache
}

func NewCompiler(engine *interp.Engine, cache *codecache.Cache) *Compiler {
	if engine == nil {
		panic("baseline compiler requires an interpreter engine")
	}
	if cache == nil {
		panic("baseline compiler requires a code cache")
	}
	return &Compiler{engine: engine, cache: cache}
}

func (compiler *Compiler) Compile(proto *bytecode.Proto) (*CompiledCode, error) {
	compiled := &CompiledCode{
		Proto:     proto,
		Metadata:  metadata.NewCodeMetadata(len(proto.Code)),
		Supported: true,
	}
	state := &compileState{
		compiler:  compiler,
		proto:     proto,
		assembler: amd64.NewAssembler(maxInt(64, len(proto.Code)*24)),
		metadata:  compiled.Metadata,
		labels:    make(map[int]*amd64.Label),
	}
	if err := state.preflight(); err != nil {
		compiled.Supported = false
		compiled.UnsupportedReason = err.Error()
		return compiled, nil
	}
	iterator := NewBytecodeIterator(proto)
	for !iterator.Done() {
		offset := iterator.CurrentOffset()
		if err := state.beginInstruction(offset); err != nil {
			return nil, err
		}
		if err := state.emitInstruction(offset, iterator.Current()); err != nil {
			return nil, err
		}
		iterator.Advance()
	}
	state.emitStatus(compiledStatusDeopt, 0)
	compiled.Metadata = state.metadata
	compiled.Metadata.Finalize(&state.offsets)
	block, err := compiler.cache.Install(state.assembler.Buffer().Bytes())
	if err != nil {
		return nil, err
	}
	compiled.Block = block
	compiled.Entry = block.Address()
	return compiled, nil
}

type compileState struct {
	compiler  *Compiler
	proto     *bytecode.Proto
	assembler *amd64.Assembler
	metadata  metadata.CodeMetadata
	offsets   metadata.OffsetTableBuilder
	labels    map[int]*amd64.Label
}

func (state *compileState) preflight() error {
	if state.proto == nil {
		return fmt.Errorf("proto cannot be nil")
	}
	for index, constant := range state.proto.Constants {
		switch constant.Kind {
		case bytecode.ConstantNil, bytecode.ConstantBoolean, bytecode.ConstantNumber, bytecode.ConstantString:
		default:
			return fmt.Errorf("constant %d: unsupported constant kind %s", index, constant.Kind)
		}
	}
	for offset, instruction := range state.proto.Code {
		switch instruction.Opcode() {
		case bytecode.OP_MOVE, bytecode.OP_LOADK, bytecode.OP_LOADBOOL, bytecode.OP_LOADNIL, bytecode.OP_JMP, bytecode.OP_CALL, bytecode.OP_TAILCALL, bytecode.OP_RETURN, bytecode.OP_FORPREP, bytecode.OP_FORLOOP:
		default:
			return fmt.Errorf("opcode %s is not lowered in Stage 6", instruction.Opcode())
		}
		switch instruction.Opcode() {
		case bytecode.OP_LOADK:
			if instruction.Bx() < 0 || instruction.Bx() >= len(state.proto.Constants) {
				return fmt.Errorf("LOADK constant index out of range at pc %d", offset)
			}
		case bytecode.OP_JMP:
			target := offset + 1 + instruction.SBx()
			if err := state.validateTarget(target, offset, instruction.Opcode()); err != nil {
				return err
			}
			state.labelFor(target)
		case bytecode.OP_LOADBOOL:
			if instruction.C() != 0 {
				target := offset + 2
				if err := state.validateTarget(target, offset, instruction.Opcode()); err != nil {
					return err
				}
				state.labelFor(target)
			}
		case bytecode.OP_CALL:
			if instruction.B() == 0 || instruction.C() == 0 {
				return fmt.Errorf("open CALL forms are not lowered in Stage 6 at pc %d", offset)
			}
		case bytecode.OP_TAILCALL:
			if instruction.B() == 0 || instruction.C() != 0 {
				return fmt.Errorf("open TAILCALL forms are not lowered in Stage 6 at pc %d", offset)
			}
		case bytecode.OP_RETURN:
			if instruction.B() == 0 {
				return fmt.Errorf("open RETURN form is not lowered in Stage 6 at pc %d", offset)
			}
		case bytecode.OP_FORPREP, bytecode.OP_FORLOOP:
			target := offset + 1 + instruction.SBx()
			if err := state.validateTarget(target, offset, instruction.Opcode()); err != nil {
				return err
			}
			state.labelFor(target)
		}
	}
	return nil
}

func (state *compileState) validateTarget(target int, pc int, opcode bytecode.Opcode) error {
	if target < 0 || target >= len(state.proto.Code) {
		return fmt.Errorf("%s target %d is out of range at pc %d", opcode, target, pc)
	}
	return nil
}

func (state *compileState) beginInstruction(offset int) error {
	if label, ok := state.labels[offset]; ok {
		if err := state.assembler.Bind(label); err != nil {
			return err
		}
	}
	codeOffset := uint32(state.assembler.Buffer().Pos())
	if err := state.metadata.RecordBytecodeOffset(offset, codeOffset); err != nil {
		return err
	}
	if err := state.offsets.AddPosition(codeOffset); err != nil {
		return err
	}
	state.assembler.MoveMemImm32(amd64.RegR13, state.CallFrameSavedBCOffOffset(), uint32(offset))
	return nil
}

func (state *compileState) emitInstruction(offset int, instruction bytecode.Instruction) error {
	switch instruction.Opcode() {
	case bytecode.OP_MOVE:
		state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(instruction.B()))
		state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(instruction.A()), amd64.RegRAX)
	case bytecode.OP_LOADK:
		state.emitLoadConstant(instruction.A(), instruction.Bx())
	case bytecode.OP_LOADBOOL:
		state.emitStoreRawTValue(instruction.A(), uint64(value.BoolValue(instruction.B() != 0).Bits()))
		if instruction.C() != 0 {
			state.assembler.Jmp(state.labelFor(offset + 2))
		}
	case bytecode.OP_LOADNIL:
		for index := instruction.A(); index <= instruction.B(); index++ {
			state.emitStoreRawTValue(index, uint64(value.NilValue().Bits()))
		}
	case bytecode.OP_JMP:
		state.assembler.Jmp(state.labelFor(offset + 1 + instruction.SBx()))
	case bytecode.OP_CALL:
		state.emitCall(instruction.A(), instruction.B(), instruction.C(), uint32(offset+1))
	case bytecode.OP_TAILCALL:
		state.emitTailCall(instruction.A(), instruction.B())
	case bytecode.OP_RETURN:
		count := instruction.B() - 1
		state.assembler.MoveRegMem64(amd64.RegR10, amd64.RegR13, state.CallFrameResultBaseOffset())
		for index := 0; index < count; index++ {
			state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(instruction.A()+index))
			state.assembler.MoveMemReg64(amd64.RegR10, slotDisp(index), amd64.RegRAX)
		}
		state.emitStatus(compiledStatusOK, uint32(count))
	case bytecode.OP_FORPREP:
		state.emitForPrep(instruction.A(), offset+1+instruction.SBx())
	case bytecode.OP_FORLOOP:
		state.emitForLoop(instruction.A(), offset+1, offset+1+instruction.SBx())
	default:
		return fmt.Errorf("unexpected opcode %s", instruction.Opcode())
	}
	return nil
}

func (state *compileState) emitSuspend(kind SuspendKind, resumePC uint32, arg0 uint32, arg1 uint32, arg2 uint32) {
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxResumePCOffset, resumePC)
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxSuspendKindOffset, uint32(kind))
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxArg0Offset, arg0)
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxArg1Offset, arg1)
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxArg2Offset, arg2)
	state.emitStatus(compiledStatusSuspend, 0)
}

func (state *compileState) emitStatus(status uint32, aux uint32) {
	if status == 0 {
		state.assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	} else {
		state.assembler.MoveRegImm32(amd64.RegRAX, status)
	}
	if aux == 0 {
		state.assembler.XorRegReg(amd64.RegRDX, amd64.RegRDX)
	} else {
		state.assembler.MoveRegImm32(amd64.RegRDX, aux)
	}
	state.assembler.Ret()
}

func (state *compileState) emitStoreRawTValue(slot int, bits uint64) {
	state.assembler.MoveRegImm64(amd64.RegRAX, bits)
	state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(slot), amd64.RegRAX)
}

func (state *compileState) emitLoadConstant(slot int, index int) {
	state.assembler.MoveRegMem64(amd64.RegR10, amd64.RegR13, state.CallFrameConstBaseOffset())
	if index != 0 {
		state.assembler.AddRegImm32(amd64.RegR10, slotDisp(index))
	}
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR10, 0)
	state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(slot), amd64.RegRAX)
}

func (state *compileState) emitCall(a int, b int, c int, resumePC uint32) {
	fallback := state.assembler.NewLabel()
	deopt := state.assembler.NewLabel()
	done := state.assembler.NewLabel()
	childRegCountReady := state.assembler.NewLabel()
	clearLoop := state.assembler.NewLabel()
	clearDone := state.assembler.NewLabel()

	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(a))
	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegRCX, value.TagShift)
	state.assembler.CmpRegImm32(amd64.RegRCX, state.shiftedBoxedTag(value.TagLuaClosureRef))
	state.assembler.Jcc(amd64.CondNotEqual, fallback)

	state.emitDecodeHeapRefFromTValue(amd64.RegRCX, amd64.RegRAX)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRCX, rtclosure.ProtoOffset)
	state.assembler.MoveRegReg(amd64.RegRDX, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegRDX, value.TagShift)
	state.assembler.CmpRegImm32(amd64.RegRDX, state.shiftedBoxedTag(value.TagProtoRef))
	state.assembler.Jcc(amd64.CondNotEqual, fallback)

	state.emitDecodeHeapRefFromTValue(amd64.RegRDX, amd64.RegRAX)
	state.assembler.MoveRegMem64(amd64.RegR10, amd64.RegRDX, rtproto.CompiledEntryOff)
	state.assembler.CmpRegImm32(amd64.RegR10, 0)
	state.assembler.Jcc(amd64.CondEqual, fallback)

	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRDX, rtproto.ProtoCountOff)
	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegRCX, 48)
	state.assembler.AndRegImm32(amd64.RegRCX, 0xFF)
	state.assembler.CmpRegImm32(amd64.RegRCX, 0)
	state.assembler.Jcc(amd64.CondNotEqual, fallback)

	state.assembler.MoveRegReg(amd64.RegR8, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegR8, 32)
	state.assembler.AndRegImm32(amd64.RegR8, 0xFF)
	state.assembler.CmpRegImm32(amd64.RegR8, 0)
	state.assembler.Jcc(amd64.CondNotEqual, childRegCountReady)
	state.assembler.MoveRegImm32(amd64.RegR8, 1)
	_ = state.assembler.Bind(childRegCountReady)

	state.assembler.MoveRegReg(amd64.RegR9, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegR9, 40)
	state.assembler.AndRegImm32(amd64.RegR9, 0xFF)

	state.assembler.MoveRegMem64(amd64.RegRCX, amd64.RegR13, state.CallFrameVarargCountOffset())
	state.assembler.MoveRegReg(amd64.RegRBX, amd64.RegRCX)
	state.assembler.ShiftRightRegImm8(amd64.RegRBX, 32)
	state.assembler.AndRegImm32(amd64.RegRBX, 0xFFFF)
	state.assembler.ShiftRightRegImm8(amd64.RegRCX, 48)
	state.assembler.AddRegReg(amd64.RegRBX, amd64.RegRCX)
	state.assembler.ShiftLeftRegImm8(amd64.RegRBX, 3)
	state.assembler.MoveRegReg(amd64.RegRSI, amd64.RegR12)
	state.assembler.AddRegReg(amd64.RegRSI, amd64.RegRBX)
	state.assembler.MoveRegReg(amd64.RegRDI, amd64.RegR13)
	state.assembler.AddRegImm32(amd64.RegRDI, rtstate.CallFrameHeaderSize)

	state.assembler.MoveRegReg(amd64.RegRAX, amd64.RegR8)
	state.assembler.ShiftLeftRegImm8(amd64.RegRAX, 4)
	state.assembler.AddRegReg(amd64.RegRAX, amd64.RegRSI)
	state.assembler.MoveRegMem64(amd64.RegRCX, amd64.RegR14, state.VMStateActiveThreadStackEndOffset())
	state.assembler.CmpRegReg(amd64.RegRAX, amd64.RegRCX)
	state.assembler.Jcc(amd64.CondAbove, fallback)
	state.assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDI)
	state.assembler.AddRegImm32(amd64.RegRAX, rtstate.CallFrameHeaderSize)
	state.assembler.MoveRegMem64(amd64.RegRCX, amd64.RegR14, state.VMStateActiveThreadFrameEndOffset())
	state.assembler.CmpRegReg(amd64.RegRAX, amd64.RegRCX)
	state.assembler.Jcc(amd64.CondAbove, fallback)

	state.assembler.MoveRegImm64(amd64.RegRAX, uint64(value.NilValue().Bits()))
	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegRSI)
	state.assembler.MoveRegReg(amd64.RegRBX, amd64.RegR8)
	_ = state.assembler.Bind(clearLoop)
	state.assembler.CmpRegImm32(amd64.RegRBX, 0)
	state.assembler.Jcc(amd64.CondEqual, clearDone)
	state.assembler.MoveMemReg64(amd64.RegRCX, 0, amd64.RegRAX)
	state.assembler.AddRegImm32(amd64.RegRCX, value.TValueSize)
	state.assembler.AddRegImm32(amd64.RegRBX, -1)
	state.assembler.Jmp(clearLoop)
	_ = state.assembler.Bind(clearDone)

	for index := 0; index < b-1; index++ {
		skipCopy := state.assembler.NewLabel()
		state.assembler.CmpRegImm32(amd64.RegR9, uint32(index+1))
		state.assembler.Jcc(amd64.CondBelow, skipCopy)
		state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(a+1+index))
		state.assembler.MoveMemReg64(amd64.RegRSI, slotDisp(index), amd64.RegRAX)
		_ = state.assembler.Bind(skipCopy)
	}

	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegR8)
	state.assembler.ShiftLeftRegImm8(amd64.RegRCX, 3)
	state.assembler.AddRegReg(amd64.RegRCX, amd64.RegRSI)

	state.assembler.MoveMemReg64(amd64.RegRDI, state.CallFramePrevFrameOffset(), amd64.RegR13)
	state.assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	state.assembler.MoveMemReg64(amd64.RegRDI, state.CallFrameCallerRetPCOffset(), amd64.RegRAX)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(a))
	state.assembler.MoveMemReg64(amd64.RegRDI, state.CallFrameClosureOffset(), amd64.RegRAX)
	state.assembler.MoveRegReg(amd64.RegRBX, amd64.RegRAX)
	state.emitDecodeHeapRefFromTValue(amd64.RegRBX, amd64.RegRBX)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRBX, rtclosure.ProtoOffset)
	state.assembler.MoveMemReg64(amd64.RegRDI, state.CallFrameProtoOffset(), amd64.RegRAX)
	state.assembler.MoveMemReg64(amd64.RegRDI, state.CallFrameRegsBaseOffset(), amd64.RegRSI)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRDX, rtproto.ConstBasePtrOff)
	state.assembler.MoveMemReg64(amd64.RegRDI, state.CallFrameConstBaseOffset(), amd64.RegRAX)
	state.assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	state.assembler.MoveMemReg64(amd64.RegRDI, state.CallFrameVarargBaseOffset(), amd64.RegRAX)
	state.assembler.MoveMemImm32(amd64.RegRDI, state.CallFrameSavedBCOffOffset(), 0)
	state.assembler.MoveMemImm32(amd64.RegRDI, state.CallFrameFlagsOffset(), state.packLuaFrameFlagsNResults(c-1))
	state.assembler.MoveMemImm32(amd64.RegRDI, state.CallFrameVarargCountOffset(), 0)
	state.assembler.MoveRegReg(amd64.RegRAX, amd64.RegR8)
	state.assembler.MoveRegReg(amd64.RegRBX, amd64.RegR8)
	state.assembler.ShiftLeftRegImm8(amd64.RegRBX, 16)
	state.assembler.AddRegReg(amd64.RegRAX, amd64.RegRBX)
	state.assembler.MoveMemReg32(amd64.RegRDI, state.CallFrameRegisterCountOffset(), amd64.RegRAX)
	state.assembler.MoveMemReg64(amd64.RegRDI, state.CallFrameResultBaseOffset(), amd64.RegRCX)

	state.assembler.MoveRegReg(amd64.RegR13, amd64.RegRDI)
	state.assembler.MoveRegReg(amd64.RegR12, amd64.RegRSI)
	state.assembler.CallReg(amd64.RegR10)
	state.assembler.CmpRegImm32(amd64.RegRAX, compiledStatusOK)
	state.assembler.Jcc(amd64.CondNotEqual, deopt)

	state.assembler.MoveRegMem64(amd64.RegRSI, amd64.RegR13, state.CallFrameResultBaseOffset())
	state.assembler.MoveRegMem64(amd64.RegR13, amd64.RegR13, state.CallFramePrevFrameOffset())
	state.assembler.MoveRegMem64(amd64.RegR12, amd64.RegR13, state.CallFrameRegsBaseOffset())
	for index := 0; index < c-1; index++ {
		copyResult := state.assembler.NewLabel()
		doneResult := state.assembler.NewLabel()
		state.assembler.CmpRegImm32(amd64.RegRDX, uint32(index+1))
		state.assembler.Jcc(amd64.CondAboveEqual, copyResult)
		state.assembler.MoveRegImm64(amd64.RegRAX, uint64(value.NilValue().Bits()))
		state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(a+index), amd64.RegRAX)
		state.assembler.Jmp(doneResult)
		_ = state.assembler.Bind(copyResult)
		state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRSI, slotDisp(index))
		state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(a+index), amd64.RegRAX)
		_ = state.assembler.Bind(doneResult)
	}
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(deopt)
	state.emitStatus(compiledStatusDeopt, 0)
	_ = state.assembler.Bind(fallback)
	state.emitSuspend(SuspendCall, resumePC, uint32(a), uint32(b), uint32(c))
	_ = state.assembler.Bind(done)
}

func (state *compileState) emitTailCall(a int, b int) {
	fallback := state.assembler.NewLabel()
	childRegCountReady := state.assembler.NewLabel()
	copiedCountReady := state.assembler.NewLabel()
	clearLoop := state.assembler.NewLabel()
	clearDone := state.assembler.NewLabel()

	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(a))
	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegRCX, value.TagShift)
	state.assembler.CmpRegImm32(amd64.RegRCX, state.shiftedBoxedTag(value.TagLuaClosureRef))
	state.assembler.Jcc(amd64.CondNotEqual, fallback)

	state.emitDecodeHeapRefFromTValue(amd64.RegRCX, amd64.RegRAX)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRCX, rtclosure.ProtoOffset)
	state.assembler.MoveRegReg(amd64.RegRDX, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegRDX, value.TagShift)
	state.assembler.CmpRegImm32(amd64.RegRDX, state.shiftedBoxedTag(value.TagProtoRef))
	state.assembler.Jcc(amd64.CondNotEqual, fallback)

	state.emitDecodeHeapRefFromTValue(amd64.RegRDX, amd64.RegRAX)
	state.assembler.MoveRegMem64(amd64.RegR10, amd64.RegRDX, rtproto.CompiledEntryOff)
	state.assembler.CmpRegImm32(amd64.RegR10, 0)
	state.assembler.Jcc(amd64.CondEqual, fallback)

	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRDX, rtproto.ProtoCountOff)
	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegRCX, 48)
	state.assembler.AndRegImm32(amd64.RegRCX, 0xFF)
	state.assembler.CmpRegImm32(amd64.RegRCX, 0)
	state.assembler.Jcc(amd64.CondNotEqual, fallback)

	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegRCX, 56)
	state.assembler.AndRegImm32(amd64.RegRCX, uint32(rtproto.ProtoCompiledFlagNoSuspend))
	state.assembler.CmpRegImm32(amd64.RegRCX, uint32(rtproto.ProtoCompiledFlagNoSuspend))
	state.assembler.Jcc(amd64.CondNotEqual, fallback)

	state.assembler.MoveRegReg(amd64.RegR8, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegR8, 32)
	state.assembler.AndRegImm32(amd64.RegR8, 0xFF)
	state.assembler.CmpRegImm32(amd64.RegR8, 0)
	state.assembler.Jcc(amd64.CondNotEqual, childRegCountReady)
	state.assembler.MoveRegImm32(amd64.RegR8, 1)
	_ = state.assembler.Bind(childRegCountReady)

	state.assembler.MoveRegReg(amd64.RegR9, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegR9, 40)
	state.assembler.AndRegImm32(amd64.RegR9, 0xFF)

	state.assembler.MoveRegMem32(amd64.RegRBX, amd64.RegR13, state.CallFrameRegisterCountOffset())
	state.assembler.AndRegImm32(amd64.RegRBX, 0xFFFF)
	state.assembler.CmpRegReg(amd64.RegRBX, amd64.RegR8)
	state.assembler.Jcc(amd64.CondBelow, fallback)

	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(a))
	state.assembler.MoveMemReg64(amd64.RegR13, state.CallFrameClosureOffset(), amd64.RegRAX)
	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegRAX)
	state.emitDecodeHeapRefFromTValue(amd64.RegRCX, amd64.RegRCX)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRCX, rtclosure.ProtoOffset)
	state.assembler.MoveMemReg64(amd64.RegR13, state.CallFrameProtoOffset(), amd64.RegRAX)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRDX, rtproto.ConstBasePtrOff)
	state.assembler.MoveMemReg64(amd64.RegR13, state.CallFrameConstBaseOffset(), amd64.RegRAX)

	for index := 0; index < b-1; index++ {
		skipCopy := state.assembler.NewLabel()
		state.assembler.CmpRegImm32(amd64.RegR9, uint32(index+1))
		state.assembler.Jcc(amd64.CondBelow, skipCopy)
		state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(a+1+index))
		state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(index), amd64.RegRAX)
		_ = state.assembler.Bind(skipCopy)
	}

	state.assembler.MoveRegReg(amd64.RegRSI, amd64.RegR9)
	state.assembler.CmpRegImm32(amd64.RegRSI, uint32(b-1))
	state.assembler.Jcc(amd64.CondBelowEqual, copiedCountReady)
	state.assembler.MoveRegImm32(amd64.RegRSI, uint32(b-1))
	_ = state.assembler.Bind(copiedCountReady)

	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegRSI)
	state.assembler.ShiftLeftRegImm8(amd64.RegRCX, 3)
	state.assembler.AddRegReg(amd64.RegRCX, amd64.RegR12)
	state.assembler.MoveRegReg(amd64.RegRAX, amd64.RegRBX)
	state.assembler.ShiftLeftRegImm8(amd64.RegRAX, 3)
	state.assembler.AddRegReg(amd64.RegRAX, amd64.RegR12)
	state.assembler.MoveRegImm64(amd64.RegRDX, uint64(value.NilValue().Bits()))
	_ = state.assembler.Bind(clearLoop)
	state.assembler.CmpRegReg(amd64.RegRCX, amd64.RegRAX)
	state.assembler.Jcc(amd64.CondAboveEqual, clearDone)
	state.assembler.MoveMemReg64(amd64.RegRCX, 0, amd64.RegRDX)
	state.assembler.AddRegImm32(amd64.RegRCX, value.TValueSize)
	state.assembler.Jmp(clearLoop)
	_ = state.assembler.Bind(clearDone)
	state.assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	state.assembler.MoveMemReg64(amd64.RegR13, state.CallFrameVarargBaseOffset(), amd64.RegRAX)
	state.assembler.MoveMemImm32(amd64.RegR13, state.CallFrameSavedBCOffOffset(), 0)
	state.assembler.MoveMemImm32(amd64.RegR13, state.CallFrameVarargCountOffset(), 0)

	state.assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR13, state.CallFrameFlagsOffset())
	state.assembler.AndRegImm32(amd64.RegRAX, 0xFFFF0000)
	state.assembler.AddRegImm32(amd64.RegRAX, int32(uint16(rtstate.FrameFlagIsLuaFrame|rtstate.FrameFlagIsTailcall)))
	state.assembler.MoveMemReg32(amd64.RegR13, state.CallFrameFlagsOffset(), amd64.RegRAX)

	state.assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR13, state.CallFrameRegisterCountOffset())
	state.assembler.AndRegImm32(amd64.RegRAX, 0xFFFF0000)
	state.assembler.AddRegReg(amd64.RegRAX, amd64.RegR8)
	state.assembler.MoveMemReg32(amd64.RegR13, state.CallFrameRegisterCountOffset(), amd64.RegRAX)

	state.assembler.JmpReg(amd64.RegR10)

	_ = state.assembler.Bind(fallback)
	state.emitStatus(compiledStatusDeopt, 0)
}

func (state *compileState) emitForPrep(a int, target int) {
	deopt := state.assembler.NewLabel()

	state.emitLoadNumericLoopSlot(a, amd64.XMM0, deopt)
	state.emitLoadNumericLoopSlot(a+1, amd64.XMM1, deopt)
	state.emitLoadNumericLoopSlot(a+2, amd64.XMM2, deopt)
	state.assembler.SubsdXmmXmm(amd64.XMM0, amd64.XMM2)
	state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(a), amd64.XMM0)
	state.assembler.Jmp(state.labelFor(target))

	_ = state.assembler.Bind(deopt)
	state.emitStatus(compiledStatusDeopt, 0)
}

func (state *compileState) emitForLoop(a int, resumePC int, target int) {
	deopt := state.assembler.NewLabel()
	positiveStep := state.assembler.NewLabel()
	continueLoop := state.assembler.NewLabel()
	done := state.assembler.NewLabel()

	state.emitLoadNumericLoopSlot(a, amd64.XMM0, deopt)
	state.emitLoadNumericLoopSlot(a+1, amd64.XMM1, deopt)
	state.emitLoadNumericLoopSlot(a+2, amd64.XMM2, deopt)
	state.assembler.AddsdXmmXmm(amd64.XMM0, amd64.XMM2)
	state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(a), amd64.XMM0)
	state.assembler.XorpsXmmXmm(amd64.XMM3, amd64.XMM3)
	state.assembler.UcomisdXmmXmm(amd64.XMM2, amd64.XMM3)
	state.assembler.Jcc(amd64.CondParity, deopt)
	state.assembler.Jcc(amd64.CondAbove, positiveStep)
	state.assembler.UcomisdXmmXmm(amd64.XMM1, amd64.XMM0)
	state.assembler.Jcc(amd64.CondParity, deopt)
	state.assembler.Jcc(amd64.CondBelowEqual, continueLoop)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(positiveStep)
	state.assembler.UcomisdXmmXmm(amd64.XMM0, amd64.XMM1)
	state.assembler.Jcc(amd64.CondParity, deopt)
	state.assembler.Jcc(amd64.CondBelowEqual, continueLoop)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(continueLoop)
	state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(a+3), amd64.XMM0)
	state.assembler.Jmp(state.labelFor(target))

	_ = state.assembler.Bind(done)
	state.assembler.Jmp(state.labelFor(resumePC))

	_ = state.assembler.Bind(deopt)
	state.emitStatus(compiledStatusDeopt, 0)
}

func (state *compileState) emitLoadNumericLoopSlot(slot int, dst amd64.XMMRegister, deopt *amd64.Label) {
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(slot))
	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegRCX, 48)
	state.assembler.CmpRegImm32(amd64.RegRCX, 0xFFFF)
	state.assembler.Jcc(amd64.CondEqual, deopt)
	state.assembler.MoveXmmMem64(dst, amd64.RegR12, slotDisp(slot))
	state.assembler.UcomisdXmmXmm(dst, dst)
	state.assembler.Jcc(amd64.CondParity, deopt)
}

func (state *compileState) emitDecodeHeapRefFromTValue(dst amd64.Register, src amd64.Register) {
	if dst != src {
		state.assembler.MoveRegReg(dst, src)
	}
	state.assembler.ShiftLeftRegImm8(dst, 20)
	state.assembler.ShiftRightRegImm8(dst, 16)
	state.assembler.AddRegReg(dst, amd64.HeapBaseRegister)
}

func (state *compileState) labelFor(offset int) *amd64.Label {
	label, ok := state.labels[offset]
	if ok {
		return label
	}
	label = state.assembler.NewLabel()
	state.labels[offset] = label
	return label
}

func (state *compileState) CallFrameSavedBCOffOffset() int32 {
	return int32(rtstate.CallFrameSavedBCOffOffset)
}

func (state *compileState) CallFramePrevFrameOffset() int32 {
	return int32(rtstate.CallFramePrevFrameOffset)
}

func (state *compileState) CallFrameCallerRetPCOffset() int32 {
	return int32(rtstate.CallFrameCallerRetPCOffset)
}

func (state *compileState) CallFrameClosureOffset() int32 {
	return int32(rtstate.CallFrameClosureOffset)
}

func (state *compileState) CallFrameProtoOffset() int32 {
	return int32(rtstate.CallFrameProtoOffset)
}

func (state *compileState) CallFrameRegsBaseOffset() int32 {
	return int32(rtstate.CallFrameRegsBaseOffset)
}

func (state *compileState) CallFrameConstBaseOffset() int32 {
	return int32(rtstate.CallFrameConstBaseOffset)
}

func (state *compileState) CallFrameVarargBaseOffset() int32 {
	return int32(rtstate.CallFrameVarargBaseOffset)
}

func (state *compileState) CallFrameFlagsOffset() int32 {
	return int32(rtstate.CallFrameFlagsOffset)
}

func (state *compileState) CallFrameVarargCountOffset() int32 {
	return int32(rtstate.CallFrameVarargCountOffset)
}

func (state *compileState) CallFrameRegisterCountOffset() int32 {
	return int32(rtstate.CallFrameRegisterCountOff)
}

func (state *compileState) CallFrameResultBaseOffset() int32 {
	return int32(rtstate.CallFrameResultBaseOffset)
}

func (state *compileState) VMStateActiveThreadStackEndOffset() int32 {
	return int32(rtstate.VMStateActiveThreadStackEndOff)
}

func (state *compileState) VMStateActiveThreadFrameEndOffset() int32 {
	return int32(rtstate.VMStateActiveThreadFrameEndOff)
}

func (state *compileState) shiftedBoxedTag(tag value.Tag) uint32 {
	return uint32((uint64(value.BoxedMarker) >> value.TagShift) | uint64(tag))
}

func (state *compileState) packLuaFrameFlagsNResults(nresults int) uint32 {
	return uint32(uint16(rtstate.FrameFlagIsLuaFrame)) | (uint32(uint16(int16(nresults))) << 16)
}

func slotDisp(index int) int32 {
	return int32(index * value.TValueSize)
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func normalizeRequestedResults(nresults int) int16 {
	if nresults < 0 {
		return -1
	}
	if nresults > math.MaxInt16 {
		return math.MaxInt16
	}
	return int16(nresults)
}

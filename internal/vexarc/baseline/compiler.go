package baseline

import (
	"fmt"
	"math"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/feedback"
	rtstate "vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/abi"
	"vexlua/internal/vexarc/amd64"
	"vexlua/internal/vexarc/codecache"
	"vexlua/internal/vexarc/metadata"
	"vexlua/internal/vexarc/stubs"
)

type Compiler struct {
	engine      *interp.Engine
	cache       *codecache.Cache
	stubEntries map[stubs.ID]uintptr
	deoptEntry  uintptr
}

func NewCompiler(engine *interp.Engine, cache *codecache.Cache, manager *stubManager) *Compiler {
	if engine == nil {
		panic("baseline compiler requires an interpreter engine")
	}
	if cache == nil {
		panic("baseline compiler requires a code cache")
	}
	if manager == nil {
		panic("baseline compiler requires shared stubs")
	}
	deoptEntry, err := manager.DeoptEntry()
	if err != nil {
		panic(err)
	}
	compiler := &Compiler{
		engine:      engine,
		cache:       cache,
		stubEntries: make(map[stubs.ID]uintptr),
		deoptEntry:  deoptEntry,
	}
	for _, id := range []stubs.ID{
		stubs.StubGetGlobal,
		stubs.StubGetTable,
		stubs.StubSetGlobal,
		stubs.StubSetTable,
		stubs.StubGetUpvalue,
		stubs.StubSetUpvalue,
		stubs.StubLuaCall,
		stubs.StubTailCall,
		stubs.StubForPrep,
		stubs.StubForLoop,
		stubs.StubSelf,
		stubs.StubLen,
		stubs.StubSetList,
		stubs.StubNewTable,
		stubs.StubConcat,
		stubs.StubClose,
		stubs.StubClosure,
	} {
		entry, err := manager.StubEntry(id)
		if err != nil {
			panic(err)
		}
		compiler.stubEntries[id] = entry
	}
	return compiler
}

func (compiler *Compiler) Compile(proto *bytecode.Proto) (*CompiledCode, error) {
	compiled := &CompiledCode{
		Proto:          proto,
		Metadata:       metadata.NewCodeMetadata(len(proto.Code)),
		FeedbackLayout: feedback.LayoutForProto(proto),
		Supported:      true,
	}
	state := &compileState{
		compiler:       compiler,
		proto:          proto,
		feedbackLayout: compiled.FeedbackLayout,
		assembler:      amd64.NewAssembler(maxInt(64, len(proto.Code)*24)),
		metadata:       compiled.Metadata,
		labels:         make(map[int]*amd64.Label),
	}
	if err := state.validateStructure(); err != nil {
		compiled.Supported = false
		compiled.UnsupportedReason = err.Error()
		return compiled, nil
	}
	liveSlots, err := analyzeLiveSlotSets(proto)
	if err != nil {
		return nil, err
	}
	for bytecodePC, liveSet := range liveSlots {
		if err := state.metadata.RecordBytecodeLiveSlots(bytecodePC, liveSet); err != nil {
			return nil, err
		}
	}
	if err := state.previsit(); err != nil {
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
	state.emitStatus(compiledStatusError, 0)
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
	compiler       *Compiler
	proto          *bytecode.Proto
	feedbackLayout *feedback.Layout
	assembler      *amd64.Assembler
	metadata       metadata.CodeMetadata
	offsets        metadata.OffsetTableBuilder
	labels         map[int]*amd64.Label
}

type instructionDisposition uint8

const (
	instructionDispositionCompiled instructionDisposition = iota
	instructionDispositionPayload
	instructionDispositionDeopt
)

func (state *compileState) dispositionForInstruction(offset int, instruction bytecode.Instruction) instructionDisposition {
	if state.isSetListExtraArgument(offset) {
		return instructionDispositionPayload
	}
	if state.isClosureCapturePayload(offset) {
		return instructionDispositionPayload
	}
	switch instruction.Opcode() {
	case bytecode.OP_MOVE,
		bytecode.OP_LOADK,
		bytecode.OP_LOADBOOL,
		bytecode.OP_LOADNIL,
		bytecode.OP_GETUPVAL,
		bytecode.OP_SELF,
		bytecode.OP_GETGLOBAL,
		bytecode.OP_GETTABLE,
		bytecode.OP_SETGLOBAL,
		bytecode.OP_SETUPVAL,
		bytecode.OP_ADD,
		bytecode.OP_SUB,
		bytecode.OP_MUL,
		bytecode.OP_DIV,
		bytecode.OP_MOD,
		bytecode.OP_POW,
		bytecode.OP_UNM,
		bytecode.OP_NOT,
		bytecode.OP_LEN,
		bytecode.OP_NEWTABLE,
		bytecode.OP_CONCAT,
		bytecode.OP_CLOSE,
		bytecode.OP_CLOSURE,
		bytecode.OP_SETTABLE,
		bytecode.OP_JMP,
		bytecode.OP_EQ,
		bytecode.OP_LT,
		bytecode.OP_LE,
		bytecode.OP_TEST,
		bytecode.OP_TESTSET,
		bytecode.OP_CALL,
		bytecode.OP_TAILCALL,
		bytecode.OP_RETURN,
		bytecode.OP_SETLIST,
		bytecode.OP_TFORLOOP,
		bytecode.OP_VARARG,
		bytecode.OP_FORPREP,
		bytecode.OP_FORLOOP:
		return instructionDispositionCompiled
	default:
		return instructionDispositionDeopt
	}
}

func (state *compileState) validateStructure() error {
	if state.proto == nil {
		return fmt.Errorf("proto cannot be nil")
	}
	if err := bytecode.ValidateProto(state.proto); err != nil {
		return err
	}
	for index, constant := range state.proto.Constants {
		switch constant.Kind {
		case bytecode.ConstantNil, bytecode.ConstantBoolean, bytecode.ConstantNumber, bytecode.ConstantString:
		default:
			return fmt.Errorf("constant %d: unsupported constant kind %s", index, constant.Kind)
		}
	}
	return nil
}

func (state *compileState) previsit() error {
	for offset, instruction := range state.proto.Code {
		if err := state.previsitInstruction(offset, instruction); err != nil {
			return err
		}
	}
	return nil
}

func (state *compileState) previsitInstruction(offset int, instruction bytecode.Instruction) error {
	disposition := state.dispositionForInstruction(offset, instruction)
	if disposition == instructionDispositionDeopt || disposition == instructionDispositionPayload {
		return nil
	}
	switch instruction.Opcode() {
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
	case bytecode.OP_FORPREP, bytecode.OP_FORLOOP:
		target := offset + 1 + instruction.SBx()
		if err := state.validateTarget(target, offset, instruction.Opcode()); err != nil {
			return err
		}
		state.labelFor(target)
	case bytecode.OP_CLOSURE:
		_, err := state.closureResumePC(offset, instruction)
		return err
	case bytecode.OP_EQ, bytecode.OP_LT, bytecode.OP_LE, bytecode.OP_TEST, bytecode.OP_TESTSET, bytecode.OP_TFORLOOP:
		target, err := state.testJumpTarget(offset, instruction.Opcode())
		if err != nil {
			return err
		}
		state.labelFor(target)
		state.labelFor(offset + 2)
	}
	return nil
}

func (state *compileState) validateTarget(target int, pc int, opcode bytecode.Opcode) error {
	if target < 0 || target >= len(state.proto.Code) {
		return fmt.Errorf("%s target %d is out of range at pc %d", opcode, target, pc)
	}
	return nil
}

func (state *compileState) validateSlot(pc int, opcode bytecode.Opcode, index int) error {
	if index < 0 || index >= int(state.proto.MaxStackSize) {
		return fmt.Errorf("%s register %d is out of range at pc %d", opcode, index, pc)
	}
	return nil
}

func (state *compileState) validateSlotRange(pc int, opcode bytecode.Opcode, start int, count int) error {
	if count < 0 {
		return fmt.Errorf("%s register count %d is invalid at pc %d", opcode, count, pc)
	}
	if count == 0 {
		return nil
	}
	if err := state.validateSlot(pc, opcode, start); err != nil {
		return err
	}
	return state.validateSlot(pc, opcode, start+count-1)
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
	disposition := state.dispositionForInstruction(offset, instruction)
	if disposition == instructionDispositionPayload {
		return nil
	}
	if disposition == instructionDispositionDeopt {
		state.emitUncoveredInstructionDeopt(offset)
		return nil
	}
	switch instruction.Opcode() {
	case bytecode.OP_MOVE:
		state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(instruction.B()))
		state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(instruction.A()), amd64.RegRAX)
		state.emitAdvanceTopForSlot(instruction.A())
	case bytecode.OP_LOADK:
		state.emitLoadConstant(instruction.A(), instruction.Bx())
	case bytecode.OP_LOADBOOL:
		return state.emitLoadBoolInstruction(offset, instruction)
	case bytecode.OP_LOADNIL:
		return state.emitLoadNilInstruction(instruction)
	case bytecode.OP_GETUPVAL:
		slotIndex, err := state.feedbackSlotIndex(offset, feedback.SlotGetUpvalue)
		if err != nil {
			return err
		}
		state.emitGetUpvalue(offset, instruction.A(), instruction.B(), slotIndex)
	case bytecode.OP_SELF:
		return state.emitSelfInstruction(offset, instruction)
	case bytecode.OP_GETGLOBAL:
		slotIndex, err := state.feedbackSlotIndex(offset, feedback.SlotGetGlobal)
		if err != nil {
			return err
		}
		state.emitGetGlobal(offset, instruction.A(), instruction.Bx(), slotIndex)
	case bytecode.OP_GETTABLE:
		slotIndex, err := state.feedbackSlotIndex(offset, feedback.SlotGetTable)
		if err != nil {
			return err
		}
		state.emitGetTable(offset, instruction.A(), instruction.B(), instruction.C(), slotIndex)
	case bytecode.OP_SETGLOBAL:
		slotIndex, err := state.feedbackSlotIndex(offset, feedback.SlotSetGlobal)
		if err != nil {
			return err
		}
		state.emitSetGlobal(offset, instruction.A(), instruction.Bx(), slotIndex)
	case bytecode.OP_SETUPVAL:
		slotIndex, err := state.feedbackSlotIndex(offset, feedback.SlotSetUpvalue)
		if err != nil {
			return err
		}
		state.emitSetUpvalue(offset, instruction.A(), instruction.B(), slotIndex)
	case bytecode.OP_ADD, bytecode.OP_SUB, bytecode.OP_MUL, bytecode.OP_DIV, bytecode.OP_MOD, bytecode.OP_POW, bytecode.OP_UNM:
		return state.emitArithmeticInstruction(offset, instruction)
	case bytecode.OP_NOT:
		return state.emitNotInstruction(offset, instruction)
	case bytecode.OP_LEN:
		return state.emitLengthInstruction(offset, instruction)
	case bytecode.OP_NEWTABLE:
		return state.emitNewTableInstruction(offset, instruction)
	case bytecode.OP_CONCAT:
		return state.emitConcatInstruction(offset, instruction)
	case bytecode.OP_CLOSURE:
		return state.emitClosureInstruction(offset, instruction)
	case bytecode.OP_SETTABLE:
		slotIndex, err := state.feedbackSlotIndex(offset, feedback.SlotSetTable)
		if err != nil {
			return err
		}
		state.emitSetTable(offset, instruction.A(), instruction.B(), instruction.C(), slotIndex)
	case bytecode.OP_JMP:
		return state.emitJumpInstruction(offset, instruction)
	case bytecode.OP_EQ, bytecode.OP_LT, bytecode.OP_LE:
		return state.emitCompareInstruction(offset, instruction)
	case bytecode.OP_TEST, bytecode.OP_TESTSET:
		return state.emitTestInstruction(offset, instruction)
	case bytecode.OP_TFORLOOP:
		return state.emitTForLoopInstruction(offset, instruction)
	case bytecode.OP_CALL:
		return state.emitCallInstruction(offset, instruction)
	case bytecode.OP_TAILCALL:
		return state.emitTailCallInstruction(offset, instruction)
	case bytecode.OP_RETURN:
		return state.emitReturnInstruction(offset, instruction)
	case bytecode.OP_SETLIST:
		return state.emitSetListInstruction(offset, instruction)
	case bytecode.OP_CLOSE:
		return state.emitCloseInstruction(offset, instruction)
	case bytecode.OP_VARARG:
		return state.emitVarargInstruction(offset, instruction)
	case bytecode.OP_FORPREP:
		return state.emitForPrepInstruction(offset, instruction)
	case bytecode.OP_FORLOOP:
		return state.emitForLoopInstruction(offset, instruction)
	default:
		return fmt.Errorf("compiled instruction %s has no emitter", instruction.Opcode())
	}
	return nil
}

func (state *compileState) emitLoadBoolInstruction(offset int, instruction bytecode.Instruction) error {
	state.emitStoreRawTValue(instruction.A(), uint64(value.BoolValue(instruction.B() != 0).Bits()))
	if instruction.C() != 0 {
		state.assembler.Jmp(state.labelFor(offset + 2))
	}
	return nil
}

func (state *compileState) emitLoadNilInstruction(instruction bytecode.Instruction) error {
	for index := instruction.A(); index <= instruction.B(); index++ {
		state.emitStoreRawTValue(index, uint64(value.NilValue().Bits()))
	}
	return nil
}

func (state *compileState) emitSelfInstruction(offset int, instruction bytecode.Instruction) error {
	state.emitSelf(offset, instruction.A(), instruction.B(), instruction.C())
	return nil
}

func (state *compileState) emitArithmeticInstruction(offset int, instruction bytecode.Instruction) error {
	state.emitArithmetic(offset, instruction.Opcode(), instruction.A(), instruction.B(), instruction.C())
	return nil
}

func (state *compileState) emitNotInstruction(offset int, instruction bytecode.Instruction) error {
	state.emitNot(offset, instruction.A(), instruction.B())
	return nil
}

func (state *compileState) emitLengthInstruction(offset int, instruction bytecode.Instruction) error {
	state.emitLength(offset, instruction.A(), instruction.B())
	return nil
}

func (state *compileState) emitNewTableInstruction(offset int, instruction bytecode.Instruction) error {
	state.emitNewTable(offset, instruction.A(), floatByteToInt(instruction.B()), floatByteToInt(instruction.C()))
	return nil
}

func (state *compileState) emitConcatInstruction(offset int, instruction bytecode.Instruction) error {
	state.emitConcat(offset, instruction.A(), instruction.B(), instruction.C())
	return nil
}

func (state *compileState) emitJumpInstruction(offset int, instruction bytecode.Instruction) error {
	state.assembler.Jmp(state.labelFor(offset + 1 + instruction.SBx()))
	return nil
}

func (state *compileState) emitCompareInstruction(offset int, instruction bytecode.Instruction) error {
	target, err := state.testJumpTarget(offset, instruction.Opcode())
	if err != nil {
		return err
	}
	state.emitCompare(offset, instruction.Opcode(), instruction.A(), instruction.B(), instruction.C(), target)
	return nil
}

func (state *compileState) emitTestInstruction(offset int, instruction bytecode.Instruction) error {
	target, err := state.testJumpTarget(offset, instruction.Opcode())
	if err != nil {
		return err
	}
	state.emitTest(offset, instruction.Opcode(), instruction.A(), instruction.B(), instruction.C(), target)
	return nil
}

func (state *compileState) emitTForLoopInstruction(offset int, instruction bytecode.Instruction) error {
	if instruction.C() == 0 {
		state.emitUncoveredInstructionDeopt(offset)
		return nil
	}
	target, err := state.testJumpTarget(offset, instruction.Opcode())
	if err != nil {
		return err
	}
	slotIndex, err := state.feedbackSlotIndex(offset, feedback.SlotCall)
	if err != nil {
		return err
	}
	siteID := state.recordContinuationSite(metadata.ContinuationCall, stubs.StubLuaCall, offset, offset, offset+2, target, uint32(instruction.A()), 3, uint32(instruction.C()+1), uint32(instruction.A()+2), metadata.ContinuationFlagAlternateResume|metadata.ContinuationFlagNativeBuiltinABI)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubLuaCall], siteID, stubs.StubLuaCall, uint64(instruction.A()), 3, uint64(instruction.C()+1), uint64(slotIndex), builtinCallBlockFlagTForLoop)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(instruction.A()+3))
	state.assembler.MoveRegImm64(amd64.RegRCX, uint64(value.NilValue().Bits()))
	state.assembler.CmpRegReg(amd64.RegRAX, amd64.RegRCX)
	state.assembler.Jcc(amd64.CondEqual, state.labelFor(offset+2))
	state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(instruction.A()+2), amd64.RegRAX)
	state.emitAdvanceTopForSlot(instruction.A() + 2)
	state.assembler.Jmp(state.labelFor(target))
	return nil
}

func (state *compileState) emitCallInstruction(offset int, instruction bytecode.Instruction) error {
	slotIndex, err := state.feedbackSlotIndex(offset, feedback.SlotCall)
	if err != nil {
		return err
	}
	state.emitCall(offset, instruction.A(), instruction.B(), instruction.C(), uint32(offset+1), slotIndex)
	return nil
}

func (state *compileState) emitTailCallInstruction(offset int, instruction bytecode.Instruction) error {
	if instruction.C() != 0 {
		state.emitUncoveredInstructionDeopt(offset)
		return nil
	}
	slotIndex, err := state.feedbackSlotIndex(offset, feedback.SlotTailCall)
	if err != nil {
		return err
	}
	state.emitTailCall(offset, instruction.A(), instruction.B(), slotIndex)
	return nil
}

func (state *compileState) emitReturnInstruction(offset int, instruction bytecode.Instruction) error {
	if instruction.B() == 0 {
		noValues := state.assembler.NewLabel()

		state.assembler.MoveRegMem64(amd64.RegR10, amd64.RegR13, state.CallFrameResultBaseOffset())
		state.assembler.MoveRegMem32(amd64.RegRCX, amd64.RegR13, state.CallFrameTopOffset())
		state.assembler.AndRegImm32(amd64.RegRCX, 0xFFFF)
		state.assembler.CmpRegImm32(amd64.RegRCX, uint32(instruction.A()))
		state.assembler.Jcc(amd64.CondBelowEqual, noValues)
		if instruction.A() != 0 {
			state.assembler.SubRegImm32(amd64.RegRCX, int32(instruction.A()))
		}
		state.assembler.MoveRegReg(amd64.RegRDX, amd64.RegRCX)
		state.assembler.MoveRegReg(amd64.RegR8, amd64.RegR12)
		if instruction.A() != 0 {
			state.assembler.AddRegImm32(amd64.RegR8, slotDisp(instruction.A()))
		}
		emitCopySlots(state.assembler, amd64.RegR10, amd64.RegR8, amd64.RegRCX)
		state.assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
		state.assembler.Ret()

		_ = state.assembler.Bind(noValues)
		state.assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
		state.assembler.XorRegReg(amd64.RegRDX, amd64.RegRDX)
		state.assembler.Ret()
		return nil
	}
	count := instruction.B() - 1
	state.assembler.MoveRegMem64(amd64.RegR10, amd64.RegR13, state.CallFrameResultBaseOffset())
	for index := 0; index < count; index++ {
		state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(instruction.A()+index))
		state.assembler.MoveMemReg64(amd64.RegR10, slotDisp(index), amd64.RegRAX)
	}
	state.emitStatus(compiledStatusOK, uint32(count))
	return nil
}

func (state *compileState) emitSetListInstruction(offset int, instruction bytecode.Instruction) error {
	block := instruction.C()
	resumePC := offset + 1
	if block == 0 {
		if offset+1 >= len(state.proto.Code) {
			return fmt.Errorf("SETLIST expects trailing extra argument instruction at pc %d", offset)
		}
		block = int(state.proto.Code[offset+1])
		resumePC = offset + 2
	}
	state.emitSetList(offset, instruction.A(), instruction.B(), block, resumePC)
	return nil
}

func (state *compileState) emitVarargInstruction(_ int, instruction bytecode.Instruction) error {
	state.assembler.MoveRegMem64(amd64.RegR10, amd64.RegR13, state.CallFrameVarargBaseOffset())
	state.assembler.MoveRegMem32(amd64.RegRCX, amd64.RegR13, state.CallFrameVarargCountOffset())
	if instruction.B() == 0 {
		state.assembler.MoveRegReg(amd64.RegRDX, amd64.RegRCX)
		if instruction.A() != 0 {
			state.assembler.AddRegImm32(amd64.RegRDX, int32(instruction.A()))
			state.assembler.MoveRegReg(amd64.RegR8, amd64.RegR12)
			state.assembler.AddRegImm32(amd64.RegR8, slotDisp(instruction.A()))
		} else {
			state.assembler.MoveRegReg(amd64.RegR8, amd64.RegR12)
		}
		emitCopySlots(state.assembler, amd64.RegR8, amd64.RegR10, amd64.RegRCX)
		state.assembler.MoveMemReg32(amd64.RegR13, state.CallFrameTopOffset(), amd64.RegRDX)
		return nil
	}
	state.assembler.MoveRegImm32(amd64.RegRDX, uint32(instruction.B()-1))
	if instruction.A() != 0 {
		state.assembler.MoveRegReg(amd64.RegR8, amd64.RegR12)
		state.assembler.AddRegImm32(amd64.RegR8, slotDisp(instruction.A()))
	} else {
		state.assembler.MoveRegReg(amd64.RegR8, amd64.RegR12)
	}
	emitCopyResultsWithNilFill(state.assembler, amd64.RegR8, amd64.RegR10, amd64.RegRDX, amd64.RegRCX)
	return nil
}

func (state *compileState) emitForPrepInstruction(offset int, instruction bytecode.Instruction) error {
	state.emitForPrep(offset, instruction.A(), offset+1+instruction.SBx())
	return nil
}

func (state *compileState) emitForLoopInstruction(offset int, instruction bytecode.Instruction) error {
	state.emitForLoop(offset, instruction.A(), offset+1, offset+1+instruction.SBx())
	return nil
}

func (state *compileState) emitCloseInstruction(offset int, instruction bytecode.Instruction) error {
	state.emitClose(offset, instruction.A())
	return nil
}

func (state *compileState) emitClosureInstruction(offset int, instruction bytecode.Instruction) error {
	resumePC, err := state.closureResumePC(offset, instruction)
	if err != nil {
		return err
	}
	state.emitClosure(offset, instruction.A(), instruction.Bx(), resumePC)
	return nil
}

func (state *compileState) emitUncoveredInstructionDeopt(bytecodePC int) {
	siteID := state.recordContinuationSite(metadata.ContinuationDeopt, stubs.StubInvalid, bytecodePC, bytecodePC, -1, -1, 0, 0, 0, 0, metadata.ContinuationFlagDeoptOnUncovered)
	state.emitDeoptExit(siteID)
}

func (state *compileState) recordContinuationSite(kind metadata.ContinuationKind, stubID stubs.ID, bytecodePC int, deoptPC int, resumePC int, altResumePC int, operand0 uint32, operand1 uint32, operand2 uint32, operand3 uint32, flags uint32) uint32 {
	var liveSetID uint32 = metadata.NoLiveSlotSet
	liveSlots := uint32(0)
	if bytecodePC >= 0 {
		if id, ok := state.metadata.LiveSlotSetID(bytecodePC); ok {
			liveSetID = id
		}
		if liveSet, ok := state.metadata.LiveSlotSetAtBytecode(bytecodePC); ok {
			liveSlots = liveSet.UpperBound(int(state.proto.MaxStackSize))
		}
	}
	return state.metadata.AddContinuationSite(metadata.ContinuationSite{
		Kind:        kind,
		Flags:       flags,
		StubID:      uint32(stubID),
		BytecodePC:  state.sitePC(bytecodePC),
		DeoptPC:     state.sitePC(deoptPC),
		ResumePC:    state.sitePC(resumePC),
		AltResumePC: state.sitePC(altResumePC),
		Operand0:    operand0,
		Operand1:    operand1,
		Operand2:    operand2,
		Operand3:    operand3,
		LiveSetID:   liveSetID,
		LiveSlots:   liveSlots,
	})
}

func (state *compileState) emitDeoptExit(siteID uint32) {
	state.emitExit(state.compiler.deoptEntry, siteID)
}

func (state *compileState) emitExit(entry uintptr, siteID uint32) {
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxSiteIDOffset, siteID)
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, 0)
	state.assembler.MoveRegImm64(amd64.RegR10, uint64(entry))
	state.assembler.JmpReg(amd64.RegR10)
}

func (state *compileState) emitBuiltinCallWithStubArgs(entry uintptr, siteID uint32, stubID stubs.ID, arg0 uint64, arg1 uint64, arg2 uint64, arg3 uint64, blockFlags uint32) {
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxSiteIDOffset, siteID)
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, 0)
	state.assembler.SubRegImm32(amd64.RegRSP, int32(abi.StubCallBlockSize))
	state.assembler.MoveMemReg64(amd64.RegRSP, state.StubCallBlockFrameOffset(), amd64.RegR13)
	state.emitMoveStackImm64(amd64.RegRSP, state.StubCallBlockArg0Offset(), arg0)
	state.emitMoveStackImm64(amd64.RegRSP, state.StubCallBlockArg1Offset(), arg1)
	state.emitMoveStackImm64(amd64.RegRSP, state.StubCallBlockArg2Offset(), arg2)
	state.emitMoveStackImm64(amd64.RegRSP, state.StubCallBlockArg3Offset(), arg3)
	state.assembler.MoveMemImm32(amd64.RegRSP, state.StubCallBlockStubIDOffset(), uint32(stubID))
	state.assembler.MoveMemImm32(amd64.RegRSP, state.StubCallBlockFlagsOffset(), blockFlags)
	state.assembler.MoveRegImm64(amd64.RegR10, uint64(entry))
	state.assembler.CallReg(amd64.RegR10)
	state.assembler.AddRegImm32(amd64.RegRSP, int32(abi.StubCallBlockSize))
}

func (state *compileState) emitMoveStackImm64(base amd64.Register, disp int32, value uint64) {
	state.assembler.MoveRegImm64(amd64.RegRAX, value)
	state.assembler.MoveMemReg64(base, disp, amd64.RegRAX)
}

func (state *compileState) isSetListExtraArgument(offset int) bool {
	if state == nil || state.proto == nil || offset <= 0 || offset >= len(state.proto.Code) {
		return false
	}
	previous := state.proto.Code[offset-1]
	return previous.Opcode() == bytecode.OP_SETLIST && previous.C() == 0
}

func (state *compileState) isClosureCapturePayload(offset int) bool {
	if state == nil || state.proto == nil || offset <= 0 || offset >= len(state.proto.Code) {
		return false
	}
	for pc := offset - 1; pc >= 0; pc-- {
		instruction := state.proto.Code[pc]
		if instruction.Opcode() != bytecode.OP_CLOSURE {
			continue
		}
		childIndex := instruction.Bx()
		if childIndex < 0 || childIndex >= len(state.proto.Protos) {
			return false
		}
		captureEnd := pc + 1 + int(state.proto.Protos[childIndex].NumUpvalues)
		return offset < captureEnd
	}
	return false
}

func (state *compileState) testJumpTarget(offset int, opcode bytecode.Opcode) (int, error) {
	if offset+1 >= len(state.proto.Code) {
		return 0, fmt.Errorf("%s is missing trailing JMP at pc %d", opcode, offset)
	}
	jump := state.proto.Code[offset+1]
	if jump.Opcode() != bytecode.OP_JMP {
		return 0, fmt.Errorf("%s expects trailing JMP at pc %d, got %s", opcode, offset, jump.Opcode())
	}
	target := offset + 2 + jump.SBx()
	if err := state.validateTarget(target, offset, opcode); err != nil {
		return 0, err
	}
	return target, nil
}

func (state *compileState) closureResumePC(offset int, instruction bytecode.Instruction) (int, error) {
	childIndex := instruction.Bx()
	if childIndex < 0 || childIndex >= len(state.proto.Protos) {
		return 0, fmt.Errorf("CLOSURE child proto %d is out of range at pc %d", childIndex, offset)
	}
	childProto := state.proto.Protos[childIndex]
	resumePC := offset + 1 + int(childProto.NumUpvalues)
	if resumePC > len(state.proto.Code) {
		return 0, fmt.Errorf("CLOSURE capture payload overruns proto at pc %d", offset)
	}
	return resumePC, nil
}

func (state *compileState) sitePC(pc int) uint32 {
	if pc < 0 {
		return metadata.UnmappedOffset
	}
	return uint32(pc)
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
	state.emitAdvanceTopForSlot(slot)
}

func (state *compileState) emitLoadConstant(slot int, index int) {
	state.assembler.MoveRegMem64(amd64.RegR10, amd64.RegR13, state.CallFrameConstBaseOffset())
	if index != 0 {
		state.assembler.AddRegImm32(amd64.RegR10, slotDisp(index))
	}
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR10, 0)
	state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(slot), amd64.RegRAX)
	state.emitAdvanceTopForSlot(slot)
}

func (state *compileState) emitAdvanceTopForSlot(slot int) {
	done := state.assembler.NewLabel()
	state.assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR13, state.CallFrameTopOffset())
	state.assembler.MoveRegReg(amd64.RegRCX, amd64.RegRAX)
	state.assembler.AndRegImm32(amd64.RegRCX, 0xFFFF)
	state.assembler.CmpRegImm32(amd64.RegRCX, uint32(slot+1))
	state.assembler.Jcc(amd64.CondAboveEqual, done)
	state.assembler.AndRegImm32(amd64.RegRAX, 0xFFFF0000)
	state.assembler.AddRegImm32(amd64.RegRAX, int32(slot+1))
	state.assembler.MoveMemReg32(amd64.RegR13, state.CallFrameTopOffset(), amd64.RegRAX)
	_ = state.assembler.Bind(done)
}

func (state *compileState) emitCall(bytecodePC int, a int, b int, c int, resumePC uint32, slotIndex uint32) {
	blockFlags := uint32(0)
	if b == 0 {
		blockFlags |= builtinCallBlockFlagOpenArgs
	}
	if c == 0 {
		blockFlags |= builtinCallBlockFlagOpenResults
	}
	siteID := state.recordContinuationSite(metadata.ContinuationCall, stubs.StubLuaCall, bytecodePC, bytecodePC, int(resumePC), -1, uint32(a), uint32(b), uint32(c), 0, metadata.ContinuationFlagNativeBuiltinABI)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubLuaCall], siteID, stubs.StubLuaCall, uint64(a), uint64(b), uint64(c), uint64(slotIndex), blockFlags)
}

func (state *compileState) emitGetUpvalue(bytecodePC int, dst int, upvalueIndex int, slotIndex uint32) {
	siteID := state.recordContinuationSite(metadata.ContinuationGetUpvalue, stubs.StubGetUpvalue, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(dst), uint32(upvalueIndex), 0, 0, metadata.ContinuationFlagNativeBuiltinABI)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubGetUpvalue], siteID, stubs.StubGetUpvalue, uint64(dst), uint64(upvalueIndex), uint64(slotIndex), 0, 0)
}

func (state *compileState) emitSetUpvalue(bytecodePC int, src int, upvalueIndex int, slotIndex uint32) {
	siteID := state.recordContinuationSite(metadata.ContinuationSetUpvalue, stubs.StubSetUpvalue, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(src), uint32(upvalueIndex), 0, 0, metadata.ContinuationFlagNativeBuiltinABI)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSetUpvalue], siteID, stubs.StubSetUpvalue, uint64(src), uint64(upvalueIndex), uint64(slotIndex), 0, 0)
}

func (state *compileState) emitTailCall(bytecodePC int, a int, b int, slotIndex uint32) {
	blockFlags := uint32(0)
	if b == 0 {
		blockFlags |= builtinCallBlockFlagOpenArgs
	}
	siteID := state.recordContinuationSite(metadata.ContinuationTailCall, stubs.StubTailCall, bytecodePC, bytecodePC, -1, -1, uint32(a), uint32(b), 0, 0, metadata.ContinuationFlagFinalExit|metadata.ContinuationFlagNativeBuiltinABI)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubTailCall], siteID, stubs.StubTailCall, uint64(a), uint64(b), 0, uint64(slotIndex), blockFlags)
}

func (state *compileState) emitSetList(bytecodePC int, a int, b int, block int, resumePC int) {
	siteID := state.recordContinuationSite(metadata.ContinuationSetList, stubs.StubSetList, bytecodePC, bytecodePC, resumePC, -1, uint32(a), uint32(b), uint32(block), 0, metadata.ContinuationFlagNativeBuiltinABI)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSetList], siteID, stubs.StubSetList, uint64(a), uint64(b), uint64(block), 0, 0)
}

func (state *compileState) emitNewTable(bytecodePC int, a int, arrayCap int, hashCap int) {
	siteID := state.recordContinuationSite(metadata.ContinuationNewTable, stubs.StubNewTable, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(a), uint32(arrayCap), uint32(hashCap), 0, metadata.ContinuationFlagNativeBuiltinABI)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubNewTable], siteID, stubs.StubNewTable, uint64(a), uint64(arrayCap), uint64(hashCap), 0, 0)
}

func (state *compileState) emitConcat(bytecodePC int, a int, b int, c int) {
	siteID := state.recordContinuationSite(metadata.ContinuationConcat, stubs.StubConcat, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(a), uint32(b), uint32(c), 0, metadata.ContinuationFlagNativeBuiltinABI)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubConcat], siteID, stubs.StubConcat, uint64(a), uint64(b), uint64(c), 0, 0)
}

func (state *compileState) emitClose(bytecodePC int, a int) {
	siteID := state.recordContinuationSite(metadata.ContinuationClose, stubs.StubClose, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(a), 0, 0, 0, metadata.ContinuationFlagNativeBuiltinABI)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubClose], siteID, stubs.StubClose, uint64(a), 0, 0, 0, 0)
}

func (state *compileState) emitClosure(bytecodePC int, a int, childIndex int, resumePC int) {
	siteID := state.recordContinuationSite(metadata.ContinuationClosure, stubs.StubClosure, bytecodePC, bytecodePC, resumePC, -1, uint32(a), uint32(childIndex), 0, 0, metadata.ContinuationFlagNativeBuiltinABI)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubClosure], siteID, stubs.StubClosure, uint64(a), uint64(childIndex), 0, 0, 0)
}

func (state *compileState) emitForPrep(bytecodePC int, a int, target int) {
	deopt := state.assembler.NewLabel()
	siteID := state.recordContinuationSite(metadata.ContinuationForPrep, stubs.StubForPrep, bytecodePC, bytecodePC, target, -1, uint32(a), uint32(target), 0, 0, metadata.ContinuationFlagNativeBuiltinABI)

	state.emitLoadNumericLoopSlot(a, amd64.XMM0, deopt)
	state.emitLoadNumericLoopSlot(a+1, amd64.XMM1, deopt)
	state.emitLoadNumericLoopSlot(a+2, amd64.XMM2, deopt)
	state.assembler.SubsdXmmXmm(amd64.XMM0, amd64.XMM2)
	state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(a), amd64.XMM0)
	state.emitAdvanceTopForSlot(a)
	state.assembler.Jmp(state.labelFor(target))

	_ = state.assembler.Bind(deopt)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubForPrep], siteID, stubs.StubForPrep, uint64(a), 0, 0, 0, 0)
	state.assembler.Jmp(state.labelFor(target))
}

func (state *compileState) emitForLoop(bytecodePC int, a int, resumePC int, target int) {
	deopt := state.assembler.NewLabel()
	positiveStep := state.assembler.NewLabel()
	continueLoop := state.assembler.NewLabel()
	safepoint := state.assembler.NewLabel()
	done := state.assembler.NewLabel()
	siteID := state.recordContinuationSite(metadata.ContinuationForLoop, stubs.StubForLoop, bytecodePC, bytecodePC, resumePC, target, uint32(a), uint32(resumePC), uint32(target), 0, metadata.ContinuationFlagAlternateResume|metadata.ContinuationFlagNativeBuiltinABI)

	state.emitLoadNumericLoopSlot(a, amd64.XMM0, deopt)
	state.emitLoadNumericLoopSlot(a+1, amd64.XMM1, deopt)
	state.emitLoadNumericLoopSlot(a+2, amd64.XMM2, deopt)
	state.assembler.AddsdXmmXmm(amd64.XMM0, amd64.XMM2)
	state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(a), amd64.XMM0)
	state.emitAdvanceTopForSlot(a)
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
	state.emitAdvanceTopForSlot(a + 3)
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxSiteIDOffset, siteID)
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, execCtxFlagAlternateResume)
	emitJumpIfGCSafepointRequested(state.assembler, amd64.RegRAX, safepoint)
	state.assembler.Jmp(state.labelFor(target))

	_ = state.assembler.Bind(safepoint)
	state.assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, execCtxFlagAlternateResume|execCtxFlagGCSafepointBoundary)
	state.emitStatus(compiledStatusStub, uint32(stubs.StubForLoop))

	_ = state.assembler.Bind(done)
	state.assembler.Jmp(state.labelFor(resumePC))

	_ = state.assembler.Bind(deopt)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubForLoop], siteID, stubs.StubForLoop, uint64(a), 0, 0, 0, 0)
	state.assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR11, execCtxFlagsOffset)
	state.assembler.AndRegImm32(amd64.RegRAX, execCtxFlagAlternateResume)
	state.assembler.CmpRegImm32(amd64.RegRAX, execCtxFlagAlternateResume)
	state.assembler.Jcc(amd64.CondEqual, continueLoop)
	state.assembler.Jmp(done)
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

func floatByteToInt(value int) int {
	if value < 8 {
		return value
	}
	e := (value >> 3) & 0x1F
	m := value & 7
	return (m + 8) << (e - 1)
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

func (state *compileState) CallFrameTopOffset() int32 {
	return int32(rtstate.CallFrameTopOffset)
}

func (state *compileState) CallFrameResultBaseOffset() int32 {
	return int32(rtstate.CallFrameResultBaseOffset)
}

func (state *compileState) StubCallBlockFrameOffset() int32 {
	return int32(rtstate.StubCallBlockFrameOffset)
}

func (state *compileState) StubCallBlockArg0Offset() int32 {
	return int32(rtstate.StubCallBlockArg0Offset)
}

func (state *compileState) StubCallBlockArg1Offset() int32 {
	return int32(rtstate.StubCallBlockArg1Offset)
}

func (state *compileState) StubCallBlockArg2Offset() int32 {
	return int32(rtstate.StubCallBlockArg2Offset)
}

func (state *compileState) StubCallBlockArg3Offset() int32 {
	return int32(rtstate.StubCallBlockArg3Offset)
}

func (state *compileState) StubCallBlockStubIDOffset() int32 {
	return int32(rtstate.StubCallBlockStubIDOffset)
}

func (state *compileState) StubCallBlockFlagsOffset() int32 {
	return int32(rtstate.StubCallBlockFlagsOffset)
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

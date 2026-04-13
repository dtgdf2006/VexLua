package baseline

import (
	"vexlua/internal/bytecode"
	rtstring "vexlua/internal/runtime/string"
	rttable "vexlua/internal/runtime/table"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/amd64"
	"vexlua/internal/vexarc/metadata"
	"vexlua/internal/vexarc/stubs"
)

func (state *compileState) emitLoadOperandValue(operand int, dst amd64.Register) {
	if operand&bytecode.BitRK != 0 {
		index := operand &^ bytecode.BitRK
		state.assembler.MoveRegMem64(dst, amd64.RegR13, state.CallFrameConstBaseOffset())
		if index != 0 {
			state.assembler.AddRegImm32(dst, slotDisp(index))
		}
		state.assembler.MoveRegMem64(dst, dst, 0)
		return
	}
	state.assembler.MoveRegMem64(dst, amd64.RegR12, slotDisp(operand))
}

func (state *compileState) emitArithmetic(bytecodePC int, opcode bytecode.Opcode, a int, b int, c int) {
	stubPath := state.assembler.NewLabel()
	done := state.assembler.NewLabel()
	siteID := state.recordContinuationSite(metadata.ContinuationArithmetic, stubs.StubArithmetic, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(a), uint32(b), uint32(c), uint32(opcode), metadata.ContinuationFlagDeoptOnUncovered)

	state.emitLoadOperandValue(b, amd64.RegRAX)
	emitLoadNumberFromValueReg(state.assembler, amd64.RegRAX, amd64.RegR9, amd64.XMM0, stubPath)
	if opcode != bytecode.OP_UNM {
		state.emitLoadOperandValue(c, amd64.RegRBX)
		emitLoadNumberFromValueReg(state.assembler, amd64.RegRBX, amd64.RegR10, amd64.XMM1, stubPath)
	}

	switch opcode {
	case bytecode.OP_ADD:
		state.assembler.AddsdXmmXmm(amd64.XMM0, amd64.XMM1)
		state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(a), amd64.XMM0)
	case bytecode.OP_SUB:
		state.assembler.SubsdXmmXmm(amd64.XMM0, amd64.XMM1)
		state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(a), amd64.XMM0)
	case bytecode.OP_MUL:
		state.assembler.MulsdXmmXmm(amd64.XMM0, amd64.XMM1)
		state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(a), amd64.XMM0)
	case bytecode.OP_DIV:
		state.assembler.DivsdXmmXmm(amd64.XMM0, amd64.XMM1)
		state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(a), amd64.XMM0)
	case bytecode.OP_MOD:
		state.assembler.Jmp(stubPath)
	case bytecode.OP_POW:
		state.assembler.Jmp(stubPath)
	case bytecode.OP_UNM:
		state.assembler.XorpsXmmXmm(amd64.XMM1, amd64.XMM1)
		state.assembler.SubsdXmmXmm(amd64.XMM1, amd64.XMM0)
		state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(a), amd64.XMM1)
	default:
		panic("unexpected arithmetic opcode")
	}
	state.emitAdvanceTopForSlot(a)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(stubPath)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubArithmetic], siteID, stubs.StubArithmetic, uint64(a), uint64(b), uint64(c), uint64(opcode), 0)

	_ = state.assembler.Bind(done)
}

func (state *compileState) emitNot(_ int, dst int, src int) {
	falsey := state.assembler.NewLabel()
	done := state.assembler.NewLabel()

	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(src))
	emitJumpIfValueFalsey(state.assembler, amd64.RegRAX, amd64.RegR10, falsey)
	state.emitStoreRawTValue(dst, uint64(value.BoolValue(false).Bits()))
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(falsey)
	state.emitStoreRawTValue(dst, uint64(value.BoolValue(true).Bits()))

	_ = state.assembler.Bind(done)
}

func (state *compileState) emitLength(bytecodePC int, dst int, src int) {
	stringPath := state.assembler.NewLabel()
	stubPath := state.assembler.NewLabel()
	done := state.assembler.NewLabel()
	siteID := state.recordContinuationSite(metadata.ContinuationLength, stubs.StubLen, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(dst), uint32(src), 0, 0, metadata.ContinuationFlagDeoptOnUncovered)

	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(src))
	state.assembler.MoveRegReg(amd64.RegR10, amd64.RegRAX)
	state.assembler.ShiftRightRegImm8(amd64.RegR10, value.TagShift)
	state.assembler.CmpRegImm32(amd64.RegR10, shiftedBoxedTag(value.TagStringRef))
	state.assembler.Jcc(amd64.CondEqual, stringPath)
	state.assembler.Jmp(stubPath)

	_ = state.assembler.Bind(stringPath)
	emitExtractHeapRefPayloadFromTValue(state.assembler, amd64.RegRDX, amd64.RegRAX)
	emitDecodeHeapRefFromRaw(state.assembler, amd64.RegRDX)
	state.assembler.MoveRegMem32(amd64.RegRCX, amd64.RegRDX, rtstring.LengthOffset)
	state.assembler.Cvtsi2sdXmmReg64(amd64.XMM0, amd64.RegRCX)
	state.assembler.MoveMemXmm64(amd64.RegR12, slotDisp(dst), amd64.XMM0)
	state.emitAdvanceTopForSlot(dst)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(stubPath)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubLen], siteID, stubs.StubLen, uint64(dst), uint64(src), 0, 0, 0)

	_ = state.assembler.Bind(done)
}

func (state *compileState) emitSelf(bytecodePC int, dst int, tableSlot int, keyOperand int) {
	tablePath := state.assembler.NewLabel()
	lookupLoop := state.assembler.NewLabel()
	nextEntry := state.assembler.NewLabel()
	notFound := state.assembler.NewLabel()
	storeResult := state.assembler.NewLabel()
	dispatchPath := state.assembler.NewLabel()
	done := state.assembler.NewLabel()
	siteID := state.recordContinuationSite(metadata.ContinuationSelf, stubs.StubSelf, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(dst), uint32(tableSlot), uint32(keyOperand), 0, metadata.ContinuationFlagDeoptOnUncovered)

	state.assembler.MoveRegMem64(amd64.RegRDX, amd64.RegR12, slotDisp(tableSlot))
	state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(dst+1), amd64.RegRDX)
	state.emitLoadOperandValue(keyOperand, amd64.RegRCX)

	state.assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	state.assembler.ShiftRightRegImm8(amd64.RegRAX, value.TagShift)
	state.assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagTableRef))
	state.assembler.Jcc(amd64.CondEqual, tablePath)
	state.assembler.Jmp(dispatchPath)

	_ = state.assembler.Bind(tablePath)
	state.assembler.MoveRegReg(amd64.RegRAX, amd64.RegRCX)
	state.assembler.ShiftRightRegImm8(amd64.RegRAX, value.TagShift)
	state.assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagStringRef))
	state.assembler.Jcc(amd64.CondNotEqual, dispatchPath)
	state.assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	emitExtractHeapRefPayloadFromTValue(state.assembler, amd64.RegRAX, amd64.RegRAX)
	emitDecodeHeapRefFromRaw(state.assembler, amd64.RegRAX)
	state.assembler.MoveRegReg(amd64.RegRBX, amd64.RegRAX)
	state.assembler.MoveRegImm64(amd64.RegRDX, uint64(value.NilValue().Bits()))
	state.assembler.MoveRegMem64(amd64.RegR9, amd64.RegRAX, rttable.EntriesDataOffset)
	state.assembler.CmpRegImm32(amd64.RegR9, 0)
	state.assembler.Jcc(amd64.CondEqual, notFound)
	state.assembler.AddRegReg(amd64.RegR9, amd64.HeapBaseRegister)
	state.assembler.MoveRegMem32(amd64.RegR10, amd64.RegRAX, rttable.HashCapacityOffset)
	state.assembler.XorRegReg(amd64.RegR8, amd64.RegR8)

	_ = state.assembler.Bind(lookupLoop)
	state.assembler.CmpRegReg(amd64.RegR8, amd64.RegR10)
	state.assembler.Jcc(amd64.CondAboveEqual, notFound)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR9, rttable.EntryKeyOffset)
	state.assembler.CmpRegReg(amd64.RegRAX, amd64.RegRCX)
	state.assembler.Jcc(amd64.CondNotEqual, nextEntry)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR9, rttable.EntryValueOffset)
	state.assembler.CmpRegReg(amd64.RegRAX, amd64.RegRDX)
	state.assembler.Jcc(amd64.CondEqual, nextEntry)
	state.assembler.MoveRegReg(amd64.RegRDX, amd64.RegRAX)
	state.assembler.Jmp(storeResult)

	_ = state.assembler.Bind(nextEntry)
	state.assembler.AddRegImm32(amd64.RegR9, rttable.EntrySize)
	state.assembler.AddRegImm32(amd64.RegR8, 1)
	state.assembler.Jmp(lookupLoop)

	_ = state.assembler.Bind(notFound)
	state.assembler.MoveRegMem32(amd64.RegRAX, amd64.RegRBX, rttable.FlagsOffset)
	state.assembler.AndRegImm32(amd64.RegRAX, uint32(rttable.FlagIndexFastPathBlocked|rttable.FlagWeakKeys|rttable.FlagWeakValues|rttable.FlagRehashing))
	state.assembler.CmpRegImm32(amd64.RegRAX, 0)
	state.assembler.Jcc(amd64.CondNotEqual, dispatchPath)
	state.assembler.Jmp(storeResult)

	_ = state.assembler.Bind(storeResult)
	state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(dst), amd64.RegRDX)
	state.emitAdvanceTopForSlot(dst + 1)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(dispatchPath)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSelf], siteID, stubs.StubSelf, uint64(dst), uint64(tableSlot), uint64(keyOperand), 0, 0)

	_ = state.assembler.Bind(done)
}

func (state *compileState) emitCompare(bytecodePC int, opcode bytecode.Opcode, a int, b int, c int, target int) {
	conditionTrue := state.assembler.NewLabel()
	conditionFalse := state.assembler.NewLabel()
	leftBoxedEq := state.assembler.NewLabel()
	stubPath := state.assembler.NewLabel()
	fallthroughPath := state.labelFor(bytecodePC + 2)
	siteID := state.recordContinuationSite(metadata.ContinuationCompare, stubs.StubCompare, bytecodePC, bytecodePC, bytecodePC+2, target, uint32(a), uint32(b), uint32(c), uint32(opcode), metadata.ContinuationFlagAlternateResume|metadata.ContinuationFlagNativeBuiltinABI|metadata.ContinuationFlagDeoptOnUncovered)

	state.emitLoadOperandValue(b, amd64.RegRAX)
	state.emitLoadOperandValue(c, amd64.RegRBX)

	switch opcode {
	case bytecode.OP_EQ:
		state.assembler.MoveRegReg(amd64.RegR9, amd64.RegRAX)
		state.assembler.ShiftRightRegImm8(amd64.RegR9, 48)
		state.assembler.CmpRegImm32(amd64.RegR9, 0xFFFF)
		state.assembler.Jcc(amd64.CondEqual, leftBoxedEq)
		state.assembler.MoveRegReg(amd64.RegR10, amd64.RegRBX)
		state.assembler.ShiftRightRegImm8(amd64.RegR10, 48)
		state.assembler.CmpRegImm32(amd64.RegR10, 0xFFFF)
		state.assembler.Jcc(amd64.CondEqual, conditionFalse)
		emitLoadNumberFromValueReg(state.assembler, amd64.RegRAX, amd64.RegR9, amd64.XMM0, stubPath)
		emitLoadNumberFromValueReg(state.assembler, amd64.RegRBX, amd64.RegR10, amd64.XMM1, stubPath)
		state.assembler.UcomisdXmmXmm(amd64.XMM0, amd64.XMM1)
		state.assembler.Jcc(amd64.CondParity, conditionFalse)
		state.assembler.Jcc(amd64.CondEqual, conditionTrue)
		state.assembler.Jmp(conditionFalse)

		_ = state.assembler.Bind(leftBoxedEq)
		state.assembler.MoveRegReg(amd64.RegR10, amd64.RegRBX)
		state.assembler.ShiftRightRegImm8(amd64.RegR10, 48)
		state.assembler.CmpRegImm32(amd64.RegR10, 0xFFFF)
		state.assembler.Jcc(amd64.CondNotEqual, conditionFalse)
		state.assembler.CmpRegReg(amd64.RegRAX, amd64.RegRBX)
		state.assembler.Jcc(amd64.CondEqual, conditionTrue)
		state.assembler.MoveRegReg(amd64.RegR9, amd64.RegRAX)
		state.assembler.ShiftRightRegImm8(amd64.RegR9, value.TagShift)
		state.assembler.MoveRegReg(amd64.RegR10, amd64.RegRBX)
		state.assembler.ShiftRightRegImm8(amd64.RegR10, value.TagShift)
		state.assembler.CmpRegReg(amd64.RegR9, amd64.RegR10)
		state.assembler.Jcc(amd64.CondNotEqual, conditionFalse)
		state.assembler.CmpRegImm32(amd64.RegR9, shiftedBoxedTag(value.TagTableRef))
		state.assembler.Jcc(amd64.CondEqual, stubPath)
		state.assembler.CmpRegImm32(amd64.RegR9, shiftedBoxedTag(value.TagHostObjectRef))
		state.assembler.Jcc(amd64.CondEqual, stubPath)
		state.assembler.Jmp(conditionFalse)
	case bytecode.OP_LT, bytecode.OP_LE:
		state.assembler.MoveRegReg(amd64.RegR9, amd64.RegRAX)
		state.assembler.ShiftRightRegImm8(amd64.RegR9, 48)
		state.assembler.CmpRegImm32(amd64.RegR9, 0xFFFF)
		state.assembler.Jcc(amd64.CondEqual, stubPath)
		state.assembler.MoveRegReg(amd64.RegR10, amd64.RegRBX)
		state.assembler.ShiftRightRegImm8(amd64.RegR10, 48)
		state.assembler.CmpRegImm32(amd64.RegR10, 0xFFFF)
		state.assembler.Jcc(amd64.CondEqual, stubPath)
		emitLoadNumberFromValueReg(state.assembler, amd64.RegRAX, amd64.RegR9, amd64.XMM0, stubPath)
		emitLoadNumberFromValueReg(state.assembler, amd64.RegRBX, amd64.RegR10, amd64.XMM1, stubPath)
		state.assembler.UcomisdXmmXmm(amd64.XMM0, amd64.XMM1)
		state.assembler.Jcc(amd64.CondParity, conditionFalse)
		if opcode == bytecode.OP_LT {
			state.assembler.Jcc(amd64.CondBelow, conditionTrue)
		} else {
			state.assembler.Jcc(amd64.CondBelowEqual, conditionTrue)
		}
		state.assembler.Jmp(conditionFalse)
	default:
		panic("unexpected compare opcode")
	}

	_ = state.assembler.Bind(conditionTrue)
	if a != 0 {
		state.assembler.Jmp(state.labelFor(target))
	} else {
		state.assembler.Jmp(fallthroughPath)
	}

	_ = state.assembler.Bind(conditionFalse)
	if a == 0 {
		state.assembler.Jmp(state.labelFor(target))
	} else {
		state.assembler.Jmp(fallthroughPath)
	}

	_ = state.assembler.Bind(stubPath)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubCompare], siteID, stubs.StubCompare, uint64(a), uint64(b), uint64(c), uint64(opcode), 0)
}

func (state *compileState) emitTest(bytecodePC int, opcode bytecode.Opcode, a int, b int, c int, target int) {
	falsey := state.assembler.NewLabel()
	fallthroughPath := state.labelFor(bytecodePC + 2)

	if opcode == bytecode.OP_TEST {
		state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(a))
		emitJumpIfValueFalsey(state.assembler, amd64.RegRAX, amd64.RegR10, falsey)
		if c != 0 {
			state.assembler.Jmp(state.labelFor(target))
		} else {
			state.assembler.Jmp(fallthroughPath)
		}
		_ = state.assembler.Bind(falsey)
		if c == 0 {
			state.assembler.Jmp(state.labelFor(target))
		} else {
			state.assembler.Jmp(fallthroughPath)
		}
		return
	}

	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(b))
	emitJumpIfValueFalsey(state.assembler, amd64.RegRAX, amd64.RegR10, falsey)
	if c != 0 {
		state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(a), amd64.RegRAX)
		state.emitAdvanceTopForSlot(a)
		state.assembler.Jmp(state.labelFor(target))
	}
	state.assembler.Jmp(fallthroughPath)

	_ = state.assembler.Bind(falsey)
	if c == 0 {
		state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(a), amd64.RegRAX)
		state.emitAdvanceTopForSlot(a)
		state.assembler.Jmp(state.labelFor(target))
	}
	state.assembler.Jmp(fallthroughPath)
}

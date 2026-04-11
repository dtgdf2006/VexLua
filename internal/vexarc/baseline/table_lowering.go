package baseline

import (
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/closure"
	"vexlua/internal/runtime/feedback"
	rttable "vexlua/internal/runtime/table"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/amd64"
	"vexlua/internal/vexarc/metadata"
	"vexlua/internal/vexarc/stubs"
)

func (state *compileState) feedbackSlotIndex(offset int, kind feedback.SlotKind) (uint32, error) {
	if state.feedbackLayout == nil {
		return 0, fmt.Errorf("missing feedback layout")
	}
	slot, slotIndex, ok := state.feedbackLayout.SlotAtPC(offset)
	if !ok {
		return 0, fmt.Errorf("pc %d has no feedback slot", offset)
	}
	if slot.Kind != kind {
		return 0, fmt.Errorf("pc %d feedback slot kind mismatch: have %d want %d", offset, slot.Kind, kind)
	}
	return slotIndex, nil
}

func (state *compileState) emitGetGlobal(bytecodePC int, dst int, keyIndex int, slotIndex uint32) {
	arrayPath := state.assembler.NewLabel()
	hashPath := state.assembler.NewLabel()
	done := state.assembler.NewLabel()
	deopt := state.assembler.NewLabel()
	siteID := state.recordContinuationSite(metadata.ContinuationGetGlobal, stubs.StubGetGlobal, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(dst), uint32(keyIndex), slotIndex, 0, metadata.ContinuationFlagNativeBuiltinABI|metadata.ContinuationFlagDeoptOnUncovered)

	state.emitLoadClosureObject(amd64.RegRBX)
	state.emitLoadFeedbackCellBase(amd64.RegRSI, amd64.RegRBX, slotIndex, deopt)
	state.assembler.MoveRegMem32(amd64.RegR8, amd64.RegRSI, feedback.CellStateOffset)
	state.assembler.CmpRegImm32(amd64.RegR8, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessArray, feedback.SlotGetGlobal))
	state.assembler.Jcc(amd64.CondEqual, arrayPath)
	state.assembler.CmpRegImm32(amd64.RegR8, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessHash, feedback.SlotGetGlobal))
	state.assembler.Jcc(amd64.CondEqual, hashPath)
	state.assembler.Jmp(deopt)

	_ = state.assembler.Bind(arrayPath)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRBX, closure.EnvOffset)
	state.emitGuardTableValueAgainstCell(amd64.RegRAX, amd64.RegRSI, deopt)
	state.emitLoadArrayValueFromCachedCell(amd64.RegRDX, amd64.RegRSI, amd64.RegRDI, amd64.RegR8, deopt)
	state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(dst), amd64.RegRAX)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(hashPath)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRBX, closure.EnvOffset)
	state.emitGuardTableValueAgainstCell(amd64.RegRAX, amd64.RegRSI, deopt)
	state.emitLoadHashValueFromCachedCell(amd64.RegRDX, amd64.RegRSI, amd64.RegRDI, amd64.RegR8, deopt)
	state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(dst), amd64.RegRAX)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(deopt)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubGetGlobal], siteID, stubs.StubGetGlobal, uint64(dst), uint64(keyIndex), uint64(slotIndex), 0, 0)
	_ = state.assembler.Bind(done)
}

func (state *compileState) emitGetTable(bytecodePC int, dst int, tableSlot int, keyOperand int, slotIndex uint32) {
	arrayPath := state.assembler.NewLabel()
	hashPath := state.assembler.NewLabel()
	done := state.assembler.NewLabel()
	deopt := state.assembler.NewLabel()
	siteID := state.recordContinuationSite(metadata.ContinuationGetTable, stubs.StubGetTable, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(dst), uint32(tableSlot), uint32(keyOperand), slotIndex, metadata.ContinuationFlagNativeBuiltinABI|metadata.ContinuationFlagDeoptOnUncovered)

	state.emitLoadClosureObject(amd64.RegRBX)
	state.emitLoadFeedbackCellBase(amd64.RegRSI, amd64.RegRBX, slotIndex, deopt)
	state.assembler.MoveRegMem32(amd64.RegR8, amd64.RegRSI, feedback.CellStateOffset)
	state.assembler.CmpRegImm32(amd64.RegR8, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessArray, feedback.SlotGetTable))
	state.assembler.Jcc(amd64.CondEqual, arrayPath)
	state.assembler.CmpRegImm32(amd64.RegR8, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessHash, feedback.SlotGetTable))
	state.assembler.Jcc(amd64.CondEqual, hashPath)
	state.assembler.Jmp(deopt)

	_ = state.assembler.Bind(arrayPath)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(tableSlot))
	state.emitGuardTableValueAgainstCell(amd64.RegRAX, amd64.RegRSI, deopt)
	state.emitGuardRegisterKeyMatchesCell(keyOperand, amd64.RegRSI, amd64.RegR9, amd64.RegR10, deopt)
	state.emitLoadArrayValueFromCachedCell(amd64.RegRDX, amd64.RegRSI, amd64.RegRDI, amd64.RegR8, deopt)
	state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(dst), amd64.RegRAX)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(hashPath)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(tableSlot))
	state.emitGuardTableValueAgainstCell(amd64.RegRAX, amd64.RegRSI, deopt)
	state.emitGuardRegisterKeyMatchesCell(keyOperand, amd64.RegRSI, amd64.RegR9, amd64.RegR10, deopt)
	state.emitLoadHashValueFromCachedCell(amd64.RegRDX, amd64.RegRSI, amd64.RegRDI, amd64.RegR8, deopt)
	state.assembler.MoveMemReg64(amd64.RegR12, slotDisp(dst), amd64.RegRAX)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(deopt)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubGetTable], siteID, stubs.StubGetTable, uint64(dst), uint64(tableSlot), uint64(keyOperand), uint64(slotIndex), 0)
	_ = state.assembler.Bind(done)
}

func (state *compileState) emitSetGlobal(bytecodePC int, src int, keyIndex int, slotIndex uint32) {
	arrayPath := state.assembler.NewLabel()
	hashPath := state.assembler.NewLabel()
	done := state.assembler.NewLabel()
	deopt := state.assembler.NewLabel()
	siteID := state.recordContinuationSite(metadata.ContinuationSetGlobal, stubs.StubSetGlobal, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(src), uint32(keyIndex), slotIndex, 0, metadata.ContinuationFlagNativeBuiltinABI|metadata.ContinuationFlagDeoptOnUncovered)

	state.emitLoadClosureObject(amd64.RegRBX)
	state.emitLoadFeedbackCellBase(amd64.RegRSI, amd64.RegRBX, slotIndex, deopt)
	state.assembler.MoveRegMem32(amd64.RegR8, amd64.RegRSI, feedback.CellStateOffset)
	state.assembler.CmpRegImm32(amd64.RegR8, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessArray, feedback.SlotSetGlobal))
	state.assembler.Jcc(amd64.CondEqual, arrayPath)
	state.assembler.CmpRegImm32(amd64.RegR8, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessHash, feedback.SlotSetGlobal))
	state.assembler.Jcc(amd64.CondEqual, hashPath)
	state.assembler.Jmp(deopt)

	_ = state.assembler.Bind(arrayPath)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRBX, closure.EnvOffset)
	state.emitGuardTableValueAgainstCell(amd64.RegRAX, amd64.RegRSI, deopt)
	state.assembler.MoveRegMem64(amd64.RegR10, amd64.RegR12, slotDisp(src))
	state.emitGuardNotNil(amd64.RegR10, amd64.RegR9, deopt)
	state.emitStoreArrayValueFromCachedCell(amd64.RegRDX, amd64.RegRSI, amd64.RegRDI, amd64.RegR8, amd64.RegR10, deopt)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(hashPath)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRBX, closure.EnvOffset)
	state.emitGuardTableValueAgainstCell(amd64.RegRAX, amd64.RegRSI, deopt)
	state.assembler.MoveRegMem64(amd64.RegR10, amd64.RegR12, slotDisp(src))
	state.emitGuardNotNil(amd64.RegR10, amd64.RegR9, deopt)
	state.emitStoreHashValueFromCachedCell(amd64.RegRDX, amd64.RegRSI, amd64.RegRDI, amd64.RegR8, amd64.RegR10, deopt)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(deopt)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSetGlobal], siteID, stubs.StubSetGlobal, uint64(src), uint64(keyIndex), uint64(slotIndex), 0, 0)
	_ = state.assembler.Bind(done)
}

func (state *compileState) emitSetTable(bytecodePC int, tableSlot int, keyOperand int, valueOperand int, slotIndex uint32) {
	arrayPath := state.assembler.NewLabel()
	hashPath := state.assembler.NewLabel()
	done := state.assembler.NewLabel()
	deopt := state.assembler.NewLabel()
	siteID := state.recordContinuationSite(metadata.ContinuationSetTable, stubs.StubSetTable, bytecodePC, bytecodePC, bytecodePC+1, -1, uint32(tableSlot), uint32(keyOperand), uint32(valueOperand), slotIndex, metadata.ContinuationFlagNativeBuiltinABI|metadata.ContinuationFlagDeoptOnUncovered)

	state.emitLoadClosureObject(amd64.RegRBX)
	state.emitLoadFeedbackCellBase(amd64.RegRSI, amd64.RegRBX, slotIndex, deopt)
	state.assembler.MoveRegMem32(amd64.RegR8, amd64.RegRSI, feedback.CellStateOffset)
	state.assembler.CmpRegImm32(amd64.RegR8, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessArray, feedback.SlotSetTable))
	state.assembler.Jcc(amd64.CondEqual, arrayPath)
	state.assembler.CmpRegImm32(amd64.RegR8, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessHash, feedback.SlotSetTable))
	state.assembler.Jcc(amd64.CondEqual, hashPath)
	state.assembler.Jmp(deopt)

	_ = state.assembler.Bind(arrayPath)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(tableSlot))
	state.emitGuardTableValueAgainstCell(amd64.RegRAX, amd64.RegRSI, deopt)
	state.emitGuardRegisterKeyMatchesCell(keyOperand, amd64.RegRSI, amd64.RegR9, amd64.RegR10, deopt)
	state.emitLoadRKIntoReg(amd64.RegR10, valueOperand)
	state.emitGuardNotNil(amd64.RegR10, amd64.RegR9, deopt)
	state.emitStoreArrayValueFromCachedCell(amd64.RegRDX, amd64.RegRSI, amd64.RegRDI, amd64.RegR8, amd64.RegR10, deopt)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(hashPath)
	state.assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR12, slotDisp(tableSlot))
	state.emitGuardTableValueAgainstCell(amd64.RegRAX, amd64.RegRSI, deopt)
	state.emitGuardRegisterKeyMatchesCell(keyOperand, amd64.RegRSI, amd64.RegR9, amd64.RegR10, deopt)
	state.emitLoadRKIntoReg(amd64.RegR10, valueOperand)
	state.emitGuardNotNil(amd64.RegR10, amd64.RegR9, deopt)
	state.emitStoreHashValueFromCachedCell(amd64.RegRDX, amd64.RegRSI, amd64.RegRDI, amd64.RegR8, amd64.RegR10, deopt)
	state.assembler.Jmp(done)

	_ = state.assembler.Bind(deopt)
	state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSetTable], siteID, stubs.StubSetTable, uint64(tableSlot), uint64(keyOperand), uint64(valueOperand), uint64(slotIndex), 0)
	_ = state.assembler.Bind(done)
}

func (state *compileState) emitLoadClosureObject(dst amd64.Register) {
	state.assembler.MoveRegMem64(dst, amd64.RegR13, state.CallFrameClosureOffset())
	state.emitDecodeHeapRefFromTValue(dst, dst)
}

func (state *compileState) emitLoadFeedbackCellBase(dst amd64.Register, closureObj amd64.Register, slotIndex uint32, deopt *amd64.Label) {
	state.assembler.MoveRegMem64(dst, closureObj, closure.FeedbackVectorOff)
	state.assembler.CmpRegImm32(dst, 0)
	state.assembler.Jcc(amd64.CondEqual, deopt)
	state.assembler.AddRegReg(dst, amd64.HeapBaseRegister)
	state.assembler.AddRegImm32(dst, int32(feedback.CellOffset(slotIndex)))
}

func (state *compileState) emitGuardTableValueAgainstCell(tableValueReg amd64.Register, cellBaseReg amd64.Register, deopt *amd64.Label) {
	state.assembler.MoveRegReg(amd64.RegRCX, tableValueReg)
	state.assembler.ShiftRightRegImm8(amd64.RegRCX, value.TagShift)
	state.assembler.CmpRegImm32(amd64.RegRCX, state.shiftedBoxedTag(value.TagTableRef))
	state.assembler.Jcc(amd64.CondNotEqual, deopt)
	state.emitExtractHeapRefPayload(amd64.RegRCX, tableValueReg)
	state.assembler.MoveRegMem64(amd64.RegR8, cellBaseReg, feedback.CellTableRefOffset)
	state.assembler.CmpRegReg(amd64.RegRCX, amd64.RegR8)
	state.assembler.Jcc(amd64.CondNotEqual, deopt)
	state.emitDecodeHeapRefFromRaw(amd64.RegRDX, amd64.RegRCX)
	state.assembler.MoveRegMem32(amd64.RegR8, amd64.RegRDX, rttable.TableVersionOffset)
	state.assembler.MoveRegMem32(amd64.RegR9, cellBaseReg, feedback.CellTableVersionOffset)
	state.assembler.CmpRegReg(amd64.RegR8, amd64.RegR9)
	state.assembler.Jcc(amd64.CondNotEqual, deopt)
}

func (state *compileState) emitGuardRegisterKeyMatchesCell(operand int, cellBaseReg amd64.Register, keyReg amd64.Register, tempReg amd64.Register, deopt *amd64.Label) {
	if bytecode.IsConstantRK(operand) {
		return
	}
	state.assembler.MoveRegMem64(keyReg, amd64.RegR12, slotDisp(operand))
	state.assembler.MoveRegMem64(tempReg, cellBaseReg, feedback.CellKeyBitsOffset)
	state.assembler.CmpRegReg(keyReg, tempReg)
	state.assembler.Jcc(amd64.CondNotEqual, deopt)
}

func (state *compileState) emitLoadArrayValueFromCachedCell(tableObjReg amd64.Register, cellBaseReg amd64.Register, addrReg amd64.Register, indexReg amd64.Register, deopt *amd64.Label) {
	state.assembler.MoveRegMem64(addrReg, tableObjReg, rttable.ArrayDataOffset)
	state.assembler.CmpRegImm32(addrReg, 0)
	state.assembler.Jcc(amd64.CondEqual, deopt)
	state.assembler.AddRegReg(addrReg, amd64.HeapBaseRegister)
	state.assembler.MoveRegMem32(indexReg, cellBaseReg, feedback.CellCachedIndexOffset)
	state.assembler.ShiftLeftRegImm8(indexReg, 3)
	state.assembler.AddRegReg(addrReg, indexReg)
	state.assembler.MoveRegMem64(amd64.RegRAX, addrReg, 0)
}

func (state *compileState) emitLoadHashValueFromCachedCell(tableObjReg amd64.Register, cellBaseReg amd64.Register, addrReg amd64.Register, indexReg amd64.Register, deopt *amd64.Label) {
	state.assembler.MoveRegMem64(addrReg, tableObjReg, rttable.EntriesDataOffset)
	state.assembler.CmpRegImm32(addrReg, 0)
	state.assembler.Jcc(amd64.CondEqual, deopt)
	state.assembler.AddRegReg(addrReg, amd64.HeapBaseRegister)
	state.assembler.MoveRegMem32(indexReg, cellBaseReg, feedback.CellCachedIndexOffset)
	state.assembler.ShiftLeftRegImm8(indexReg, 5)
	state.assembler.AddRegReg(addrReg, indexReg)
	state.assembler.MoveRegMem64(amd64.RegRAX, addrReg, rttable.EntryValueOffset)
}

func (state *compileState) emitStoreArrayValueFromCachedCell(tableObjReg amd64.Register, cellBaseReg amd64.Register, addrReg amd64.Register, indexReg amd64.Register, valueReg amd64.Register, deopt *amd64.Label) {
	state.assembler.MoveRegMem64(addrReg, tableObjReg, rttable.ArrayDataOffset)
	state.assembler.CmpRegImm32(addrReg, 0)
	state.assembler.Jcc(amd64.CondEqual, deopt)
	state.assembler.AddRegReg(addrReg, amd64.HeapBaseRegister)
	state.assembler.MoveRegMem32(indexReg, cellBaseReg, feedback.CellCachedIndexOffset)
	state.assembler.ShiftLeftRegImm8(indexReg, 3)
	state.assembler.AddRegReg(addrReg, indexReg)
	state.assembler.MoveMemReg64(addrReg, 0, valueReg)
}

func (state *compileState) emitStoreHashValueFromCachedCell(tableObjReg amd64.Register, cellBaseReg amd64.Register, addrReg amd64.Register, indexReg amd64.Register, valueReg amd64.Register, deopt *amd64.Label) {
	state.assembler.MoveRegMem64(addrReg, tableObjReg, rttable.EntriesDataOffset)
	state.assembler.CmpRegImm32(addrReg, 0)
	state.assembler.Jcc(amd64.CondEqual, deopt)
	state.assembler.AddRegReg(addrReg, amd64.HeapBaseRegister)
	state.assembler.MoveRegMem32(indexReg, cellBaseReg, feedback.CellCachedIndexOffset)
	state.assembler.ShiftLeftRegImm8(indexReg, 5)
	state.assembler.AddRegReg(addrReg, indexReg)
	state.assembler.MoveMemReg64(addrReg, rttable.EntryValueOffset, valueReg)
}

func (state *compileState) emitGuardNotNil(valueReg amd64.Register, tempReg amd64.Register, deopt *amd64.Label) {
	state.assembler.MoveRegImm64(tempReg, uint64(value.NilValue().Bits()))
	state.assembler.CmpRegReg(valueReg, tempReg)
	state.assembler.Jcc(amd64.CondEqual, deopt)
}

func (state *compileState) emitLoadConstantToReg(dst amd64.Register, index int) {
	state.assembler.MoveRegMem64(dst, amd64.RegR13, state.CallFrameConstBaseOffset())
	if index != 0 {
		state.assembler.AddRegImm32(dst, slotDisp(index))
	}
	state.assembler.MoveRegMem64(dst, dst, 0)
}

func (state *compileState) emitLoadRKIntoReg(dst amd64.Register, operand int) {
	if bytecode.IsConstantRK(operand) {
		state.emitLoadConstantToReg(dst, bytecode.IndexK(operand))
		return
	}
	state.assembler.MoveRegMem64(dst, amd64.RegR12, slotDisp(operand))
}

func (state *compileState) emitExtractHeapRefPayload(dst amd64.Register, src amd64.Register) {
	if dst != src {
		state.assembler.MoveRegReg(dst, src)
	}
	state.assembler.ShiftLeftRegImm8(dst, 20)
	state.assembler.ShiftRightRegImm8(dst, 20)
}

func (state *compileState) emitDecodeHeapRefFromRaw(dst amd64.Register, src amd64.Register) {
	if dst != src {
		state.assembler.MoveRegReg(dst, src)
	}
	state.assembler.ShiftLeftRegImm8(dst, 4)
	state.assembler.AddRegReg(dst, amd64.HeapBaseRegister)
}

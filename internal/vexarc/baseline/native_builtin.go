package baseline

import (
	"vexlua/internal/bytecode"
	rtclosure "vexlua/internal/runtime/closure"
	"vexlua/internal/runtime/feedback"
	rthost "vexlua/internal/runtime/host"
	rtproto "vexlua/internal/runtime/proto"
	rtstate "vexlua/internal/runtime/state"
	rtstring "vexlua/internal/runtime/string"
	rttable "vexlua/internal/runtime/table"
	rtupvalue "vexlua/internal/runtime/upvalue"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/abi"
	"vexlua/internal/vexarc/amd64"
)

const builtinBodyCallBlockBaseOffset = 0x10

const builtinTerminalExitStackAdjust = abi.StubCallBlockSize + 16

const (
	builtinScratchTableRefOffset = rtstate.StubCallBlockFrameOffset
	builtinScratchPayload0Offset = rtstate.StubCallBlockArg0Offset
	builtinScratchPayload1Offset = rtstate.StubCallBlockArg1Offset
	builtinScratchPayload2Offset = rtstate.StubCallBlockArg2Offset
	builtinScratchPayload3Offset = rtstate.StubCallBlockArg3Offset
)

func buildBuiltinEntryVeneer(body uintptr) []byte {
	assembler := amd64.NewAssembler(16)
	assembler.MoveRegImm64(amd64.RegR10, uint64(body))
	assembler.CallReg(amd64.RegR10)
	assembler.Ret()
	return assembler.Buffer().Bytes()
}

func buildGetUpvalueBuiltinBody() []byte {
	assembler := amd64.NewAssembler(160)
	loadClosed := assembler.NewLabel()
	loadOpen := assembler.NewLabel()
	errorPath := assembler.NewLabel()
	storeResult := assembler.NewLabel()

	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg2Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload3Offset, amd64.RegR8)
	emitLoadClosureObjectFromFrame(assembler, amd64.RegRAX)
	assembler.MoveRegMem64(amd64.RegR9, amd64.RegRAX, rtclosure.UpvaluesDataOffset)
	assembler.CmpRegImm32(amd64.RegR9, 0)
	assembler.Jcc(amd64.CondEqual, errorPath)
	assembler.AddRegReg(amd64.RegR9, amd64.HeapBaseRegister)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg1Offset)
	assembler.ShiftLeftRegImm8(amd64.RegR8, 3)
	assembler.AddRegReg(amd64.RegR9, amd64.RegR8)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, 0)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR10)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegR10)
	assembler.MoveRegMem32(amd64.RegRCX, amd64.RegR10, rtupvalue.StateOffset)
	assembler.CmpRegImm32(amd64.RegRCX, uint32(rtupvalue.StateClosed))
	assembler.Jcc(amd64.CondEqual, loadClosed)
	assembler.CmpRegImm32(amd64.RegRCX, uint32(rtupvalue.StateOpen))
	assembler.Jcc(amd64.CondEqual, loadOpen)
	assembler.Jmp(errorPath)

	_ = assembler.Bind(loadOpen)
	assembler.MoveRegMem64(amd64.RegRDX, amd64.RegR10, rtupvalue.SlotAddrOffset)
	assembler.CmpRegImm32(amd64.RegRDX, 0)
	assembler.Jcc(amd64.CondEqual, errorPath)
	assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRDX, 0)
	assembler.MoveRegReg(amd64.RegRDX, amd64.RegRAX)
	emitUpdateUpvalueFeedbackEligible(assembler, feedback.SlotGetUpvalue, feedback.AccessUpvalueOpen, amd64.RegRBX, amd64.RegRDX)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	assembler.Jmp(storeResult)

	_ = assembler.Bind(loadClosed)
	assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR10, rtupvalue.ClosedValueOffset)
	assembler.MoveRegReg(amd64.RegRDX, amd64.RegRAX)
	emitUpdateUpvalueFeedbackEligible(assembler, feedback.SlotGetUpvalue, feedback.AccessUpvalueClosed, amd64.RegRBX, amd64.RegRDX)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)

	_ = assembler.Bind(storeResult)
	assembler.MoveRegReg(amd64.RegRDX, amd64.RegRAX)
	emitStoreResultRegisterFromCallArgReg(assembler, rtstate.StubCallBlockArg0Offset, amd64.RegRDX, amd64.RegR8, amd64.RegR9)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(errorPath)
	emitUpdateUpvalueFeedbackIneligible(assembler, feedback.SlotGetUpvalue)
	emitExitBuiltinStatus(assembler, compiledStatusError, true)
	return assembler.Buffer().Bytes()
}

func buildSetUpvalueBuiltinBody() []byte {
	assembler := amd64.NewAssembler(160)
	storeClosed := assembler.NewLabel()
	storeOpen := assembler.NewLabel()
	errorPath := assembler.NewLabel()
	done := assembler.NewLabel()

	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg2Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload3Offset, amd64.RegR8)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg0Offset)
	assembler.ShiftLeftRegImm8(amd64.RegR8, 3)
	assembler.MoveRegReg(amd64.RegR9, amd64.RegsBaseRegister)
	assembler.AddRegReg(amd64.RegR9, amd64.RegR8)
	assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR9, 0)

	emitLoadClosureObjectFromFrame(assembler, amd64.RegR9)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, rtclosure.UpvaluesDataOffset)
	assembler.CmpRegImm32(amd64.RegR10, 0)
	assembler.Jcc(amd64.CondEqual, errorPath)
	assembler.AddRegReg(amd64.RegR10, amd64.HeapBaseRegister)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg1Offset)
	assembler.ShiftLeftRegImm8(amd64.RegR8, 3)
	assembler.AddRegReg(amd64.RegR10, amd64.RegR8)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR10, 0)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR10)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegR10)
	assembler.MoveRegMem32(amd64.RegRCX, amd64.RegR10, rtupvalue.StateOffset)
	assembler.CmpRegImm32(amd64.RegRCX, uint32(rtupvalue.StateClosed))
	assembler.Jcc(amd64.CondEqual, storeClosed)
	assembler.CmpRegImm32(amd64.RegRCX, uint32(rtupvalue.StateOpen))
	assembler.Jcc(amd64.CondEqual, storeOpen)
	assembler.Jmp(errorPath)

	_ = assembler.Bind(storeOpen)
	assembler.MoveRegMem64(amd64.RegRDX, amd64.RegR10, rtupvalue.SlotAddrOffset)
	assembler.CmpRegImm32(amd64.RegRDX, 0)
	assembler.Jcc(amd64.CondEqual, errorPath)
	assembler.MoveMemReg64(amd64.RegRDX, 0, amd64.RegRAX)
	emitUpdateUpvalueFeedbackEligible(assembler, feedback.SlotSetUpvalue, feedback.AccessUpvalueOpen, amd64.RegRBX, amd64.RegRAX)
	assembler.Jmp(done)

	_ = assembler.Bind(storeClosed)
	assembler.MoveMemReg64(amd64.RegR10, rtupvalue.ClosedValueOffset, amd64.RegRAX)
	emitUpdateUpvalueFeedbackEligible(assembler, feedback.SlotSetUpvalue, feedback.AccessUpvalueClosed, amd64.RegRBX, amd64.RegRAX)

	_ = assembler.Bind(done)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(errorPath)
	emitUpdateUpvalueFeedbackIneligible(assembler, feedback.SlotSetUpvalue)
	emitExitBuiltinStatus(assembler, compiledStatusError, true)
	return assembler.Buffer().Bytes()
}

func buildForPrepBuiltinBody() []byte {
	assembler := amd64.NewAssembler(192)
	errorPath := assembler.NewLabel()

	emitLoadLoopSlotAddress(assembler, amd64.RegR9, amd64.RegR8)
	emitLoadNumberFromSlotAddress(assembler, amd64.RegR9, amd64.RegRAX, amd64.RegRCX, amd64.XMM0, errorPath)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegR9)
	assembler.AddRegImm32(amd64.RegR10, int32(value.TValueSize*2))
	emitLoadNumberFromSlotAddress(assembler, amd64.RegR10, amd64.RegRAX, amd64.RegRCX, amd64.XMM2, errorPath)
	assembler.SubsdXmmXmm(amd64.XMM0, amd64.XMM2)
	assembler.MoveMemXmm64(amd64.RegR9, 0, amd64.XMM0)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(errorPath)
	emitExitBuiltinStatus(assembler, compiledStatusError, true)
	return assembler.Buffer().Bytes()
}

func buildForLoopBuiltinBody() []byte {
	assembler := amd64.NewAssembler(256)
	errorPath := assembler.NewLabel()
	positiveStep := assembler.NewLabel()
	continueLoop := assembler.NewLabel()
	exitLoop := assembler.NewLabel()

	emitLoadLoopSlotAddress(assembler, amd64.RegR9, amd64.RegR8)
	emitLoadNumberFromSlotAddress(assembler, amd64.RegR9, amd64.RegRAX, amd64.RegRCX, amd64.XMM0, errorPath)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegR9)
	assembler.AddRegImm32(amd64.RegR10, int32(value.TValueSize))
	emitLoadNumberFromSlotAddress(assembler, amd64.RegR10, amd64.RegRAX, amd64.RegRCX, amd64.XMM1, errorPath)
	assembler.AddRegImm32(amd64.RegR10, int32(value.TValueSize))
	emitLoadNumberFromSlotAddress(assembler, amd64.RegR10, amd64.RegRAX, amd64.RegRCX, amd64.XMM2, errorPath)

	assembler.AddsdXmmXmm(amd64.XMM0, amd64.XMM2)
	assembler.MoveMemXmm64(amd64.RegR9, 0, amd64.XMM0)
	assembler.XorpsXmmXmm(amd64.XMM3, amd64.XMM3)
	assembler.UcomisdXmmXmm(amd64.XMM2, amd64.XMM3)
	assembler.Jcc(amd64.CondParity, exitLoop)
	assembler.Jcc(amd64.CondAbove, positiveStep)
	assembler.UcomisdXmmXmm(amd64.XMM1, amd64.XMM0)
	assembler.Jcc(amd64.CondParity, exitLoop)
	assembler.Jcc(amd64.CondBelowEqual, continueLoop)
	assembler.Jmp(exitLoop)

	_ = assembler.Bind(positiveStep)
	assembler.UcomisdXmmXmm(amd64.XMM0, amd64.XMM1)
	assembler.Jcc(amd64.CondParity, exitLoop)
	assembler.Jcc(amd64.CondBelowEqual, continueLoop)
	assembler.Jmp(exitLoop)

	_ = assembler.Bind(continueLoop)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegR9)
	assembler.AddRegImm32(amd64.RegR10, int32(value.TValueSize*3))
	assembler.MoveMemXmm64(amd64.RegR10, 0, amd64.XMM0)
	assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, execCtxFlagAlternateResume)
	emitReturnBuiltinContinuation(assembler, false)

	_ = assembler.Bind(exitLoop)
	assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, 0)
	emitReturnBuiltinContinuation(assembler, false)

	_ = assembler.Bind(errorPath)
	emitExitBuiltinStatus(assembler, compiledStatusError, true)
	return assembler.Buffer().Bytes()
}

func buildNewTableBuiltinBody() []byte {
	assembler := amd64.NewAssembler(32)
	emitExitCurrentBuiltinToRuntime(assembler, true)
	return assembler.Buffer().Bytes()
}

func buildSelfBuiltinBody() []byte {
	assembler := amd64.NewAssembler(1792)
	tablePath := assembler.NewLabel()
	lookupLoop := assembler.NewLabel()
	nextEntry := assembler.NewLabel()
	foundEntry := assembler.NewLabel()
	notFound := assembler.NewLabel()
	storeResult := assembler.NewLabel()
	keyDeopt := assembler.NewLabel()
	blockedMissDeopt := assembler.NewLabel()
	deoptPath := assembler.NewLabel()

	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg1Offset)
	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegR8, amd64.RegRDX, amd64.RegR10)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg0Offset)
	assembler.AddRegImm32(amd64.RegR9, 1)
	assembler.ShiftLeftRegImm8(amd64.RegR9, 3)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegsBaseRegister)
	assembler.AddRegReg(amd64.RegR10, amd64.RegR9)
	assembler.MoveMemReg64(amd64.RegR10, 0, amd64.RegRDX)

	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg2Offset)
	emitLoadRKValueFromOperandReg(assembler, amd64.RegR9, amd64.RegRCX, amd64.RegRAX, amd64.RegR10)

	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	assembler.ShiftRightRegImm8(amd64.RegRAX, value.TagShift)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagTableRef))
	assembler.Jcc(amd64.CondEqual, tablePath)
	assembler.Jmp(deoptPath)

	_ = assembler.Bind(tablePath)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRCX)
	assembler.ShiftRightRegImm8(amd64.RegRAX, value.TagShift)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagStringRef))
	assembler.Jcc(amd64.CondNotEqual, keyDeopt)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRAX, amd64.RegRAX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegRAX)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegRAX)
	assembler.MoveRegImm64(amd64.RegRDX, uint64(value.NilValue().Bits()))
	assembler.MoveRegMem64(amd64.RegR9, amd64.RegRAX, rttable.EntriesDataOffset)
	assembler.CmpRegImm32(amd64.RegR9, 0)
	assembler.Jcc(amd64.CondEqual, notFound)
	assembler.AddRegReg(amd64.RegR9, amd64.HeapBaseRegister)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegRAX, rttable.HashCapacityOffset)
	emitStoreBuiltinScratch32(assembler, builtinScratchPayload1Offset, amd64.RegR10)
	assembler.XorRegReg(amd64.RegR8, amd64.RegR8)

	_ = assembler.Bind(lookupLoop)
	emitLoadBuiltinScratch32(assembler, amd64.RegR10, builtinScratchPayload1Offset)
	assembler.CmpRegReg(amd64.RegR8, amd64.RegR10)
	assembler.Jcc(amd64.CondAboveEqual, notFound)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, rttable.EntryKeyOffset)
	assembler.CmpRegReg(amd64.RegR10, amd64.RegRCX)
	assembler.Jcc(amd64.CondNotEqual, nextEntry)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, rttable.EntryValueOffset)
	assembler.CmpRegReg(amd64.RegR10, amd64.RegRDX)
	assembler.Jcc(amd64.CondEqual, nextEntry)
	assembler.MoveRegReg(amd64.RegRDX, amd64.RegR10)
	assembler.MoveRegImm32(amd64.RegRAX, 1)
	assembler.Jmp(foundEntry)

	_ = assembler.Bind(nextEntry)
	assembler.AddRegImm32(amd64.RegR9, rttable.EntrySize)
	assembler.AddRegImm32(amd64.RegR8, 1)
	assembler.Jmp(lookupLoop)

	_ = assembler.Bind(notFound)
	assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	assembler.XorRegReg(amd64.RegR8, amd64.RegR8)

	_ = assembler.Bind(foundEntry)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegRBX, rttable.FlagsOffset)
	assembler.AndRegImm32(amd64.RegR10, uint32(rttable.FlagIndexFastPathBlocked|rttable.FlagWeakKeys|rttable.FlagWeakValues|rttable.FlagRehashing))
	assembler.CmpRegImm32(amd64.RegRAX, 0)
	assembler.Jcc(amd64.CondNotEqual, storeResult)
	assembler.CmpRegImm32(amd64.RegR10, 0)
	assembler.Jcc(amd64.CondNotEqual, blockedMissDeopt)
	assembler.Jmp(storeResult)

	_ = assembler.Bind(storeResult)
	emitStoreResultRegisterFromCallArgReg(assembler, rtstate.StubCallBlockArg0Offset, amd64.RegRDX, amd64.RegR9, amd64.RegR10)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(keyDeopt)
	assembler.Jmp(deoptPath)

	_ = assembler.Bind(blockedMissDeopt)
	assembler.Jmp(deoptPath)

	_ = assembler.Bind(deoptPath)
	emitExitBuiltinStatus(assembler, compiledStatusDeopt, true)
	return assembler.Buffer().Bytes()
}

func buildSetListBuiltinBody() []byte {
	assembler := amd64.NewAssembler(512)
	openCount := assembler.NewLabel()
	countReady := assembler.NewLabel()
	noValues := assembler.NewLabel()
	runtimeDispatch := assembler.NewLabel()
	deoptPath := assembler.NewLabel()
	writeLoop := assembler.NewLabel()
	oldNil := assembler.NewLabel()
	storeSlot := assembler.NewLabel()
	storeValue := assembler.NewLabel()
	afterWrite := assembler.NewLabel()
	bumpVersion := assembler.NewLabel()
	versionReady := assembler.NewLabel()
	refreshZero := assembler.NewLabel()
	refreshLoop := assembler.NewLabel()
	refreshDone := assembler.NewLabel()

	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg0Offset)
	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegR8, amd64.RegRAX, amd64.RegR10)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegRAX)
	assembler.ShiftRightRegImm8(amd64.RegR10, value.TagShift)
	assembler.CmpRegImm32(amd64.RegR10, shiftedBoxedTag(value.TagTableRef))
	assembler.Jcc(amd64.CondNotEqual, deoptPath)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegR9, amd64.RegRAX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegR9)

	emitLoadBuiltinCallArg64(assembler, amd64.RegRCX, rtstate.StubCallBlockArg1Offset)
	assembler.CmpRegImm32(amd64.RegRCX, 0)
	assembler.Jcc(amd64.CondEqual, openCount)
	assembler.Jmp(countReady)

	_ = assembler.Bind(openCount)
	assembler.MoveRegMem32(amd64.RegRCX, amd64.RegR13, rtstate.CallFrameTopOffset)
	assembler.AndRegImm32(amd64.RegRCX, 0xFFFF)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg0Offset)
	assembler.AddRegImm32(amd64.RegR8, 1)
	assembler.XorRegReg(amd64.RegRBP, amd64.RegRBP)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegRCX)
	emitCountOpenCallArguments(assembler, amd64.RegR8, amd64.RegRCX, amd64.RegRBP, amd64.RegR10)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegRBP)

	_ = assembler.Bind(countReady)
	assembler.CmpRegImm32(amd64.RegRCX, 0)
	assembler.Jcc(amd64.CondEqual, noValues)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegR9, rttable.FlagsOffset)
	assembler.AndRegImm32(amd64.RegR10, uint32(rttable.FlagNewIndexFastPathBlocked|rttable.FlagWeakKeys|rttable.FlagWeakValues|rttable.FlagRehashing|rttable.FlagFrozen|rttable.FlagReadOnly))
	assembler.CmpRegImm32(amd64.RegR10, 0)
	assembler.Jcc(amd64.CondNotEqual, deoptPath)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg2Offset)
	assembler.CmpRegImm32(amd64.RegR8, 0)
	assembler.Jcc(amd64.CondEqual, deoptPath)
	assembler.SubRegImm32(amd64.RegR8, 1)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegR8)
	assembler.ShiftLeftRegImm8(amd64.RegR10, 1)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegR8)
	assembler.ShiftLeftRegImm8(amd64.RegRAX, 4)
	assembler.AddRegReg(amd64.RegR10, amd64.RegRAX)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegR8)
	assembler.ShiftLeftRegImm8(amd64.RegRAX, 5)
	assembler.AddRegReg(amd64.RegR10, amd64.RegRAX)
	assembler.AddRegImm32(amd64.RegR10, 1)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegR10)
	assembler.AddRegReg(amd64.RegRAX, amd64.RegRCX)
	assembler.SubRegImm32(amd64.RegRAX, 1)
	assembler.MoveRegMem32(amd64.RegRDX, amd64.RegR9, rttable.ArrayCapOffset)
	assembler.CmpRegReg(amd64.RegRAX, amd64.RegRDX)
	assembler.Jcc(amd64.CondAbove, runtimeDispatch)
	assembler.MoveRegMem64(amd64.RegRDX, amd64.RegR9, rttable.ArrayDataOffset)
	assembler.CmpRegImm32(amd64.RegRDX, 0)
	assembler.Jcc(amd64.CondEqual, runtimeDispatch)
	assembler.AddRegReg(amd64.RegRDX, amd64.HeapBaseRegister)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR10)
	assembler.SubRegImm32(amd64.RegRBX, 1)
	assembler.ShiftLeftRegImm8(amd64.RegRBX, 3)
	assembler.AddRegReg(amd64.RegRDX, amd64.RegRBX)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg0Offset)
	assembler.AddRegImm32(amd64.RegR8, 1)
	assembler.MoveRegImm64(amd64.RegRSI, uint64(value.NilValue().Bits()))
	assembler.XorRegReg(amd64.RegRDI, amd64.RegRDI)

	_ = assembler.Bind(writeLoop)
	assembler.CmpRegImm32(amd64.RegRCX, 0)
	assembler.Jcc(amd64.CondEqual, afterWrite)
	assembler.MoveRegMem64(amd64.RegRBX, amd64.RegRDX, 0)
	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegR8, amd64.RegRAX, amd64.RegR10)
	assembler.CmpRegReg(amd64.RegRBX, amd64.RegRSI)
	assembler.Jcc(amd64.CondEqual, oldNil)
	assembler.CmpRegReg(amd64.RegRAX, amd64.RegRSI)
	assembler.Jcc(amd64.CondNotEqual, storeSlot)
	assembler.MoveRegImm32(amd64.RegRDI, 1)
	assembler.Jmp(storeSlot)

	_ = assembler.Bind(oldNil)
	assembler.CmpRegReg(amd64.RegRAX, amd64.RegRSI)
	assembler.Jcc(amd64.CondEqual, storeSlot)
	assembler.MoveRegImm32(amd64.RegRDI, 1)

	_ = assembler.Bind(storeValue)
	assembler.Jmp(storeSlot)

	_ = assembler.Bind(storeSlot)
	assembler.MoveMemReg64(amd64.RegRDX, 0, amd64.RegRAX)
	assembler.AddRegImm32(amd64.RegRDX, int32(value.TValueSize))
	assembler.AddRegImm32(amd64.RegR8, 1)
	assembler.AddRegImm32(amd64.RegRCX, -1)
	assembler.Jmp(writeLoop)

	_ = assembler.Bind(afterWrite)
	assembler.CmpRegImm32(amd64.RegRDI, 0)
	assembler.Jcc(amd64.CondEqual, versionReady)
	assembler.Jmp(bumpVersion)

	_ = assembler.Bind(bumpVersion)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR9, rttable.TableVersionOffset)
	assembler.AddRegImm32(amd64.RegRAX, 1)
	assembler.CmpRegImm32(amd64.RegRAX, 0)
	assembler.Jcc(amd64.CondNotEqual, versionReady)
	assembler.AddRegImm32(amd64.RegRAX, 1)
	assembler.MoveMemReg32(amd64.RegR9, rttable.TableVersionOffset, amd64.RegRAX)

	_ = assembler.Bind(versionReady)
	assembler.MoveRegMem32(amd64.RegRCX, amd64.RegR9, rttable.ArrayCapOffset)
	assembler.CmpRegImm32(amd64.RegRCX, 0)
	assembler.Jcc(amd64.CondEqual, refreshZero)
	assembler.MoveRegMem64(amd64.RegRDX, amd64.RegR9, rttable.ArrayDataOffset)
	assembler.CmpRegImm32(amd64.RegRDX, 0)
	assembler.Jcc(amd64.CondEqual, refreshZero)
	assembler.AddRegReg(amd64.RegRDX, amd64.HeapBaseRegister)
	assembler.XorRegReg(amd64.RegR8, amd64.RegR8)
	assembler.XorRegReg(amd64.RegR10, amd64.RegR10)
	assembler.Jmp(refreshLoop)

	_ = assembler.Bind(refreshZero)
	assembler.XorRegReg(amd64.RegR10, amd64.RegR10)
	assembler.Jmp(refreshDone)

	_ = assembler.Bind(refreshLoop)
	assembler.CmpRegReg(amd64.RegR8, amd64.RegRCX)
	assembler.Jcc(amd64.CondAboveEqual, refreshDone)
	assembler.MoveRegMem64(amd64.RegRAX, amd64.RegRDX, 0)
	assembler.CmpRegReg(amd64.RegRAX, amd64.RegRSI)
	assembler.Jcc(amd64.CondEqual, refreshDone)
	assembler.AddRegImm32(amd64.RegR8, 1)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegR8)
	assembler.AddRegImm32(amd64.RegRDX, int32(value.TValueSize))
	assembler.Jmp(refreshLoop)

	_ = assembler.Bind(refreshDone)
	assembler.MoveMemReg32(amd64.RegR9, rttable.ArrayLenHintOffset, amd64.RegR10)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(noValues)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(runtimeDispatch)
	emitExitCurrentBuiltinToRuntime(assembler, true)

	_ = assembler.Bind(deoptPath)
	emitExitBuiltinStatus(assembler, compiledStatusDeopt, true)
	return assembler.Buffer().Bytes()
}

func buildLenBuiltinBody() []byte {
	assembler := amd64.NewAssembler(256)
	stringPath := assembler.NewLabel()
	tablePath := assembler.NewLabel()
	deoptPath := assembler.NewLabel()

	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg1Offset)
	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegR8, amd64.RegRAX, amd64.RegR9)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegRAX)
	assembler.ShiftRightRegImm8(amd64.RegR10, value.TagShift)
	assembler.CmpRegImm32(amd64.RegR10, shiftedBoxedTag(value.TagStringRef))
	assembler.Jcc(amd64.CondEqual, stringPath)
	assembler.CmpRegImm32(amd64.RegR10, shiftedBoxedTag(value.TagTableRef))
	assembler.Jcc(amd64.CondEqual, tablePath)
	assembler.Jmp(deoptPath)

	_ = assembler.Bind(stringPath)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRDX, amd64.RegRAX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegRDX)
	assembler.MoveRegMem32(amd64.RegRCX, amd64.RegRDX, rtstring.LengthOffset)
	assembler.Cvtsi2sdXmmReg64(amd64.XMM0, amd64.RegRCX)
	emitStoreResultRegisterFromCallArgXmm(assembler, rtstate.StubCallBlockArg0Offset, amd64.XMM0, amd64.RegR8, amd64.RegR9)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(tablePath)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRDX, amd64.RegRAX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegRDX)
	assembler.MoveRegMem32(amd64.RegRCX, amd64.RegRDX, rttable.FlagsOffset)
	assembler.AndRegImm32(amd64.RegRCX, uint32(rttable.FlagHasMetatable))
	assembler.CmpRegImm32(amd64.RegRCX, 0)
	assembler.Jcc(amd64.CondNotEqual, deoptPath)
	assembler.MoveRegMem32(amd64.RegRCX, amd64.RegRDX, rttable.ArrayLenHintOffset)
	assembler.Cvtsi2sdXmmReg64(amd64.XMM0, amd64.RegRCX)
	emitStoreResultRegisterFromCallArgXmm(assembler, rtstate.StubCallBlockArg0Offset, amd64.XMM0, amd64.RegR8, amd64.RegR9)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(deoptPath)
	emitExitBuiltinStatus(assembler, compiledStatusDeopt, true)
	return assembler.Buffer().Bytes()
}

func buildConcatBuiltinBody() []byte {
	assembler := amd64.NewAssembler(320)
	loop := assembler.NewLabel()
	checkBoxed := assembler.NewLabel()
	boundaryPath := assembler.NewLabel()
	deoptPath := assembler.NewLabel()

	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg1Offset)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg2Offset)
	assembler.CmpRegReg(amd64.RegR8, amd64.RegR9)
	assembler.Jcc(amd64.CondAbove, deoptPath)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload1Offset, amd64.RegR9)

	_ = assembler.Bind(loop)
	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegR8, amd64.RegRAX, amd64.RegRBX)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegRAX)
	assembler.ShiftRightRegImm8(amd64.RegR10, 48)
	assembler.CmpRegImm32(amd64.RegR10, 0xFFFF)
	assembler.Jcc(amd64.CondEqual, checkBoxed)
	assembler.AddRegImm32(amd64.RegR8, 1)
	emitLoadBuiltinScratch64(assembler, amd64.RegR9, builtinScratchPayload1Offset)
	assembler.CmpRegReg(amd64.RegR8, amd64.RegR9)
	assembler.Jcc(amd64.CondBelowEqual, loop)
	assembler.Jmp(boundaryPath)

	_ = assembler.Bind(checkBoxed)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegRAX)
	assembler.ShiftRightRegImm8(amd64.RegR10, value.TagShift)
	assembler.CmpRegImm32(amd64.RegR10, shiftedBoxedTag(value.TagStringRef))
	assembler.Jcc(amd64.CondNotEqual, deoptPath)
	assembler.AddRegImm32(amd64.RegR8, 1)
	emitLoadBuiltinScratch64(assembler, amd64.RegR9, builtinScratchPayload1Offset)
	assembler.CmpRegReg(amd64.RegR8, amd64.RegR9)
	assembler.Jcc(amd64.CondBelowEqual, loop)

	_ = assembler.Bind(boundaryPath)
	emitExitCurrentBuiltinToRuntime(assembler, true)

	_ = assembler.Bind(deoptPath)
	emitExitBuiltinStatus(assembler, compiledStatusDeopt, true)
	return assembler.Buffer().Bytes()
}

func buildLuaCallBuiltinBody() []byte {
	assembler := amd64.NewAssembler(4096)
	runtimeDispatch := assembler.NewLabel()
	hostDispatch := assembler.NewLabel()
	luaClosure := assembler.NewLabel()
	openArgCount := assembler.NewLabel()
	callArgCountReady := assembler.NewLabel()
	resultSlotsReady := assembler.NewLabel()
	regCountReady := assembler.NewLabel()
	openArgCopy := assembler.NewLabel()
	copyCountReady := assembler.NewLabel()
	callArgsCopied := assembler.NewLabel()
	callOK := assembler.NewLabel()
	callStub := assembler.NewLabel()
	callDeopt := assembler.NewLabel()
	callError := assembler.NewLabel()
	openResults := assembler.NewLabel()
	callDone := assembler.NewLabel()
	noResults := assembler.NewLabel()
	fixedResultBaseReady := assembler.NewLabel()
	fixedTopBaseReady := assembler.NewLabel()
	openResultBaseReady := assembler.NewLabel()
	openTopBaseReady := assembler.NewLabel()
	noResultBaseReady := assembler.NewLabel()

	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR11, execCtxSiteIDOffset)
	emitStoreBuiltinScratch64(assembler, builtinScratchTableRefOffset, amd64.RegRAX)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg0Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload0Offset, amd64.RegR8)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg1Offset)
	assembler.CmpRegImm32(amd64.RegR9, 0)
	assembler.Jcc(amd64.CondEqual, openArgCount)
	assembler.AddRegImm32(amd64.RegR9, -1)
	assembler.MoveRegReg(amd64.RegRSI, amd64.RegR9)
	assembler.Jmp(callArgCountReady)

	_ = assembler.Bind(openArgCount)
	assembler.XorRegReg(amd64.RegRSI, amd64.RegRSI)

	_ = assembler.Bind(callArgCountReady)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR10, rtstate.StubCallBlockArg2Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload1Offset, amd64.RegR10)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload2Offset, amd64.RegR12)

	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegR8, amd64.RegRDX, amd64.RegRAX)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	assembler.ShiftRightRegImm8(amd64.RegRAX, value.TagShift)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagLuaClosureRef))
	assembler.Jcc(amd64.CondEqual, luaClosure)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagHostFunctionRef))
	assembler.Jcc(amd64.CondEqual, hostDispatch)
	emitUpdateCallFeedbackIneligible(assembler, feedback.SlotCall)
	assembler.Jmp(runtimeDispatch)

	_ = assembler.Bind(hostDispatch)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRCX, amd64.RegRDX)
	emitUpdateCallFeedbackEligible(assembler, feedback.SlotCall, feedback.AccessCallHostFunction, amd64.RegRDX, amd64.RegRCX)
	emitRefreshHostObjectWrapper(assembler, amd64.RegRDX, amd64.RegRAX, amd64.RegR8, amd64.RegR10)
	assembler.MoveRegMem32(amd64.RegRCX, amd64.RegRAX, rthost.WrapperFlagsOffset)
	assembler.AndRegImm32(amd64.RegRCX, uint32(rthost.WrapperFlagCallable))
	assembler.CmpRegImm32(amd64.RegRCX, uint32(rthost.WrapperFlagCallable))
	assembler.Jcc(amd64.CondNotEqual, runtimeDispatch)
	assembler.MoveRegMem64(amd64.RegR8, amd64.RegRAX, rthost.WrapperNativeMetaOffset)
	assembler.CmpRegImm32(amd64.RegR8, 0)
	assembler.Jcc(amd64.CondEqual, runtimeDispatch)
	assembler.AddRegReg(amd64.RegR8, amd64.HeapBaseRegister)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegR8, rthost.NativeDescriptorKindOffset)
	assembler.CmpRegImm32(amd64.RegR10, uint32(rthost.DescriptorKindFunction))
	assembler.Jcc(amd64.CondNotEqual, runtimeDispatch)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegR8, rthost.NativeDescriptorArityOffset)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR10)
	assembler.AndRegImm32(amd64.RegRCX, 0xFFFF)
	assembler.ShiftRightRegImm8(amd64.RegR10, 16)
	assembler.AndRegImm32(amd64.RegR10, uint32(rthost.DescriptorFlagVariadic))
	assembler.CmpRegImm32(amd64.RegR10, 0)
	assembler.Jcc(amd64.CondNotEqual, runtimeDispatch)
	assembler.CmpRegReg(amd64.RegRCX, amd64.RegRSI)
	assembler.Jcc(amd64.CondNotEqual, runtimeDispatch)
	assembler.Jmp(runtimeDispatch)

	_ = assembler.Bind(luaClosure)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRCX, amd64.RegRDX)
	emitUpdateCallFeedbackEligible(assembler, feedback.SlotCall, feedback.AccessCallLuaClosure, amd64.RegRDX, amd64.RegRCX)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRAX, amd64.RegRDX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegRAX)
	assembler.MoveRegMem64(amd64.RegRBX, amd64.RegRAX, rtclosure.ProtoOffset)
	assembler.MoveRegReg(amd64.RegR8, amd64.RegRBX)
	assembler.ShiftRightRegImm8(amd64.RegR8, value.TagShift)
	assembler.CmpRegImm32(amd64.RegR8, shiftedBoxedTag(value.TagProtoRef))
	assembler.Jcc(amd64.CondNotEqual, runtimeDispatch)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegR8, amd64.RegRBX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegR8)
	assembler.MoveRegMem64(amd64.RegR9, amd64.RegR8, rtproto.CompiledEntryOff)
	assembler.CmpRegImm32(amd64.RegR9, 0)
	assembler.Jcc(amd64.CondEqual, runtimeDispatch)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegR8, rtproto.MaxStackSizeOff)
	assembler.AndRegImm32(amd64.RegR10, 0xFF)
	assembler.CmpRegImm32(amd64.RegR10, 0)
	assembler.Jcc(amd64.CondNotEqual, regCountReady)
	assembler.MoveRegImm32(amd64.RegR10, 1)

	_ = assembler.Bind(regCountReady)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR8, rtproto.VarargFlagsOff)
	assembler.AndRegImm32(amd64.RegRAX, 0xFF)
	assembler.CmpRegImm32(amd64.RegRAX, 0)
	assembler.Jcc(amd64.CondNotEqual, runtimeDispatch)
	assembler.MoveRegReg(amd64.RegRDI, amd64.RegR10)
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchPayload1Offset)
	assembler.CmpRegImm32(amd64.RegRAX, 0)
	assembler.Jcc(amd64.CondEqual, resultSlotsReady)
	assembler.AddRegImm32(amd64.RegRAX, -1)
	assembler.CmpRegReg(amd64.RegRAX, amd64.RegRDI)
	assembler.Jcc(amd64.CondBelowEqual, resultSlotsReady)
	assembler.MoveRegReg(amd64.RegRDI, amd64.RegRAX)

	_ = assembler.Bind(resultSlotsReady)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR13, rtstate.CallFrameRegisterCountOff)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegRAX)
	assembler.AndRegImm32(amd64.RegRCX, 0xFFFF)
	assembler.ShiftRightRegImm8(amd64.RegRAX, 16)
	assembler.AddRegReg(amd64.RegRCX, amd64.RegRAX)
	assembler.ShiftLeftRegImm8(amd64.RegRCX, 3)
	assembler.MoveRegReg(amd64.RegR9, amd64.RegR12)
	assembler.AddRegReg(amd64.RegR9, amd64.RegRCX)

	emitLoadThreadStateHeader(assembler, amd64.RegRAX)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR13)
	assembler.AddRegImm32(amd64.RegRCX, rtstate.CallFrameHeaderSize*2)
	assembler.MoveRegMem64(amd64.RegRBX, amd64.RegRAX, rtstate.ThreadFrameEndOffset)
	assembler.CmpRegReg(amd64.RegRCX, amd64.RegRBX)
	assembler.Jcc(amd64.CondAbove, runtimeDispatch)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR10)
	assembler.AddRegReg(amd64.RegRCX, amd64.RegRDI)
	assembler.ShiftLeftRegImm8(amd64.RegRCX, 3)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR9)
	assembler.AddRegReg(amd64.RegRBX, amd64.RegRCX)
	assembler.MoveRegMem64(amd64.RegRCX, amd64.RegRAX, rtstate.ThreadStackEndOffset)
	assembler.CmpRegReg(amd64.RegRBX, amd64.RegRCX)
	assembler.Jcc(amd64.CondAbove, runtimeDispatch)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR10)
	assembler.AddRegReg(amd64.RegRCX, amd64.RegRDI)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR9)
	emitZeroSlots(assembler, amd64.RegRBX, amd64.RegRCX)

	emitLoadBuiltinCallFlags32(assembler, amd64.RegRAX)
	assembler.AndRegImm32(amd64.RegRAX, builtinCallBlockFlagOpenArgs)
	assembler.CmpRegImm32(amd64.RegRAX, builtinCallBlockFlagOpenArgs)
	assembler.Jcc(amd64.CondEqual, openArgCopy)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegRSI)
	assembler.CmpRegReg(amd64.RegRCX, amd64.RegR10)
	assembler.Jcc(amd64.CondBelowEqual, copyCountReady)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR10)

	_ = assembler.Bind(copyCountReady)
	assembler.MoveRegReg(amd64.RegRBP, amd64.RegRCX)
	emitLoadBuiltinScratch64(assembler, amd64.RegRDX, builtinScratchPayload0Offset)
	assembler.AddRegImm32(amd64.RegRDX, 1)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR9)
	emitCopyCallArguments(assembler, amd64.RegRBX, amd64.RegR12, amd64.RegRDX, amd64.RegRCX, amd64.RegR8)
	assembler.Jmp(callArgsCopied)

	_ = assembler.Bind(openArgCopy)
	assembler.MoveRegMem32(amd64.RegRCX, amd64.RegR13, rtstate.CallFrameTopOffset)
	assembler.AndRegImm32(amd64.RegRCX, 0xFFFF)
	emitLoadBuiltinScratch64(assembler, amd64.RegRDX, builtinScratchPayload0Offset)
	assembler.AddRegImm32(amd64.RegRDX, 1)
	assembler.XorRegReg(amd64.RegRBP, amd64.RegRBP)
	emitCountOpenCallArguments(assembler, amd64.RegRDX, amd64.RegRCX, amd64.RegRBP, amd64.RegR10)
	emitLoadBuiltinScratch64(assembler, amd64.RegRDX, builtinScratchPayload0Offset)
	assembler.AddRegImm32(amd64.RegRDX, 1)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegRBP)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR9)
	emitCopyCallArguments(assembler, amd64.RegRBX, amd64.RegR12, amd64.RegRDX, amd64.RegRCX, amd64.RegR8)

	_ = assembler.Bind(callArgsCopied)

	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR13)
	assembler.AddRegImm32(amd64.RegRCX, rtstate.CallFrameHeaderSize)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFramePrevFrameOffset, amd64.RegR13)
	assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameCallerRetPCOffset, amd64.RegRAX)
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchPayload0Offset)
	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegRAX, amd64.RegRDX, amd64.RegRBX)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameClosureOffset, amd64.RegRDX)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRAX, amd64.RegRDX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegRAX)
	assembler.MoveRegMem64(amd64.RegRBX, amd64.RegRAX, rtclosure.ProtoOffset)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameProtoOffset, amd64.RegRBX)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegR8, amd64.RegRBX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegR8)
	assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR8, rtproto.ConstBasePtrOff)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameRegsBaseOffset, amd64.RegR9)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameConstBaseOffset, amd64.RegRAX)
	assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameVarargBaseOffset, amd64.RegRAX)
	assembler.MoveMemImm32(amd64.RegRCX, rtstate.CallFrameSavedBCOffOffset, 0)
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchPayload1Offset)
	assembler.AddRegImm32(amd64.RegRAX, -1)
	assembler.ShiftLeftRegImm8(amd64.RegRAX, 16)
	assembler.AddRegImm32(amd64.RegRAX, int32(rtstate.FrameFlagIsLuaFrame))
	assembler.MoveMemReg32(amd64.RegRCX, rtstate.CallFrameFlagsOffset, amd64.RegRAX)
	assembler.MoveMemImm32(amd64.RegRCX, rtstate.CallFrameVarargCountOffset, 0)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDI)
	assembler.ShiftLeftRegImm8(amd64.RegRAX, 16)
	assembler.AddRegReg(amd64.RegRAX, amd64.RegR10)
	assembler.MoveMemReg32(amd64.RegRCX, rtstate.CallFrameRegisterCountOff, amd64.RegRAX)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegR10)
	assembler.ShiftLeftRegImm8(amd64.RegRAX, 3)
	assembler.MoveRegReg(amd64.RegRDX, amd64.RegR9)
	assembler.AddRegReg(amd64.RegRDX, amd64.RegRAX)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameResultBaseOffset, amd64.RegRDX)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDI)
	assembler.ShiftLeftRegImm8(amd64.RegRAX, 16)
	assembler.AddRegReg(amd64.RegRAX, amd64.RegRBP)
	assembler.MoveMemReg32(amd64.RegRCX, rtstate.CallFrameTopOffset, amd64.RegRAX)
	emitLoadThreadStateHeader(assembler, amd64.RegRAX)
	emitStoreThreadCurrentFrame(assembler, amd64.RegRAX, amd64.RegRCX)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload3Offset, amd64.RegR13)
	assembler.MoveRegReg(amd64.RegR13, amd64.RegRCX)
	assembler.MoveRegReg(amd64.RegR12, amd64.RegR9)
	assembler.MoveRegMem64(amd64.RegR8, amd64.RegR8, rtproto.CompiledEntryOff)
	assembler.CallReg(amd64.RegR8)
	assembler.CmpRegImm32(amd64.RegRAX, compiledStatusOK)
	assembler.Jcc(amd64.CondEqual, callOK)
	assembler.CmpRegImm32(amd64.RegRAX, compiledStatusStub)
	assembler.Jcc(amd64.CondEqual, callStub)
	assembler.CmpRegImm32(amd64.RegRAX, compiledStatusDeopt)
	assembler.Jcc(amd64.CondEqual, callDeopt)
	assembler.Jmp(callError)

	_ = assembler.Bind(callOK)
	emitRestoreCallerSiteID(assembler)
	emitLoadBuiltinScratch64(assembler, amd64.RegRCX, builtinScratchPayload1Offset)
	assembler.CmpRegImm32(amd64.RegRCX, 0)
	assembler.Jcc(amd64.CondEqual, openResults)
	assembler.CmpRegImm32(amd64.RegRCX, 1)
	assembler.Jcc(amd64.CondEqual, noResults)
	assembler.AddRegImm32(amd64.RegRCX, -1)
	emitLoadBuiltinScratch64(assembler, amd64.RegR8, builtinScratchPayload2Offset)
	emitLoadBuiltinScratch64(assembler, amd64.RegR9, builtinScratchPayload0Offset)
	emitLoadBuiltinCallFlags32(assembler, amd64.RegRAX)
	assembler.AndRegImm32(amd64.RegRAX, builtinCallBlockFlagTForLoop)
	assembler.CmpRegImm32(amd64.RegRAX, builtinCallBlockFlagTForLoop)
	assembler.Jcc(amd64.CondNotEqual, fixedResultBaseReady)
	assembler.AddRegImm32(amd64.RegR9, 3)

	_ = assembler.Bind(fixedResultBaseReady)
	assembler.ShiftLeftRegImm8(amd64.RegR9, 3)
	assembler.AddRegReg(amd64.RegR8, amd64.RegR9)
	assembler.MoveRegMem64(amd64.RegR9, amd64.RegR13, rtstate.CallFrameResultBaseOffset)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegRDX)
	emitCopyResultsWithNilFill(assembler, amd64.RegR8, amd64.RegR9, amd64.RegRCX, amd64.RegRBX)
	emitLoadBuiltinScratch64(assembler, amd64.RegRBX, builtinScratchPayload3Offset)
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchPayload0Offset)
	emitLoadBuiltinCallFlags32(assembler, amd64.RegR10)
	assembler.AndRegImm32(amd64.RegR10, builtinCallBlockFlagTForLoop)
	assembler.CmpRegImm32(amd64.RegR10, builtinCallBlockFlagTForLoop)
	assembler.Jcc(amd64.CondNotEqual, fixedTopBaseReady)
	assembler.AddRegImm32(amd64.RegRAX, 3)

	_ = assembler.Bind(fixedTopBaseReady)
	emitLoadBuiltinScratch64(assembler, amd64.RegRCX, builtinScratchPayload1Offset)
	assembler.AddRegImm32(amd64.RegRCX, -1)
	assembler.AddRegReg(amd64.RegRAX, amd64.RegRCX)
	emitPreserveCallerTopForTForLoop(assembler, amd64.RegRBX, amd64.RegRAX, amd64.RegR10, amd64.RegRCX)
	assembler.MoveMemReg32(amd64.RegRBX, rtstate.CallFrameTopOffset, amd64.RegRAX)
	assembler.Jmp(callDone)

	_ = assembler.Bind(openResults)
	emitLoadBuiltinScratch64(assembler, amd64.RegR8, builtinScratchPayload2Offset)
	emitLoadBuiltinScratch64(assembler, amd64.RegR9, builtinScratchPayload0Offset)
	emitLoadBuiltinCallFlags32(assembler, amd64.RegRAX)
	assembler.AndRegImm32(amd64.RegRAX, builtinCallBlockFlagTForLoop)
	assembler.CmpRegImm32(amd64.RegRAX, builtinCallBlockFlagTForLoop)
	assembler.Jcc(amd64.CondNotEqual, openResultBaseReady)
	assembler.AddRegImm32(amd64.RegR9, 3)

	_ = assembler.Bind(openResultBaseReady)
	assembler.ShiftLeftRegImm8(amd64.RegR9, 3)
	assembler.AddRegReg(amd64.RegR8, amd64.RegR9)
	assembler.MoveRegMem64(amd64.RegR9, amd64.RegR13, rtstate.CallFrameResultBaseOffset)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegRDX)
	emitCopySlots(assembler, amd64.RegR8, amd64.RegR9, amd64.RegRCX)
	emitLoadBuiltinScratch64(assembler, amd64.RegRBX, builtinScratchPayload3Offset)
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchPayload0Offset)
	emitLoadBuiltinCallFlags32(assembler, amd64.RegR10)
	assembler.AndRegImm32(amd64.RegR10, builtinCallBlockFlagTForLoop)
	assembler.CmpRegImm32(amd64.RegR10, builtinCallBlockFlagTForLoop)
	assembler.Jcc(amd64.CondNotEqual, openTopBaseReady)
	assembler.AddRegImm32(amd64.RegRAX, 3)

	_ = assembler.Bind(openTopBaseReady)
	assembler.AddRegReg(amd64.RegRAX, amd64.RegRDX)
	emitPreserveCallerTopForTForLoop(assembler, amd64.RegRBX, amd64.RegRAX, amd64.RegR10, amd64.RegRCX)
	assembler.MoveMemReg32(amd64.RegRBX, rtstate.CallFrameTopOffset, amd64.RegRAX)
	assembler.Jmp(callDone)

	_ = assembler.Bind(noResults)
	emitLoadBuiltinScratch64(assembler, amd64.RegRBX, builtinScratchPayload3Offset)
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchPayload0Offset)
	emitLoadBuiltinCallFlags32(assembler, amd64.RegR10)
	assembler.AndRegImm32(amd64.RegR10, builtinCallBlockFlagTForLoop)
	assembler.CmpRegImm32(amd64.RegR10, builtinCallBlockFlagTForLoop)
	assembler.Jcc(amd64.CondNotEqual, noResultBaseReady)
	assembler.AddRegImm32(amd64.RegRAX, 3)

	_ = assembler.Bind(noResultBaseReady)
	emitPreserveCallerTopForTForLoop(assembler, amd64.RegRBX, amd64.RegRAX, amd64.RegR10, amd64.RegRCX)
	assembler.MoveMemReg32(amd64.RegRBX, rtstate.CallFrameTopOffset, amd64.RegRAX)

	_ = assembler.Bind(callDone)
	emitRestoreCallerFrameFromScratch(assembler)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(callStub)
	emitReturnNestedCallDispatch(assembler, execCtxFlagNestedCallPending, true, amd64.RegRDX)

	_ = assembler.Bind(callDeopt)
	emitReturnNestedCallDispatch(assembler, execCtxFlagNestedCallPending|execCtxFlagNestedCallDeopt, false, amd64.RegRDX)

	_ = assembler.Bind(callError)
	emitReturnNestedCallDispatch(assembler, execCtxFlagNestedCallPending|execCtxFlagNestedCallError, false, amd64.RegRDX)

	_ = assembler.Bind(runtimeDispatch)
	emitExitCurrentBuiltinToRuntime(assembler, true)
	return assembler.Buffer().Bytes()
}

func buildTailCallBuiltinBody() []byte {
	assembler := amd64.NewAssembler(4096)
	runtimeDispatch := assembler.NewLabel()
	hostDispatch := assembler.NewLabel()
	luaClosure := assembler.NewLabel()
	openArgCount := assembler.NewLabel()
	tailArgCountReady := assembler.NewLabel()
	regCountReady := assembler.NewLabel()
	openArgCopy := assembler.NewLabel()
	copyCountReady := assembler.NewLabel()
	tailArgsCopied := assembler.NewLabel()
	callOK := assembler.NewLabel()
	callStub := assembler.NewLabel()
	callDeopt := assembler.NewLabel()
	callError := assembler.NewLabel()

	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR13, rtstate.CallFrameFlagsOffset)
	assembler.OrRegImm32(amd64.RegRAX, uint32(rtstate.FrameFlagIsTailcall))
	assembler.MoveMemReg32(amd64.RegR13, rtstate.CallFrameFlagsOffset, amd64.RegRAX)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR11, execCtxSiteIDOffset)
	emitStoreBuiltinScratch64(assembler, builtinScratchTableRefOffset, amd64.RegRAX)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg0Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload0Offset, amd64.RegR8)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload2Offset, amd64.RegR12)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg1Offset)
	assembler.CmpRegImm32(amd64.RegR9, 0)
	assembler.Jcc(amd64.CondEqual, openArgCount)
	assembler.AddRegImm32(amd64.RegR9, -1)
	assembler.MoveRegReg(amd64.RegRSI, amd64.RegR9)
	assembler.Jmp(tailArgCountReady)

	_ = assembler.Bind(openArgCount)
	assembler.XorRegReg(amd64.RegRSI, amd64.RegRSI)

	_ = assembler.Bind(tailArgCountReady)

	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegR8, amd64.RegRDX, amd64.RegRAX)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	assembler.ShiftRightRegImm8(amd64.RegRAX, value.TagShift)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagLuaClosureRef))
	assembler.Jcc(amd64.CondEqual, luaClosure)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagHostFunctionRef))
	assembler.Jcc(amd64.CondEqual, hostDispatch)
	emitUpdateCallFeedbackIneligible(assembler, feedback.SlotTailCall)
	assembler.Jmp(runtimeDispatch)

	_ = assembler.Bind(hostDispatch)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRCX, amd64.RegRDX)
	emitUpdateCallFeedbackEligible(assembler, feedback.SlotTailCall, feedback.AccessCallHostFunction, amd64.RegRDX, amd64.RegRCX)
	emitRefreshHostObjectWrapper(assembler, amd64.RegRDX, amd64.RegRAX, amd64.RegR8, amd64.RegR10)
	assembler.Jmp(runtimeDispatch)

	_ = assembler.Bind(luaClosure)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRCX, amd64.RegRDX)
	emitUpdateCallFeedbackEligible(assembler, feedback.SlotTailCall, feedback.AccessCallLuaClosure, amd64.RegRDX, amd64.RegRCX)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRAX, amd64.RegRDX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegRAX)
	assembler.MoveRegMem64(amd64.RegRBX, amd64.RegRAX, rtclosure.ProtoOffset)
	assembler.MoveRegReg(amd64.RegR8, amd64.RegRBX)
	assembler.ShiftRightRegImm8(amd64.RegR8, value.TagShift)
	assembler.CmpRegImm32(amd64.RegR8, shiftedBoxedTag(value.TagProtoRef))
	assembler.Jcc(amd64.CondNotEqual, runtimeDispatch)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegR8, amd64.RegRBX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegR8)
	assembler.MoveRegMem64(amd64.RegR9, amd64.RegR8, rtproto.CompiledEntryOff)
	assembler.CmpRegImm32(amd64.RegR9, 0)
	assembler.Jcc(amd64.CondEqual, runtimeDispatch)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegR8, rtproto.MaxStackSizeOff)
	assembler.AndRegImm32(amd64.RegR10, 0xFF)
	assembler.CmpRegImm32(amd64.RegR10, 0)
	assembler.Jcc(amd64.CondNotEqual, regCountReady)
	assembler.MoveRegImm32(amd64.RegR10, 1)

	_ = assembler.Bind(regCountReady)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR8, rtproto.VarargFlagsOff)
	assembler.AndRegImm32(amd64.RegRAX, 0xFF)
	assembler.CmpRegImm32(amd64.RegRAX, 0)
	assembler.Jcc(amd64.CondNotEqual, runtimeDispatch)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR13, rtstate.CallFrameRegisterCountOff)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegRAX)
	assembler.AndRegImm32(amd64.RegRCX, 0xFFFF)
	assembler.CmpRegReg(amd64.RegR10, amd64.RegRCX)
	assembler.Jcc(amd64.CondAbove, runtimeDispatch)
	assembler.ShiftRightRegImm8(amd64.RegRAX, 16)
	assembler.AddRegReg(amd64.RegRCX, amd64.RegRAX)
	assembler.ShiftLeftRegImm8(amd64.RegRCX, 3)
	assembler.MoveRegReg(amd64.RegR9, amd64.RegR12)
	assembler.AddRegReg(amd64.RegR9, amd64.RegRCX)

	emitLoadThreadStateHeader(assembler, amd64.RegRAX)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR13)
	assembler.AddRegImm32(amd64.RegRCX, rtstate.CallFrameHeaderSize*2)
	assembler.MoveRegMem64(amd64.RegRBX, amd64.RegRAX, rtstate.ThreadFrameEndOffset)
	assembler.CmpRegReg(amd64.RegRCX, amd64.RegRBX)
	assembler.Jcc(amd64.CondAbove, runtimeDispatch)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR10)
	assembler.AddRegReg(amd64.RegRCX, amd64.RegR10)
	assembler.ShiftLeftRegImm8(amd64.RegRCX, 3)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR9)
	assembler.AddRegReg(amd64.RegRBX, amd64.RegRCX)
	assembler.MoveRegMem64(amd64.RegRCX, amd64.RegRAX, rtstate.ThreadStackEndOffset)
	assembler.CmpRegReg(amd64.RegRBX, amd64.RegRCX)
	assembler.Jcc(amd64.CondAbove, runtimeDispatch)

	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR10)
	assembler.AddRegReg(amd64.RegRCX, amd64.RegR10)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR9)
	emitZeroSlots(assembler, amd64.RegRBX, amd64.RegRCX)

	emitLoadBuiltinCallArg64(assembler, amd64.RegRAX, rtstate.StubCallBlockArg1Offset)
	assembler.CmpRegImm32(amd64.RegRAX, 0)
	assembler.Jcc(amd64.CondEqual, openArgCopy)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegRSI)
	assembler.CmpRegReg(amd64.RegRCX, amd64.RegR10)
	assembler.Jcc(amd64.CondBelowEqual, copyCountReady)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR10)

	_ = assembler.Bind(copyCountReady)
	assembler.MoveRegReg(amd64.RegRBP, amd64.RegRCX)
	emitLoadBuiltinScratch64(assembler, amd64.RegRDX, builtinScratchPayload0Offset)
	assembler.AddRegImm32(amd64.RegRDX, 1)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR9)
	emitCopyCallArguments(assembler, amd64.RegRBX, amd64.RegR12, amd64.RegRDX, amd64.RegRCX, amd64.RegR8)
	assembler.Jmp(tailArgsCopied)

	_ = assembler.Bind(openArgCopy)
	assembler.MoveRegMem32(amd64.RegRCX, amd64.RegR13, rtstate.CallFrameTopOffset)
	assembler.AndRegImm32(amd64.RegRCX, 0xFFFF)
	emitLoadBuiltinScratch64(assembler, amd64.RegRDX, builtinScratchPayload0Offset)
	assembler.AddRegImm32(amd64.RegRDX, 1)
	assembler.XorRegReg(amd64.RegRBP, amd64.RegRBP)
	emitCountOpenCallArguments(assembler, amd64.RegRDX, amd64.RegRCX, amd64.RegRBP, amd64.RegR10)
	emitLoadBuiltinScratch64(assembler, amd64.RegRDX, builtinScratchPayload0Offset)
	assembler.AddRegImm32(amd64.RegRDX, 1)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegRBP)
	assembler.MoveRegReg(amd64.RegRBX, amd64.RegR9)
	emitCopyCallArguments(assembler, amd64.RegRBX, amd64.RegR12, amd64.RegRDX, amd64.RegRCX, amd64.RegR8)

	_ = assembler.Bind(tailArgsCopied)

	assembler.MoveRegReg(amd64.RegRCX, amd64.RegR13)
	assembler.AddRegImm32(amd64.RegRCX, rtstate.CallFrameHeaderSize)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFramePrevFrameOffset, amd64.RegR13)
	assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameCallerRetPCOffset, amd64.RegRAX)
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchPayload0Offset)
	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegRAX, amd64.RegRDX, amd64.RegRBX)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameClosureOffset, amd64.RegRDX)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRAX, amd64.RegRDX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegRAX)
	assembler.MoveRegMem64(amd64.RegRBX, amd64.RegRAX, rtclosure.ProtoOffset)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameProtoOffset, amd64.RegRBX)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegR8, amd64.RegRBX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegR8)
	assembler.MoveRegMem64(amd64.RegRAX, amd64.RegR8, rtproto.ConstBasePtrOff)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameRegsBaseOffset, amd64.RegR9)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameConstBaseOffset, amd64.RegRAX)
	assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameVarargBaseOffset, amd64.RegRAX)
	assembler.MoveMemImm32(amd64.RegRCX, rtstate.CallFrameSavedBCOffOffset, 0)
	assembler.MoveMemImm32(amd64.RegRCX, rtstate.CallFrameFlagsOffset, uint32(rtstate.FrameFlagIsLuaFrame)|(uint32(0xFFFF)<<16))
	assembler.MoveMemImm32(amd64.RegRCX, rtstate.CallFrameVarargCountOffset, 0)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegR10)
	assembler.ShiftLeftRegImm8(amd64.RegRAX, 16)
	assembler.AddRegReg(amd64.RegRAX, amd64.RegR10)
	assembler.MoveMemReg32(amd64.RegRCX, rtstate.CallFrameRegisterCountOff, amd64.RegRAX)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegR10)
	assembler.ShiftLeftRegImm8(amd64.RegRAX, 3)
	assembler.MoveRegReg(amd64.RegRDX, amd64.RegR9)
	assembler.AddRegReg(amd64.RegRDX, amd64.RegRAX)
	assembler.MoveMemReg64(amd64.RegRCX, rtstate.CallFrameResultBaseOffset, amd64.RegRDX)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegR10)
	assembler.ShiftLeftRegImm8(amd64.RegRAX, 16)
	assembler.AddRegReg(amd64.RegRAX, amd64.RegRBP)
	assembler.MoveMemReg32(amd64.RegRCX, rtstate.CallFrameTopOffset, amd64.RegRAX)
	emitLoadThreadStateHeader(assembler, amd64.RegRAX)
	emitStoreThreadCurrentFrame(assembler, amd64.RegRAX, amd64.RegRCX)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload3Offset, amd64.RegR13)
	assembler.MoveRegReg(amd64.RegR13, amd64.RegRCX)
	assembler.MoveRegReg(amd64.RegR12, amd64.RegR9)
	assembler.MoveRegMem64(amd64.RegR8, amd64.RegR8, rtproto.CompiledEntryOff)
	assembler.CallReg(amd64.RegR8)
	assembler.CmpRegImm32(amd64.RegRAX, compiledStatusOK)
	assembler.Jcc(amd64.CondEqual, callOK)
	assembler.CmpRegImm32(amd64.RegRAX, compiledStatusStub)
	assembler.Jcc(amd64.CondEqual, callStub)
	assembler.CmpRegImm32(amd64.RegRAX, compiledStatusDeopt)
	assembler.Jcc(amd64.CondEqual, callDeopt)
	assembler.Jmp(callError)

	_ = assembler.Bind(callOK)
	emitRestoreCallerSiteID(assembler)
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchPayload3Offset)
	assembler.MoveRegMem64(amd64.RegR8, amd64.RegRAX, rtstate.CallFrameResultBaseOffset)
	assembler.MoveRegMem64(amd64.RegR9, amd64.RegR13, rtstate.CallFrameResultBaseOffset)
	assembler.MoveRegReg(amd64.RegRCX, amd64.RegRDX)
	emitCopySlots(assembler, amd64.RegR8, amd64.RegR9, amd64.RegRCX)
	emitRestoreCallerFrameFromScratch(assembler)
	emitExitBuiltinStatusWithRegAux(assembler, compiledStatusOK, amd64.RegRDX, true)

	_ = assembler.Bind(callStub)
	emitReturnNestedCallDispatch(assembler, execCtxFlagNestedCallPending, true, amd64.RegRDX)

	_ = assembler.Bind(callDeopt)
	emitReturnNestedCallDispatch(assembler, execCtxFlagNestedCallPending|execCtxFlagNestedCallDeopt, false, amd64.RegRDX)

	_ = assembler.Bind(callError)
	emitReturnNestedCallDispatch(assembler, execCtxFlagNestedCallPending|execCtxFlagNestedCallError, false, amd64.RegRDX)

	_ = assembler.Bind(runtimeDispatch)
	emitExitCurrentBuiltinToRuntime(assembler, true)
	return assembler.Buffer().Bytes()
}

func buildGetGlobalBuiltinBody() []byte {
	assembler := amd64.NewAssembler(1536)

	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg0Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload0Offset, amd64.RegR8)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR10, rtstate.StubCallBlockArg2Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload3Offset, amd64.RegR10)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg1Offset)
	emitLoadConstantFromFrameIndexReg(assembler, amd64.RegR9, amd64.RegRCX, amd64.RegRAX)
	emitLoadClosureObjectFromFrame(assembler, amd64.RegRAX)
	assembler.MoveRegMem64(amd64.RegRDX, amd64.RegRAX, rtclosure.EnvOffset)
	emitGenericStringKeyGetFlow(assembler, feedback.SlotGetGlobal)
	return assembler.Buffer().Bytes()
}

func buildGetTableBuiltinBody() []byte {
	assembler := amd64.NewAssembler(1664)

	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg0Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload0Offset, amd64.RegR8)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR10, rtstate.StubCallBlockArg3Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload3Offset, amd64.RegR10)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg1Offset)
	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegR9, amd64.RegRDX, amd64.RegR8)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg2Offset)
	emitLoadRKValueFromOperandReg(assembler, amd64.RegR9, amd64.RegRCX, amd64.RegRAX, amd64.RegR10)
	emitGenericStringKeyGetFlow(assembler, feedback.SlotGetTable)
	return assembler.Buffer().Bytes()
}

func buildSetGlobalBuiltinBody() []byte {
	assembler := amd64.NewAssembler(1664)

	emitLoadBuiltinCallArg64(assembler, amd64.RegR8, rtstate.StubCallBlockArg0Offset)
	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegR8, amd64.RegRAX, amd64.RegR9)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload0Offset, amd64.RegRAX)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR10, rtstate.StubCallBlockArg2Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload3Offset, amd64.RegR10)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg1Offset)
	emitLoadConstantFromFrameIndexReg(assembler, amd64.RegR9, amd64.RegRCX, amd64.RegRAX)
	emitLoadClosureObjectFromFrame(assembler, amd64.RegRAX)
	assembler.MoveRegMem64(amd64.RegRDX, amd64.RegRAX, rtclosure.EnvOffset)
	emitGenericStringKeySetFlow(assembler, feedback.SlotSetGlobal)
	return assembler.Buffer().Bytes()
}

func buildSetTableBuiltinBody() []byte {
	assembler := amd64.NewAssembler(1792)

	emitLoadBuiltinCallArg64(assembler, amd64.RegR10, rtstate.StubCallBlockArg3Offset)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload3Offset, amd64.RegR10)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg0Offset)
	emitLoadRegisterValueFromIndexReg(assembler, amd64.RegR9, amd64.RegRDX, amd64.RegR8)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg1Offset)
	emitLoadRKValueFromOperandReg(assembler, amd64.RegR9, amd64.RegRCX, amd64.RegRAX, amd64.RegR10)
	emitLoadBuiltinCallArg64(assembler, amd64.RegR9, rtstate.StubCallBlockArg2Offset)
	emitLoadRKValueFromOperandReg(assembler, amd64.RegR9, amd64.RegRAX, amd64.RegR8, amd64.RegR10)
	emitStoreBuiltinScratch64(assembler, builtinScratchPayload0Offset, amd64.RegRAX)
	emitGenericStringKeySetFlow(assembler, feedback.SlotSetTable)
	return assembler.Buffer().Bytes()
}

func emitGenericStringKeyGetFlow(assembler *amd64.Assembler, slotKind feedback.SlotKind) {
	tablePath := assembler.NewLabel()
	hostPath := assembler.NewLabel()
	lookupLoop := assembler.NewLabel()
	nextEntry := assembler.NewLabel()
	foundEntry := assembler.NewLabel()
	notFound := assembler.NewLabel()
	keyDeopt := assembler.NewLabel()
	blockedMissDeopt := assembler.NewLabel()
	eligibleContinue := assembler.NewLabel()
	ineligibleContinue := assembler.NewLabel()
	unblockedMissContinue := assembler.NewLabel()
	deoptPath := assembler.NewLabel()
	dispatchPath := assembler.NewLabel()

	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	assembler.ShiftRightRegImm8(amd64.RegRAX, value.TagShift)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagTableRef))
	assembler.Jcc(amd64.CondEqual, tablePath)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagHostObjectRef))
	assembler.Jcc(amd64.CondEqual, hostPath)
	emitUpdateTableFeedbackIneligible(assembler, slotKind)
	assembler.Jmp(deoptPath)

	_ = assembler.Bind(hostPath)
	emitUpdateTableFeedbackIneligible(assembler, slotKind)
	emitRefreshHostObjectWrapper(assembler, amd64.RegRDX, amd64.RegRAX, amd64.RegR9, amd64.RegR10)
	assembler.Jmp(dispatchPath)

	_ = assembler.Bind(tablePath)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRCX)
	assembler.ShiftRightRegImm8(amd64.RegRAX, value.TagShift)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagStringRef))
	assembler.Jcc(amd64.CondNotEqual, keyDeopt)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRAX, amd64.RegRAX)
	emitStoreBuiltinScratch64(assembler, builtinScratchTableRefOffset, amd64.RegRAX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegRAX)
	assembler.MoveRegImm64(amd64.RegRDX, uint64(value.NilValue().Bits()))
	assembler.MoveRegMem64(amd64.RegR9, amd64.RegRAX, rttable.EntriesDataOffset)
	assembler.CmpRegImm32(amd64.RegR9, 0)
	assembler.Jcc(amd64.CondEqual, notFound)
	assembler.AddRegReg(amd64.RegR9, amd64.HeapBaseRegister)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegRAX, rttable.HashCapacityOffset)
	emitStoreBuiltinScratch32(assembler, builtinScratchPayload1Offset, amd64.RegR10)
	assembler.XorRegReg(amd64.RegR8, amd64.RegR8)

	_ = assembler.Bind(lookupLoop)
	emitLoadBuiltinScratch32(assembler, amd64.RegR10, builtinScratchPayload1Offset)
	assembler.CmpRegReg(amd64.RegR8, amd64.RegR10)
	assembler.Jcc(amd64.CondAboveEqual, notFound)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, rttable.EntryKeyOffset)
	assembler.CmpRegReg(amd64.RegR10, amd64.RegRCX)
	assembler.Jcc(amd64.CondNotEqual, nextEntry)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, rttable.EntryValueOffset)
	assembler.CmpRegReg(amd64.RegR10, amd64.RegRDX)
	assembler.Jcc(amd64.CondEqual, nextEntry)
	assembler.MoveRegReg(amd64.RegRDX, amd64.RegR10)
	assembler.MoveRegImm32(amd64.RegRAX, 1)
	assembler.Jmp(foundEntry)

	_ = assembler.Bind(nextEntry)
	assembler.AddRegImm32(amd64.RegR9, rttable.EntrySize)
	assembler.AddRegImm32(amd64.RegR8, 1)
	assembler.Jmp(lookupLoop)

	_ = assembler.Bind(notFound)
	assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	assembler.XorRegReg(amd64.RegR8, amd64.RegR8)

	_ = assembler.Bind(foundEntry)
	emitLoadBuiltinScratch64(assembler, amd64.RegR9, builtinScratchTableRefOffset)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegR9)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegR9, rttable.FlagsOffset)
	assembler.AndRegImm32(amd64.RegR10, uint32(rttable.FlagIndexFastPathBlocked|rttable.FlagWeakKeys|rttable.FlagWeakValues|rttable.FlagRehashing))
	assembler.CmpRegImm32(amd64.RegRAX, 0)
	assembler.Jcc(amd64.CondEqual, unblockedMissContinue)
	assembler.CmpRegImm32(amd64.RegR10, 0)
	assembler.Jcc(amd64.CondEqual, eligibleContinue)
	assembler.Jmp(ineligibleContinue)

	_ = assembler.Bind(unblockedMissContinue)
	assembler.CmpRegImm32(amd64.RegR10, 0)
	assembler.Jcc(amd64.CondNotEqual, blockedMissDeopt)
	emitUpdateTableFeedbackIneligible(assembler, slotKind)
	emitStoreResultRegisterFromScratchIndex(assembler, amd64.RegRDX)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(eligibleContinue)
	emitUpdateTableFeedbackEligibleHash(assembler, slotKind, amd64.RegRCX, amd64.RegR8)
	emitStoreResultRegisterFromScratchIndex(assembler, amd64.RegRDX)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(ineligibleContinue)
	emitUpdateTableFeedbackIneligible(assembler, slotKind)
	emitStoreResultRegisterFromScratchIndex(assembler, amd64.RegRDX)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(blockedMissDeopt)
	emitUpdateTableFeedbackIneligible(assembler, slotKind)
	assembler.Jmp(deoptPath)

	_ = assembler.Bind(keyDeopt)
	emitUpdateTableFeedbackIneligible(assembler, slotKind)
	assembler.Jmp(deoptPath)

	_ = assembler.Bind(dispatchPath)
	emitExitCurrentBuiltinToRuntime(assembler, true)

	_ = assembler.Bind(deoptPath)
	emitExitBuiltinStatus(assembler, compiledStatusDeopt, true)
}

func emitGenericStringKeySetFlow(assembler *amd64.Assembler, slotKind feedback.SlotKind) {
	tablePath := assembler.NewLabel()
	hostPath := assembler.NewLabel()
	lookupLoop := assembler.NewLabel()
	nextEntry := assembler.NewLabel()
	foundEntry := assembler.NewLabel()
	unsupportedDeopt := assembler.NewLabel()
	eligibleContinue := assembler.NewLabel()
	ineligibleContinue := assembler.NewLabel()
	deoptPath := assembler.NewLabel()
	dispatchPath := assembler.NewLabel()

	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	assembler.ShiftRightRegImm8(amd64.RegRAX, value.TagShift)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagTableRef))
	assembler.Jcc(amd64.CondEqual, tablePath)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagHostObjectRef))
	assembler.Jcc(amd64.CondEqual, hostPath)
	emitUpdateTableFeedbackIneligible(assembler, slotKind)
	assembler.Jmp(deoptPath)

	_ = assembler.Bind(hostPath)
	emitUpdateTableFeedbackIneligible(assembler, slotKind)
	emitRefreshHostObjectWrapper(assembler, amd64.RegRDX, amd64.RegRAX, amd64.RegR9, amd64.RegR10)
	assembler.Jmp(dispatchPath)

	_ = assembler.Bind(tablePath)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRCX)
	assembler.ShiftRightRegImm8(amd64.RegRAX, value.TagShift)
	assembler.CmpRegImm32(amd64.RegRAX, shiftedBoxedTag(value.TagStringRef))
	assembler.Jcc(amd64.CondNotEqual, unsupportedDeopt)
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchPayload0Offset)
	assembler.MoveRegImm64(amd64.RegR10, uint64(value.NilValue().Bits()))
	assembler.CmpRegReg(amd64.RegRAX, amd64.RegR10)
	assembler.Jcc(amd64.CondEqual, unsupportedDeopt)
	assembler.MoveRegReg(amd64.RegRAX, amd64.RegRDX)
	emitExtractHeapRefPayloadFromTValue(assembler, amd64.RegRAX, amd64.RegRAX)
	emitStoreBuiltinScratch64(assembler, builtinScratchTableRefOffset, amd64.RegRAX)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegRAX)
	assembler.MoveRegMem64(amd64.RegR9, amd64.RegRAX, rttable.EntriesDataOffset)
	assembler.CmpRegImm32(amd64.RegR9, 0)
	assembler.Jcc(amd64.CondEqual, unsupportedDeopt)
	assembler.AddRegReg(amd64.RegR9, amd64.HeapBaseRegister)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegRAX, rttable.HashCapacityOffset)
	emitStoreBuiltinScratch32(assembler, builtinScratchPayload1Offset, amd64.RegR10)
	assembler.XorRegReg(amd64.RegR8, amd64.RegR8)
	assembler.MoveRegImm64(amd64.RegRDX, uint64(value.NilValue().Bits()))

	_ = assembler.Bind(lookupLoop)
	emitLoadBuiltinScratch32(assembler, amd64.RegR10, builtinScratchPayload1Offset)
	assembler.CmpRegReg(amd64.RegR8, amd64.RegR10)
	assembler.Jcc(amd64.CondAboveEqual, unsupportedDeopt)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, rttable.EntryKeyOffset)
	assembler.CmpRegReg(amd64.RegR10, amd64.RegRCX)
	assembler.Jcc(amd64.CondNotEqual, nextEntry)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, rttable.EntryValueOffset)
	assembler.CmpRegReg(amd64.RegR10, amd64.RegRDX)
	assembler.Jcc(amd64.CondEqual, unsupportedDeopt)
	emitLoadBuiltinScratch64(assembler, amd64.RegR10, builtinScratchPayload0Offset)
	assembler.MoveMemReg64(amd64.RegR9, rttable.EntryValueOffset, amd64.RegR10)
	assembler.Jmp(foundEntry)

	_ = assembler.Bind(nextEntry)
	assembler.AddRegImm32(amd64.RegR9, rttable.EntrySize)
	assembler.AddRegImm32(amd64.RegR8, 1)
	assembler.Jmp(lookupLoop)

	_ = assembler.Bind(foundEntry)
	emitLoadBuiltinScratch64(assembler, amd64.RegR9, builtinScratchTableRefOffset)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegR9)
	assembler.MoveRegMem32(amd64.RegR10, amd64.RegR9, rttable.FlagsOffset)
	assembler.AndRegImm32(amd64.RegR10, uint32(rttable.FlagNewIndexFastPathBlocked|rttable.FlagWeakKeys|rttable.FlagWeakValues|rttable.FlagRehashing|rttable.FlagFrozen|rttable.FlagReadOnly))
	assembler.CmpRegImm32(amd64.RegR10, 0)
	assembler.Jcc(amd64.CondEqual, eligibleContinue)
	assembler.Jmp(ineligibleContinue)

	_ = assembler.Bind(eligibleContinue)
	emitUpdateTableFeedbackEligibleHash(assembler, slotKind, amd64.RegRCX, amd64.RegR8)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(ineligibleContinue)
	emitUpdateTableFeedbackIneligible(assembler, slotKind)
	emitReturnBuiltinContinuation(assembler, true)

	_ = assembler.Bind(unsupportedDeopt)
	emitUpdateTableFeedbackIneligible(assembler, slotKind)
	assembler.Jmp(deoptPath)

	_ = assembler.Bind(dispatchPath)
	emitExitCurrentBuiltinToRuntime(assembler, true)

	_ = assembler.Bind(deoptPath)
	emitExitBuiltinStatus(assembler, compiledStatusDeopt, true)
}

func emitUpdateTableFeedbackEligibleHash(assembler *amd64.Assembler, slotKind feedback.SlotKind, keyReg amd64.Register, slotIndexReg amd64.Register) {
	noVector := assembler.NewLabel()
	compareMonomorphic := assembler.NewLabel()
	writeCandidate := assembler.NewLabel()
	writeMegamorphic := assembler.NewLabel()
	done := assembler.NewLabel()

	emitLoadFeedbackCellBaseFromScratch(assembler, amd64.RegR9, amd64.RegRAX, noVector)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR9, feedback.CellStateOffset)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMegamorphic, feedback.AccessInvalid, slotKind))
	assembler.Jcc(amd64.CondEqual, done)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateGeneric, feedback.AccessInvalid, slotKind))
	assembler.Jcc(amd64.CondEqual, writeCandidate)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessHash, slotKind))
	assembler.Jcc(amd64.CondEqual, compareMonomorphic)
	assembler.Jmp(writeMegamorphic)

	_ = assembler.Bind(compareMonomorphic)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, feedback.CellHeapRefOffset)
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchTableRefOffset)
	assembler.CmpRegReg(amd64.RegR10, amd64.RegRAX)
	assembler.Jcc(amd64.CondNotEqual, writeMegamorphic)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, feedback.CellValueBitsOffset)
	assembler.CmpRegReg(amd64.RegR10, keyReg)
	assembler.Jcc(amd64.CondNotEqual, writeMegamorphic)

	_ = assembler.Bind(writeCandidate)
	assembler.MoveMemImm32(amd64.RegR9, feedback.CellStateOffset, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessHash, slotKind))
	emitLoadBuiltinScratch64(assembler, amd64.RegR10, builtinScratchTableRefOffset)
	assembler.MoveMemReg64(amd64.RegR9, feedback.CellHeapRefOffset, amd64.RegR10)
	emitDecodeHeapRefFromRaw(assembler, amd64.RegR10)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR10, rttable.TableVersionOffset)
	assembler.MoveMemReg32(amd64.RegR9, feedback.CellPayload32AOffset, amd64.RegRAX)
	assembler.MoveMemReg32(amd64.RegR9, feedback.CellPayload32BOffset, slotIndexReg)
	assembler.MoveMemImm32(amd64.RegR9, feedback.CellPayload32COffset, 0)
	assembler.MoveMemReg64(amd64.RegR9, feedback.CellValueBitsOffset, keyReg)
	assembler.Jmp(done)

	_ = assembler.Bind(writeMegamorphic)
	emitWriteMegamorphicFeedbackCell(assembler, amd64.RegR9, amd64.RegR10, slotKind)

	_ = assembler.Bind(noVector)
	_ = assembler.Bind(done)
}

func emitUpdateTableFeedbackIneligible(assembler *amd64.Assembler, slotKind feedback.SlotKind) {
	noVector := assembler.NewLabel()
	writeMegamorphic := assembler.NewLabel()
	done := assembler.NewLabel()

	emitLoadFeedbackCellBaseFromScratch(assembler, amd64.RegR9, amd64.RegRAX, noVector)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR9, feedback.CellStateOffset)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessHash, slotKind))
	assembler.Jcc(amd64.CondEqual, writeMegamorphic)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessArray, slotKind))
	assembler.Jcc(amd64.CondEqual, writeMegamorphic)
	assembler.Jmp(done)

	_ = assembler.Bind(writeMegamorphic)
	emitWriteMegamorphicFeedbackCell(assembler, amd64.RegR9, amd64.RegR10, slotKind)

	_ = assembler.Bind(noVector)
	_ = assembler.Bind(done)
}

func emitUpdateCallFeedbackEligible(assembler *amd64.Assembler, slotKind feedback.SlotKind, accessKind feedback.AccessKind, targetValueReg amd64.Register, targetRefReg amd64.Register) {
	noVector := assembler.NewLabel()
	compareMonomorphic := assembler.NewLabel()
	writeCandidate := assembler.NewLabel()
	writeMegamorphic := assembler.NewLabel()
	done := assembler.NewLabel()

	emitLoadFeedbackCellBaseFromCallArg(assembler, amd64.RegR9, amd64.RegRAX, rtstate.StubCallBlockArg3Offset, noVector)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR9, feedback.CellStateOffset)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMegamorphic, feedback.AccessInvalid, slotKind))
	assembler.Jcc(amd64.CondEqual, done)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateGeneric, feedback.AccessInvalid, slotKind))
	assembler.Jcc(amd64.CondEqual, writeCandidate)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMonomorphic, accessKind, slotKind))
	assembler.Jcc(amd64.CondEqual, compareMonomorphic)
	assembler.Jmp(writeMegamorphic)

	_ = assembler.Bind(compareMonomorphic)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, feedback.CellHeapRefOffset)
	assembler.CmpRegReg(amd64.RegR10, targetRefReg)
	assembler.Jcc(amd64.CondNotEqual, writeMegamorphic)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, feedback.CellValueBitsOffset)
	assembler.CmpRegReg(amd64.RegR10, targetValueReg)
	assembler.Jcc(amd64.CondNotEqual, writeCandidate)
	assembler.Jmp(done)

	_ = assembler.Bind(writeCandidate)
	assembler.MoveMemImm32(amd64.RegR9, feedback.CellStateOffset, feedback.PackCellPrefix(feedback.StateMonomorphic, accessKind, slotKind))
	assembler.MoveMemImm32(amd64.RegR9, feedback.CellPayload32AOffset, 0)
	assembler.MoveMemImm32(amd64.RegR9, feedback.CellPayload32BOffset, 0)
	assembler.MoveMemImm32(amd64.RegR9, feedback.CellPayload32COffset, 0)
	assembler.MoveMemReg64(amd64.RegR9, feedback.CellHeapRefOffset, targetRefReg)
	assembler.MoveMemReg64(amd64.RegR9, feedback.CellValueBitsOffset, targetValueReg)
	assembler.Jmp(done)

	_ = assembler.Bind(writeMegamorphic)
	emitWriteMegamorphicFeedbackCell(assembler, amd64.RegR9, amd64.RegR10, slotKind)

	_ = assembler.Bind(noVector)
	_ = assembler.Bind(done)
}

func emitUpdateCallFeedbackIneligible(assembler *amd64.Assembler, slotKind feedback.SlotKind) {
	noVector := assembler.NewLabel()
	writeMegamorphic := assembler.NewLabel()
	done := assembler.NewLabel()

	emitLoadFeedbackCellBaseFromCallArg(assembler, amd64.RegR9, amd64.RegRAX, rtstate.StubCallBlockArg3Offset, noVector)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR9, feedback.CellStateOffset)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessCallLuaClosure, slotKind))
	assembler.Jcc(amd64.CondEqual, writeMegamorphic)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessCallHostFunction, slotKind))
	assembler.Jcc(amd64.CondEqual, writeMegamorphic)
	assembler.Jmp(done)

	_ = assembler.Bind(writeMegamorphic)
	emitWriteMegamorphicFeedbackCell(assembler, amd64.RegR9, amd64.RegR10, slotKind)

	_ = assembler.Bind(noVector)
	_ = assembler.Bind(done)
}

func emitUpdateUpvalueFeedbackEligible(assembler *amd64.Assembler, slotKind feedback.SlotKind, accessKind feedback.AccessKind, upvalueRefReg amd64.Register, observedValueReg amd64.Register) {
	noVector := assembler.NewLabel()
	compareMonomorphic := assembler.NewLabel()
	writeCandidate := assembler.NewLabel()
	writeMegamorphic := assembler.NewLabel()
	done := assembler.NewLabel()

	emitLoadFeedbackCellBaseFromScratch(assembler, amd64.RegR9, amd64.RegRAX, noVector)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR9, feedback.CellStateOffset)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMegamorphic, feedback.AccessInvalid, slotKind))
	assembler.Jcc(amd64.CondEqual, done)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateGeneric, feedback.AccessInvalid, slotKind))
	assembler.Jcc(amd64.CondEqual, writeCandidate)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessUpvalueOpen, slotKind))
	assembler.Jcc(amd64.CondEqual, compareMonomorphic)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessUpvalueClosed, slotKind))
	assembler.Jcc(amd64.CondEqual, compareMonomorphic)
	assembler.Jmp(writeMegamorphic)

	_ = assembler.Bind(compareMonomorphic)
	assembler.MoveRegMem64(amd64.RegR10, amd64.RegR9, feedback.CellHeapRefOffset)
	assembler.CmpRegReg(amd64.RegR10, upvalueRefReg)
	assembler.Jcc(amd64.CondNotEqual, writeMegamorphic)

	_ = assembler.Bind(writeCandidate)
	assembler.MoveMemImm32(amd64.RegR9, feedback.CellStateOffset, feedback.PackCellPrefix(feedback.StateMonomorphic, accessKind, slotKind))
	assembler.MoveMemImm32(amd64.RegR9, feedback.CellPayload32AOffset, 0)
	assembler.MoveMemImm32(amd64.RegR9, feedback.CellPayload32BOffset, 0)
	assembler.MoveMemImm32(amd64.RegR9, feedback.CellPayload32COffset, 0)
	assembler.MoveMemReg64(amd64.RegR9, feedback.CellHeapRefOffset, upvalueRefReg)
	assembler.MoveMemReg64(amd64.RegR9, feedback.CellValueBitsOffset, observedValueReg)
	assembler.Jmp(done)

	_ = assembler.Bind(writeMegamorphic)
	emitWriteMegamorphicFeedbackCell(assembler, amd64.RegR9, amd64.RegR10, slotKind)

	_ = assembler.Bind(noVector)
	_ = assembler.Bind(done)
}

func emitUpdateUpvalueFeedbackIneligible(assembler *amd64.Assembler, slotKind feedback.SlotKind) {
	noVector := assembler.NewLabel()
	writeMegamorphic := assembler.NewLabel()
	done := assembler.NewLabel()

	emitLoadFeedbackCellBaseFromScratch(assembler, amd64.RegR9, amd64.RegRAX, noVector)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR9, feedback.CellStateOffset)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessUpvalueOpen, slotKind))
	assembler.Jcc(amd64.CondEqual, writeMegamorphic)
	assembler.CmpRegImm32(amd64.RegRAX, feedback.PackCellPrefix(feedback.StateMonomorphic, feedback.AccessUpvalueClosed, slotKind))
	assembler.Jcc(amd64.CondEqual, writeMegamorphic)
	assembler.Jmp(done)

	_ = assembler.Bind(writeMegamorphic)
	emitWriteMegamorphicFeedbackCell(assembler, amd64.RegR9, amd64.RegR10, slotKind)

	_ = assembler.Bind(noVector)
	_ = assembler.Bind(done)
}

func emitWriteMegamorphicFeedbackCell(assembler *amd64.Assembler, cellBaseReg amd64.Register, scratchReg amd64.Register, slotKind feedback.SlotKind) {
	assembler.MoveMemImm32(cellBaseReg, feedback.CellStateOffset, feedback.PackCellPrefix(feedback.StateMegamorphic, feedback.AccessInvalid, slotKind))
	assembler.MoveMemImm32(cellBaseReg, feedback.CellPayload32AOffset, 0)
	assembler.MoveMemImm32(cellBaseReg, feedback.CellPayload32BOffset, 0)
	assembler.MoveMemImm32(cellBaseReg, feedback.CellPayload32COffset, 0)
	assembler.XorRegReg(scratchReg, scratchReg)
	assembler.MoveMemReg64(cellBaseReg, feedback.CellHeapRefOffset, scratchReg)
	assembler.MoveMemReg64(cellBaseReg, feedback.CellValueBitsOffset, scratchReg)
}

func emitLoadFeedbackCellBaseFromScratch(assembler *amd64.Assembler, dstReg amd64.Register, slotReg amd64.Register, noVector *amd64.Label) {
	emitLoadClosureObjectFromFrame(assembler, dstReg)
	assembler.MoveRegMem64(dstReg, dstReg, rtclosure.FeedbackVectorOff)
	assembler.CmpRegImm32(dstReg, 0)
	assembler.Jcc(amd64.CondEqual, noVector)
	assembler.AddRegReg(dstReg, amd64.HeapBaseRegister)
	assembler.AddRegImm32(dstReg, int32(feedback.HeaderSize))
	emitLoadBuiltinScratch64(assembler, slotReg, builtinScratchPayload3Offset)
	assembler.ShiftLeftRegImm8(slotReg, 5)
	assembler.AddRegReg(dstReg, slotReg)
}

func emitLoadFeedbackCellBaseFromCallArg(assembler *amd64.Assembler, dstReg amd64.Register, slotReg amd64.Register, slotArgOffset int32, noVector *amd64.Label) {
	emitLoadClosureObjectFromFrame(assembler, dstReg)
	assembler.MoveRegMem64(dstReg, dstReg, rtclosure.FeedbackVectorOff)
	assembler.CmpRegImm32(dstReg, 0)
	assembler.Jcc(amd64.CondEqual, noVector)
	assembler.AddRegReg(dstReg, amd64.HeapBaseRegister)
	assembler.AddRegImm32(dstReg, int32(feedback.HeaderSize))
	emitLoadBuiltinCallArg64(assembler, slotReg, slotArgOffset)
	assembler.ShiftLeftRegImm8(slotReg, 5)
	assembler.AddRegReg(dstReg, slotReg)
}

func emitRefreshHostObjectWrapper(assembler *amd64.Assembler, wrapperValueReg amd64.Register, wrapperReg amd64.Register, metaReg amd64.Register, flagsReg amd64.Register) {
	done := assembler.NewLabel()
	skipCallable := assembler.NewLabel()
	skipIndexable := assembler.NewLabel()
	skipWritable := assembler.NewLabel()

	emitExtractHeapRefPayloadFromTValue(assembler, wrapperReg, wrapperValueReg)
	emitDecodeHeapRefFromRaw(assembler, wrapperReg)
	assembler.MoveRegMem64(metaReg, wrapperReg, rthost.WrapperNativeMetaOffset)
	assembler.CmpRegImm32(metaReg, 0)
	assembler.Jcc(amd64.CondEqual, done)
	assembler.AddRegReg(metaReg, amd64.HeapBaseRegister)
	assembler.MoveRegMem32(flagsReg, metaReg, rthost.NativeDescriptorVersionOffset)
	assembler.MoveMemReg32(wrapperReg, rthost.WrapperDescriptorVersionOffset, flagsReg)
	assembler.MoveRegMem32(flagsReg, metaReg, rthost.NativeDescriptorFlagsOffset)
	assembler.AndRegImm32(flagsReg, 0xFFFF)
	assembler.XorRegReg(metaReg, metaReg)
	assembler.MoveRegReg(wrapperValueReg, flagsReg)
	assembler.AndRegImm32(wrapperValueReg, uint32(rthost.DescriptorFlagCallable))
	assembler.CmpRegImm32(wrapperValueReg, 0)
	assembler.Jcc(amd64.CondEqual, skipCallable)
	assembler.OrRegImm32(metaReg, uint32(rthost.WrapperFlagCallable))

	_ = assembler.Bind(skipCallable)
	assembler.MoveRegReg(wrapperValueReg, flagsReg)
	assembler.AndRegImm32(wrapperValueReg, uint32(rthost.DescriptorFlagIndexable))
	assembler.CmpRegImm32(wrapperValueReg, 0)
	assembler.Jcc(amd64.CondEqual, skipIndexable)
	assembler.OrRegImm32(metaReg, uint32(rthost.WrapperFlagIndexable))

	_ = assembler.Bind(skipIndexable)
	assembler.MoveRegReg(wrapperValueReg, flagsReg)
	assembler.AndRegImm32(wrapperValueReg, uint32(rthost.DescriptorFlagWritable))
	assembler.CmpRegImm32(wrapperValueReg, 0)
	assembler.Jcc(amd64.CondEqual, skipWritable)
	assembler.OrRegImm32(metaReg, uint32(rthost.WrapperFlagWritable))

	_ = assembler.Bind(skipWritable)
	assembler.MoveMemReg32(wrapperReg, rthost.WrapperFlagsOffset, metaReg)

	_ = assembler.Bind(done)
}

func emitLoadConstantFromFrameIndexReg(assembler *amd64.Assembler, indexReg amd64.Register, dstReg amd64.Register, scratchReg amd64.Register) {
	assembler.MoveRegMem64(dstReg, amd64.CallFrameRegister, rtstate.CallFrameConstBaseOffset)
	assembler.MoveRegReg(scratchReg, indexReg)
	assembler.ShiftLeftRegImm8(scratchReg, 3)
	assembler.AddRegReg(dstReg, scratchReg)
	assembler.MoveRegMem64(dstReg, dstReg, 0)
}

func emitLoadRegisterValueFromIndexReg(assembler *amd64.Assembler, indexReg amd64.Register, dstReg amd64.Register, scratchReg amd64.Register) {
	assembler.MoveRegReg(dstReg, amd64.RegsBaseRegister)
	assembler.MoveRegReg(scratchReg, indexReg)
	assembler.ShiftLeftRegImm8(scratchReg, 3)
	assembler.AddRegReg(dstReg, scratchReg)
	assembler.MoveRegMem64(dstReg, dstReg, 0)
}

func emitLoadRKValueFromOperandReg(assembler *amd64.Assembler, operandReg amd64.Register, dstReg amd64.Register, maskReg amd64.Register, indexReg amd64.Register) {
	constantPath := assembler.NewLabel()
	done := assembler.NewLabel()

	assembler.MoveRegReg(maskReg, operandReg)
	assembler.AndRegImm32(maskReg, uint32(bytecode.BitRK))
	assembler.CmpRegImm32(maskReg, uint32(bytecode.BitRK))
	assembler.Jcc(amd64.CondEqual, constantPath)
	emitLoadRegisterValueFromIndexReg(assembler, operandReg, dstReg, indexReg)
	assembler.Jmp(done)

	_ = assembler.Bind(constantPath)
	assembler.MoveRegReg(indexReg, operandReg)
	assembler.AndRegImm32(indexReg, ^uint32(bytecode.BitRK))
	emitLoadConstantFromFrameIndexReg(assembler, indexReg, dstReg, maskReg)

	_ = assembler.Bind(done)
}

func emitExtractHeapRefPayloadFromTValue(assembler *amd64.Assembler, dst amd64.Register, src amd64.Register) {
	if dst != src {
		assembler.MoveRegReg(dst, src)
	}
	assembler.ShiftLeftRegImm8(dst, 20)
	assembler.ShiftRightRegImm8(dst, 20)
}

func emitStoreBuiltinScratch64(assembler *amd64.Assembler, offset int32, src amd64.Register) {
	assembler.MoveMemReg64(amd64.RegRSP, builtinBodyCallBlockBaseOffset+offset, src)
}

func emitLoadBuiltinScratch64(assembler *amd64.Assembler, dst amd64.Register, offset int32) {
	assembler.MoveRegMem64(dst, amd64.RegRSP, builtinBodyCallBlockBaseOffset+offset)
}

func emitStoreBuiltinScratch32(assembler *amd64.Assembler, offset int32, src amd64.Register) {
	assembler.MoveMemReg32(amd64.RegRSP, builtinBodyCallBlockBaseOffset+offset, src)
}

func emitLoadBuiltinScratch32(assembler *amd64.Assembler, dst amd64.Register, offset int32) {
	assembler.MoveRegMem32(dst, amd64.RegRSP, builtinBodyCallBlockBaseOffset+offset)
}

func emitStoreResultRegisterFromScratchIndex(assembler *amd64.Assembler, resultReg amd64.Register) {
	emitLoadBuiltinScratch64(assembler, amd64.RegR8, builtinScratchPayload0Offset)
	emitAdvanceFrameTopForIndexReg(assembler, amd64.RegR8, amd64.RegR10)
	assembler.ShiftLeftRegImm8(amd64.RegR8, 3)
	assembler.MoveRegReg(amd64.RegR10, amd64.RegsBaseRegister)
	assembler.AddRegReg(amd64.RegR10, amd64.RegR8)
	assembler.MoveMemReg64(amd64.RegR10, 0, resultReg)
}

func emitReturnBuiltinContinuation(assembler *amd64.Assembler, clearFlags bool) {
	if clearFlags {
		assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, 0)
	}
	assembler.Ret()
}

func emitExitBuiltinStatus(assembler *amd64.Assembler, status uint32, clearFlags bool) {
	if clearFlags {
		assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, 0)
	}
	assembler.MoveRegImm32(amd64.RegRAX, status)
	assembler.XorRegReg(amd64.RegRDX, amd64.RegRDX)
	assembler.AddRegImm32(amd64.RegRSP, int32(builtinTerminalExitStackAdjust))
	assembler.Ret()
}

func emitExitBuiltinStatusWithRegAux(assembler *amd64.Assembler, status uint32, auxReg amd64.Register, clearFlags bool) {
	if clearFlags {
		assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, 0)
	}
	assembler.MoveRegImm32(amd64.RegRAX, status)
	if auxReg != amd64.RegRDX {
		assembler.MoveRegReg(amd64.RegRDX, auxReg)
	}
	assembler.AddRegImm32(amd64.RegRSP, int32(builtinTerminalExitStackAdjust))
	assembler.Ret()
}

func emitExitCurrentBuiltinToRuntime(assembler *amd64.Assembler, clearFlags bool) {
	emitLoadBuiltinCallStubID32(assembler, amd64.RegRDX)
	emitExitBuiltinStatusWithRegAux(assembler, compiledStatusStub, amd64.RegRDX, clearFlags)
}

func shiftedBoxedTag(tag value.Tag) uint32 {
	return uint32((uint64(value.BoxedMarker) >> value.TagShift) | uint64(tag))
}

func emitLoadBuiltinCallArg64(assembler *amd64.Assembler, dst amd64.Register, offset int32) {
	assembler.MoveRegMem64(dst, amd64.RegRSP, builtinBodyCallBlockBaseOffset+offset)
}

func emitLoadBuiltinCallStubID32(assembler *amd64.Assembler, dst amd64.Register) {
	assembler.MoveRegMem32(dst, amd64.RegRSP, builtinBodyCallBlockBaseOffset+rtstate.StubCallBlockStubIDOffset)
}

func emitLoadBuiltinCallFlags32(assembler *amd64.Assembler, dst amd64.Register) {
	assembler.MoveRegMem32(dst, amd64.RegRSP, builtinBodyCallBlockBaseOffset+rtstate.StubCallBlockFlagsOffset)
}

func emitLoadClosureObjectFromFrame(assembler *amd64.Assembler, dst amd64.Register) {
	assembler.MoveRegMem64(dst, amd64.CallFrameRegister, rtstate.CallFrameClosureOffset)
	assembler.ShiftLeftRegImm8(dst, 20)
	assembler.ShiftRightRegImm8(dst, 16)
	assembler.AddRegReg(dst, amd64.HeapBaseRegister)
}

func emitDecodeHeapRefFromRaw(assembler *amd64.Assembler, dst amd64.Register) {
	assembler.ShiftLeftRegImm8(dst, 4)
	assembler.AddRegReg(dst, amd64.HeapBaseRegister)
}

func emitLoadLoopSlotAddress(assembler *amd64.Assembler, dst amd64.Register, indexReg amd64.Register) {
	emitLoadBuiltinCallArg64(assembler, indexReg, rtstate.StubCallBlockArg0Offset)
	assembler.ShiftLeftRegImm8(indexReg, 3)
	assembler.MoveRegReg(dst, amd64.RegsBaseRegister)
	assembler.AddRegReg(dst, indexReg)
}

func emitLoadNumberFromSlotAddress(assembler *amd64.Assembler, addrReg amd64.Register, rawReg amd64.Register, tagReg amd64.Register, dst amd64.XMMRegister, errorPath *amd64.Label) {
	assembler.MoveRegMem64(rawReg, addrReg, 0)
	assembler.MoveRegReg(tagReg, rawReg)
	assembler.ShiftRightRegImm8(tagReg, 48)
	assembler.CmpRegImm32(tagReg, 0xFFFF)
	assembler.Jcc(amd64.CondEqual, errorPath)
	assembler.MoveXmmMem64(dst, addrReg, 0)
}

func emitLoadNumberFromValueReg(assembler *amd64.Assembler, valueReg amd64.Register, tagReg amd64.Register, dst amd64.XMMRegister, errorPath *amd64.Label) {
	assembler.MoveRegReg(tagReg, valueReg)
	assembler.ShiftRightRegImm8(tagReg, 48)
	assembler.CmpRegImm32(tagReg, 0xFFFF)
	assembler.Jcc(amd64.CondEqual, errorPath)
	assembler.MoveXmmReg64(dst, valueReg)
}

func emitLoadFloat64Immediate(assembler *amd64.Assembler, dst amd64.XMMRegister, bits uint64, scratchReg amd64.Register) {
	assembler.MoveRegImm64(scratchReg, bits)
	assembler.MoveXmmReg64(dst, scratchReg)
}

func emitStoreResultRegisterFromCallArgXmm(assembler *amd64.Assembler, argOffset int32, resultReg amd64.XMMRegister, indexReg amd64.Register, addrReg amd64.Register) {
	emitLoadBuiltinCallArg64(assembler, indexReg, argOffset)
	emitAdvanceFrameTopForIndexReg(assembler, indexReg, addrReg)
	assembler.ShiftLeftRegImm8(indexReg, 3)
	assembler.MoveRegReg(addrReg, amd64.RegsBaseRegister)
	assembler.AddRegReg(addrReg, indexReg)
	assembler.MoveMemXmm64(addrReg, 0, resultReg)
}

func emitStoreResultRegisterFromCallArgReg(assembler *amd64.Assembler, argOffset int32, resultReg amd64.Register, indexReg amd64.Register, addrReg amd64.Register) {
	emitLoadBuiltinCallArg64(assembler, indexReg, argOffset)
	emitAdvanceFrameTopForIndexReg(assembler, indexReg, addrReg)
	assembler.ShiftLeftRegImm8(indexReg, 3)
	assembler.MoveRegReg(addrReg, amd64.RegsBaseRegister)
	assembler.AddRegReg(addrReg, indexReg)
	assembler.MoveMemReg64(addrReg, 0, resultReg)
}

func emitAdvanceFrameTopForIndexReg(assembler *amd64.Assembler, indexReg amd64.Register, scratchReg amd64.Register) {
	done := assembler.NewLabel()
	assembler.AddRegImm32(indexReg, 1)
	assembler.MoveRegMem32(amd64.RegRAX, amd64.CallFrameRegister, rtstate.CallFrameTopOffset)
	assembler.MoveRegReg(scratchReg, amd64.RegRAX)
	assembler.AndRegImm32(scratchReg, 0xFFFF)
	assembler.CmpRegReg(scratchReg, indexReg)
	assembler.Jcc(amd64.CondAboveEqual, done)
	assembler.AndRegImm32(amd64.RegRAX, 0xFFFF0000)
	assembler.AddRegReg(amd64.RegRAX, indexReg)
	assembler.MoveMemReg32(amd64.CallFrameRegister, rtstate.CallFrameTopOffset, amd64.RegRAX)
	_ = assembler.Bind(done)
	assembler.AddRegImm32(indexReg, -1)
}

func emitPreserveCallerTopForTForLoop(assembler *amd64.Assembler, callerFrameReg amd64.Register, topReg amd64.Register, scratchReg amd64.Register, compareReg amd64.Register) {
	done := assembler.NewLabel()
	keepExisting := assembler.NewLabel()
	emitLoadBuiltinCallFlags32(assembler, compareReg)
	assembler.AndRegImm32(compareReg, builtinCallBlockFlagTForLoop)
	assembler.CmpRegImm32(compareReg, builtinCallBlockFlagTForLoop)
	assembler.Jcc(amd64.CondNotEqual, done)
	assembler.MoveRegMem32(scratchReg, callerFrameReg, rtstate.CallFrameTopOffset)
	assembler.MoveRegReg(compareReg, scratchReg)
	assembler.AndRegImm32(compareReg, 0xFFFF)
	assembler.CmpRegReg(compareReg, topReg)
	assembler.Jcc(amd64.CondAboveEqual, keepExisting)
	assembler.AndRegImm32(scratchReg, 0xFFFF0000)
	assembler.AddRegReg(scratchReg, topReg)
	assembler.MoveRegReg(topReg, scratchReg)
	assembler.Jmp(done)
	_ = assembler.Bind(keepExisting)
	assembler.MoveRegReg(topReg, scratchReg)
	_ = assembler.Bind(done)
}

func emitJumpIfValueFalsey(assembler *amd64.Assembler, valueReg amd64.Register, scratchReg amd64.Register, falseyLabel *amd64.Label) {
	assembler.MoveRegImm64(scratchReg, uint64(value.NilValue().Bits()))
	assembler.CmpRegReg(valueReg, scratchReg)
	assembler.Jcc(amd64.CondEqual, falseyLabel)
	assembler.MoveRegImm64(scratchReg, uint64(value.BoolValue(false).Bits()))
	assembler.CmpRegReg(valueReg, scratchReg)
	assembler.Jcc(amd64.CondEqual, falseyLabel)
}

func emitLoadThreadStateHeader(assembler *amd64.Assembler, dst amd64.Register) {
	assembler.MoveRegMem64(dst, amd64.VMStateRegister, rtstate.VMStateActiveThreadStateOffset)
}

func emitStoreThreadCurrentFrame(assembler *amd64.Assembler, threadReg amd64.Register, frameReg amd64.Register) {
	assembler.MoveMemReg64(threadReg, rtstate.ThreadCurrentFrameOffset, frameReg)
}

func emitRestoreCallerSiteID(assembler *amd64.Assembler) {
	emitLoadBuiltinScratch64(assembler, amd64.RegRAX, builtinScratchTableRefOffset)
	assembler.MoveMemReg32(amd64.RegR11, execCtxSiteIDOffset, amd64.RegRAX)
}

func emitRestoreCallerFrameFromScratch(assembler *amd64.Assembler) {
	emitLoadBuiltinScratch64(assembler, amd64.RegR13, builtinScratchPayload3Offset)
	emitLoadBuiltinScratch64(assembler, amd64.RegR12, builtinScratchPayload2Offset)
	emitLoadThreadStateHeader(assembler, amd64.RegRAX)
	emitStoreThreadCurrentFrame(assembler, amd64.RegRAX, amd64.RegR13)
}

func emitReturnNestedCallDispatch(assembler *amd64.Assembler, flags uint32, hasAux bool, auxReg amd64.Register) {
	assembler.MoveRegMem32(amd64.RegRAX, amd64.RegR11, execCtxSiteIDOffset)
	assembler.MoveMemReg32(amd64.RegR11, execCtxReserved0Off, amd64.RegRAX)
	if hasAux {
		assembler.MoveMemReg32(amd64.RegR11, execCtxReserved1Off, auxReg)
	} else {
		assembler.MoveMemImm32(amd64.RegR11, execCtxReserved1Off, 0)
	}
	assembler.MoveMemImm32(amd64.RegR11, execCtxReserved2Off, 0)
	assembler.MoveMemImm32(amd64.RegR11, execCtxReserved3Off, 0)
	emitRestoreCallerSiteID(assembler)
	assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, flags)
	emitExitCurrentBuiltinToRuntime(assembler, false)
}

func emitZeroSlots(assembler *amd64.Assembler, baseReg amd64.Register, countReg amd64.Register) {
	loop := assembler.NewLabel()
	done := assembler.NewLabel()
	assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	_ = assembler.Bind(loop)
	assembler.CmpRegImm32(countReg, 0)
	assembler.Jcc(amd64.CondEqual, done)
	assembler.MoveMemReg64(baseReg, 0, amd64.RegRAX)
	assembler.AddRegImm32(baseReg, int32(value.TValueSize))
	assembler.AddRegImm32(countReg, -1)
	assembler.Jmp(loop)
	_ = assembler.Bind(done)
}

func emitCopySlots(assembler *amd64.Assembler, dstReg amd64.Register, srcReg amd64.Register, countReg amd64.Register) {
	loop := assembler.NewLabel()
	done := assembler.NewLabel()
	_ = assembler.Bind(loop)
	assembler.CmpRegImm32(countReg, 0)
	assembler.Jcc(amd64.CondEqual, done)
	assembler.MoveRegMem64(amd64.RegRAX, srcReg, 0)
	assembler.MoveMemReg64(dstReg, 0, amd64.RegRAX)
	assembler.AddRegImm32(srcReg, int32(value.TValueSize))
	assembler.AddRegImm32(dstReg, int32(value.TValueSize))
	assembler.AddRegImm32(countReg, -1)
	assembler.Jmp(loop)
	_ = assembler.Bind(done)
}

func emitCopyCallArguments(assembler *amd64.Assembler, dstReg amd64.Register, srcBaseReg amd64.Register, srcIndexReg amd64.Register, countReg amd64.Register, addrScratchReg amd64.Register) {
	loop := assembler.NewLabel()
	done := assembler.NewLabel()
	_ = assembler.Bind(loop)
	assembler.CmpRegImm32(countReg, 0)
	assembler.Jcc(amd64.CondEqual, done)
	assembler.MoveRegReg(addrScratchReg, srcIndexReg)
	assembler.ShiftLeftRegImm8(addrScratchReg, 3)
	assembler.AddRegReg(addrScratchReg, srcBaseReg)
	assembler.MoveRegMem64(amd64.RegRAX, addrScratchReg, 0)
	assembler.MoveMemReg64(dstReg, 0, amd64.RegRAX)
	assembler.AddRegImm32(dstReg, int32(value.TValueSize))
	assembler.AddRegImm32(srcIndexReg, 1)
	assembler.AddRegImm32(countReg, -1)
	assembler.Jmp(loop)
	_ = assembler.Bind(done)
}

func emitCountOpenCallArguments(assembler *amd64.Assembler, srcIndexReg amd64.Register, topReg amd64.Register, countReg amd64.Register, limitReg amd64.Register) {
	loop := assembler.NewLabel()
	done := assembler.NewLabel()
	_ = assembler.Bind(loop)
	assembler.CmpRegReg(srcIndexReg, topReg)
	assembler.Jcc(amd64.CondAboveEqual, done)
	assembler.CmpRegReg(countReg, limitReg)
	assembler.Jcc(amd64.CondAboveEqual, done)
	assembler.AddRegImm32(srcIndexReg, 1)
	assembler.AddRegImm32(countReg, 1)
	assembler.Jmp(loop)
	_ = assembler.Bind(done)
}

func emitCopyResultsWithNilFill(assembler *amd64.Assembler, dstReg amd64.Register, srcReg amd64.Register, wantedReg amd64.Register, actualReg amd64.Register) {
	loop := assembler.NewLabel()
	writeNil := assembler.NewLabel()
	stored := assembler.NewLabel()
	done := assembler.NewLabel()
	assembler.MoveRegImm64(amd64.RegRSI, uint64(value.NilValue().Bits()))
	_ = assembler.Bind(loop)
	assembler.CmpRegImm32(wantedReg, 0)
	assembler.Jcc(amd64.CondEqual, done)
	assembler.CmpRegImm32(actualReg, 0)
	assembler.Jcc(amd64.CondEqual, writeNil)
	assembler.MoveRegMem64(amd64.RegRAX, srcReg, 0)
	assembler.MoveMemReg64(dstReg, 0, amd64.RegRAX)
	assembler.AddRegImm32(srcReg, int32(value.TValueSize))
	assembler.AddRegImm32(actualReg, -1)
	assembler.Jmp(stored)

	_ = assembler.Bind(writeNil)
	assembler.MoveMemReg64(dstReg, 0, amd64.RegRSI)

	_ = assembler.Bind(stored)
	assembler.AddRegImm32(dstReg, int32(value.TValueSize))
	assembler.AddRegImm32(wantedReg, -1)
	assembler.Jmp(loop)
	_ = assembler.Bind(done)
}

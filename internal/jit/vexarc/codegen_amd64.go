//go:build amd64

package vexarc

import (
	"math"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/jit"
	rt "vexlua/internal/runtime"
)

var (
	exitReasonOffset     = int32(unsafe.Offsetof(jit.NativeExitRecord{}.Reason))
	exitResumePCOffset   = int32(unsafe.Offsetof(jit.NativeExitRecord{}.ResumePC))
	exitCodeOffsetOffset = int32(unsafe.Offsetof(jit.NativeExitRecord{}.CodeOffset))
	exitHelperIDOffset   = int32(unsafe.Offsetof(jit.NativeExitRecord{}.HelperID))
	exitDetailOffset     = int32(unsafe.Offsetof(jit.NativeExitRecord{}.Detail))
	exitFlagsOffset      = int32(unsafe.Offsetof(jit.NativeExitRecord{}.Flags))
	exitReturnValOffset  = int32(unsafe.Offsetof(jit.NativeExitRecord{}.ReturnValue))

	threadStackTopOffset         = int32(unsafe.Offsetof(jit.NativeThreadState{}.StackTop))
	threadStackCapacityOffset    = int32(unsafe.Offsetof(jit.NativeThreadState{}.StackCapacity))
	threadHeapTablesBaseOffset   = int32(unsafe.Offsetof(jit.NativeThreadState{}.HeapTablesBase))
	threadHeapTablesLenOffset    = int32(unsafe.Offsetof(jit.NativeThreadState{}.HeapTablesLen))
	threadFieldCachesBaseOffset  = int32(unsafe.Offsetof(jit.NativeThreadState{}.FieldCachesBase))
	threadFieldCachesLenOffset   = int32(unsafe.Offsetof(jit.NativeThreadState{}.FieldCachesLen))
	threadCallCachesBaseOffset   = int32(unsafe.Offsetof(jit.NativeThreadState{}.CallCachesBase))
	threadCallCachesLenOffset    = int32(unsafe.Offsetof(jit.NativeThreadState{}.CallCachesLen))
	threadUpvaluesBaseOffset     = int32(unsafe.Offsetof(jit.NativeThreadState{}.UpvaluesBase))
	threadUpvaluesLenOffset      = int32(unsafe.Offsetof(jit.NativeThreadState{}.UpvaluesLen))
	threadDirectCallCountOffset  = int32(unsafe.Offsetof(jit.NativeThreadState{}.DirectCallCount))
	threadPendingCallCacheOffset = int32(unsafe.Offsetof(jit.NativeThreadState{}.PendingCallCache))
	threadCurrentEnvHandleOffset = int32(unsafe.Offsetof(jit.NativeThreadState{}.CurrentEnvHandle))
	threadPendingCalleeOffset    = int32(unsafe.Offsetof(jit.NativeThreadState{}.PendingCallee))
	threadPendingFrameOffset     = int32(unsafe.Offsetof(jit.NativeThreadState{}.PendingFrame))
	threadPendingCallExitOffset  = int32(unsafe.Offsetof(jit.NativeThreadState{}.PendingCallExit))

	frameBaseOffset        = int32(unsafe.Offsetof(jit.NativeFrameState{}.Base))
	framePCOffset          = int32(unsafe.Offsetof(jit.NativeFrameState{}.PC))
	frameMaxStackOffset    = int32(unsafe.Offsetof(jit.NativeFrameState{}.MaxStack))
	frameSlotsBaseOffset   = int32(unsafe.Offsetof(jit.NativeFrameState{}.SlotsBase))
	frameResultRegOffset   = int32(unsafe.Offsetof(jit.NativeFrameState{}.ResultReg))
	frameResultCountOffset = int32(unsafe.Offsetof(jit.NativeFrameState{}.ResultCount))
	frameVarargCountOffset = int32(unsafe.Offsetof(jit.NativeFrameState{}.VarargCount))

	directCallCacheCalleeOffset          = int32(unsafe.Offsetof(jit.DirectCallCache{}.Callee))
	directCallCacheEntryOffset           = int32(unsafe.Offsetof(jit.DirectCallCache{}.Entry))
	directCallCacheMaxStackOffset        = int32(unsafe.Offsetof(jit.DirectCallCache{}.MaxStack))
	directCallCacheFlagsOffset           = int32(unsafe.Offsetof(jit.DirectCallCache{}.Flags))
	directCallCacheFieldCachesBaseOffset = int32(unsafe.Offsetof(jit.DirectCallCache{}.FieldCachesBase))
	directCallCacheFieldCachesLenOffset  = int32(unsafe.Offsetof(jit.DirectCallCache{}.FieldCachesLen))
	directCallCacheCallCachesBaseOffset  = int32(unsafe.Offsetof(jit.DirectCallCache{}.CallCachesBase))
	directCallCacheCallCachesLenOffset   = int32(unsafe.Offsetof(jit.DirectCallCache{}.CallCachesLen))
	directCallCacheEnvHandleOffset       = int32(unsafe.Offsetof(jit.DirectCallCache{}.EnvHandle))
	directCallCacheSize                  = int32(unsafe.Sizeof(jit.DirectCallCache{}))

	directCallSaveBXOffset              = int32(0)
	directCallSaveSIOffset              = int32(8)
	directCallSaveDXOffset              = int32(16)
	directCallSaveOldTopOffset          = int32(24)
	directCallSaveMaxStackOffset        = int32(32)
	directCallSaveCalleeOffset          = int32(40)
	directCallSaveFieldCachesBaseOffset = int32(48)
	directCallSaveFieldCachesLenOffset  = int32(56)
	directCallSaveCallCachesBaseOffset  = int32(64)
	directCallSaveCallCachesLenOffset   = int32(72)
	directCallSaveEnvHandleOffset       = int32(80)
	directCallFrameOffset               = int32(88)
	directCallExitOffset                = alignI32(directCallFrameOffset+int32(unsafe.Sizeof(jit.NativeFrameState{})), 8)
	directCallStackSize                 = alignI32(directCallExitOffset+int32(unsafe.Sizeof(jit.NativeExitRecord{})), 8)

	tableArrayOffset   = int32(rt.VexarcTableArrayOffset)
	tableFieldsOffset  = int32(rt.VexarcTableFieldsOffset)
	tableMetaOffset    = int32(rt.VexarcTableMetaOffset)
	tableVersionOffset = int32(rt.VexarcTableVersionOffset)

	fieldCacheSize          = int32(rt.VexarcFieldCacheSize)
	fieldCacheTableOffset   = int32(rt.VexarcFieldCacheTableOffset)
	fieldCacheVersionOffset = int32(rt.VexarcFieldCacheVersionOffset)
	fieldCacheSlotOffset    = int32(rt.VexarcFieldCacheSlotOffset)

	sliceDataOffset = int32(rt.VexarcSliceDataOffset)
	sliceLenOffset  = int32(rt.VexarcSliceLenOffset)
	sliceCapOffset  = int32(rt.VexarcSliceCapOffset)

	nativeUpvalueCellOffset = int32(unsafe.Offsetof(jit.NativeUpvalue{}.Cell))
	nativeUpvalueSize       = int32(unsafe.Sizeof(jit.NativeUpvalue{}))
)

func alignI32(value int32, alignment int32) int32 {
	mask := alignment - 1
	return (value + mask) &^ mask
}

func compileWholeProtoNative(cache *jit.CodeCache, req jit.CompileRequest) (jit.CompiledUnit, error) {
	meta := jit.NewWholeProtoMeta(req.Proto)
	return compileNativeUnit(cache, req, meta, req.Proto.Name)
}

func canCompileNativeRange(proto *bytecode.Proto, region jit.Region) error {
	for pc := region.StartPC; pc < region.EndPC; {
		span, err := nativeInstrSpan(proto, pc, region.EndPC)
		if err != nil {
			return err
		}
		pc += span
	}
	return nil
}

func nativeInstrSpan(proto *bytecode.Proto, pc int, endPC int) (int, error) {
	instr := proto.Code[pc]
	if _, ok := fusedCompareJumpInRange(proto, pc, endPC); ok {
		return 2, nil
	}
	switch instr.Op {
	case bytecode.OpNoop, bytecode.OpMove, bytecode.OpAddNum, bytecode.OpJump, bytecode.OpJumpIfFalse, bytecode.OpJumpIfTrue, bytecode.OpLessEqualJump, bytecode.OpReturn:
		return 1, nil
	case bytecode.OpAdd, bytecode.OpConcat, bytecode.OpUnm:
		return 1, nil
	case bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod, bytecode.OpPow:
		return 1, nil
	case bytecode.OpEqual, bytecode.OpLess, bytecode.OpLessEqual, bytecode.OpNot:
		return 1, nil
	case bytecode.OpGetField, bytecode.OpGetTable, bytecode.OpSetTable, bytecode.OpLen, bytecode.OpSelf:
		return 1, nil
	case bytecode.OpCall:
		return 1, nil
	case bytecode.OpCallMulti, bytecode.OpTailCall:
		if !proto.Scripted {
			return 0, jit.ErrUnsupported
		}
		return 1, nil
	case bytecode.OpLoadGlobal, bytecode.OpStoreGlobal, bytecode.OpSetField, bytecode.OpNewTable:
		return 1, nil
	case bytecode.OpLoadUpvalue, bytecode.OpStoreUpvalue, bytecode.OpClosure, bytecode.OpVararg, bytecode.OpAppendTable, bytecode.OpReturnAppendPending, bytecode.OpIterPairs, bytecode.OpIterIPairs, bytecode.OpYield, bytecode.OpClose:
		if !proto.Scripted {
			return 0, jit.ErrUnsupported
		}
		return 1, nil
	case bytecode.OpAddConst:
		if !proto.Constants[instr.D].IsNumber() || math.IsNaN(proto.Constants[instr.D].Number()) {
			return 0, jit.ErrUnsupported
		}
		return 1, nil
	case bytecode.OpGetFieldIC, bytecode.OpSelfIC, bytecode.OpGetTableArray, bytecode.OpSetTableArray, bytecode.OpLenTable:
		return 1, nil
	case bytecode.OpLoadConst:
		value := proto.Constants[instr.D]
		switch value.Kind() {
		case rt.KindNumber:
			if math.IsNaN(value.Number()) {
				return 0, jit.ErrUnsupported
			}
		case rt.KindNil, rt.KindBool, rt.KindHandle:
		default:
			return 0, jit.ErrUnsupported
		}
		return 1, nil
	case bytecode.OpReturnMulti:
		if !proto.Scripted && instr.B > 1 {
			return 0, jit.ErrUnsupported
		}
		return 1, nil
	default:
		return 0, jit.ErrUnsupported
	}
}

func compileNativeUnit(cache *jit.CodeCache, req jit.CompileRequest, meta *jit.CompiledUnitMeta, name string) (jit.CompiledUnit, error) {
	if err := canCompileNativeRange(req.Proto, meta.Region); err != nil {
		return nil, err
	}
	builder := jit.NewBytecodeOffsetTableBuilder(meta)
	auxInlineCacheSlots := allocateAuxInlineCacheSlots(meta)
	directCallSlots := allocateDirectCallCacheSlots(meta)
	assembler := newAMD64Assembler()
	for pc := meta.Region.StartPC; pc < meta.Region.EndPC; pc++ {
		instr := req.Proto.Code[pc]
		if err := builder.Add(pc, assembler.pc()); err != nil {
			return nil, err
		}
		assembler.bind(pc)
		if next, ok := fusedCompareJumpInRange(req.Proto, pc, meta.Region.EndPC); ok {
			if err := emitCompareJumpFalse(meta, assembler, pc, instr, next); err != nil {
				return nil, err
			}
			pc++
			continue
		}
		if handled, err := emitInlineFastPath(meta, assembler, pc, instr, auxInlineCacheSlots, directCallSlots); handled {
			if err != nil {
				return nil, err
			}
			continue
		}
		if helperKind, ok := helperKindForInstr(instr.Op); ok {
			helperID, err := meta.AddHelperCall(pc, pc+1, helperKind, assembler.pc())
			if err != nil {
				return nil, err
			}
			emitHelperExit(assembler, helperID, pc+1, assembler.pc())
			continue
		}
		switch instr.Op {
		case bytecode.OpNoop:
		case bytecode.OpLoadConst:
			assembler.movRegImm64(regAX, uint64(req.Proto.Constants[instr.D]))
			assembler.movMemReg64(regBX, slotDisp(instr.A), regAX)
		case bytecode.OpMove:
			assembler.movRegMem64(regAX, regBX, slotDisp(instr.B))
			assembler.movMemReg64(regBX, slotDisp(instr.A), regAX)
		case bytecode.OpAddNum:
			emitAddNum(assembler, instr)
		case bytecode.OpAddConst:
			emitAddConst(assembler, req.Proto, instr)
		case bytecode.OpJump:
			if err := emitJump(meta, assembler, pc, int(instr.D)); err != nil {
				return nil, err
			}
		case bytecode.OpJumpIfFalse:
			if err := emitTruthyJump(meta, assembler, pc, instr, false); err != nil {
				return nil, err
			}
		case bytecode.OpJumpIfTrue:
			if err := emitTruthyJump(meta, assembler, pc, instr, true); err != nil {
				return nil, err
			}
		case bytecode.OpLessEqualJump:
			if err := emitLessEqualJump(meta, assembler, pc, instr); err != nil {
				return nil, err
			}
		case bytecode.OpReturn:
			emitReturn(assembler, instr.A)
		case bytecode.OpReturnMulti:
			if instr.B <= 1 {
				emitReturnMulti(assembler, instr)
				break
			}
			helperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperReturnMulti, assembler.pc())
			if err != nil {
				return nil, err
			}
			emitHelperExit(assembler, helperID, pc+1, assembler.pc())
		}
	}
	if len(assembler.code) == 0 || assembler.code[len(assembler.code)-1] != 0xC3 {
		emitInterpretExit(assembler, meta.Region.EndPC, assembler.pc())
	}
	if err := assembler.patch(); err != nil {
		return nil, err
	}
	builder.Finish()
	blob, err := cache.Install(name, meta, assembler.code, 0)
	if err != nil {
		return nil, err
	}
	return &stubUnit{name: name, meta: meta, blob: blob}, nil
}

func allocateAuxInlineCacheSlots(meta *jit.CompiledUnitMeta) map[int]int {
	if meta == nil || meta.Proto == nil {
		return nil
	}
	slots := make(map[int]int)
	nextSlot := meta.Proto.InlineCaches
	for pc := meta.Region.StartPC; pc < meta.Region.EndPC; pc++ {
		switch meta.Proto.Code[pc].Op {
		case bytecode.OpLoadGlobal, bytecode.OpSetField:
			slots[pc] = nextSlot
			meta.InlineCacheSlots = append(meta.InlineCacheSlots, nextSlot)
			nextSlot++
		}
	}
	return slots
}

func allocateDirectCallCacheSlots(meta *jit.CompiledUnitMeta) map[int]int {
	if meta == nil || meta.Proto == nil || !meta.Proto.Scripted {
		return nil
	}
	slots := make(map[int]int)
	nextSlot := 0
	for pc := meta.Region.StartPC; pc < meta.Region.EndPC; pc++ {
		switch meta.Proto.Code[pc].Op {
		case bytecode.OpCall, bytecode.OpCallMulti, bytecode.OpTailCall:
		default:
			continue
		}
		slots[pc] = nextSlot
		meta.CallCacheSlots = append(meta.CallCacheSlots, nextSlot)
		nextSlot++
	}
	return slots
}

func fusedCompareJump(proto *bytecode.Proto, pc int) (bytecode.Instr, bool) {
	return fusedCompareJumpInRange(proto, pc, len(proto.Code))
}

func fusedCompareJumpInRange(proto *bytecode.Proto, pc int, endPC int) (bytecode.Instr, bool) {
	if pc+1 >= endPC {
		return bytecode.Instr{}, false
	}
	instr := proto.Code[pc]
	next := proto.Code[pc+1]
	if next.Op != bytecode.OpJumpIfFalse || next.A != instr.A {
		return bytecode.Instr{}, false
	}
	switch instr.Op {
	case bytecode.OpLess, bytecode.OpLessEqual:
		return next, true
	default:
		return bytecode.Instr{}, false
	}
}

func emitAddNum(a *amd64Assembler, instr bytecode.Instr) {
	a.movsdXmmMem(xmm0, regBX, slotDisp(instr.B))
	a.addsdXmmMem(xmm0, regBX, slotDisp(instr.C))
	nanLabel := a.newLabel()
	doneLabel := a.newLabel()
	a.ucomisdXmmXmm(xmm0, xmm0)
	a.jp(nanLabel)
	a.movsdMemXmm(regBX, slotDisp(instr.A), xmm0)
	a.jump(doneLabel)
	a.bind(nanLabel)
	a.movRegImm64(regAX, uint64(rt.NaNValue))
	a.movMemReg64(regBX, slotDisp(instr.A), regAX)
	a.bind(doneLabel)
}

func emitAddConst(a *amd64Assembler, proto *bytecode.Proto, instr bytecode.Instr) {
	a.movsdXmmMem(xmm0, regBX, slotDisp(instr.B))
	a.movRegImm64(regAX, uint64(proto.Constants[instr.D]))
	a.movqXmmReg(xmm1, regAX)
	a.addsdXmmXmm(xmm0, xmm1)
	nanLabel := a.newLabel()
	doneLabel := a.newLabel()
	a.ucomisdXmmXmm(xmm0, xmm0)
	a.jp(nanLabel)
	a.movsdMemXmm(regBX, slotDisp(instr.A), xmm0)
	a.jump(doneLabel)
	a.bind(nanLabel)
	a.movRegImm64(regAX, uint64(rt.NaNValue))
	a.movMemReg64(regBX, slotDisp(instr.A), regAX)
	a.bind(doneLabel)
}

func emitLessEqualJump(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr) error {
	a.movsdXmmMem(xmm0, regBX, slotDisp(instr.A))
	a.ucomisdXmmMem(xmm0, regBX, slotDisp(instr.B))
	if meta.ContainsPC(int(instr.D)) {
		nanLabel := a.newLabel()
		a.jp(nanLabel)
		a.jbe(int(instr.D))
		a.bind(nanLabel)
		return nil
	}
	exitLabel := a.newLabel()
	doneLabel := a.newLabel()
	a.jp(doneLabel)
	a.jbe(exitLabel)
	a.jump(doneLabel)
	a.bind(exitLabel)
	if err := meta.AddSideExitAt(pc, int(instr.D), jit.ExitSideExit, a.pc()); err != nil {
		return err
	}
	emitSideExit(a, int(instr.D), a.pc())
	a.bind(doneLabel)
	return nil
}

func emitCompareJumpFalse(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, jump bytecode.Instr) error {
	a.movsdXmmMem(xmm0, regBX, slotDisp(instr.B))
	a.ucomisdXmmMem(xmm0, regBX, slotDisp(instr.C))
	if meta.ContainsPC(int(jump.D)) {
		a.jp(int(jump.D))
		switch instr.Op {
		case bytecode.OpLess:
			a.jae(int(jump.D))
		case bytecode.OpLessEqual:
			a.ja(int(jump.D))
		}
		return nil
	}
	exitLabel := a.newLabel()
	doneLabel := a.newLabel()
	a.jp(exitLabel)
	switch instr.Op {
	case bytecode.OpLess:
		a.jae(exitLabel)
	case bytecode.OpLessEqual:
		a.ja(exitLabel)
	}
	a.jump(doneLabel)
	a.bind(exitLabel)
	if err := meta.AddSideExitAt(pc, int(jump.D), jit.ExitSideExit, a.pc()); err != nil {
		return err
	}
	emitSideExit(a, int(jump.D), a.pc())
	a.bind(doneLabel)
	return nil
}

func emitTruthyJump(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, branchOnTruthy bool) error {
	branchLabel := a.newLabel()
	doneLabel := a.newLabel()
	a.movRegMem64(regAX, regBX, slotDisp(instr.A))
	a.movRegImm64(regR8, uint64(rt.NilValue))
	a.cmpRegReg(regAX, regR8)
	if branchOnTruthy {
		a.je(doneLabel)
	} else {
		a.je(branchLabel)
	}
	a.movRegImm64(regR8, uint64(rt.FalseValue))
	a.cmpRegReg(regAX, regR8)
	if branchOnTruthy {
		a.je(doneLabel)
	} else {
		a.je(branchLabel)
	}
	if branchOnTruthy {
		a.jump(branchLabel)
	} else {
		a.jump(doneLabel)
	}
	if meta.ContainsPC(int(instr.D)) {
		a.bind(branchLabel)
		a.jump(int(instr.D))
		a.bind(doneLabel)
	} else {
		if err := meta.AddSideExitAt(pc, int(instr.D), jit.ExitSideExit, a.pc()); err != nil {
			return err
		}
		a.bind(branchLabel)
		emitSideExit(a, int(instr.D), a.pc())
		a.bind(doneLabel)
		return nil
	}
	return nil
}

func emitInlineFastPath(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, auxInlineCacheSlots map[int]int, directCallSlots map[int]int) (bool, error) {
	switch instr.Op {
	case bytecode.OpEqual:
		return true, emitEqualFast(meta, a, pc, instr)
	case bytecode.OpLess:
		return true, emitCompareFast(meta, a, pc, instr, jit.HelperLess)
	case bytecode.OpLessEqual:
		return true, emitCompareFast(meta, a, pc, instr, jit.HelperLessEqual)
	case bytecode.OpNot:
		return true, emitNotFast(a, instr)
	case bytecode.OpSub:
		return true, emitBinaryArithmeticFast(meta, a, pc, instr, jit.HelperSub, (*amd64Assembler).subsdXmmMem)
	case bytecode.OpMul:
		return true, emitBinaryArithmeticFast(meta, a, pc, instr, jit.HelperMul, (*amd64Assembler).mulsdXmmMem)
	case bytecode.OpDiv:
		return true, emitBinaryArithmeticFast(meta, a, pc, instr, jit.HelperDiv, (*amd64Assembler).divsdXmmMem)
	case bytecode.OpMod:
		return true, emitModFast(meta, a, pc, instr)
	case bytecode.OpLoadUpvalue:
		return true, emitLoadUpvalueFast(meta, a, pc, instr)
	case bytecode.OpStoreUpvalue:
		return true, emitStoreUpvalueFast(meta, a, pc, instr)
	case bytecode.OpGetTableArray:
		return true, emitGetTableArrayFast(meta, a, pc, instr)
	case bytecode.OpSetTableArray:
		return true, emitSetTableArrayFast(meta, a, pc, instr)
	case bytecode.OpLenTable:
		return true, emitLenTableFast(meta, a, pc, instr)
	case bytecode.OpGetFieldIC:
		return true, emitFieldICFast(meta, a, pc, instr, false)
	case bytecode.OpSelfIC:
		return true, emitFieldICFast(meta, a, pc, instr, true)
	case bytecode.OpLoadGlobal:
		cacheSlot, ok := auxInlineCacheSlots[pc]
		if !ok {
			return false, nil
		}
		return true, emitLoadGlobalFast(meta, a, pc, instr, cacheSlot)
	case bytecode.OpSetField:
		cacheSlot, ok := auxInlineCacheSlots[pc]
		if !ok {
			return false, nil
		}
		return true, emitSetFieldFast(meta, a, pc, instr, cacheSlot)
	case bytecode.OpCall:
		callCacheSlot := -1
		if directCallSlots != nil {
			if slot, ok := directCallSlots[pc]; ok {
				callCacheSlot = slot
			}
		}
		return true, emitCallFast(meta, a, pc, instr, callCacheSlot)
	case bytecode.OpCallMulti:
		callCacheSlot := -1
		if directCallSlots != nil {
			if slot, ok := directCallSlots[pc]; ok {
				callCacheSlot = slot
			}
		}
		return true, emitCallMultiFast(meta, a, pc, instr, callCacheSlot)
	case bytecode.OpTailCall:
		callCacheSlot := -1
		if directCallSlots != nil {
			if slot, ok := directCallSlots[pc]; ok {
				callCacheSlot = slot
			}
		}
		return true, emitTailCallFast(meta, a, pc, instr, callCacheSlot)
	default:
		return false, nil
	}
}

func emitCallFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, callCacheSlot int) error {
	luaLabel := a.newLabel()
	genericLabel := a.newLabel()
	doneLabel := 0
	hasDoneLabel := false
	if err := emitGuardHandleKindForSlotOrJump(a, instr.B, rt.ObjectHostFunction, luaLabel); err != nil {
		return err
	}
	hostHelperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperCallHostFunction, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, hostHelperID, pc+1, a.pc())
	a.bind(luaLabel)
	if meta != nil && meta.Proto != nil && meta.Proto.Scripted {
		helperLabel := a.newLabel()
		if err := emitGuardHandleKindForSlotOrJump(a, instr.B, rt.ObjectLuaClosure, genericLabel); err != nil {
			return err
		}
		if callCacheSlot >= 0 {
			doneLabel = a.newLabel()
			hasDoneLabel = true
			if err := emitDirectLuaClosureCall(meta, a, pc, instr, callCacheSlot, int(instr.D), 1, helperLabel, doneLabel); err != nil {
				return err
			}
			a.bind(helperLabel)
		}
		luaHelperID, err := meta.AddHelperCallWithCallCache(pc, pc+1, jit.HelperCallLuaClosure, a.pc(), callCacheSlot)
		if err != nil {
			return err
		}
		emitHelperExit(a, luaHelperID, pc+1, a.pc())
	}
	a.bind(genericLabel)
	genericHelperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperCall, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, genericHelperID, pc+1, a.pc())
	if hasDoneLabel {
		a.bind(doneLabel)
	}
	return nil
}

func emitBinaryArithmeticFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, helperKind jit.HelperKind, emitOp func(*amd64Assembler, byte, byte, int32)) error {
	helperLabel := a.newLabel()
	doneLabel := a.newLabel()
	emitGuardNumericSlotOrJump(a, instr.B, helperLabel)
	emitGuardNumericSlotOrJump(a, instr.C, helperLabel)
	a.movsdXmmMem(xmm0, regBX, slotDisp(instr.B))
	emitOp(a, xmm0, regBX, slotDisp(instr.C))
	emitStoreNumericResultOrNaN(a, instr.A, xmm0)
	a.jump(doneLabel)
	a.bind(helperLabel)
	helperID, err := meta.AddHelperCall(pc, pc+1, helperKind, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitEqualFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr) error {
	helperLabel := a.newLabel()
	lhsBoxedLabel := a.newLabel()
	falseLabel := a.newLabel()
	trueLabel := a.newLabel()
	doneLabel := a.newLabel()

	a.movRegMem64(regAX, regBX, slotDisp(instr.B))
	a.movRegMem64(regR8, regBX, slotDisp(instr.C))

	// Fast number-number equality.
	a.movRegReg(regR10, regAX)
	a.movRegImm64(regR11, rt.VexarcValueBoxCheckMask)
	a.andRegReg(regR10, regR11)
	a.movRegImm64(regR11, rt.VexarcValueBoxBase)
	a.cmpRegReg(regR10, regR11)
	a.je(lhsBoxedLabel)
	a.movRegReg(regR10, regR8)
	a.movRegImm64(regR11, rt.VexarcValueBoxCheckMask)
	a.andRegReg(regR10, regR11)
	a.movRegImm64(regR11, rt.VexarcValueBoxBase)
	a.cmpRegReg(regR10, regR11)
	a.je(falseLabel)
	a.movqXmmReg(xmm0, regAX)
	a.movqXmmReg(xmm1, regR8)
	a.ucomisdXmmXmm(xmm0, xmm1)
	a.jp(falseLabel)
	a.jne(falseLabel)
	a.jump(trueLabel)

	// Non-number equality keeps the obvious identical-value case in native code.
	a.bind(lhsBoxedLabel)
	a.movRegReg(regR10, regR8)
	a.movRegImm64(regR11, rt.VexarcValueBoxCheckMask)
	a.andRegReg(regR10, regR11)
	a.movRegImm64(regR11, rt.VexarcValueBoxBase)
	a.cmpRegReg(regR10, regR11)
	a.jne(falseLabel)
	a.cmpRegReg(regAX, regR8)
	a.jne(helperLabel)
	a.movRegImm64(regR10, uint64(rt.NaNValue))
	a.cmpRegReg(regAX, regR10)
	a.je(falseLabel)
	a.jump(trueLabel)

	a.bind(trueLabel)
	a.movRegImm64(regR10, uint64(rt.TrueValue))
	a.movMemReg64(regBX, slotDisp(instr.A), regR10)
	a.jump(doneLabel)

	a.bind(falseLabel)
	a.movRegImm64(regR10, uint64(rt.FalseValue))
	a.movMemReg64(regBX, slotDisp(instr.A), regR10)
	a.jump(doneLabel)

	a.bind(helperLabel)
	helperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperEqual, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitCompareFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, helperKind jit.HelperKind) error {
	helperLabel := a.newLabel()
	trueLabel := a.newLabel()
	doneLabel := a.newLabel()

	emitGuardNumericSlotOrJump(a, instr.B, helperLabel)
	emitGuardNumericSlotOrJump(a, instr.C, helperLabel)
	a.movsdXmmMem(xmm0, regBX, slotDisp(instr.B))
	a.ucomisdXmmMem(xmm0, regBX, slotDisp(instr.C))
	a.jp(helperLabel)
	switch helperKind {
	case jit.HelperLess:
		a.jb(trueLabel)
	case jit.HelperLessEqual:
		a.jbe(trueLabel)
	default:
		return nil
	}
	a.movRegImm64(regR10, uint64(rt.FalseValue))
	a.movMemReg64(regBX, slotDisp(instr.A), regR10)
	a.jump(doneLabel)

	a.bind(trueLabel)
	a.movRegImm64(regR10, uint64(rt.TrueValue))
	a.movMemReg64(regBX, slotDisp(instr.A), regR10)
	a.jump(doneLabel)

	a.bind(helperLabel)
	helperID, err := meta.AddHelperCall(pc, pc+1, helperKind, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitNotFast(a *amd64Assembler, instr bytecode.Instr) error {
	trueLabel := a.newLabel()
	doneLabel := a.newLabel()
	a.movRegMem64(regAX, regBX, slotDisp(instr.B))
	a.movRegImm64(regR8, uint64(rt.NilValue))
	a.cmpRegReg(regAX, regR8)
	a.je(trueLabel)
	a.movRegImm64(regR8, uint64(rt.FalseValue))
	a.cmpRegReg(regAX, regR8)
	a.je(trueLabel)
	a.movRegImm64(regR10, uint64(rt.FalseValue))
	a.movMemReg64(regBX, slotDisp(instr.A), regR10)
	a.jump(doneLabel)

	a.bind(trueLabel)
	a.movRegImm64(regR10, uint64(rt.TrueValue))
	a.movMemReg64(regBX, slotDisp(instr.A), regR10)
	a.bind(doneLabel)
	return nil
}

func emitModFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr) error {
	helperLabel := a.newLabel()
	zeroLabel := a.newLabel()
	adjustLabel := a.newLabel()
	storeLabel := a.newLabel()
	doneLabel := a.newLabel()
	a.movRegReg(regR12, regDX)

	emitGuardNumericSlotOrJump(a, instr.B, helperLabel)
	emitGuardNumericSlotOrJump(a, instr.C, helperLabel)

	a.movsdXmmMem(xmm0, regBX, slotDisp(instr.B))
	a.cvttsd2siRegXmm(regAX, xmm0)
	a.cvtsi2sdXmmReg(xmm1, regAX)
	a.ucomisdXmmXmm(xmm0, xmm1)
	a.jp(helperLabel)
	a.jne(helperLabel)
	a.movRegReg(regR8, regAX)

	a.movsdXmmMem(xmm0, regBX, slotDisp(instr.C))
	a.cvttsd2siRegXmm(regAX, xmm0)
	a.cvtsi2sdXmmReg(xmm1, regAX)
	a.ucomisdXmmXmm(xmm0, xmm1)
	a.jp(helperLabel)
	a.jne(helperLabel)
	a.movRegReg(regR9, regAX)

	a.cmpRegImm32(regR9, 0)
	a.je(helperLabel)
	a.cmpRegImm32(regR9, 1)
	a.je(zeroLabel)
	a.cmpRegImm32(regR9, -1)
	a.je(zeroLabel)

	a.movRegReg(regAX, regR8)
	a.cqo()
	a.idivReg(regR9)
	a.cmpRegImm32(regDX, 0)
	a.je(storeLabel)
	a.movRegReg(regR10, regR8)
	a.xorRegReg(regR10, regR9)
	a.js(adjustLabel)
	a.jump(storeLabel)

	a.bind(adjustLabel)
	a.addRegReg(regDX, regR9)

	a.bind(storeLabel)
	a.cvtsi2sdXmmReg(xmm0, regDX)
	a.movRegReg(regDX, regR12)
	a.movsdMemXmm(regBX, slotDisp(instr.A), xmm0)
	a.jump(doneLabel)

	a.bind(zeroLabel)
	a.movRegReg(regDX, regR12)
	a.movRegImm64(regAX, 0)
	a.movMemReg64(regBX, slotDisp(instr.A), regAX)
	a.jump(doneLabel)

	a.bind(helperLabel)
	helperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperMod, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitLoadUpvalueFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr) error {
	helperLabel := a.newLabel()
	doneLabel := a.newLabel()
	emitUpvalueCellOrJump(a, instr.B, helperLabel)
	a.movRegMem64(regR10, regAX, 0)
	a.movMemReg64(regBX, slotDisp(instr.A), regR10)
	a.jump(doneLabel)
	a.bind(helperLabel)
	helperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperLoadUpvalue, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitStoreUpvalueFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr) error {
	helperLabel := a.newLabel()
	doneLabel := a.newLabel()
	emitUpvalueCellOrJump(a, instr.B, helperLabel)
	a.movRegMem64(regR10, regBX, slotDisp(instr.A))
	a.movMemReg64(regAX, 0, regR10)
	a.jump(doneLabel)
	a.bind(helperLabel)
	helperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperStoreUpvalue, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitUpvalueCellOrJump(a *amd64Assembler, index uint16, target int) {
	a.movRegMem64(regAX, regDI, threadUpvaluesLenOffset)
	a.cmpRegImm32(regAX, int32(index)+1)
	a.jb(target)
	a.movRegMem64(regAX, regDI, threadUpvaluesBaseOffset)
	a.movRegMem64(regAX, regAX, int32(index)*nativeUpvalueSize+nativeUpvalueCellOffset)
	a.movRegImm64(regR10, 0)
	a.cmpRegReg(regAX, regR10)
	a.je(target)
}

func emitCallMultiFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, callCacheSlot int) error {
	argCount, resultCount, appendPending := bytecode.UnpackCallSpec(instr.D)
	_ = argCount
	helperLabel := a.newLabel()
	doneLabel := 0
	hasDoneLabel := false
	if meta != nil && meta.Proto != nil && meta.Proto.Scripted && callCacheSlot >= 0 && !appendPending && resultCount > 0 {
		doneLabel = a.newLabel()
		hasDoneLabel = true
		if err := emitGuardHandleKindForSlotOrJump(a, instr.B, rt.ObjectLuaClosure, helperLabel); err != nil {
			return err
		}
		if err := emitDirectLuaClosureCall(meta, a, pc, instr, callCacheSlot, argCount, resultCount, helperLabel, doneLabel); err != nil {
			return err
		}
		a.bind(helperLabel)
	}
	helperID, err := meta.AddHelperCallWithCallCache(pc, pc+1, jit.HelperCallMulti, a.pc(), callCacheSlot)
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	if hasDoneLabel {
		a.bind(doneLabel)
	}
	return nil
}

func emitTailCallFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, callCacheSlot int) error {
	_, _, appendPending := bytecode.UnpackCallSpec(instr.D)
	helperLabel := a.newLabel()
	if meta != nil && meta.Proto != nil && meta.Proto.Scripted && callCacheSlot >= 0 && !appendPending {
		if err := emitGuardHandleKindForSlotOrJump(a, instr.B, rt.ObjectLuaClosure, helperLabel); err != nil {
			return err
		}
		if err := emitDirectLuaClosureTailCall(meta, a, pc, instr, callCacheSlot, helperLabel); err != nil {
			return err
		}
		a.bind(helperLabel)
	}
	helperID, err := meta.AddHelperCallWithCallCache(pc, pc+1, jit.HelperTailCall, a.pc(), callCacheSlot)
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	return nil
}

func emitDirectLuaClosureCall(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, callCacheSlot int, argCount int, resultCount int, helperLabel int, doneLabel int) error {
	nestedLabel := a.newLabel()
	clearLoopLabel := a.newLabel()
	clearDoneLabel := a.newLabel()

	a.movRegMem64(regR8, regDI, threadCallCachesBaseOffset)
	a.cmpRegImm32(regR8, 0)
	a.je(helperLabel)
	cacheDisp := int32(callCacheSlot) * directCallCacheSize
	if cacheDisp != 0 {
		a.addRegImm32(regR8, cacheDisp)
	}
	a.movRegMem64(regR9, regBX, slotDisp(instr.B))
	a.movRegMem64(regR10, regR8, directCallCacheCalleeOffset)
	a.cmpRegReg(regR9, regR10)
	a.jne(helperLabel)
	a.movRegMem64(regR10, regR8, directCallCacheEntryOffset)
	a.cmpRegImm32(regR10, 0)
	a.je(helperLabel)
	a.movRegMem32(regR11, regR8, directCallCacheMaxStackOffset)
	a.cmpRegImm32(regR11, 0)
	a.je(helperLabel)
	a.movRegMem32(regR12, regDI, threadStackTopOffset)
	a.movRegReg(regR13, regR12)
	a.addRegReg(regR13, regR11)
	a.movRegMem32(regR15, regDI, threadStackCapacityOffset)
	a.cmpRegReg(regR13, regR15)
	a.ja(helperLabel)

	a.subRegImm32(regSP, directCallStackSize)
	a.movMemReg64(regSP, directCallSaveBXOffset, regBX)
	a.movMemReg64(regSP, directCallSaveSIOffset, regSI)
	a.movMemReg64(regSP, directCallSaveDXOffset, regDX)
	a.movMemReg64(regSP, directCallSaveOldTopOffset, regR12)
	a.movMemReg64(regSP, directCallSaveMaxStackOffset, regR11)
	a.movMemReg64(regSP, directCallSaveCalleeOffset, regR9)
	a.movRegMem64(regR15, regDI, threadFieldCachesBaseOffset)
	a.movMemReg64(regSP, directCallSaveFieldCachesBaseOffset, regR15)
	a.movRegMem64(regR15, regDI, threadFieldCachesLenOffset)
	a.movMemReg64(regSP, directCallSaveFieldCachesLenOffset, regR15)
	a.movRegMem64(regR15, regDI, threadCallCachesBaseOffset)
	a.movMemReg64(regSP, directCallSaveCallCachesBaseOffset, regR15)
	a.movRegMem64(regR15, regDI, threadCallCachesLenOffset)
	a.movMemReg64(regSP, directCallSaveCallCachesLenOffset, regR15)
	a.movRegMem64(regR15, regDI, threadCurrentEnvHandleOffset)
	a.movMemReg64(regSP, directCallSaveEnvHandleOffset, regR15)
	a.movMemReg32(regDI, threadStackTopOffset, regR13)
	a.movRegMem64(regR15, regR8, directCallCacheFieldCachesBaseOffset)
	a.movMemReg64(regDI, threadFieldCachesBaseOffset, regR15)
	a.movRegMem64(regR15, regR8, directCallCacheFieldCachesLenOffset)
	a.movMemReg64(regDI, threadFieldCachesLenOffset, regR15)
	a.movRegMem64(regR15, regR8, directCallCacheCallCachesBaseOffset)
	a.movMemReg64(regDI, threadCallCachesBaseOffset, regR15)
	a.movRegMem64(regR15, regR8, directCallCacheCallCachesLenOffset)
	a.movMemReg64(regDI, threadCallCachesLenOffset, regR15)
	a.movRegMem64(regR15, regR8, directCallCacheEnvHandleOffset)
	a.movMemReg64(regDI, threadCurrentEnvHandleOffset, regR15)

	a.movRegMem32(regR14, regSI, frameMaxStackOffset)
	a.shlRegImm8(regR14, 3)
	a.addRegReg(regR14, regBX)
	a.movRegReg(regR15, regR14)
	a.movRegImm64(regR13, uint64(rt.NilValue))
	a.bind(clearLoopLabel)
	a.cmpRegImm32(regR11, 0)
	a.je(clearDoneLabel)
	a.movMemReg64(regR15, 0, regR13)
	a.addRegImm32(regR15, 8)
	a.subRegImm32(regR11, 1)
	a.jump(clearLoopLabel)
	a.bind(clearDoneLabel)

	for index := 0; index < argCount; index++ {
		a.movRegMem64(regR15, regBX, slotDisp(instr.C+uint16(index)))
		a.movMemReg64(regR14, int32(index*8), regR15)
	}

	a.movRegReg(regR15, regSP)
	a.addRegImm32(regR15, directCallFrameOffset)
	a.movRegMem32(regR12, regSP, directCallSaveOldTopOffset)
	a.movMemReg32(regR15, frameBaseOffset, regR12)
	a.movMemImm32(regR15, framePCOffset, 0)
	a.movRegMem32(regR11, regSP, directCallSaveMaxStackOffset)
	a.movMemReg32(regR15, frameMaxStackOffset, regR11)
	a.movMemReg64(regR15, frameSlotsBaseOffset, regR14)
	a.movMemImm32(regR15, frameResultRegOffset, int32(instr.A))
	a.movMemImm32(regR15, frameResultCountOffset, int32(resultCount))
	a.movMemImm32(regR15, frameVarargCountOffset, 0)

	a.movRegReg(regSI, regR15)
	a.movRegReg(regDX, regSP)
	a.addRegImm32(regDX, directCallExitOffset)
	a.movRegReg(regBX, regR14)
	a.movRegReg(regAX, regR10)
	a.callReg(regAX)

	a.movRegReg(regR15, regSP)
	a.addRegImm32(regR15, directCallExitOffset)
	a.movRegMem32(regR11, regR15, exitReasonOffset)
	a.cmpRegImm32(regR11, int32(jit.ExitReturn))
	a.jne(nestedLabel)
	a.movRegMem64(regAX, regR15, exitReturnValOffset)
	a.movRegMem32(regR12, regSP, directCallSaveOldTopOffset)
	a.movMemReg32(regDI, threadStackTopOffset, regR12)
	a.movRegMem64(regR15, regSP, directCallSaveFieldCachesBaseOffset)
	a.movMemReg64(regDI, threadFieldCachesBaseOffset, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveFieldCachesLenOffset)
	a.movMemReg64(regDI, threadFieldCachesLenOffset, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveCallCachesBaseOffset)
	a.movMemReg64(regDI, threadCallCachesBaseOffset, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveCallCachesLenOffset)
	a.movMemReg64(regDI, threadCallCachesLenOffset, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveEnvHandleOffset)
	a.movMemReg64(regDI, threadCurrentEnvHandleOffset, regR15)
	a.addMemImm32(regDI, threadDirectCallCountOffset, 1)
	a.movRegMem64(regR15, regSP, directCallSaveBXOffset)
	a.movRegReg(regBX, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveSIOffset)
	a.movRegReg(regSI, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveDXOffset)
	a.movRegReg(regDX, regR15)
	a.addRegImm32(regSP, directCallStackSize)
	a.movMemReg64(regBX, slotDisp(instr.A), regAX)
	if resultCount > 1 {
		a.movRegImm64(regR13, uint64(rt.NilValue))
		for index := 1; index < resultCount; index++ {
			a.movMemReg64(regBX, slotDisp(instr.A+uint16(index)), regR13)
		}
	}
	a.jump(doneLabel)

	a.bind(nestedLabel)
	a.movMemImm32(regDI, threadPendingCallCacheOffset, int32(callCacheSlot))
	a.movRegMem64(regR9, regSP, directCallSaveCalleeOffset)
	a.movMemReg64(regDI, threadPendingCalleeOffset, regR9)
	a.movRegReg(regR15, regSP)
	a.addRegImm32(regR15, directCallFrameOffset)
	a.movRegMem32(regR11, regR15, frameBaseOffset)
	a.movMemReg32(regDI, threadPendingFrameOffset+frameBaseOffset, regR11)
	a.movRegMem32(regR11, regR15, frameMaxStackOffset)
	a.movMemReg32(regDI, threadPendingFrameOffset+frameMaxStackOffset, regR11)
	a.movRegMem64(regR11, regR15, frameSlotsBaseOffset)
	a.movMemReg64(regDI, threadPendingFrameOffset+frameSlotsBaseOffset, regR11)
	a.movRegMem32(regR11, regR15, frameResultRegOffset)
	a.movMemReg32(regDI, threadPendingFrameOffset+frameResultRegOffset, regR11)
	a.movRegMem32(regR11, regR15, frameResultCountOffset)
	a.movMemReg32(regDI, threadPendingFrameOffset+frameResultCountOffset, regR11)
	a.movRegMem32(regR11, regR15, frameVarargCountOffset)
	a.movMemReg32(regDI, threadPendingFrameOffset+frameVarargCountOffset, regR11)
	a.movRegMem32(regR11, regSP, directCallExitOffset+exitResumePCOffset)
	a.movMemReg32(regDI, threadPendingFrameOffset+framePCOffset, regR11)

	a.movRegReg(regR15, regSP)
	a.addRegImm32(regR15, directCallExitOffset)
	a.movRegMem32(regR11, regR15, exitReasonOffset)
	a.movMemReg32(regDI, threadPendingCallExitOffset+exitReasonOffset, regR11)
	a.movRegMem32(regR11, regR15, exitResumePCOffset)
	a.movMemReg32(regDI, threadPendingCallExitOffset+exitResumePCOffset, regR11)
	a.movRegMem32(regR11, regR15, exitCodeOffsetOffset)
	a.movMemReg32(regDI, threadPendingCallExitOffset+exitCodeOffsetOffset, regR11)
	a.movRegMem32(regR11, regR15, exitHelperIDOffset)
	a.movMemReg32(regDI, threadPendingCallExitOffset+exitHelperIDOffset, regR11)
	a.movRegMem32(regR11, regR15, exitDetailOffset)
	a.movMemReg32(regDI, threadPendingCallExitOffset+exitDetailOffset, regR11)
	a.movRegMem64(regR11, regR15, exitReturnValOffset)
	a.movMemReg64(regDI, threadPendingCallExitOffset+exitReturnValOffset, regR11)

	a.movRegMem32(regR12, regSP, directCallSaveOldTopOffset)
	a.movMemReg32(regDI, threadStackTopOffset, regR12)
	a.movRegMem64(regR15, regSP, directCallSaveFieldCachesBaseOffset)
	a.movMemReg64(regDI, threadFieldCachesBaseOffset, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveFieldCachesLenOffset)
	a.movMemReg64(regDI, threadFieldCachesLenOffset, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveCallCachesBaseOffset)
	a.movMemReg64(regDI, threadCallCachesBaseOffset, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveCallCachesLenOffset)
	a.movMemReg64(regDI, threadCallCachesLenOffset, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveEnvHandleOffset)
	a.movMemReg64(regDI, threadCurrentEnvHandleOffset, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveDXOffset)
	a.movMemImm32(regR15, exitReasonOffset, int32(jit.ExitNestedCall))
	a.movMemImm32(regR15, exitResumePCOffset, int32(pc+1))
	a.movMemImm32(regR15, exitCodeOffsetOffset, int32(a.pc()))
	a.movMemImm32(regR15, exitDetailOffset, int32(callCacheSlot))
	a.movRegMem64(regR15, regSP, directCallSaveBXOffset)
	a.movRegReg(regBX, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveSIOffset)
	a.movRegReg(regSI, regR15)
	a.movRegMem64(regR15, regSP, directCallSaveDXOffset)
	a.movRegReg(regDX, regR15)
	a.addRegImm32(regSP, directCallStackSize)
	a.ret()
	return nil
}

func emitDirectLuaClosureTailCall(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, callCacheSlot int, helperLabel int) error {
	_ = meta
	clearLoopLabel := a.newLabel()
	clearDoneLabel := a.newLabel()

	a.movRegMem64(regR8, regDI, threadCallCachesBaseOffset)
	a.cmpRegImm32(regR8, 0)
	a.je(helperLabel)
	cacheDisp := int32(callCacheSlot) * directCallCacheSize
	if cacheDisp != 0 {
		a.addRegImm32(regR8, cacheDisp)
	}
	a.movRegMem32(regR11, regR8, directCallCacheFlagsOffset)
	a.movRegImm64(regR12, uint64(jit.DirectCallNoVararg))
	a.andRegReg(regR11, regR12)
	a.cmpRegImm32(regR11, int32(jit.DirectCallNoVararg))
	a.jne(helperLabel)
	a.movRegMem64(regR9, regBX, slotDisp(instr.B))
	a.movRegMem64(regR10, regR8, directCallCacheCalleeOffset)
	a.cmpRegReg(regR9, regR10)
	a.jne(helperLabel)
	a.movRegMem64(regR10, regR8, directCallCacheEntryOffset)
	a.cmpRegImm32(regR10, 0)
	a.je(helperLabel)
	a.movRegMem32(regR11, regR8, directCallCacheMaxStackOffset)
	a.cmpRegImm32(regR11, 0)
	a.je(helperLabel)
	a.movRegReg(regR10, regR11)
	argCount, _, _ := bytecode.UnpackCallSpec(instr.D)
	a.cmpRegImm32(regR11, int32(argCount))
	a.jb(helperLabel)
	a.movRegMem32(regR12, regSI, frameBaseOffset)
	a.movRegReg(regR13, regR12)
	a.addRegReg(regR13, regR11)
	a.movRegMem32(regR15, regDI, threadStackCapacityOffset)
	a.cmpRegReg(regR13, regR15)
	a.ja(helperLabel)
	a.addMemImm32(regDI, threadDirectCallCountOffset, 1)

	for index := 0; index < argCount; index++ {
		a.movRegMem64(regR15, regBX, slotDisp(instr.C+uint16(index)))
		a.movMemReg64(regBX, int32(index*8), regR15)
	}

	a.movRegReg(regR14, regBX)
	if argCount != 0 {
		a.addRegImm32(regR14, int32(argCount*8))
	}
	a.movRegImm64(regR13, uint64(rt.NilValue))
	a.movRegReg(regR11, regR10)
	a.subRegImm32(regR11, int32(argCount))
	a.bind(clearLoopLabel)
	a.cmpRegImm32(regR11, 0)
	a.je(clearDoneLabel)
	a.movMemReg64(regR14, 0, regR13)
	a.addRegImm32(regR14, 8)
	a.subRegImm32(regR11, 1)
	a.jump(clearLoopLabel)
	a.bind(clearDoneLabel)

	a.movMemImm32(regDI, threadPendingCallCacheOffset, int32(callCacheSlot))
	a.movMemReg64(regDI, threadPendingCalleeOffset, regR9)
	a.movMemReg32(regDI, threadPendingFrameOffset+frameBaseOffset, regR12)
	a.movMemImm32(regDI, threadPendingFrameOffset+framePCOffset, 0)
	a.movMemReg32(regDI, threadPendingFrameOffset+frameMaxStackOffset, regR10)
	a.movMemReg64(regDI, threadPendingFrameOffset+frameSlotsBaseOffset, regBX)
	a.movRegMem32(regR15, regSI, frameResultRegOffset)
	a.movMemReg32(regDI, threadPendingFrameOffset+frameResultRegOffset, regR15)
	a.movRegMem32(regR15, regSI, frameResultCountOffset)
	a.movMemReg32(regDI, threadPendingFrameOffset+frameResultCountOffset, regR15)
	a.movMemImm32(regDI, threadPendingFrameOffset+frameVarargCountOffset, 0)
	a.movMemImm32(regDX, exitReasonOffset, int32(jit.ExitNestedCall))
	a.movMemImm32(regDX, exitResumePCOffset, int32(pc+1))
	a.movMemImm32(regDX, exitCodeOffsetOffset, int32(a.pc()))
	a.movMemImm32(regDX, exitDetailOffset, int32(callCacheSlot))
	a.movMemImm32(regDX, exitFlagsOffset, int32(jit.ExitFlagTailReplace))
	a.ret()
	return nil
}

func emitLoadGlobalFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, cacheSlot int) error {
	helperLabel := a.newLabel()
	doneLabel := a.newLabel()
	a.movRegMem64(regR8, regDI, threadCurrentEnvHandleOffset)
	a.cmpRegImm32(regR8, 0)
	a.je(helperLabel)
	if err := emitLoadTablePointerForHandleOrHelper(a, regR8, helperLabel); err != nil {
		return err
	}
	a.movRegMem64(regR10, regDI, threadFieldCachesBaseOffset)
	a.cmpRegImm32(regR10, 0)
	a.je(helperLabel)
	cacheDisp := int32(cacheSlot) * fieldCacheSize
	if cacheDisp != 0 {
		a.addRegImm32(regR10, cacheDisp)
	}
	a.movRegMem64(regR11, regR10, fieldCacheTableOffset)
	a.cmpRegReg(regR8, regR11)
	a.jne(helperLabel)
	a.movRegMem32(regR11, regR9, tableVersionOffset)
	a.movRegMem32(regR12, regR10, fieldCacheVersionOffset)
	a.cmpRegReg(regR11, regR12)
	a.jne(helperLabel)
	a.movRegMem32(regR11, regR10, fieldCacheSlotOffset)
	a.movRegMem64(regR12, regR9, tableFieldsOffset+sliceLenOffset)
	a.cmpRegReg(regR11, regR12)
	a.jae(helperLabel)
	a.movRegMem64(regR13, regR9, tableFieldsOffset+sliceDataOffset)
	a.shlRegImm8(regR11, 3)
	a.addRegReg(regR11, regR13)
	a.movRegMem64(regAX, regR11, 0)
	a.movRegImm64(regR12, uint64(rt.NilValue))
	a.cmpRegReg(regAX, regR12)
	a.je(helperLabel)
	a.movMemReg64(regBX, slotDisp(instr.A), regAX)
	a.jump(doneLabel)
	a.bind(helperLabel)
	helperID, err := meta.AddHelperCallWithInlineCache(pc, pc+1, jit.HelperLoadGlobal, a.pc(), cacheSlot)
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitSetFieldFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, cacheSlot int) error {
	helperLabel := a.newLabel()
	doneLabel := a.newLabel()
	if err := emitLoadTablePointerForSlotOrHelper(a, instr.A, helperLabel); err != nil {
		return err
	}
	a.movRegMem64(regR10, regDI, threadFieldCachesBaseOffset)
	a.cmpRegImm32(regR10, 0)
	a.je(helperLabel)
	cacheDisp := int32(cacheSlot) * fieldCacheSize
	if cacheDisp != 0 {
		a.addRegImm32(regR10, cacheDisp)
	}
	a.movRegMem64(regR11, regR10, fieldCacheTableOffset)
	a.cmpRegReg(regR8, regR11)
	a.jne(helperLabel)
	a.movRegMem32(regR11, regR9, tableVersionOffset)
	a.movRegMem32(regR12, regR10, fieldCacheVersionOffset)
	a.cmpRegReg(regR11, regR12)
	a.jne(helperLabel)
	a.movRegMem32(regR11, regR10, fieldCacheSlotOffset)
	a.movRegMem64(regR12, regR9, tableFieldsOffset+sliceLenOffset)
	a.cmpRegReg(regR11, regR12)
	a.jae(helperLabel)
	a.movRegMem64(regR13, regR9, tableFieldsOffset+sliceDataOffset)
	a.shlRegImm8(regR11, 3)
	a.addRegReg(regR11, regR13)
	a.movRegMem64(regAX, regBX, slotDisp(instr.B))
	a.movMemReg64(regR11, 0, regAX)
	a.addMemImm32(regR9, tableVersionOffset, 1)
	a.addMemImm32(regR10, fieldCacheVersionOffset, 1)
	a.jump(doneLabel)
	a.bind(helperLabel)
	helperID, err := meta.AddHelperCallWithInlineCache(pc, pc+1, jit.HelperSetField, a.pc(), cacheSlot)
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitLuaClosureCallFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr) error {
	genericLabel := a.newLabel()
	if err := emitGuardHandleKindForSlotOrJump(a, instr.B, rt.ObjectLuaClosure, genericLabel); err != nil {
		return err
	}
	luaHelperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperCallLuaClosure, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, luaHelperID, pc+1, a.pc())
	a.bind(genericLabel)
	genericHelperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperCall, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, genericHelperID, pc+1, a.pc())
	return nil
}

func emitGetTableArrayFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr) error {
	helperLabel := a.newLabel()
	loadLabel := a.newLabel()
	doneLabel := a.newLabel()
	if err := emitLoadTablePointerForSlotOrHelper(a, instr.B, helperLabel); err != nil {
		return err
	}
	emitGuardTableMetaNilOrJump(a, helperLabel)
	emitDecodePositiveIntegerSlotOrJump(a, instr.C, helperLabel)
	a.movRegMem64(regR11, regR9, tableArrayOffset+sliceLenOffset)
	a.cmpRegReg(regR10, regR11)
	a.jbe(loadLabel)
	a.movRegImm64(regAX, uint64(rt.NilValue))
	a.movMemReg64(regBX, slotDisp(instr.A), regAX)
	a.jump(doneLabel)
	a.bind(loadLabel)
	a.movRegMem64(regR12, regR9, tableArrayOffset+sliceDataOffset)
	a.subRegImm32(regR10, 1)
	a.shlRegImm8(regR10, 3)
	a.addRegReg(regR10, regR12)
	a.movRegMem64(regAX, regR10, 0)
	a.movMemReg64(regBX, slotDisp(instr.A), regAX)
	a.jump(doneLabel)
	a.bind(helperLabel)
	helperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperGetTableArray, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitSetTableArrayFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr) error {
	helperLabel := a.newLabel()
	inBoundsLabel := a.newLabel()
	storeLabel := a.newLabel()
	doneLabel := a.newLabel()
	if err := emitLoadTablePointerForSlotOrHelper(a, instr.A, helperLabel); err != nil {
		return err
	}
	emitGuardTableMetaNilOrJump(a, helperLabel)
	emitDecodePositiveIntegerSlotOrJump(a, instr.B, helperLabel)
	a.movRegMem64(regAX, regBX, slotDisp(instr.C))
	a.movRegImm64(regR11, uint64(rt.NilValue))
	a.cmpRegReg(regAX, regR11)
	a.je(helperLabel)
	a.movRegMem64(regR11, regR9, tableArrayOffset+sliceLenOffset)
	a.movRegMem64(regR12, regR9, tableArrayOffset+sliceCapOffset)
	a.movRegMem64(regR13, regR9, tableArrayOffset+sliceDataOffset)
	a.cmpRegReg(regR10, regR11)
	a.jbe(inBoundsLabel)
	a.movRegReg(regR14, regR11)
	a.addRegImm32(regR14, 1)
	a.cmpRegReg(regR10, regR14)
	a.jne(helperLabel)
	a.cmpRegReg(regR11, regR12)
	a.jae(helperLabel)
	a.movMemReg64(regR9, tableArrayOffset+sliceLenOffset, regR14)
	a.jump(storeLabel)
	a.bind(inBoundsLabel)
	a.jump(storeLabel)
	a.bind(storeLabel)
	a.subRegImm32(regR10, 1)
	a.shlRegImm8(regR10, 3)
	a.addRegReg(regR10, regR13)
	a.movMemReg64(regR10, 0, regAX)
	a.addMemImm32(regR9, tableVersionOffset, 1)
	a.jump(doneLabel)
	a.bind(helperLabel)
	helperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperSetTableArray, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitLenTableFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr) error {
	helperLabel := a.newLabel()
	loopLabel := a.newLabel()
	doneScanLabel := a.newLabel()
	doneLabel := a.newLabel()
	if err := emitLoadTablePointerForSlotOrHelper(a, instr.B, helperLabel); err != nil {
		return err
	}
	emitGuardTableMetaNilOrJump(a, helperLabel)
	a.movRegMem64(regR11, regR9, tableArrayOffset+sliceLenOffset)
	a.movRegMem64(regR13, regR9, tableArrayOffset+sliceDataOffset)
	a.movRegImm64(regR10, 0)
	a.movRegImm64(regR12, uint64(rt.NilValue))
	a.bind(loopLabel)
	a.cmpRegReg(regR10, regR11)
	a.jae(doneScanLabel)
	a.movRegReg(regR14, regR10)
	a.shlRegImm8(regR14, 3)
	a.addRegReg(regR14, regR13)
	a.movRegMem64(regAX, regR14, 0)
	a.cmpRegReg(regAX, regR12)
	a.je(doneScanLabel)
	a.addRegImm32(regR10, 1)
	a.jump(loopLabel)
	a.bind(doneScanLabel)
	a.cvtsi2sdXmmReg(xmm0, regR10)
	a.movsdMemXmm(regBX, slotDisp(instr.A), xmm0)
	a.jump(doneLabel)
	a.bind(helperLabel)
	helperID, err := meta.AddHelperCall(pc, pc+1, jit.HelperLenTable, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitFieldICFast(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, instr bytecode.Instr, self bool) error {
	helperLabel := a.newLabel()
	doneLabel := a.newLabel()
	if err := emitLoadTablePointerForSlotOrHelper(a, instr.B, helperLabel); err != nil {
		return err
	}
	a.movRegMem64(regR10, regDI, threadFieldCachesBaseOffset)
	a.cmpRegImm32(regR10, 0)
	a.je(helperLabel)
	cacheDisp := int32(instr.C) * fieldCacheSize
	if cacheDisp != 0 {
		a.addRegImm32(regR10, cacheDisp)
	}
	a.movRegMem64(regR11, regR10, fieldCacheTableOffset)
	a.cmpRegReg(regR8, regR11)
	a.jne(helperLabel)
	a.movRegMem32(regR11, regR9, tableVersionOffset)
	a.movRegMem32(regR12, regR10, fieldCacheVersionOffset)
	a.cmpRegReg(regR11, regR12)
	a.jne(helperLabel)
	a.movRegMem32(regR11, regR10, fieldCacheSlotOffset)
	a.movRegMem64(regR12, regR9, tableFieldsOffset+sliceLenOffset)
	a.cmpRegReg(regR11, regR12)
	a.jae(helperLabel)
	a.movRegMem64(regR13, regR9, tableFieldsOffset+sliceDataOffset)
	a.shlRegImm8(regR11, 3)
	a.addRegReg(regR11, regR13)
	a.movRegMem64(regAX, regR11, 0)
	a.movRegImm64(regR12, uint64(rt.NilValue))
	a.cmpRegReg(regAX, regR12)
	a.je(helperLabel)
	if self {
		a.movRegMem64(regR14, regBX, slotDisp(instr.B))
	}
	a.movMemReg64(regBX, slotDisp(instr.A), regAX)
	if self {
		a.movMemReg64(regBX, slotDisp(instr.A+1), regR14)
	}
	a.jump(doneLabel)
	a.bind(helperLabel)
	kind := jit.HelperGetFieldIC
	if self {
		kind = jit.HelperSelfIC
	}
	helperID, err := meta.AddHelperCall(pc, pc+1, kind, a.pc())
	if err != nil {
		return err
	}
	emitHelperExit(a, helperID, pc+1, a.pc())
	a.bind(doneLabel)
	return nil
}

func emitGuardNumericSlotOrJump(a *amd64Assembler, slot uint16, helperLabel int) {
	numberLabel := a.newLabel()
	a.movRegMem64(regAX, regBX, slotDisp(slot))
	a.movRegReg(regR10, regAX)
	a.movRegImm64(regR11, rt.VexarcValueBoxCheckMask)
	a.andRegReg(regR10, regR11)
	a.movRegImm64(regR11, rt.VexarcValueBoxBase)
	a.cmpRegReg(regR10, regR11)
	a.jne(numberLabel)
	a.movRegReg(regR10, regAX)
	a.shrRegImm8(regR10, byte(rt.VexarcValueTagShift))
	a.movRegImm64(regR11, 0xF)
	a.andRegReg(regR10, regR11)
	a.cmpRegImm32(regR10, 0)
	a.jne(helperLabel)
	a.bind(numberLabel)
}

func emitStoreNumericResultOrNaN(a *amd64Assembler, slot uint16, xmm byte) {
	nanLabel := a.newLabel()
	doneLabel := a.newLabel()
	a.ucomisdXmmXmm(xmm, xmm)
	a.jp(nanLabel)
	a.movsdMemXmm(regBX, slotDisp(slot), xmm)
	a.jump(doneLabel)
	a.bind(nanLabel)
	a.movRegImm64(regAX, uint64(rt.NaNValue))
	a.movMemReg64(regBX, slotDisp(slot), regAX)
	a.bind(doneLabel)
}

func emitLoadTablePointerForSlotOrHelper(a *amd64Assembler, slot uint16, helperLabel int) error {
	a.movRegMem64(regAX, regBX, slotDisp(slot))
	a.movRegReg(regR10, regAX)
	a.movRegImm64(regR11, rt.VexarcValueBoxCheckMask)
	a.andRegReg(regR10, regR11)
	a.movRegImm64(regR11, rt.VexarcValueBoxBase)
	a.cmpRegReg(regR10, regR11)
	a.jne(helperLabel)
	a.movRegReg(regR10, regAX)
	a.shrRegImm8(regR10, byte(rt.VexarcValueTagShift))
	a.movRegImm64(regR11, 0xF)
	a.andRegReg(regR10, regR11)
	a.cmpRegImm32(regR10, int32(rt.VexarcValueHandleTag))
	a.jne(helperLabel)
	a.movRegReg(regR8, regAX)
	a.movRegImm64(regR11, rt.VexarcValuePayloadMask)
	a.andRegReg(regR8, regR11)
	return emitLoadTablePointerForHandleOrHelper(a, regR8, helperLabel)
}

func emitLoadTablePointerForHandleOrHelper(a *amd64Assembler, handleReg byte, helperLabel int) error {
	a.movRegReg(regR10, handleReg)
	a.shrRegImm8(regR10, byte(rt.VexarcHandleKindShift))
	a.cmpRegImm32(regR10, int32(rt.VexarcObjectKindTable))
	a.jne(helperLabel)
	a.movRegMem64(regR9, regDI, threadHeapTablesBaseOffset)
	a.cmpRegImm32(regR9, 0)
	a.je(helperLabel)
	a.movRegMem64(regR11, regDI, threadHeapTablesLenOffset)
	a.movRegReg(regR10, handleReg)
	a.shlRegImm8(regR10, 32)
	a.shrRegImm8(regR10, 32)
	a.cmpRegReg(regR10, regR11)
	a.jae(helperLabel)
	a.shlRegImm8(regR10, 3)
	a.addRegReg(regR10, regR9)
	a.movRegMem64(regR9, regR10, 0)
	return nil
}

func emitGuardHandleKindForSlotOrJump(a *amd64Assembler, slot uint16, kind rt.ObjectKind, helperLabel int) error {
	a.movRegMem64(regAX, regBX, slotDisp(slot))
	a.movRegReg(regR10, regAX)
	a.movRegImm64(regR11, rt.VexarcValueBoxCheckMask)
	a.andRegReg(regR10, regR11)
	a.movRegImm64(regR11, rt.VexarcValueBoxBase)
	a.cmpRegReg(regR10, regR11)
	a.jne(helperLabel)
	a.movRegReg(regR10, regAX)
	a.shrRegImm8(regR10, byte(rt.VexarcValueTagShift))
	a.movRegImm64(regR11, 0xF)
	a.andRegReg(regR10, regR11)
	a.cmpRegImm32(regR10, int32(rt.VexarcValueHandleTag))
	a.jne(helperLabel)
	a.movRegReg(regR10, regAX)
	a.movRegImm64(regR11, rt.VexarcValuePayloadMask)
	a.andRegReg(regR10, regR11)
	a.shrRegImm8(regR10, byte(rt.VexarcHandleKindShift))
	a.cmpRegImm32(regR10, int32(kind))
	a.jne(helperLabel)
	return nil
}

func emitGuardTableMetaNilOrJump(a *amd64Assembler, helperLabel int) {
	a.movRegMem64(regR10, regR9, tableMetaOffset)
	a.movRegImm64(regR11, uint64(rt.NilValue))
	a.cmpRegReg(regR10, regR11)
	a.jne(helperLabel)
}

func emitDecodePositiveIntegerSlotOrJump(a *amd64Assembler, slot uint16, helperLabel int) {
	a.movRegMem64(regCX, regBX, slotDisp(slot))
	a.movRegReg(regR10, regCX)
	a.movRegImm64(regR11, rt.VexarcValueBoxCheckMask)
	a.andRegReg(regR10, regR11)
	a.movRegImm64(regR11, rt.VexarcValueBoxBase)
	a.cmpRegReg(regR10, regR11)
	a.je(helperLabel)
	a.movqXmmReg(xmm0, regCX)
	a.cvttsd2siRegXmm(regR10, xmm0)
	a.cmpRegImm32(regR10, 1)
	a.jb(helperLabel)
	a.cvtsi2sdXmmReg(xmm1, regR10)
	a.ucomisdXmmXmm(xmm0, xmm1)
	a.jp(helperLabel)
	a.jne(helperLabel)
}

func emitJump(meta *jit.CompiledUnitMeta, a *amd64Assembler, pc int, target int) error {
	if meta.ContainsPC(target) {
		a.jump(target)
		return nil
	}
	if err := meta.AddSideExitAt(pc, target, jit.ExitSideExit, a.pc()); err != nil {
		return err
	}
	emitSideExit(a, target, a.pc())
	return nil
}

func emitReturn(a *amd64Assembler, slot uint16) {
	offset := a.pc()
	a.movMemImm32(regDX, exitReasonOffset, int32(jit.ExitReturn))
	a.movMemImm32(regDX, exitCodeOffsetOffset, int32(offset))
	a.movRegMem64(regAX, regBX, slotDisp(slot))
	a.movMemReg64(regDX, exitReturnValOffset, regAX)
	a.ret()
}

func emitReturnMulti(a *amd64Assembler, instr bytecode.Instr) {
	if instr.B == 0 {
		offset := a.pc()
		a.movMemImm32(regDX, exitReasonOffset, int32(jit.ExitReturn))
		a.movMemImm32(regDX, exitCodeOffsetOffset, int32(offset))
		a.movRegImm64(regAX, uint64(rt.NilValue))
		a.movMemReg64(regDX, exitReturnValOffset, regAX)
		a.ret()
		return
	}
	emitReturn(a, instr.A)
}

func emitInterpretExit(a *amd64Assembler, resumePC int, codeOffset uint32) {
	a.movMemImm32(regDX, exitReasonOffset, int32(jit.ExitInterpret))
	a.movMemImm32(regDX, exitResumePCOffset, int32(resumePC))
	a.movMemImm32(regDX, exitCodeOffsetOffset, int32(codeOffset))
	a.ret()
}

func emitSideExit(a *amd64Assembler, resumePC int, codeOffset uint32) {
	a.movMemImm32(regDX, exitReasonOffset, int32(jit.ExitSideExit))
	a.movMemImm32(regDX, exitResumePCOffset, int32(resumePC))
	a.movMemImm32(regDX, exitCodeOffsetOffset, int32(codeOffset))
	a.ret()
}

func emitHelperExit(a *amd64Assembler, helperID uint32, resumePC int, codeOffset uint32) {
	a.movMemImm32(regDX, exitReasonOffset, int32(jit.ExitCallHelper))
	a.movMemImm32(regDX, exitResumePCOffset, int32(resumePC))
	a.movMemImm32(regDX, exitCodeOffsetOffset, int32(codeOffset))
	a.movMemImm32(regDX, exitHelperIDOffset, int32(helperID))
	a.ret()
}

func helperKindForInstr(op bytecode.Op) (jit.HelperKind, bool) {
	switch op {
	case bytecode.OpGetField:
		return jit.HelperGetField, true
	case bytecode.OpSelf:
		return jit.HelperSelf, true
	case bytecode.OpGetTable:
		return jit.HelperGetTable, true
	case bytecode.OpSetTable:
		return jit.HelperSetTable, true
	case bytecode.OpLen:
		return jit.HelperLen, true
	case bytecode.OpAdd:
		return jit.HelperAdd, true
	case bytecode.OpGetFieldIC:
		return jit.HelperGetFieldIC, true
	case bytecode.OpSelfIC:
		return jit.HelperSelfIC, true
	case bytecode.OpGetTableArray:
		return jit.HelperGetTableArray, true
	case bytecode.OpSetTableArray:
		return jit.HelperSetTableArray, true
	case bytecode.OpLenTable:
		return jit.HelperLenTable, true
	case bytecode.OpEqual:
		return jit.HelperEqual, true
	case bytecode.OpLess:
		return jit.HelperLess, true
	case bytecode.OpLessEqual:
		return jit.HelperLessEqual, true
	case bytecode.OpNot:
		return jit.HelperNot, true
	case bytecode.OpUnm:
		return jit.HelperUnm, true
	case bytecode.OpConcat:
		return jit.HelperConcat, true
	case bytecode.OpSub:
		return jit.HelperSub, true
	case bytecode.OpMul:
		return jit.HelperMul, true
	case bytecode.OpDiv:
		return jit.HelperDiv, true
	case bytecode.OpMod:
		return jit.HelperMod, true
	case bytecode.OpPow:
		return jit.HelperPow, true
	case bytecode.OpCall:
		return jit.HelperCall, true
	case bytecode.OpCallMulti:
		return jit.HelperCallMulti, true
	case bytecode.OpTailCall:
		return jit.HelperTailCall, true
	case bytecode.OpAppendTable:
		return jit.HelperAppendTable, true
	case bytecode.OpReturnAppendPending:
		return jit.HelperReturnAppendPending, true
	case bytecode.OpLoadGlobal:
		return jit.HelperLoadGlobal, true
	case bytecode.OpStoreGlobal:
		return jit.HelperStoreGlobal, true
	case bytecode.OpSetField:
		return jit.HelperSetField, true
	case bytecode.OpNewTable:
		return jit.HelperNewTable, true
	case bytecode.OpClosure:
		return jit.HelperClosure, true
	case bytecode.OpLoadUpvalue:
		return jit.HelperLoadUpvalue, true
	case bytecode.OpStoreUpvalue:
		return jit.HelperStoreUpvalue, true
	case bytecode.OpVararg:
		return jit.HelperVararg, true
	case bytecode.OpYield:
		return jit.HelperYield, true
	case bytecode.OpIterPairs:
		return jit.HelperIterPairs, true
	case bytecode.OpIterIPairs:
		return jit.HelperIterIPairs, true
	case bytecode.OpClose:
		return jit.HelperClose, true
	default:
		return 0, false
	}
}

func slotDisp(slot uint16) int32 {
	return int32(slot) * 8
}

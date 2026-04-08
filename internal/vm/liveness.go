package vm

import "vexlua/internal/bytecode"

func analyzeLiveRegisters(proto *bytecode.Proto) [][]uint64 {
	codeLen := 0
	maxStack := 0
	if proto != nil {
		codeLen = len(proto.Code)
		maxStack = proto.MaxStack
	}
	liveIn := make([][]uint64, codeLen+1)
	if codeLen == 0 || maxStack == 0 {
		return liveIn
	}
	liveOut := make([][]uint64, codeLen)
	use := make([][]uint64, codeLen)
	defs := make([][]uint64, codeLen)
	successors := make([][]int, codeLen)
	for pc, instr := range proto.Code {
		liveIn[pc] = newRegisterBitset(maxStack)
		liveOut[pc] = newRegisterBitset(maxStack)
		use[pc], defs[pc] = instructionUseDef(instr, maxStack)
		successors[pc] = instructionSuccessors(proto, pc)
	}
	liveIn[codeLen] = newRegisterBitset(maxStack)
	changed := true
	for changed {
		changed = false
		for pc := codeLen - 1; pc >= 0; pc-- {
			clearRegisterBitset(liveOut[pc])
			for _, succ := range successors[pc] {
				orRegisterBitset(liveOut[pc], liveIn[succ])
			}
			if transferRegisterBitset(liveIn[pc], liveOut[pc], defs[pc], use[pc]) {
				changed = true
			}
		}
	}
	return liveIn
}

func instructionUseDef(instr bytecode.Instr, maxStack int) ([]uint64, []uint64) {
	use := newRegisterBitset(maxStack)
	defs := newRegisterBitset(maxStack)
	switch instr.Op {
	case bytecode.OpLoadConst, bytecode.OpLoadUpvalue, bytecode.OpClosure, bytecode.OpNewTable, bytecode.OpLoadGlobal:
		markRegisterRange(defs, int(instr.A), 1)
	case bytecode.OpMove:
		markRegisterRange(use, int(instr.B), 1)
		markRegisterRange(defs, int(instr.A), 1)
	case bytecode.OpStoreUpvalue, bytecode.OpStoreGlobal:
		markRegisterRange(use, int(instr.A), 1)
	case bytecode.OpGetField, bytecode.OpGetFieldIC, bytecode.OpGetTable, bytecode.OpLen, bytecode.OpNot:
		markRegisterRange(use, int(instr.B), 1)
		markRegisterRange(defs, int(instr.A), 1)
	case bytecode.OpSetField:
		markRegisterRange(use, int(instr.A), 1)
		markRegisterRange(use, int(instr.B), 1)
	case bytecode.OpSetTable:
		markRegisterRange(use, int(instr.A), 1)
		markRegisterRange(use, int(instr.B), 1)
		markRegisterRange(use, int(instr.C), 1)
	case bytecode.OpAppendTable:
		markRegisterRange(use, int(instr.A), 1)
	case bytecode.OpAdd, bytecode.OpAddNum, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpMod, bytecode.OpPow,
		bytecode.OpConcat, bytecode.OpEqual, bytecode.OpLess, bytecode.OpLessEqual:
		markRegisterRange(use, int(instr.B), 1)
		markRegisterRange(use, int(instr.C), 1)
		markRegisterRange(defs, int(instr.A), 1)
	case bytecode.OpAddConst:
		markRegisterRange(use, int(instr.B), 1)
		markRegisterRange(defs, int(instr.A), 1)
	case bytecode.OpIterPairs, bytecode.OpIterIPairs:
		markRegisterRange(use, int(instr.A), 1)
		markRegisterRange(use, int(instr.B), 1)
		markRegisterRange(defs, int(instr.A), int(instr.C))
	case bytecode.OpSelf, bytecode.OpSelfIC:
		markRegisterRange(use, int(instr.B), 1)
		markRegisterRange(defs, int(instr.A), 2)
	case bytecode.OpCall:
		markRegisterRange(use, int(instr.B), 1)
		markRegisterRange(use, int(instr.C), int(instr.D))
		markRegisterRange(defs, int(instr.A), 1)
	case bytecode.OpTailCall:
		argCount, _, _ := bytecode.UnpackCallSpec(instr.D)
		markRegisterRange(use, int(instr.B), 1)
		markRegisterRange(use, int(instr.C), argCount)
	case bytecode.OpCallMulti:
		argCount, resultCount, _ := bytecode.UnpackCallSpec(instr.D)
		markRegisterRange(use, int(instr.B), 1)
		markRegisterRange(use, int(instr.C), argCount)
		if resultCount > 0 {
			markRegisterRange(defs, int(instr.A), resultCount)
		}
	case bytecode.OpVararg:
		count := int(instr.B)
		if count > 0 {
			markRegisterRange(defs, int(instr.A), count)
		}
	case bytecode.OpYield:
		yieldCount, _, _ := bytecode.UnpackCallSpec(instr.D)
		markRegisterRange(use, int(instr.B), yieldCount)
	case bytecode.OpJumpIfFalse, bytecode.OpJumpIfTrue:
		markRegisterRange(use, int(instr.A), 1)
	case bytecode.OpLessEqualJump:
		markRegisterRange(use, int(instr.A), 1)
		markRegisterRange(use, int(instr.B), 1)
	case bytecode.OpReturn:
		markRegisterRange(use, int(instr.A), 1)
	case bytecode.OpReturnMulti, bytecode.OpReturnAppendPending:
		markRegisterRange(use, int(instr.A), int(instr.B))
	}
	return use, defs
}

func instructionSuccessors(proto *bytecode.Proto, pc int) []int {
	if proto == nil || pc < 0 || pc >= len(proto.Code) {
		return nil
	}
	instr := proto.Code[pc]
	next := pc + 1
	target := int(instr.D)
	switch instr.Op {
	case bytecode.OpJump:
		return validSuccessors(len(proto.Code), target)
	case bytecode.OpJumpIfFalse, bytecode.OpJumpIfTrue, bytecode.OpLessEqualJump:
		return validSuccessors(len(proto.Code), next, target)
	case bytecode.OpTailCall, bytecode.OpYield, bytecode.OpReturn, bytecode.OpReturnMulti, bytecode.OpReturnAppendPending:
		return nil
	default:
		return validSuccessors(len(proto.Code), next)
	}
}

func validSuccessors(codeLen int, indexes ...int) []int {
	result := make([]int, 0, len(indexes))
	for _, index := range indexes {
		if index < 0 || index > codeLen {
			continue
		}
		duplicate := false
		for _, existing := range result {
			if existing == index {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, index)
		}
	}
	return result
}

func newRegisterBitset(maxStack int) []uint64 {
	if maxStack <= 0 {
		return nil
	}
	return make([]uint64, (maxStack+63)/64)
}

func clearRegisterBitset(bits []uint64) {
	for i := range bits {
		bits[i] = 0
	}
}

func orRegisterBitset(dst []uint64, src []uint64) {
	for i := range dst {
		dst[i] |= src[i]
	}
}

func transferRegisterBitset(dst []uint64, out []uint64, defs []uint64, use []uint64) bool {
	changed := false
	for i := range dst {
		next := (out[i] &^ defs[i]) | use[i]
		if dst[i] != next {
			dst[i] = next
			changed = true
		}
	}
	return changed
}

func markRegisterRange(bits []uint64, start int, count int) {
	if start < 0 || count <= 0 {
		return
	}
	for reg := start; reg < start+count; reg++ {
		word := reg / 64
		if word >= len(bits) {
			return
		}
		bits[word] |= uint64(1) << (reg % 64)
	}
}

func liveRegisterContains(bits []uint64, reg int) bool {
	if reg < 0 {
		return false
	}
	word := reg / 64
	if word >= len(bits) {
		return false
	}
	return bits[word]&(uint64(1)<<(reg%64)) != 0
}

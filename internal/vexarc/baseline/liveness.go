package baseline

import (
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/vexarc/metadata"
)

type slotRange struct {
	start int
	end   int
}

type liveRegisterSet struct {
	words           []uint64
	dynamicTopStart int
}

type livenessTransfer struct {
	uses            []int
	useRanges       []slotRange
	killRanges      []slotRange
	dynamicUseStart int
}

func analyzeLiveSlotSets(proto *bytecode.Proto) ([]metadata.LiveSlotSet, error) {
	slotCount := livenessSlotCount(proto)
	liveIn := make([]liveRegisterSet, len(proto.Code)+1)
	for index := range liveIn {
		liveIn[index] = newLiveRegisterSet(slotCount)
	}
	changed := true
	for changed {
		changed = false
		for pc := len(proto.Code) - 1; pc >= 0; pc-- {
			next, err := transferLiveRegisters(proto, pc, liveIn, slotCount)
			if err != nil {
				return nil, err
			}
			if !liveIn[pc].equal(next) {
				liveIn[pc] = next
				changed = true
			}
		}
	}
	sets := make([]metadata.LiveSlotSet, len(liveIn))
	for pc, set := range liveIn {
		sets[pc] = set.toMetadata(slotCount)
	}
	return sets, nil
}

func transferLiveRegisters(proto *bytecode.Proto, pc int, liveIn []liveRegisterSet, slotCount int) (liveRegisterSet, error) {
	out := newLiveRegisterSet(slotCount)
	successors, err := instructionSuccessors(proto, pc)
	if err != nil {
		return liveRegisterSet{}, err
	}
	for _, successor := range successors {
		if successor < 0 || successor >= len(liveIn) {
			return liveRegisterSet{}, fmt.Errorf("liveness successor %d is out of range at pc %d", successor, pc)
		}
		out.unionFrom(liveIn[successor])
	}
	transfer, err := instructionTransfer(proto, pc)
	if err != nil {
		return liveRegisterSet{}, err
	}
	in := out.clone()
	for _, kill := range transfer.killRanges {
		in.removeRange(kill.start, kill.end)
	}
	for _, use := range transfer.uses {
		in.add(use)
	}
	for _, useRange := range transfer.useRanges {
		in.addRange(useRange.start, useRange.end)
	}
	if transfer.dynamicUseStart >= 0 {
		in.setDynamicTopStart(transfer.dynamicUseStart)
	}
	return in, nil
}

func instructionSuccessors(proto *bytecode.Proto, pc int) ([]int, error) {
	if pc < 0 || pc >= len(proto.Code) {
		return nil, fmt.Errorf("liveness pc %d is out of range", pc)
	}
	instruction := proto.Code[pc]
	switch instruction.Opcode() {
	case bytecode.OP_RETURN, bytecode.OP_TAILCALL:
		return nil, nil
	case bytecode.OP_JMP, bytecode.OP_FORPREP:
		return []int{pc + 1 + instruction.SBx()}, nil
	case bytecode.OP_FORLOOP:
		return []int{pc + 1 + instruction.SBx(), pc + 1}, nil
	case bytecode.OP_LOADBOOL:
		if instruction.C() != 0 {
			return []int{pc + 2}, nil
		}
		return []int{pc + 1}, nil
	case bytecode.OP_EQ, bytecode.OP_LT, bytecode.OP_LE, bytecode.OP_TEST, bytecode.OP_TESTSET, bytecode.OP_TFORLOOP:
		if pc+1 >= len(proto.Code) {
			return nil, fmt.Errorf("%s is missing trailing JMP at pc %d", instruction.Opcode(), pc)
		}
		jump := proto.Code[pc+1]
		if jump.Opcode() != bytecode.OP_JMP {
			return nil, fmt.Errorf("%s expects trailing JMP at pc %d, got %s", instruction.Opcode(), pc, jump.Opcode())
		}
		return []int{pc + 2 + jump.SBx(), pc + 2}, nil
	case bytecode.OP_SETLIST:
		if instruction.C() == 0 {
			return []int{pc + 2}, nil
		}
		return []int{pc + 1}, nil
	case bytecode.OP_CLOSURE:
		childIndex := instruction.Bx()
		if childIndex < 0 || childIndex >= len(proto.Protos) {
			return nil, fmt.Errorf("CLOSURE child proto %d is out of range at pc %d", childIndex, pc)
		}
		return []int{pc + 1 + int(proto.Protos[childIndex].NumUpvalues)}, nil
	default:
		return []int{pc + 1}, nil
	}
}

func instructionTransfer(proto *bytecode.Proto, pc int) (livenessTransfer, error) {
	instruction := proto.Code[pc]
	transfer := livenessTransfer{dynamicUseStart: -1}
	addRKUse := func(operand int) {
		if !bytecode.IsConstantRK(operand) {
			transfer.uses = append(transfer.uses, operand)
		}
	}
	switch instruction.Opcode() {
	case bytecode.OP_MOVE:
		transfer.uses = append(transfer.uses, instruction.B())
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A()})
	case bytecode.OP_LOADK, bytecode.OP_LOADBOOL, bytecode.OP_NEWTABLE, bytecode.OP_GETUPVAL, bytecode.OP_GETGLOBAL, bytecode.OP_VARARG:
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A()})
		if instruction.Opcode() == bytecode.OP_VARARG {
			if instruction.B() > 1 {
				transfer.killRanges[0] = slotRange{start: instruction.A(), end: instruction.A() + instruction.B() - 2}
			} else if instruction.B() == 0 {
				transfer.killRanges = nil
			}
		}
	case bytecode.OP_LOADNIL:
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.B()})
	case bytecode.OP_GETTABLE:
		transfer.uses = append(transfer.uses, instruction.B())
		addRKUse(instruction.C())
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A()})
	case bytecode.OP_SETGLOBAL, bytecode.OP_SETUPVAL:
		transfer.uses = append(transfer.uses, instruction.A())
	case bytecode.OP_SETTABLE:
		transfer.uses = append(transfer.uses, instruction.A())
		addRKUse(instruction.B())
		addRKUse(instruction.C())
	case bytecode.OP_SELF:
		transfer.uses = append(transfer.uses, instruction.B())
		addRKUse(instruction.C())
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A() + 1})
	case bytecode.OP_ADD, bytecode.OP_SUB, bytecode.OP_MUL, bytecode.OP_DIV, bytecode.OP_MOD, bytecode.OP_POW:
		addRKUse(instruction.B())
		addRKUse(instruction.C())
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A()})
	case bytecode.OP_UNM, bytecode.OP_NOT, bytecode.OP_LEN:
		transfer.uses = append(transfer.uses, instruction.B())
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A()})
	case bytecode.OP_CONCAT:
		transfer.useRanges = append(transfer.useRanges, slotRange{start: instruction.B(), end: instruction.C()})
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A()})
	case bytecode.OP_EQ, bytecode.OP_LT, bytecode.OP_LE:
		addRKUse(instruction.B())
		addRKUse(instruction.C())
	case bytecode.OP_TEST:
		transfer.uses = append(transfer.uses, instruction.A())
	case bytecode.OP_TESTSET:
		transfer.uses = append(transfer.uses, instruction.B())
	case bytecode.OP_CALL:
		transfer.uses = append(transfer.uses, instruction.A())
		if instruction.B() == 0 {
			transfer.dynamicUseStart = instruction.A() + 1
		} else if instruction.B() > 1 {
			transfer.useRanges = append(transfer.useRanges, slotRange{start: instruction.A() + 1, end: instruction.A() + instruction.B() - 1})
		}
		if instruction.C() > 1 {
			transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A() + instruction.C() - 2})
		}
	case bytecode.OP_TAILCALL:
		transfer.uses = append(transfer.uses, instruction.A())
		if instruction.B() == 0 {
			transfer.dynamicUseStart = instruction.A() + 1
		} else if instruction.B() > 1 {
			transfer.useRanges = append(transfer.useRanges, slotRange{start: instruction.A() + 1, end: instruction.A() + instruction.B() - 1})
		}
	case bytecode.OP_RETURN:
		if instruction.B() == 0 {
			transfer.dynamicUseStart = instruction.A()
		} else if instruction.B() > 1 {
			transfer.useRanges = append(transfer.useRanges, slotRange{start: instruction.A(), end: instruction.A() + instruction.B() - 2})
		}
	case bytecode.OP_SETLIST:
		transfer.uses = append(transfer.uses, instruction.A())
		if instruction.B() == 0 {
			transfer.dynamicUseStart = instruction.A() + 1
		} else if instruction.B() > 0 {
			transfer.useRanges = append(transfer.useRanges, slotRange{start: instruction.A() + 1, end: instruction.A() + instruction.B()})
		}
	case bytecode.OP_FORPREP:
		transfer.useRanges = append(transfer.useRanges, slotRange{start: instruction.A(), end: instruction.A() + 2})
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A() + 2})
	case bytecode.OP_FORLOOP:
		transfer.useRanges = append(transfer.useRanges, slotRange{start: instruction.A(), end: instruction.A() + 2})
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A()})
	case bytecode.OP_TFORLOOP:
		transfer.useRanges = append(transfer.useRanges, slotRange{start: instruction.A(), end: instruction.A() + 2})
	case bytecode.OP_CLOSURE:
		childIndex := instruction.Bx()
		if childIndex < 0 || childIndex >= len(proto.Protos) {
			return livenessTransfer{}, fmt.Errorf("CLOSURE child proto %d is out of range at pc %d", childIndex, pc)
		}
		childProto := proto.Protos[childIndex]
		for capturePC := pc + 1; capturePC < pc+1+int(childProto.NumUpvalues); capturePC++ {
			if capturePC >= len(proto.Code) {
				return livenessTransfer{}, fmt.Errorf("CLOSURE capture payload overruns proto at pc %d", pc)
			}
			capture := proto.Code[capturePC]
			if capture.Opcode() == bytecode.OP_MOVE {
				transfer.uses = append(transfer.uses, capture.B())
			}
		}
		transfer.killRanges = append(transfer.killRanges, slotRange{start: instruction.A(), end: instruction.A()})
	}
	return transfer, nil
}

func livenessSlotCount(proto *bytecode.Proto) int {
	if proto.MaxStackSize == 0 {
		return 1
	}
	return int(proto.MaxStackSize)
}

func newLiveRegisterSet(slotCount int) liveRegisterSet {
	if slotCount < 1 {
		slotCount = 1
	}
	return liveRegisterSet{
		words:           make([]uint64, (slotCount+63)/64),
		dynamicTopStart: -1,
	}
}

func (set liveRegisterSet) clone() liveRegisterSet {
	return liveRegisterSet{
		words:           append([]uint64(nil), set.words...),
		dynamicTopStart: set.dynamicTopStart,
	}
}

func (set *liveRegisterSet) add(slot int) {
	if slot < 0 {
		return
	}
	word := slot / 64
	bit := uint(slot % 64)
	if word >= len(set.words) {
		grown := make([]uint64, word+1)
		copy(grown, set.words)
		set.words = grown
	}
	set.words[word] |= uint64(1) << bit
}

func (set *liveRegisterSet) addRange(start int, end int) {
	if start < 0 || end < start {
		return
	}
	for slot := start; slot <= end; slot++ {
		set.add(slot)
	}
}

func (set *liveRegisterSet) removeRange(start int, end int) {
	if start < 0 || end < start {
		return
	}
	for slot := start; slot <= end; slot++ {
		word := slot / 64
		if word >= len(set.words) {
			return
		}
		bit := uint(slot % 64)
		set.words[word] &^= uint64(1) << bit
	}
}

func (set *liveRegisterSet) setDynamicTopStart(start int) {
	if start < 0 {
		return
	}
	if set.dynamicTopStart < 0 || start < set.dynamicTopStart {
		set.dynamicTopStart = start
	}
}

func (set *liveRegisterSet) unionFrom(other liveRegisterSet) {
	if len(other.words) > len(set.words) {
		grown := make([]uint64, len(other.words))
		copy(grown, set.words)
		set.words = grown
	}
	for index, word := range other.words {
		set.words[index] |= word
	}
	if other.dynamicTopStart >= 0 {
		set.setDynamicTopStart(other.dynamicTopStart)
	}
}

func (set liveRegisterSet) equal(other liveRegisterSet) bool {
	if set.dynamicTopStart != other.dynamicTopStart {
		return false
	}
	if len(set.words) != len(other.words) {
		return false
	}
	for index, word := range set.words {
		if word != other.words[index] {
			return false
		}
	}
	return true
}

func (set liveRegisterSet) toMetadata(slotCount int) metadata.LiveSlotSet {
	out := metadata.NewLiveSlotSet(slotCount)
	copy(out.RegisterWords, set.words)
	if set.dynamicTopStart >= 0 {
		out.SetDynamicTopStart(set.dynamicTopStart)
	}
	return out
}

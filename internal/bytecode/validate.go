package bytecode

import "fmt"

type ValidationError struct {
	PC     int
	Opcode Opcode
	Reason string
}

func (err *ValidationError) Error() string {
	if err == nil {
		return ""
	}
	if err.PC >= 0 {
		return fmt.Sprintf("pc %d (%s): %s", err.PC, err.Opcode, err.Reason)
	}
	return err.Reason
}

func ValidateProto(proto *Proto) error {
	if proto == nil {
		return &ValidationError{PC: -1, Reason: "nil proto"}
	}
	return validateProto(proto)
}

func validateProto(proto *Proto) error {
	if len(proto.LineInfo) > 0 && len(proto.LineInfo) != len(proto.Code) {
		return &ValidationError{PC: -1, Reason: "line info size does not match code size"}
	}
	if len(proto.UpvalueNames) > 0 && len(proto.UpvalueNames) != int(proto.NumUpvalues) {
		return &ValidationError{PC: -1, Reason: "upvalue name count does not match num upvalues"}
	}

	maxStack := int(proto.MaxStackSize)
	for pc, in := range proto.Code {
		op := in.Opcode()
		if !op.Valid() {
			return &ValidationError{PC: pc, Opcode: op, Reason: "invalid opcode"}
		}
		if err := validateInstruction(proto, pc, in, maxStack); err != nil {
			return err
		}
	}

	for _, child := range proto.Protos {
		if err := validateProto(child); err != nil {
			return err
		}
	}

	return nil
}

func validateInstruction(proto *Proto, pc int, in Instruction, maxStack int) error {
	op := in.Opcode()
	a := in.A()
	b := in.B()
	c := in.C()

	checkReg := func(reg int, name string) error {
		if reg < 0 || reg >= maxStack {
			return &ValidationError{PC: pc, Opcode: op, Reason: fmt.Sprintf("%s register out of range: %d", name, reg)}
		}
		return nil
	}

	checkRK := func(operand int, name string) error {
		if IsConstantRK(operand) {
			idx := IndexK(operand)
			if idx < 0 || idx >= len(proto.Constants) {
				return &ValidationError{PC: pc, Opcode: op, Reason: fmt.Sprintf("%s constant out of range: %d", name, idx)}
			}
			return nil
		}
		return checkReg(operand, name)
	}

	checkJump := func(sbx int) error {
		target := pc + 1 + sbx
		if target < 0 || target >= len(proto.Code) {
			return &ValidationError{PC: pc, Opcode: op, Reason: fmt.Sprintf("jump target out of range: %d", target)}
		}
		return nil
	}

	if op.Info().SetsA {
		if err := checkReg(a, "A"); err != nil {
			return err
		}
	}

	switch op {
	case OP_MOVE:
		return checkReg(b, "B")
	case OP_LOADK:
		if in.Bx() >= len(proto.Constants) {
			return &ValidationError{PC: pc, Opcode: op, Reason: fmt.Sprintf("constant out of range: %d", in.Bx())}
		}
	case OP_LOADBOOL:
		return nil
	case OP_LOADNIL:
		if a > b {
			return &ValidationError{PC: pc, Opcode: op, Reason: "A register exceeds B register"}
		}
		return checkReg(b, "B")
	case OP_GETUPVAL, OP_SETUPVAL:
		if b >= int(proto.NumUpvalues) {
			return &ValidationError{PC: pc, Opcode: op, Reason: fmt.Sprintf("upvalue out of range: %d", b)}
		}
	case OP_GETGLOBAL, OP_SETGLOBAL:
		idx := in.Bx()
		if idx >= len(proto.Constants) {
			return &ValidationError{PC: pc, Opcode: op, Reason: fmt.Sprintf("constant out of range: %d", idx)}
		}
		if proto.Constants[idx].Kind != ConstantString {
			return &ValidationError{PC: pc, Opcode: op, Reason: "global name must be a string constant"}
		}
	case OP_GETTABLE:
		if err := checkReg(b, "B"); err != nil {
			return err
		}
		return checkRK(c, "C")
	case OP_SETTABLE:
		if err := checkReg(a, "A"); err != nil {
			return err
		}
		if err := checkRK(b, "B"); err != nil {
			return err
		}
		return checkRK(c, "C")
	case OP_NEWTABLE:
		return nil
	case OP_SELF:
		if err := checkReg(a+1, "A+1"); err != nil {
			return err
		}
		if err := checkReg(b, "B"); err != nil {
			return err
		}
		return checkRK(c, "C")
	case OP_ADD, OP_SUB, OP_MUL, OP_DIV, OP_MOD, OP_POW, OP_EQ, OP_LT, OP_LE:
		if err := checkRK(b, "B"); err != nil {
			return err
		}
		return checkRK(c, "C")
	case OP_UNM, OP_NOT, OP_LEN:
		return checkReg(b, "B")
	case OP_CONCAT:
		if b > c {
			return &ValidationError{PC: pc, Opcode: op, Reason: "B register exceeds C register"}
		}
		if err := checkReg(b, "B"); err != nil {
			return err
		}
		return checkReg(c, "C")
	case OP_JMP:
		return checkJump(in.SBx())
	case OP_TEST:
		if c > 1 {
			return &ValidationError{PC: pc, Opcode: op, Reason: "C flag must be 0 or 1"}
		}
	case OP_TESTSET:
		if err := checkReg(b, "B"); err != nil {
			return err
		}
		if c > 1 {
			return &ValidationError{PC: pc, Opcode: op, Reason: "C flag must be 0 or 1"}
		}
	case OP_CALL, OP_TAILCALL:
		if b > 0 {
			if err := checkReg(a+b-1, "A+B-1"); err != nil {
				return err
			}
		}
		if c > 1 {
			if err := checkReg(a+c-2, "A+C-2"); err != nil {
				return err
			}
		}
	case OP_RETURN:
		if err := checkReg(a, "A"); err != nil {
			return err
		}
		if b > 1 {
			return checkReg(a+b-2, "A+B-2")
		}
	case OP_FORLOOP, OP_FORPREP:
		if err := checkReg(a+3, "A+3"); err != nil {
			return err
		}
		return checkJump(in.SBx())
	case OP_TFORLOOP:
		if err := checkReg(a+2+c, "A+2+C"); err != nil {
			return err
		}
	case OP_SETLIST:
		if b > 0 {
			if err := checkReg(a+b, "A+B"); err != nil {
				return err
			}
		}
		if c == 0 && pc+1 >= len(proto.Code) {
			return &ValidationError{PC: pc, Opcode: op, Reason: "SETLIST expects trailing extra argument instruction"}
		}
	case OP_CLOSE:
		return checkReg(a, "A")
	case OP_CLOSURE:
		if in.Bx() >= len(proto.Protos) {
			return &ValidationError{PC: pc, Opcode: op, Reason: fmt.Sprintf("child proto out of range: %d", in.Bx())}
		}
	case OP_VARARG:
		if b > 0 {
			return checkReg(a+b-1, "A+B-1")
		}
	}

	return nil
}

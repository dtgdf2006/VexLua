package bytecode

import "fmt"

type Instruction uint32

const (
	SizeC  = 9
	SizeB  = 9
	SizeBx = SizeC + SizeB
	SizeA  = 8
	SizeOp = 6

	PosOp = 0
	PosA  = PosOp + SizeOp
	PosC  = PosA + SizeA
	PosB  = PosC + SizeC
	PosBx = PosC

	MaxArgA   = (1 << SizeA) - 1
	MaxArgB   = (1 << SizeB) - 1
	MaxArgC   = (1 << SizeC) - 1
	MaxArgBx  = (1 << SizeBx) - 1
	MaxArgSBx = MaxArgBx >> 1

	BitRK      = 1 << (SizeB - 1)
	MaxIndexRK = BitRK - 1
	NoReg      = MaxArgA
)

func mask1(bits, pos uint) Instruction {
	return ((1 << bits) - 1) << pos
}

func field(in Instruction, pos, bits uint) int {
	return int((in >> pos) & ((1 << bits) - 1))
}

func (in Instruction) Opcode() Opcode {
	return Opcode(field(in, PosOp, SizeOp))
}

func (in Instruction) A() int {
	return field(in, PosA, SizeA)
}

func (in Instruction) B() int {
	return field(in, PosB, SizeB)
}

func (in Instruction) C() int {
	return field(in, PosC, SizeC)
}

func (in Instruction) Bx() int {
	return field(in, PosBx, SizeBx)
}

func (in Instruction) SBx() int {
	return in.Bx() - MaxArgSBx
}

func (in Instruction) String() string {
	return FormatInstruction(in)
}

func IsConstantRK(operand int) bool {
	return operand&BitRK != 0
}

func IndexK(operand int) int {
	return operand &^ BitRK
}

func RKAsk(index int) int {
	assertRange("rk index", index, MaxIndexRK)
	return index | BitRK
}

func CreateABC(op Opcode, a, b, c int) Instruction {
	assertOpcode(op)
	assertRange("A", a, MaxArgA)
	assertRange("B", b, MaxArgB)
	assertRange("C", c, MaxArgC)
	return Instruction(op)<<PosOp |
		Instruction(a)<<PosA |
		Instruction(b)<<PosB |
		Instruction(c)<<PosC
}

func CreateABx(op Opcode, a, bx int) Instruction {
	assertOpcode(op)
	assertRange("A", a, MaxArgA)
	assertRange("Bx", bx, MaxArgBx)
	return Instruction(op)<<PosOp |
		Instruction(a)<<PosA |
		Instruction(bx)<<PosBx
}

func CreateAsBx(op Opcode, a, sbx int) Instruction {
	assertRange("sBx", sbx+MaxArgSBx, MaxArgBx)
	return CreateABx(op, a, sbx+MaxArgSBx)
}

func SetOpcode(in Instruction, op Opcode) Instruction {
	assertOpcode(op)
	return (in &^ mask1(SizeOp, PosOp)) | (Instruction(op) << PosOp)
}

func SetA(in Instruction, a int) Instruction {
	assertRange("A", a, MaxArgA)
	return (in &^ mask1(SizeA, PosA)) | (Instruction(a) << PosA)
}

func SetB(in Instruction, b int) Instruction {
	assertRange("B", b, MaxArgB)
	return (in &^ mask1(SizeB, PosB)) | (Instruction(b) << PosB)
}

func SetC(in Instruction, c int) Instruction {
	assertRange("C", c, MaxArgC)
	return (in &^ mask1(SizeC, PosC)) | (Instruction(c) << PosC)
}

func SetBx(in Instruction, bx int) Instruction {
	assertRange("Bx", bx, MaxArgBx)
	return (in &^ mask1(SizeBx, PosBx)) | (Instruction(bx) << PosBx)
}

func SetSBx(in Instruction, sbx int) Instruction {
	assertRange("sBx", sbx+MaxArgSBx, MaxArgBx)
	return SetBx(in, sbx+MaxArgSBx)
}

func assertRange(name string, value, max int) {
	if value < 0 || value > max {
		panic(fmt.Sprintf("%s out of range: %d (max %d)", name, value, max))
	}
}

func assertOpcode(op Opcode) {
	if !op.Valid() {
		panic(fmt.Sprintf("invalid opcode: %d", op))
	}
}

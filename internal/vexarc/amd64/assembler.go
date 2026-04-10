package amd64

import (
	"encoding/binary"
	"fmt"
)

type Condition byte

const (
	CondOverflow     Condition = 0x0
	CondNoOverflow   Condition = 0x1
	CondBelow        Condition = 0x2
	CondAboveEqual   Condition = 0x3
	CondEqual        Condition = 0x4
	CondNotEqual     Condition = 0x5
	CondBelowEqual   Condition = 0x6
	CondAbove        Condition = 0x7
	CondSign         Condition = 0x8
	CondNotSign      Condition = 0x9
	CondParity       Condition = 0xA
	CondNotParity    Condition = 0xB
	CondLess         Condition = 0xC
	CondGreaterEqual Condition = 0xD
	CondLessEqual    Condition = 0xE
	CondGreater      Condition = 0xF
)

type Label struct {
	bound   bool
	pos     int
	patches []int
}

type CodeBuffer struct {
	bytes []byte
}

func NewCodeBuffer(capacity int) *CodeBuffer {
	return &CodeBuffer{bytes: make([]byte, 0, capacity)}
}

func (buffer *CodeBuffer) Pos() int {
	return len(buffer.bytes)
}

func (buffer *CodeBuffer) Bytes() []byte {
	return append([]byte(nil), buffer.bytes...)
}

func (buffer *CodeBuffer) emitByte(value byte) {
	buffer.bytes = append(buffer.bytes, value)
}

func (buffer *CodeBuffer) emitUint32(value uint32) {
	var temp [4]byte
	binary.LittleEndian.PutUint32(temp[:], value)
	buffer.bytes = append(buffer.bytes, temp[:]...)
}

func (buffer *CodeBuffer) emitInt32(value int32) {
	buffer.emitUint32(uint32(value))
}

func (buffer *CodeBuffer) emitUint64(value uint64) {
	var temp [8]byte
	binary.LittleEndian.PutUint64(temp[:], value)
	buffer.bytes = append(buffer.bytes, temp[:]...)
}

type Assembler struct {
	buffer *CodeBuffer
}

func NewAssembler(capacity int) *Assembler {
	return &Assembler{buffer: NewCodeBuffer(capacity)}
}

func (assembler *Assembler) Buffer() *CodeBuffer {
	return assembler.buffer
}

func (assembler *Assembler) NewLabel() *Label {
	return &Label{}
}

func (assembler *Assembler) Bind(label *Label) error {
	if label == nil {
		return fmt.Errorf("label cannot be nil")
	}
	if label.bound {
		return fmt.Errorf("label already bound")
	}
	label.bound = true
	label.pos = assembler.buffer.Pos()
	for _, patch := range label.patches {
		rel := int32(label.pos - (patch + 4))
		binary.LittleEndian.PutUint32(assembler.buffer.bytes[patch:patch+4], uint32(rel))
	}
	return nil
}

func (assembler *Assembler) Jmp(label *Label) {
	assembler.buffer.emitByte(0xE9)
	assembler.emitLabelPatch(label)
}

func (assembler *Assembler) Jcc(condition Condition, label *Label) {
	assembler.buffer.emitByte(0x0F)
	assembler.buffer.emitByte(0x80 | byte(condition))
	assembler.emitLabelPatch(label)
}

func (assembler *Assembler) CallReg(target Register) {
	assembler.emitRex(false, 0, 0, hiBit(target))
	assembler.buffer.emitByte(0xFF)
	assembler.emitModRM(0b11, 2, lowBits(target))
}

func (assembler *Assembler) JmpReg(target Register) {
	assembler.emitRex(false, 0, 0, hiBit(target))
	assembler.buffer.emitByte(0xFF)
	assembler.emitModRM(0b11, 4, lowBits(target))
}

func (assembler *Assembler) Ret() {
	assembler.buffer.emitByte(0xC3)
}

func (assembler *Assembler) MoveRegImm64(dst Register, value uint64) {
	assembler.emitRex(true, 0, 0, hiBit(dst))
	assembler.buffer.emitByte(0xB8 + lowBits(dst))
	assembler.buffer.emitUint64(value)
}

func (assembler *Assembler) MoveRegImm32(dst Register, value uint32) {
	assembler.emitRex(false, 0, 0, hiBit(dst))
	assembler.buffer.emitByte(0xB8 + lowBits(dst))
	assembler.buffer.emitUint32(value)
}

func (assembler *Assembler) MoveRegReg(dst Register, src Register) {
	assembler.emitRex(true, hiBit(dst), 0, hiBit(src))
	assembler.buffer.emitByte(0x8B)
	assembler.emitModRM(0b11, lowBits(dst), lowBits(src))
}

func (assembler *Assembler) MoveRegMem64(dst Register, base Register, disp int32) {
	assembler.emitRex(true, hiBit(dst), 0, hiBit(base))
	assembler.buffer.emitByte(0x8B)
	assembler.emitMemory(lowBits(dst), base, disp)
}

func (assembler *Assembler) MoveRegMem32(dst Register, base Register, disp int32) {
	assembler.emitRex(false, hiBit(dst), 0, hiBit(base))
	assembler.buffer.emitByte(0x8B)
	assembler.emitMemory(lowBits(dst), base, disp)
}

func (assembler *Assembler) MoveMemReg64(base Register, disp int32, src Register) {
	assembler.emitRex(true, hiBit(src), 0, hiBit(base))
	assembler.buffer.emitByte(0x89)
	assembler.emitMemory(lowBits(src), base, disp)
}

func (assembler *Assembler) MoveMemReg32(base Register, disp int32, src Register) {
	assembler.emitRex(false, hiBit(src), 0, hiBit(base))
	assembler.buffer.emitByte(0x89)
	assembler.emitMemory(lowBits(src), base, disp)
}

func (assembler *Assembler) MoveMemImm32(base Register, disp int32, value uint32) {
	assembler.emitRex(false, 0, 0, hiBit(base))
	assembler.buffer.emitByte(0xC7)
	assembler.emitMemory(0, base, disp)
	assembler.buffer.emitUint32(value)
}

func (assembler *Assembler) XorRegReg(dst Register, src Register) {
	assembler.emitRex(false, hiBit(src), 0, hiBit(dst))
	assembler.buffer.emitByte(0x31)
	assembler.emitModRM(0b11, lowBits(src), lowBits(dst))
}

func (assembler *Assembler) AddRegImm32(dst Register, value int32) {
	assembler.emitRex(true, 0, 0, hiBit(dst))
	assembler.buffer.emitByte(0x81)
	assembler.emitModRM(0b11, 0, lowBits(dst))
	assembler.buffer.emitInt32(value)
}

func (assembler *Assembler) OrRegImm32(dst Register, value uint32) {
	assembler.emitRex(true, 0, 0, hiBit(dst))
	assembler.buffer.emitByte(0x81)
	assembler.emitModRM(0b11, 1, lowBits(dst))
	assembler.buffer.emitUint32(value)
}

func (assembler *Assembler) AddRegReg(dst Register, src Register) {
	assembler.emitRex(true, hiBit(dst), 0, hiBit(src))
	assembler.buffer.emitByte(0x03)
	assembler.emitModRM(0b11, lowBits(dst), lowBits(src))
}

func (assembler *Assembler) AndRegImm32(dst Register, value uint32) {
	assembler.emitRex(true, 0, 0, hiBit(dst))
	assembler.buffer.emitByte(0x81)
	assembler.emitModRM(0b11, 4, lowBits(dst))
	assembler.buffer.emitUint32(value)
}

func (assembler *Assembler) CmpRegImm32(dst Register, value uint32) {
	assembler.emitRex(true, 0, 0, hiBit(dst))
	assembler.buffer.emitByte(0x81)
	assembler.emitModRM(0b11, 7, lowBits(dst))
	assembler.buffer.emitUint32(value)
}

func (assembler *Assembler) CmpRegReg(left Register, right Register) {
	assembler.emitRex(true, hiBit(left), 0, hiBit(right))
	assembler.buffer.emitByte(0x3B)
	assembler.emitModRM(0b11, lowBits(left), lowBits(right))
}

func (assembler *Assembler) ShiftLeftRegImm8(dst Register, value byte) {
	assembler.emitRex(true, 0, 0, hiBit(dst))
	assembler.buffer.emitByte(0xC1)
	assembler.emitModRM(0b11, 4, lowBits(dst))
	assembler.buffer.emitByte(value)
}

func (assembler *Assembler) ShiftRightRegImm8(dst Register, value byte) {
	assembler.emitRex(true, 0, 0, hiBit(dst))
	assembler.buffer.emitByte(0xC1)
	assembler.emitModRM(0b11, 5, lowBits(dst))
	assembler.buffer.emitByte(value)
}

func (assembler *Assembler) MoveXmmMem64(dst XMMRegister, base Register, disp int32) {
	assembler.buffer.emitByte(0xF2)
	assembler.emitRex(false, hiBitXmm(dst), 0, hiBit(base))
	assembler.buffer.emitByte(0x0F)
	assembler.buffer.emitByte(0x10)
	assembler.emitMemory(lowBitsXmm(dst), base, disp)
}

func (assembler *Assembler) MoveMemXmm64(base Register, disp int32, src XMMRegister) {
	assembler.buffer.emitByte(0xF2)
	assembler.emitRex(false, hiBitXmm(src), 0, hiBit(base))
	assembler.buffer.emitByte(0x0F)
	assembler.buffer.emitByte(0x11)
	assembler.emitMemory(lowBitsXmm(src), base, disp)
}

func (assembler *Assembler) AddsdXmmXmm(dst XMMRegister, src XMMRegister) {
	assembler.buffer.emitByte(0xF2)
	assembler.emitRex(false, hiBitXmm(dst), 0, hiBitXmm(src))
	assembler.buffer.emitByte(0x0F)
	assembler.buffer.emitByte(0x58)
	assembler.emitModRM(0b11, lowBitsXmm(dst), lowBitsXmm(src))
}

func (assembler *Assembler) SubsdXmmXmm(dst XMMRegister, src XMMRegister) {
	assembler.buffer.emitByte(0xF2)
	assembler.emitRex(false, hiBitXmm(dst), 0, hiBitXmm(src))
	assembler.buffer.emitByte(0x0F)
	assembler.buffer.emitByte(0x5C)
	assembler.emitModRM(0b11, lowBitsXmm(dst), lowBitsXmm(src))
}

func (assembler *Assembler) UcomisdXmmXmm(left XMMRegister, right XMMRegister) {
	assembler.buffer.emitByte(0x66)
	assembler.emitRex(false, hiBitXmm(left), 0, hiBitXmm(right))
	assembler.buffer.emitByte(0x0F)
	assembler.buffer.emitByte(0x2E)
	assembler.emitModRM(0b11, lowBitsXmm(left), lowBitsXmm(right))
}

func (assembler *Assembler) XorpsXmmXmm(dst XMMRegister, src XMMRegister) {
	assembler.emitRex(false, hiBitXmm(dst), 0, hiBitXmm(src))
	assembler.buffer.emitByte(0x0F)
	assembler.buffer.emitByte(0x57)
	assembler.emitModRM(0b11, lowBitsXmm(dst), lowBitsXmm(src))
}

func (assembler *Assembler) emitLabelPatch(label *Label) {
	patch := assembler.buffer.Pos()
	assembler.buffer.emitUint32(0)
	if label == nil {
		return
	}
	if label.bound {
		rel := int32(label.pos - (patch + 4))
		binary.LittleEndian.PutUint32(assembler.buffer.bytes[patch:patch+4], uint32(rel))
		return
	}
	label.patches = append(label.patches, patch)
}

func (assembler *Assembler) emitRex(w bool, r byte, x byte, b byte) {
	prefix := byte(0x40)
	if w {
		prefix |= 0x08
	}
	if r != 0 {
		prefix |= 0x04
	}
	if x != 0 {
		prefix |= 0x02
	}
	if b != 0 {
		prefix |= 0x01
	}
	if prefix != 0x40 {
		assembler.buffer.emitByte(prefix)
	}
	if prefix == 0x40 && w {
		assembler.buffer.emitByte(0x48)
	}
	if prefix == 0x40 && !w {
		return
	}
	if prefix == 0x48 {
		return
	}
	if w && prefix != 0x48 {
		return
	}
}

func (assembler *Assembler) emitModRM(mod byte, reg byte, rm byte) {
	assembler.buffer.emitByte((mod << 6) | ((reg & 0x7) << 3) | (rm & 0x7))
}

func (assembler *Assembler) emitMemory(regField byte, base Register, disp int32) {
	baseBits := lowBits(base)
	if baseBits == 4 {
		assembler.emitModRM(0b10, regField, 4)
		assembler.buffer.emitByte((0 << 6) | (4 << 3) | baseBits)
		assembler.buffer.emitInt32(disp)
		return
	}
	assembler.emitModRM(0b10, regField, baseBits)
	assembler.buffer.emitInt32(disp)
}

func lowBits(register Register) byte {
	return byte(register & 0x7)
}

func hiBit(register Register) byte {
	return byte((register >> 3) & 0x1)
}

func lowBitsXmm(register XMMRegister) byte {
	return byte(register & 0x7)
}

func hiBitXmm(register XMMRegister) byte {
	return byte((register >> 3) & 0x1)
}

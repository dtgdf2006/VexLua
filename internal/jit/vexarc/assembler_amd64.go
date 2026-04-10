//go:build amd64

package vexarc

import "fmt"

const (
	regAX  byte = 0
	regCX  byte = 1
	regDX  byte = 2
	regBX  byte = 3
	regSP  byte = 4
	regBP  byte = 5
	regSI  byte = 6
	regDI  byte = 7
	regR8  byte = 8
	regR9  byte = 9
	regR10 byte = 10
	regR11 byte = 11
	regR12 byte = 12
	regR13 byte = 13
	regR14 byte = 14
	regR15 byte = 15

	xmm0 byte = 0
	xmm1 byte = 1
)

type amd64Fixup struct {
	relPos int
	target int
}

type amd64Assembler struct {
	code      []byte
	labels    map[int]int
	fixups    []amd64Fixup
	nextLabel int
}

func newAMD64Assembler() *amd64Assembler {
	return &amd64Assembler{
		code:      make([]byte, 0, 256),
		labels:    make(map[int]int, 32),
		nextLabel: -1,
	}
}

func (a *amd64Assembler) pc() uint32 {
	return uint32(len(a.code))
}

func (a *amd64Assembler) newLabel() int {
	label := a.nextLabel
	a.nextLabel--
	return label
}

func (a *amd64Assembler) bind(label int) {
	a.labels[label] = len(a.code)
}

func (a *amd64Assembler) emit(bytes ...byte) {
	a.code = append(a.code, bytes...)
}

func (a *amd64Assembler) emitI32(v int32) {
	a.code = append(a.code, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func (a *amd64Assembler) emitU64(v uint64) {
	for i := 0; i < 8; i++ {
		a.code = append(a.code, byte(v>>(8*i)))
	}
}

func (a *amd64Assembler) rex(w bool, r byte, b byte) {
	prefix := byte(0x40)
	if w {
		prefix |= 0x08
	}
	if r>>3 != 0 {
		prefix |= 0x04
	}
	if b>>3 != 0 {
		prefix |= 0x01
	}
	if prefix != 0x40 {
		a.emit(prefix)
	}
}

func (a *amd64Assembler) modRMDisp32(reg byte, base byte) {
	modrm := byte(0x80 | ((reg & 7) << 3) | (base & 7))
	a.emit(modrm)
	if base&7 == regSP {
		a.emit(0x24)
	}
}

func (a *amd64Assembler) movRegImm64(reg byte, imm uint64) {
	a.rex(true, 0, reg)
	a.emit(0xB8 + (reg & 7))
	a.emitU64(imm)
}

func (a *amd64Assembler) movRegReg(dst byte, src byte) {
	a.rex(true, src, dst)
	a.emit(0x89, 0xC0|((src&7)<<3)|(dst&7))
}

func (a *amd64Assembler) movRegMem64(reg byte, base byte, disp int32) {
	a.rex(true, reg, base)
	a.emit(0x8B)
	a.modRMDisp32(reg, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) movRegMem32(reg byte, base byte, disp int32) {
	a.rex(false, reg, base)
	a.emit(0x8B)
	a.modRMDisp32(reg, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) movMemReg64(base byte, disp int32, reg byte) {
	a.rex(true, reg, base)
	a.emit(0x89)
	a.modRMDisp32(reg, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) movMemReg32(base byte, disp int32, reg byte) {
	a.rex(false, reg, base)
	a.emit(0x89)
	a.modRMDisp32(reg, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) movMemImm32(base byte, disp int32, imm int32) {
	a.rex(false, 0, base)
	a.emit(0xC7)
	a.modRMDisp32(0, base)
	a.emitI32(disp)
	a.emitI32(imm)
}

func (a *amd64Assembler) movsdXmmMem(xmm byte, base byte, disp int32) {
	a.emit(0xF2)
	a.rex(false, xmm, base)
	a.emit(0x0F, 0x10)
	a.modRMDisp32(xmm, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) movsdMemXmm(base byte, disp int32, xmm byte) {
	a.emit(0xF2)
	a.rex(false, xmm, base)
	a.emit(0x0F, 0x11)
	a.modRMDisp32(xmm, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) addsdXmmMem(xmm byte, base byte, disp int32) {
	a.emit(0xF2)
	a.rex(false, xmm, base)
	a.emit(0x0F, 0x58)
	a.modRMDisp32(xmm, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) subsdXmmMem(xmm byte, base byte, disp int32) {
	a.emit(0xF2)
	a.rex(false, xmm, base)
	a.emit(0x0F, 0x5C)
	a.modRMDisp32(xmm, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) mulsdXmmMem(xmm byte, base byte, disp int32) {
	a.emit(0xF2)
	a.rex(false, xmm, base)
	a.emit(0x0F, 0x59)
	a.modRMDisp32(xmm, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) divsdXmmMem(xmm byte, base byte, disp int32) {
	a.emit(0xF2)
	a.rex(false, xmm, base)
	a.emit(0x0F, 0x5E)
	a.modRMDisp32(xmm, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) ucomisdXmmMem(xmm byte, base byte, disp int32) {
	a.emit(0x66)
	a.rex(false, xmm, base)
	a.emit(0x0F, 0x2E)
	a.modRMDisp32(xmm, base)
	a.emitI32(disp)
}

func (a *amd64Assembler) ucomisdXmmXmm(lhs byte, rhs byte) {
	a.emit(0x66)
	a.rex(false, lhs, rhs)
	a.emit(0x0F, 0x2E, 0xC0|((lhs&7)<<3)|(rhs&7))
}

func (a *amd64Assembler) movqXmmReg(xmm byte, reg byte) {
	a.emit(0x66)
	a.rex(true, xmm, reg)
	a.emit(0x0F, 0x6E, 0xC0|((xmm&7)<<3)|(reg&7))
}

func (a *amd64Assembler) cvttsd2siRegXmm(reg byte, xmm byte) {
	a.emit(0xF2)
	a.rex(true, reg, xmm)
	a.emit(0x0F, 0x2C, 0xC0|((reg&7)<<3)|(xmm&7))
}

func (a *amd64Assembler) cvtsi2sdXmmReg(xmm byte, reg byte) {
	a.emit(0xF2)
	a.rex(true, xmm, reg)
	a.emit(0x0F, 0x2A, 0xC0|((xmm&7)<<3)|(reg&7))
}

func (a *amd64Assembler) addsdXmmXmm(dst byte, src byte) {
	a.emit(0xF2)
	a.rex(false, dst, src)
	a.emit(0x0F, 0x58, 0xC0|((dst&7)<<3)|(src&7))
}

func (a *amd64Assembler) xorRegReg(dst byte, src byte) {
	a.rex(true, src, dst)
	a.emit(0x31, 0xC0|((src&7)<<3)|(dst&7))
}

func (a *amd64Assembler) addRegReg(dst byte, src byte) {
	a.rex(true, src, dst)
	a.emit(0x01, 0xC0|((src&7)<<3)|(dst&7))
}

func (a *amd64Assembler) addRegImm32(reg byte, imm int32) {
	a.rex(true, 0, reg)
	a.emit(0x81, 0xC0|(reg&7))
	a.emitI32(imm)
}

func (a *amd64Assembler) addMemImm32(base byte, disp int32, imm int32) {
	a.rex(false, 0, base)
	a.emit(0x81)
	a.modRMDisp32(0, base)
	a.emitI32(disp)
	a.emitI32(imm)
}

func (a *amd64Assembler) subRegImm32(reg byte, imm int32) {
	a.rex(true, 0, reg)
	a.emit(0x81, 0xE8|(reg&7))
	a.emitI32(imm)
}

func (a *amd64Assembler) andRegReg(dst byte, src byte) {
	a.rex(true, src, dst)
	a.emit(0x21, 0xC0|((src&7)<<3)|(dst&7))
}

func (a *amd64Assembler) shrRegImm8(reg byte, imm byte) {
	a.rex(true, 0, reg)
	a.emit(0xC1, 0xE8|(reg&7), imm)
}

func (a *amd64Assembler) shlRegImm8(reg byte, imm byte) {
	a.rex(true, 0, reg)
	a.emit(0xC1, 0xE0|(reg&7), imm)
}

func (a *amd64Assembler) cmpRegReg(lhs byte, rhs byte) {
	a.rex(true, rhs, lhs)
	a.emit(0x39, 0xC0|((rhs&7)<<3)|(lhs&7))
}

func (a *amd64Assembler) cmpRegImm32(reg byte, imm int32) {
	a.rex(true, 0, reg)
	a.emit(0x81, 0xF8|(reg&7))
	a.emitI32(imm)
}

func (a *amd64Assembler) jump(target int) {
	a.emit(0xE9)
	a.fixups = append(a.fixups, amd64Fixup{relPos: len(a.code), target: target})
	a.emitI32(0)
}

func (a *amd64Assembler) je(target int) {
	a.emit(0x0F, 0x84)
	a.fixups = append(a.fixups, amd64Fixup{relPos: len(a.code), target: target})
	a.emitI32(0)
}

func (a *amd64Assembler) jne(target int) {
	a.emit(0x0F, 0x85)
	a.fixups = append(a.fixups, amd64Fixup{relPos: len(a.code), target: target})
	a.emitI32(0)
}

func (a *amd64Assembler) jp(target int) {
	a.emit(0x0F, 0x8A)
	a.fixups = append(a.fixups, amd64Fixup{relPos: len(a.code), target: target})
	a.emitI32(0)
}

func (a *amd64Assembler) jb(target int) {
	a.emit(0x0F, 0x82)
	a.fixups = append(a.fixups, amd64Fixup{relPos: len(a.code), target: target})
	a.emitI32(0)
}

func (a *amd64Assembler) jbe(target int) {
	a.emit(0x0F, 0x86)
	a.fixups = append(a.fixups, amd64Fixup{relPos: len(a.code), target: target})
	a.emitI32(0)
}

func (a *amd64Assembler) ja(target int) {
	a.emit(0x0F, 0x87)
	a.fixups = append(a.fixups, amd64Fixup{relPos: len(a.code), target: target})
	a.emitI32(0)
}

func (a *amd64Assembler) jae(target int) {
	a.emit(0x0F, 0x83)
	a.fixups = append(a.fixups, amd64Fixup{relPos: len(a.code), target: target})
	a.emitI32(0)
}

func (a *amd64Assembler) js(target int) {
	a.emit(0x0F, 0x88)
	a.fixups = append(a.fixups, amd64Fixup{relPos: len(a.code), target: target})
	a.emitI32(0)
}

func (a *amd64Assembler) cqo() {
	a.rex(true, 0, 0)
	a.emit(0x99)
}

func (a *amd64Assembler) idivReg(reg byte) {
	a.rex(true, 0, reg)
	a.emit(0xF7, 0xF8|(reg&7))
}

func (a *amd64Assembler) callReg(reg byte) {
	a.rex(true, 0, reg)
	a.emit(0xFF, 0xD0|(reg&7))
}

func (a *amd64Assembler) ret() {
	a.emit(0xC3)
}

func (a *amd64Assembler) patch() error {
	for _, fixup := range a.fixups {
		target, ok := a.labels[fixup.target]
		if !ok {
			return fmt.Errorf("missing label target %d", fixup.target)
		}
		rel := int32(target - (fixup.relPos + 4))
		a.code[fixup.relPos+0] = byte(rel)
		a.code[fixup.relPos+1] = byte(rel >> 8)
		a.code[fixup.relPos+2] = byte(rel >> 16)
		a.code[fixup.relPos+3] = byte(rel >> 24)
	}
	return nil
}

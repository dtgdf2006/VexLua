//go:build windows && amd64

package amd64

import (
	"fmt"
	"syscall"
	"unsafe"

	"vexlua/internal/bytecode"
	"vexlua/internal/jit"
	rt "vexlua/internal/runtime"
)

const (
	memCommit       = 0x1000
	memReserve      = 0x2000
	pageReadWrite   = 0x04
	pageExecuteRead = 0x20
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procVirtualAlloc   = kernel32.NewProc("VirtualAlloc")
	procVirtualProtect = kernel32.NewProc("VirtualProtect")
	procRtlMoveMemory  = kernel32.NewProc("RtlMoveMemory")
)

type Compiler struct{}

type program struct {
	name string
	mem  *execMemory
}

type execMemory struct {
	addr uintptr
	size uintptr
}

type emitter struct {
	code    []byte
	labels  map[int]int
	fixups  []fixup
	current int
}

type fixup struct {
	relPos   int
	targetPC int
}

func NewCompiler() jit.Compiler {
	return &Compiler{}
}

func (c *Compiler) Compile(proto *bytecode.Proto) (jit.Program, error) {
	if len(proto.Code) == 0 {
		return nil, jit.ErrUnsupported
	}
	emit := &emitter{
		code:   make([]byte, 0, 256),
		labels: make(map[int]int, len(proto.Code)),
	}
	for pc := 0; pc < len(proto.Code); pc++ {
		instr := proto.Code[pc]
		emit.labels[pc] = len(emit.code)
		if pc+1 < len(proto.Code) && (instr.Op == bytecode.OpLess || instr.Op == bytecode.OpLessEqual) {
			next := proto.Code[pc+1]
			if next.Op == bytecode.OpJumpIfFalse && next.A == instr.A {
				emit.movsdMem(0, slotDisp(instr.B))
				emit.ucomisdMem(0, slotDisp(instr.C))
				if instr.Op == bytecode.OpLess {
					emit.jae(int(next.D))
				} else {
					emit.ja(int(next.D))
				}
				pc++
				continue
			}
		}
		switch instr.Op {
		case bytecode.OpNoop:
		case bytecode.OpLoadConst:
			if int(instr.D) < 0 || int(instr.D) >= len(proto.Constants) {
				return nil, jit.ErrUnsupported
			}
			constant := proto.Constants[instr.D]
			switch constant.Kind() {
			case rt.KindNumber:
				emit.movMemImm64(slotDisp(instr.A), uint64(constant))
			case rt.KindNil, rt.KindBool:
				emit.movMemImm64(slotDisp(instr.A), uint64(constant))
			default:
				return nil, jit.ErrUnsupported
			}
		case bytecode.OpMove:
			emit.movRegMem(0, slotDisp(instr.B))
			emit.movMemReg(slotDisp(instr.A), 0)
		case bytecode.OpAdd, bytecode.OpAddNum:
			emit.movsdMem(0, slotDisp(instr.B))
			emit.addsdMem(0, slotDisp(instr.C))
			emit.movsdStore(slotDisp(instr.A), 0)
		case bytecode.OpAddConst:
			if int(instr.D) < 0 || int(instr.D) >= len(proto.Constants) {
				return nil, jit.ErrUnsupported
			}
			constant := proto.Constants[instr.D]
			if !constant.IsNumber() {
				return nil, jit.ErrUnsupported
			}
			emit.movsdMem(0, slotDisp(instr.B))
			emit.movRegImm64(0, uint64(constant))
			emit.movqXMMReg(1, 0)
			emit.addsdXMM(0, 1)
			emit.movsdStore(slotDisp(instr.A), 0)
		case bytecode.OpJump:
			emit.jump(int(instr.D))
		case bytecode.OpLessEqualJump:
			emit.movsdMem(0, slotDisp(instr.A))
			emit.ucomisdMem(0, slotDisp(instr.B))
			emit.jbe(int(instr.D))
		case bytecode.OpReturn:
			emit.movRegMem(0, slotDisp(instr.A))
			emit.ret()
		case bytecode.OpReturnMulti:
			if instr.B != 1 {
				return nil, jit.ErrUnsupported
			}
			emit.movRegMem(0, slotDisp(instr.A))
			emit.ret()
		default:
			return nil, jit.ErrUnsupported
		}
	}
	if len(emit.code) == 0 || emit.code[len(emit.code)-1] != 0xC3 {
		return nil, jit.ErrUnsupported
	}
	if err := emit.patch(); err != nil {
		return nil, err
	}
	mem, err := newExecMemory(emit.code)
	if err != nil {
		return nil, err
	}
	return &program{name: proto.Name, mem: mem}, nil
}

func (p *program) Name() string {
	return p.name
}

func (p *program) Run(regs []rt.Value) (rt.Value, error) {
	if len(regs) == 0 {
		return rt.NilValue, nil
	}
	ret, _, _ := syscall.SyscallN(p.mem.addr, uintptr(unsafe.Pointer(&regs[0])))
	return rt.Value(ret), nil
}

func newExecMemory(code []byte) (*execMemory, error) {
	addr, _, err := procVirtualAlloc.Call(0, uintptr(len(code)), memCommit|memReserve, pageReadWrite)
	if addr == 0 {
		return nil, fmt.Errorf("VirtualAlloc failed: %w", err)
	}
	if len(code) > 0 {
		procRtlMoveMemory.Call(addr, uintptr(unsafe.Pointer(&code[0])), uintptr(len(code)))
	}
	var oldProtect uintptr
	r1, _, protectErr := procVirtualProtect.Call(addr, uintptr(len(code)), pageExecuteRead, uintptr(unsafe.Pointer(&oldProtect)))
	if r1 == 0 {
		return nil, fmt.Errorf("VirtualProtect failed: %w", protectErr)
	}
	return &execMemory{addr: addr, size: uintptr(len(code))}, nil
}

func slotDisp(slot uint16) int32 {
	return int32(slot) * 8
}

func (e *emitter) movRegImm64(reg byte, imm uint64) {
	e.emit(0x48, 0xB8+reg)
	e.emitU64(imm)
}

func (e *emitter) movMemImm64(disp int32, imm uint64) {
	e.movRegImm64(0, imm)
	e.movMemReg(disp, 0)
}

func (e *emitter) movMemReg(disp int32, reg byte) {
	e.emit(0x48, 0x89, 0x81|(reg<<3))
	e.emitI32(disp)
}

func (e *emitter) movRegMem(reg byte, disp int32) {
	e.emit(0x48, 0x8B, 0x81|(reg<<3))
	e.emitI32(disp)
}

func (e *emitter) movsdMem(xmm byte, disp int32) {
	e.emit(0xF2, 0x0F, 0x10, 0x81|(xmm<<3))
	e.emitI32(disp)
}

func (e *emitter) movsdStore(disp int32, xmm byte) {
	e.emit(0xF2, 0x0F, 0x11, 0x81|(xmm<<3))
	e.emitI32(disp)
}

func (e *emitter) addsdMem(dstXMM byte, disp int32) {
	e.emit(0xF2, 0x0F, 0x58, 0x81|(dstXMM<<3))
	e.emitI32(disp)
}

func (e *emitter) addsdXMM(dstXMM, srcXMM byte) {
	e.emit(0xF2, 0x0F, 0x58, 0xC0|(dstXMM<<3)|srcXMM)
}

func (e *emitter) movqXMMReg(xmm, reg byte) {
	e.emit(0x66, 0x48, 0x0F, 0x6E, 0xC0|(xmm<<3)|reg)
}

func (e *emitter) xorRegReg(dst, src byte) {
	e.emit(0x48, 0x31, 0xC0|(src<<3)|dst)
}

func (e *emitter) orRegReg(dst, src byte) {
	e.emit(0x48, 0x09, 0xC0|(src<<3)|dst)
}

func (e *emitter) cmpRegReg(dst, src byte) {
	e.emit(0x48, 0x39, 0xC0|(src<<3)|dst)
}

func (e *emitter) setcc(op byte, reg byte) {
	e.emit(0x0F, op, 0xC0|reg)
}

func (e *emitter) ucomisdMem(xmm byte, disp int32) {
	e.emit(0x66, 0x0F, 0x2E, 0x81|(xmm<<3))
	e.emitI32(disp)
}

func (e *emitter) movBoolFromCompare(disp int32, setccOp byte) {
	e.xorRegReg(0, 0)
	e.setcc(setccOp, 0)
	e.movRegImm64(2, uint64(rt.FalseValue))
	e.orRegReg(0, 2)
	e.movMemReg(disp, 0)
}

func (e *emitter) jumpIfFalse(disp int32, targetPC int) {
	e.movRegMem(0, disp)
	e.movRegImm64(2, uint64(rt.FalseValue))
	e.cmpRegReg(0, 2)
	e.je(targetPC)
	e.movRegImm64(2, uint64(rt.NilValue))
	e.cmpRegReg(0, 2)
	e.je(targetPC)
}

func (e *emitter) jump(targetPC int) {
	e.emit(0xE9)
	e.fixups = append(e.fixups, fixup{relPos: len(e.code), targetPC: targetPC})
	e.emitI32(0)
}

func (e *emitter) je(targetPC int) {
	e.emit(0x0F, 0x84)
	e.fixups = append(e.fixups, fixup{relPos: len(e.code), targetPC: targetPC})
	e.emitI32(0)
}

func (e *emitter) jae(targetPC int) {
	e.emit(0x0F, 0x83)
	e.fixups = append(e.fixups, fixup{relPos: len(e.code), targetPC: targetPC})
	e.emitI32(0)
}

func (e *emitter) ja(targetPC int) {
	e.emit(0x0F, 0x87)
	e.fixups = append(e.fixups, fixup{relPos: len(e.code), targetPC: targetPC})
	e.emitI32(0)
}

func (e *emitter) jbe(targetPC int) {
	e.emit(0x0F, 0x86)
	e.fixups = append(e.fixups, fixup{relPos: len(e.code), targetPC: targetPC})
	e.emitI32(0)
}

func (e *emitter) ret() {
	e.emit(0xC3)
}

func (e *emitter) patch() error {
	for _, f := range e.fixups {
		target, ok := e.labels[f.targetPC]
		if !ok {
			return fmt.Errorf("unknown branch target pc %d", f.targetPC)
		}
		rel := int32(target - (f.relPos + 4))
		putI32(e.code[f.relPos:f.relPos+4], rel)
	}
	return nil
}

func (e *emitter) emit(bytes ...byte) {
	e.code = append(e.code, bytes...)
}

func (e *emitter) emitI32(v int32) {
	start := len(e.code)
	e.code = append(e.code, 0, 0, 0, 0)
	putI32(e.code[start:start+4], v)
}

func (e *emitter) emitU64(v uint64) {
	for i := 0; i < 8; i++ {
		e.code = append(e.code, byte(v>>(8*i)))
	}
}

func putI32(dst []byte, v int32) {
	dst[0] = byte(v)
	dst[1] = byte(v >> 8)
	dst[2] = byte(v >> 16)
	dst[3] = byte(v >> 24)
}

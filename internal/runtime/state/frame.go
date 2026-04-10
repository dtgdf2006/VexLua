// Package state contains VM state, thread state, and call frame contracts.
package state

import (
	"fmt"
	"unsafe"

	"vexlua/internal/runtime/value"
)

type FrameFlags uint16

const (
	CallFrameHeaderSize = 0x50
	StubCallBlockSize   = 0x30
)

const (
	CallFramePrevFrameOffset   = 0x00
	CallFrameCallerRetPCOffset = 0x08
	CallFrameClosureOffset     = 0x10
	CallFrameProtoOffset       = 0x18
	CallFrameRegsBaseOffset    = 0x20
	CallFrameConstBaseOffset   = 0x28
	CallFrameVarargBaseOffset  = 0x30
	CallFrameSavedBCOffOffset  = 0x38
	CallFrameFlagsOffset       = 0x3C
	CallFrameNResultsOffset    = 0x3E
	CallFrameVarargCountOffset = 0x40
	CallFrameRegisterCountOff  = 0x44
	CallFrameSpillCountOffset  = 0x46
	CallFrameResultBaseOffset  = 0x48
)

const (
	StubCallBlockFrameOffset  = 0x00
	StubCallBlockArg0Offset   = 0x08
	StubCallBlockArg1Offset   = 0x10
	StubCallBlockArg2Offset   = 0x18
	StubCallBlockArg3Offset   = 0x20
	StubCallBlockStubIDOffset = 0x28
	StubCallBlockFlagsOffset  = 0x2C
)

const (
	FrameFlagIsLuaFrame FrameFlags = 1 << iota
	FrameFlagHasVararg
	FrameFlagIsTailcall
	FrameFlagInHookMode
	FrameFlagCanYield
	FrameFlagPendingError
	FrameFlagDeoptRequested
	FrameFlagHostBoundary
)

type CallFrameHeader struct {
	PrevFrame     uint64
	CallerRetPC   uint64
	Closure       value.TValue
	Proto         value.TValue
	RegsBase      uint64
	ConstBase     uint64
	VarargBase    uint64
	SavedBCOff    uint32
	Flags         FrameFlags
	NResults      int16
	VarargCount   uint32
	RegisterCount uint16
	SpillCount    uint16
	ResultBase    uint64
}

type StubCallBlock struct {
	Frame  uint64
	Arg0   uint64
	Arg1   uint64
	Arg2   uint64
	Arg3   uint64
	StubID uint32
	Flags  uint32
}

func (flags FrameFlags) Has(mask FrameFlags) bool {
	return flags&mask == mask
}

func (frame *CallFrameHeader) SetFlag(flag FrameFlags, enabled bool) {
	if enabled {
		frame.Flags |= flag
		return
	}
	frame.Flags &^= flag
}

func (frame CallFrameHeader) Validate() error {
	if err := ValidateLayout(); err != nil {
		return err
	}
	if frame.RegsBase == 0 {
		return fmt.Errorf("regs_base cannot be zero")
	}
	if frame.RegsBase%value.TValueSize != 0 {
		return fmt.Errorf("regs_base %#x is not %d-byte aligned", frame.RegsBase, value.TValueSize)
	}
	if frame.ConstBase != 0 && frame.ConstBase%value.TValueSize != 0 {
		return fmt.Errorf("const_base %#x is not %d-byte aligned", frame.ConstBase, value.TValueSize)
	}
	if frame.VarargBase != 0 && frame.VarargBase%value.TValueSize != 0 {
		return fmt.Errorf("vararg_base %#x is not %d-byte aligned", frame.VarargBase, value.TValueSize)
	}
	if frame.ResultBase != 0 && frame.ResultBase%value.TValueSize != 0 {
		return fmt.Errorf("result_base %#x is not %d-byte aligned", frame.ResultBase, value.TValueSize)
	}
	if frame.VarargCount > 0 && !frame.Flags.Has(FrameFlagHasVararg) {
		return fmt.Errorf("vararg_count is non-zero but has_vararg flag is not set")
	}
	return nil
}

func (frame CallFrameHeader) RegisterAddress(index uint16) (uintptr, error) {
	if index >= frame.RegisterCount {
		return 0, fmt.Errorf("register index %d is outside %d slots", index, frame.RegisterCount)
	}
	return uintptr(frame.RegsBase) + uintptr(index)*value.TValueSize, nil
}

func (frame CallFrameHeader) SpillAddress(index uint16) (uintptr, error) {
	if index >= frame.SpillCount {
		return 0, fmt.Errorf("spill index %d is outside %d slots", index, frame.SpillCount)
	}
	spillBase := uintptr(frame.RegsBase) + uintptr(frame.RegisterCount)*value.TValueSize
	return spillBase + uintptr(index)*value.TValueSize, nil
}

func (frame CallFrameHeader) ResultAddress(index uint16) (uintptr, error) {
	if frame.ResultBase == 0 {
		return 0, fmt.Errorf("result_base is not set")
	}
	return uintptr(frame.ResultBase) + uintptr(index)*value.TValueSize, nil
}

func ValidateLayout() error {
	if unsafe.Sizeof(CallFrameHeader{}) != CallFrameHeaderSize {
		return fmt.Errorf("CallFrameHeader size mismatch: got %#x want %#x", unsafe.Sizeof(CallFrameHeader{}), CallFrameHeaderSize)
	}
	if unsafe.Offsetof(CallFrameHeader{}.PrevFrame) != CallFramePrevFrameOffset {
		return fmt.Errorf("PrevFrame offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.PrevFrame), CallFramePrevFrameOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.CallerRetPC) != CallFrameCallerRetPCOffset {
		return fmt.Errorf("CallerRetPC offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.CallerRetPC), CallFrameCallerRetPCOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.Closure) != CallFrameClosureOffset {
		return fmt.Errorf("Closure offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.Closure), CallFrameClosureOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.Proto) != CallFrameProtoOffset {
		return fmt.Errorf("Proto offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.Proto), CallFrameProtoOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.RegsBase) != CallFrameRegsBaseOffset {
		return fmt.Errorf("RegsBase offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.RegsBase), CallFrameRegsBaseOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.ConstBase) != CallFrameConstBaseOffset {
		return fmt.Errorf("ConstBase offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.ConstBase), CallFrameConstBaseOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.VarargBase) != CallFrameVarargBaseOffset {
		return fmt.Errorf("VarargBase offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.VarargBase), CallFrameVarargBaseOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.SavedBCOff) != CallFrameSavedBCOffOffset {
		return fmt.Errorf("SavedBCOff offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.SavedBCOff), CallFrameSavedBCOffOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.Flags) != CallFrameFlagsOffset {
		return fmt.Errorf("Flags offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.Flags), CallFrameFlagsOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.NResults) != CallFrameNResultsOffset {
		return fmt.Errorf("NResults offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.NResults), CallFrameNResultsOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.VarargCount) != CallFrameVarargCountOffset {
		return fmt.Errorf("VarargCount offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.VarargCount), CallFrameVarargCountOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.RegisterCount) != CallFrameRegisterCountOff {
		return fmt.Errorf("RegisterCount offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.RegisterCount), CallFrameRegisterCountOff)
	}
	if unsafe.Offsetof(CallFrameHeader{}.SpillCount) != CallFrameSpillCountOffset {
		return fmt.Errorf("SpillCount offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.SpillCount), CallFrameSpillCountOffset)
	}
	if unsafe.Offsetof(CallFrameHeader{}.ResultBase) != CallFrameResultBaseOffset {
		return fmt.Errorf("ResultBase offset mismatch: got %#x want %#x", unsafe.Offsetof(CallFrameHeader{}.ResultBase), CallFrameResultBaseOffset)
	}
	if unsafe.Sizeof(StubCallBlock{}) != StubCallBlockSize {
		return fmt.Errorf("StubCallBlock size mismatch: got %#x want %#x", unsafe.Sizeof(StubCallBlock{}), StubCallBlockSize)
	}
	if unsafe.Offsetof(StubCallBlock{}.Frame) != StubCallBlockFrameOffset {
		return fmt.Errorf("StubCallBlock.Frame offset mismatch")
	}
	if unsafe.Offsetof(StubCallBlock{}.Arg0) != StubCallBlockArg0Offset {
		return fmt.Errorf("StubCallBlock.Arg0 offset mismatch")
	}
	if unsafe.Offsetof(StubCallBlock{}.Arg1) != StubCallBlockArg1Offset {
		return fmt.Errorf("StubCallBlock.Arg1 offset mismatch")
	}
	if unsafe.Offsetof(StubCallBlock{}.Arg2) != StubCallBlockArg2Offset {
		return fmt.Errorf("StubCallBlock.Arg2 offset mismatch")
	}
	if unsafe.Offsetof(StubCallBlock{}.Arg3) != StubCallBlockArg3Offset {
		return fmt.Errorf("StubCallBlock.Arg3 offset mismatch")
	}
	if unsafe.Offsetof(StubCallBlock{}.StubID) != StubCallBlockStubIDOffset {
		return fmt.Errorf("StubCallBlock.StubID offset mismatch")
	}
	if unsafe.Offsetof(StubCallBlock{}.Flags) != StubCallBlockFlagsOffset {
		return fmt.Errorf("StubCallBlock.Flags offset mismatch")
	}
	return nil
}

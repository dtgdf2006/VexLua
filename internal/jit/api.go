package jit

import (
	"errors"
	"fmt"

	"vexlua/internal/bytecode"
	rt "vexlua/internal/runtime"
)

var ErrUnsupported = errors.New("jit: unsupported program")
var ErrRetryLater = errors.New("jit: retry compilation later")

const InlineResultCap = 4

type CompileMode uint8

const (
	CompileWholeProto CompileMode = iota + 1
	CompileRegion
)

type Region struct {
	ID      uint32
	StartPC int
	EndPC   int
}

type ExitReason uint8

const (
	ExitInterpret ExitReason = iota + 1
	ExitReturn
	ExitNestedCall
	ExitSideExit
	ExitCallHelper
	ExitTailCall
	ExitYield
	ExitHook
	ExitGC
	ExitPanic
)

const (
	ExitFlagTailReplace uint32 = 1 << iota
)

type CompileRequest struct {
	Proto  *bytecode.Proto
	Mode   CompileMode
	Region Region
}

type Compiler interface {
	Compile(req CompileRequest) (CompiledUnit, error)
}

type CompiledUnit interface {
	Name() string
	Meta() *CompiledUnitMeta
	Entry() uintptr
	Enter(thread *NativeThreadState, frame *NativeFrameState) (NativeExitRecord, error)
}

type NativeMultiResultBuffer struct {
	Count      uint32
	SpillCount uint32
	Flags      uint32
	Reserved   uint32
	SpillBase  uintptr
	Inline     [InlineResultCap]rt.Value
}

type DirectCallCache struct {
	Callee          rt.Value
	Entry           uintptr
	MaxStack        uint32
	Flags           uint32
	FieldCachesBase uintptr
	FieldCachesLen  uintptr
	CallCachesBase  uintptr
	CallCachesLen   uintptr
	EnvHandle       rt.Handle
}

const (
	DirectCallTailReturnSafe uint32 = 1 << iota
	DirectCallNoVararg
)

type NativeUpvalue struct {
	Cell uintptr
}

type NativeThreadState struct {
	Flags            uint32
	FrameDepth       uint32
	StackTop         uint32
	StackCapacity    uint32
	CurrentStatus    uint32
	ActiveFrame      uint32
	PendingHelper    uint32
	PendingExit      uint32
	DirectCallCount  uint32
	PendingCallCache uint32
	Reserved0        uint32
	Reserved1        uint32
	HeapTablesBase   uintptr
	HeapTablesLen    uintptr
	FieldCachesBase  uintptr
	FieldCachesLen   uintptr
	CallCachesBase   uintptr
	CallCachesLen    uintptr
	UpvaluesBase     uintptr
	UpvaluesLen      uintptr
	CurrentEnvHandle rt.Handle
	PendingCallee    rt.Value
	PendingFrame     NativeFrameState
	PendingCallExit  NativeExitRecord
	LastExit         NativeExitRecord
}

type NativeFrameState struct {
	ProtoID     uint32
	Base        uint32
	PC          uint32
	MaxStack    uint32
	SlotsBase   uintptr
	ResultReg   uint32
	ResultCount uint32
	VarargCount uint32
	Flags       uint32
	Pending     NativeMultiResultBuffer
	Varargs     NativeMultiResultBuffer
}

type NativeExitRecord struct {
	Reason          ExitReason
	ResumePC        uint32
	CodeOffset      uint32
	LiveBitmapIndex uint32
	HelperID        uint32
	Detail          uint32
	Flags           uint32
	ReturnValue     rt.Value
}

func (mode CompileMode) String() string {
	switch mode {
	case CompileWholeProto:
		return "whole-proto"
	case CompileRegion:
		return "region"
	default:
		return fmt.Sprintf("compile-mode(%d)", mode)
	}
}

func (reason ExitReason) String() string {
	switch reason {
	case ExitInterpret:
		return "interpret"
	case ExitReturn:
		return "return"
	case ExitNestedCall:
		return "nested-call"
	case ExitSideExit:
		return "side-exit"
	case ExitCallHelper:
		return "call-helper"
	case ExitTailCall:
		return "tail-call"
	case ExitYield:
		return "yield"
	case ExitHook:
		return "hook"
	case ExitGC:
		return "gc"
	case ExitPanic:
		return "panic"
	default:
		return fmt.Sprintf("exit-reason(%d)", reason)
	}
}

func (region Region) ValidFor(proto *bytecode.Proto) bool {
	if proto == nil {
		return false
	}
	if region.StartPC < 0 || region.EndPC > len(proto.Code) {
		return false
	}
	return region.StartPC < region.EndPC
}

func (req CompileRequest) Validate() error {
	if req.Proto == nil {
		return fmt.Errorf("%w: missing proto", ErrUnsupported)
	}
	if len(req.Proto.Code) == 0 {
		return fmt.Errorf("%w: proto %q has no code", ErrUnsupported, req.Proto.Name)
	}
	switch req.Mode {
	case CompileWholeProto:
		return nil
	case CompileRegion:
		if !req.Region.ValidFor(req.Proto) {
			return fmt.Errorf("%w: invalid region [%d,%d) for %q", ErrUnsupported, req.Region.StartPC, req.Region.EndPC, req.Proto.Name)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown compile mode %s", ErrUnsupported, req.Mode)
	}
}

func (buf *NativeMultiResultBuffer) Reset() {
	*buf = NativeMultiResultBuffer{}
}

func (state *NativeThreadState) Reset() {
	*state = NativeThreadState{}
}

func (state *NativeFrameState) Reset() {
	*state = NativeFrameState{}
}

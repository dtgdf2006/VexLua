package baseline

import (
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/codecache"
	"vexlua/internal/vexarc/metadata"
)

const (
	compiledStatusOK      = 0
	compiledStatusYield   = 1
	compiledStatusError   = 2
	compiledStatusDeopt   = 3
	compiledStatusSuspend = 4
)

type SuspendKind uint32

const (
	SuspendNone SuspendKind = iota
	SuspendCall
	SuspendForPrep
	SuspendForLoop
)

const (
	execCtxResumePCOffset    = 0x00
	execCtxSuspendKindOffset = 0x04
	execCtxArg0Offset        = 0x08
	execCtxArg1Offset        = 0x0C
	execCtxArg2Offset        = 0x10
)

type executionContext struct {
	ResumePC    uint32
	SuspendKind uint32
	Arg0        uint32
	Arg1        uint32
	Arg2        uint32
	Reserved    uint32
}

type CompiledCode struct {
	Proto             *bytecode.Proto
	ProtoRef          value.HeapRef44
	Block             *codecache.Block
	Metadata          metadata.CodeMetadata
	Entry             uintptr
	Supported         bool
	UnsupportedReason string
}

func (code *CompiledCode) EntryAtBytecode(bytecodeOffset int) (uintptr, error) {
	if code == nil || !code.Supported || code.Block == nil {
		return 0, fmt.Errorf("compiled code is not executable")
	}
	offset, ok := code.Metadata.CodeOffset(bytecodeOffset)
	if !ok {
		return 0, fmt.Errorf("no code offset for bytecode %d", bytecodeOffset)
	}
	return code.Entry + uintptr(offset), nil
}

func (code *CompiledCode) Release(cache *codecache.Cache) error {
	if cache == nil || code == nil || code.Block == nil {
		return nil
	}
	err := cache.Release(code.Block)
	if err == nil {
		code.Block = nil
		code.Entry = 0
	}
	return err
}

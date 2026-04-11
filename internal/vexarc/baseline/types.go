package baseline

import (
	"fmt"

	"vexlua/internal/bytecode"
	"vexlua/internal/runtime/feedback"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/codecache"
	"vexlua/internal/vexarc/metadata"
)

const (
	compiledStatusOK    = 0
	compiledStatusYield = 1
	compiledStatusError = 2
	compiledStatusDeopt = 3
	compiledStatusStub  = 4
)

const (
	execCtxSiteIDOffset = 0x00
	execCtxFlagsOffset  = 0x04
)

type executionContext struct {
	SiteID    uint32
	Flags     uint32
	Reserved0 uint32
	Reserved1 uint32
	Reserved2 uint32
	Reserved3 uint32
}

type CompiledCode struct {
	Proto             *bytecode.Proto
	ProtoRef          value.HeapRef44
	Block             *codecache.Block
	Metadata          metadata.CodeMetadata
	FeedbackLayout    *feedback.Layout
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

func (code *CompiledCode) ContinuationSite(siteID uint32) (metadata.ContinuationSite, error) {
	if code == nil {
		return metadata.ContinuationSite{}, fmt.Errorf("compiled code is nil")
	}
	site, ok := code.Metadata.ContinuationSite(siteID)
	if !ok {
		return metadata.ContinuationSite{}, fmt.Errorf("unknown continuation site %d", siteID)
	}
	return site, nil
}

func (code *CompiledCode) EntryAtSite(site metadata.ContinuationSite, alternate bool) (uintptr, error) {
	if code == nil || !code.Supported || code.Block == nil {
		return 0, fmt.Errorf("compiled code is not executable")
	}
	offset := site.ResumeCodeOffset
	if alternate {
		offset = site.AltResumeCodeOff
	}
	if offset == metadata.UnmappedOffset {
		return 0, fmt.Errorf("continuation site %d has no compiled resume target", site.BytecodePC)
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

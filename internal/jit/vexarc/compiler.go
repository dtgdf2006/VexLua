package vexarc

import (
	"errors"

	"vexlua/internal/jit"
)

type Compiler struct {
	cache *jit.CodeCache
}

type stubUnit struct {
	name string
	meta *jit.CompiledUnitMeta
	blob *jit.CodeBlob
}

func NewCompiler() jit.Compiler {
	return NewCompilerWithCache(nil)
}

func NewCompilerWithCache(cache *jit.CodeCache) *Compiler {
	if cache == nil {
		cache = jit.NewCodeCache()
	}
	return &Compiler{cache: cache}
}

func (c *Compiler) Compile(req jit.CompileRequest) (jit.CompiledUnit, error) {
	if req.Mode == 0 {
		req.Mode = jit.CompileWholeProto
	}
	if err := CanCompileWithVexarc(req); err != nil {
		return nil, err
	}
	if req.Mode == jit.CompileRegion {
		return compileRegion(c.cache, req)
	}
	unit, err := compileWholeProto(c.cache, req)
	if err == nil {
		return unit, nil
	}
	if !errors.Is(err, jit.ErrRetryLater) {
		return nil, err
	}
	regionReq, ok := planRetryLaterRegion(req)
	if !ok {
		return nil, err
	}
	return compileRegion(c.cache, regionReq)
}

func (u *stubUnit) Name() string {
	return u.name
}

func (u *stubUnit) Meta() *jit.CompiledUnitMeta {
	return u.meta
}

func (u *stubUnit) Entry() uintptr {
	if u.blob == nil {
		return 0
	}
	return u.blob.Entry()
}

func (u *stubUnit) Enter(thread *jit.NativeThreadState, frame *jit.NativeFrameState) (jit.NativeExitRecord, error) {
	if u.blob == nil {
		exit := jit.NativeExitRecord{Reason: jit.ExitInterpret}
		if thread != nil {
			thread.PendingExit = uint32(jit.ExitInterpret)
		}
		if frame != nil {
			exit.ResumePC = frame.PC
		}
		return exit, nil
	}
	entry := u.blob.Entry()
	if u.meta != nil && frame != nil {
		if offset, ok := u.meta.CodeOffsetForPC(int(frame.PC)); ok {
			entry = u.blob.EntryAt(offset)
		} else if frame.PC != 0 {
			exit := jit.NativeExitRecord{Reason: jit.ExitInterpret, ResumePC: frame.PC}
			if thread != nil {
				thread.PendingExit = uint32(jit.ExitInterpret)
			}
			return exit, nil
		}
	}
	exit := invokeEnterABI(entry, thread, frame)
	if u.meta != nil {
		exit.Detail = u.meta.UnitID
	}
	return exit, nil
}

func stubCode() []byte {
	return []byte{0xC3}
}

package vexarc

import (
	"errors"

	"vexlua/internal/jit"
)

func compileWholeProto(cache *jit.CodeCache, req jit.CompileRequest) (jit.CompiledUnit, error) {
	unit, err := compileWholeProtoNative(cache, req)
	if err == nil {
		return unit, nil
	}
	if errors.Is(err, jit.ErrRetryLater) {
		return nil, err
	}
	return compileWholeProtoStub(cache, req)
}

func compileWholeProtoStub(cache *jit.CodeCache, req jit.CompileRequest) (jit.CompiledUnit, error) {
	meta := jit.NewWholeProtoMeta(req.Proto)
	builder := jit.NewBytecodeOffsetTableBuilder(meta)
	builder.FillInterpretFallback()
	builder.Finish()
	if err := meta.AddSideExit(meta.Region.StartPC, jit.ExitInterpret); err != nil {
		return nil, err
	}
	blob, err := cache.Install(req.Proto.Name, meta, stubCode(), 0)
	if err != nil {
		return nil, err
	}
	return &stubUnit{name: req.Proto.Name, meta: meta, blob: blob}, nil
}

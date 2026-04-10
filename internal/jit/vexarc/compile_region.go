package vexarc

import (
	"errors"
	"fmt"

	"vexlua/internal/jit"
)

func compileRegion(cache *jit.CodeCache, req jit.CompileRequest) (jit.CompiledUnit, error) {
	meta := jit.NewRegionMeta(req.Proto, req.Region)
	name := fmt.Sprintf("%s#region[%d:%d]", req.Proto.Name, req.Region.StartPC, req.Region.EndPC)
	unit, err := compileNativeUnit(cache, req, meta, name)
	if err == nil {
		return unit, nil
	}
	if errors.Is(err, jit.ErrRetryLater) {
		return nil, err
	}
	builder := jit.NewBytecodeOffsetTableBuilder(meta)
	builder.FillInterpretFallback()
	builder.Finish()
	if err := meta.AddSideExit(meta.Region.StartPC, jit.ExitInterpret); err != nil {
		return nil, err
	}
	blob, err := cache.Install(name, meta, stubCode(), 0)
	if err != nil {
		return nil, err
	}
	return &stubUnit{name: name, meta: meta, blob: blob}, nil
}

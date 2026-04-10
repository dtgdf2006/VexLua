//go:build !amd64

package vexarc

import "vexlua/internal/jit"

func compileWholeProtoNative(cache *jit.CodeCache, req jit.CompileRequest) (jit.CompiledUnit, error) {
	return nil, jit.ErrUnsupported
}

func compileNativeUnit(cache *jit.CodeCache, req jit.CompileRequest, meta *jit.CompiledUnitMeta, name string) (jit.CompiledUnit, error) {
	_ = cache
	_ = req
	_ = meta
	_ = name
	return nil, jit.ErrUnsupported
}

package vexarc

import "vexlua/internal/jit"

func CanCompileWithVexarc(req jit.CompileRequest) error {
	return req.Validate()
}

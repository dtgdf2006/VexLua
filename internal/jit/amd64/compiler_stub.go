//go:build !windows || !amd64

package amd64

import (
	"vexlua/internal/bytecode"
	"vexlua/internal/jit"
)

type Compiler struct{}

func NewCompiler() jit.Compiler {
	return &Compiler{}
}

func (c *Compiler) Compile(proto *bytecode.Proto) (jit.Program, error) {
	return nil, jit.ErrUnsupported
}

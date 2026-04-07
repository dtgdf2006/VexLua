package jit

import (
	"errors"

	"vexlua/internal/bytecode"
	rt "vexlua/internal/runtime"
)

var ErrUnsupported = errors.New("jit: unsupported program")

type Program interface {
	Name() string
	Run(regs []rt.Value) (rt.Value, error)
}

type Compiler interface {
	Compile(proto *bytecode.Proto) (Program, error)
}

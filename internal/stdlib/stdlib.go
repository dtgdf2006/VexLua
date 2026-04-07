package stdlib

import (
	"vexlua/internal/bytecode"
	rt "vexlua/internal/runtime"
	"vexlua/internal/vm"
)

type SourceCompiler interface {
	CompileSource(source string) (*bytecode.Proto, error)
}

func Register(runtime *rt.Runtime, machine *vm.VM, compiler SourceCompiler) error {
	if err := registerBase(runtime, machine, compiler); err != nil {
		return err
	}
	if err := registerMath(runtime); err != nil {
		return err
	}
	if err := registerString(runtime, machine); err != nil {
		return err
	}
	if err := registerTable(runtime, machine); err != nil {
		return err
	}
	if err := registerCoroutine(runtime, machine); err != nil {
		return err
	}
	return nil
}

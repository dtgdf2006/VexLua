package baseline

import (
	"vexlua/internal/bytecode"
)

type BytecodeIterator struct {
	inner *bytecode.Iterator
}

func NewBytecodeIterator(proto *bytecode.Proto) *BytecodeIterator {
	return &BytecodeIterator{inner: bytecode.NewProtoIterator(proto)}
}

func (iterator *BytecodeIterator) Done() bool {
	return iterator.inner.Done()
}

func (iterator *BytecodeIterator) Current() bytecode.Instruction {
	return iterator.inner.Current()
}

func (iterator *BytecodeIterator) CurrentOffset() int {
	return iterator.inner.CurrentOffset()
}

func (iterator *BytecodeIterator) Advance() {
	iterator.inner.Advance()
}

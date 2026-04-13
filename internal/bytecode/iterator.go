package bytecode

import "fmt"

type Iterator struct {
	code   []Instruction
	offset int
}

func NewIterator(code []Instruction) *Iterator {
	return &Iterator{code: code}
}

func NewProtoIterator(proto *Proto) *Iterator {
	return NewIterator(proto.Code)
}

func (it *Iterator) Reset() {
	it.offset = 0
}

func (it *Iterator) Done() bool {
	return it.offset < 0 || it.offset >= len(it.code)
}

func (it *Iterator) Current() Instruction {
	if it.Done() {
		return 0
	}
	return it.code[it.offset]
}

func (it *Iterator) CurrentOpcode() Opcode {
	return it.Current().Opcode()
}

func (it *Iterator) CurrentOffset() int {
	return it.offset
}

func (it *Iterator) NextOffset() int {
	return it.offset + 1
}

func (it *Iterator) Advance() {
	if !it.Done() {
		it.offset++
	}
}

func (it *Iterator) SetOffset(offset int) error {
	if offset < 0 || offset > len(it.code) {
		return fmt.Errorf("bytecode offset out of range: %d", offset)
	}
	it.offset = offset
	return nil
}

func (it *Iterator) AdvanceTo(offset int) error {
	if offset < it.offset {
		return fmt.Errorf("cannot advance backwards from %d to %d", it.offset, offset)
	}
	return it.SetOffset(offset)
}

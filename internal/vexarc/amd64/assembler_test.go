package amd64

import "testing"

func TestAssemblerBindsLabels(t *testing.T) {
	assembler := NewAssembler(32)
	label := assembler.NewLabel()
	assembler.Jmp(label)
	assembler.MoveRegImm32(RegRAX, 1)
	if err := assembler.Bind(label); err != nil {
		t.Fatalf("bind label: %v", err)
	}
	assembler.Ret()
	bytes := assembler.Buffer().Bytes()
	if len(bytes) == 0 {
		t.Fatalf("assembler should emit bytes")
	}
	if bytes[0] != 0xE9 {
		t.Fatalf("expected jmp opcode, got %#x", bytes[0])
	}
}

func TestAssemblerMovesThroughR12Memory(t *testing.T) {
	assembler := NewAssembler(32)
	assembler.MoveRegMem64(RegRAX, RegR12, 0x20)
	assembler.MoveMemReg64(RegR12, 0x28, RegRAX)
	assembler.Ret()
	if len(assembler.Buffer().Bytes()) == 0 {
		t.Fatalf("assembler emitted no code")
	}
}

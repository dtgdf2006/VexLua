package bytecode

import (
	"fmt"
	"strings"
)

func FormatInstruction(in Instruction) string {
	return FormatInstructionAt(-1, in)
}

func FormatInstructionAt(pc int, in Instruction) string {
	op := in.Opcode()
	if !op.Valid() {
		if pc >= 0 {
			return fmt.Sprintf("[%04d] INVALID %#08x", pc, uint32(in))
		}
		return fmt.Sprintf("INVALID %#08x", uint32(in))
	}

	info := op.Info()
	parts := make([]string, 0, 5)
	if pc >= 0 {
		parts = append(parts, fmt.Sprintf("[%04d]", pc))
	}
	parts = append(parts, info.Name)

	switch info.Mode {
	case ModeABC:
		parts = append(parts, fmt.Sprintf("A=%d", in.A()))
		if info.BMode != ArgN {
			parts = append(parts, formatOperand("B", in.B(), info.BMode))
		}
		if info.CMode != ArgN {
			parts = append(parts, formatOperand("C", in.C(), info.CMode))
		}
	case ModeABx:
		parts = append(parts, fmt.Sprintf("A=%d", in.A()))
		parts = append(parts, fmt.Sprintf("Bx=%d", in.Bx()))
	case ModeAsBx:
		if op != OP_JMP {
			parts = append(parts, fmt.Sprintf("A=%d", in.A()))
		}
		parts = append(parts, fmt.Sprintf("sBx=%d", in.SBx()))
	}

	return strings.Join(parts, " ")
}

func DumpCode(code []Instruction) string {
	var builder strings.Builder
	for pc, in := range code {
		if pc > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(FormatInstructionAt(pc, in))
	}
	return builder.String()
}

func formatOperand(name string, value int, mode ArgMode) string {
	switch mode {
	case ArgK:
		if IsConstantRK(value) {
			return fmt.Sprintf("%s=K%d", name, IndexK(value))
		}
		return fmt.Sprintf("%s=R%d", name, value)
	default:
		return fmt.Sprintf("%s=%d", name, value)
	}
}

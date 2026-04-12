package baseline

import (
	"reflect"
	"testing"

	"vexlua/internal/bytecode"
)

func TestPhase0CompiledDispositionSnapshot(t *testing.T) {
	if bytecode.NumOpcodes != 38 {
		t.Fatalf("phase-0 snapshot expects 38 opcodes, got %d", bytecode.NumOpcodes)
	}

	state := &compileState{}
	compiledCount := 0
	deoptOpcodes := make([]string, 0)
	for opcodeIndex := 0; opcodeIndex < bytecode.NumOpcodes; opcodeIndex++ {
		opcode := bytecode.Opcode(opcodeIndex)
		instruction := bytecode.CreateABC(opcode, 0, 0, 0)
		switch state.dispositionForInstruction(0, instruction) {
		case instructionDispositionCompiled:
			compiledCount++
		case instructionDispositionDeopt:
			deoptOpcodes = append(deoptOpcodes, opcode.String())
		case instructionDispositionPayload:
			t.Fatalf("phase-0 snapshot should not classify opcode %s as payload", opcode)
		default:
			t.Fatalf("phase-0 snapshot saw unknown disposition for opcode %s", opcode)
		}
	}

	if compiledCount != 38 {
		t.Fatalf("phase-0 compiled disposition = %d / %d, want 38 / 38", compiledCount, bytecode.NumOpcodes)
	}

	wantDeoptOpcodes := []string{}
	if !reflect.DeepEqual(deoptOpcodes, wantDeoptOpcodes) {
		t.Fatalf("phase-0 uncovered opcode set = %v, want %v", deoptOpcodes, wantDeoptOpcodes)
	}
}

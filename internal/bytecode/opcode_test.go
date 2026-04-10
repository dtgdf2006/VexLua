package bytecode_test

import (
	"strings"
	"testing"

	"vexlua/internal/bytecode"
)

func TestOpcodeOrderAndMetadata(t *testing.T) {
	if bytecode.NumOpcodes != 38 {
		t.Fatalf("unexpected opcode count: %d", bytecode.NumOpcodes)
	}
	if bytecode.OP_MOVE.String() != "MOVE" {
		t.Fatalf("unexpected opcode name: %s", bytecode.OP_MOVE)
	}
	if bytecode.OP_RETURN.Info().Mode != bytecode.ModeABC {
		t.Fatalf("unexpected RETURN mode")
	}
	if !bytecode.OP_EQ.Info().IsTest {
		t.Fatalf("EQ should be marked as a test opcode")
	}
	if bytecode.OP_LOADK.Info().Mode != bytecode.ModeABx {
		t.Fatalf("LOADK should be ABx")
	}
}

func TestInstructionEncodingAndRKHelpers(t *testing.T) {
	inst := bytecode.CreateABC(bytecode.OP_GETTABLE, 1, 2, bytecode.RKAsk(3))
	if inst.Opcode() != bytecode.OP_GETTABLE {
		t.Fatalf("unexpected opcode: %s", inst.Opcode())
	}
	if inst.A() != 1 || inst.B() != 2 || bytecode.IndexK(inst.C()) != 3 {
		t.Fatalf("unexpected operands: %s", inst)
	}
	if !bytecode.IsConstantRK(inst.C()) {
		t.Fatalf("expected RK constant operand")
	}

	jmp := bytecode.CreateAsBx(bytecode.OP_JMP, 0, -3)
	if jmp.SBx() != -3 {
		t.Fatalf("unexpected sBx: %d", jmp.SBx())
	}
}

func TestIteratorAndFormatting(t *testing.T) {
	code := []bytecode.Instruction{
		bytecode.CreateABx(bytecode.OP_LOADK, 0, 1),
		bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
	}
	iter := bytecode.NewIterator(code)
	if iter.Done() {
		t.Fatalf("iterator should start on first instruction")
	}
	if iter.CurrentOpcode() != bytecode.OP_LOADK {
		t.Fatalf("unexpected current opcode: %s", iter.CurrentOpcode())
	}
	iter.Advance()
	if iter.CurrentOpcode() != bytecode.OP_RETURN {
		t.Fatalf("unexpected second opcode: %s", iter.CurrentOpcode())
	}
	dump := bytecode.DumpCode(code)
	if !strings.Contains(dump, "LOADK") || !strings.Contains(dump, "RETURN") {
		t.Fatalf("unexpected dump output: %s", dump)
	}
}

func TestProtoClosureTemplateAndValidation(t *testing.T) {
	proto := &bytecode.Proto{
		NumUpvalues:  2,
		MaxStackSize: 2,
		Constants:    []bytecode.Constant{bytecode.NumberConstant(1)},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	tpl := proto.NewClosureTemplate()
	if tpl.UpvalueCount != 2 || tpl.Proto != proto {
		t.Fatalf("unexpected closure template: %+v", tpl)
	}
	if err := bytecode.ValidateProto(proto); err != nil {
		t.Fatalf("expected proto validation success, got: %v", err)
	}
}

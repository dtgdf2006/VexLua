package jit

import (
	"testing"

	"vexlua/internal/bytecode"
	rt "vexlua/internal/runtime"
)

func TestBytecodeOffsetTableBuilderFillInterpretFallback(t *testing.T) {
	proto := bytecode.NewProto("offsets", 2, 0)
	constant := proto.AddConstant(rt.NumberValue(1))
	proto.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(constant))
	proto.Emit(bytecode.OpMove, 1, 0, 0, 0)
	proto.Emit(bytecode.OpReturn, 1, 0, 0, 0)

	meta := NewWholeProtoMeta(proto)
	builder := NewBytecodeOffsetTableBuilder(meta)
	builder.FillInterpretFallback()
	builder.Finish()

	if got, want := len(meta.PCOffsets), len(proto.Code); got != want {
		t.Fatalf("pc offset count = %d, want %d", got, want)
	}
	for pc := range proto.Code {
		offset, ok := meta.CodeOffsetForPC(pc)
		if !ok {
			t.Fatalf("missing code offset for pc %d", pc)
		}
		if offset != 0 {
			t.Fatalf("fallback offset for pc %d = %d, want 0", pc, offset)
		}
	}
}

func TestCompiledUnitMetaHelperCallLookup(t *testing.T) {
	proto := bytecode.NewProto("helpers", 2, 0)
	proto.Emit(bytecode.OpNoop, 0, 0, 0, 0)
	proto.Emit(bytecode.OpReturn, 0, 0, 0, 0)

	meta := NewWholeProtoMeta(proto)
	helperID, err := meta.AddHelperCall(0, 1, HelperGetTableArray, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := meta.AddSideExitAt(0, 1, ExitInterpret, 24); err != nil {
		t.Fatal(err)
	}
	desc, ok := meta.HelperCallForID(helperID)
	if !ok {
		t.Fatalf("missing helper call id %d", helperID)
	}
	if desc.Kind != HelperGetTableArray {
		t.Fatalf("helper kind = %s, want %s", desc.Kind, HelperGetTableArray)
	}
	if desc.ResumePC != 1 || desc.CodeOffset != 24 {
		t.Fatalf("helper desc = %+v, want resume pc 1 and code offset 24", desc)
	}
	if got := meta.SideExits[0]; got.ResumePC != 1 || got.CodeOffset != 24 {
		t.Fatalf("side exit = %+v, want resume pc 1 and code offset 24", got)
	}
}

func TestCompiledUnitMetaHelperCallInlineCacheSlot(t *testing.T) {
	proto := bytecode.NewProto("helpers_inline_cache", 2, 0)
	proto.Emit(bytecode.OpNoop, 0, 0, 0, 0)
	proto.Emit(bytecode.OpReturn, 0, 0, 0, 0)

	meta := NewWholeProtoMeta(proto)
	helperID, err := meta.AddHelperCallWithInlineCache(0, 1, HelperLoadGlobal, 16, 3)
	if err != nil {
		t.Fatal(err)
	}
	desc, ok := meta.HelperCallForID(helperID)
	if !ok {
		t.Fatalf("missing helper call id %d", helperID)
	}
	if desc.InlineCacheSlot != 3 {
		t.Fatalf("inline cache slot = %d, want 3", desc.InlineCacheSlot)
	}
}

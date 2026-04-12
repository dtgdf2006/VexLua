package metadata

import (
	"testing"

	"vexlua/internal/runtime/value"
)

func TestOffsetTableBuilderAndIterator(t *testing.T) {
	builder := &OffsetTableBuilder{}
	for _, position := range []uint32{0, 5, 9, 24} {
		if err := builder.AddPosition(position); err != nil {
			t.Fatalf("add position %d: %v", position, err)
		}
	}
	iterator := NewOffsetIterator(builder.Bytes())
	want := []uint32{0, 5, 9, 24}
	for index, expected := range want {
		if !iterator.Advance() {
			t.Fatalf("advance %d failed", index)
		}
		if iterator.CurrentCodeOffset() != expected {
			t.Fatalf("offset[%d] = %d, want %d", index, iterator.CurrentCodeOffset(), expected)
		}
	}
	if iterator.Advance() {
		t.Fatalf("iterator should be exhausted")
	}
}

func TestCodeMetadataFinalizesContinuationSites(t *testing.T) {
	builder := &OffsetTableBuilder{}
	metadata := NewCodeMetadata(4)
	live := NewLiveSlotSet(4)
	live.AddRegister(1)
	live.AddRegister(3)
	liveID := metadata.AddLiveSlotSet(live)
	if err := metadata.RecordBytecodeLiveSlots(1, live); err != nil {
		t.Fatalf("record bytecode live slots: %v", err)
	}
	positions := []uint32{0, 7, 15, 23}
	for bytecodePC, position := range positions {
		if err := metadata.RecordBytecodeOffset(bytecodePC, position); err != nil {
			t.Fatalf("record bytecode offset %d: %v", bytecodePC, err)
		}
		if err := builder.AddPosition(position); err != nil {
			t.Fatalf("add position %d: %v", position, err)
		}
	}
	siteID := metadata.AddContinuationSite(ContinuationSite{
		Kind:        ContinuationGetTable,
		Flags:       ContinuationFlagAlternateResume | ContinuationFlagNativeBuiltinABI | ContinuationFlagDeoptOnUncovered,
		StubID:      42,
		BytecodePC:  1,
		DeoptPC:     1,
		ResumePC:    2,
		AltResumePC: 3,
		Operand0:    9,
		Operand1:    10,
		Operand2:    11,
		LiveSetID:   liveID,
		LiveSlots:   live.UpperBound(4),
	})
	metadata.Finalize(builder)
	site, ok := metadata.ContinuationSite(siteID)
	if !ok {
		t.Fatalf("missing continuation site %d", siteID)
	}
	if site.ResumeCodeOffset != positions[2] {
		t.Fatalf("resume code offset = %d, want %d", site.ResumeCodeOffset, positions[2])
	}
	if site.AltResumeCodeOff != positions[3] {
		t.Fatalf("alt resume code offset = %d, want %d", site.AltResumeCodeOff, positions[3])
	}
	if site.Kind != ContinuationGetTable || site.StubID != 42 {
		t.Fatalf("unexpected continuation site: %+v", site)
	}
	if !site.HasAlternateResume() {
		t.Fatalf("expected alternate resume flag on site: %+v", site)
	}
	if !site.UsesNativeBuiltinABI() {
		t.Fatalf("expected native builtin ABI flag on site: %+v", site)
	}
	if !site.DeoptsOnUncovered() {
		t.Fatalf("expected deopt-on-uncovered flag on site: %+v", site)
	}
	if site.Operand0 != 9 || site.Operand1 != 10 || site.Operand2 != 11 || site.LiveSlots != 4 || site.LiveSetID != liveID {
		t.Fatalf("unexpected continuation operands/live slots: %+v", site)
	}
	recorded, ok := metadata.LiveSlotSetAtBytecode(1)
	if !ok {
		t.Fatalf("missing recorded live slot set for bytecode 1")
	}
	if !recorded.HasStaticRegister(1) || !recorded.HasStaticRegister(3) || recorded.HasStaticRegister(0) {
		t.Fatalf("unexpected recorded live slot set: %+v", recorded)
	}
}

func TestLiveSlotSetWalksStaticAndDynamicRegisters(t *testing.T) {
	set := NewLiveSlotSet(8)
	set.AddRegister(1)
	set.AddRegister(5)
	set.SetDynamicTopStart(3)
	var visited []uint32
	if err := set.WalkRegisters(6, func(slot uint32) error {
		visited = append(visited, slot)
		return nil
	}); err != nil {
		t.Fatalf("walk live slot set: %v", err)
	}
	want := []uint32{1, 5, 3, 4}
	if len(visited) != len(want) {
		t.Fatalf("visited live slots = %#v, want %#v", visited, want)
	}
	for index, slot := range want {
		if visited[index] != slot {
			t.Fatalf("visited live slots = %#v, want %#v", visited, want)
		}
	}
}

func TestCodeMetadataWalksHeapRefs(t *testing.T) {
	metadata := NewCodeMetadata(0)
	metadata.AddHeapRef(0x55)
	metadata.AddHeapRef(0)
	metadata.AddHeapRef(0x77)
	var visited []value.HeapRef44
	if err := metadata.WalkHeapRefs(func(ref value.HeapRef44) error {
		visited = append(visited, ref)
		return nil
	}); err != nil {
		t.Fatalf("walk heap refs: %v", err)
	}
	if len(visited) != 2 || visited[0] != 0x55 || visited[1] != 0x77 {
		t.Fatalf("visited heap refs = %#v, want [0x55 0x77]", visited)
	}
}

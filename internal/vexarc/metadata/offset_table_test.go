package metadata

import "testing"

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
		Flags:       ContinuationFlagAlternateResume,
		StubID:      42,
		BytecodePC:  1,
		DeoptPC:     1,
		ResumePC:    2,
		AltResumePC: 3,
		Operand0:    9,
		Operand1:    10,
		Operand2:    11,
		LiveSlots:   4,
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
	if site.Operand0 != 9 || site.Operand1 != 10 || site.Operand2 != 11 || site.LiveSlots != 4 {
		t.Fatalf("unexpected continuation operands/live slots: %+v", site)
	}
}

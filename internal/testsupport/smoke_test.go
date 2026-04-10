package testsupport_test

import (
	"testing"

	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
)

func TestStage0LayoutConstants(t *testing.T) {
	if value.TValueSize != 8 {
		t.Fatalf("unexpected TValue size constant: %d", value.TValueSize)
	}
	if value.CommonHeaderSize != 0x10 {
		t.Fatalf("unexpected common header size: %#x", value.CommonHeaderSize)
	}
	if state.CallFrameHeaderSize != 0x50 {
		t.Fatalf("unexpected call frame header size: %#x", state.CallFrameHeaderSize)
	}
	if state.StubCallBlockSize != 0x30 {
		t.Fatalf("unexpected stub call block size: %#x", state.StubCallBlockSize)
	}
}

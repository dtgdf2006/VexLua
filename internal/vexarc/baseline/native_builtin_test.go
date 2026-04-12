package baseline

import (
	"os"
	"strings"
	"testing"
	"unsafe"

	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/abi"
	"vexlua/internal/vexarc/amd64"
	"vexlua/internal/vexarc/codecache"
	"vexlua/internal/vexarc/stubs"
)

func TestStubManagerInstallsAllCoveredNativeBuiltinBodies(t *testing.T) {
	cache := codecache.New()
	manager, err := newStubManager(cache)
	if err != nil {
		t.Fatalf("new stub manager: %v", err)
	}
	defer func() { _ = manager.Release() }()
	for _, id := range []stubs.ID{
		stubs.StubGetGlobal,
		stubs.StubGetTable,
		stubs.StubSetGlobal,
		stubs.StubSetTable,
		stubs.StubGetUpvalue,
		stubs.StubSetUpvalue,
		stubs.StubLuaCall,
		stubs.StubTailCall,
		stubs.StubForPrep,
		stubs.StubForLoop,
		stubs.StubSetList,
	} {
		if manager.stubBodies[id] == nil {
			t.Fatalf("stub %d should install a native body block", id)
		}
		if manager.stubBlocks[id] == nil {
			t.Fatalf("stub %d should install an entry veneer block", id)
		}
	}
}

func TestStubManagerInstallsNativeTableGlobalBodies(t *testing.T) {
	cache := codecache.New()
	manager, err := newStubManager(cache)
	if err != nil {
		t.Fatalf("new stub manager: %v", err)
	}
	defer func() { _ = manager.Release() }()
	for _, id := range []stubs.ID{stubs.StubGetGlobal, stubs.StubGetTable, stubs.StubSetGlobal, stubs.StubSetTable} {
		if manager.stubBodies[id] == nil {
			t.Fatalf("stub %d should install a native body block", id)
		}
		if manager.stubBlocks[id] == nil {
			t.Fatalf("stub %d should install an entry veneer block", id)
		}
	}
}

func TestNativeBuiltinCallReturnsToCompiledContinuation(t *testing.T) {
	cache := codecache.New()
	manager, err := newStubManager(cache)
	if err != nil {
		t.Fatalf("new stub manager: %v", err)
	}
	defer func() { _ = manager.Release() }()

	want := value.NumberValue(123)
	builtinEntry, err := manager.InstallNativeBuiltin(buildTestContinueBuiltinBody(0, want.Bits(), 0xC0DE))
	if err != nil {
		t.Fatalf("install native builtin: %v", err)
	}

	assembler := amd64.NewAssembler(64)
	assembler.MoveMemImm32(amd64.RegR11, execCtxSiteIDOffset, 19)
	assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, 0)
	assembler.SubRegImm32(amd64.RegRSP, int32(abi.StubCallBlockSize))
	assembler.MoveMemImm32(amd64.RegRSP, int32(state.StubCallBlockStubIDOffset), 0xFEEDBEEF)
	assembler.MoveMemImm32(amd64.RegRSP, int32(state.StubCallBlockFlagsOffset), 0xA5A5A5A5)
	assembler.MoveRegImm64(amd64.RegR10, uint64(builtinEntry))
	assembler.CallReg(amd64.RegR10)
	assembler.AddRegImm32(amd64.RegRSP, int32(abi.StubCallBlockSize))
	assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	assembler.XorRegReg(amd64.RegRDX, amd64.RegRDX)
	assembler.Ret()

	block, err := cache.Install(assembler.Buffer().Bytes())
	if err != nil {
		t.Fatalf("install continuation block: %v", err)
	}
	defer func() { _ = cache.Release(block) }()

	registers := []value.TValue{value.NilValue()}
	results := []value.TValue{value.NilValue()}
	frame := &state.CallFrameHeader{
		Closure:       value.NilValue(),
		Proto:         value.NilValue(),
		RegsBase:      uint64(uintptr(unsafe.Pointer(&registers[0]))),
		ResultBase:    uint64(uintptr(unsafe.Pointer(&results[0]))),
		Flags:         state.FrameFlagIsLuaFrame,
		RegisterCount: 1,
	}
	ctx := executionContext{}
	status, aux := abi.EnterCompiled(block.Address(), 0, nil, unsafe.Pointer(frame), uintptr(unsafe.Pointer(&registers[0])), unsafe.Pointer(&ctx))
	if status != compiledStatusOK || aux != 0 {
		t.Fatalf("native builtin should return to continuation, got status=%d aux=%d", status, aux)
	}
	if registers[0].Bits() != want.Bits() {
		t.Fatalf("native builtin should update register in place, got %s want %s", registers[0], want)
	}
	if ctx.Flags != 0xC0DE {
		t.Fatalf("native builtin should be able to write exec context flags, got %#x", ctx.Flags)
	}
	if ctx.SiteID != 19 {
		t.Fatalf("native builtin call should preserve current site id, got %d", ctx.SiteID)
	}
}

func TestStubManagerUsesVeneerBackedEntries(t *testing.T) {
	source, err := os.ReadFile("stub_manager.go")
	if err != nil {
		t.Fatalf("read stub_manager.go: %v", err)
	}
	text := string(source)
	forbidden := []string{"buildExitStub(compiledStatusStub"}
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Fatalf("stub manager should no longer install legacy status exit blocks via %q", needle)
		}
	}
	required := []string{"buildBuiltinEntryVeneer(", "InstallNativeBuiltin("}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("stub manager should expose veneer-backed native builtin infrastructure %q", needle)
		}
	}
	for _, needle := range []string{"buildGetGlobalBuiltinBody()", "buildGetTableBuiltinBody()", "buildSetGlobalBuiltinBody()", "buildSetTableBuiltinBody()"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("stub manager should install batch-3 native builtin body %q", needle)
		}
	}
}

func TestCompilerStubDispatchUsesBuiltinCallABI(t *testing.T) {
	source, err := os.ReadFile("compiler.go")
	if err != nil {
		t.Fatalf("read compiler.go: %v", err)
	}
	text := string(source)
	required := []string{"emitBuiltinCallWithStubArgs(", "SubRegImm32(amd64.RegRSP, int32(abi.StubCallBlockSize))", "CallReg(amd64.RegR10)"}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("compiler should dispatch shared stubs through builtin call ABI %q", needle)
		}
	}
}

func TestNativeBuiltinABIContractLayout(t *testing.T) {
	if abi.WindowsShadowSpaceSize != 32 {
		t.Fatalf("windows shadow space = %d, want 32", abi.WindowsShadowSpaceSize)
	}
	if abi.StackAlignment != 16 {
		t.Fatalf("stack alignment = %d, want 16", abi.StackAlignment)
	}
	if abi.StubCallBlockSize != state.StubCallBlockSize {
		t.Fatalf("abi stub call block size = %#x, want state %#x", abi.StubCallBlockSize, state.StubCallBlockSize)
	}
	if abi.StubCallBlockSize != abi.WindowsShadowSpaceSize+16 {
		t.Fatalf("stub call block size = %#x, want shadow space + 16-byte contract = %#x", abi.StubCallBlockSize, abi.WindowsShadowSpaceSize+16)
	}
	if got := unsafe.Sizeof(executionContext{}); got != 0x18 {
		t.Fatalf("executionContext size = %#x, want 0x18", got)
	}
	if unsafe.Offsetof(executionContext{}.SiteID) != execCtxSiteIDOffset {
		t.Fatalf("executionContext.SiteID offset = %#x, want %#x", unsafe.Offsetof(executionContext{}.SiteID), execCtxSiteIDOffset)
	}
	if unsafe.Offsetof(executionContext{}.Flags) != execCtxFlagsOffset {
		t.Fatalf("executionContext.Flags offset = %#x, want %#x", unsafe.Offsetof(executionContext{}.Flags), execCtxFlagsOffset)
	}
	if unsafe.Offsetof(executionContext{}.Reserved0) != execCtxReserved0Off {
		t.Fatalf("executionContext.Reserved0 offset = %#x, want %#x", unsafe.Offsetof(executionContext{}.Reserved0), execCtxReserved0Off)
	}
	if unsafe.Offsetof(executionContext{}.Reserved1) != execCtxReserved1Off {
		t.Fatalf("executionContext.Reserved1 offset = %#x, want %#x", unsafe.Offsetof(executionContext{}.Reserved1), execCtxReserved1Off)
	}
	if unsafe.Offsetof(executionContext{}.Reserved2) != execCtxReserved2Off {
		t.Fatalf("executionContext.Reserved2 offset = %#x, want %#x", unsafe.Offsetof(executionContext{}.Reserved2), execCtxReserved2Off)
	}
	if unsafe.Offsetof(executionContext{}.Reserved3) != execCtxReserved3Off {
		t.Fatalf("executionContext.Reserved3 offset = %#x, want %#x", unsafe.Offsetof(executionContext{}.Reserved3), execCtxReserved3Off)
	}
	if err := state.ValidateLayout(); err != nil {
		t.Fatalf("validate frame layout: %v", err)
	}
	if err := state.ValidateThreadStateLayout(); err != nil {
		t.Fatalf("validate thread layout: %v", err)
	}
}

func TestCompiledEntryTrampolinePreservesBuiltinABIRegisters(t *testing.T) {
	source, err := os.ReadFile("../abi/entry_amd64.s")
	if err != nil {
		t.Fatalf("read entry_amd64.s: %v", err)
	}
	text := string(source)
	required := []string{
		"PUSHQ R12",
		"PUSHQ R13",
		"PUSHQ R14",
		"PUSHQ R15",
		"MOVQ heapBase+8(FP), R15",
		"MOVQ vmState+16(FP), R14",
		"MOVQ frame+24(FP), R13",
		"MOVQ regsBase+32(FP), R12",
		"MOVQ execCtx+40(FP), R11",
		"POPQ R15",
		"POPQ R14",
		"POPQ R13",
		"POPQ R12",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("compiled entry trampoline should preserve builtin ABI contract %q", needle)
		}
	}
}

func TestCompilerEmitsNativeBuiltinContinuationContracts(t *testing.T) {
	compilerSource, err := os.ReadFile("compiler.go")
	if err != nil {
		t.Fatalf("read compiler.go: %v", err)
	}
	compilerText := string(compilerSource)
	for _, needle := range []string{
		"recordContinuationSite(metadata.ContinuationCall, stubs.StubLuaCall",
		"recordContinuationSite(metadata.ContinuationSetList, stubs.StubSetList",
		"recordContinuationSite(metadata.ContinuationGetUpvalue, stubs.StubGetUpvalue",
		"recordContinuationSite(metadata.ContinuationSetUpvalue, stubs.StubSetUpvalue",
		"recordContinuationSite(metadata.ContinuationTailCall, stubs.StubTailCall",
		"metadata.ContinuationFlagFinalExit|metadata.ContinuationFlagNativeBuiltinABI",
		"recordContinuationSite(metadata.ContinuationForPrep, stubs.StubForPrep",
		"recordContinuationSite(metadata.ContinuationForLoop, stubs.StubForLoop",
		"metadata.ContinuationFlagAlternateResume|metadata.ContinuationFlagNativeBuiltinABI",
	} {
		if !strings.Contains(compilerText, needle) {
			t.Fatalf("compiler should lock builtin continuation metadata contract %q", needle)
		}
	}
	tableSource, err := os.ReadFile("table_lowering.go")
	if err != nil {
		t.Fatalf("read table_lowering.go: %v", err)
	}
	tableText := string(tableSource)
	for _, needle := range []string{
		"recordContinuationSite(metadata.ContinuationGetGlobal, stubs.StubGetGlobal",
		"recordContinuationSite(metadata.ContinuationGetTable, stubs.StubGetTable",
		"recordContinuationSite(metadata.ContinuationSetGlobal, stubs.StubSetGlobal",
		"recordContinuationSite(metadata.ContinuationSetTable, stubs.StubSetTable",
		"metadata.ContinuationFlagNativeBuiltinABI|metadata.ContinuationFlagDeoptOnUncovered",
	} {
		if !strings.Contains(tableText, needle) {
			t.Fatalf("table lowering should lock builtin continuation metadata contract %q", needle)
		}
	}
}

func buildTestContinueBuiltinBody(slot int, bits value.Raw, flags uint32) []byte {
	assembler := amd64.NewAssembler(32)
	assembler.MoveMemImm32(amd64.RegR11, execCtxFlagsOffset, flags)
	assembler.MoveRegImm64(amd64.RegRAX, uint64(bits))
	assembler.MoveMemReg64(amd64.RegR12, slotDisp(slot), amd64.RegRAX)
	assembler.Ret()
	return assembler.Buffer().Bytes()
}

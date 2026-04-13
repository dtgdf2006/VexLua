package baseline

import (
	"os"
	"strings"
	"testing"
)

func TestTableLoweringStaysOnFastPathDispatchLayer(t *testing.T) {
	source, err := os.ReadFile("table_lowering.go")
	if err != nil {
		t.Fatalf("read table_lowering.go: %v", err)
	}
	text := string(source)
	forbidden := []string{
		"WriteFeedbackCell",
		"ReadFeedbackCell",
		"UpdateTableFeedback",
		"DescribeFastGet",
		"DescribeFastSet",
		"Tables.Get(",
		"Tables.Set(",
		"hostObject",
		"compiledStatusDeopt",
		"emitStubExit(stubs.StubGetGlobal",
		"emitStubExit(stubs.StubGetTable",
		"emitStubExit(stubs.StubSetGlobal",
		"emitStubExit(stubs.StubSetTable",
	}
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Fatalf("table lowering should remain guard/dispatch glue only, found forbidden dependency %q", needle)
		}
	}
	required := []string{
		"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubGetGlobal]",
		"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubGetTable]",
		"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSetGlobal]",
		"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSetTable]",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("table lowering should dispatch through native builtin ABI %q", needle)
		}
	}
}

func TestRuntimeStubsUseSharedHostBoundary(t *testing.T) {
	source, err := os.ReadFile("runtime_stubs.go")
	if err != nil {
		t.Fatalf("read runtime_stubs.go: %v", err)
	}
	text := string(source)
	forbidden := []string{
		"Engine.Call(",
		"hostObjectGet",
		"hostObjectSet",
		"callHostFunction",
	}
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Fatalf("runtime stubs should not route host continuation through %q", needle)
		}
	}
	required := []string{
		"ReadIndexMetaBoundary",
		"WriteIndexMetaBoundary",
		"CallResolvedBoundary",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("runtime stubs should depend on shared host boundary %q", needle)
		}
	}
}

func TestNativeBuiltinStringKeyFlowsDoNotSpecialCaseHostObjects(t *testing.T) {
	source, err := os.ReadFile("native_builtin.go")
	if err != nil {
		t.Fatalf("read native_builtin.go: %v", err)
	}
	text := string(source)
	getFlowStart := strings.Index(text, "func emitGenericStringKeyGetFlow(")
	setFlowStart := strings.Index(text, "func emitGenericStringKeySetFlow(")
	updateFlowStart := strings.Index(text, "func emitUpdateTableFeedbackEligibleHash(")
	if getFlowStart < 0 || setFlowStart < 0 || updateFlowStart < 0 {
		t.Fatalf("could not locate generic string-key native builtin flow blocks")
	}
	getFlowText := text[getFlowStart:setFlowStart]
	setFlowText := text[setFlowStart:updateFlowStart]
	forbidden := []string{
		"shiftedBoxedTag(value.TagHostObjectRef)",
		"hostPath := assembler.NewLabel()",
	}
	for _, needle := range forbidden {
		if strings.Contains(getFlowText, needle) || strings.Contains(setFlowText, needle) {
			t.Fatalf("string-key get/set native builtins should route host objects through shared exact helper, found %q", needle)
		}
	}
}

func TestNativeBuiltinCallFlowsKeepHostTerminalBoundaryExplicit(t *testing.T) {
	source, err := os.ReadFile("native_builtin.go")
	if err != nil {
		t.Fatalf("read native_builtin.go: %v", err)
	}
	text := string(source)
	callStart := strings.Index(text, "func buildLuaCallBuiltinBody() []byte {")
	tailStart := strings.Index(text, "func buildTailCallBuiltinBody() []byte {")
	if callStart < 0 || tailStart < 0 || tailStart <= callStart {
		t.Fatalf("failed to locate call/tailcall builtin bodies")
	}
	callFlow := text[callStart:tailStart]
	tailHelperStart := strings.Index(text[tailStart:], "func emitBranchMegamorphicCallFeedback(")
	if tailHelperStart < 0 {
		t.Fatalf("failed to locate tailcall builtin end")
	}
	tailFlow := text[tailStart : tailStart+tailHelperStart]
	for _, flow := range []string{callFlow, tailFlow} {
		if !strings.Contains(flow, "emitReturnHostCallDispatch(") {
			t.Fatalf("call/tailcall native builtins should keep explicit host terminal dispatch helper")
		}
	}
	forbidden := []string{
		"shiftedBoxedTag(value.TagHostObjectRef)",
		"emitRefreshHostObjectWrapper(",
	}
	for _, needle := range forbidden {
		if strings.Contains(callFlow, needle) || strings.Contains(tailFlow, needle) {
			t.Fatalf("call/tailcall native builtins should not special-case host objects, found %q", needle)
		}
	}
}

func TestCompilerAndStubManagerRegisterSharedUpvalueStubs(t *testing.T) {
	files := []string{"compiler.go", "stub_manager.go"}
	required := []string{"StubGetUpvalue", "StubSetUpvalue"}
	for _, name := range files {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(source)
		for _, needle := range required {
			if !strings.Contains(text, needle) {
				t.Fatalf("%s should register shared upvalue stub %q", name, needle)
			}
		}
	}
}

func TestStubManagerInstallsNativeUpvalueAndLoopBodies(t *testing.T) {
	source, err := os.ReadFile("stub_manager.go")
	if err != nil {
		t.Fatalf("read stub_manager.go: %v", err)
	}
	text := string(source)
	required := []string{
		"buildGetGlobalBuiltinBody()",
		"buildGetTableBuiltinBody()",
		"buildSetGlobalBuiltinBody()",
		"buildSetTableBuiltinBody()",
		"buildSetListBuiltinBody()",
		"buildNewTableBuiltinBody()",
		"buildConcatBuiltinBody()",
		"buildCloseBuiltinBody()",
		"buildClosureBuiltinBody()",
		"buildGetUpvalueBuiltinBody()",
		"buildSetUpvalueBuiltinBody()",
		"buildForPrepBuiltinBody()",
		"buildForLoopBuiltinBody()",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("stub manager should install native builtin body %q", needle)
		}
	}
}

func TestCompilerRegistersAllocationBoundaryStubs(t *testing.T) {
	source, err := os.ReadFile("compiler.go")
	if err != nil {
		t.Fatalf("read compiler.go: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"StubNewTable", "StubConcat", "StubClose", "StubClosure"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("compiler should register allocation/builder stub %q", needle)
		}
	}
}

func TestStubManagerInstallsNativeCallAndTailBodies(t *testing.T) {
	source, err := os.ReadFile("stub_manager.go")
	if err != nil {
		t.Fatalf("read stub_manager.go: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"buildLuaCallBuiltinBody()", "buildTailCallBuiltinBody()"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("stub manager should install batch-4 native builtin body %q", needle)
		}
	}
}

func TestRuntimeStubsFinishNestedCompiledCalls(t *testing.T) {
	source, err := os.ReadFile("runtime_stubs.go")
	if err != nil {
		t.Fatalf("read runtime_stubs.go: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"finishNestedCompiledCall(", "compiledFrameState(", "resumeCompiledFrame("} {
		if !strings.Contains(text, needle) {
			t.Fatalf("runtime stubs should keep batch-4 nested compiled call recovery helper %q", needle)
		}
	}
}

func TestRuntimeStubsDoNotOwnBatch3NativeBuiltins(t *testing.T) {
	source, err := os.ReadFile("runtime_stubs.go")
	if err != nil {
		t.Fatalf("read runtime_stubs.go: %v", err)
	}
	text := string(source)
	forbidden := []string{
		"Tables.Get(",
		"Tables.Set(",
		"recordTableStubFeedback(",
		"feedbackSlotForSite(",
	}
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Fatalf("runtime stubs should not keep batch-3 table/global ownership via %q", needle)
		}
	}
}

func TestRuntimeStubsDoNotOwnBatch2NativeBuiltins(t *testing.T) {
	source, err := os.ReadFile("runtime_stubs.go")
	if err != nil {
		t.Fatalf("read runtime_stubs.go: %v", err)
	}
	text := string(source)
	forbidden := []string{
		"case stubs.StubGetUpvalue:",
		"case stubs.StubSetUpvalue:",
		"case stubs.StubForLoop:",
		"handleGetUpvalueStub(",
		"handleSetUpvalueStub(",
		"handleForLoopStub(",
	}
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Fatalf("runtime stubs should not keep batch-2 native builtin ownership via %q", needle)
		}
	}
	for _, needle := range []string{"case stubs.StubForPrep:", "handleForPrepStub("} {
		if !strings.Contains(text, needle) {
			t.Fatalf("runtime stubs should own FORPREP string/slow coercion recovery via %q", needle)
		}
	}
}

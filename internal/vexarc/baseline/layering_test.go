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
		"ReadIndexBoundary",
		"WriteIndexBoundary",
		"CallValueBoundary",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("runtime stubs should depend on shared host boundary %q", needle)
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
		"case stubs.StubForPrep:",
		"case stubs.StubForLoop:",
		"handleGetUpvalueStub(",
		"handleSetUpvalueStub(",
		"handleForPrepStub(",
		"handleForLoopStub(",
	}
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Fatalf("runtime stubs should not keep batch-2 native builtin ownership via %q", needle)
		}
	}
}

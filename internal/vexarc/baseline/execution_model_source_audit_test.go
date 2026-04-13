package baseline

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionModelSourceAudit(t *testing.T) {
	root := repoRoot(t)
	t.Run("legacy-helper-names-absent", func(t *testing.T) {
		forbidden := []string{
			"compiledRunsWithoutSuspend",
			"recordTableFeedback",
			"func (engine *Engine) hostObjectGet",
			"func (engine *Engine) hostObjectSet",
			"func (engine *Engine) callHostFunction",
		}
		var hits []string
		err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(content)
			for _, needle := range forbidden {
				if strings.Contains(text, needle) {
					hits = append(hits, filepath.ToSlash(path)+": "+needle)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk internal sources: %v", err)
		}
		if len(hits) != 0 {
			t.Fatalf("found residual execution-model helpers or heuristics: %v", hits)
		}
	})

	t.Run("transition-cleanup-residue-absent", func(t *testing.T) {
		forbidden := []string{
			"constByProto",
			"resolveCount",
			"ResolveCount(",
			"CallSuspendCount(",
			"ForPrepSuspendCount(",
			"ForLoopSuspendCount(",
			"syncCompiledStoreWriteback",
			"ResolveNative",
			"SyncNative",
		}
		var hits []string
		err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(content)
			for _, needle := range forbidden {
				if strings.Contains(text, needle) {
					hits = append(hits, filepath.ToSlash(path)+": "+needle)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk internal sources: %v", err)
		}
		if len(hits) != 0 {
			t.Fatalf("found residual transition cleanup symbols: %v", hits)
		}
	})

	t.Run("runtime-pinner-stays-execution-state-only", func(t *testing.T) {
		allowed := map[string]struct{}{
			"internal/runtime/state/thread.go": {},
		}
		var hits []string
		err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !strings.Contains(string(content), "runtime.Pinner") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if _, ok := allowed[rel]; !ok {
				hits = append(hits, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk internal sources: %v", err)
		}
		if len(hits) != 0 {
			t.Fatalf("runtime.Pinner should remain execution-state only, found: %v", hits)
		}
	})

	t.Run("activation-struct-caches-hot-state-only", func(t *testing.T) {
		text := readRepoFile(t, "internal", "interp", "engine.go")
		block := sourceBlock(t, text, "type activation struct {", "type threadSnapshot struct {")
		required := []string{"fn     *bytecode.Proto", "code   []bytecode.Instruction", "callee value.HeapRef44", "slots  []value.TValue"}
		for _, needle := range required {
			if !strings.Contains(block, needle) {
				t.Fatalf("activation struct should cache hot state %q: %s", needle, block)
			}
		}
		forbidden := []string{"closureObject", "upvalueRefs", "registerBase", "baseAddress", "reservedSlots"}
		for _, needle := range forbidden {
			if strings.Contains(block, needle) {
				t.Fatalf("activation struct should not cache broad derived state %q: %s", needle, block)
			}
		}
	})

	t.Run("feedback-path-stays-shared", func(t *testing.T) {
		text := readRepoFile(t, "internal", "interp", "feedback.go")
		forbidden := []string{"recordTableFeedback", "LayoutForProto("}
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Fatalf("feedback path should not depend on %q", needle)
			}
		}
		required := []string{"UpdateTableFeedbackAtPC", "UpdateTableFeedbackAtSlot"}
		for _, needle := range required {
			if !strings.Contains(text, needle) {
				t.Fatalf("feedback path should expose shared helper %q", needle)
			}
		}
	})

	t.Run("covered-lowering-uses-shared-stubs", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "table_lowering.go")
		forbidden := []string{"emitDeoptExit(", "compiledStatusDeopt", "Engine.Call("}
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Fatalf("table lowering should not fall back through %q", needle)
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
				t.Fatalf("table lowering should dispatch through %q", needle)
			}
		}
	})

	t.Run("upvalue-lowering-uses-shared-stubs", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "compiler.go")
		required := []string{"bytecode.OP_GETUPVAL", "bytecode.OP_SETUPVAL", "emitGetUpvalue(", "emitSetUpvalue(", "stubs.StubGetUpvalue", "stubs.StubSetUpvalue"}
		for _, needle := range required {
			if !strings.Contains(text, needle) {
				t.Fatalf("upvalue lowering should route through shared stub %q", needle)
			}
		}
	})

	t.Run("phase0-family-boundaries-stay-frozen", func(t *testing.T) {
		compilerText := readRepoFile(t, "internal", "vexarc", "baseline", "compiler.go")
		pureJITText := readRepoFile(t, "internal", "vexarc", "baseline", "pure_jit_lowering.go")
		tableText := readRepoFile(t, "internal", "vexarc", "baseline", "table_lowering.go")
		runtimeText := readRepoFile(t, "internal", "vexarc", "baseline", "runtime_stubs.go")
		stubManagerText := readRepoFile(t, "internal", "vexarc", "baseline", "stub_manager.go")

		for _, needle := range []string{
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubLuaCall]",
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubTailCall]",
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSetList]",
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubGetUpvalue]",
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSetUpvalue]",
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubForPrep]",
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubForLoop]",
		} {
			if !strings.Contains(compilerText, needle) {
				t.Fatalf("phase-0 native builtin boundary should keep %q", needle)
			}
		}

		for _, needle := range []string{
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubGetGlobal]",
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubGetTable]",
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSetGlobal]",
			"emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSetTable]",
		} {
			if !strings.Contains(tableText, needle) {
				t.Fatalf("phase-0 native builtin boundary should keep table/global lowering through %q", needle)
			}
		}

		for _, needle := range []string{
			"func (state *compileState) emitArithmetic(",
			"func (state *compileState) emitSelf(",
			"func (state *compileState) emitLength(",
			"func (state *compileState) emitCompare(",
		} {
			if !strings.Contains(pureJITText, needle) {
				t.Fatalf("phase-0 inline family should stay owned by local emitter %q", needle)
			}
		}
		if !strings.Contains(compilerText, "func (state *compileState) emitReturnInstruction(") {
			t.Fatalf("phase-0 inline family should keep RETURN ownership local to compiler emitter")
		}

		if strings.Contains(compilerText, "state.compiler.stubEntries[stubs.StubLen]") || strings.Contains(tableText, "state.compiler.stubEntries[stubs.StubLen]") {
			t.Fatalf("LEN should stay owned by the local length emitter instead of table/global lowering")
		}
		if !strings.Contains(pureJITText, "state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubArithmetic]") {
			t.Fatalf("ARITHMETIC should route uncovered coercion/metamethod paths through the shared arithmetic stub")
		}
		if !strings.Contains(stubManagerText, "buildArithmeticBuiltinBody()") {
			t.Fatalf("stub manager should install the arithmetic builtin veneer for shared runtime dispatch")
		}
		if !strings.Contains(pureJITText, "state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubLen]") {
			t.Fatalf("LEN should route exact luaH_getn / TM_LEN paths through the shared len stub")
		}
		if !strings.Contains(stubManagerText, "buildLenBuiltinBody()") {
			t.Fatalf("stub manager should install the len builtin veneer for shared runtime dispatch")
		}
		if !strings.Contains(pureJITText, "state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubSelf]") {
			t.Fatalf("SELF should route uncovered metatable paths through the shared self stub")
		}
		if !strings.Contains(pureJITText, "state.emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubCompare]") {
			t.Fatalf("COMPARE should route uncovered exact-semantic paths through the shared compare stub")
		}
		if !strings.Contains(stubManagerText, "buildCompareBuiltinBody()") {
			t.Fatalf("stub manager should install the compare builtin veneer for shared runtime dispatch")
		}

		if !strings.Contains(runtimeText, "case stubs.StubLen:") {
			t.Fatalf("LEN should be owned by the shared runtime dispatcher once it exits the string fast path")
		}
		if !strings.Contains(runtimeText, "case stubs.StubArithmetic:") {
			t.Fatalf("ARITHMETIC should be owned by the shared runtime dispatcher once it exits the inline fast path")
		}
		if !strings.Contains(runtimeText, "case stubs.StubSelf:") {
			t.Fatalf("SELF should be owned by the shared runtime dispatcher once it exits to the exact helper path")
		}
		if !strings.Contains(runtimeText, "case stubs.StubCompare:") {
			t.Fatalf("COMPARE should be owned by the shared runtime dispatcher once it exits the inline fast path")
		}
	})

	t.Run("batch2-families-leave-runtime-dispatcher", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "runtime_stubs.go")
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
				t.Fatalf("batch-2 native builtin families should not remain in runtime dispatcher via %q", needle)
			}
		}
		for _, needle := range []string{"case stubs.StubForPrep:", "handleForPrepStub("} {
			if !strings.Contains(text, needle) {
				t.Fatalf("FORPREP should keep its slow coercion path in the shared runtime dispatcher via %q", needle)
			}
		}
	})

	t.Run("batch2-native-bodies-are-installed", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "stub_manager.go")
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
				t.Fatalf("stub manager should install batch-2 native body %q", needle)
			}
		}
	})

	t.Run("batch4-native-bodies-are-installed", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "stub_manager.go")
		for _, needle := range []string{"buildLuaCallBuiltinBody()", "buildTailCallBuiltinBody()"} {
			if !strings.Contains(text, needle) {
				t.Fatalf("stub manager should install batch-4 native body %q", needle)
			}
		}
	})

	t.Run("batch4-runtime-dispatch-stays-terminal-only", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "runtime_stubs.go")
		for _, needle := range []string{"finishNestedCompiledCall(", "compiledFrameState(", "resumeCompiledFrame("} {
			if !strings.Contains(text, needle) {
				t.Fatalf("batch-4 runtime boundary should keep nested compiled recovery helper %q", needle)
			}
		}
	})

	t.Run("batch4-native-call-feedback-observes-polymorphic-sidecar", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "native_builtin.go")
		required := []string{
			"feedback.PackCellPrefix(feedback.StatePolymorphic, feedback.AccessInvalid, slotKind)",
			"feedback.CallPolyEntryAccessKindOffset",
			"feedback.CallPolyEntryTargetRefOffset",
			"feedback.CallPolyEntryValueBitsOffset",
			"emitLoadCallPolymorphicDataBaseFromCell(",
		}
		for _, needle := range required {
			if !strings.Contains(text, needle) {
				t.Fatalf("batch-4 native call builtin should keep polymorphic call-feedback support %q", needle)
			}
		}
	})

	t.Run("batch4-native-resolved-lua-closure-guard-supports-all-callable-shapes", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "native_builtin.go")
		block := sourceBlock(t, text, "func emitMatchResolvedLuaClosureEntry(", "func emitMatchResolvedHostFunctionEntry(")
		required := []string{
			"feedback.CallShapeHostObjectMetatable",
			"feedback.CallShapeTypeMetatable",
			"value.TagHostObjectRef",
			"rthost.WrapperMetatableVersionOffset",
			"rthost.WrapperMetatableOffset",
			"emitLoadTypeMetatableKind(",
			"rtstate.VMStateTypeMetatableStateOff",
			"rtmeta.RegistryEntryVersionOffset",
			"rtmeta.RegistryEntryMetatableOffset",
		}
		for _, needle := range required {
			if !strings.Contains(block, needle) {
				t.Fatalf("resolved-lua native guard should keep callable-shape coverage %q", needle)
			}
		}
	})

	t.Run("covered-families-do-not-install-legacy-status-exits", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "stub_manager.go")
		if strings.Contains(text, "buildExitStub(compiledStatusStub") {
			t.Fatalf("covered native builtin families should not be installed as legacy compiledStatusStub exits")
		}
		for _, needle := range []string{
			"buildGetGlobalBuiltinBody()",
			"buildGetTableBuiltinBody()",
			"buildSetGlobalBuiltinBody()",
			"buildSetTableBuiltinBody()",
			"buildSetListBuiltinBody()",
			"buildGetUpvalueBuiltinBody()",
			"buildSetUpvalueBuiltinBody()",
			"buildLuaCallBuiltinBody()",
			"buildTailCallBuiltinBody()",
			"buildForPrepBuiltinBody()",
			"buildForLoopBuiltinBody()",
		} {
			if !strings.Contains(text, needle) {
				t.Fatalf("covered native builtin family should install real body %q", needle)
			}
		}
	})

	t.Run("call-tail-runtime-boundaries-stay-terminal", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "runtime_stubs.go")
		callBlock := sourceBlock(t, text, "func (runtime *Runtime) handleCallStub(", "func (runtime *Runtime) handleTailCallStub(")
		for _, needle := range []string{"finishNestedCompiledCall(", "callResolvedBoundary(", "storeFrameCallResults(", "resumeCallContinuation("} {
			if !strings.Contains(callBlock, needle) {
				t.Fatalf("handleCallStub should keep terminal-boundary glue %q", needle)
			}
		}
		for _, needle := range []string{"runtime.Call(", "Engine.CallValueBoundary("} {
			if strings.Contains(callBlock, needle) {
				t.Fatalf("handleCallStub should not own covered call semantics through %q", needle)
			}
		}
		tailBlock := sourceBlock(t, text, "func (runtime *Runtime) handleTailCallStub(", "func (runtime *Runtime) callValueBoundary(")
		for _, needle := range []string{"finishNestedCompiledCall(", "callResolvedBoundary(", "FrameFlagIsTailcall"} {
			if !strings.Contains(tailBlock, needle) {
				t.Fatalf("handleTailCallStub should keep terminal-boundary glue %q", needle)
			}
		}
		for _, needle := range []string{"runtime.Call(", "Engine.CallValueBoundary("} {
			if strings.Contains(tailBlock, needle) {
				t.Fatalf("handleTailCallStub should not own covered tailcall semantics through %q", needle)
			}
		}
	})

	t.Run("tforloop-lowering-stays-call-builtin-plus-local-continuation", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "compiler.go")
		block := sourceBlock(t, text, "func (state *compileState) emitTForLoopInstruction(", "func (state *compileState) emitCallInstruction(")
		for _, needle := range []string{"feedback.SlotCall", "recordContinuationSite(metadata.ContinuationCall, stubs.StubLuaCall", "metadata.ContinuationFlagAlternateResume|metadata.ContinuationFlagNativeBuiltinABI", "emitBuiltinCallWithStubArgs(state.compiler.stubEntries[stubs.StubLuaCall]"} {
			if !strings.Contains(block, needle) {
				t.Fatalf("emitTForLoopInstruction should keep call-family lowering via %q", needle)
			}
		}
		if strings.Contains(block, "emitUncoveredInstructionDeopt(offset)") && !strings.Contains(block, "instruction.C() == 0") {
			t.Fatalf("emitTForLoopInstruction should only deopt the unsupported zero-result shape")
		}
	})

	t.Run("tforloop-runtime-continuation-keeps-control-and-alternate-resume", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "runtime_stubs.go")
		block := sourceBlock(t, text, "func (runtime *Runtime) resumeCallContinuation(", "func (runtime *Runtime) handleTailCallStub(")
		for _, needle := range []string{"site.HasAlternateResume() && site.Operand3 != 0", "thread.Register(frame, uint16(site.Operand0+3))", "thread.SetRegister(frame, uint16(site.Operand3), firstResult)", "compiled.EntryAtSite(site, true)"} {
			if !strings.Contains(block, needle) {
				t.Fatalf("resumeCallContinuation should preserve TFORLOOP alternate resume contract via %q", needle)
			}
		}
	})

	t.Run("setlist-runtime-boundary-stays-growth-only", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "runtime_stubs.go")
		setListBlock := sourceBlock(t, text, "func (runtime *Runtime) handleSetListStub(", "func (runtime *Runtime) handleCallStub(")
		for _, needle := range []string{"SetListArray(", "deoptThroughSite("} {
			if !strings.Contains(setListBlock, needle) {
				t.Fatalf("handleSetListStub should keep narrow growth/deopt boundary %q", needle)
			}
		}
		for _, needle := range []string{"WriteIndexBoundary("} {
			if strings.Contains(setListBlock, needle) {
				t.Fatalf("handleSetListStub should not retain generic boundary write via %q", needle)
			}
		}
	})

	t.Run("batch3-families-leave-runtime-dispatcher", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "runtime_stubs.go")
		forbidden := []string{
			"Tables.Get(",
			"Tables.Set(",
			"recordTableStubFeedback(",
			"feedbackSlotForSite(",
		}
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Fatalf("batch-3 native builtin families should not retain table/global ownership via %q", needle)
			}
		}
	})

	t.Run("docs-have-no-execution-model-tail", func(t *testing.T) {
		forbidden := []string{"Stage 8 再补 metadata", "后面再把 host bridge shared stub 化", "先保留解释器 feedback 状态机", "暂时先靠 Go 调度", "后续再对齐"}
		docs := []string{
			readRepoFile(t, "DEV.md"),
			readRepoFile(t, "docs", "design", "table-binary-contract.md"),
			readRepoFile(t, "docs", "design", "callframe-abi-contract.md"),
		}
		for _, text := range docs {
			for _, needle := range forbidden {
				if strings.Contains(text, needle) {
					t.Fatalf("docs should not retain execution-model tail item %q", needle)
				}
			}
		}
	})
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "TODO.md")); err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	allParts := append([]string{repoRoot(t)}, parts...)
	path := filepath.Join(allParts...)
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.ToSlash(path), err)
	}
	return string(bytes)
}

func sourceBlock(t *testing.T, text string, startMarker string, endMarker string) string {
	t.Helper()
	start := strings.Index(text, startMarker)
	if start < 0 {
		t.Fatalf("missing source block start %q", startMarker)
	}
	end := strings.Index(text[start:], endMarker)
	if end < 0 {
		t.Fatalf("missing source block end %q", endMarker)
	}
	return text[start : start+end]
}

package baseline

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/value"
)

func TestExecutionModelFinalAcceptanceMatrix(t *testing.T) {
	t.Run("compiled-island-call-chain", TestBaselineRuntimeCallStubChainsAcrossCompiledFrames)
	t.Run("nested-compiled-call-host-boundary", TestBaselineRuntimeNestedCompiledCallCanReachHostBoundary)
	t.Run("native-builtin-island-composition", TestBaselineRuntimeNativeBuiltinCompositionStaysInCompiledIsland)
	t.Run("compiled-proto-cache", TestBaselineRuntimeCachesCompiledProtoByRef)
	t.Run("shared-stub-feedback", TestBaselineRuntimeFeedbackTransitionsMatchInterpreter)
	t.Run("table-slow-stub-call-continuation", TestBaselineRuntimeTableSlowStubContinuesIntoCall)
	t.Run("metatable-blocker-slow-stub", TestBaselineRuntimeMetatableBlockedTableUsesSharedSlowStub)
	t.Run("setglobal-native-builtin", TestBaselineRuntimeCompiledSetGlobalFastPathAfterWarmup)
	t.Run("real-deopt-continuation", TestBaselineRuntimeDeoptResumesWithoutReplayingEarlierSideEffects)
	t.Run("host-bridge-continuation", TestBaselineRuntimeHostBridgeStubsResumeCompiledContinuation)
	t.Run("host-descriptor-refresh", TestBaselineRuntimeHostDescriptorRefreshKeepsCompiledContinuation)
	t.Run("upvalue-get-stub-continuation", TestBaselineRuntimeUpvalueStubsResumeCompiledContinuation)
	t.Run("upvalue-set-stub-continuation", TestBaselineRuntimeSetUpvalueStubResumesCompiledContinuation)
	t.Run("closed-upvalue-native-builtin", TestBaselineRuntimeClosedUpvalueNativeBuiltinResumesCompiledContinuation)
	t.Run("upvalue-open-return-top", TestBaselineRuntimeOpenReturnUsesUpdatedTopAfterGetUpvalueBuiltin)
	t.Run("host-getglobal-open-return-top", TestBaselineRuntimeOpenReturnUsesUpdatedTopAfterHostGetGlobalStub)
	t.Run("vararg-fixed", TestBaselineRuntimeCompiledVarargFixedCountWithNilFill)
	t.Run("vararg-open", TestBaselineRuntimeCompiledVarargOpenFormSupportsOpenReturn)
	t.Run("setlist-fast-path", TestBaselineRuntimeCompiledSetListFastPath)
	t.Run("setlist-open-count", TestBaselineRuntimeCompiledSetListOpenCountFromVararg)
	t.Run("tforloop-lua-iterator", TestBaselineRuntimeCompiledTForLoopWithLuaIterator)
	t.Run("tforloop-host-boundary", TestBaselineRuntimeTForLoopHostBoundaryResumesCompiledContinuation)
	t.Run("tforloop-open-return-top", TestBaselineRuntimeTForLoopPreservesOpenReturnTop)
	t.Run("setlist-payload-fallback", TestBaselineRuntimeSetListPayloadFallbackResumesContinuation)
	t.Run("tailcall-continuation", TestBaselineRuntimeCompiledTailCall)
	t.Run("nested-compiled-tailcall-host-boundary", TestBaselineRuntimeNestedCompiledTailCallCanReachHostBoundary)
	t.Run("loop-backedge-continuation", TestBaselineRuntimeLoopSlowStubResumesContinuation)
	t.Run("activation-convergence", testExecutionModelActivationConvergence)
	t.Run("table-layering-audit", TestTableLoweringStaysOnFastPathDispatchLayer)
	t.Run("runtime-stub-host-boundary-audit", TestRuntimeStubsUseSharedHostBoundary)
	t.Run("runtime-stub-batch3-native-audit", TestRuntimeStubsDoNotOwnBatch3NativeBuiltins)
	t.Run("runtime-stub-batch2-native-audit", TestRuntimeStubsDoNotOwnBatch2NativeBuiltins)
	t.Run("stub-manager-batch2-native-install-audit", TestStubManagerInstallsNativeUpvalueAndLoopBodies)
	t.Run("stub-manager-covered-native-install-audit", TestStubManagerInstallsAllCoveredNativeBuiltinBodies)
	t.Run("stub-manager-batch4-native-install-audit", TestStubManagerInstallsNativeCallAndTailBodies)
	t.Run("runtime-stub-batch4-nested-audit", TestRuntimeStubsFinishNestedCompiledCalls)
	t.Run("native-builtin-abi-layout", TestNativeBuiltinABIContractLayout)
	t.Run("native-builtin-entry-trampoline-contract", TestCompiledEntryTrampolinePreservesBuiltinABIRegisters)
	t.Run("native-builtin-continuation-metadata-contract", TestCompilerEmitsNativeBuiltinContinuationContracts)
}

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

	t.Run("activation-struct-stays-thin", func(t *testing.T) {
		text := readRepoFile(t, "internal", "interp", "engine.go")
		block := sourceBlock(t, text, "type activation struct {", "type threadSnapshot struct {")
		forbidden := []string{"proto", "env", "closureObject", "upvalueRefs", "registerBase", "baseAddress", "reservedSlots"}
		for _, needle := range forbidden {
			if strings.Contains(block, needle) {
				t.Fatalf("activation struct should not cache derived state %q: %s", needle, block)
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

		for _, needle := range []string{
			"state.compiler.stubEntries[stubs.StubSelf]",
			"state.compiler.stubEntries[stubs.StubLen]",
			"state.compiler.stubEntries[stubs.StubArithmetic]",
			"state.compiler.stubEntries[stubs.StubCompare]",
		} {
			if strings.Contains(compilerText, needle) || strings.Contains(pureJITText, needle) || strings.Contains(tableText, needle) {
				t.Fatalf("phase-0 inline families should not be promoted to shared native builtin dispatch via %q", needle)
			}
		}

		for _, needle := range []string{
			"case stubs.StubSelf:",
			"case stubs.StubLen:",
			"case stubs.StubArithmetic:",
			"case stubs.StubCompare:",
		} {
			if strings.Contains(runtimeText, needle) {
				t.Fatalf("phase-0 inline families should not move runtime ownership into shared dispatcher via %q", needle)
			}
		}
	})

	t.Run("batch2-families-leave-runtime-dispatcher", func(t *testing.T) {
		text := readRepoFile(t, "internal", "vexarc", "baseline", "runtime_stubs.go")
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
				t.Fatalf("batch-2 native builtin families should not remain in runtime dispatcher via %q", needle)
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
		for _, needle := range []string{"finishNestedCompiledCall(", "callValueBoundary(", "storeFrameCallResults(", "resumeCallContinuation("} {
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
		for _, needle := range []string{"finishNestedCompiledCall(", "callValueBoundary(", "FrameFlagIsTailcall"} {
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

func testExecutionModelActivationConvergence(t *testing.T) {
	engine := interp.New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}
	childOne := &bytecode.Proto{
		Source:       "@accept-child-one.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(10),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	childTwo := &bytecode.Proto{
		Source:       "@accept-child-two.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.NumberConstant(20),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_LOADK, 0, 0),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	outerA := &bytecode.Proto{
		Source:       "@accept-outer-a.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("swapProto"),
		},
		Protos: []*bytecode.Proto{childOne},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 1),
			bytecode.CreateABx(bytecode.OP_CLOSURE, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	outerB := &bytecode.Proto{
		Source:       "@accept-outer-b.lua",
		MaxStackSize: 1,
		Constants: []bytecode.Constant{
			bytecode.StringConstant("swapProto"),
		},
		Protos: []*bytecode.Proto{childTwo},
		Code: []bytecode.Instruction{
			bytecode.CreateABx(bytecode.OP_GETGLOBAL, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 1),
			bytecode.CreateABx(bytecode.OP_CLOSURE, 0, 0),
			bytecode.CreateABC(bytecode.OP_CALL, 0, 1, 2),
			bytecode.CreateABC(bytecode.OP_RETURN, 0, 2, 0),
		},
	}
	outerBHandle, err := engine.Protos.Intern(outerB)
	if err != nil {
		t.Fatalf("intern alternate proto: %v", err)
	}
	swapProto, err := engine.RegisterHostFunction("swapProto", func() {
		frame := thread.CurrentFrame()
		if frame == nil {
			t.Fatalf("expected active Lua frame during host callback")
		}
		frame.Proto = outerBHandle.Value
	}, env.Value)
	if err != nil {
		t.Fatalf("register swapProto: %v", err)
	}
	if err := engine.SetGlobal(env.Value, "swapProto", swapProto.Value); err != nil {
		t.Fatalf("set global swapProto: %v", err)
	}
	closureHandle, err := engine.NewClosure(outerA, env.Value, nil)
	if err != nil {
		t.Fatalf("create outer closure: %v", err)
	}
	results, err := engine.Call(thread, closureHandle.Value, nil, -1)
	if err != nil {
		t.Fatalf("execute outer closure: %v", err)
	}
	if len(results) != 1 || results[0].Bits() != value.NumberValue(20).Bits() {
		t.Fatalf("activation should pick child proto from current frame, got %v", results)
	}
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

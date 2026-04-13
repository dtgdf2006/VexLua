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

	t.Run("activation-shadow-state-absent", func(t *testing.T) {
		text := readRepoFile(t, "internal", "interp", "engine.go")
		forbidden := []string{"type activation struct {", "type threadContext struct {", "type threadSnapshot struct {", "threads map[uint64]*threadContext"}
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Fatalf("interpreter should not keep shadow execution state %q", needle)
			}
		}
	})

	t.Run("thread-state-current-frame-mirror-absent", func(t *testing.T) {
		text := readRepoFile(t, "internal", "runtime", "state", "thread.go")
		forbidden := []string{"currentFrame int", "thread.currentFrame =", "thread.currentFrame--", "thread.currentFrame++"}
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Fatalf("thread state should not mirror native current frame via %q", needle)
			}
		}
		required := []string{"nativeHeader.CurrentFrame", "func (thread *ThreadState) currentFrameIndex()"}
		for _, needle := range required {
			if !strings.Contains(text, needle) {
				t.Fatalf("thread state should derive active frame from native header via %q", needle)
			}
		}
	})

	t.Run("feedback-path-stays-shared", func(t *testing.T) {
		text := readRepoFile(t, "internal", "interp", "feedback.go")
		forbidden := []string{"LayoutForProto(", "activationProto(", "activationClosureRef(", "if act == nil", "act.callee", "act.feedbackLayout", "updateActivationFeedbackAtPC"}
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Fatalf("feedback path should not depend on %q", needle)
			}
		}
		required := []string{"UpdateTableFeedbackAtPC", "UpdateTableFeedbackAtSlot", "updateLayoutFeedbackAtPC", "layout *feedback.Layout", "closureRef value.HeapRef44"}
		for _, needle := range required {
			if !strings.Contains(text, needle) {
				t.Fatalf("feedback path should expose shared helper %q", needle)
			}
		}
	})

	t.Run("frame-hot-helpers-stay-localized", func(t *testing.T) {
		text := readRepoFile(t, "internal", "interp", "execute.go")
		forbidden := []string{"func (engine *Engine) activationProto(", "func (engine *Engine) activationClosureRef(", "func (engine *Engine) activationRegisterBase(", "func (engine *Engine) setActivationTop(", "func (engine *Engine) activationEnv(", "func (engine *Engine) activationUpvalueRef(", "if frame == nil || index >= len(slots)", "if frame == nil {\n\t\treturn fmt.Errorf(\"frame cannot be nil\")"}
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Fatalf("execute helpers should not regress to %q", needle)
			}
		}
		required := []string{"func (engine *Engine) executeLuaFrame(", "func (engine *Engine) setFrameTop(", "func (engine *Engine) closureEnv(", "func (engine *Engine) closureUpvalueRef(", "func (engine *Engine) validateFrameConstBase("}
		for _, needle := range required {
			if !strings.Contains(text, needle) {
				t.Fatalf("execute helpers should keep %q", needle)
			}
		}
	})

	t.Run("nil-receiver-noise-absent-in-gc-and-baseline-helpers", func(t *testing.T) {
		collectorText := readRepoFile(t, "internal", "runtime", "gc", "collector.go")
		for _, needle := range []string{"if collector == nil", "if collector == nil || collector.heap == nil", "gc collector requires a heap"} {
			if strings.Contains(collectorText, needle) {
				t.Fatalf("collector should not keep nil-receiver noise %q", needle)
			}
		}
		tracerText := readRepoFile(t, "internal", "runtime", "gc", "object_trace.go")
		for _, needle := range []string{"gc tracer requires a heap"} {
			if strings.Contains(tracerText, needle) {
				t.Fatalf("gc tracer should not keep dead defensive guard %q", needle)
			}
		}
		scannerText := readRepoFile(t, "internal", "runtime", "gc", "root_scan.go")
		for _, needle := range []string{"gc scanner requires a heap"} {
			if strings.Contains(scannerText, needle) {
				t.Fatalf("gc scanner should not keep dead defensive guard %q", needle)
			}
		}
		gcControllerText := readRepoFile(t, "internal", "runtime", "heap", "gc_controller.go")
		for _, needle := range []string{"if heap == nil || heap.gc == nil", "if controller == nil", "if heap == nil {"} {
			if strings.Contains(gcControllerText, needle) {
				t.Fatalf("gc controller should not keep nil-receiver noise %q", needle)
			}
		}
		heapText := readRepoFile(t, "internal", "runtime", "heap", "heap.go")
		for _, needle := range []string{"if heap == nil || heap.native == nil"} {
			if strings.Contains(heapText, needle) {
				t.Fatalf("heap helpers should not keep dead defensive guard %q", needle)
			}
		}
		runtimeStubsText := readRepoFile(t, "internal", "vexarc", "baseline", "runtime_stubs.go")
		for _, needle := range []string{"if compiled == nil || compiled.FeedbackLayout == nil", "if compiled == nil || compiled.Proto == nil", "if thread == nil {", "if callerFrame == nil {", "if frame == nil {\n\t\treturn 0, nil, fmt.Errorf(\"frame cannot be nil\")"} {
			if strings.Contains(runtimeStubsText, needle) {
				t.Fatalf("baseline helper should not keep dead defensive guard %q", needle)
			}
		}
		typesText := readRepoFile(t, "internal", "vexarc", "baseline", "types.go")
		for _, needle := range []string{"if code == nil", "if cache == nil"} {
			if strings.Contains(typesText, needle) {
				t.Fatalf("compiled code helpers should not keep dead defensive guard %q", needle)
			}
		}
		stubManagerText := readRepoFile(t, "internal", "vexarc", "baseline", "stub_manager.go")
		for _, needle := range []string{"if manager == nil", "if manager == nil || manager.cache == nil"} {
			if strings.Contains(stubManagerText, needle) {
				t.Fatalf("stub manager should not keep dead defensive guard %q", needle)
			}
		}
		compilerText := readRepoFile(t, "internal", "vexarc", "baseline", "compiler.go")
		for _, needle := range []string{"if state == nil || state.proto == nil", "if state.proto == nil", "baseline compiler requires an interpreter engine", "baseline compiler requires a code cache", "baseline compiler requires shared stubs"} {
			if strings.Contains(compilerText, needle) {
				t.Fatalf("compileState helpers should not keep dead defensive guard %q", needle)
			}
		}
		offsetTableText := readRepoFile(t, "internal", "vexarc", "metadata", "offset_table.go")
		for _, needle := range []string{"if set == nil", "if visit == nil", "if builder == nil", "if int(siteID) < 0", "if int(id) < 0"} {
			if strings.Contains(offsetTableText, needle) {
				t.Fatalf("metadata helpers should not keep dead defensive guard %q", needle)
			}
		}
		assemblerText := readRepoFile(t, "internal", "vexarc", "amd64", "assembler.go")
		for _, needle := range []string{"if label == nil"} {
			if strings.Contains(assemblerText, needle) {
				t.Fatalf("assembler should not keep dead defensive guard %q", needle)
			}
		}
		threadText := readRepoFile(t, "internal", "runtime", "state", "thread.go")
		for _, needle := range []string{"if vm == nil", "if thread == nil", "if thread == nil || thread.nativeHeader == nil", "vm state requires a heap"} {
			if strings.Contains(threadText, needle) {
				t.Fatalf("thread state should not keep dead defensive guard %q", needle)
			}
		}
		layoutText := readRepoFile(t, "internal", "runtime", "feedback", "layout.go")
		for _, needle := range []string{"if layout == nil", "if proto == nil"} {
			if strings.Contains(layoutText, needle) {
				t.Fatalf("feedback layout should not keep dead defensive guard %q", needle)
			}
		}
		interpFeedbackText := readRepoFile(t, "internal", "interp", "feedback.go")
		for _, needle := range []string{"if layout == nil"} {
			if strings.Contains(interpFeedbackText, needle) {
				t.Fatalf("interp feedback should not keep dead defensive guard %q", needle)
			}
		}
		metaRegistryText := readRepoFile(t, "internal", "runtime", "meta", "registry.go")
		for _, needle := range []string{"if registry == nil", "meta registry requires a heap"} {
			if strings.Contains(metaRegistryText, needle) {
				t.Fatalf("meta registry should not keep dead defensive guard %q", needle)
			}
		}
		hostRegistryText := readRepoFile(t, "internal", "runtime", "host", "registry.go")
		for _, needle := range []string{"host registry requires a heap", "entry cannot be nil"} {
			if strings.Contains(hostRegistryText, needle) {
				t.Fatalf("host registry should not keep dead defensive guard %q", needle)
			}
		}
		nativeArenaText := readRepoFile(t, "internal", "runtime", "heap", "native_arena.go")
		for _, needle := range []string{"if arena == nil || arena.impl == nil"} {
			if strings.Contains(nativeArenaText, needle) {
				t.Fatalf("native arena should not keep dead defensive guard %q", needle)
			}
		}
		frontendCompileText := readRepoFile(t, "internal", "frontend", "compiler", "compile.go")
		for _, needle := range []string{"if driver == nil", "if proto == nil"} {
			if strings.Contains(frontendCompileText, needle) {
				t.Fatalf("frontend compile driver should not keep dead defensive guard %q", needle)
			}
		}
		protoStoreText := readRepoFile(t, "internal", "runtime", "proto", "store.go")
		for _, needle := range []string{"if proto == nil", "proto store requires a heap"} {
			if strings.Contains(protoStoreText, needle) {
				t.Fatalf("proto store should not keep dead defensive guard %q", needle)
			}
		}
		closureStoreText := readRepoFile(t, "internal", "runtime", "closure", "store.go")
		for _, needle := range []string{"if proto == nil", "closure store requires a heap", "closure store requires a proto store"} {
			if strings.Contains(closureStoreText, needle) {
				t.Fatalf("closure store should not keep dead defensive guard %q", needle)
			}
		}
		tableStoreText := readRepoFile(t, "internal", "runtime", "table", "store.go")
		for _, needle := range []string{"table store requires a heap"} {
			if strings.Contains(tableStoreText, needle) {
				t.Fatalf("table store should not keep dead defensive guard %q", needle)
			}
		}
		stringInternText := readRepoFile(t, "internal", "runtime", "string", "intern.go")
		for _, needle := range []string{"intern table requires a heap"} {
			if strings.Contains(stringInternText, needle) {
				t.Fatalf("string intern should not keep dead defensive guard %q", needle)
			}
		}
		upvalueManagerText := readRepoFile(t, "internal", "runtime", "upvalue", "manager.go")
		for _, needle := range []string{"thread cannot be nil", "upvalue manager requires a heap", "upvalue manager requires vm state"} {
			if strings.Contains(upvalueManagerText, needle) {
				t.Fatalf("upvalue manager should not keep dead defensive guard %q", needle)
			}
		}
		vmStateText := readRepoFile(t, "internal", "runtime", "state", "vm.go")
		for _, needle := range []string{"if vm == nil"} {
			if strings.Contains(vmStateText, needle) {
				t.Fatalf("vm state should not keep dead defensive guard %q", needle)
			}
		}
		livenessText := readRepoFile(t, "internal", "vexarc", "baseline", "liveness.go")
		for _, needle := range []string{"if proto == nil", "if set == nil"} {
			if strings.Contains(livenessText, needle) {
				t.Fatalf("baseline liveness should not keep dead defensive guard %q", needle)
			}
		}
		protoBuilderText := readRepoFile(t, "internal", "frontend", "compiler", "proto_builder.go")
		for _, needle := range []string{"if builder == nil"} {
			if strings.Contains(protoBuilderText, needle) {
				t.Fatalf("proto builder should not keep dead defensive guard %q", needle)
			}
		}
		interpEngineText := readRepoFile(t, "internal", "interp", "engine.go")
		for _, needle := range []string{"if thread == nil", "if proto == nil"} {
			if strings.Contains(interpEngineText, needle) {
				t.Fatalf("interp engine should not keep dead defensive guard %q", needle)
			}
		}
		interpExecuteText := readRepoFile(t, "internal", "interp", "execute.go")
		for _, needle := range []string{"if thread == nil", "if frame == nil"} {
			if strings.Contains(interpExecuteText, needle) {
				t.Fatalf("interp execute should not keep dead defensive guard %q", needle)
			}
		}
		allocationBoundaryText := readRepoFile(t, "internal", "interp", "allocation_boundary.go")
		for _, needle := range []string{"if publish == nil", "if proto == nil"} {
			if strings.Contains(allocationBoundaryText, needle) {
				t.Fatalf("allocation boundary should not keep dead defensive guard %q", needle)
			}
		}
		baselineRuntimeText := readRepoFile(t, "internal", "vexarc", "baseline", "runtime.go")
		for _, needle := range []string{"if thread == nil", "if proto == nil", "baseline runtime requires an interpreter engine"} {
			if strings.Contains(baselineRuntimeText, needle) {
				t.Fatalf("baseline runtime should not keep dead defensive guard %q", needle)
			}
		}
		bytecodeProtoText := readRepoFile(t, "internal", "bytecode", "proto.go")
		for _, needle := range []string{"if proto == nil"} {
			if strings.Contains(bytecodeProtoText, needle) {
				t.Fatalf("bytecode proto helpers should not keep dead defensive guard %q", needle)
			}
		}
		bytecodeIteratorText := readRepoFile(t, "internal", "bytecode", "iterator.go")
		for _, needle := range []string{"if proto == nil"} {
			if strings.Contains(bytecodeIteratorText, needle) {
				t.Fatalf("bytecode iterator should not keep dead defensive guard %q", needle)
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

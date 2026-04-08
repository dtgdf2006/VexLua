package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"vexlua"
	"vexlua/internal/bytecode"
	"vexlua/internal/jit"
	amd64jit "vexlua/internal/jit/amd64"
	rt "vexlua/internal/runtime"
)

type jitExpectation int

const (
	expectJIT jitExpectation = iota
	expectFallback
)

type jitCase struct {
	name    string
	summary string
	build   func(*vexlua.Engine) (*bytecode.Proto, error)
	verify  func(vexlua.Value) error
	expect  jitExpectation
}

type jitSupportReport struct {
	supported bool
	reasons   []string
}

func main() {
	runs := flag.Int("runs", 6, "number of runs per workload")
	hot := flag.Uint("hot-threshold", 2, "JIT hot threshold")
	workloads := flag.String("cases", "all", "workloads to run: all, supported, fallback, or comma-separated names")
	list := flag.Bool("list", false, "list available workloads and exit")
	requireJIT := flag.String("require-jit", "auto", "whether expected-JIT workloads must compile: auto, on, off")
	flag.Parse()

	cases := allJITCases()
	if *list {
		printCases(cases)
		return
	}
	selected, err := selectCases(cases, *workloads)
	if err != nil {
		fatalf("select workloads: %v", err)
	}
	if len(selected) == 0 {
		fatalf("no workloads selected")
	}
	jitRequired, err := parseRequireMode(*requireJIT)
	if err != nil {
		fatalf("parse require-jit: %v", err)
	}

	engine := vexlua.NewWithOptions(vexlua.Options{EnableJIT: true, HotThreshold: uint32(*hot)})
	fmt.Printf("platform=%s/%s hot-threshold=%d runs=%d workloads=%s\n", runtime.GOOS, runtime.GOARCH, *hot, *runs, joinCaseNames(selected))

	failed := false
	for _, testCase := range selected {
		proto, err := testCase.build(engine)
		if err != nil {
			fatalf("build %s: %v", testCase.name, err)
		}
		report, err := analyzeJITSupport(proto)
		if err != nil {
			fatalf("analyze %s: %v", testCase.name, err)
		}
		started := time.Now()
		var result vexlua.Value
		for i := 0; i < *runs; i++ {
			result, err = engine.Run(proto)
			if err != nil {
				fatalf("run %s: %v", testCase.name, err)
			}
		}
		if err := testCase.verify(result); err != nil {
			fatalf("verify %s: %v", testCase.name, err)
		}
		stats := engine.Stats(proto)
		fmt.Printf("%s => expect=%s compiler=%s result=%s runs=%d quickened=%d jit=%v elapsed=%s\n", testCase.name, testCase.expect, report.format(), engine.FormatValue(result), stats.Runs, stats.QuickenedOps, stats.JITCompiled, time.Since(started))
		if !report.supported && stats.JITCompiled {
			fmt.Fprintf(os.Stderr, "%s reported unsupported but reached JIT: %+v\n", testCase.name, stats)
			failed = true
		}
		if shouldRequireJIT(jitRequired, testCase.expect) && !stats.JITCompiled {
			fmt.Fprintf(os.Stderr, "%s did not reach JIT when required: compiler=%s stats=%+v\n", testCase.name, report.format(), stats)
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}
}

func allJITCases() []jitCase {
	return []jitCase{
		{
			name:    "sum_loop",
			summary: "builder numeric loop expected to JIT",
			build: func(engine *vexlua.Engine) (*bytecode.Proto, error) {
				return engine.BuildSumLoop(10000), nil
			},
			verify: func(value vexlua.Value) error {
				want := float64(10000*10001) / 2
				if math.Abs(value.Number()-want) > 0.001 {
					return fmt.Errorf("sum_loop result = %v, want %v", value.Number(), want)
				}
				return nil
			},
			expect: expectJIT,
		},
		{
			name:    "scripted_numeric_for",
			summary: "scripted numeric-for expected to JIT",
			build: func(engine *vexlua.Engine) (*bytecode.Proto, error) {
				return engine.CompileStringNamed(`
local sum = 0
for i = 1, 1000 do
	sum = sum + i
end
return sum
`, "@debugjit_numeric_for.lua")
			},
			verify: func(value vexlua.Value) error {
				if value.Number() != 500500 {
					return fmt.Errorf("scripted_numeric_for result = %v, want 500500", value.Number())
				}
				return nil
			},
			expect: expectJIT,
		},
		{
			name:    "scripted_while_loop",
			summary: "scripted while-loop expected to JIT",
			build: func(engine *vexlua.Engine) (*bytecode.Proto, error) {
				return engine.CompileStringNamed(`
local i = 1
local sum = 0
while i <= 1000 do
	sum = sum + i
	i = i + 1
end
return sum
`, "@debugjit_while.lua")
			},
			verify: func(value vexlua.Value) error {
				if value.Number() != 500500 {
					return fmt.Errorf("scripted_while_loop result = %v, want 500500", value.Number())
				}
				return nil
			},
			expect: expectJIT,
		},
		{
			name:    "closure_upvalues",
			summary: "closure/upvalue path should fall back without changing semantics",
			build: func(engine *vexlua.Engine) (*bytecode.Proto, error) {
				return engine.CompileStringNamed(`
local seed = 40
local function make()
	local offset = 2
	return function(v)
		return v + seed + offset
	end
end
local fn = make()
return fn(0)
`, "@debugjit_closure.lua")
			},
			verify: func(value vexlua.Value) error {
				if value.Number() != 42 {
					return fmt.Errorf("closure_upvalues result = %v, want 42", value.Number())
				}
				return nil
			},
			expect: expectFallback,
		},
		{
			name:    "method_call",
			summary: "SELF/CALL method dispatch should fall back cleanly",
			build: func(engine *vexlua.Engine) (*bytecode.Proto, error) {
				return engine.CompileStringNamed(`
local box = {base = 40}
function box:inc(v)
	return self.base + v + 1
end
return box:inc(1)
`, "@debugjit_method.lua")
			},
			verify: func(value vexlua.Value) error {
				if value.Number() != 42 {
					return fmt.Errorf("method_call result = %v, want 42", value.Number())
				}
				return nil
			},
			expect: expectFallback,
		},
		{
			name:    "coroutine_resume",
			summary: "coroutine yield/resume should stay interpreter-only but correct",
			build: func(engine *vexlua.Engine) (*bytecode.Proto, error) {
				return engine.CompileStringNamed(`
local co = coroutine.create(function(v)
	local next = coroutine.yield(v + 1)
	return next + 2
end)
local ok1, first = coroutine.resume(co, 40)
local ok2, second = coroutine.resume(co, 40)
return (ok1 and 1 or 0) + first + (ok2 and 1 or 0) + second
`, "@debugjit_coroutine.lua")
			},
			verify: func(value vexlua.Value) error {
				if value.Number() != 85 {
					return fmt.Errorf("coroutine_resume result = %v, want 85", value.Number())
				}
				return nil
			},
			expect: expectFallback,
		},
		{
			name:    "debug_hook",
			summary: "debug hook path should fall back cleanly",
			build: func(engine *vexlua.Engine) (*bytecode.Proto, error) {
				return engine.CompileStringNamed(`
local fn = assert(loadstring("local a = 1\nlocal b = 2\nreturn a + b\n", "@jit_hook.lua"))
local info = debug.getinfo(fn, "SL")

local function localDemo()
	local first = 10
	local second = 20
	local name1, value1 = debug.getlocal(1, 1)
	local changed = debug.setlocal(1, 2, 99)
	return name1 == "first" and value1 == 10 and changed == "second" and second == 99
end

local lines = {}
local counts = 0
local function hook(event, line)
	if event == "line" and type(line) == "number" then
		lines[#lines + 1] = line
	elseif event == "count" then
		counts = counts + 1
	end
end

debug.sethook(hook, "l", 2)
local function hooked()
	local sum = 0
	sum = sum + 1
	sum = sum + 2
	return sum
end
local hookResult = hooked()
local hookFn, hookMask, hookCount = debug.gethook()
debug.sethook()
local clearedFn, clearedMask, clearedCount = debug.gethook()

return (localDemo() and 1 or 0)
	+ (((info.activelines[1] == true) and (info.activelines[2] == true) and (info.activelines[3] == true)) and 10 or 0)
	+ ((hookResult == 3) and 100 or 0)
	+ ((type(hookFn) == "function" and hookMask == "l" and hookCount == 2) and 1000 or 0)
	+ ((#lines > 0 and counts > 0) and 10000 or 0)
	+ (((clearedFn == nil) and clearedMask == "" and clearedCount == 0) and 100000 or 0)
`, "@debugjit_hook.lua")
			},
			verify: func(value vexlua.Value) error {
				if value.Number() != 111111 {
					return fmt.Errorf("debug_hook result = %v, want 111111", value.Number())
				}
				return nil
			},
			expect: expectFallback,
		},
	}
}

func selectCases(cases []jitCase, spec string) ([]jitCase, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" || trimmed == "all" {
		return cases, nil
	}
	if trimmed == "supported" {
		selected := make([]jitCase, 0, len(cases))
		for _, testCase := range cases {
			if testCase.expect == expectJIT {
				selected = append(selected, testCase)
			}
		}
		return selected, nil
	}
	if trimmed == "fallback" {
		selected := make([]jitCase, 0, len(cases))
		for _, testCase := range cases {
			if testCase.expect == expectFallback {
				selected = append(selected, testCase)
			}
		}
		return selected, nil
	}
	byName := make(map[string]jitCase, len(cases))
	for _, testCase := range cases {
		byName[testCase.name] = testCase
	}
	selected := make([]jitCase, 0, len(cases))
	seen := make(map[string]struct{}, len(cases))
	for _, part := range strings.Split(trimmed, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		testCase, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown workload %q", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		selected = append(selected, testCase)
	}
	return selected, nil
}

func printCases(cases []jitCase) {
	for _, testCase := range cases {
		fmt.Printf("%s\t%s\t%s\n", testCase.name, testCase.expect, testCase.summary)
	}
}

func parseRequireMode(mode string) (bool, error) {
	switch strings.TrimSpace(mode) {
	case "auto":
		return runtime.GOOS == "windows" && runtime.GOARCH == "amd64", nil
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("unsupported mode %q", mode)
	}
}

func shouldRequireJIT(enabled bool, expect jitExpectation) bool {
	return enabled && expect == expectJIT
}

func analyzeJITSupport(proto *bytecode.Proto) (jitSupportReport, error) {
	compiler := amd64jit.NewCompiler()
	_, err := compiler.Compile(proto)
	if err == nil {
		return jitSupportReport{supported: true}, nil
	}
	if !errors.Is(err, jit.ErrUnsupported) {
		return jitSupportReport{}, err
	}
	return jitSupportReport{supported: false, reasons: unsupportedReasons(proto)}, nil
}

func unsupportedReasons(proto *bytecode.Proto) []string {
	reasons := make(map[string]struct{})
	addReason := func(reason string) {
		reasons[reason] = struct{}{}
	}
	for pc, instr := range proto.Code {
		switch instr.Op {
		case bytecode.OpNoop, bytecode.OpMove, bytecode.OpAdd, bytecode.OpAddNum, bytecode.OpJump, bytecode.OpLessEqualJump, bytecode.OpReturn:
		case bytecode.OpLoadConst:
			if int(instr.D) < 0 || int(instr.D) >= len(proto.Constants) {
				addReason(fmt.Sprintf("LOAD_CONST constant %d out of range", instr.D))
				continue
			}
			constant := proto.Constants[instr.D]
			if !constant.IsNumber() && constant.Kind() != rt.KindNil && constant.Kind() != rt.KindBool {
				addReason(fmt.Sprintf("LOAD_CONST constant %d is not number/nil/bool", instr.D))
			}
		case bytecode.OpAddConst:
			if int(instr.D) < 0 || int(instr.D) >= len(proto.Constants) {
				addReason(fmt.Sprintf("ADD_CONST constant %d out of range", instr.D))
				continue
			}
			if !proto.Constants[instr.D].IsNumber() {
				addReason(fmt.Sprintf("ADD_CONST constant %d is not numeric", instr.D))
			}
		case bytecode.OpReturnMulti:
			if instr.B > 1 {
				addReason(fmt.Sprintf("RETURN_MULTI with %d results", instr.B))
			}
		case bytecode.OpLess, bytecode.OpLessEqual:
			if pc+1 >= len(proto.Code) {
				addReason(instr.Op.String() + " at end of proto")
				continue
			}
			next := proto.Code[pc+1]
			if next.Op != bytecode.OpJumpIfFalse || next.A != instr.A {
				addReason(instr.Op.String() + " without fused JUMP_IF_FALSE")
			}
		case bytecode.OpJumpIfFalse:
			if pc == 0 {
				addReason("JUMP_IF_FALSE without leading compare")
				continue
			}
			prev := proto.Code[pc-1]
			if (prev.Op != bytecode.OpLess && prev.Op != bytecode.OpLessEqual) || prev.A != instr.A {
				addReason("standalone JUMP_IF_FALSE")
			}
		default:
			addReason(instr.Op.String())
		}
	}
	if len(reasons) == 0 {
		return []string{"compiler returned unsupported without a simple opcode explanation"}
	}
	ordered := make([]string, 0, len(reasons))
	for reason := range reasons {
		ordered = append(ordered, reason)
	}
	sort.Strings(ordered)
	return ordered
}

func joinCaseNames(cases []jitCase) string {
	names := make([]string, 0, len(cases))
	for _, testCase := range cases {
		names = append(names, testCase.name)
	}
	return strings.Join(names, ",")
}

func (expect jitExpectation) String() string {
	if expect == expectJIT {
		return "jit"
	}
	return "fallback"
}

func (report jitSupportReport) format() string {
	if report.supported {
		return "supported"
	}
	return "unsupported(" + strings.Join(report.reasons, "; ") + ")"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

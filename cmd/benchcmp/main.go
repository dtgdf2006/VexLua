package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"vexlua"
	"vexlua/internal/bytecode"
)

type workload struct {
	Name     string
	Source   string
	Expected string
	Notes    string
}

type sourceBenchResult struct {
	Iterations int
	VexNSOp    float64
	LuaNSOp    float64
}

type runBenchResult struct {
	Iterations int
	VexNSOp    float64
	VexJITNSOp float64
	LuaNSOp    float64
}

type summary struct {
	Workload workload
	Source   sourceBenchResult
	Run      runBenchResult
}

type vexSourceRunner struct {
	engine   *vexlua.Engine
	workload workload
}

type vexRunRunner struct {
	engine   *vexlua.Engine
	proto    *bytecode.Proto
	workload workload
}

type luaRunner struct {
	binary string
	script string
	work   workload
}

var workloads = []workload{
	{
		Name: "numeric_for_sum",
		Source: `
local sum = 0
for i = 1, 20000 do
	sum = sum + i
end
return sum
`,
		Expected: "200010000",
		Notes:    "数值 for 循环与算术",
	},
	{
		Name: "table_field_sum",
		Source: `
local obj = {x = 1, y = 2, z = 3}
local sum = 0
for i = 1, 50000 do
	sum = sum + obj.x + obj.y + obj.z
end
return sum
`,
		Expected: "300000",
		Notes:    "table 字段访问与 inline cache 热点",
	},
	{
		Name: "method_dispatch",
		Source: `
local box = {base = 32}
function box:mix(a, b, c)
	return self.base + a + b + c
end
local sum = 0
for i = 1, 5000 do
	sum = sum + box:mix(2, 3, 4)
end
return sum
`,
		Expected: "205000",
		Notes:    "方法查找、self 注入与调用开销",
	},
	{
		Name: "closure_upvalue",
		Source: `
local seed = 40
local function make()
	local offset = 2
	return function(v)
		return v + seed + offset
	end
end
local fn = make()
local sum = 0
for i = 1, 5000 do
	sum = sum + fn(0)
end
return sum
`,
		Expected: "210000",
		Notes:    "闭包、upvalue 与函数调用",
	},
	{
		Name: "generic_for_pairs",
		Source: `
local t = {a = 1, b = 2, c = 3, d = 4, e = 5}
local sum = 0
for i = 1, 5000 do
	for _, v in pairs(t) do
		sum = sum + v
	end
end
return sum
`,
		Expected: "75000",
		Notes:    "generic for、pairs 与迭代协议",
	},
}

func main() {
	var luaBin string
	var targetMS int
	flag.StringVar(&luaBin, "lua-bin", "", "Lua 5.1 executable to compare against; defaults to auto-detect from PATH")
	flag.IntVar(&targetMS, "target-ms", 250, "Target duration in milliseconds for each benchmark calibration pass")
	flag.Parse()

	target := time.Duration(targetMS) * time.Millisecond
	if target < 50*time.Millisecond {
		fatalf("target-ms must be >= 50")
	}

	resolvedLua, version, err := detectLua(luaBin)
	if err != nil {
		fatalf("failed to locate Lua 5.1: %v", err)
	}

	tempDir, err := os.MkdirTemp("", "vexlua-benchcmp-")
	if err != nil {
		fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	summaries := make([]summary, 0, len(workloads))
	for _, work := range workloads {
		sourceVex := &vexSourceRunner{engine: vexlua.NewWithOptions(vexlua.Options{EnableJIT: true, HotThreshold: 2}), workload: work}
		runVex, err := newVexRunRunner(false, work)
		if err != nil {
			fatalf("prepare VexLua interpreter runner for %s: %v", work.Name, err)
		}
		runVexJIT, err := newVexRunRunner(true, work)
		if err != nil {
			fatalf("prepare VexLua JIT runner for %s: %v", work.Name, err)
		}
		luaHarness, err := newLuaRunner(resolvedLua, tempDir, work)
		if err != nil {
			fatalf("prepare Lua runner for %s: %v", work.Name, err)
		}

		sourceIterations, err := calibrate(target, 1, 1<<20, sourceVex.bench)
		if err != nil {
			fatalf("calibrate source+run iterations for %s: %v", work.Name, err)
		}
		runIterations, err := calibrate(target, 1, 1<<22, runVex.bench)
		if err != nil {
			fatalf("calibrate run-only iterations for %s: %v", work.Name, err)
		}

		sourceVexDur, err := sourceVex.bench(sourceIterations)
		if err != nil {
			fatalf("bench VexLua source+run for %s: %v", work.Name, err)
		}
		sourceLuaDur, err := luaHarness.bench("source", sourceIterations)
		if err != nil {
			fatalf("bench Lua source+run for %s: %v", work.Name, err)
		}
		runVexDur, err := runVex.bench(runIterations)
		if err != nil {
			fatalf("bench VexLua run-only for %s: %v", work.Name, err)
		}
		runVexJITDur, err := runVexJIT.bench(runIterations)
		if err != nil {
			fatalf("bench VexLua JIT run-only for %s: %v", work.Name, err)
		}
		runLuaDur, err := luaHarness.bench("run", runIterations)
		if err != nil {
			fatalf("bench Lua run-only for %s: %v", work.Name, err)
		}

		summaries = append(summaries, summary{
			Workload: work,
			Source: sourceBenchResult{
				Iterations: sourceIterations,
				VexNSOp:    nsPerOp(sourceVexDur, sourceIterations),
				LuaNSOp:    nsPerOp(sourceLuaDur, sourceIterations),
			},
			Run: runBenchResult{
				Iterations: runIterations,
				VexNSOp:    nsPerOp(runVexDur, runIterations),
				VexJITNSOp: nsPerOp(runVexJITDur, runIterations),
				LuaNSOp:    nsPerOp(runLuaDur, runIterations),
			},
		})
	}

	printReport(resolvedLua, version, target, summaries)
}

func detectLua(explicit string) (string, string, error) {
	candidates := make([]string, 0, 4)
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		candidates = append(candidates, "lua", "lua5.1", "lua51", "lua5_1")
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, "-v")
		output, err := cmd.CombinedOutput()
		version := strings.TrimSpace(string(output))
		if err != nil && version == "" {
			continue
		}
		if strings.Contains(version, "Lua 5.1") {
			return path, version, nil
		}
		if explicit != "" {
			return "", "", fmt.Errorf("%s reports %q, not Lua 5.1", path, version)
		}
	}
	return "", "", errors.New("no Lua 5.1 executable found in PATH")
}

func newVexRunRunner(enableJIT bool, work workload) (*vexRunRunner, error) {
	hotThreshold := uint32(1024)
	if enableJIT {
		hotThreshold = 2
	}
	engine := vexlua.NewWithOptions(vexlua.Options{EnableJIT: enableJIT, HotThreshold: hotThreshold})
	proto, err := engine.CompileString(work.Source)
	if err != nil {
		return nil, err
	}
	result, err := engine.Run(proto)
	if err != nil {
		return nil, err
	}
	if got := engine.FormatValue(result); !matchesExpected(got, work.Expected) {
		return nil, fmt.Errorf("unexpected VexLua result %q, want %q", got, work.Expected)
	}
	if enableJIT {
		for i := 0; i < 6; i++ {
			if _, err := engine.Run(proto); err != nil {
				return nil, err
			}
		}
	}
	return &vexRunRunner{engine: engine, proto: proto, workload: work}, nil
}

func (r *vexSourceRunner) bench(iterations int) (time.Duration, error) {
	start := time.Now()
	var result vexlua.Value
	var err error
	for i := 0; i < iterations; i++ {
		result, err = r.engine.DoString(r.workload.Source)
		if err != nil {
			return 0, err
		}
	}
	if got := r.engine.FormatValue(result); !matchesExpected(got, r.workload.Expected) {
		return 0, fmt.Errorf("unexpected VexLua source result %q, want %q", got, r.workload.Expected)
	}
	return time.Since(start), nil
}

func (r *vexRunRunner) bench(iterations int) (time.Duration, error) {
	start := time.Now()
	var result vexlua.Value
	var err error
	for i := 0; i < iterations; i++ {
		result, err = r.engine.Run(r.proto)
		if err != nil {
			return 0, err
		}
	}
	if got := r.engine.FormatValue(result); !matchesExpected(got, r.workload.Expected) {
		return 0, fmt.Errorf("unexpected VexLua run result %q, want %q", got, r.workload.Expected)
	}
	return time.Since(start), nil
}

func newLuaRunner(luaBin string, tempDir string, work workload) (*luaRunner, error) {
	scriptPath := filepath.Join(tempDir, work.Name+".lua")
	script := buildLuaHarness(work)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return nil, err
	}
	return &luaRunner{binary: luaBin, script: scriptPath, work: work}, nil
}

func (r *luaRunner) bench(mode string, iterations int) (time.Duration, error) {
	cmd := exec.Command(r.binary, r.script, mode, strconv.Itoa(iterations))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("lua command failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected lua output: %q", strings.TrimSpace(string(output)))
	}
	elapsedSec, err := strconv.ParseFloat(strings.TrimSpace(lines[0]), 64)
	if err != nil {
		return 0, fmt.Errorf("parse lua elapsed time: %w", err)
	}
	result := strings.TrimSpace(lines[1])
	if result != r.work.Expected {
		return 0, fmt.Errorf("unexpected Lua result %q, want %q", result, r.work.Expected)
	}
	return time.Duration(elapsedSec * float64(time.Second)), nil
}

func buildLuaHarness(work workload) string {
	return fmt.Sprintf(`local mode = assert(arg[1], "mode required")
local iterations = assert(tonumber(arg[2]), "iterations required")
local source = [==[%s]==]
local expected = [==[%s]==]

local expected_num = tonumber(expected)

local function matches_expected(value)
	if expected_num ~= nil then
		local actual_num = tonumber(value)
		return actual_num ~= nil and actual_num == expected_num
	end
	return tostring(value) == expected
end

local function run_source()
	local fn, err = loadstring(source)
	if not fn then
		error(err)
	end
	return fn()
end

local result
if mode == "run" then
	local fn, err = loadstring(source)
	if not fn then
		error(err)
	end
	result = fn()
	if not matches_expected(result) then
		error(string.format("unexpected result %%s want %%s", tostring(result), expected))
	end
	local start = os.clock()
	for i = 1, iterations do
		result = fn()
	end
	local elapsed = os.clock() - start
	io.write(string.format("%%.9f\n", elapsed))
	io.write(tostring(result), "\n")
	return
end

if mode ~= "source" then
	error("unknown mode: " .. tostring(mode))
end

result = run_source()
if not matches_expected(result) then
	error(string.format("unexpected result %%s want %%s", tostring(result), expected))
end
local start = os.clock()
for i = 1, iterations do
	result = run_source()
end
local elapsed = os.clock() - start
io.write(string.format("%%.9f\n", elapsed))
io.write(tostring(result), "\n")
`, strings.TrimSpace(work.Source), work.Expected)
}

func calibrate(target time.Duration, minIterations int, maxIterations int, run func(int) (time.Duration, error)) (int, error) {
	iterations := minIterations
	for {
		duration, err := run(iterations)
		if err != nil {
			return 0, err
		}
		if duration >= target || iterations >= maxIterations {
			return iterations, nil
		}
		if duration <= 0 {
			iterations = minInt(maxIterations, iterations*10)
			continue
		}
		scale := float64(target) / float64(duration)
		next := int(math.Ceil(float64(iterations) * scale * 1.1))
		if next <= iterations {
			next = iterations * 2
		}
		iterations = minInt(maxIterations, next)
	}
}

func printReport(luaBin string, version string, target time.Duration, summaries []summary) {
	fmt.Printf("Lua baseline: %s (%s)\n", luaBin, version)
	fmt.Printf("Calibration target per row: %s\n\n", target)

	fmt.Println("Source+Run benchmark")
	fmt.Println("说明: VexLua 使用 DoString；同一 source 会复用已编译 proto，从而累积 IC/JIT 热点。Lua 5.1 对照仍使用每次 loadstring(source)()")
	fmt.Println("| workload | notes | iterations | VexLua ns/op | Lua 5.1 ns/op | VexLua vs Lua |")
	fmt.Println("| --- | --- | ---: | ---: | ---: | ---: |")
	for _, item := range summaries {
		fmt.Printf("| %s | %s | %d | %.1f | %.1f | %.2fx |\n",
			item.Workload.Name,
			item.Workload.Notes,
			item.Source.Iterations,
			item.Source.VexNSOp,
			item.Source.LuaNSOp,
			speedup(item.Source.LuaNSOp, item.Source.VexNSOp),
		)
	}

	fmt.Println()
	fmt.Println("Run-Only benchmark")
	fmt.Println("说明: 源码只编译一次; VexLua 对比 interpreter/JIT 两档，对照 Lua 5.1 使用预先 loadstring 后重复调用")
	fmt.Println("| workload | notes | iterations | VexLua interp ns/op | VexLua JIT ns/op | Lua 5.1 ns/op | interp vs Lua | JIT vs Lua |")
	fmt.Println("| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, item := range summaries {
		fmt.Printf("| %s | %s | %d | %.1f | %.1f | %.1f | %.2fx | %.2fx |\n",
			item.Workload.Name,
			item.Workload.Notes,
			item.Run.Iterations,
			item.Run.VexNSOp,
			item.Run.VexJITNSOp,
			item.Run.LuaNSOp,
			speedup(item.Run.LuaNSOp, item.Run.VexNSOp),
			speedup(item.Run.LuaNSOp, item.Run.VexJITNSOp),
		)
	}
}

func nsPerOp(duration time.Duration, iterations int) float64 {
	if iterations <= 0 {
		return 0
	}
	return float64(duration.Nanoseconds()) / float64(iterations)
}

func speedup(baseline float64, contender float64) float64 {
	if baseline == 0 || contender == 0 {
		return 0
	}
	return baseline / contender
}

func matchesExpected(actual string, expected string) bool {
	if actual == expected {
		return true
	}
	actualNum, actualErr := strconv.ParseFloat(actual, 64)
	expectedNum, expectedErr := strconv.ParseFloat(expected, 64)
	if actualErr == nil && expectedErr == nil {
		return actualNum == expectedNum
	}
	return false
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

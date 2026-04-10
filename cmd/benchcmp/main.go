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
	benchmarks "vexlua/internal/benchmarks"
	"vexlua/internal/bytecode"
)

type sourceBenchResult struct {
	Iterations int
	VexNSOp    float64
	LuaNSOp    float64
}

type runBenchResult struct {
	Iterations      int
	VexInterpNSOp   float64
	VexarcNSOp      float64
	LuaNSOp         float64
	VexQuickenedOps int
	VexarcActive    bool
}

type summary struct {
	Workload benchmarks.Workload
	Source   sourceBenchResult
	Run      runBenchResult
}

type vexSourceRunner struct {
	engine   *vexlua.Engine
	workload benchmarks.Workload
}

type vexRunRunner struct {
	engine   *vexlua.Engine
	proto    *bytecode.Proto
	workload benchmarks.Workload
}

type vexRunMode uint8

const (
	vexRunInterp vexRunMode = iota + 1
	vexRunVexarc
)

type luaRunner struct {
	binary string
	script string
	work   benchmarks.Workload
}

func main() {
	var luaBin string
	var targetMS int
	var workloadSpec string
	var listWorkloads bool
	flag.StringVar(&luaBin, "lua-bin", "", "Lua 5.1 executable to compare against; defaults to auto-detect from PATH")
	flag.IntVar(&targetMS, "target-ms", 250, "Target duration in milliseconds for each benchmark calibration pass")
	flag.StringVar(&workloadSpec, "workloads", "all", "Comma-separated workload names or tags (all, core, extended, numeric, table, call, closure, iterator, string, coroutine, stdlib, vararg, tailcall, metatable, vexarc)")
	flag.BoolVar(&listWorkloads, "list", false, "List available workloads and tags, then exit")
	flag.Parse()

	if listWorkloads {
		printWorkloads(benchmarks.ScriptWorkloads())
		return
	}

	selected, err := benchmarks.SelectWorkloads(workloadSpec)
	if err != nil {
		fatalf("invalid workloads selection: %v", err)
	}

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

	summaries := make([]summary, 0, len(selected))
	for _, work := range selected {
		sourceVex := &vexSourceRunner{engine: vexlua.New(), workload: work}
		runVex, err := newVexRunRunner(vexRunInterp, work)
		if err != nil {
			fatalf("prepare VexLua interpreter runner for %s: %v", work.Name, err)
		}
		runVexarc, err := newVexRunRunner(vexRunVexarc, work)
		if err != nil {
			fatalf("prepare VexLua vexarc runner for %s: %v", work.Name, err)
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
		runVexarcDur, err := runVexarc.bench(runIterations)
		if err != nil {
			fatalf("bench VexLua vexarc run-only for %s: %v", work.Name, err)
		}
		runLuaDur, err := luaHarness.bench("run", runIterations)
		if err != nil {
			fatalf("bench Lua run-only for %s: %v", work.Name, err)
		}
		vexarcStats := runVexarc.engine.Stats(runVexarc.proto)

		summaries = append(summaries, summary{
			Workload: work,
			Source: sourceBenchResult{
				Iterations: sourceIterations,
				VexNSOp:    nsPerOp(sourceVexDur, sourceIterations),
				LuaNSOp:    nsPerOp(sourceLuaDur, sourceIterations),
			},
			Run: runBenchResult{
				Iterations:      runIterations,
				VexInterpNSOp:   nsPerOp(runVexDur, runIterations),
				VexarcNSOp:      nsPerOp(runVexarcDur, runIterations),
				LuaNSOp:         nsPerOp(runLuaDur, runIterations),
				VexQuickenedOps: vexarcStats.QuickenedOps,
				VexarcActive:    vexarcActive(vexarcStats),
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

func newVexRunRunner(mode vexRunMode, work benchmarks.Workload) (*vexRunRunner, error) {
	engine := newBenchmarkEngine(mode)
	proto, err := engine.CompileString(work.Source)
	if err != nil {
		return nil, err
	}
	result, err := engine.Run(proto)
	if err != nil {
		return nil, err
	}
	if got := engine.FormatValue(result); !benchmarks.MatchesExpected(got, work.Expected) {
		return nil, fmt.Errorf("unexpected VexLua result %q, want %q", got, work.Expected)
	}
	if mode != vexRunInterp {
		for i := 0; i < 6; i++ {
			if _, err := engine.Run(proto); err != nil {
				return nil, err
			}
		}
	}
	return &vexRunRunner{engine: engine, proto: proto, workload: work}, nil
}

func newBenchmarkEngine(mode vexRunMode) *vexlua.Engine {
	switch mode {
	case vexRunVexarc:
		return vexlua.NewWithOptions(vexlua.Options{EnableJIT: true, HotThreshold: 1})
	default:
		return vexlua.New()
	}
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
	if got := r.engine.FormatValue(result); !benchmarks.MatchesExpected(got, r.workload.Expected) {
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
	if got := r.engine.FormatValue(result); !benchmarks.MatchesExpected(got, r.workload.Expected) {
		return 0, fmt.Errorf("unexpected VexLua run result %q, want %q", got, r.workload.Expected)
	}
	return time.Since(start), nil
}

func newLuaRunner(luaBin string, tempDir string, work benchmarks.Workload) (*luaRunner, error) {
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
	if !benchmarks.MatchesExpected(result, r.work.Expected) {
		return 0, fmt.Errorf("unexpected Lua result %q, want %q", result, r.work.Expected)
	}
	return time.Duration(elapsedSec * float64(time.Second)), nil
}

func buildLuaHarness(work benchmarks.Workload) string {
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
	fmt.Printf("Selected workloads: %d\n", len(summaries))
	fmt.Printf("Calibration target per row: %s\n\n", target)
	fmt.Printf("Geomean speedup vs Lua 5.1: source %.2fx | run interp %.2fx | run vexarc(active) %.2fx\n", sourceGeomean(summaries), runInterpGeomean(summaries), runVexarcGeomean(summaries))
	fmt.Printf("Quickened workloads: %d/%d | Vexarc active workloads: %d/%d\n\n", quickenedWorkloadCount(summaries), len(summaries), vexarcActiveCount(summaries), len(summaries))

	fmt.Println("Source+Run benchmark")
	fmt.Println("说明: VexLua 使用 DoString；同一 source 会复用已编译 proto，从而累积 IC 和 quickening。Lua 5.1 对照仍使用每次 loadstring(source)()")
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
	fmt.Println("说明: 源码只编译一次；VexLua 对比冷启动解释器和启用 compiled tier 的 vexarc 运行档位。vexarc 档位会先做若干次预热以触发 quickening 和编译；vexarc active 表示该 workload 至少出现过一次 compiled return 或 helper reentry，而不是只进入空 stub 后立即回解释器。")
	fmt.Println("| workload | notes | iterations | VexLua interp ns/op | VexLua vexarc ns/op | quickened | vexarc active | Lua 5.1 ns/op | interp vs Lua | vexarc vs Lua |")
	fmt.Println("| --- | --- | ---: | ---: | ---: | ---: | --- | ---: | ---: | ---: |")
	for _, item := range summaries {
		fmt.Printf("| %s | %s | %d | %.1f | %s | %d | %s | %.1f | %.2fx | %s |\n",
			item.Workload.Name,
			item.Workload.Notes,
			item.Run.Iterations,
			item.Run.VexInterpNSOp,
			formatMetric(item.Run.VexarcActive, item.Run.VexarcNSOp),
			item.Run.VexQuickenedOps,
			formatBool(item.Run.VexarcActive),
			item.Run.LuaNSOp,
			speedup(item.Run.LuaNSOp, item.Run.VexInterpNSOp),
			formatSpeedup(item.Run.VexarcActive, item.Run.LuaNSOp, item.Run.VexarcNSOp),
		)
	}
}

func printWorkloads(workloads []benchmarks.Workload) {
	fmt.Println("Available workloads:")
	for _, work := range workloads {
		fmt.Printf("- %s [%s]: %s\n", work.Name, strings.Join(work.Tags, ","), work.Notes)
	}
	fmt.Println()
	fmt.Printf("Available tags: %s\n", strings.Join(benchmarks.AllTags(), ", "))
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

func sourceGeomean(summaries []summary) float64 {
	values := make([]float64, 0, len(summaries))
	for _, item := range summaries {
		values = append(values, speedup(item.Source.LuaNSOp, item.Source.VexNSOp))
	}
	return geometricMean(values)
}

func runInterpGeomean(summaries []summary) float64 {
	values := make([]float64, 0, len(summaries))
	for _, item := range summaries {
		values = append(values, speedup(item.Run.LuaNSOp, item.Run.VexInterpNSOp))
	}
	return geometricMean(values)
}

func runVexarcGeomean(summaries []summary) float64 {
	values := make([]float64, 0, len(summaries))
	for _, item := range summaries {
		if !item.Run.VexarcActive {
			continue
		}
		values = append(values, speedup(item.Run.LuaNSOp, item.Run.VexarcNSOp))
	}
	return geometricMean(values)
}

func geometricMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	count := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		sum += math.Log(value)
		count++
	}
	if count == 0 {
		return 0
	}
	return math.Exp(sum / float64(count))
}

func quickenedWorkloadCount(summaries []summary) int {
	count := 0
	for _, item := range summaries {
		if item.Run.VexQuickenedOps > 0 {
			count++
		}
	}
	return count
}

func vexarcActiveCount(summaries []summary) int {
	count := 0
	for _, item := range summaries {
		if item.Run.VexarcActive {
			count++
		}
	}
	return count
}

func vexarcActive(stats vexlua.ProgramStats) bool {
	return stats.CompiledReturns > 0 || stats.CompiledHelperCalls > 0
}

func formatBool(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func formatMetric(active bool, value float64) string {
	if !active {
		return "-"
	}
	return fmt.Sprintf("%.1f", value)
}

func formatSpeedup(active bool, baseline float64, contender float64) string {
	if !active {
		return "-"
	}
	return fmt.Sprintf("%.2fx", speedup(baseline, contender))
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

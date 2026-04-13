package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	frontendcompiler "vexlua/internal/frontend/compiler"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/state"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/baseline"
)

const (
	defaultSamples          = 2
	defaultTargetDuration   = 10 * time.Millisecond
	defaultMaxCaseDuration  = 80 * time.Millisecond
	maxCalibrationAttempts  = 8
	maxCalibratedIterations = 50_000_000
)

const systemLuaHarnessSource = `local cache = {}

local function read_source(path)
    local file = assert(io.open(path, "rb"))
    local source = assert(file:read("*a"))
    file:close()
    return source
end

local function load_runner(name, path)
    local chunk, load_err = loadstring(read_source(path), "@" .. name)
    if not chunk then
        return nil, load_err
    end
    local run = chunk()
    if type(run) ~= "function" then
        return nil, "chunk must return function"
    end
    cache[name] = run
    return run
end

local function get_runner(name, path)
    local run = cache[name]
    if run ~= nil then
        return run
    end
    return load_runner(name, path)
end

local function format_number(number)
    if number ~= number then
        return "nan"
    end
    if number == math.huge then
        return "inf"
    end
    if number == -math.huge then
        return "-inf"
    end
    if number % 1 == 0 then
        return string.format("%.0f", number)
    end
    return string.format("%.17g", number)
end

for line in io.lines() do
    if line == "quit" then
        break
    end

    local command, name, path, iterations_text = string.match(line, "^([^\t]+)\t([^\t]+)\t([^\t]+)\t([^\t]+)$")
    if command ~= "run" then
        io.write("error\tinvalid command\n")
        io.flush()
    else
        local iterations = tonumber(iterations_text)
        if not iterations or iterations < 1 then
            io.write("error\tinvalid iteration count\n")
            io.flush()
        else
            local run, err = get_runner(name, path)
            if not run then
                io.write("error\t", tostring(err), "\n")
                io.flush()
            else
                local ok, result = pcall(run, iterations)
                if not ok then
                    io.write("error\t", tostring(result), "\n")
                    io.flush()
                elseif type(result) ~= "number" then
                    io.write("error\tbenchmark must return a number\n")
                    io.flush()
                else
                    io.write("ok\t", format_number(result), "\n")
                    io.flush()
                end
            end
        end
    end
end
`

type config struct {
	luaPath    string
	samples    int
	target     time.Duration
	maxCase    time.Duration
	caseFilter string
}

type sampleResult struct {
	Duration time.Duration
	Result   string
}

type caseSummary struct {
	BenchCase    benchmarkCase
	Iterations   int
	LuaMedian    time.Duration
	InterpMedian time.Duration
	JITMedian    time.Duration
	Result       string
}

type systemLuaRunner struct {
	command     *exec.Cmd
	stdin       io.WriteCloser
	stdout      *bufio.Reader
	stderr      bytes.Buffer
	workDir     string
	harnessPath string
	sourcePaths map[string]string
	closed      bool
}

type vexRunner struct {
	engine  *interp.Engine
	runtime *baseline.Runtime
	thread  *state.ThreadState
	runner  value.TValue
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "benchmark error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := parseConfig()
	if err != nil {
		return err
	}

	selectedCases, err := selectBenchmarkCases(config.caseFilter)
	if err != nil {
		return err
	}

	if config.luaPath == "" {
		config.luaPath, err = lookPathAny("lua5.1", "lua")
		if err != nil {
			return fmt.Errorf("resolve system lua: %w", err)
		}
	}

	systemRunner, err := newSystemLuaRunner(config.luaPath, selectedCases)
	if err != nil {
		return err
	}
	defer func() {
		_ = systemRunner.Close()
	}()

	summaries := make([]caseSummary, 0, len(selectedCases))
	for index, benchCase := range selectedCases {
		caseStarted := time.Now()
		fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", index+1, len(selectedCases), benchCase.Name)

		iterations, err := calibrateIterations(systemRunner, benchCase, config.target)
		if err != nil {
			return fmt.Errorf("calibrate %s: %w", benchCase.Name, err)
		}

		interpRunner, err := newVexRunner(benchCase, false)
		if err != nil {
			return fmt.Errorf("prepare interpreter %s: %w", benchCase.Name, err)
		}
		iterations, err = clampIterationsForInterpreter(interpRunner, iterations, config.maxCase)
		if err != nil {
			_ = interpRunner.Close()
			return fmt.Errorf("probe interpreter %s: %w", benchCase.Name, err)
		}

		luaSamples, err := collectSystemSamples(systemRunner, benchCase, iterations, config.samples)
		if err != nil {
			_ = interpRunner.Close()
			return fmt.Errorf("system lua %s: %w", benchCase.Name, err)
		}
		result := luaSamples[0].Result

		interpSamples, interpErr := collectVexSamples(interpRunner, iterations, config.samples, 1, result)
		closeInterpErr := interpRunner.Close()
		if interpErr != nil {
			return fmt.Errorf("interpreter %s: %w", benchCase.Name, interpErr)
		}
		if closeInterpErr != nil {
			return fmt.Errorf("close interpreter %s: %w", benchCase.Name, closeInterpErr)
		}

		jitRunner, err := newVexRunner(benchCase, true)
		if err != nil {
			return fmt.Errorf("prepare jit %s: %w", benchCase.Name, err)
		}
		jitSamples, jitErr := collectVexSamples(jitRunner, iterations, config.samples, 2, result)
		closeJITErr := jitRunner.Close()
		if jitErr != nil {
			return fmt.Errorf("jit %s: %w", benchCase.Name, jitErr)
		}
		if closeJITErr != nil {
			return fmt.Errorf("close jit %s: %w", benchCase.Name, closeJITErr)
		}

		summaries = append(summaries, caseSummary{
			BenchCase:    benchCase,
			Iterations:   iterations,
			LuaMedian:    medianDuration(luaSamples),
			InterpMedian: medianDuration(interpSamples),
			JITMedian:    medianDuration(jitSamples),
			Result:       result,
		})

		fmt.Fprintf(os.Stderr, "[%d/%d] %s done in %s\n", index+1, len(selectedCases), benchCase.Name, time.Since(caseStarted).Round(time.Millisecond))
	}

	printSummary(os.Stdout, summaries, config)
	return nil
}

func parseConfig() (config, error) {
	var config config
	flag.StringVar(&config.luaPath, "lua", "", "Path to system lua5.1 executable")
	flag.IntVar(&config.samples, "samples", defaultSamples, "Timed samples per benchmark case")
	flag.DurationVar(&config.target, "target", defaultTargetDuration, "Target system-lua duration used for calibration")
	flag.DurationVar(&config.maxCase, "max-case", defaultMaxCaseDuration, "Soft cap for interpreter sample duration per case")
	flag.StringVar(&config.caseFilter, "cases", "", "Comma-separated benchmark case list")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: go run ./cmd/benchmark [flags]\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Available cases: %s\n\n", strings.Join(benchmarkCaseNames(), ", "))
		flag.PrintDefaults()
	}
	flag.Parse()

	if config.samples < 1 {
		return config, fmt.Errorf("samples must be >= 1")
	}
	if config.target <= 0 {
		return config, fmt.Errorf("target must be > 0")
	}
	if config.maxCase <= 0 {
		return config, fmt.Errorf("max-case must be > 0")
	}
	return config, nil
}

func newSystemLuaRunner(luaPath string, cases []benchmarkCase) (*systemLuaRunner, error) {
	workDir, err := os.MkdirTemp("", "vexlua-benchmark-")
	if err != nil {
		return nil, fmt.Errorf("create work dir: %w", err)
	}

	runner := &systemLuaRunner{
		workDir:     workDir,
		harnessPath: filepath.Join(workDir, "harness.lua"),
		sourcePaths: make(map[string]string, len(cases)),
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = runner.Close()
		}
	}()

	if err := os.WriteFile(runner.harnessPath, []byte(systemLuaHarnessSource), 0o600); err != nil {
		return nil, fmt.Errorf("write lua harness: %w", err)
	}
	for _, benchCase := range cases {
		sourcePath := filepath.Join(workDir, benchCase.Name+".lua")
		if err := os.WriteFile(sourcePath, []byte(benchCase.Source), 0o600); err != nil {
			return nil, fmt.Errorf("write case source %s: %w", benchCase.Name, err)
		}
		runner.sourcePaths[benchCase.Name] = sourcePath
	}

	runner.command = exec.Command(luaPath, runner.harnessPath)
	runner.command.Stderr = &runner.stderr
	runner.stdin, err = runner.command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open lua stdin: %w", err)
	}
	stdoutPipe, err := runner.command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open lua stdout: %w", err)
	}
	runner.stdout = bufio.NewReader(stdoutPipe)
	if err := runner.command.Start(); err != nil {
		return nil, fmt.Errorf("start lua harness: %w", err)
	}

	cleanupOnError = false
	return runner, nil
}

func (runner *systemLuaRunner) Close() error {
	if runner == nil || runner.closed {
		return nil
	}
	runner.closed = true

	var closeErr error
	if runner.stdin != nil {
		_, _ = io.WriteString(runner.stdin, "quit\n")
		closeErr = errors.Join(closeErr, runner.stdin.Close())
	}
	if runner.command != nil {
		waitErr := runner.command.Wait()
		if waitErr != nil {
			stderrText := strings.TrimSpace(runner.stderr.String())
			if stderrText != "" {
				waitErr = fmt.Errorf("%w: %s", waitErr, stderrText)
			}
			closeErr = errors.Join(closeErr, waitErr)
		}
	}
	if runner.workDir != "" {
		closeErr = errors.Join(closeErr, os.RemoveAll(runner.workDir))
	}
	return closeErr
}

func (runner *systemLuaRunner) Run(benchCase benchmarkCase, iterations int) (sampleResult, error) {
	if runner == nil {
		return sampleResult{}, fmt.Errorf("system lua runner is nil")
	}
	if iterations < 1 {
		return sampleResult{}, fmt.Errorf("iterations must be >= 1")
	}
	sourcePath, ok := runner.sourcePaths[benchCase.Name]
	if !ok {
		return sampleResult{}, fmt.Errorf("missing source path for %s", benchCase.Name)
	}

	started := time.Now()
	if _, err := fmt.Fprintf(runner.stdin, "run\t%s\t%s\t%d\n", benchCase.Name, sourcePath, iterations); err != nil {
		return sampleResult{}, fmt.Errorf("send lua command: %w", err)
	}
	line, err := runner.stdout.ReadString('\n')
	if err != nil {
		stderrText := strings.TrimSpace(runner.stderr.String())
		if stderrText != "" {
			return sampleResult{}, fmt.Errorf("read lua response: %w: %s", err, stderrText)
		}
		return sampleResult{}, fmt.Errorf("read lua response: %w", err)
	}
	elapsed := time.Since(started)

	status, payload, ok := strings.Cut(strings.TrimSpace(line), "\t")
	if !ok {
		return sampleResult{}, fmt.Errorf("malformed lua response %q", strings.TrimSpace(line))
	}
	if status != "ok" {
		return sampleResult{}, fmt.Errorf("lua harness error: %s", payload)
	}
	return sampleResult{Duration: elapsed, Result: payload}, nil
}

func newVexRunner(benchCase benchmarkCase, withJIT bool) (*vexRunner, error) {
	engine := interp.New()
	thread, err := engine.NewThread(0, 0)
	if err != nil {
		_ = engine.Close()
		return nil, fmt.Errorf("new thread: %w", err)
	}
	env, err := engine.NewTable(0, 0)
	if err != nil {
		_ = engine.Close()
		return nil, fmt.Errorf("new env: %w", err)
	}
	proto, err := frontendcompiler.Compile("@benchmark/"+benchCase.Name+".lua", []byte(benchCase.Source))
	if err != nil {
		_ = engine.Close()
		return nil, fmt.Errorf("compile source: %w", err)
	}
	chunk, err := engine.NewClosure(proto, env.Value, nil)
	if err != nil {
		_ = engine.Close()
		return nil, fmt.Errorf("new chunk closure: %w", err)
	}
	results, err := engine.Call(thread, chunk.Value, nil, 1)
	if err != nil {
		_ = engine.Close()
		return nil, fmt.Errorf("execute chunk: %w", err)
	}
	if len(results) != 1 {
		_ = engine.Close()
		return nil, fmt.Errorf("chunk returned %d values, want 1", len(results))
	}
	if !results[0].IsBoxedTag(value.TagLuaClosureRef) {
		_ = engine.Close()
		return nil, fmt.Errorf("chunk returned %s, want lua closure", results[0])
	}

	runner := &vexRunner{engine: engine, thread: thread, runner: results[0]}
	if withJIT {
		runner.runtime = baseline.NewRuntime(engine)
	}
	return runner, nil
}

func (runner *vexRunner) Close() error {
	if runner == nil {
		return nil
	}
	var closeErr error
	if runner.runtime != nil {
		closeErr = errors.Join(closeErr, runner.runtime.Close())
	}
	if runner.engine != nil {
		closeErr = errors.Join(closeErr, runner.engine.Close())
	}
	return closeErr
}

func (runner *vexRunner) Run(iterations int) (sampleResult, error) {
	if iterations < 1 {
		return sampleResult{}, fmt.Errorf("iterations must be >= 1")
	}
	arg := value.NumberValue(float64(iterations))
	started := time.Now()
	var (
		results []value.TValue
		err     error
	)
	if runner.runtime != nil {
		results, err = runner.runtime.Call(runner.thread, runner.runner, []value.TValue{arg}, 1)
	} else {
		results, err = runner.engine.Call(runner.thread, runner.runner, []value.TValue{arg}, 1)
	}
	elapsed := time.Since(started)
	if err != nil {
		return sampleResult{}, err
	}
	formatted, err := formatValues(results)
	if err != nil {
		return sampleResult{}, err
	}
	return sampleResult{Duration: elapsed, Result: formatted}, nil
}

func calibrateIterations(systemRunner *systemLuaRunner, benchCase benchmarkCase, target time.Duration) (int, error) {
	iterations := maxInt(1, benchCase.MinIterations)
	for attempt := 0; attempt < maxCalibrationAttempts; attempt++ {
		sample, err := systemRunner.Run(benchCase, iterations)
		if err != nil {
			return 0, err
		}
		if sample.Duration >= target/2 && sample.Duration <= target*2 {
			return iterations, nil
		}
		if sample.Duration <= 0 {
			iterations = minInt(iterations*10, maxCalibratedIterations)
			continue
		}

		scale := float64(target) / float64(sample.Duration)
		next := int(math.Ceil(float64(iterations) * scale))
		if sample.Duration < target/2 {
			next = maxInt(next, iterations*2)
		} else {
			next = maxInt(benchCase.MinIterations, next)
		}
		next = minInt(next, maxCalibratedIterations)
		if next == iterations {
			break
		}
		iterations = next
	}
	return iterations, nil
}

func collectSystemSamples(systemRunner *systemLuaRunner, benchCase benchmarkCase, iterations int, samples int) ([]sampleResult, error) {
	if _, err := systemRunner.Run(benchCase, iterations); err != nil {
		return nil, fmt.Errorf("warm system lua: %w", err)
	}
	collected := make([]sampleResult, 0, samples)
	for index := 0; index < samples; index++ {
		sample, err := systemRunner.Run(benchCase, iterations)
		if err != nil {
			return nil, err
		}
		if index > 0 && sample.Result != collected[0].Result {
			return nil, fmt.Errorf("non-deterministic system-lua result: got %s want %s", sample.Result, collected[0].Result)
		}
		collected = append(collected, sample)
	}
	return collected, nil
}

func collectVexSamples(runner *vexRunner, iterations int, samples int, warmups int, wantResult string) ([]sampleResult, error) {
	for warmup := 0; warmup < warmups; warmup++ {
		sample, err := runner.Run(iterations)
		if err != nil {
			return nil, fmt.Errorf("warm call %d: %w", warmup+1, err)
		}
		if sample.Result != wantResult {
			return nil, fmt.Errorf("warm result = %s, want %s", sample.Result, wantResult)
		}
	}

	collected := make([]sampleResult, 0, samples)
	for index := 0; index < samples; index++ {
		sample, err := runner.Run(iterations)
		if err != nil {
			return nil, err
		}
		if sample.Result != wantResult {
			return nil, fmt.Errorf("result = %s, want %s", sample.Result, wantResult)
		}
		collected = append(collected, sample)
	}
	return collected, nil
}

func clampIterationsForInterpreter(runner *vexRunner, iterations int, maxCase time.Duration) (int, error) {
	if runner == nil || maxCase <= 0 {
		return iterations, nil
	}
	probe, err := runner.Run(iterations)
	if err != nil {
		return 0, err
	}
	if probe.Duration <= maxCase {
		return iterations, nil
	}
	scaled := int(math.Floor(float64(iterations) * float64(maxCase) / float64(probe.Duration)))
	if scaled >= iterations {
		scaled = iterations / 2
	}
	return maxInt(1, scaled), nil
}

func formatValues(results []value.TValue) (string, error) {
	if len(results) != 1 {
		return "", fmt.Errorf("expected 1 result, got %d", len(results))
	}
	number, ok := results[0].Float64()
	if !ok {
		return "", fmt.Errorf("expected numeric result, got %s", results[0])
	}
	if math.IsNaN(number) {
		return "nan", nil
	}
	if math.IsInf(number, 1) {
		return "inf", nil
	}
	if math.IsInf(number, -1) {
		return "-inf", nil
	}
	if number == math.Trunc(number) {
		return fmt.Sprintf("%.0f", number), nil
	}
	return strconv.FormatFloat(number, 'g', 17, 64), nil
}

func medianDuration(samples []sampleResult) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	durations := make([]time.Duration, len(samples))
	for index, sample := range samples {
		durations[index] = sample.Duration
	}
	sort.Slice(durations, func(i int, j int) bool { return durations[i] < durations[j] })
	middle := len(durations) / 2
	if len(durations)%2 == 1 {
		return durations[middle]
	}
	return (durations[middle-1] + durations[middle]) / 2
}

func printSummary(writer io.Writer, summaries []caseSummary, config config) {
	table := tabwriter.NewWriter(writer, 0, 0, 2, ' ', 0)
	fmt.Fprintf(table, "case\tdescription\titerations\tlua5.1\tinterp\tjit\tresult\n")
	for _, summary := range summaries {
		fmt.Fprintf(
			table,
			"%s\t%s\t%d\t%s (1.00x)\t%s (%s)\t%s (%s)\t%s\n",
			summary.BenchCase.Name,
			summary.BenchCase.Description,
			summary.Iterations,
			formatDuration(summary.LuaMedian),
			formatDuration(summary.InterpMedian),
			formatSpeedRatio(summary.InterpMedian, summary.LuaMedian),
			formatDuration(summary.JITMedian),
			formatSpeedRatio(summary.JITMedian, summary.LuaMedian),
			summary.Result,
		)
	}
	_ = table.Flush()

	interpGeomean := geometricMeanSpeed(summaries, func(summary caseSummary) time.Duration { return summary.InterpMedian })
	jitGeomean := geometricMeanSpeed(summaries, func(summary caseSummary) time.Duration { return summary.JITMedian })
	fmt.Fprintf(writer, "\nselected %d cases, %d timed samples each, calibrated against %s target, interpreter capped at %s\n", len(summaries), config.samples, config.target, config.maxCase)
	fmt.Fprintf(writer, "geomean relative speed: interp %s, jit %s\n", formatFloatRatio(interpGeomean), formatFloatRatio(jitGeomean))
	fmt.Fprintf(writer, "system lua path: %s\n", config.luaPath)
}

func geometricMeanSpeed(summaries []caseSummary, pick func(caseSummary) time.Duration) float64 {
	if len(summaries) == 0 {
		return 0
	}
	sum := 0.0
	count := 0
	for _, summary := range summaries {
		if summary.LuaMedian <= 0 {
			continue
		}
		ratio := float64(summary.LuaMedian) / float64(pick(summary))
		if ratio <= 0 {
			continue
		}
		sum += math.Log(ratio)
		count++
	}
	if count == 0 {
		return 0
	}
	return math.Exp(sum / float64(count))
}

func formatDuration(duration time.Duration) string {
	if duration >= time.Second {
		return fmt.Sprintf("%.3fs", duration.Seconds())
	}
	if duration >= time.Millisecond {
		return fmt.Sprintf("%.3fms", float64(duration)/float64(time.Millisecond))
	}
	if duration >= time.Microsecond {
		return fmt.Sprintf("%.3fus", float64(duration)/float64(time.Microsecond))
	}
	return fmt.Sprintf("%dns", duration)
}

func formatSpeedRatio(duration time.Duration, baseline time.Duration) string {
	if baseline <= 0 || duration <= 0 {
		return "n/a"
	}
	return formatFloatRatio(float64(baseline) / float64(duration))
}

func formatFloatRatio(ratio float64) string {
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return "n/a"
	}
	return fmt.Sprintf("%.2fx", ratio)
}

func lookPathAny(names ...string) (string, error) {
	var lastErr error
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no executable names provided")
	}
	return "", lastErr
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

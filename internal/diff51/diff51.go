package diff51

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"vexlua"
)

const resultSeparator = "<|>"

type Result struct {
	Case      Case
	LuaOutput string
	VexOutput string
	Match     bool
}

func DetectLua(explicit string) (string, string, error) {
	candidates := make([]string, 0, 6)
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		if env := strings.TrimSpace(os.Getenv("VEXLUA_LUA51_BIN")); env != "" {
			candidates = append(candidates, env)
		}
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
	return "", "", errors.New("no Lua 5.1 executable found; set VEXLUA_LUA51_BIN or pass -lua-bin")
}

func RunCase(luaBin string, testCase Case) (Result, error) {
	source, err := loadCaseSource(testCase)
	if err != nil {
		return Result{}, fmt.Errorf("load case %s: %w", testCase.Name, err)
	}
	if testCase.CaptureStdout {
		return withSharedScriptTempDir(func(tempDir string) (Result, error) {
			vexOutput, err := runVexScriptInDir(tempDir, source, testCase)
			if err != nil {
				return Result{}, fmt.Errorf("run VexLua case %s: %w", testCase.Name, err)
			}
			luaOutput, err := runLuaScriptInDir(tempDir, luaBin, source, testCase)
			if err != nil {
				return Result{}, fmt.Errorf("run Lua 5.1 case %s: %w", testCase.Name, err)
			}
			return Result{
				Case:      testCase,
				LuaOutput: luaOutput,
				VexOutput: vexOutput,
				Match:     luaOutput == vexOutput,
			}, nil
		})
	}
	vexOutput, err := runVex(buildHarness(source, false))
	if err != nil {
		return Result{}, fmt.Errorf("run VexLua case %s: %w", testCase.Name, err)
	}
	luaOutput, err := runLua(luaBin, buildHarness(source, true), testCase.Name)
	if err != nil {
		return Result{}, fmt.Errorf("run Lua 5.1 case %s: %w", testCase.Name, err)
	}
	return Result{
		Case:      testCase,
		LuaOutput: luaOutput,
		VexOutput: vexOutput,
		Match:     luaOutput == vexOutput,
	}, nil
}

func loadCaseSource(testCase Case) (string, error) {
	if testCase.Source != "" {
		return testCase.Source, nil
	}
	if testCase.SourceFile == "" {
		return "", fmt.Errorf("empty case source")
	}
	data, err := os.ReadFile(testCase.SourceFile)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func FormatOutput(output string) string {
	return strings.ReplaceAll(output, resultSeparator, " | ")
}

func runVex(source string) (string, error) {
	engine := vexlua.NewWithOptions(vexlua.Options{EnableJIT: false, HotThreshold: 16})
	value, err := engine.DoString(source)
	if err != nil {
		return "", err
	}
	return engine.FormatValue(value), nil
}

func runVexScript(source string, testCase Case) (string, error) {
	return withTempDir(func(tempDir string) (string, error) {
		return runVexScriptInDir(tempDir, source, testCase)
	})
}

func runVexScriptInDir(tempDir string, source string, testCase Case) (string, error) {
	scriptSource := buildScriptHarness(source, testCase)
	scriptPath := filepath.Join(tempDir, testCase.Name+".lua")
	output, err := captureStdout(func() error {
		return withWorkingDir(tempDir, func() error {
			engine := vexlua.NewWithOptions(vexlua.Options{EnableJIT: false, HotThreshold: 16})
			_, err := engine.DoStringNamed(scriptSource, "@"+scriptPath)
			return err
		})
	})
	if err != nil {
		return "", err
	}
	return normalizeOutput(output), nil
}

func runLua(luaBin string, source string, name string) (string, error) {
	tempDir, err := os.MkdirTemp("", "vexlua-lua51diff-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	scriptPath := filepath.Join(tempDir, name+".lua")
	if err := os.WriteFile(scriptPath, []byte(source), 0o600); err != nil {
		return "", err
	}
	cmd := exec.Command(luaBin, scriptPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}
	return normalizeOutput(string(output)), nil
}

func runLuaScript(luaBin string, source string, testCase Case) (string, error) {
	return withTempDir(func(tempDir string) (string, error) {
		return runLuaScriptInDir(tempDir, luaBin, source, testCase)
	})
}

func runLuaScriptInDir(tempDir string, luaBin string, source string, testCase Case) (string, error) {
	scriptSource := buildScriptHarness(source, testCase)
	scriptPath := filepath.Join(tempDir, testCase.Name+".lua")
	if err := os.WriteFile(scriptPath, []byte(scriptSource), 0o600); err != nil {
		return "", err
	}
	cmd := exec.Command(luaBin, scriptPath)
	cmd.Dir = tempDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}
	return normalizeOutput(string(output)), nil
}

func captureStdout(run func() error) (string, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	oldStdout := os.Stdout
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()
	type readResult struct {
		data []byte
		err  error
	}
	readDone := make(chan readResult, 1)
	go func() {
		data, readErr := io.ReadAll(reader)
		_ = reader.Close()
		readDone <- readResult{data: data, err: readErr}
	}()
	runErr := run()
	_ = writer.Close()
	dataResult := <-readDone
	if runErr != nil {
		return "", runErr
	}
	if dataResult.err != nil {
		return "", dataResult.err
	}
	return string(dataResult.data), nil
}

func normalizeOutput(output string) string {
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	return strings.TrimRight(normalized, "\r\n")
}

func withTempDir(run func(tempDir string) (string, error)) (string, error) {
	tempDir, err := os.MkdirTemp("", "vexlua-lua51diff-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	return run(tempDir)
}

func withSharedScriptTempDir(run func(tempDir string) (Result, error)) (Result, error) {
	tempDir, err := os.MkdirTemp("", "vexlua-lua51diff-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tempDir)
	return run(tempDir)
}

func withWorkingDir(dir string, run func() error) error {
	oldDir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	defer os.Chdir(oldDir)
	return run()
}

func buildScriptHarness(source string, testCase Case) string {
	return fmt.Sprintf(`local __source = %s
local __chunkname = %s
local __argv = %s
local __stdin = %s
local __files = %s
local __prelude = %s
local __postlude = %s

local function __normalize_error(err)
	local text = tostring(err)
	text = string.gsub(text, '^%%[string ".-"%%]:%%d+:%%s*', '')
	text = string.gsub(text, '^@?[^:\n]+:%%d+:%%s*', '')
	return text
end

local function __write_file(name, content)
	local file = assert(io.open(name, "wb"))
	assert(file:write(content))
	assert(file:close())
end

local function __run_chunk(chunk, suffix)
	local loader, err = loadstring(chunk, "@" .. __chunkname .. suffix)
	if not loader then
		return false, err
	end
	return pcall(loader)
end

local __arg = {[0] = __chunkname}
for i = 1, table.getn(__argv) do
	__arg[i] = __argv[i]
end
arg = __arg

if __files ~= nil then
	for name, content in pairs(__files) do
		__write_file(name, content)
	end
end
if __stdin ~= nil then
	__write_file("__stdin.txt", __stdin)
	assert(io.input("__stdin.txt"))
end
if __prelude ~= nil then
	local ok, err = __run_chunk(__prelude, ":prelude")
	if not ok then
		io.write("<PRELUDEERR>" .. __normalize_error(err))
		return
	end
end
local __ok, __err = __run_chunk(__source, "")
if __ok and __postlude ~= nil then
	__ok, __err = __run_chunk(__postlude, ":postlude")
end
if not __ok then
	io.write("<ERR>" .. __normalize_error(__err))
end
`,
		luaQuotedStringLiteral(source),
		luaQuotedStringLiteral(caseChunkName(testCase)),
		luaStringSliceLiteral(testCase.Args),
		luaOptionalStringLiteral(testCase.Stdin),
		luaStringMapLiteral(testCase.Files),
		luaOptionalStringLiteral(testCase.Prelude),
		luaOptionalStringLiteral(testCase.Postlude),
	)
}

func caseChunkName(testCase Case) string {
	if testCase.SourceFile != "" {
		return filepath.ToSlash(testCase.SourceFile)
	}
	return testCase.Name + ".lua"
}

func luaOptionalStringLiteral(text string) string {
	if text == "" {
		return "nil"
	}
	return luaQuotedStringLiteral(text)
}

func luaStringSliceLiteral(values []string) string {
	if len(values) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, luaQuotedStringLiteral(value))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func luaStringMapLiteral(values map[string]string) string {
	if len(values) == 0 {
		return "nil"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, "["+luaQuotedStringLiteral(key)+"] = "+luaQuotedStringLiteral(values[key]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func buildHarness(source string, emitOutput bool) string {
	resultSink := "return __result"
	if emitOutput {
		resultSink = "io.write(__result)"
	}
	return fmt.Sprintf(`local __source = %s
local __sep = %s

local function __serialize_one(value)
	local kind = type(value)
	if kind == "nil" then
		return "nil"
	end
	return kind .. ":" .. tostring(value)
end

local function __pack(...)
	return { n = select("#", ...), ... }
end

local function __serialize_pack(pack)
	local out = {"OK", tostring(pack.n)}
	for i = 1, pack.n do
		out[#out + 1] = __serialize_one(pack[i])
	end
	return table.concat(out, __sep)
end

local __load_ok, __loader_or_nil, __load_err = pcall(loadstring, __source)
if not __load_ok then
	return table.concat({"LOADERR", __serialize_one(__loader_or_nil)}, __sep)
end
if not __loader_or_nil then
	return table.concat({"LOADERR", __serialize_one(__load_err)}, __sep)
end
local __loader = __loader_or_nil

local __ok, __pack_or_err = pcall(function()
	return __pack(__loader())
end)
if not __ok then
	return table.concat({"ERR", __serialize_one(__pack_or_err)}, __sep)
end

local __result = __serialize_pack(__pack_or_err)
%s
`, luaQuotedStringLiteral(source), luaQuotedStringLiteral(resultSeparator), resultSink)
}

func luaQuotedStringLiteral(text string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range text {
		switch r {
		case '\\':
			builder.WriteString("\\\\")
		case '"':
			builder.WriteString("\\\"")
		case '\n':
			builder.WriteString("\\n")
		case '\t':
			builder.WriteString("\\t")
		case '\r':
			builder.WriteString("\\n")
		default:
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

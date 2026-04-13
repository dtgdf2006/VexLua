package testsupport_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"vexlua/internal/bytecode"
	"vexlua/internal/frontend/chunk"
	frontendcompiler "vexlua/internal/frontend/compiler"
	"vexlua/internal/interp"
	"vexlua/internal/runtime/host"
	"vexlua/internal/runtime/state"
	rttable "vexlua/internal/runtime/table"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/baseline"
)

const lua51CompileScript = `
local source_path = assert(arg[1], "missing source path")
local chunk_path = assert(arg[2], "missing chunk path")
local chunk_name = arg[3] or ("@" .. source_path)

local source_file = assert(io.open(source_path, "rb"))
local source = assert(source_file:read("*a"))
source_file:close()

local func, err = loadstring(source, chunk_name)
if not func then
  io.stderr:write(err, "\n")
  os.exit(1)
end

local chunk_file = assert(io.open(chunk_path, "wb"))
chunk_file:write(string.dump(func))
chunk_file:close()
`

const lua51EvalScript = `
local function hex(text)
  return (text:gsub('.', function(ch)
    return string.format('%02x', string.byte(ch))
  end))
end

local function pack(...)
  return { n = select('#', ...), ... }
end

local function write_value(out, current)
  local kind = type(current)
  if kind == 'nil' then
    out:write('nil\n')
    return true
  end
  if kind == 'boolean' then
    out:write('bool\t', current and '1' or '0', '\n')
    return true
  end
  if kind == 'number' then
    if current ~= current then
      out:write('num\tnan\n')
    else
      out:write('num\t', string.format('%.17g', current), '\n')
    end
    return true
  end
  if kind == 'string' then
    out:write('str\t', tostring(#current), '\t', hex(current), '\n')
    return true
  end
  out:write('unsupported\t', kind, '\n')
  return false
end

local source_path = assert(arg[1], 'missing source path')
local result_path = assert(arg[2], 'missing result path')
local chunk_name = arg[3] or ('@' .. source_path)

local source_file = assert(io.open(source_path, 'rb'))
local source = assert(source_file:read('*a'))
source_file:close()

local out = assert(io.open(result_path, 'wb'))
local func, err = loadstring(source, chunk_name)
if not func then
  out:write('loaderr\t', hex(tostring(err)), '\n')
  out:close()
  os.exit(0)
end

local results = pack(pcall(func))
if results[1] then
  out:write('ok\t', tostring(results.n - 1), '\n')
  for index = 2, results.n do
    if not write_value(out, results[index]) then
      out:close()
      os.exit(1)
    end
  end
else
  out:write('err\t', hex(tostring(results[2])), '\n')
end
out:close()
`

type lua51Value struct {
	kind      string
	boolValue bool
	numValue  float64
	strValue  string
}

type lua51Outcome struct {
	ok     bool
	values []lua51Value
	err    string
}

type currentOutcome struct {
	values []value.TValue
	err    string
}

var lua51NamedTypeErrorPattern = regexp.MustCompile(`^(attempt to .+?) (?:global|local|field|method|upvalue) '.*' \((a .+ value)\)$`)

type lua51DiffHarness struct {
	luaPath           string
	luacPath          string
	compileScriptPath string
	evalScriptPath    string
	engine            *interp.Engine
	runtime           *baseline.Runtime
	thread            *state.ThreadState
	env               value.TValue
}

func newLua51DiffHarness(t *testing.T) *lua51DiffHarness {
	t.Helper()

	luaPath, err := lookPathAny("lua5.1", "lua")
	if err != nil {
		t.Skip("lua5.1 not found on PATH")
	}
	luacPath, _ := lookPathAny("luac5.1", "luac")

	workDir := t.TempDir()
	compileScriptPath := filepath.Join(workDir, "compile.lua")
	evalScriptPath := filepath.Join(workDir, "eval.lua")
	if err := os.WriteFile(compileScriptPath, []byte(lua51CompileScript), 0o600); err != nil {
		t.Fatalf("write compile script: %v", err)
	}
	if err := os.WriteFile(evalScriptPath, []byte(lua51EvalScript), 0o600); err != nil {
		t.Fatalf("write eval script: %v", err)
	}

	engine := interp.New()
	runtime := baseline.NewRuntime(engine)
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = engine.Close()
	})

	thread, err := engine.NewThread(0, 0)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	envHandle, err := engine.NewTable(0, 0)
	if err != nil {
		t.Fatalf("create env: %v", err)
	}

	h := &lua51DiffHarness{
		luaPath:           luaPath,
		luacPath:          luacPath,
		compileScriptPath: compileScriptPath,
		evalScriptPath:    evalScriptPath,
		engine:            engine,
		runtime:           runtime,
		thread:            thread,
		env:               envHandle.Value,
	}
	h.installBuiltins(t)
	return h
}

func (h *lua51DiffHarness) installBuiltins(t *testing.T) {
	t.Helper()

	h.registerBuiltin(t, "setmetatable", func(tableValue value.TValue, metatable value.TValue) (value.TValue, error) {
		ref, err := h.tableRef(tableValue, "setmetatable")
		if err != nil {
			return value.NilValue(), err
		}
		if !metatable.IsBoxedTag(value.TagNil) && !metatable.IsBoxedTag(value.TagTableRef) {
			return value.NilValue(), fmt.Errorf("setmetatable expects table or nil metatable, got %s", metatable)
		}
		if err := h.engine.Tables.SetMetatable(ref, metatable); err != nil {
			return value.NilValue(), err
		}
		return tableValue, nil
	})

	h.registerBuiltin(t, "getmetatable", func(target any) (value.TValue, error) {
		targetValue, err := host.FromHostValue(h.engine.Strings, target)
		if err != nil {
			return value.NilValue(), err
		}
		metatable, found, err := h.engine.GetMetatableBoundary(targetValue)
		if err != nil {
			return value.NilValue(), err
		}
		if !found {
			return value.NilValue(), nil
		}
		return metatable, nil
	})

	h.registerBuiltin(t, "rawget", func(tableValue value.TValue, key any) (value.TValue, error) {
		ref, err := h.tableRef(tableValue, "rawget")
		if err != nil {
			return value.NilValue(), err
		}
		keyValue, err := host.FromHostValue(h.engine.Strings, key)
		if err != nil {
			return value.NilValue(), err
		}
		result, _, err := h.engine.Tables.Get(ref, keyValue)
		if err != nil {
			return value.NilValue(), err
		}
		return result, nil
	})

	h.registerBuiltin(t, "rawset", func(tableValue value.TValue, key any, newValue any) (value.TValue, error) {
		ref, err := h.tableRef(tableValue, "rawset")
		if err != nil {
			return value.NilValue(), err
		}
		keyValue, err := host.FromHostValue(h.engine.Strings, key)
		if err != nil {
			return value.NilValue(), err
		}
		newSlotValue, err := host.FromHostValue(h.engine.Strings, newValue)
		if err != nil {
			return value.NilValue(), err
		}
		if err := h.engine.Tables.Set(ref, keyValue, newSlotValue); err != nil {
			return value.NilValue(), err
		}
		return tableValue, nil
	})

	h.registerBuiltin(t, "type", func(candidate any) string {
		return luaTypeNameFromHost(candidate)
	})

	debugTable, err := h.engine.NewTable(0, 2)
	if err != nil {
		t.Fatalf("new debug table: %v", err)
	}
	h.registerTableBuiltin(t, debugTable, "setmetatable", func(target any, metatable value.TValue) (value.TValue, error) {
		targetValue, err := host.FromHostValue(h.engine.Strings, target)
		if err != nil {
			return value.NilValue(), err
		}
		if err := h.engine.SetValueMetatableBoundary(targetValue, metatable); err != nil {
			return value.NilValue(), err
		}
		return targetValue, nil
	})
	h.registerTableBuiltin(t, debugTable, "getmetatable", func(target any) (value.TValue, error) {
		targetValue, err := host.FromHostValue(h.engine.Strings, target)
		if err != nil {
			return value.NilValue(), err
		}
		metatable, found, err := h.engine.GetMetatableBoundary(targetValue)
		if err != nil {
			return value.NilValue(), err
		}
		if !found {
			return value.NilValue(), nil
		}
		return metatable, nil
	})
	if err := h.engine.SetGlobal(h.env, "debug", debugTable.Value); err != nil {
		t.Fatalf("set builtin debug: %v", err)
	}

	if err := h.engine.SetGlobal(h.env, "_G", h.env); err != nil {
		t.Fatalf("set builtin _G: %v", err)
	}
}

func (h *lua51DiffHarness) registerBuiltin(t *testing.T, name string, function any) {
	t.Helper()

	hostFunction, err := h.engine.RegisterHostFunction(name, function, h.env)
	if err != nil {
		t.Fatalf("register builtin %s: %v", name, err)
	}
	if err := h.engine.SetGlobal(h.env, name, hostFunction.Value); err != nil {
		t.Fatalf("set builtin %s: %v", name, err)
	}
}

func (h *lua51DiffHarness) registerTableBuiltin(t *testing.T, tableHandle rttable.Handle, name string, function any) {
	t.Helper()

	hostFunction, err := h.engine.RegisterHostFunction(name, function, h.env)
	if err != nil {
		t.Fatalf("register table builtin %s: %v", name, err)
	}
	key, err := h.engine.InternString(name)
	if err != nil {
		t.Fatalf("intern table builtin key %s: %v", name, err)
	}
	if err := h.engine.Tables.Set(tableHandle.Ref, key.Value, hostFunction.Value); err != nil {
		t.Fatalf("set table builtin %s: %v", name, err)
	}
}

func (h *lua51DiffHarness) tableRef(tableValue value.TValue, opname string) (value.HeapRef44, error) {
	if !tableValue.IsBoxedTag(value.TagTableRef) {
		return 0, fmt.Errorf("%s expects table, got %s", opname, tableValue)
	}
	ref, _ := tableValue.HeapRef()
	return ref, nil
}

func (h *lua51DiffHarness) assertSourceMatches(t *testing.T, name string, source string) {
	t.Helper()

	reference, chunkBytes := h.compileAndRunReference(t, name, source)
	if !reference.ok {
		t.Fatalf("reference Lua 5.1 returned error for %s: %s", name, reference.err)
	}
	chunkHarness := newLua51DiffHarness(t)
	chunkCurrent := chunkHarness.runCurrentChunk(t, name, chunkBytes)
	if chunkCurrent.err != "" {
		t.Fatalf("current VM returned error for %s from reference chunk: %s", name, chunkCurrent.err)
	}
	sourceHarness := newLua51DiffHarness(t)
	sourceCurrent := sourceHarness.runCurrentSource(t, name, source)
	if sourceCurrent.err != "" {
		t.Fatalf("current VM returned error for %s from source compile: %s", name, sourceCurrent.err)
	}
	assertTValueSliceEqual(t, chunkCurrent.values, chunkHarness.referenceValuesAsTValues(t, reference.values))
	assertTValueSliceEqual(t, sourceCurrent.values, sourceHarness.referenceValuesAsTValues(t, reference.values))
}

func (h *lua51DiffHarness) assertSourceBothError(t *testing.T, name string, source string) {
	t.Helper()

	reference, chunkBytes := h.compileAndRunReference(t, name, source)
	if reference.ok {
		t.Fatalf("reference Lua 5.1 unexpectedly succeeded for %s with %d values", name, len(reference.values))
	}
	chunkHarness := newLua51DiffHarness(t)
	chunkCurrent := chunkHarness.runCurrentChunk(t, name, chunkBytes)
	if chunkCurrent.err == "" {
		t.Fatalf("current VM unexpectedly succeeded for %s; reference error: %s", name, reference.err)
	}
	sourceHarness := newLua51DiffHarness(t)
	sourceCurrent := sourceHarness.runCurrentSource(t, name, source)
	if sourceCurrent.err == "" {
		t.Fatalf("current source compile unexpectedly succeeded for %s; reference error: %s", name, reference.err)
	}
	assertNormalizedLua51ErrorEqual(t, reference.err, chunkCurrent.err)
	assertNormalizedLua51ErrorEqual(t, reference.err, sourceCurrent.err)
}

func assertNormalizedLua51ErrorEqual(t *testing.T, referenceErr string, currentErr string) {
	t.Helper()

	normalizedReference := normalizeLua51Error(referenceErr)
	normalizedCurrent := normalizeLua51Error(currentErr)
	if normalizedReference != normalizedCurrent {
		t.Fatalf("normalized error mismatch:\nreference: %q\ncurrent:   %q\nnormalized reference: %q\nnormalized current:   %q", referenceErr, currentErr, normalizedReference, normalizedCurrent)
	}
}

func normalizeLua51Error(text string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if newline := strings.Index(normalized, "\n"); newline >= 0 {
		normalized = normalized[:newline]
	}
	for {
		stripped, ok := stripLua51ErrorLocationPrefix(normalized)
		if !ok {
			break
		}
		normalized = stripped
	}
	normalized = strings.Replace(normalized, "'for' ", "for ", 1)
	if match := lua51NamedTypeErrorPattern.FindStringSubmatch(normalized); len(match) == 3 {
		normalized = match[1] + " " + match[2]
	}
	return strings.TrimSpace(normalized)
}

func stripLua51ErrorLocationPrefix(text string) (string, bool) {
	firstColon := strings.Index(text, ":")
	if firstColon < 0 || firstColon+2 >= len(text) {
		return text, false
	}
	secondColon := strings.Index(text[firstColon+1:], ":")
	if secondColon < 0 {
		return text, false
	}
	secondColon += firstColon + 1
	lineText := text[firstColon+1 : secondColon]
	if lineText == "" {
		return text, false
	}
	for _, ch := range lineText {
		if ch < '0' || ch > '9' {
			return text, false
		}
	}
	return strings.TrimLeft(text[secondColon+1:], " "), true
}

func (h *lua51DiffHarness) compileAndRunReference(t *testing.T, name string, source string) (lua51Outcome, []byte) {
	t.Helper()

	sourcePath, chunkPath, resultPath := h.newCasePaths(t, name)
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write source %s: %v", name, err)
	}
	h.compileReferenceChunkFile(t, sourcePath, chunkPath, name)
	h.runLuaScript(t, h.evalScriptPath, sourcePath, resultPath, name)

	chunkBytes, err := os.ReadFile(chunkPath)
	if err != nil {
		t.Fatalf("read compiled chunk %s: %v", name, err)
	}
	resultBytes, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read lua5.1 result %s: %v", name, err)
	}
	outcome, err := parseLua51Outcome(resultBytes)
	if err != nil {
		t.Fatalf("parse lua5.1 result for %s: %v", name, err)
	}
	return outcome, chunkBytes
}

func (h *lua51DiffHarness) compileReferenceProto(t *testing.T, name string, source string) *bytecode.Proto {
	t.Helper()
	chunkBytes := h.compileReferenceChunkBytes(t, name, source)
	proto, err := chunk.Load(name, chunkBytes)
	if err != nil {
		t.Fatalf("chunk.Load reference proto %s: %v", name, err)
	}
	return proto
}

func (h *lua51DiffHarness) compileReferenceChunkBytes(t *testing.T, name string, source string) []byte {
	t.Helper()
	sourcePath, chunkPath, _ := h.newCasePaths(t, name)
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write source %s: %v", name, err)
	}
	h.compileReferenceChunkFile(t, sourcePath, chunkPath, name)

	chunkBytes, err := os.ReadFile(chunkPath)
	if err != nil {
		t.Fatalf("read compiled chunk %s: %v", name, err)
	}
	return chunkBytes
}

func (h *lua51DiffHarness) compileCurrentProto(t *testing.T, name string, source string) *bytecode.Proto {
	t.Helper()
	proto, err := frontendcompiler.Compile(name, []byte(source))
	if err != nil {
		t.Fatalf("frontend compiler failed for %s: %v", name, err)
	}
	return proto
}

func (h *lua51DiffHarness) runCurrentSource(t *testing.T, name string, source string) currentOutcome {
	t.Helper()
	proto, err := frontendcompiler.Compile(name, []byte(source))
	if err != nil {
		return currentOutcome{err: err.Error()}
	}
	closure, err := h.engine.NewClosure(proto, h.env, nil)
	if err != nil {
		return currentOutcome{err: err.Error()}
	}
	results, err := h.runtime.Call(h.thread, closure.Value, nil, -1)
	if err != nil {
		return currentOutcome{err: err.Error()}
	}
	copied := make([]value.TValue, len(results))
	copy(copied, results)
	return currentOutcome{values: copied}
}

func (h *lua51DiffHarness) compileReferenceChunkFile(t *testing.T, sourcePath string, chunkPath string, name string) {
	t.Helper()
	if h.luacPath != "" {
		h.runLuac(t, sourcePath, chunkPath)
		return
	}
	h.runLuaScript(t, h.compileScriptPath, sourcePath, chunkPath, name)
}

func (h *lua51DiffHarness) newCasePaths(t *testing.T, name string) (string, string, string) {
	t.Helper()
	caseDir := t.TempDir()
	sourcePath := filepath.Join(caseDir, strings.TrimPrefix(strings.TrimPrefix(name, "@"), "="))
	if filepath.Ext(sourcePath) == "" {
		sourcePath += ".lua"
	}
	chunkPath := sourcePath + ".luac"
	resultPath := sourcePath + ".result"
	return sourcePath, chunkPath, resultPath
}

func (h *lua51DiffHarness) runLuac(t *testing.T, sourcePath string, chunkPath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, h.luacPath, "-o", chunkPath, sourcePath)
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("luac5.1 timed out compiling %s", filepath.Base(sourcePath))
	}
	if err != nil {
		t.Fatalf("luac5.1 failed compiling %s: %v\n%s", filepath.Base(sourcePath), err, strings.TrimSpace(string(output)))
	}
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

func (h *lua51DiffHarness) runLuaScript(t *testing.T, scriptPath string, args ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	commandArgs := append([]string{scriptPath}, args...)
	command := exec.CommandContext(ctx, h.luaPath, commandArgs...)
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("lua5.1 timed out running %s", filepath.Base(scriptPath))
	}
	if err != nil {
		t.Fatalf("lua5.1 failed running %s: %v\n%s", filepath.Base(scriptPath), err, strings.TrimSpace(string(output)))
	}
}

func (h *lua51DiffHarness) runCurrentChunk(t *testing.T, name string, chunkBytes []byte) currentOutcome {
	t.Helper()

	proto, err := chunk.Load(name, chunkBytes)
	if err != nil {
		return currentOutcome{err: err.Error()}
	}
	closure, err := h.engine.NewClosure(proto, h.env, nil)
	if err != nil {
		return currentOutcome{err: err.Error()}
	}
	results, err := h.runtime.Call(h.thread, closure.Value, nil, -1)
	if err != nil {
		return currentOutcome{err: err.Error()}
	}
	copied := make([]value.TValue, len(results))
	copy(copied, results)
	return currentOutcome{values: copied}
}

func (h *lua51DiffHarness) referenceValuesAsTValues(t *testing.T, reference []lua51Value) []value.TValue {
	t.Helper()

	converted := make([]value.TValue, 0, len(reference))
	for _, current := range reference {
		switch current.kind {
		case "nil":
			converted = append(converted, value.NilValue())
		case "bool":
			converted = append(converted, value.BoolValue(current.boolValue))
		case "num":
			converted = append(converted, value.NumberValue(current.numValue))
		case "str":
			handle, err := h.engine.InternString(current.strValue)
			if err != nil {
				t.Fatalf("intern reference string: %v", err)
			}
			converted = append(converted, handle.Value)
		default:
			t.Fatalf("unsupported reference value kind %q", current.kind)
		}
	}
	return converted
}

func parseLua51Outcome(data []byte) (lua51Outcome, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return lua51Outcome{}, fmt.Errorf("empty lua5.1 result")
	}
	lines := strings.Split(trimmed, "\n")
	header := strings.Split(lines[0], "\t")
	switch header[0] {
	case "ok":
		if len(header) != 2 {
			return lua51Outcome{}, fmt.Errorf("invalid ok header %q", lines[0])
		}
		count, err := strconv.Atoi(header[1])
		if err != nil {
			return lua51Outcome{}, fmt.Errorf("parse ok count: %w", err)
		}
		if len(lines)-1 != count {
			return lua51Outcome{}, fmt.Errorf("value count mismatch: header=%d lines=%d", count, len(lines)-1)
		}
		values := make([]lua51Value, 0, count)
		for _, line := range lines[1:] {
			parsed, err := parseLua51Value(line)
			if err != nil {
				return lua51Outcome{}, err
			}
			values = append(values, parsed)
		}
		return lua51Outcome{ok: true, values: values}, nil
	case "err", "loaderr":
		if len(header) != 2 {
			return lua51Outcome{}, fmt.Errorf("invalid error header %q", lines[0])
		}
		decoded, err := decodeLua51Hex(header[1])
		if err != nil {
			return lua51Outcome{}, err
		}
		return lua51Outcome{err: decoded}, nil
	default:
		return lua51Outcome{}, fmt.Errorf("unknown lua5.1 result header %q", header[0])
	}
}

func parseLua51Value(line string) (lua51Value, error) {
	parts := strings.Split(line, "\t")
	switch parts[0] {
	case "nil":
		return lua51Value{kind: "nil"}, nil
	case "bool":
		if len(parts) != 2 {
			return lua51Value{}, fmt.Errorf("invalid bool line %q", line)
		}
		return lua51Value{kind: "bool", boolValue: parts[1] == "1"}, nil
	case "num":
		if len(parts) != 2 {
			return lua51Value{}, fmt.Errorf("invalid num line %q", line)
		}
		if parts[1] == "nan" {
			return lua51Value{kind: "num", numValue: math.NaN()}, nil
		}
		number, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return lua51Value{}, fmt.Errorf("parse number %q: %w", parts[1], err)
		}
		return lua51Value{kind: "num", numValue: number}, nil
	case "str":
		if len(parts) != 3 {
			return lua51Value{}, fmt.Errorf("invalid string line %q", line)
		}
		decoded, err := decodeLua51Hex(parts[2])
		if err != nil {
			return lua51Value{}, err
		}
		if len(decoded) != mustParseLength(parts[1]) {
			return lua51Value{}, fmt.Errorf("string length mismatch for %q", line)
		}
		return lua51Value{kind: "str", strValue: decoded}, nil
	case "unsupported":
		if len(parts) != 2 {
			return lua51Value{}, fmt.Errorf("invalid unsupported line %q", line)
		}
		return lua51Value{}, fmt.Errorf("lua5.1 returned unsupported value kind %q", parts[1])
	default:
		return lua51Value{}, fmt.Errorf("unknown value line %q", line)
	}
}

func mustParseLength(text string) int {
	length, err := strconv.Atoi(text)
	if err != nil {
		panic(err)
	}
	return length
}

func decodeLua51Hex(text string) (string, error) {
	decoded, err := hex.DecodeString(text)
	if err != nil {
		return "", fmt.Errorf("decode hex %q: %w", text, err)
	}
	return string(decoded), nil
}

func assertTValueSliceEqual(t *testing.T, got []value.TValue, want []value.TValue) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("result count mismatch: got %d want %d", len(got), len(want))
	}
	for index := range want {
		if !tvalueEqual(got[index], want[index]) {
			t.Fatalf("result %d mismatch: got %s want %s", index, got[index], want[index])
		}
	}
}

func tvalueEqual(left value.TValue, right value.TValue) bool {
	if left.IsNumber() && right.IsNumber() {
		leftNumber, _ := left.Float64()
		rightNumber, _ := right.Float64()
		return leftNumber == rightNumber || (math.IsNaN(leftNumber) && math.IsNaN(rightNumber))
	}
	return left.Bits() == right.Bits()
}

func luaTypeName(slotValue value.TValue) string {
	if slotValue.IsBoxedTag(value.TagNil) {
		return "nil"
	}
	if _, ok := slotValue.Bool(); ok {
		return "boolean"
	}
	if slotValue.IsNumber() {
		return "number"
	}
	if slotValue.IsBoxedTag(value.TagStringRef) {
		return "string"
	}
	if slotValue.IsBoxedTag(value.TagTableRef) {
		return "table"
	}
	if slotValue.IsBoxedTag(value.TagLuaClosureRef) || slotValue.IsBoxedTag(value.TagHostFunctionRef) {
		return "function"
	}
	return "userdata"
}

func luaTypeNameFromHost(candidate any) string {
	switch typed := candidate.(type) {
	case nil:
		return "nil"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "number"
	case value.TValue:
		return luaTypeName(typed)
	default:
		return "userdata"
	}
}

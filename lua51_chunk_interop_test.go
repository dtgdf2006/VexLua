package vexlua

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	rt "vexlua/internal/runtime"
)

type lua51ChunkInteropCase struct {
	name    string
	source  string
	luaCall string
	vexArgs []rt.Value
	want    float64
}

var lua51ChunkInteropCases = []lua51ChunkInteropCase{
	{
		name: "simple_closure",
		source: `
return function(v)
	return v + 1
end
`,
		luaCall: "inner(41)",
		vexArgs: []rt.Value{rt.NumberValue(41)},
		want:    42,
	},
	{
		name: "nested_closure_upvalues",
		source: `
return function()
	local seed = 40
	local function make()
		local offset = 2
		return function(v)
			return v + seed + offset
		end
	end
	local fn = make()
	return fn(0)
end
`,
		luaCall: "inner()",
		want:    42,
	},
	{
		name: "numeric_for_loop",
		source: `
return function()
	local sum = 0
	for i = 1, 4 do
		sum = sum + i
	end
	for i = 5, 1, -2 do
		sum = sum + i
	end
	return sum
end
`,
		luaCall: "inner()",
		want:    19,
	},
	{
		name: "break_repeat_until",
		source: `
return function()
	local out = 0
	for i = 1, 4 do
		if i > 2 then
			break
		end
		out = out + i
	end
	local j = 2
	repeat
		out = out + j
		j = j - 1
	until j == 0
	return out
end
`,
		luaCall: "inner()",
		want:    6,
	},
	{
		name: "method_call",
		source: `
return function()
	local box = {base = 40}
	function box:inc(v)
		return self.base + v + 1
	end
	return box:inc(1)
end
`,
		luaCall: "inner()",
		want:    42,
	},
	{
		name: "string_method_call",
		source: `
return function()
	return (("vexlua"):sub(2, 4) == "exl") and 42 or 0
end
`,
		luaCall: "inner()",
		want:    42,
	},
	{
		name: "table_constructor_tail_multret",
		source: `
return function()
	local function triple()
		return 2, 3, 4
	end
	local t = {x = 5, 1, triple()}
	return t.x + t[1] * 10000 + t[2] * 1000 + t[3] * 100 + t[4] * 10
end
`,
		luaCall: "inner()",
		want:    12345,
	},
	{
		name: "call_arg_multret",
		source: `
return function()
	local function triple()
		return 2, 3, 4
	end
	local function pack(a, b, c, d)
		return a * 1000 + b * 100 + c * 10 + d
	end
	return pack(1, triple())
end
`,
		luaCall: "inner()",
		want:    1234,
	},
	{
		name: "multi_return_assignment",
		source: `
return function()
	local function pair()
		return 20, 22
	end
	local a, b = pair()
	return a + b
end
`,
		luaCall: "inner()",
		want:    42,
	},
	{
		name: "vararg_spread",
		source: `
return function(...)
	local function spread(...)
		return ...
	end
	local function pack(a, b, c, d)
		return a * 1000 + b * 100 + c * 10 + d
	end
	return pack(1, spread(...))
end
`,
		luaCall: "inner(2, 3, 4)",
		vexArgs: []rt.Value{rt.NumberValue(2), rt.NumberValue(3), rt.NumberValue(4)},
		want:    1234,
	},
	{
		name: "generic_for_pairs_ipairs",
		source: `
return function()
	local sum = 0
	for _, v in ipairs({10, 20}) do
		sum = sum + v
	end
	for _, v in pairs({x = 5, y = 7}) do
		sum = sum + v
	end
	return sum
end
`,
		luaCall: "inner()",
		want:    42,
	},
}

func detectLua51ForChunkInterop(t *testing.T) string {
	t.Helper()
	candidates := []string{"lua", "lua5.1", "lua51", "lua5_1"}
	if env := strings.TrimSpace(os.Getenv("VEXLUA_LUA51_BIN")); env != "" {
		candidates = append([]string{env}, candidates...)
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, "-v")
		output, err := cmd.CombinedOutput()
		version := strings.TrimSpace(string(output))
		if err == nil || version != "" {
			if strings.Contains(version, "Lua 5.1") {
				t.Logf("Lua baseline: %s (%s)", path, version)
				return path
			}
		}
	}
	t.Skip("skipping Lua 5.1 chunk interop test: no Lua 5.1 executable found")
	return ""
}

func runLua51ScriptInDir(t *testing.T, luaBin string, dir string, name string, source string) string {
	t.Helper()
	scriptPath := filepath.Join(dir, name)
	if err := os.WriteFile(scriptPath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(luaBin, scriptPath)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("system Lua 5.1 command failed: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	return normalizeStdout(string(output))
}

func dumpVexChunkForInterop(t *testing.T, testCase lua51ChunkInteropCase) []byte {
	t.Helper()
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	proto, err := engine.CompileStringNamed(testCase.source, "@input.lua")
	if err != nil {
		t.Fatal(err)
	}
	data, err := engine.DumpProto(proto)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte{0x1b, 'L', 'u', 'a'}) {
		t.Fatalf("expected official Lua 5.1 chunk header, got %v", data[:min(len(data), 6)])
	}
	return data
}

func dumpSystemLuaChunkForInterop(t *testing.T, luaBin string, tempDir string, testCase lua51ChunkInteropCase) []byte {
	t.Helper()
	if err := os.WriteFile(filepath.Join(tempDir, "input.lua"), []byte(testCase.source), 0o644); err != nil {
		t.Fatal(err)
	}
	runLua51ScriptInDir(t, luaBin, tempDir, "dump_chunk.lua", `
local fn = assert(loadfile("input.lua"))
local dumped = string.dump(fn)
local file = assert(io.open("system.luac", "wb"))
assert(file:write(dumped))
assert(file:close())
`)
	data, err := os.ReadFile(filepath.Join(tempDir, "system.luac"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte{0x1b, 'L', 'u', 'a'}) {
		t.Fatalf("expected official Lua 5.1 chunk header, got %v", data[:min(len(data), 6)])
	}
	return data
}

func callInteropClosureInVex(t *testing.T, data []byte, testCase lua51ChunkInteropCase) float64 {
	t.Helper()
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	loaded, err := engine.LoadProto(data)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(loaded)
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.machine.CallValue(result, testCase.vexArgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected loaded chunk to return callable function result")
	}
	return results[0].Number()
}

func TestVexLuaDumpedChunkRoundTripsInteropCases(t *testing.T) {
	for _, testCase := range lua51ChunkInteropCases {
		t.Run(testCase.name, func(t *testing.T) {
			data := dumpVexChunkForInterop(t, testCase)
			if got := callInteropClosureInVex(t, data, testCase); got != testCase.want {
				t.Fatalf("VexLua roundtrip chunk result = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestVexLuaDumpedChunkRunsOnSystemLua51(t *testing.T) {
	luaBin := detectLua51ForChunkInterop(t)
	for _, testCase := range lua51ChunkInteropCases {
		t.Run(testCase.name, func(t *testing.T) {
			data := dumpVexChunkForInterop(t, testCase)
			tempDir := t.TempDir()
			chunkPath := filepath.Join(tempDir, "vexlua.luac")
			if err := os.WriteFile(chunkPath, data, 0o644); err != nil {
				t.Fatal(err)
			}
			script := fmt.Sprintf(`
local fn = assert(loadfile(%q))
local inner = fn()
io.write(%s, "\n")
`, filepath.Base(chunkPath), testCase.luaCall)
			want := fmt.Sprintf("%.14g", testCase.want)
			got := runLua51ScriptInDir(t, luaBin, tempDir, "run_chunk.lua", script)
			if got != want {
				t.Fatalf("system Lua 5.1 executing VexLua chunk = %q, want %q", got, want)
			}
		})
	}
}

func TestSystemLua51DumpedChunkLoadsInVexLua(t *testing.T) {
	luaBin := detectLua51ForChunkInterop(t)
	for _, testCase := range lua51ChunkInteropCases {
		t.Run(testCase.name, func(t *testing.T) {
			data := dumpSystemLuaChunkForInterop(t, luaBin, t.TempDir(), testCase)
			if got := callInteropClosureInVex(t, data, testCase); got != testCase.want {
				t.Fatalf("VexLua executing system Lua 5.1 chunk = %v, want %v", got, testCase.want)
			}
		})
	}
}

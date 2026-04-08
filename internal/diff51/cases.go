package diff51

type Expectation int

const (
	ExpectMatch Expectation = iota
	ExpectKnownMismatch
)

func (e Expectation) String() string {
	switch e {
	case ExpectMatch:
		return "match"
	case ExpectKnownMismatch:
		return "known-mismatch"
	default:
		return "unknown"
	}
}

type Case struct {
	Name          string
	Notes         string
	Source        string
	SourceFile    string
	CaptureStdout bool
	Args          []string
	Stdin         string
	Files         map[string]string
	Prelude       string
	Postlude      string
	Expectation   Expectation
}

const officialScriptRoot = "testdata/lua-5.1.5/test"

func officialScript(name string) string {
	return officialScriptRoot + "/" + name
}

func DefaultCases() []Case {
	return []Case{
		{
			Name:  "numeric_for_sum",
			Notes: "数值 for、算术与 return",
			Source: `local sum = 0
for i = 1, 10 do
	sum = sum + i
end
return sum`,
			Expectation: ExpectMatch,
		},
		{
			Name:        "multi_return_string_match",
			Notes:       "string.match 的多返回序列化",
			Source:      `return string.match("go-42", "(%a+)%-(%d+)")`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "proper_tail_call_frame_reuse",
			Notes: "尾调用复用帧后，后续普通调用也不应覆盖当前帧",
			Source: `local loop, step
local function add0(x)
	return x
end
function loop(n, acc)
	if n == 0 then
		return acc
	end
	return step(n - 1, acc + 1)
end
function step(n, acc)
	local current = add0(acc)
	if n == 0 then
		return current
	end
	return loop(n, current)
end
return loop(50000, 0)`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "proper_tail_call_depth",
			Notes: "return f(...) 应该走 proper tail call，而不是无限累积 Lua 帧",
			Source: `local function loop(n, acc)
	if n == 0 then
		return acc
	end
	return loop(n - 1, acc + 1)
end
return loop(200000, 0)`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "package_preload_require",
			Notes: "package.preload 与 require 缓存语义",
			Source: `package.preload["demo.mod"] = function()
	return { answer = 42 }
end
local mod = require("demo.mod")
return mod.answer, require("demo.mod") == mod`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "coroutine_resume_multret",
			Notes: "coroutine.resume/yield 的多返回",
			Source: `local co = coroutine.create(function()
	local a, b = coroutine.yield(10, 20)
	return a + b, a * b
end)
local ok1, y1, y2 = coroutine.resume(co)
local ok2, r1, r2 = coroutine.resume(co, 3, 4)
return ok1, y1, y2, ok2, r1, r2`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "metatable_core_paths",
			Notes: "__tostring、__call、__lt 和 <= fallback",
			Source: `local mt = {
	__tostring = function(self)
		return "box:" .. self.v
	end,
	__call = function(self, a, b)
		return self.v + a + b
	end,
	__lt = function(lhs, rhs)
		return lhs.v < rhs.v
	end,
}
local a = setmetatable({v = 4}, mt)
local b = setmetatable({v = 9}, mt)
return tostring(a), a(2, 3), a < b, a <= b, b <= a`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "advanced_string_patterns",
			Notes: "%b/%f/backref/() 这些高级 pattern 语义",
			Source: `local p1, p2 = string.match("abc", "()b()")
local s, e = string.find("123abc", "%f[%a]abc")
local balanced = string.match("(ab)(ab)", "(%b())%1")
return p1, p2, s, e, balanced`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "dump_load_roundtrip",
			Notes: "string.dump -> loadstring(binary chunk)",
			Source: `local dumped = string.dump(function(v)
	local base = 2
	return v + base
end)
local loaded = assert(loadstring(dumped))
return loaded(40)`,
			Expectation: ExpectMatch,
		},
		{
			Name:        "thread_type",
			Notes:       "coroutine.create 应该返回 Lua thread 值类型",
			Source:      `return type(coroutine.create(function() end))`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "string_metatable_method_call",
			Notes: "string metatable 的 __index 应该指向 string 库",
			Source: `local mt = getmetatable("")
return ("vexlua"):sub(2, 4), type(mt), mt.__index == string`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "string_format_quote_and_numeric_coercion",
			Notes: "%q quoting 和 string 库对 number -> string 的兼容",
			Source: `local quoted = string.format("%q", string.char(65, 0, 66, 10, 67, 13, 34, 92))
return string.len(10), string.sub(12345, 2, 4), quoted`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "string_literal_syntax",
			Notes: "long string / long comment / 短字符串转义",
			Source: `--[=[
ignored comment
]=]
local long = [=[alpha
beta]=]
local short = "\097\10\r\t\v\f\b\a\\\"\'"
return long, short`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "userdata_and_non_table_metatable",
			Notes: "newproxy userdata、复制 metatable 与 number __index",
			Source: `local proxy = newproxy(true)
local proxy2 = newproxy(proxy)
local meta = getmetatable(proxy)
meta.__index = { answer = 42 }
debug.setmetatable(1, { __index = function(v, key) return v + 41 end })
local n = 1
return type(proxy), proxy2.answer, n.answer, getmetatable(proxy2) == meta`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "nil_metatable_debug_support",
			Notes: "debug.setmetatable(nil, ...) 应该驱动 nil 的 __index/__newindex/__tostring 与保护元表语义",
			Source: `local sink = {}
local raw = {
	__metatable = "nil-locked",
	__index = function(value, key)
		if value == nil and key == "answer" then
			return 42
		end
	end,
	__newindex = function(value, key, assigned)
		sink[key] = assigned
	end,
	__tostring = function(value)
		if value == nil then
			return "nil-meta"
		end
		return "unexpected"
	end,
}
debug.setmetatable(nil, raw)
local probe = nil
probe.written = 7
local read = (function()
	local value = nil
	return value.answer
end)()
local visible = getmetatable(nil)
local direct = debug.getmetatable(nil)
local printed = tostring(nil)
debug.setmetatable(nil, nil)
return visible, direct == raw, read, sink.written, printed, getmetatable(nil) == nil, debug.getmetatable(nil) == nil`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "userdata_env_and_debug_info",
			Notes: "userdata environment、debug.getinfo/getupvalue/setupvalue/getregistry",
			Source: `local proxy = newproxy(true)
local env = { answer = 42 }
debug.setfenv(proxy, env)
local function make()
	local captured = 41
	return function()
		return captured
	end
end
local fn = make()
local info = debug.getinfo(fn, "Su")
local name, value = debug.getupvalue(fn, 1)
local changed = debug.setupvalue(fn, 1, 99)
local _, updated = debug.getupvalue(fn, 1)
return debug.getfenv(proxy) == env, info.what, info.nups, name, value, changed, updated, fn(), type(debug.getregistry())`,
			Expectation: ExpectMatch,
		},
		{
			Name:        "missing_base_globals",
			Notes:       "_G 和 _VERSION 基础全局应该存在",
			Source:      `return _G ~= nil, _VERSION`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "io_os_loadfile_and_gc",
			Notes: "io/os/loadfile/dofile/collectgarbage/gcinfo 的主路径",
			Source: `local dataPath = os.tmpname()
local movedPath = dataPath .. ".moved"
local codePath = dataPath .. ".lua"
local writer = assert(io.open(dataPath, "w"))
assert(writer:write("alpha\nbeta\n"))
assert(writer:close())
local reader = assert(io.open(dataPath, "r"))
local line1 = reader:read()
local line2 = reader:read("*l")
assert(reader:close())
assert(os.rename(dataPath, movedPath))
local moved = assert(io.open(movedPath, "r"))
local all = moved:read("*a")
assert(moved:close())
assert(os.remove(movedPath))
local chunk = assert(io.open(codePath, "w"))
assert(chunk:write("return 20 + 22"))
assert(chunk:close())
local loaded = assert(loadfile(codePath))
local total = loaded() + dofile(codePath)
assert(os.remove(codePath))
local utc = os.date("!%Y-%m-%d", 86400)
local stamp = os.time({year = 2001, month = 9, day = 9, hour = 1, min = 46, sec = 40})
local stampInfo = os.date("*t", stamp)
return line1, line2, all, total, io.type(io.stdout), utc, stampInfo.year, stampInfo.month, stampInfo.day, stampInfo.hour, stampInfo.min, stampInfo.sec, type(collectgarbage("count")), type(collectgarbage("setpause", 150)), type(gcinfo())`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "math_library_surface",
			Notes: "math 的常用常量与函数应该可用",
			Source: `local ip, fp = math.modf(3.25)
return math.sqrt(9), math.floor(2.9), math.ceil(2.1), math.pi > 3, ip, fp`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "do_block_statement",
			Notes: "standalone do ... end block 与局部作用域",
			Source: `do
	local x = 40
end
return 42`,
			Expectation: ExpectMatch,
		},
		{
			Name:  "weak_table_gc_and_userdata_finalizer",
			Notes: "弱值、弱键和 userdata __gc 应该在 collectgarbage 后生效",
			Source: `local finalized = 0
local weakValues = setmetatable({}, { __mode = "v" })
do
	local value = {}
	weakValues[1] = value
end
collectgarbage("collect")
collectgarbage("collect")
local weakKeys = setmetatable({}, { __mode = "k" })
do
	local key = {}
	weakKeys[key] = 42
end
collectgarbage("collect")
collectgarbage("collect")
do
	local proxy = newproxy(true)
	getmetatable(proxy).__gc = function()
		finalized = finalized + 1
	end
	proxy = nil
end
collectgarbage("collect")
collectgarbage("collect")
return weakValues[1] == nil, next(weakKeys) == nil, finalized`,
			Expectation: ExpectMatch,
		},
		{
			Name:          "official_hello_script",
			Notes:         "official hello.lua 的 stdout",
			SourceFile:    officialScript("hello.lua"),
			CaptureStdout: true,
			Expectation:   ExpectMatch,
		},
		{
			Name:          "official_factorial_script",
			Notes:         "official factorial.lua 的 stdout",
			SourceFile:    officialScript("factorial.lua"),
			CaptureStdout: true,
			Expectation:   ExpectMatch,
		},
		{
			Name:          "official_fibfor_script",
			Notes:         "official fibfor.lua 的 stdout",
			SourceFile:    officialScript("fibfor.lua"),
			CaptureStdout: true,
			Expectation:   ExpectMatch,
		},
		{
			Name:          "official_cf_script",
			Notes:         "official cf.lua 的 stdout",
			SourceFile:    officialScript("cf.lua"),
			CaptureStdout: true,
			Expectation:   ExpectMatch,
		},
		{
			Name:          "official_sort_script",
			Notes:         "official sort.lua 的 stdout",
			SourceFile:    officialScript("sort.lua"),
			CaptureStdout: true,
			Prelude: `local __rand_values = {4, 1, 7, 2, 9, 3, 8, 5, 6}
local __rand_index = 0
math.random = function(lower, upper)
	__rand_index = __rand_index + 1
	local value = __rand_values[((__rand_index - 1) % table.getn(__rand_values)) + 1]
	if lower == nil then
		return value / 10
	end
	if upper == nil then
		return ((value - 1) % lower) + 1
	end
	return ((value - 1) % (upper - lower + 1)) + lower
end`,
			Expectation: ExpectMatch,
		},
		{
			Name:          "official_sieve_script",
			Notes:         "official sieve.lua 的 stdout",
			SourceFile:    officialScript("sieve.lua"),
			CaptureStdout: true,
			Expectation:   ExpectMatch,
		},
		{
			Name:          "official_bisect_script",
			Notes:         "official bisect.lua 的 stdout",
			SourceFile:    officialScript("bisect.lua"),
			CaptureStdout: true,
			Expectation:   ExpectMatch,
		},
		{
			Name:          "official_echo_script",
			Notes:         "official echo.lua 的 argv stdout",
			SourceFile:    officialScript("echo.lua"),
			CaptureStdout: true,
			Args:          []string{"alpha", "beta", "gamma"},
			Expectation:   ExpectMatch,
		},
		{
			Name:          "official_env_script",
			Notes:         "official env.lua 的 getenv/global bridge stdout",
			SourceFile:    officialScript("env.lua"),
			CaptureStdout: true,
			Prelude: `local __env = {
	USER = "copilot",
	PATH = "/deterministic/bin",
}
os.getenv = function(name)
	return __env[name]
end`,
			Expectation: ExpectMatch,
		},
		{
			Name:          "official_fib_script",
			Notes:         "official fib.lua 的 argv 与 os.clock stdout",
			SourceFile:    officialScript("fib.lua"),
			CaptureStdout: true,
			Args:          []string{"24"},
			Prelude: `os.clock = function()
	return 0
end`,
			Expectation: ExpectMatch,
		},
		{
			Name:          "official_globals_script",
			Notes:         "official globals.lua 的 stdin filter stdout",
			SourceFile:    officialScript("globals.lua"),
			CaptureStdout: true,
			Stdin: `[1] GETGLOBAL 0 0 ; alpha
[2] SETGLOBAL 0 0 ; beta
[3] GETGLOBAL 0 0 ; alpha
[4] SETGLOBAL 0 0 ; gamma
`,
			Expectation: ExpectMatch,
		},
		{
			Name:          "official_life_script",
			Notes:         "official life.lua 的 stdout",
			SourceFile:    officialScript("life.lua"),
			CaptureStdout: true,
			Expectation:   ExpectMatch,
		},
		{
			Name:          "official_luac_script",
			Notes:         "official luac.lua 的 loadfile/string.dump 二进制输出（当前 chunk dump 还未与 Lua 5.1 完全字节对齐）",
			SourceFile:    officialScript("luac.lua"),
			CaptureStdout: true,
			Args:          []string{"input.lua"},
			Files: map[string]string{
				"input.lua": "return function(v) return v + 1 end\n",
			},
			Postlude: `local file = assert(io.open("luac.out", "rb"))
local data = assert(file:read("*a"))
assert(file:close())
for i = 1, string.len(data) do
	io.write(string.format("%02X", string.byte(data, i)))
	if i < string.len(data) then
		io.write(" ")
	end
end
io.write("\n")`,
			Expectation: ExpectKnownMismatch,
		},
		{
			Name:          "official_printf_script",
			Notes:         "official printf.lua 的 getenv/date stdout",
			SourceFile:    officialScript("printf.lua"),
			CaptureStdout: true,
			Prelude: `os.getenv = function(name)
	if name == "USER" then
		return "copilot"
	end
	return nil
end
os.date = function()
	return "Mon Jan  2 03:04:05 2006"
end`,
			Expectation: ExpectMatch,
		},
		{
			Name:          "official_readonly_script",
			Notes:         "official readonly.lua 的报错结果",
			SourceFile:    officialScript("readonly.lua"),
			CaptureStdout: true,
			Expectation:   ExpectMatch,
		},
		{
			Name:          "official_table_script",
			Notes:         "official table.lua 的 stdin regroup stdout",
			SourceFile:    officialScript("table.lua"),
			CaptureStdout: true,
			Stdin: `"apple" one
"apple" two
banana three
banana four
carrot five
`,
			Expectation: ExpectMatch,
		},
		{
			Name:          "official_trace_calls_script",
			Notes:         "official trace-calls.lua 的 debug hook stdout",
			SourceFile:    officialScript("trace-calls.lua"),
			CaptureStdout: true,
			Postlude: `local function leaf(v)
	return v + 1
end
local function branch(v)
	return leaf(v)
end
branch(41)`,
			Expectation: ExpectMatch,
		},
		{
			Name:          "official_trace_globals_script",
			Notes:         "official trace-globals.lua 的 stdout",
			SourceFile:    officialScript("trace-globals.lua"),
			CaptureStdout: true,
			Expectation:   ExpectMatch,
		},
		{
			Name:          "official_xd_script",
			Notes:         "official xd.lua 的 stdin hex dump stdout",
			SourceFile:    officialScript("xd.lua"),
			CaptureStdout: true,
			Stdin:         "Lua 5.1\nhex dump demo\n",
			Expectation:   ExpectMatch,
		},
	}
}

package vexlua

import (
	"bytes"
	"math"
	"path/filepath"
	"runtime"
	"testing"
)

type benchBox struct {
	Bias float64
}

func (b *benchBox) Scale(v float64) float64 {
	return v + b.Bias
}

func TestBridgeAndProxy(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 4})
	if err := engine.RegisterFunc("double", func(v float64) float64 { return v * 2 }); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterObject("box", &benchBox{Bias: 2.5}); err != nil {
		t.Fatal(err)
	}

	result, err := engine.Run(engine.BuildFunctionDemo("double", 21))
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("double demo = %v, want 42", got)
	}

	fieldProto := engine.BuildFieldAddDemo("box", "Bias", 7.5)
	result, err = engine.Run(fieldProto)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 10 {
		t.Fatalf("field demo = %v, want 10", got)
	}

	result, err = engine.Run(engine.BuildMethodDemo("box", "Scale", 10))
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 12.5 {
		t.Fatalf("method demo = %v, want 12.5", got)
	}
}

func TestQuickeningAndIC(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 32})
	if err := engine.RegisterTable("box", map[string]any{"Bias": 2.5}); err != nil {
		t.Fatal(err)
	}
	proto := engine.BuildFieldAddDemo("box", "Bias", 7.5)
	for i := 0; i < 2; i++ {
		if _, err := engine.Run(proto); err != nil {
			t.Fatal(err)
		}
	}
	stats := engine.Stats(proto)
	if stats.QuickenedOps == 0 {
		t.Fatalf("expected quickened ops, got %+v", stats)
	}
}

func TestJITSumLoop(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 2})
	proto := engine.BuildSumLoop(10000)
	var result Value
	var err error
	for i := 0; i < 4; i++ {
		result, err = engine.Run(proto)
		if err != nil {
			t.Fatal(err)
		}
	}
	want := float64(10000*10001) / 2
	if math.Abs(result.Number()-want) > 0.001 {
		t.Fatalf("sum loop = %v, want %v", result.Number(), want)
	}
	stats := engine.Stats(proto)
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" && !stats.JITCompiled {
		t.Fatalf("expected JIT compilation on windows amd64, got %+v", stats)
	}
}

func TestScriptedNumericForCanJIT(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 2})
	proto, err := engine.CompileString(`
local sum = 0
for i = 1, 1000 do
	sum = sum + i
end
return sum
`)
	if err != nil {
		t.Fatal(err)
	}
	var result Value
	for i := 0; i < 4; i++ {
		result, err = engine.Run(proto)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := result.Number(); got != 500500 {
		t.Fatalf("scripted numeric-for result = %v, want 500500", got)
	}
	stats := engine.Stats(proto)
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" && !stats.JITCompiled {
		t.Fatalf("expected scripted proto to reach JIT on windows amd64, got %+v", stats)
	}
}

func TestDoStringCachesSourceProto(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	source := "return 40 + 2"
	result, err := engine.DoString(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("first DoString result = %v, want 42", got)
	}
	first := engine.sources[source]
	if first == nil {
		t.Fatal("expected DoString to cache compiled proto")
	}
	result, err = engine.DoString(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("second DoString result = %v, want 42", got)
	}
	if engine.sources[source] != first {
		t.Fatal("expected DoString to reuse cached proto for identical source")
	}
}

func TestDoStringFunctionAndStdlib(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 4})
	result, err := engine.DoString(`
function inc(v)
	return v + 1
end
return inc(41)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("script function = %v, want 42", got)
	}

	result, err = engine.DoString(`
return math.max(40, 41) + 1
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("stdlib math = %v, want 42", got)
	}
}

func TestDoStringClosure(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local seed = 40
local function make()
	local offset = 2
	return function(v)
		return v + seed + offset
	end
end
local fn = make()
return fn(0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("closure result = %v, want 42", got)
	}
}

func TestDoStringCoroutine(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local co = coroutine.create(function(v)
	local next = coroutine.yield(v + 1)
	return next + 2
end)
local ok1, first = coroutine.resume(co, 40)
local ok2, second = coroutine.resume(co, 40)
return (ok1 and 1 or 0) + first + (ok2 and 1 or 0) + second
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 85 {
		t.Fatalf("coroutine result = %v, want 85", got)
	}
}

func TestLoadString(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local fn = loadstring("return 40 + 2")
return fn()
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("loadstring result = %v, want 42", got)
	}
}

func TestControlFlowTableAndComparison(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local t = {5, 7, 9, x = 1}
if t.x == 1 then
	t[2] = t[2] + 30
else
	t[2] = 0
end
local i = 1
local sum = 0
while i <= 3 do
	sum = sum + t[i]
	i = i + 1
end
return sum
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 51 {
		t.Fatalf("control flow result = %v, want 51", got)
	}
}

func TestUnaryNotGreaterAndStringIndex(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local t = {x = 41}
if not (3 > 4) then
	return t["x"] + 1
end
return 0
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("not/greater/string index result = %v, want 42", got)
	}
}

func TestRepeatNumericForAndLogicalOps(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local sum = 0
for i = 1, 5 do
	sum = sum + ((i < 3 and i) or 10)
end
for j = 3, 1, -1 do
	sum = sum + j
end
local done = 0
repeat
	done = done + 1
	sum = sum + (done and 1 or 0)
until done >= 2
return sum
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 41 {
		t.Fatalf("repeat/for/logical result = %v, want 41", got)
	}
}

func TestMethodCallBreakAndOperators(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local box = {base = 40}
function box:inc(v)
	local sum = 0
	while true do
		sum = self.base + v
		break
	end
	return sum + 1
end
return box:inc(1)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("method/break result = %v, want 42", got)
	}

	result, err = engine.DoString(`
local t = {10, 20, 30}
return #("ve" .. "x") + #t + (9 % 4) + 2 ^ 3
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 15 {
		t.Fatalf("operator result = %v, want 15", got)
	}
}

func TestMetatableIndexNewIndexAndAdd(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local sink = {}
local mt = {
	__index = function(_, key)
		if key == "value" then
			return 40
		end
		return nil
	end,
	__newindex = function(_, key, value)
		sink[key] = value
	end,
	__add = function(lhs, rhs)
		return lhs.value + rhs.value + 2
	end,
}
local a = setmetatable({}, mt)
local b = setmetatable({}, mt)
a.answer = 5
return (getmetatable(a) == mt and 1 or 0) + sink.answer + (a + b)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 88 {
		t.Fatalf("metatable result = %v, want 88", got)
	}
}

func TestVarargAndMultiReturn(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local function trio()
	return 4, 5, 6
end
local function relay()
	return trio()
end
local function collect(...)
	local a, b, c = ...
	return a * 100 + b * 10 + c
end
local x, y, z = relay()
return x * 100 + y * 10 + z + collect(1, 2, 3)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 579 {
		t.Fatalf("vararg/multi-return result = %v, want 579", got)
	}
}

func TestTailMultiReturnInCallArgs(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local function triple()
	return 2, 3, 4
end
local function pack(a, b, c, d)
	return a * 1000 + b * 100 + c * 10 + d
end
local box = {base = 32}
function box:mix(a, b, c)
	return self.base + a + b + c
end
return pack(1, triple()) + box:mix(triple())
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 1275 {
		t.Fatalf("tail multret args result = %v, want 1275", got)
	}
}

func TestTableConstructorTailMultiReturnAndCoroutineWrap(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local function triple()
	return 2, 3, 4
end
local t = {x = 5, 1, triple()}
local main = coroutine.running()
local seen = nil
local wrapped = coroutine.wrap(function(a, b)
	seen = coroutine.running()
	local x, y = coroutine.yield(a + b, a * b)
	return x - y, seen == nil
end)
local y1, y2 = wrapped(3, 4)
local y3, y4 = wrapped(20, 9)
return t.x + t[1] * 10000 + t[2] * 1000 + t[3] * 100 + t[4] * 10
	+ ((main == nil) and 1 or 0)
	+ ((seen ~= nil) and 2 or 0)
	+ y1 + y2 + y3
	+ ((y4 == false) and 4 or 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 12382 {
		t.Fatalf("table constructor/wrap result = %v, want 12382", got)
	}
}

func TestGenericForPairsAndIpairs(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local sum = 0
for _, v in ipairs({10, 20}) do
	sum = sum + v
end
for _, v in pairs({x = 5, y = 7}) do
	sum = sum + v
end
return sum
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("generic for result = %v, want 42", got)
	}
}

func TestEnvironmentAndModule(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local loader = loadstring("module(\"demo.tools\")\nvalue = 20\nfunction twice(v)\n\treturn value + v\nend")
loader()
local env = getfenv(demo.tools.twice)
env.value = 20
local fresh = {value = 21}
setfenv(demo.tools.twice, fresh)
return demo.tools.twice(21) + env.value + ((getfenv(demo.tools.twice) == fresh) and 100 or 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 162 {
		t.Fatalf("environment/module result = %v, want 162", got)
	}
}

func TestPackageSeeAllAndDebugAliases(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
if type(package) ~= "table" then
	return -1
end
if type(package.seeall) ~= "function" then
	return -9
end
if type(debug) ~= "table" or type(debug.getfenv) ~= "function" or type(debug.setfenv) ~= "function" then
	return -2
end
local loader = loadstring("module(\"pkg.mod\", package.seeall)\nfunction forty_two()\n\treturn math.max(40, 42)\nend")
loader()
if type(pkg) ~= "table" or type(pkg.mod) ~= "table" then
	return -3
end
local fn = pkg.mod.forty_two
if type(fn) ~= "function" then
	return -4
end
local env = debug.getfenv(fn)
if type(env) ~= "table" then
	return -5
end
if type(env.math) ~= "table" or type(env.math.max) ~= "function" then
	return -6
end
local fresh = {}
setmetatable(fresh, {__index = env})
debug.setfenv(fn, fresh)
if debug.getfenv(fn) ~= fresh then
	return -7
end
if package.loaded["pkg.mod"] ~= pkg.mod then
	return -8
end
return fn()
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("package/debug alias result = %v, want 42", got)
	}
}

func TestRawAccessSelectAndUnpack(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local sink = {}
local mt = {
	__index = function(_, key)
		if key == "virtual" then
			return 90
		end
		return nil
	end,
	__newindex = function(_, key, value)
		sink[key] = value
	end,
}
local t = setmetatable({x = 1, [2] = 4}, mt)
rawset(t, "y", 5)
t.z = 7
local u1, u2 = unpack({8, 9}, 1, 2)
local s1, s2 = select(2, 10, 20, 30)
local count = select("#", "a", "b", "c")
return rawget(t, "x")
	+ rawget(t, "y")
	+ ((rawget(t, "virtual") == nil) and 1 or 0)
	+ sink.z
	+ (rawequal(t, t) and 1 or 0)
	+ count + s1 + s2 + u1 + u2
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 85 {
		t.Fatalf("raw/select/unpack result = %v, want 85", got)
	}
}

func TestPCallXpcallAndCoroutineMultiReturn(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local co = coroutine.create(function()
	local a, b = coroutine.yield(10, 20)
	return a + b, a * b
end)
local ok1, y1, y2 = coroutine.resume(co)
local ok2, r1, r2 = coroutine.resume(co, 3, 4)
local ok3, err1 = pcall(function()
	error("boom")
end)
local ok4, err2 = xpcall(function()
	error("x")
end, function(message)
	return "handled:" .. message
end)
local ok5, value = pcall(function()
	return "ok"
end)
return (ok1 and 1 or 0)
	+ (ok2 and 1 or 0)
	+ (ok3 and 0 or 1)
	+ (ok4 and 0 or 1)
	+ (ok5 and 1 or 0)
	+ y1 + y2 + r1 + r2
	+ ((err1 == "boom") and 100 or 0)
	+ ((err2 == "handled:x") and 1000 or 0)
	+ ((value == "ok") and 10000 or 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 11154 {
		t.Fatalf("pcall/xpcall/coroutine multret result = %v, want 11154", got)
	}
}

func TestRequireStringAndTableLibraries(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
package.preload["demo.mod"] = function(name)
	return {
		name = name,
		upper = string.upper("ab"),
		joined = table.concat({"x", "y"}, "-"),
	}
end
local first = require("demo.mod")
local second = require("demo.mod")
local b1, b2 = string.byte("AZ", 1, 2)
local bytes = {b1, b2}
table.insert(bytes, 2, 77)
local removed = table.remove(bytes, 3)
return ((first == second) and 1 or 0)
	+ ((first.name == "demo.mod") and 10 or 0)
	+ ((first.upper == "AB") and 100 or 0)
	+ ((first.joined == "x-y") and 1000 or 0)
	+ bytes[1] + bytes[2] + removed
	+ string.len(string.sub("vexlua", 2, 4))
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 1346 {
		t.Fatalf("require/string/table result = %v, want 1346", got)
	}
}

func TestStringPatternAndTableSortLibraries(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local s, e, digits = string.find("abc123xyz", "(%d+)")
local ps, pe = string.find("a.b.c", ".", 1, true)
local word, num = string.match("go-42", "(%a+)%-(%d+)")
local rep = string.rep("ha", 3)
local rev = string.reverse("vex")
local t = {4, 1, 3, 2}
table.sort(t)
local asc = t[1] * 1000 + t[2] * 100 + t[3] * 10 + t[4]
table.sort(t, function(a, b)
	return a > b
end)
local desc = t[1] * 1000 + t[2] * 100 + t[3] * 10 + t[4]
local mx = table.maxn({[1] = 1, [7] = 2, [3.5] = 3})
return s + e + ps + pe
	+ ((digits == "123") and 10 or 0)
	+ ((word == "go") and 20 or 0)
	+ ((num == "42") and 30 or 0)
	+ string.len(rep)
	+ ((rev == "xev") and 40 or 0)
	+ asc + desc + mx
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 5682 {
		t.Fatalf("string pattern/table sort result = %v, want 5682", got)
	}
}

func TestStringGSubGMatchFormatAndLegacyTableLibraries(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local formatted = string.format("%s:%02d:%0.1f:%X:%q", "id", 7, 3.25, 255, "hi")
local r1, c1 = string.gsub("abc123abc", "(%a+)", "<%1>", 2)
local dict = {one = "1", two = "2"}
local r2, c2 = string.gsub("one two three", "(%a+)", dict)
local r3, c3 = string.gsub("ab12cd34", "(%a+)(%d+)", function(a, b)
	return string.upper(a) .. b
end)
local total = 0
for letters, digits in string.gmatch("ab12cd34", "(%a+)(%d+)") do
	total = total + string.len(letters) * 100 + tonumber(digits)
end
local t = {10, 20, 30}
local foreachi = table.foreachi(t, function(i, v)
	if i == 2 then
		return v + 1
	end
end)
local foreach = table.foreach({a = 3, b = 4}, function(k, v)
	if k == "b" then
		return v + 2
	end
end)
return ((formatted == "id:07:3.2:FF:\"hi\"") and 1 or 0)
	+ ((r1 == "<abc>123<abc>") and 10 or 0) + c1
	+ ((r2 == "1 2 three") and 20 or 0) + c2
	+ ((r3 == "AB12CD34") and 30 or 0) + c3
	+ total
	+ table.getn(t)
	+ foreachi
	+ foreach
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 544 {
		t.Fatalf("gsub/gmatch/format/legacy table result = %v, want 544", got)
	}
}

func TestStringDumpAdvancedPatternsAndObsoleteSetN(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local dumped = string.dump(function(v)
	local base = 2
	return v + base
end)
local loaded = assert(loadstring(dumped))
local p1, p2 = string.match("abc", "()b()")
local s, e = string.find("123abc", "%f[%a]abc")
local balanced = string.match("(ab)(ab)", "(%b())%1")
local replaced, count = string.gsub("abc", "()b()", "%1-%2")
local total = 0
for a, b in string.gfind("abc", "()b()") do
	total = total + a + b
end
local ok, err = pcall(function()
	return table.setn({}, 10)
end)
return loaded(40)
	+ p1 + p2
	+ s + e
	+ ((balanced == "(ab)") and 100 or 0)
	+ ((replaced == "a2-3c") and 1000 or 0)
	+ count * 10000
	+ total
	+ (ok and 0 or 100000)
	+ ((string.find(err, "obsolete", 1, true) ~= nil) and 1000000 or 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 1111162 {
		t.Fatalf("string advanced/dump result = %v, want 1111162", got)
	}
}

func TestStringMetatableFormatAndNumericCoercion(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local mt = getmetatable("")
local quoted = string.format("%q", string.char(65, 0, 66, 10, 67, 13, 34, 92))
local expected = string.char(34, 65, 92, 48, 48, 48, 66, 92, 10, 67, 92, 114, 92, 34, 92, 92, 34)
return ((type(mt) == "table") and 1 or 0)
	+ ((mt.__index == string) and 10 or 0)
	+ ((("vexlua"):sub(2, 4) == "exl") and 100 or 0)
	+ ((string.len(10) == 2) and 1000 or 0)
	+ ((string.sub(12345, 2, 4) == "234") and 10000 or 0)
	+ ((quoted == expected) and 100000 or 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 111111 {
		t.Fatalf("string metatable/format result = %v, want 111111", got)
	}
}

func TestLongStringsCommentsAndEscapes(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
--[=[
ignored comment
]=]
local long = [=[alpha
beta]=]
local short = "\097\10\r\t\v\f\b\a\\\"\'"
local expected = string.char(97, 10, 13, 9, 11, 12, 8, 7, 92, 34, 39)
return ((long == "alpha\nbeta") and 1 or 0)
	+ ((short == expected) and 10 or 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 11 {
		t.Fatalf("long string/comment/escape result = %v, want 11", got)
	}
}

func TestThreadUserdataMetatablesGlobalsAndMath(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local co = coroutine.create(function() end)
local proxy = newproxy(true)
local proxy2 = newproxy(proxy)
local meta = getmetatable(proxy)
meta.__index = { answer = 42 }
debug.setmetatable(1, { __index = function(v, key) return v + 41 end })
local n = 1
local ip, fp = math.modf(3.25)
return ((type(co) == "thread") and 1 or 0)
	+ ((type(proxy) == "userdata") and 10 or 0)
	+ ((proxy2.answer == 42) and 100 or 0)
	+ ((n.answer == 42) and 1000 or 0)
	+ (((_G ~= nil) and (_VERSION == "Lua 5.1")) and 10000 or 0)
	+ ((math.sqrt(9) == 3) and 100000 or 0)
	+ (((math.floor(2.9) == 2) and (math.ceil(2.1) == 3)) and 1000000 or 0)
	+ (((ip == 3) and (fp == 0.25)) and 10000000 or 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 11111111 {
		t.Fatalf("thread/userdata/metatable/globals/math result = %v, want 11111111", got)
	}
}

func TestDoBlockStatementAndScope(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local x = 1
do
	local x = 40
end
do
	x = x + 1
end
return x
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 2 {
		t.Fatalf("do-block result = %v, want 2", got)
	}
}

func TestProperTailCall(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local function loop(n, acc)
	if n == 0 then
		return acc
	end
	return loop(n - 1, acc + 1)
end
return loop(200000, 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 200000 {
		t.Fatalf("proper tail call result = %v, want 200000", got)
	}
}

func TestProperTailCallFrameReuseWithNestedCalls(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local loop, step
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
return loop(50000, 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 50000 {
		t.Fatalf("proper tail call frame reuse result = %v, want 50000", got)
	}
}

func TestUserdataEnvAndDebugIntrospection(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local proxy = newproxy(true)
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
local trace = debug.traceback("boom", 1)
local registry = debug.getregistry()
return ((debug.getfenv(proxy) == env) and 1 or 0)
	+ ((info.what == "Lua") and 10 or 0)
	+ ((info.nups == 1) and 100 or 0)
	+ ((name == "captured") and 1000 or 0)
	+ ((value == 41) and 10000 or 0)
	+ ((changed == "captured") and 100000 or 0)
	+ ((updated == 99) and 1000000 or 0)
	+ ((fn() == 99) and 10000000 or 0)
	+ ((type(registry) == "table") and 100000000 or 0)
	+ ((string.find(trace, "stack traceback:", 1, true) ~= nil) and 1000000000 or 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 1111111111 {
		t.Fatalf("userdata/debug result = %v, want 1111111111", got)
	}
}

func TestIOOSLoadfileAndGC(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	tempDir := t.TempDir()
	dataPath := filepath.ToSlash(filepath.Join(tempDir, "data.txt"))
	movedPath := filepath.ToSlash(filepath.Join(tempDir, "data-moved.txt"))
	codePath := filepath.ToSlash(filepath.Join(tempDir, "code.lua"))
	source := `
local dataPath = [[` + dataPath + `]]
local movedPath = [[` + movedPath + `]]
local codePath = [[` + codePath + `]]
local writer = assert(io.open(dataPath, "w"))
assert(io.type(writer) == "file")
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
local count = collectgarbage("count")
local prev = collectgarbage("setpause", 150)
return ((line1 == "alpha") and 1 or 0)
	+ ((line2 == "beta") and 10 or 0)
	+ ((all == "alpha\nbeta\n") and 100 or 0)
	+ ((total == 84) and 1000 or 0)
	+ ((io.type(io.stdout) == "file") and 10000 or 0)
	+ ((utc == "1970-01-02") and 100000 or 0)
	+ (((stampInfo.year == 2001) and (stampInfo.month == 9) and (stampInfo.day == 9) and (stampInfo.hour == 1) and (stampInfo.min == 46) and (stampInfo.sec == 40)) and 1000000 or 0)
	+ ((type(count) == "number" and type(prev) == "number" and type(gcinfo()) == "number") and 10000000 or 0)
`
	result, err := engine.DoString(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 11111111 {
		t.Fatalf("io/os/loadfile/gc result = %v, want 11111111", got)
	}
}

func TestMetatableCallToStringProtectionAndCompareFallback(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local mt = {
	__metatable = "locked",
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
local ok = pcall(function()
	setmetatable(a, {})
end)
return ((getmetatable(a) == "locked") and 1 or 0)
	+ ((tostring(a) == "box:4") and 10 or 0)
	+ a(2, 3)
	+ ((a <= b) and 100 or 0)
	+ ((b <= a) and 0 or 1000)
	+ (ok and 0 or 10000)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 11120 {
		t.Fatalf("metatable edge result = %v, want 11120", got)
	}
}

func TestProtoDumpLoadRoundTrip(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	proto, err := engine.CompileString(`
local seed = 40
local function make()
	local offset = 2
	return function(v)
		return v + seed + offset
	end
end
local fn = make()
return fn(0)
`)
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
	loaded, err := engine.LoadProto(data)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("roundtrip result = %v, want 42", got)
	}
}

func TestProtoDumpLoadRoundTripFallback(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	proto, err := engine.CompileString(`
local t = {5, 7, 9, x = 1}
if t.x == 1 then
	t[2] = t[2] + 30
end
local i = 1
local sum = 0
while i <= 3 do
	sum = sum + t[i]
	i = i + 1
end
return sum
`)
	if err != nil {
		t.Fatal(err)
	}
	data, err := engine.DumpProto(proto)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte{0x1b, 'L', 'u', 'a'}) && !bytes.HasPrefix(data, []byte{'V', 'X', 'L', '5', '1', 0}) {
		t.Fatalf("unexpected chunk header %v", data[:min(len(data), 6)])
	}
	loaded, err := engine.LoadProto(data)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 51 {
		t.Fatalf("fallback roundtrip result = %v, want 51", got)
	}
}

func TestProtoDumpLoadRoundTripExtendedOfficial(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	proto, err := engine.CompileString(`
local sum = 0
for i = 1, 4 do
	sum = sum + ((i < 3 and i) or 10)
end
local j = 2
repeat
	sum = sum + j
	j = j - 1
until j == 0
return sum
`)
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
	loaded, err := engine.LoadProto(data)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 26 {
		t.Fatalf("extended official roundtrip result = %v, want 26", got)
	}
}

func TestProtoDumpLoadRoundTripOperatorsOfficial(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	proto, err := engine.CompileString(`
local out = ""
for i = 1, 4 do
	if i > 2 then
		break
	end
	out = out .. i
end
return out .. (9 % 4) .. (2 ^ 3)
`)
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
	loaded, err := engine.LoadProto(data)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(loaded)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := engine.runtime.ToString(result)
	if !ok || got != "1218" {
		t.Fatalf("operator official roundtrip result = %q, want %q", got, "1218")
	}
}

func TestProtoDumpLoadRoundTripMethodOfficial(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	proto, err := engine.CompileString(`
local box = {base = 40}
function box:inc(v)
	return self.base + v + 1
end
return box:inc(1)
`)
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
	loaded, err := engine.LoadProto(data)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 42 {
		t.Fatalf("method official roundtrip result = %v, want 42", got)
	}
}

func TestWeakTablesAndUserdataFinalizer(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local finalized = 0
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
return ((weakValues[1] == nil) and 1 or 0)
	+ ((next(weakKeys) == nil) and 10 or 0)
	+ ((finalized == 1) and 100 or 0)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 111 {
		t.Fatalf("weak table/finalizer result = %v, want 111", got)
	}
}

func TestCoroutineWrapSurvivesCollectGarbage(t *testing.T) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	result, err := engine.DoString(`
local wrapped = coroutine.wrap(function()
	local next = coroutine.yield(40)
	return next + 2
end)
local first = wrapped()
collectgarbage("collect")
local second = wrapped(40)
return first + second
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Number(); got != 82 {
		t.Fatalf("coroutine.wrap GC result = %v, want 82", got)
	}
}

func BenchmarkInterpreterSumLoop(b *testing.B) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 1024})
	proto := engine.BuildSumLoop(20000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Run(proto); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJITSumLoop(b *testing.B) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 2})
	proto := engine.BuildSumLoop(20000)
	for i := 0; i < 4; i++ {
		if _, err := engine.Run(proto); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Run(proto); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScriptedFunction(b *testing.B) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	proto, err := engine.CompileString(`
function inc(v)
	return v + 1
end
return inc(41)
`)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Run(proto); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScriptedClosure(b *testing.B) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	proto, err := engine.CompileString(`
local seed = 40
local function make()
	local offset = 2
	return function(v)
		return v + seed + offset
	end
end
local fn = make()
return fn(0)
`)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Run(proto); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScriptedNumericFor(b *testing.B) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 16})
	proto, err := engine.CompileString(`
local sum = 0
for i = 1, 100 do
	sum = sum + i
end
return sum
`)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Run(proto); err != nil {
			b.Fatal(err)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

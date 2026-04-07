package vexlua

import (
	"bytes"
	"math"
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

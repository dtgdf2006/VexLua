package main

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"vexlua"
	"vexlua/internal/bytecode"
)

type Box struct {
	Bias float64
}

func (b *Box) Scale(v float64) float64 {
	return v + b.Bias
}

func main() {
	engine := vexlua.New()
	must(engine.RegisterFunc("double", func(v float64) float64 {
		return v * 2
	}))
	must(engine.RegisterObject("box", &Box{Bias: 2.5}))
	must(engine.RegisterTable("fastbox", map[string]any{"Bias": 2.5}))

	functionDemo := engine.BuildFunctionDemo("double", 21)
	fieldDemo := engine.BuildFieldAddDemo("box", "Bias", 7.5)
	tableDemo := engine.BuildFieldAddDemo("fastbox", "Bias", 7.5)
	methodDemo := engine.BuildMethodDemo("box", "Scale", 10)
	loopDemo := engine.BuildSumLoop(1000000)

	printResult("function", engine, functionDemo)
	printResult("field", engine, fieldDemo)
	printResult("field warm", engine, fieldDemo)
	printResult("table field", engine, tableDemo)
	printResult("table field warm", engine, tableDemo)
	printResult("method", engine, methodDemo)
	printSourceResult("script function", engine, `
function inc(v)
  return v + 1
end
return inc(41)
`)
	printSourceResult("script closure", engine, `
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
	printSourceResult("script coroutine", engine, `
local co = coroutine.create(function(v)
  local next = coroutine.yield(v + 1)
  return next + 2
end)
	local ok1, first = coroutine.resume(co, 40)
	local ok2, second = coroutine.resume(co, 40)
	return (ok1 and 1 or 0) + first + (ok2 and 1 or 0) + second
`)
	printSourceResult("script loadstring", engine, `
local fn = loadstring("return 40 + 2")
return fn()
`)
	printSourceResult("script control", engine, `
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
	printSourceResult("script loops", engine, `
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
	printSourceResult("script metatable", engine, `
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
	printSourceResult("script methods", engine, `
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
	printSourceResult("script operators", engine, `
local out = ""
for i = 1, 4 do
	if i > 2 then
		break
	end
	out = out .. i
end
return out .. (9 % 4) .. (2 ^ 3)
`)
	printChunkRoundTrip(engine)

	start := time.Now()
	var sum vexlua.Value
	for i := 0; i < 16; i++ {
		sum = mustValue(engine.Run(loopDemo))
	}
	duration := time.Since(start)
	stats := engine.Stats(loopDemo)
	fmt.Printf("sum loop => %s, runs=%d, quickened=%d, jit=%v, elapsed=%s\n", sum, stats.Runs, stats.QuickenedOps, stats.JITCompiled, duration)
}

func printResult(label string, engine *vexlua.Engine, proto *bytecode.Proto) {
	result := mustValue(engine.Run(proto))
	stats := engine.Stats(proto)
	fmt.Printf("%s => %s, runs=%d, quickened=%d, jit=%v\n", label, engine.FormatValue(result), stats.Runs, stats.QuickenedOps, stats.JITCompiled)
}

func printSourceResult(label string, engine *vexlua.Engine, source string) {
	result := mustValue(engine.DoString(source))
	fmt.Printf("%s => %s\n", label, engine.FormatValue(result))
}

func printChunkRoundTrip(engine *vexlua.Engine) {
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
	must(err)
	data, err := engine.DumpProto(proto)
	must(err)
	loaded, err := engine.LoadProto(data)
	must(err)
	result := mustValue(engine.Run(loaded))
	fmt.Printf("chunk roundtrip => %s, official=%v, size=%d\n", engine.FormatValue(result), bytes.HasPrefix(data, []byte{0x1b, 'L', 'u', 'a'}), len(data))
}

func must(err error) {
	if err != nil {
		if code, ok := vexlua.ExitCode(err); ok {
			os.Exit(code)
		}
		panic(err)
	}
}

func mustValue(v vexlua.Value, err error) vexlua.Value {
	if err != nil {
		if code, ok := vexlua.ExitCode(err); ok {
			os.Exit(code)
		}
		panic(err)
	}
	return v
}

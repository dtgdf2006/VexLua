package benchmarks

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Workload struct {
	Name     string
	Source   string
	Expected string
	Notes    string
	Tags     []string
}

var scriptWorkloads = []Workload{
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
		Tags:     []string{"core", "numeric", "vexarc"},
	},
	{
		Name: "table_array_sum",
		Source: `
local t = {}
for i = 1, 1000 do
	t[i] = i
end
local sum = 0
for round = 1, 200 do
	for i = 1, #t do
		sum = sum + t[i]
	end
end
return sum
`,
		Expected: "100100000",
		Notes:    "数组形 table 访问与顺序遍历",
		Tags:     []string{"core", "numeric", "table", "vexarc"},
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
		Tags:     []string{"core", "table", "vexarc"},
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
		Tags:     []string{"core", "call", "table", "vexarc"},
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
		Tags:     []string{"core", "closure", "call"},
	},
	{
		Name: "closure_upvalue_mutation",
		Source: `
local function make()
	local x = 0
	return function(v)
		x = x + v
		return x
	end
end
local fn = make()
local sum = 0
for i = 1, 5000 do
	sum = sum + fn(1)
end
return sum
`,
		Expected: "12502500",
		Notes:    "闭包、upvalue 读写与状态累积",
		Tags:     []string{"extended", "closure", "call", "vexarc"},
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
		Tags:     []string{"core", "iterator", "table", "stdlib"},
	},
	{
		Name: "vararg_multret_chain",
		Source: `
local function source(v)
	return v, v + 1, v + 2, v + 3
end

local function relay(...)
	return ...
end

local function pack(a, b, c, d)
	return a + b + c + d
end

local sum = 0
for i = 1, 5000 do
	sum = sum + pack(relay(source(i)))
end
return sum
`,
		Expected: "50040000",
		Notes:    "vararg 与多返回传播链路",
		Tags:     []string{"extended", "call", "vararg"},
	},
	{
		Name: "tailcall_chain",
		Source: `
local bounce

local function step(n, acc)
	if n == 0 then
		return acc
	end
	return bounce(n - 1, acc + 1)
end

bounce = function(n, acc)
	return step(n, acc)
end

local sum = 0
for i = 1, 2000 do
	sum = sum + bounce(50, 0)
end
return sum
`,
		Expected: "100000",
		Notes:    "proper tailcall 链与帧复用",
		Tags:     []string{"extended", "call", "tailcall"},
	},
	{
		Name: "metatable_dispatch",
		Source: `
local sink = {}
local mt = {
	__index = function(t, k)
		if k == "x" then
			return rawget(t, "base") + 1
		end
		return nil
	end,
	__newindex = function(_, k, v)
		sink[k] = v
	end,
	__add = function(a, b)
		return a.x + b.x
	end,
}

local proxy = setmetatable({base = 3}, mt)
local other = setmetatable({base = 4}, mt)
local sum = 0
for i = 1, 5000 do
	proxy.y = i
	sum = sum + proxy.x + (proxy + other) + sink.y
end
return sum
`,
		Expected: "12567500",
		Notes:    "__index/__newindex/__add 元方法分发",
		Tags:     []string{"extended", "metatable", "table", "call", "vexarc"},
	},
	{
		Name: "string_find_match",
		Source: `
local input = "user=alpha42 code=beta99"
local sum = 0
for i = 1, 5000 do
	local s1, e1, key1, value1, digits1 = string.find(input, "(%a+)=(%a+)(%d+)")
	local value2, digits2 = string.match(input, "code=(%a+)(%d+)")
	sum = sum + s1 + e1 + #key1 + #value1 + tonumber(digits1) + #value2 + tonumber(digits2)
end
return sum
`,
		Expected: "835000",
		Notes:    "string.find/string.match 捕获与返回值",
		Tags:     []string{"extended", "string", "stdlib", "vexarc"},
	},
	{
		Name: "string_gsub",
		Source: `
local input = "alpha=123 beta=456 gamma=789"
local total = 0
for i = 1, 5000 do
	local replaced, count = string.gsub(input, "(%a+)=(%d+)", "%2:%1")
	total = total + #replaced + count
end
return total
`,
		Expected: "155000",
		Notes:    "string.gsub 模式匹配与替换",
		Tags:     []string{"extended", "string", "stdlib", "vexarc"},
	},
	{
		Name: "coroutine_resume",
		Source: `
local sum = 0
for round = 1, 1000 do
	local co = coroutine.create(function()
		for i = 1, 5 do
			coroutine.yield(i)
		end
		return 0
	end)
	local ok, value = coroutine.resume(co)
	while coroutine.status(co) ~= "dead" do
		if not ok then
			error(value)
		end
		sum = sum + value
		ok, value = coroutine.resume(co)
	end
	if not ok then
		error(value)
	end
	sum = sum + value
end
return sum
`,
		Expected: "15000",
		Notes:    "coroutine.create/resume/yield 切换开销",
		Tags:     []string{"extended", "coroutine", "iterator", "stdlib", "vexarc"},
	},
	{
		Name: "coroutine_steady_state",
		Source: `
local co = coroutine.create(function()
	local value = 0
	while true do
		value = value + 1
		coroutine.yield(value)
	end
end)

local sum = 0
for i = 1, 5000 do
	local ok, value = coroutine.resume(co)
	if not ok then
		error(value)
	end
	sum = sum + value
end
return sum
`,
		Expected: "12502500",
		Notes:    "steady-state resume/yield 热路径",
		Tags:     []string{"extended", "coroutine", "call", "vexarc"},
	},
	{
		Name: "table_sort",
		Source: `
local sum = 0
for i = 1, 2000 do
	local t = {9, 1, 8, 2, 7, 3, 6, 4, 5}
	table.sort(t)
	sum = sum + t[1] * 10 + t[#t]
end
return sum
`,
		Expected: "38000",
		Notes:    "table.sort 与短数组排序热点",
		Tags:     []string{"extended", "table", "stdlib", "vexarc"},
	},
}

func ScriptWorkloads() []Workload {
	out := make([]Workload, len(scriptWorkloads))
	for i, work := range scriptWorkloads {
		out[i] = work
		out[i].Tags = append([]string(nil), work.Tags...)
	}
	return out
}

func AllTags() []string {
	seen := make(map[string]struct{}, len(scriptWorkloads))
	for _, work := range scriptWorkloads {
		for _, tag := range work.Tags {
			seen[tag] = struct{}{}
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func SelectWorkloads(spec string) ([]Workload, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" || strings.EqualFold(trimmed, "all") {
		return ScriptWorkloads(), nil
	}

	byName := make(map[string]Workload, len(scriptWorkloads))
	byTag := make(map[string][]string)
	for _, work := range scriptWorkloads {
		byName[work.Name] = work
		for _, tag := range work.Tags {
			byTag[tag] = append(byTag[tag], work.Name)
		}
	}

	selected := make([]Workload, 0, len(scriptWorkloads))
	seen := make(map[string]struct{}, len(scriptWorkloads))
	for _, raw := range strings.Split(trimmed, ",") {
		token := strings.ToLower(strings.TrimSpace(raw))
		if token == "" {
			continue
		}
		if token == "all" {
			for _, work := range scriptWorkloads {
				if _, ok := seen[work.Name]; ok {
					continue
				}
				selected = append(selected, work)
				seen[work.Name] = struct{}{}
			}
			continue
		}
		if work, ok := byName[token]; ok {
			if _, ok := seen[work.Name]; !ok {
				selected = append(selected, work)
				seen[work.Name] = struct{}{}
			}
			continue
		}
		if names, ok := byTag[token]; ok {
			for _, name := range names {
				if _, ok := seen[name]; ok {
					continue
				}
				selected = append(selected, byName[name])
				seen[name] = struct{}{}
			}
			continue
		}
		return nil, fmt.Errorf("unknown workload or tag %q", token)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no workloads selected for %q", spec)
	}
	return selected, nil
}

func MatchesExpected(actual string, expected string) bool {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
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

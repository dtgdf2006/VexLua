package main

import (
	"fmt"
	"sort"
	"strings"
)

type benchmarkCase struct {
	Name          string
	Description   string
	MinIterations int
	Source        string
}

var benchmarkCases = []benchmarkCase{
	{
		Name:          "arith_for",
		Description:   "numeric for plus arithmetic",
		MinIterations: 50000,
		Source: `local scale = 3

return function(n)
    local sum = 0
    for i = 1, n do
        sum = sum + i * scale - 1
    end
    return sum
end
`,
	},
	{
		Name:          "branch_mod",
		Description:   "branching with modulo",
		MinIterations: 50000,
		Source: `return function(n)
    local sum = 0
    for i = 1, n do
        if i % 2 == 0 then
            sum = sum + i
        else
            sum = sum - 1
        end
    end
    return sum
end
`,
	},
	{
		Name:          "array_get",
		Description:   "table array reads",
		MinIterations: 50000,
		Source: `local values = {3, 1, 4, 1, 5, 9, 2, 6}

return function(n)
    local sum = 0
    for i = 1, n do
        local index = (i % 8) + 1
        sum = sum + values[index]
    end
    return sum
end
`,
	},
	{
		Name:          "array_set",
		Description:   "table array writes",
		MinIterations: 25000,
		Source: `local values = {0, 0, 0, 0, 0, 0, 0, 0}

return function(n)
    for i = 1, 8 do
        values[i] = 0
    end
    for i = 1, n do
        local index = (i % 8) + 1
        values[index] = values[index] + i
    end
    local sum = 0
    for i = 1, 8 do
        sum = sum + values[i]
    end
    return sum
end
`,
	},
	{
		Name:          "string_len",
		Description:   "string length",
		MinIterations: 100000,
		Source: `local text = "benchmark"

return function(n)
	    local sum = 0
	    for _ = 1, n do
	        sum = sum + #text
    end
	    return sum
end
`,
	},
	{
		Name:          "field_set",
		Description:   "table field writes",
		MinIterations: 40000,
		Source: `local object = {x = 0}

return function(n)
    object.x = 0
    for i = 1, n do
        object.x = object.x + (i % 7)
    end
    return object.x
end
`,
	},
	{
		Name:          "generic_for",
		Description:   "generic for iterator",
		MinIterations: 20000,
		Source: `local function iter(limit, current)
    if current < limit then
        local nextValue = current + 1
        return nextValue, nextValue
    end
end

return function(n)
    local sum = 0
    for _, value in iter, n, 0 do
        sum = sum + value
    end
    return sum
end
`,
	},
	{
		Name:          "concat_len",
		Description:   "string concat and length",
		MinIterations: 2000,
		Source: `local piece = "ab"

return function(n)
    local text = ""
    for _ = 1, n do
        text = text .. piece
    end
    return #text
end
`,
	},
}

func selectBenchmarkCases(filter string) ([]benchmarkCase, error) {
	if strings.TrimSpace(filter) == "" {
		selected := make([]benchmarkCase, len(benchmarkCases))
		copy(selected, benchmarkCases)
		return selected, nil
	}

	index := make(map[string]benchmarkCase, len(benchmarkCases))
	for _, benchCase := range benchmarkCases {
		index[benchCase.Name] = benchCase
	}

	seen := make(map[string]struct{})
	selected := make([]benchmarkCase, 0, len(benchmarkCases))
	for _, rawName := range strings.Split(filter, ",") {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		benchCase, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("unknown benchmark case %q", name)
		}
		seen[name] = struct{}{}
		selected = append(selected, benchCase)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no benchmark cases selected")
	}
	return selected, nil
}

func benchmarkCaseNames() []string {
	names := make([]string, 0, len(benchmarkCases))
	for _, benchCase := range benchmarkCases {
		names = append(names, benchCase.Name)
	}
	sort.Strings(names)
	return names
}

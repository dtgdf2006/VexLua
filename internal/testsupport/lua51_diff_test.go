package testsupport_test

import (
	"runtime"
	"testing"
)

func TestLua51DiffArithmeticAndControl(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-arith.lua", `
local a = 1
local b = 2
local c = 3
return a + b * c, not false, not nil
`)
}

func TestLua51DiffTableMethodAndSetList(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-table-self.lua", `
local t = {11, 22, 33}
function t:id()
  return 99
end
return t[2], t:id()
`)
}

func TestLua51DiffClosureVarargAndTailcall(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-closure-vararg-tail.lua", `
local function make_adder(a)
  return function(b)
    return a + b
  end
end

local function sum(a, b)
  return a + b
end

local function tail(a, b)
  return sum(a, b)
end

local add40 = make_adder(40)
local function keep(...)
	local a, b, c = ...
	return a + b + c
end

return add40(2), tail(10, 32), keep(1, 2, 3)
`)
}

func TestLua51DiffConcatLenAndNumericFor(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-len-concat-for.lua", `
local s = "he" .. "llo"
local total = 0
for i = 1, 3 do
  total = total + i
end
local t = {}
t[s] = 42
return #s, t[s], total
`)
}

func TestLua51DiffConcatMetamethodAndRightFold(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-concat-meta.lua", `
local mt = {
	__concat = function(lhs, rhs)
		return lhs.label .. rhs.label
	end,
}

local b = setmetatable({ label = "B" }, mt)
local c = setmetatable({ label = "C" }, mt)

return "A" .. b .. c
`)
}

func TestLua51DiffConcatBatchesStringNumberAndMetamethod(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-concat-batch-meta.lua", `
local mt = {
	__concat = function(lhs, rhs)
		local function label(v)
			if type(v) == "table" then
				return v.label
			end
			return v .. ""
		end
		return label(lhs) .. label(rhs)
	end,
}

local box = setmetatable({ label = "B" }, mt)

return "A" .. 10 .. box .. "Z"
`)
}

func TestLua51DiffConcatTypeError(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceBothError(t, "@diff-concat-error.lua", `
return "x" .. true
`)
}

func TestLua51DiffNumberCoercionAndFormatting(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-number-coercion-format.lua", `
local a = " 3.5 "
local b = "0x10"
local c = "1e2"
return a + 2, b + 1, c + 1, 100000000000000 .. "", 0.00001 .. ""
`)
}

func TestLua51DiffArithmeticStringCoercion(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-arith-string-coercion.lua", `
return "0x10" + 2, "6" / "3", "3" * "2", -"0x10"
`)
}

func TestLua51DiffNumberFormattingSpecialValues(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific Lua 5.1 CRT spelling")
	}
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-number-format-special.lua", `
return 1/0 .. "", -1/0 .. "", 0/0 .. ""
`)
}

func TestLua51DiffArithmeticMetamethodsAndForPrepCoercion(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-arith-meta-forprep.lua", `
local add_box = setmetatable({ value = 40 }, {
	__add = function(lhs, rhs)
		return lhs.value + rhs
	end,
})

local neg_box = setmetatable({ value = 7 }, {
	__unm = function(v)
		return v.value + 1
	end,
})

local total = 0
for i = "1", "3", "1" do
	total = total + i
end

return add_box + 2, -neg_box, -("3"), -5 % 3, 9 ^ 0.5, total
`)
}

func TestLua51DiffArithmeticRightOperandMetamethodAndDescendingFor(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-arith-right-meta-desc-for.lua", `
local rhs = setmetatable({ value = 41 }, {
	__add = function(lhs, target)
		return lhs + target.value
	end,
})

local total = 0
for i = "3", "1", "-1" do
	total = total * 10 + i
end

return 1 + rhs, " 0x10 " + 1, total
`)
}

func TestLua51DiffPhase3CrossFamilyComposition(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-phase3-cross-family.lua", `
local mt = {
	__concat = function(lhs, rhs)
		local function label(v)
			if type(v) == "table" then
				return v.label
			end
			return v .. ""
		end
		return label(lhs) .. label(rhs)
	end,
}

local box = setmetatable({ label = "B" }, mt)

local total = 0
for i = "3", "1", "-1" do
	total = total * 10 + i
end

local text = "A" .. box .. total
local sparse = {1, 2}
sparse[4] = 4

return text, #text, text < "AB400", #sparse, total + "0x10"
`)
}

func TestLua51DiffPhase3CrossFamilyErrorComposition(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceBothError(t, "@diff-phase3-cross-error-concat-compare.lua", `
local mt = {
	__concat = function(lhs, rhs)
		local function label(v)
			if type(v) == "table" then
				return v.label
			end
			return v .. ""
		end
		return label(lhs) .. label(rhs)
	end,
}

local box = setmetatable({ label = "B" }, mt)

return ("A" .. box .. "C") < {}
`)
	harness.assertSourceBothError(t, "@diff-phase3-cross-error-arith-compare.lua", `
local rhs = setmetatable({ value = 41 }, {
	__add = function(lhs, target)
		return { value = lhs + target.value }
	end,
})

return (1 + rhs) < 2
`)
	harness.assertSourceBothError(t, "@diff-phase3-cross-error-len-forprep.lua", `
for i = 1, #true, 1 do
end
`)
	harness.assertSourceBothError(t, "@diff-phase3-cross-error-concat-forprep.lua", `
for i = 1, 3, "1" .. true do
end
`)
}

func TestLua51DiffArithmeticAndForPrepErrors(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceBothError(t, "@diff-arith-error.lua", `
return 1 + true
`)
	harness.assertSourceBothError(t, "@diff-forprep-initial-error.lua", `
for i = "oops", 3, 1 do
end
`)
	harness.assertSourceBothError(t, "@diff-forprep-limit-error.lua", `
for i = 1, "oops", 1 do
end
`)
	harness.assertSourceBothError(t, "@diff-forprep-step-error.lua", `
for i = 1, 3, "oops" do
end
`)
}

func TestLua51DiffCompareMetamethodsAndLEFallback(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-compare-meta.lua", `
local eq_calls = 0
local lt_calls = 0

local shared_eq = function(lhs, rhs)
	eq_calls = eq_calls + 1
	return lhs.id == rhs.id
end

local shared_lt = function(lhs, rhs)
	lt_calls = lt_calls + 1
	return lhs.rank < rhs.rank
end

local function new_box(id, rank)
	return setmetatable({ id = id, rank = rank }, {
		__eq = shared_eq,
		__lt = shared_lt,
	})
end

local a = new_box(1, 1)
local b = new_box(1, 2)

return a == b, a < b, a <= b, b <= a, eq_calls, lt_calls
`)
}

func TestLua51DiffCompareStringOrderingAndEqIdentity(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-compare-string-nul-eq-identity.lua", `
local eq_left = setmetatable({}, {
	__eq = function()
		return true
	end,
})

local eq_right = setmetatable({}, {
	__eq = function()
		return true
	end,
})

local a = "ab\000c"
local b = "ab\000d"

return a < b, a <= "ab\000c", eq_left == eq_right
`)
}

func TestLua51DiffCompareMismatchedOrderMetamethodIdentityErrors(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceBothError(t, "@diff-compare-order-identity-error.lua", `
local a = setmetatable({ rank = 1 }, {
	__lt = function(lhs, rhs)
		return lhs.rank < rhs.rank
	end,
})

local b = setmetatable({ rank = 2 }, {
	__lt = function(lhs, rhs)
		return lhs.rank < rhs.rank
	end,
})

return a < b
`)
}

func TestLua51DiffCompareOrderErrors(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceBothError(t, "@diff-compare-error-same-type.lua", `
local a = {}
local b = {}
return a < b
`)
	harness.assertSourceBothError(t, "@diff-compare-error-mixed-type.lua", `
return {} < 1
`)
}

func TestLua51DiffLenMetamethodsAndBoundaries(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-len-meta.lua", `
local calls = 0

local t = {1, 2}
t[4] = 4
setmetatable(t, {
	__len = function()
		calls = calls + 100
		return 99
	end,
})

local s = "hello"
debug.setmetatable(s, {
	__len = function()
		calls = calls + 1000
		return 999
	end,
})

debug.setmetatable(0, {
	__len = function(v)
		calls = calls + 1
		return v + 35
	end,
})

return #t, #s, #7, calls
`)
}

func TestLua51DiffLenZeroAndHashBoundary(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-len-hash-boundary.lua", `
local hash_only = {}
hash_only[3] = 3

local mixed = {1, 2}
mixed[4] = 4

return #hash_only, #mixed
`)
}

func TestLua51DiffLenErrors(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceBothError(t, "@diff-len-error.lua", `
return #true
`)
}

func TestLua51DiffBuiltinMetatableHelpers(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-metatable-builtins.lua", `
local t = {}
local mt = { marker = 7 }
setmetatable(t, mt)
rawset(t, "x", 42)
return getmetatable(t) == mt, rawget(t, "x"), mt.marker, type(t), type(mt)
`)
}

func TestLua51DiffIndexMetamethodFunction(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-index-meta-function.lua", `
local t = setmetatable({}, {
	__index = function(_, key)
		if key == "answer" then
			return 42
		end
	end,
})
return t.answer
`)
}

func TestLua51DiffIndexMetamethodTableAndSelf(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-index-meta-table-self.lua", `
local methods = { answer = 42 }
function methods:id()
	return self.answer + 57
end

local t = setmetatable({}, { __index = methods })
return t.answer, t:id()
`)
}

func TestLua51DiffNewIndexMetamethodFunction(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-newindex-meta-function.lua", `
local sink = {}
local t = setmetatable({}, {
	__newindex = function(_, key, value)
		rawset(sink, key, value + 1)
	end,
})
t.answer = 41
return sink.answer
`)
}

func TestLua51DiffNewIndexMetamethodTable(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-newindex-meta-table.lua", `
local sink = {}
local t = setmetatable({}, { __newindex = sink })
t.answer = 42
return sink.answer, rawget(t, "answer"), t.answer
`)
}

func TestLua51DiffGlobalMetamethods(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-global-meta.lua", `
local mt = {
	__index = function(_, key)
		if key == "missing" then
			return 42
		end
	end,
	__newindex = function(target, key, value)
		rawset(target, key, value + 1)
	end,
}

setmetatable(_G, mt)
created = 9
return missing, created
`)
}

func TestLua51DiffNonTableIndexMetamethods(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-non-table-index-meta.lua", `
local string_mt = { __index = { answer = 42 } }
local function_mt = { __index = { answer = 99 } }
local s = "hello"
local f = function() end

debug.setmetatable(s, string_mt)
debug.setmetatable(f, function_mt)

return s.answer, f.answer, getmetatable(s) == string_mt, getmetatable(f) == function_mt
`)
}

func TestLua51DiffNonTableNewIndexMetamethods(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-non-table-newindex-meta.lua", `
local sink = {}
debug.setmetatable(0, { __newindex = sink })

local n = 7
n.answer = 42

return sink.answer, rawget(sink, "answer")
`)
}

func TestLua51DiffTMCallCallTailcallAndTForLoop(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-tmcall-call-family.lua", `
local callable = setmetatable({}, {
	__call = function(_, a, b)
		return a + b
	end,
})

local function tail(a, b)
	return callable(a, b)
end

local iterator = setmetatable({}, {
	__call = function(_, state, control)
		if control >= state then
			return nil, nil
		end
		local next = control + 1
		return next, next + 10
	end,
})

local total = 0
for control, value in iterator, 2, 0 do
	total = total + value
end

return callable(10, 32), tail(20, 22), total
`)
}

func TestLua51DiffTMCallSupportsNonTableTypeMetatable(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-tmcall-non-table.lua", `
local mt = {
	__call = function(self, x)
		return self + x
	end,
}

debug.setmetatable(0, mt)

local n = 7
return n(35), getmetatable(n) == mt
`)
}

func TestLua51DiffTMCallMixedDirectAndCallableAtSameSite(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceMatches(t, "@diff-tmcall-mixed-site.lua", `
local callable = setmetatable({ bias = 40 }, {
	__call = function(self, x)
		return self.bias + x
	end,
})

local function direct(x)
	return x * 2
end

local total = 0
for i = 1, 4 do
	local f = direct
	if i % 2 == 0 then
		f = callable
	end
	total = total + f(i)
end

return total
`)
}

func TestLua51DiffTMCallCapturesErrors(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceBothError(t, "@diff-tmcall-error.lua", `
local callable = setmetatable({}, { __call = 42 })
return callable(1)
`)
}

func TestLua51DiffCapturesRuntimeErrors(t *testing.T) {
	harness := newLua51DiffHarness(t)
	harness.assertSourceBothError(t, "@diff-runtime-error.lua", `
local x = 1
return x[1]
`)
}

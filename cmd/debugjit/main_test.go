package main

import (
	"runtime"
	"strings"
	"testing"

	"vexlua"
)

func TestSelectCasesBuiltInGroups(t *testing.T) {
	cases := allJITCases()

	supported, err := selectCases(cases, "supported")
	if err != nil {
		t.Fatal(err)
	}
	if len(supported) == 0 {
		t.Fatal("expected supported selection to return workloads")
	}
	for _, testCase := range supported {
		if testCase.expect != expectJIT {
			t.Fatalf("supported selection included %s with expect=%s", testCase.name, testCase.expect)
		}
	}

	fallback, err := selectCases(cases, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback) == 0 {
		t.Fatal("expected fallback selection to return workloads")
	}
	for _, testCase := range fallback {
		if testCase.expect != expectFallback {
			t.Fatalf("fallback selection included %s with expect=%s", testCase.name, testCase.expect)
		}
	}
}

func TestSelectCasesPreservesOrderAndDeduplicates(t *testing.T) {
	selected, err := selectCases(allJITCases(), "method_call,sum_loop,method_call")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinCaseNames(selected), "method_call,sum_loop"; got != want {
		t.Fatalf("selected cases = %q, want %q", got, want)
	}
}

func TestSelectCasesRejectsUnknownNames(t *testing.T) {
	_, err := selectCases(allJITCases(), "missing_case")
	if err == nil {
		t.Fatal("expected unknown workload to fail")
	}
	if !strings.Contains(err.Error(), "missing_case") {
		t.Fatalf("unknown workload error = %q, want name to be mentioned", err)
	}
}

func TestParseRequireMode(t *testing.T) {
	auto, err := parseRequireMode("auto")
	if err != nil {
		t.Fatal(err)
	}
	if want := runtime.GOOS == "windows" && runtime.GOARCH == "amd64"; auto != want {
		t.Fatalf("auto require-jit = %v, want %v", auto, want)
	}

	on, err := parseRequireMode("on")
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Fatal("require-jit on should return true")
	}

	off, err := parseRequireMode("off")
	if err != nil {
		t.Fatal(err)
	}
	if off {
		t.Fatal("require-jit off should return false")
	}

	if _, err := parseRequireMode("maybe"); err == nil {
		t.Fatal("expected unsupported require-jit mode to fail")
	}
}

func TestAnalyzeJITSupportReportsExpectedModes(t *testing.T) {
	engine := vexlua.NewWithOptions(vexlua.Options{EnableJIT: false, HotThreshold: 16})

	supportedReport, err := analyzeJITSupport(engine.BuildSumLoop(32))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" && !supportedReport.supported {
		t.Fatalf("expected sum_loop to be compiler-supported on windows amd64, got %s", supportedReport.format())
	}
	if supportedReport.supported {
		if got := supportedReport.format(); got != "supported" {
			t.Fatalf("supported format = %q, want supported", got)
		}
	}

	unsupportedProto, err := engine.CompileStringNamed(`
local seed = 40
local function make()
	local offset = 2
	return function(v)
		return v + seed + offset
	end
end
local fn = make()
return fn(0)
`, "@debugjit_unsupported.lua")
	if err != nil {
		t.Fatal(err)
	}
	unsupportedReport, err := analyzeJITSupport(unsupportedProto)
	if err != nil {
		t.Fatal(err)
	}
	if unsupportedReport.supported {
		t.Fatalf("expected closure proto to be unsupported, got %s", unsupportedReport.format())
	}
	if !containsReason(unsupportedReport.reasons, "CLOSURE") {
		t.Fatalf("unsupported reasons = %v, want CLOSURE", unsupportedReport.reasons)
	}
	if got := unsupportedReport.format(); !strings.Contains(got, "unsupported(") || !strings.Contains(got, "CLOSURE") {
		t.Fatalf("unsupported format = %q, want CLOSURE details", got)
	}
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

package vexlua_test

import (
	"testing"

	"vexlua/internal/diff51"
)

func TestLua51Parity(t *testing.T) {
	luaBin, version, err := diff51.DetectLua("")
	if err != nil {
		t.Skipf("skipping Lua 5.1 parity test: %v", err)
	}
	t.Logf("Lua baseline: %s (%s)", luaBin, version)

	for _, testCase := range diff51.DefaultCases() {
		if testCase.Expectation != diff51.ExpectMatch {
			continue
		}
		t.Run(testCase.Name, func(t *testing.T) {
			result, err := diff51.RunCase(luaBin, testCase)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Match {
				t.Fatalf("lua51 mismatch\nnotes: %s\nlua: %s\nvexlua: %s", testCase.Notes, diff51.FormatOutput(result.LuaOutput), diff51.FormatOutput(result.VexOutput))
			}
		})
	}
}

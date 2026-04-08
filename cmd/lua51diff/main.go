package main

import (
	"flag"
	"fmt"
	"os"

	"vexlua/internal/diff51"
)

func main() {
	var luaBin string
	flag.StringVar(&luaBin, "lua-bin", "", "Lua 5.1 executable; defaults to VEXLUA_LUA51_BIN or auto-detect from PATH")
	flag.Parse()

	resolvedLua, version, err := diff51.DetectLua(luaBin)
	if err != nil {
		fatalf("failed to locate Lua 5.1: %v", err)
	}

	passCount := 0
	knownGapCount := 0
	unexpectedMismatchCount := 0
	gapClosedCount := 0

	fmt.Printf("Lua baseline: %s (%s)\n", resolvedLua, version)
	fmt.Printf("Official script source: testdata/lua-5.1.5/test/\n\n")

	for _, testCase := range diff51.DefaultCases() {
		result, err := diff51.RunCase(resolvedLua, testCase)
		if err != nil {
			unexpectedMismatchCount++
			fmt.Printf("[ERROR] %s (%s)\n", testCase.Name, testCase.Notes)
			fmt.Printf("        %v\n\n", err)
			continue
		}

		switch testCase.Expectation {
		case diff51.ExpectMatch:
			if result.Match {
				passCount++
				fmt.Printf("[PASS] %s (%s)\n", testCase.Name, testCase.Notes)
			} else {
				unexpectedMismatchCount++
				fmt.Printf("[FAIL] %s (%s)\n", testCase.Name, testCase.Notes)
				fmt.Printf("       Lua:    %s\n", diff51.FormatOutput(result.LuaOutput))
				fmt.Printf("       VexLua: %s\n", diff51.FormatOutput(result.VexOutput))
			}
		case diff51.ExpectKnownMismatch:
			if result.Match {
				gapClosedCount++
				fmt.Printf("[CLOSED] %s (%s)\n", testCase.Name, testCase.Notes)
				fmt.Printf("         Output: %s\n", diff51.FormatOutput(result.VexOutput))
			} else {
				knownGapCount++
				fmt.Printf("[GAP] %s (%s)\n", testCase.Name, testCase.Notes)
				fmt.Printf("      Lua:    %s\n", diff51.FormatOutput(result.LuaOutput))
				fmt.Printf("      VexLua: %s\n", diff51.FormatOutput(result.VexOutput))
			}
		}
	}

	fmt.Printf("\nSummary: pass=%d unexpected-mismatch=%d known-gap=%d gap-closed=%d\n", passCount, unexpectedMismatchCount, knownGapCount, gapClosedCount)
	if unexpectedMismatchCount > 0 {
		os.Exit(1)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

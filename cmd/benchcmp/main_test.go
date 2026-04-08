package main

import "testing"

func TestGeometricMean(t *testing.T) {
	if got := geometricMean([]float64{2, 8}); got < 3.99 || got > 4.01 {
		t.Fatalf("geometricMean([2 8]) = %v, want 4", got)
	}
	if got := geometricMean(nil); got != 0 {
		t.Fatalf("geometricMean(nil) = %v, want 0", got)
	}
	if got := geometricMean([]float64{0, -1}); got != 0 {
		t.Fatalf("geometricMean of non-positive values = %v, want 0", got)
	}
}

func TestJITActiveCount(t *testing.T) {
	summaries := []summary{
		{Run: runBenchResult{VexJITCompiled: true}},
		{Run: runBenchResult{VexJITCompiled: false}},
		{Run: runBenchResult{VexJITCompiled: true}},
	}
	if got := jitActiveCount(summaries); got != 2 {
		t.Fatalf("jitActiveCount = %d, want 2", got)
	}
}

func TestFormatBool(t *testing.T) {
	if got := formatBool(true); got != "yes" {
		t.Fatalf("formatBool(true) = %q, want yes", got)
	}
	if got := formatBool(false); got != "no" {
		t.Fatalf("formatBool(false) = %q, want no", got)
	}
}

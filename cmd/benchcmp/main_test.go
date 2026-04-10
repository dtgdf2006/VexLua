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

func TestQuickenedWorkloadCount(t *testing.T) {
	summaries := []summary{
		{Run: runBenchResult{VexQuickenedOps: 2}},
		{Run: runBenchResult{VexQuickenedOps: 0}},
		{Run: runBenchResult{VexQuickenedOps: 5}},
	}
	if got := quickenedWorkloadCount(summaries); got != 2 {
		t.Fatalf("quickenedWorkloadCount = %d, want 2", got)
	}
}

func TestVexarcActiveCount(t *testing.T) {
	summaries := []summary{
		{Run: runBenchResult{VexarcActive: true}},
		{Run: runBenchResult{VexarcActive: false}},
		{Run: runBenchResult{VexarcActive: true}},
	}
	if got := vexarcActiveCount(summaries); got != 2 {
		t.Fatalf("vexarcActiveCount = %d, want 2", got)
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

func TestFormatMetricAndSpeedup(t *testing.T) {
	if got := formatMetric(false, 12.5); got != "-" {
		t.Fatalf("formatMetric(inactive) = %q, want -", got)
	}
	if got := formatMetric(true, 12.5); got != "12.5" {
		t.Fatalf("formatMetric(active) = %q, want 12.5", got)
	}
	if got := formatSpeedup(false, 10, 2); got != "-" {
		t.Fatalf("formatSpeedup(inactive) = %q, want -", got)
	}
	if got := formatSpeedup(true, 10, 2); got != "5.00x" {
		t.Fatalf("formatSpeedup(active) = %q, want 5.00x", got)
	}
}

package stdlib

import (
	"strings"
	"testing"
)

func TestCompileStringPatternCachesByPatternAndMode(t *testing.T) {
	plain1, err := compileStringPattern("a.b", true)
	if err != nil {
		t.Fatal(err)
	}
	plain2, err := compileStringPattern("a.b", true)
	if err != nil {
		t.Fatal(err)
	}
	if plain1 != plain2 {
		t.Fatal("expected plain pattern compilation to be cached")
	}

	pattern1, err := compileStringPattern("a.b", false)
	if err != nil {
		t.Fatal(err)
	}
	pattern2, err := compileStringPattern("a.b", false)
	if err != nil {
		t.Fatal(err)
	}
	if pattern1 != pattern2 {
		t.Fatal("expected non-plain pattern compilation to be cached")
	}
	if plain1 == pattern1 {
		t.Fatal("expected cache key to distinguish plain and pattern modes")
	}
}

func TestCompileStringGSubReplacementPlanCachesAndExpands(t *testing.T) {
	plan1, err := compileStringGSubReplacementPlan("%2:%1:%%:%0")
	if err != nil {
		t.Fatal(err)
	}
	plan2, err := compileStringGSubReplacementPlan("%2:%1:%%:%0")
	if err != nil {
		t.Fatal(err)
	}
	if plan1 != plan2 {
		t.Fatal("expected gsub replacement plan to be cached")
	}

	var builder strings.Builder
	plan1.appendTo(&builder, "alpha=123", []stringPatternCapture{{text: "alpha"}, {text: "123"}})
	if got := builder.String(); got != "123:alpha:%:alpha=123" {
		t.Fatalf("replacement expansion = %q, want %q", got, "123:alpha:%:alpha=123")
	}
}

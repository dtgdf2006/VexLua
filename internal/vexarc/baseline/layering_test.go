package baseline

import (
	"os"
	"strings"
	"testing"
)

func TestTableLoweringStaysOnFastPathDispatchLayer(t *testing.T) {
	source, err := os.ReadFile("table_lowering.go")
	if err != nil {
		t.Fatalf("read table_lowering.go: %v", err)
	}
	text := string(source)
	forbidden := []string{
		"WriteFeedbackCell",
		"ReadFeedbackCell",
		"UpdateTableFeedback",
		"DescribeFastGet",
		"DescribeFastSet",
		"Tables.Get(",
		"Tables.Set(",
		"hostObject",
		"compiledStatusDeopt",
	}
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Fatalf("table lowering should remain guard/dispatch glue only, found forbidden dependency %q", needle)
		}
	}
}

func TestRuntimeStubsUseSharedHostBoundary(t *testing.T) {
	source, err := os.ReadFile("runtime_stubs.go")
	if err != nil {
		t.Fatalf("read runtime_stubs.go: %v", err)
	}
	text := string(source)
	forbidden := []string{
		"Engine.Call(",
		"hostObjectGet",
		"hostObjectSet",
		"callHostFunction",
	}
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Fatalf("runtime stubs should not route host continuation through %q", needle)
		}
	}
	required := []string{
		"ReadIndexBoundary",
		"WriteIndexBoundary",
		"CallValueBoundary",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("runtime stubs should depend on shared host boundary %q", needle)
		}
	}
}

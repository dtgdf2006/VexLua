package benchmarks

import (
	"strings"
	"testing"
)

func TestSelectWorkloadsTags(t *testing.T) {
	selected, err := SelectWorkloads("core")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinWorkloadNames(selected), "numeric_for_sum,table_array_sum,table_field_sum,method_dispatch,closure_upvalue,generic_for_pairs"; got != want {
		t.Fatalf("core workloads = %q, want %q", got, want)
	}

	selected, err = SelectWorkloads("extended")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinWorkloadNames(selected), "closure_upvalue_mutation,vararg_multret_chain,tailcall_chain,metatable_dispatch,string_find_match,string_gsub,coroutine_resume,coroutine_steady_state,table_sort"; got != want {
		t.Fatalf("extended workloads = %q, want %q", got, want)
	}
}

func TestSelectWorkloadsPreservesOrderAndDeduplicates(t *testing.T) {
	selected, err := SelectWorkloads("table_sort,string,table_sort,coroutine")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinWorkloadNames(selected), "table_sort,string_find_match,string_gsub,coroutine_resume,coroutine_steady_state"; got != want {
		t.Fatalf("selected workloads = %q, want %q", got, want)
	}
}

func TestSelectWorkloadsNewSemanticTags(t *testing.T) {
	selected, err := SelectWorkloads("vararg,tailcall,metatable")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinWorkloadNames(selected), "vararg_multret_chain,tailcall_chain,metatable_dispatch"; got != want {
		t.Fatalf("semantic workloads = %q, want %q", got, want)
	}
}

func TestSelectWorkloadsVexarcTag(t *testing.T) {
	selected, err := SelectWorkloads("vexarc")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinWorkloadNames(selected), "numeric_for_sum,table_array_sum,table_field_sum,method_dispatch,closure_upvalue_mutation,metatable_dispatch,string_find_match,string_gsub,coroutine_resume,coroutine_steady_state,table_sort"; got != want {
		t.Fatalf("vexarc workloads = %q, want %q", got, want)
	}
}

func TestSelectWorkloadsRejectsUnknownToken(t *testing.T) {
	_, err := SelectWorkloads("missing_case")
	if err == nil {
		t.Fatal("expected unknown workload selection to fail")
	}
	if !strings.Contains(err.Error(), "missing_case") {
		t.Fatalf("unknown selection error = %q, want token to be mentioned", err)
	}
}

func TestMatchesExpected(t *testing.T) {
	testCases := []struct {
		name     string
		actual   string
		expected string
		want     bool
	}{
		{name: "exact", actual: "42", expected: "42", want: true},
		{name: "numeric equivalent", actual: "42.0", expected: "42", want: true},
		{name: "string mismatch", actual: "hello", expected: "world", want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := MatchesExpected(testCase.actual, testCase.expected); got != testCase.want {
				t.Fatalf("MatchesExpected(%q, %q) = %v, want %v", testCase.actual, testCase.expected, got, testCase.want)
			}
		})
	}
}

func joinWorkloadNames(workloads []Workload) string {
	names := make([]string, 0, len(workloads))
	for _, work := range workloads {
		names = append(names, work.Name)
	}
	return strings.Join(names, ",")
}

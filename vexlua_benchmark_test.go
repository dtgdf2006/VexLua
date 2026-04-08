package vexlua

import (
	"testing"

	benchmarks "vexlua/internal/benchmarks"
)

func BenchmarkSystematic(b *testing.B) {
	b.Run("run_interp", func(b *testing.B) {
		for _, work := range benchmarks.ScriptWorkloads() {
			work := work
			b.Run(work.Name, func(b *testing.B) {
				benchmarkCompiledWorkload(b, work, false)
			})
		}
	})

	b.Run("run_jit", func(b *testing.B) {
		for _, work := range benchmarks.ScriptWorkloads() {
			work := work
			b.Run(work.Name, func(b *testing.B) {
				benchmarkCompiledWorkload(b, work, true)
			})
		}
	})

	b.Run("do_string_cached", func(b *testing.B) {
		for _, work := range benchmarks.ScriptWorkloads() {
			work := work
			b.Run(work.Name, func(b *testing.B) {
				benchmarkCachedSourceWorkload(b, work)
			})
		}
	})

	b.Run("host_bridge", func(b *testing.B) {
		b.Run("function_call", benchmarkHostFunctionCall)
		b.Run("object_method", benchmarkHostObjectMethod)
	})
}

func benchmarkCompiledWorkload(b *testing.B, work benchmarks.Workload, enableJIT bool) {
	hotThreshold := uint32(1024)
	if enableJIT {
		hotThreshold = 2
	}
	engine := NewWithOptions(Options{EnableJIT: enableJIT, HotThreshold: hotThreshold})
	proto, err := engine.CompileStringNamed(work.Source, "@bench_"+work.Name+".lua")
	if err != nil {
		b.Fatal(err)
	}
	result, err := engine.Run(proto)
	if err != nil {
		b.Fatal(err)
	}
	if got := engine.FormatValue(result); !benchmarks.MatchesExpected(got, work.Expected) {
		b.Fatalf("benchmark %s initial result = %q, want %q", work.Name, got, work.Expected)
	}
	if enableJIT {
		for i := 0; i < 6; i++ {
			if _, err := engine.Run(proto); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err = engine.Run(proto)
		if err != nil {
			b.Fatal(err)
		}
	}
	if got := engine.FormatValue(result); !benchmarks.MatchesExpected(got, work.Expected) {
		b.Fatalf("benchmark %s final result = %q, want %q", work.Name, got, work.Expected)
	}
}

func benchmarkCachedSourceWorkload(b *testing.B, work benchmarks.Workload) {
	engine := NewWithOptions(Options{EnableJIT: true, HotThreshold: 2})
	result, err := engine.DoString(work.Source)
	if err != nil {
		b.Fatal(err)
	}
	if got := engine.FormatValue(result); !benchmarks.MatchesExpected(got, work.Expected) {
		b.Fatalf("cached source %s initial result = %q, want %q", work.Name, got, work.Expected)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err = engine.DoString(work.Source)
		if err != nil {
			b.Fatal(err)
		}
	}
	if got := engine.FormatValue(result); !benchmarks.MatchesExpected(got, work.Expected) {
		b.Fatalf("cached source %s final result = %q, want %q", work.Name, got, work.Expected)
	}
}

func benchmarkHostFunctionCall(b *testing.B) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 1024})
	if err := engine.RegisterFunc("double", func(v float64) float64 { return v * 2 }); err != nil {
		b.Fatal(err)
	}
	proto, err := engine.CompileStringNamed(`
local sum = 0
for i = 1, 5000 do
	sum = sum + double(i)
end
return sum
`, "@bench_host_function.lua")
	if err != nil {
		b.Fatal(err)
	}
	result, err := engine.Run(proto)
	if err != nil {
		b.Fatal(err)
	}
	if got := engine.FormatValue(result); got != "25005000" {
		b.Fatalf("host function initial result = %q, want 25005000", got)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err = engine.Run(proto)
		if err != nil {
			b.Fatal(err)
		}
	}
	if got := engine.FormatValue(result); got != "25005000" {
		b.Fatalf("host function final result = %q, want 25005000", got)
	}
}

func benchmarkHostObjectMethod(b *testing.B) {
	engine := NewWithOptions(Options{EnableJIT: false, HotThreshold: 1024})
	if err := engine.RegisterObject("box", &benchBox{Bias: 2.5}); err != nil {
		b.Fatal(err)
	}
	proto, err := engine.CompileStringNamed(`
local sum = 0
for i = 1, 1000 do
	sum = sum + box.Scale(i)
end
return sum
`, "@bench_host_method.lua")
	if err != nil {
		b.Fatal(err)
	}
	result, err := engine.Run(proto)
	if err != nil {
		b.Fatal(err)
	}
	if got := engine.FormatValue(result); got != "503000" {
		b.Fatalf("host method initial result = %q, want 503000", got)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err = engine.Run(proto)
		if err != nil {
			b.Fatal(err)
		}
	}
	if got := engine.FormatValue(result); got != "503000" {
		b.Fatalf("host method final result = %q, want 503000", got)
	}
}

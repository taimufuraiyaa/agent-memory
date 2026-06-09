# Performance Benchmarking Guide

## Overview

agent-memory includes comprehensive Go benchmarks for performance testing and regression detection. This guide covers how to run benchmarks, interpret results, and contribute new benchmarks.

## Quick Start

```bash
# Run all benchmarks
make bench

# Run benchmarks with memory profiling
make bench-mem

# Run benchmarks for a specific package
go test -bench=. -benchmem ./internal/engine

# Run a specific benchmark
go test -bench=BenchmarkWritePipeline -benchmem ./internal/engine
```

## Benchmark Coverage

### Engine Benchmarks (`internal/engine`)

- **BenchmarkWritePipeline**: Full write pipeline including embedding
- **BenchmarkWritePipelineParallel**: Concurrent write operations
- **BenchmarkRetrievalSearch**: Semantic search retrieval
- **BenchmarkRetrievalRecall**: Recall mode retrieval
- **BenchmarkVectorSearcher**: Direct vector search
- **BenchmarkTokenClipper**: Token budget clipping
- **BenchmarkRebalanceRecallHits**: Hit rebalancing
- **BenchmarkAssembleRecallSections**: Context assembly
- **BenchmarkDecayEngineUpdateWorkspaceDecay**: Decay score updates

### Storage Benchmarks (`internal/storage/sqlite`)

- **BenchmarkUpsertMemory**: Memory upsert operations
- **BenchmarkUpsertMemoryWithEmbedding**: Upsert with vector embedding
- **BenchmarkListMemoryVectors**: Vector listing
- **BenchmarkListRecentMemories**: Recent memory retrieval (various limits)
- **BenchmarkListMemoriesByWorkspace**: Workspace memory listing
- **BenchmarkGetMemory**: Single memory retrieval
- **BenchmarkSetPinned**: Pin/unpin operations
- **BenchmarkMarkAccessed**: Access tracking
- **BenchmarkDeleteByIDs**: Bulk deletion (various sizes)
- **BenchmarkAddTokenMetricV2**: Token metric recording
- **BenchmarkConcurrentReads**: Concurrent read operations
- **BenchmarkConcurrentWrites**: Concurrent write operations

### Validation Benchmarks (`internal/validation`)

- **BenchmarkValidateWorkspaceName**: Workspace name validation
- **BenchmarkValidateFilePath**: File path validation with security checks
- **BenchmarkValidateContentLength**: Content size validation (various sizes)
- **BenchmarkValidateDiagramCode**: Diagram code validation
- **BenchmarkSanitizeWorkspaceName**: Name sanitization
- **BenchmarkValidationPipeline**: Full validation pipeline
- **BenchmarkConcurrentValidation**: Concurrent validation operations
- **BenchmarkPathTraversalDetection**: Path traversal attack detection

### Embeddings Benchmarks (`internal/embeddings`)

- **BenchmarkLocalProviderEmbedBatch100**: Batch embedding generation

### Decay Benchmarks (`internal/engine`)

- **BenchmarkDecayEngineUpdateWorkspaceDecay**: Decay score computation

### Dashboard Benchmarks (`internal/api/dashboard`)

- **BenchmarkGetEmbeddedHandler**: Embedded asset serving

## Running Benchmarks

### Basic Benchmark Run

```bash
# Run all benchmarks
go test -bench=. ./...

# Run benchmarks with memory allocation stats
go test -bench=. -benchmem ./...

# Run benchmarks for longer (more accurate)
go test -bench=. -benchtime=1s ./...
```

### Specific Package Benchmarks

```bash
# Engine benchmarks only
go test -bench=. ./internal/engine

# Storage benchmarks only
go test -bench=. ./internal/storage/sqlite

# Validation benchmarks only
go test -bench=. ./internal/validation
```

### Filtering Benchmarks

```bash
# Run only Write benchmarks
go test -bench=Write ./internal/engine

# Run only benchmarks matching pattern
go test -bench=BenchmarkRetrieval.* ./internal/engine
```

### Memory Profiling

```bash
# Generate memory profile
go test -bench=. -memprofile=mem.prof ./internal/engine

# View memory profile
go tool pprof mem.prof

# Top memory consumers
go tool pprof -top mem.prof

# Interactive analysis
go tool pprof -http=:8080 mem.prof
```

### CPU Profiling

```bash
# Generate CPU profile
go test -bench=. -cpuprofile=cpu.prof ./internal/engine

# View CPU profile
go tool pprof cpu.prof

# Flame graph (requires graphviz)
go tool pprof -http=:8080 cpu.prof
```

## Interpreting Results

### Benchmark Output Format

```
BenchmarkWritePipeline-11    56455    1794 ns/op    3417 B/op    16 allocs/op
```

- **BenchmarkWritePipeline-11**: Benchmark name with GOMAXPROCS suffix
- **56455**: Number of iterations run
- **1794 ns/op**: Nanoseconds per operation
- **3417 B/op**: Bytes allocated per operation
- **16 allocs/op**: Number of allocations per operation

### Performance Guidelines

**Write Pipeline:**
- Target: < 5000 ns/op
- Memory: < 5000 B/op
- Allocations: < 50 allocs/op

**Retrieval (Search):**
- Target: < 100ms per search (with 100 memories)
- Memory: < 1MB per search
- Allocations: < 10,000 allocs/op

**Storage Operations:**
- Upsert: < 100µs per operation
- Vector upsert: < 500µs per operation
- List recent: < 10ms for 100 items

**Validation:**
- Workspace name: < 1µs per validation
- File path: < 100ns per validation
- Content length: < 1µs per validation

## Comparing Benchmarks

### Using benchstat

Install benchstat:
```bash
go install golang.org/x/perf/cmd/benchstat@latest
```

Run benchmarks before and after changes:
```bash
# Before changes
go test -bench=. -count=5 ./internal/engine > old.txt

# Make your changes...

# After changes
go test -bench=. -count=5 ./internal/engine > new.txt

# Compare
benchstat old.txt new.txt
```

Example output:
```
name                    old time/op    new time/op    delta
WritePipeline-11          1.79µs ± 2%    1.45µs ± 1%  -19.00%
RetrievalSearch-11         123ms ± 3%     108ms ± 2%  -12.20%
```

### Regression Detection

Monitor these metrics for regressions:
- **Time/op increase > 10%**: Investigate performance issue
- **Memory/op increase > 20%**: Investigate memory leak
- **Allocs/op increase > 20%**: Investigate allocation patterns

## Writing New Benchmarks

### Benchmark Template

```go
func BenchmarkMyFeature(b *testing.B) {
    // Setup (not timed)
    ctx := context.Background()
    store := setupBenchStore(b)
    defer store.Close()
    
    // Reset timer before measurement
    b.ResetTimer()
    
    // Benchmark loop
    for i := 0; i < b.N; i++ {
        // Code to benchmark
        result, err := myFeature(ctx, store, input)
        if err != nil {
            b.Fatal(err)
        }
        
        // Prevent compiler optimization
        _ = result
    }
}
```

### Parallel Benchmarks

```go
func BenchmarkMyFeatureParallel(b *testing.B) {
    ctx := context.Background()
    store := setupBenchStore(b)
    defer store.Close()
    
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            _, err := myFeature(ctx, store, i)
            if err != nil {
                b.Fatal(err)
            }
            i++
        }
    })
}
```

### Sub-Benchmarks

```go
func BenchmarkWithVariations(b *testing.B) {
    sizes := []int{10, 100, 1000}
    
    for _, size := range sizes {
        b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
            // Setup with size
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                // Benchmark with size
            }
        })
    }
}
```

### Best Practices

1. **Reset Timer**: Always call `b.ResetTimer()` after setup
2. **Use Temp Dirs**: Use `b.TempDir()` for temporary databases
3. **Defer Cleanup**: Use `defer` for resource cleanup
4. **Prevent Optimization**: Assign results to `_` to prevent compiler optimization
5. **Error Handling**: Use `b.Fatal()` for errors, not `b.Error()`
6. **Realistic Data**: Use realistic data sizes and patterns
7. **Multiple Runs**: Run with `-count=5` for statistical significance

## Continuous Integration

### Running Benchmarks in CI

```yaml
# .github/workflows/benchmarks.yml
name: Benchmarks

on:
  pull_request:
    branches: [main]

jobs:
  benchmark:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.23'
      
      - name: Run benchmarks
        run: make bench
      
      - name: Check for regressions
        run: |
          # Compare with baseline
          # Fail if performance degrades > 10%
```

## Python Benchmarks

agent-memory also includes Python-based system benchmarks in the `benchmark/` directory. See `benchmark/README.md` for details on running end-to-end benchmarks.

## Troubleshooting

### Benchmark Times Out

- Reduce `-benchtime` (default is 1s)
- Run specific benchmarks instead of all
- Use shorter test data sets

### Inconsistent Results

- Run with `-count=5` or more
- Use `benchstat` to analyze variance
- Check for background processes
- Pin CPU frequency (on Linux)

### High Memory Usage

- Check for leaks with `-memprofile`
- Verify cleanup in `defer` statements
- Use `b.ReportAllocs()` for detailed allocation tracking

## Resources

- [Go Benchmarking Guide](https://pkg.go.dev/testing#hdr-Benchmarks)
- [benchstat Documentation](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat)
- [pprof Tutorial](https://jvns.ca/blog/2017/09/24/profiling-go-with-pprof/)
- [Python Benchmarks](../benchmark/README.md)

## Contributing

When adding new features:

1. Add benchmarks for performance-critical code
2. Run benchmarks before and after changes
3. Include benchmark results in PR description
4. Document any expected performance changes

**Benchmark coverage goal: 80% of performance-critical paths**

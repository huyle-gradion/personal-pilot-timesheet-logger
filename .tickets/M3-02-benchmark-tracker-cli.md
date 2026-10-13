# [M3-02] Empirical Benchmark Tracker CLI & JSONL Transcript Parser

## 🎯 Vertical Slice
Implement the benchmarking CLI in `cmd/benchmark/main.go` and parser in `internal/benchmark/parser.go`. Parses Claude session JSONL transcripts to extract input/output/cache tokens, measures Go execution duration and peak RSS memory, appends daily records to `benchmarks/metrics.csv`, and generates trend charts.

## 🔗 Blocking Edges
- **Blocked by**: None
- **Blocks**: `[M4-01]`, `[M4-02]`

## 📖 Context & Specification
- **Spec Reference**: Section 3.6 (Track Your Tokens & Time) & Note-1 Complexity Benchmarking.
- **ADR Reference**: ADR 0001 (Section 7: Benchmarking & Empirical Tracking).
- **Required Metrics**:
  - `date`: `YYYY-MM-DD`
  - `total_wall_clock_ms`: Integer execution time
  - `peak_rss_mb`: Peak resident set size of Go processes
  - `llm_input_tokens`: Raw prompt tokens
  - `llm_output_tokens`: Completion tokens
  - `llm_cache_read_tokens`: Prompt cache read tokens
  - `llm_total_tokens`: Total tokens consumed by AI step
  - `entry_count`: Number of timesheet blocks logged

## 📐 Pre-Agreed Seams & Interface Contracts
- **CLI Commands**:
  ```bash
  go run ./cmd/benchmark -record -transcript=~/.gemini/antigravity-cli/brain/.../transcript.jsonl
  go run ./cmd/benchmark -chart
  go run ./cmd/benchmark -report
  ```
- **CSV Format (`benchmarks/metrics.csv`)**:
  ```csv
  date,wall_clock_ms,peak_rss_mb,input_tokens,output_tokens,cache_tokens,total_tokens,entries
  2026-10-07,1420,18.4,285,110,0,395,4
  ```

## ✅ Acceptance Criteria (DoD)
- [ ] Parses Claude Code / Antigravity JSONL transcripts without memory bloat.
- [ ] Accurately measures process execution time and RSS memory on macOS and Linux.
- [ ] Appends cleanly to `benchmarks/metrics.csv` (creates header if file does not exist).
- [ ] `-chart` outputs a terminal ASCII bar chart showing daily token consumption against the 600-token ceiling.
- [ ] Includes test fixtures with sample JSONL transcripts.

## 🧪 Verification Commands
```bash
go test -v ./internal/benchmark/...
go run ./cmd/benchmark -report
```

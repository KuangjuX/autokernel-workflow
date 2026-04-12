package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeKernelNameForMatch(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"rms_norm", "rms_norm"},
		{"rms_norm_forward", "rms_norm"},
		{"cuda_rms_norm", "rms_norm"},
		{"triton_rms_norm_backward", "rms_norm"},
		{"cuda_unpermute_v2", "unpermute_v2"},
		{"select_topk_grad", "select_topk_grad"},
		{"rms_norm_fwd", "rms_norm"},
		{"CUDA_RMS_NORM", "rms_norm"},
	}
	for _, tt := range tests {
		got := normalizeKernelNameForMatch(tt.input)
		if got != tt.want {
			t.Errorf("normalizeKernelNameForMatch(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFindRelatedRuns(t *testing.T) {
	runs := []RunRecord{
		{
			RunID: "run-rms-001",
			Iterations: []IterationRecord{
				{Kernel: "rms_norm_forward", Backend: "triton"},
			},
		},
		{
			RunID: "run-rms-002",
			Iterations: []IterationRecord{
				{Kernel: "rms_norm", Backend: "triton"},
			},
		},
		{
			RunID: "run-gemm-001",
			Iterations: []IterationRecord{
				{Kernel: "gemm_bf16", Backend: "cuda"},
			},
		},
	}

	// Match rms_norm (should find both rms_norm runs)
	got := findRelatedRuns(runs, "rms_norm", "")
	if len(got) != 2 {
		t.Errorf("expected 2 runs for rms_norm, got %d", len(got))
	}

	// Match rms_norm with triton backend
	got = findRelatedRuns(runs, "cuda_rms_norm", "triton")
	if len(got) != 2 {
		t.Errorf("expected 2 runs for cuda_rms_norm+triton, got %d", len(got))
	}

	// Match gemm
	got = findRelatedRuns(runs, "gemm_bf16", "")
	if len(got) != 1 {
		t.Errorf("expected 1 run for gemm_bf16, got %d", len(got))
	}

	// No match
	got = findRelatedRuns(runs, "nonexistent", "")
	if len(got) != 0 {
		t.Errorf("expected 0 runs for nonexistent, got %d", len(got))
	}
}

func TestBuildHistorySummary(t *testing.T) {
	runs := []RunRecord{
		{
			RunID: "run-test-001",
			Iterations: []IterationRecord{
				{
					Iteration:         0,
					CommitHash:        "aaa",
					Subject:           "[baseline] init",
					Kernel:            "rms_norm",
					Correctness:       "PASS",
					SpeedupVsBaseline: 1.0,
					HasSpeedup:        true,
					LatencyUs:         100.0,
					HasLatency:        true,
					Changes:           "Baseline setup",
					Analysis:          "Initial measurement",
				},
				{
					Iteration:         1,
					CommitHash:        "bbb",
					Subject:           "exp 1: increase num_warps",
					Kernel:            "rms_norm",
					Correctness:       "PASS",
					SpeedupVsBaseline: 1.5,
					HasSpeedup:        true,
					LatencyUs:         66.7,
					HasLatency:        true,
					Changes:           "num_warps 4 -> 8",
					Analysis:          "Better memory throughput with more warps",
				},
				{
					Iteration:         2,
					CommitHash:        "ccc",
					Subject:           "exp 2: try eviction policy",
					Kernel:            "rms_norm",
					Correctness:       "PASS",
					SpeedupVsBaseline: 1.48,
					HasSpeedup:        true,
					LatencyUs:         67.5,
					HasLatency:        true,
					Changes:           "Added evict_first on loads",
					Analysis:          "No improvement, eviction policy not helpful here",
				},
			},
		},
	}

	doc := buildHistorySummary("rms_norm", runs)

	if !strings.Contains(doc, "# Prior Optimization History: rms_norm") {
		t.Error("missing title")
	}
	if !strings.Contains(doc, "**Prior runs**: 1") {
		t.Error("missing run count")
	}
	if !strings.Contains(doc, "**Best speedup achieved**: 1.50x") {
		t.Error("missing best speedup")
	}
	if !strings.Contains(doc, "Strategies That Worked") {
		t.Error("missing success section")
	}
	if !strings.Contains(doc, "Strategies That Did NOT Work") {
		t.Error("missing failure section")
	}
	if !strings.Contains(doc, "num_warps") {
		t.Error("should contain num_warps in successful strategies")
	}
	if !strings.Contains(doc, "Run Timeline") {
		t.Error("missing timeline section")
	}
}

func TestBuildHistorySummary_Empty(t *testing.T) {
	doc := buildHistorySummary("test", nil)
	if !strings.Contains(doc, "# Prior Optimization History: test") {
		t.Error("empty runs should still produce header")
	}
}

func TestGenerateContext_MissingParams(t *testing.T) {
	err := GenerateContext(GenerateContextOptions{KernelName: "test"})
	if err == nil {
		t.Error("expected error for missing db-path")
	}

	err = GenerateContext(GenerateContextOptions{DBPath: "/tmp/x.db"})
	if err == nil {
		t.Error("expected error for missing kernel-name")
	}
}

func TestGenerateContext_WriteFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	run := RunRecord{
		RunID:       "run-test",
		Branch:      "agent/test",
		RepoPath:    "/tmp",
		SyncedAt:    "2025-01-01T00:00:00Z",
		CommitCount: 1,
		Iterations: []IterationRecord{
			{
				Iteration:         0,
				CommitHash:        "aaa",
				CommitTime:        "2025-01-01T00:00:00Z",
				Subject:           "init",
				Hypothesis:        "init",
				Kernel:            "rms_norm",
				Correctness:       "PASS",
				SpeedupVsBaseline: 1.5,
				HasSpeedup:        true,
				Changes:           "num_warps 4 -> 8",
				Analysis:          "more warps",
			},
		},
	}
	appendRun(dbPath, run)

	outPath := filepath.Join(dir, "context", "history_summary.md")
	err := GenerateContext(GenerateContextOptions{
		DBPath:     dbPath,
		KernelName: "rms_norm",
		OutputPath: outPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "rms_norm") {
		t.Error("output file should contain kernel name")
	}
}

func TestOneLine(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello world", 50, "hello world"},
		{"line1\nline2\nline3", 50, "line1 line2 line3"},
		{"a very long string that exceeds the limit", 20, "a very long strin..."},
		{"has | pipe", 50, "has / pipe"},
	}
	for _, tt := range tests {
		got := oneLine(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("oneLine(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestCountIterations(t *testing.T) {
	runs := []RunRecord{
		{Iterations: []IterationRecord{{}, {}, {}}},
		{Iterations: []IterationRecord{{}, {}}},
	}
	if n := countIterations(runs); n != 5 {
		t.Errorf("countIterations = %d, want 5", n)
	}
}

func TestDeriveTakeaways(t *testing.T) {
	successes := []iterationInsight{
		{Changes: "num_warps 4 -> 8", Analysis: "better occupancy"},
		{Changes: "num_warps 8 -> 16", Analysis: "more warps helped"},
		{Changes: "block_size 64 -> 128", Analysis: "larger tiles"},
	}
	var regressions []iterationInsight

	takeaways := deriveTakeaways(successes, regressions)
	if len(takeaways) == 0 {
		t.Error("should derive at least one takeaway")
	}
	foundWarps := false
	for _, t := range takeaways {
		if strings.Contains(t, "num_warps") {
			foundWarps = true
		}
	}
	if !foundWarps {
		t.Error("should identify num_warps as a pattern")
	}
}

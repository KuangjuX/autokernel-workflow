package commands

import (
	"path/filepath"
	"strings"
	"testing"
)

func seedDB(t *testing.T, dir string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "test.db")
	runs := []RunRecord{
		{
			RunID:       "run-001",
			Branch:      "agent/run-001",
			RepoPath:    "/tmp/fake",
			SyncedAt:    "2025-06-01T00:00:00Z",
			CommitCount: 2,
			Iterations: []IterationRecord{
				{
					Iteration:         0,
					CommitHash:        "aaa111222333",
					CommitTime:        "2025-06-01T00:00:00Z",
					Subject:           "[baseline] init",
					Hypothesis:        "init",
					Kernel:            "rms_norm",
					Agent:             "cursor",
					GPU:               "B200",
					Backend:           "triton",
					Correctness:       "PASS",
					SpeedupVsBaseline: 1.0,
					HasSpeedup:        true,
					LatencyUs:         100,
					HasLatency:        true,
				},
				{
					Iteration:         1,
					CommitHash:        "bbb222333444",
					ParentCommitHash:  "aaa111222333",
					CommitTime:        "2025-06-01T00:05:00Z",
					Subject:           "exp 1: bigger tiles",
					Hypothesis:        "bigger tiles",
					Kernel:            "rms_norm",
					Agent:             "cursor",
					GPU:               "B200",
					Backend:           "triton",
					Correctness:       "PASS",
					SpeedupVsBaseline: 1.8,
					HasSpeedup:        true,
					LatencyUs:         55.6,
					HasLatency:        true,
					Changes:           "BLOCK_SIZE 64 -> 128",
					Analysis:          "more occupancy",
				},
			},
		},
		{
			RunID:       "run-002",
			Branch:      "agent/run-002",
			RepoPath:    "/tmp/fake2",
			SyncedAt:    "2025-06-02T00:00:00Z",
			CommitCount: 1,
			Iterations: []IterationRecord{
				{
					Iteration:         0,
					CommitHash:        "ccc333",
					CommitTime:        "2025-06-02T00:00:00Z",
					Subject:           "[baseline] init",
					Hypothesis:        "init",
					Kernel:            "softmax",
					Agent:             "other-agent",
					GPU:               "H800",
					Correctness:       "FAIL",
					SpeedupVsBaseline: 0.5,
					HasSpeedup:        true,
				},
			},
		},
	}
	for _, r := range runs {
		if err := appendRun(dbPath, r); err != nil {
			t.Fatalf("seedDB appendRun: %v", err)
		}
	}
	return dbPath
}

func TestBuildStats(t *testing.T) {
	runs := []RunRecord{
		{
			Iterations: []IterationRecord{
				{Kernel: "rms_norm", Agent: "cursor", GPU: "B200", SpeedupVsBaseline: 1.5},
				{Kernel: "rms_norm", Agent: "cursor", GPU: "B200", SpeedupVsBaseline: 2.0},
			},
		},
		{
			Iterations: []IterationRecord{
				{Kernel: "softmax", Agent: "other", GPU: "H800", SpeedupVsBaseline: 3.0},
			},
		},
	}
	stats := buildStats(runs)

	if stats["run_count"] != 2 {
		t.Errorf("run_count = %v, want 2", stats["run_count"])
	}
	if stats["iteration_count"] != 3 {
		t.Errorf("iteration_count = %v, want 3", stats["iteration_count"])
	}
	if stats["unique_kernels"] != 2 {
		t.Errorf("unique_kernels = %v, want 2", stats["unique_kernels"])
	}
	if stats["unique_agents"] != 2 {
		t.Errorf("unique_agents = %v, want 2", stats["unique_agents"])
	}
	if stats["unique_gpus"] != 2 {
		t.Errorf("unique_gpus = %v, want 2", stats["unique_gpus"])
	}
	if stats["best_speedup"] != 3.0 {
		t.Errorf("best_speedup = %v, want 3.0", stats["best_speedup"])
	}
}

func TestBuildStats_Empty(t *testing.T) {
	stats := buildStats(nil)
	if stats["run_count"] != 0 {
		t.Errorf("run_count = %v, want 0", stats["run_count"])
	}
	if stats["best_speedup"] != 0.0 {
		t.Errorf("best_speedup = %v, want 0.0", stats["best_speedup"])
	}
}

func TestBuildSnapshot(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedDB(t, dir)

	snap, err := buildSnapshot(dbPath, false)
	if err != nil {
		t.Fatal(err)
	}

	if snap.Meta["format_version"] != "v1" {
		t.Errorf("format_version = %v", snap.Meta["format_version"])
	}
	if len(snap.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(snap.Runs))
	}
	if snap.Runs[0].RunID != "run-001" {
		t.Errorf("first run = %q", snap.Runs[0].RunID)
	}

	runCount, ok := snap.Stats["run_count"].(int)
	if !ok || runCount != 2 {
		t.Errorf("stats.run_count = %v", snap.Stats["run_count"])
	}
}

func TestClampPatch(t *testing.T) {
	short := "diff --git a/x.py b/x.py\n+hello\n"
	if got := clampPatch(short); got != strings.TrimSpace(short) {
		t.Errorf("short patch should be unchanged")
	}

	long := strings.Repeat("x", maxEmbeddedPatchChars+100)
	clamped := clampPatch(long)
	if len(clamped) >= len(long) {
		t.Error("long patch should be truncated")
	}
	if !strings.Contains(clamped, "truncated") {
		t.Error("truncated patch should contain truncation notice")
	}
}

func TestCloneRuns(t *testing.T) {
	runs := []RunRecord{
		{
			RunID:      "r1",
			Iterations: []IterationRecord{{Iteration: 0, Subject: "init"}},
		},
	}
	cloned := cloneRuns(runs)
	cloned[0].RunID = "modified"
	cloned[0].Iterations[0].Subject = "modified"

	if runs[0].RunID == "modified" {
		t.Error("clone should not modify original RunID")
	}
	if runs[0].Iterations[0].Subject == "modified" {
		t.Error("clone should not modify original iterations")
	}
}

func TestRenderStaticHTML(t *testing.T) {
	snap := Snapshot{
		Meta:  map[string]any{"format_version": "v1"},
		Stats: map[string]any{"run_count": 1},
		Runs: []RunRecord{
			{RunID: "test-run", CommitCount: 1},
		},
	}
	html := renderStaticHTML(snap)
	if !strings.Contains(html, "<!doctype html>") {
		t.Error("should produce valid HTML")
	}
	if !strings.Contains(html, "KernelHub") {
		t.Error("should contain KernelHub title")
	}
	if !strings.Contains(html, "test-run") {
		t.Error("should contain run ID in embedded JSON")
	}
}

func TestShortHash(t *testing.T) {
	if got := shortHash("abcdef123456789", 12); got != "abcdef123456" {
		t.Errorf("shortHash = %q, want %q", got, "abcdef123456")
	}
	if got := shortHash("abc", 12); got != "abc" {
		t.Errorf("short input: shortHash = %q, want %q", got, "abc")
	}
	if got := shortHash("", 12); got != "" {
		t.Errorf("empty input: shortHash = %q", got)
	}
}

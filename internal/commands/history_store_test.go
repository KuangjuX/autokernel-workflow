package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDBRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	run := RunRecord{
		RunID:       "run-test-001",
		Branch:      "agent/run-test-001",
		RepoPath:    "/tmp/fake",
		SyncedAt:    "2025-01-01T00:00:00Z",
		CommitCount: 2,
		Iterations: []IterationRecord{
			{
				Iteration:  0,
				CommitHash: "aaa111",
				CommitTime: "2025-01-01T00:00:00Z",
				Subject:    "[baseline] init",
				Hypothesis: "init",
				Kernel:     "rms_norm",
				Agent:      "test-agent",
				GPU:        "B200",
				Backend:    "triton",
				Correctness: "PASS",
				SpeedupVsBaseline: 1.0,
				HasSpeedup: true,
				LatencyUs:  100.0,
				HasLatency: true,
			},
			{
				Iteration:  1,
				CommitHash: "bbb222",
				ParentCommitHash: "aaa111",
				CommitTime: "2025-01-01T00:01:00Z",
				Subject:    "exp 1: fix num_sms",
				Hypothesis: "fix num_sms",
				Kernel:     "rms_norm",
				Agent:      "test-agent",
				GPU:        "B200",
				Backend:    "triton",
				Correctness: "PASS",
				SpeedupVsBaseline: 1.5,
				HasSpeedup: true,
				LatencyUs:  66.7,
				HasLatency: true,
				Changes:    "num_sms 132 -> 148",
				Analysis:   "better utilization",
			},
		},
	}

	if err := appendRun(dbPath, run); err != nil {
		t.Fatalf("appendRun: %v", err)
	}

	history, err := loadHistory(dbPath)
	if err != nil {
		t.Fatalf("loadHistory: %v", err)
	}

	if len(history.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(history.Runs))
	}

	got := history.Runs[0]
	if got.RunID != "run-test-001" {
		t.Errorf("RunID = %q, want %q", got.RunID, "run-test-001")
	}
	if got.CommitCount != 2 {
		t.Errorf("CommitCount = %d, want 2", got.CommitCount)
	}
	if len(got.Iterations) != 2 {
		t.Fatalf("expected 2 iterations, got %d", len(got.Iterations))
	}

	it0 := got.Iterations[0]
	if it0.CommitHash != "aaa111" {
		t.Errorf("it0.CommitHash = %q, want %q", it0.CommitHash, "aaa111")
	}
	if it0.Kernel != "rms_norm" {
		t.Errorf("it0.Kernel = %q", it0.Kernel)
	}
	if !it0.HasSpeedup || it0.SpeedupVsBaseline != 1.0 {
		t.Errorf("it0 speedup = %v / %f", it0.HasSpeedup, it0.SpeedupVsBaseline)
	}

	it1 := got.Iterations[1]
	if it1.ParentCommitHash != "aaa111" {
		t.Errorf("it1.ParentCommitHash = %q", it1.ParentCommitHash)
	}
	if it1.Changes != "num_sms 132 -> 148" {
		t.Errorf("it1.Changes = %q", it1.Changes)
	}
	if it1.Backend != "triton" {
		t.Errorf("it1.Backend = %q", it1.Backend)
	}
	if !it1.HasLatency || it1.LatencyUs != 66.7 {
		t.Errorf("it1 latency = %v / %f", it1.HasLatency, it1.LatencyUs)
	}
}

func TestDBMultipleRuns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	for _, id := range []string{"run-a", "run-b", "run-c"} {
		run := RunRecord{
			RunID:       id,
			Branch:      "agent/" + id,
			RepoPath:    "/tmp",
			SyncedAt:    "2025-01-01T00:00:00Z",
			CommitCount: 0,
		}
		if err := appendRun(dbPath, run); err != nil {
			t.Fatalf("appendRun(%s): %v", id, err)
		}
	}

	history, err := loadHistory(dbPath)
	if err != nil {
		t.Fatalf("loadHistory: %v", err)
	}
	if len(history.Runs) != 3 {
		t.Errorf("expected 3 runs, got %d", len(history.Runs))
	}
}

func TestDBNullSpeedupLatency(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	run := RunRecord{
		RunID:       "run-null",
		Branch:      "agent/null",
		RepoPath:    "/tmp",
		SyncedAt:    "2025-01-01T00:00:00Z",
		CommitCount: 1,
		Iterations: []IterationRecord{
			{
				Iteration:   0,
				CommitHash:  "abc",
				CommitTime:  "2025-01-01T00:00:00Z",
				Subject:     "test",
				Hypothesis:  "test",
				Correctness: "FAIL",
				// No speedup or latency set
			},
		},
	}

	if err := appendRun(dbPath, run); err != nil {
		t.Fatal(err)
	}

	history, err := loadHistory(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	it := history.Runs[0].Iterations[0]
	if it.HasSpeedup {
		t.Error("HasSpeedup should be false for null value")
	}
	if it.HasLatency {
		t.Error("HasLatency should be false for null value")
	}
}

func TestDBOpenEmptyPath(t *testing.T) {
	_, err := openHistoryDB("")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestDBOpenCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "test.db")

	db, err := openHistoryDB(deep)
	if err != nil {
		t.Fatalf("openHistoryDB: %v", err)
	}
	db.Close()

	if _, err := os.Stat(deep); err != nil {
		t.Errorf("db file should exist: %v", err)
	}
}

func TestGetSetMeta(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := openHistoryDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	if err := setMetaTx(tx, "version", "1.0"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	val, err := getMeta(db, "version")
	if err != nil {
		t.Fatal(err)
	}
	if val != "1.0" {
		t.Errorf("meta version = %q, want %q", val, "1.0")
	}

	// Upsert
	tx2, _ := db.Begin()
	setMetaTx(tx2, "version", "2.0")
	tx2.Commit()

	val2, _ := getMeta(db, "version")
	if val2 != "2.0" {
		t.Errorf("meta version after upsert = %q, want %q", val2, "2.0")
	}
}

package commands

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type SyncGitOptions struct {
	RepoPath string
	Branch   string
	DBPath   string
	RunID    string
	DryRun   bool
}

type HistoryFile struct {
	GeneratedAt string             `json:"generated_at"`
	Runs        []RunRecord        `json:"runs"`
	Archives    []GitArchiveRecord `json:"archives,omitempty"`
}

type RunRecord struct {
	RunID       string            `json:"run_id"`
	Branch      string            `json:"branch"`
	RepoPath    string            `json:"repo_path"`
	SyncedAt    string            `json:"synced_at"`
	CommitCount int               `json:"commit_count"`
	Iterations  []IterationRecord `json:"iterations"`
}

type IterationRecord struct {
	Iteration         int     `json:"iteration"`
	CommitHash        string  `json:"commit_hash"`
	ParentCommitHash  string  `json:"parent_commit_hash"`
	CommitTime        string  `json:"commit_time"`
	Subject           string  `json:"subject"`
	Hypothesis        string  `json:"hypothesis"`
	Kernel            string  `json:"kernel,omitempty"`
	Agent             string  `json:"agent,omitempty"`
	Correctness       string  `json:"correctness,omitempty"`
	SpeedupVsBaseline float64 `json:"speedup_vs_baseline,omitempty"`
	LatencyUs         float64 `json:"latency_us,omitempty"`
	HasSpeedup        bool    `json:"-"`
	HasLatency        bool    `json:"-"`
	Patch             string  `json:"patch,omitempty"`
	PatchError        string  `json:"patch_error,omitempty"`
}

func SyncGit(opts SyncGitOptions) error {
	if opts.Branch == "" {
		return errors.New("--branch is required")
	}
	if opts.RepoPath == "" {
		return errors.New("--repo-path cannot be empty")
	}
	if opts.DBPath == "" {
		return errors.New("--db-path cannot be empty")
	}

	repoAbs, err := filepath.Abs(opts.RepoPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repoAbs, ".git")); err != nil {
		return fmt.Errorf("repo path is not a git repo: %w", err)
	}

	records, err := collectGitRecords(repoAbs, opts.Branch)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Println("[kernelhub sync-git] no commits found")
		return nil
	}

	runID := opts.RunID
	if runID == "" {
		runID = time.Now().UTC().Format("run-20060102-150405")
	}

	run := RunRecord{
		RunID:       runID,
		Branch:      opts.Branch,
		RepoPath:    repoAbs,
		SyncedAt:    time.Now().UTC().Format(time.RFC3339),
		CommitCount: len(records),
		Iterations:  records,
	}

	if opts.DryRun {
		fmt.Printf("[kernelhub sync-git] dry-run run_id=%s commits=%d\n", runID, len(records))
		return nil
	}

	if err := appendRun(opts.DBPath, run); err != nil {
		return err
	}

	fmt.Printf("[kernelhub sync-git] synced run_id=%s commits=%d -> %s\n", runID, len(records), opts.DBPath)
	return nil
}

func collectGitRecords(repoPath, branch string) ([]IterationRecord, error) {
	cmd := exec.Command(
		"git", "-C", repoPath, "log", branch,
		"--reverse",
		"--pretty=format:%H%x1f%P%x1f%ct%x1f%s%x1f%b%x1e",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	entries := strings.Split(string(out), "\x1e")
	var records []IterationRecord
	for idx, raw := range entries {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 5)
		if len(parts) < 5 {
			continue
		}
		hash := parts[0]
		parent := ""
		if parts[1] != "" {
			parent = strings.Split(parts[1], " ")[0]
		}
		tsUnix, _ := strconv.ParseInt(parts[2], 10, 64)
		subject := strings.TrimSpace(parts[3])
		body := strings.TrimSpace(parts[4])

		iteration := parseIteration(subject, idx)
		record := IterationRecord{
			Iteration:        iteration,
			CommitHash:       hash,
			ParentCommitHash: parent,
			CommitTime:       time.Unix(tsUnix, 0).UTC().Format(time.RFC3339),
			Subject:          subject,
			Hypothesis:       parseHypothesis(subject),
			Kernel:           extractField(`(?m)^kernel:\s*(.+)$`, body),
			Agent:            extractField(`(?m)^agent:\s*(.+)$`, body),
			Correctness:      extractField(`(?m)^correctness:\s*(.+)$`, body),
		}
		if v, ok := parseFloat(extractField(`(?m)^speedup_vs_baseline:\s*(.+)$`, body)); ok {
			record.SpeedupVsBaseline = v
			record.HasSpeedup = true
		}
		if v, ok := parseFloat(extractField(`(?m)^latency_us:\s*(.+)$`, body)); ok {
			record.LatencyUs = v
			record.HasLatency = true
		}
		records = append(records, record)
	}
	return records, nil
}

func parseIteration(subject string, fallback int) int {
	re := regexp.MustCompile(`(?i)\bexp\s+(\d+)\b`)
	m := re.FindStringSubmatch(subject)
	if len(m) < 2 {
		return fallback
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return fallback
	}
	return n
}

func parseHypothesis(subject string) string {
	if i := strings.Index(subject, ":"); i >= 0 {
		return strings.TrimSpace(subject[i+1:])
	}
	return strings.TrimSpace(subject)
}

func extractField(pattern, text string) string {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func parseFloat(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	token := strings.TrimSpace(strings.TrimSuffix(strings.ToLower(s), "x"))
	v, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

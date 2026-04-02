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
	Changes           string  `json:"changes,omitempty"`
	Analysis          string  `json:"analysis,omitempty"`
	Kernel            string  `json:"kernel,omitempty"`
	Agent             string  `json:"agent,omitempty"`
	GPU               string  `json:"gpu,omitempty"`
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
		fields := parseCommitBodyFields(body)

		iteration := parseIteration(subject, idx)
		record := IterationRecord{
			Iteration:        iteration,
			CommitHash:       hash,
			ParentCommitHash: parent,
			CommitTime:       time.Unix(tsUnix, 0).UTC().Format(time.RFC3339),
			Subject:          subject,
			Hypothesis:       parseHypothesis(subject),
			Changes:          firstNonEmpty(fields["changes"], fields["change"]),
			Analysis:         fields["analysis"],
			Kernel:           firstNonEmpty(fields["kernel"], extractField(`(?m)^kernel:\s*(.+)$`, body)),
			Agent:            firstNonEmpty(fields["agent"], extractField(`(?m)^agent:\s*(.+)$`, body)),
			GPU: firstNonEmpty(
				fields["gpu"],
				fields["gpu_model"],
				fields["device"],
				fields["card"],
				extractField(`(?mi)^(?:gpu|gpu_model|device|card):\s*(.+)$`, body),
			),
			Correctness: firstNonEmpty(fields["correctness"], extractField(`(?m)^correctness:\s*(.+)$`, body)),
		}
		if v, ok := parseFloat(firstNonEmpty(
			fields["speedup_vs_baseline"],
			fields["speedup"],
			extractField(`(?m)^speedup_vs_baseline:\s*(.+)$`, body),
		)); ok {
			record.SpeedupVsBaseline = v
			record.HasSpeedup = true
		}
		if v, ok := parseFloat(firstNonEmpty(
			fields["latency_us"],
			fields["latency"],
			extractField(`(?m)^latency_us:\s*(.+)$`, body),
		)); ok {
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

func parseCommitBodyFields(body string) map[string]string {
	fields := map[string][]string{}
	currentKey := ""
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		key, value, ok := parseCommitFieldLine(line)
		if ok {
			currentKey = key
			fields[currentKey] = []string{value}
			continue
		}
		if currentKey == "" {
			continue
		}
		// Preserve multiline field text for keys like "changes" and "analysis".
		fields[currentKey] = append(fields[currentKey], strings.TrimRight(line, " \t"))
	}

	out := map[string]string{}
	for key, chunks := range fields {
		out[key] = strings.TrimSpace(strings.Join(chunks, "\n"))
	}
	return out
}

func parseCommitFieldLine(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", "", false
	}
	first := trimmed[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')) {
		return "", "", false
	}
	keyRaw, value, ok := strings.Cut(trimmed, ":")
	if !ok {
		return "", "", false
	}
	key := normalizeCommitFieldKey(keyRaw)
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

func normalizeCommitFieldKey(raw string) string {
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			lastUnderscore = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastUnderscore = false
		case r == '_' || r == '-' || r == ' ':
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		default:
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

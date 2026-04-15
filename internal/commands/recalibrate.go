package commands

import (
	"bufio"
	"database/sql"
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

type RecalibrateOptions struct {
	DBPath   string
	RunsDir  string
	RunID    string // empty = all runs
	DryRun   bool
	Verbose  bool
}

type recalResult struct {
	RunID            string
	Backend          string
	SolutionRuntime  float64
	RefRuntime       float64
	BaselineRuntime  float64
	SpeedupRef       float64
	SpeedupBaseline  float64
	Correct          bool
	Error            string
}

func Recalibrate(opts RecalibrateOptions) error {
	if opts.DBPath == "" {
		return errors.New("--db-path is required")
	}
	if opts.RunsDir == "" {
		return errors.New("--runs-dir is required")
	}

	history, err := loadHistory(opts.DBPath)
	if err != nil {
		return fmt.Errorf("failed to load history: %w", err)
	}

	var targets []RunRecord
	for _, run := range history.Runs {
		if opts.RunID != "" && run.RunID != opts.RunID {
			continue
		}
		targets = append(targets, run)
	}

	if len(targets) == 0 {
		if opts.RunID != "" {
			return fmt.Errorf("run_id %q not found in history", opts.RunID)
		}
		fmt.Println("[recalibrate] no runs found in history")
		return nil
	}

	fmt.Printf("[recalibrate] found %d run(s) to recalibrate\n", len(targets))

	var results []recalResult
	var errCount, skipCount, okCount int

	for _, run := range targets {
		akoRoot := filepath.Join(opts.RunsDir, run.RunID, "ako")
		result := recalibrateRun(akoRoot, run, opts)
		results = append(results, result)

		if result.Error != "" {
			errCount++
			fmt.Printf("  ERR  %-45s %s\n", run.RunID, result.Error)
			continue
		}
		if result.SpeedupBaseline <= 0 {
			skipCount++
			fmt.Printf("  SKIP %-45s baseline_speedup not available\n", run.RunID)
			continue
		}

		okCount++
		fmt.Printf("  OK   %-45s baseline_speedup=%.4fx (was ref_speedup=%.4fx)\n",
			run.RunID, result.SpeedupBaseline, result.SpeedupRef)
	}

	fmt.Printf("\n[recalibrate] %d OK, %d skipped, %d errors (total %d)\n",
		okCount, skipCount, errCount, len(results))

	if opts.DryRun {
		fmt.Println("[recalibrate] dry-run mode — database not updated")
		return nil
	}

	updated := 0
	for _, r := range results {
		if r.Error != "" || r.SpeedupBaseline <= 0 {
			continue
		}
		if err := updateBestIterationSpeedup(opts.DBPath, r.RunID, r.SpeedupBaseline); err != nil {
			fmt.Printf("[recalibrate] WARNING: failed to update %s: %v\n", r.RunID, err)
			continue
		}
		updated++
	}
	fmt.Printf("[recalibrate] updated %d run(s) in %s\n", updated, opts.DBPath)
	return nil
}

func recalibrateRun(akoRoot string, run RunRecord, opts RecalibrateOptions) recalResult {
	result := recalResult{RunID: run.RunID}

	akoAbs, err := filepath.Abs(akoRoot)
	if err != nil {
		result.Error = fmt.Sprintf("bad path: %v", err)
		return result
	}

	solutionPath := filepath.Join(akoAbs, "solution", "kernel.py")
	refPath := filepath.Join(akoAbs, "input", "reference.py")
	baselinePath := filepath.Join(akoAbs, "input", "kernel.py")

	// Prefer the main AKO4ALL bench.py (which has --baseline support)
	// over the worktree-local copy which may be outdated.
	runsAbs, _ := filepath.Abs(opts.RunsDir)
	mainBenchPy, _ := filepath.Abs(filepath.Join(runsAbs, "..", "..", "third_party", "AKO4ALL", "bench", "kernelbench", "bench.py"))
	benchPy := filepath.Join(akoAbs, "bench", "kernelbench", "bench.py")
	if _, err := os.Stat(mainBenchPy); err == nil {
		benchPy = mainBenchPy
	}

	for _, p := range []string{solutionPath, refPath, baselinePath, benchPy} {
		if _, err := os.Stat(p); err != nil {
			result.Error = fmt.Sprintf("missing: %s", filepath.Base(p))
			return result
		}
	}

	backend := inferBackend(run)
	result.Backend = backend

	precision := inferPrecision(akoAbs)

	args := []string{
		benchPy,
		"--ref", refPath,
		"--solution", solutionPath,
		"--baseline", baselinePath,
		"--backend", backend,
		"--num-correct-trials", "3",
		"--num-perf-trials", "10",
		"--verbose",
	}
	if precision != "" {
		args = append(args, "--precision", precision)
	}

	if opts.Verbose {
		fmt.Printf("[recalibrate] running: python %s\n", strings.Join(args, " "))
	}

	cmd := exec.Command("python", args...)
	cmd.Dir = akoAbs
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		result.Error = fmt.Sprintf("bench failed: %v", err)
		if opts.Verbose {
			fmt.Printf("[recalibrate] bench output:\n%s\n", string(out))
		}
		return result
	}

	parsed := parseBenchOutput(string(out))
	result.SolutionRuntime = parsed.runtime
	result.RefRuntime = parsed.refRuntime
	result.BaselineRuntime = parsed.baselineRuntime
	result.SpeedupRef = parsed.speedup
	result.SpeedupBaseline = parsed.baselineSpeedup
	result.Correct = parsed.correct

	return result
}

type benchParsed struct {
	runtime         float64
	refRuntime      float64
	baselineRuntime float64
	speedup         float64
	baselineSpeedup float64
	correct         bool
}

func parseBenchOutput(output string) benchParsed {
	var p benchParsed
	scanner := bufio.NewScanner(strings.NewReader(output))
	reFloat := regexp.MustCompile(`^[\d.]+`)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "CORRECT:"):
			p.correct = strings.Contains(strings.ToLower(line), "true")
		case strings.HasPrefix(line, "RUNTIME:"):
			p.runtime = parseFloatFromLine(line, reFloat)
		case strings.HasPrefix(line, "REF_RUNTIME:"):
			p.refRuntime = parseFloatFromLine(line, reFloat)
		case strings.HasPrefix(line, "BASELINE_RUNTIME:"):
			p.baselineRuntime = parseFloatFromLine(line, reFloat)
		case strings.HasPrefix(line, "SPEEDUP:"):
			p.speedup = parseFloatFromLine(line, reFloat)
		case strings.HasPrefix(line, "BASELINE_SPEEDUP:"):
			p.baselineSpeedup = parseFloatFromLine(line, reFloat)
		}
	}
	return p
}

func parseFloatFromLine(line string, reFloat *regexp.Regexp) float64 {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return 0
	}
	val := strings.TrimSpace(parts[1])
	val = strings.TrimSuffix(val, "x")
	m := reFloat.FindString(val)
	if m == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(m, 64)
	return f
}

func inferBackend(run RunRecord) string {
	for i := len(run.Iterations) - 1; i >= 0; i-- {
		b := strings.TrimSpace(strings.ToLower(run.Iterations[i].Backend))
		if b != "" {
			return b
		}
	}
	if strings.Contains(strings.ToLower(run.RunID), "cuda") {
		return "cuda"
	}
	return "triton"
}

// inferPrecision extracts the --precision value from the run's bench.sh.
// Returns "" if not found (bench.py will use its own default).
func inferPrecision(akoAbs string) string {
	benchSh := filepath.Join(akoAbs, "scripts", "bench.sh")
	data, err := os.ReadFile(benchSh)
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`--precision\s+(\S+)`)
	m := re.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

// updateBestIterationSpeedup updates the speedup_vs_baseline for the
// best iteration (highest existing speedup with correctness=PASS) of a run.
func updateBestIterationSpeedup(dbPath, runID string, newSpeedup float64) error {
	db, err := openHistoryDB(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	var runRowID int64
	if err := db.QueryRow(
		`SELECT id FROM runs WHERE run_id = ? LIMIT 1`, runID,
	).Scan(&runRowID); err != nil {
		return fmt.Errorf("run %s not found: %w", runID, err)
	}

	// Find the iteration with the best speedup and PASS correctness
	var iterID int64
	err = db.QueryRow(`
		SELECT id FROM iterations
		WHERE run_row_id = ?
		  AND UPPER(correctness) = 'PASS'
		  AND speedup_vs_baseline IS NOT NULL
		ORDER BY speedup_vs_baseline DESC
		LIMIT 1`, runRowID,
	).Scan(&iterID)

	if errors.Is(err, sql.ErrNoRows) {
		// No PASS iteration with speedup — update the last iteration instead
		err = db.QueryRow(`
			SELECT id FROM iterations
			WHERE run_row_id = ?
			ORDER BY id DESC
			LIMIT 1`, runRowID,
		).Scan(&iterID)
	}
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		UPDATE iterations SET speedup_vs_baseline = ?
		WHERE id = ?`, newSpeedup, iterID)
	if err != nil {
		return err
	}

	// Update synced_at to mark the recalibration
	_, err = db.Exec(`
		UPDATE runs SET synced_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), runRowID)
	return err
}

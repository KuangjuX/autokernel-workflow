package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type PrepareOptions struct {
	KernelSrc      string
	AKORoot        string
	ReferenceSrc   string
	BenchSrc       string
	ContextSrc     string
	RunID          string
	WorkloadConfig string
	DryRun         bool
}

type WorkloadConfig struct {
	Name        string                       `json:"name"`
	Description string                       `json:"description"`
	Model       map[string]interface{}       `json:"model"`
	Training    map[string]interface{}       `json:"training"`
	Shapes      map[string]map[string]int64  `json:"shapes"`
}

func Prepare(opts PrepareOptions) error {
	if opts.KernelSrc == "" {
		return errors.New("--kernel-src is required")
	}
	if opts.AKORoot == "" {
		return errors.New("--ako-root cannot be empty")
	}

	akoRoot, err := filepath.Abs(opts.AKORoot)
	if err != nil {
		return err
	}
	kernelSrc, err := filepath.Abs(opts.KernelSrc)
	if err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(akoRoot, "TASK.md")); err != nil {
		return fmt.Errorf("invalid AKO4ALL root (TASK.md missing): %w", err)
	}
	if _, err := os.Stat(kernelSrc); err != nil {
		return fmt.Errorf("kernel source not found: %w", err)
	}

	runID := opts.RunID
	if runID == "" {
		runID = time.Now().UTC().Format("run-20060102-150405")
	}

	inputDir := filepath.Join(akoRoot, "input")
	benchCustomDir := filepath.Join(akoRoot, "bench", "custom")
	contextDir := filepath.Join(akoRoot, "context")
	if !opts.DryRun {
		if err := os.MkdirAll(inputDir, 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(benchCustomDir, 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(contextDir, 0o755); err != nil {
			return err
		}
	}

	if err := copyToTarget(kernelSrc, filepath.Join(inputDir, filepath.Base(kernelSrc)), opts.DryRun); err != nil {
		return err
	}
	if opts.ReferenceSrc != "" {
		refAbs, err := filepath.Abs(opts.ReferenceSrc)
		if err != nil {
			return err
		}
		if err := copyToTarget(refAbs, filepath.Join(inputDir, filepath.Base(refAbs)), opts.DryRun); err != nil {
			return err
		}
	}
	if opts.BenchSrc != "" {
		benchAbs, err := filepath.Abs(opts.BenchSrc)
		if err != nil {
			return err
		}
		if err := copyToTarget(benchAbs, filepath.Join(benchCustomDir, filepath.Base(benchAbs)), opts.DryRun); err != nil {
			return err
		}
	}
	if opts.ContextSrc != "" {
		ctxAbs, err := filepath.Abs(opts.ContextSrc)
		if err != nil {
			return err
		}
		if err := copyToTarget(ctxAbs, filepath.Join(contextDir, filepath.Base(ctxAbs)), opts.DryRun); err != nil {
			return err
		}
	}

	// Apply workload config shape overrides to reference.py if provided.
	var wlCfg *WorkloadConfig
	if opts.WorkloadConfig != "" {
		cfgPath, err := filepath.Abs(opts.WorkloadConfig)
		if err != nil {
			return err
		}
		wlCfg, err = loadWorkloadConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("failed to load workload config: %w", err)
		}
		fmt.Printf("[kernelhub prepare] loaded workload config: %s (%s)\n", wlCfg.Name, cfgPath)

		if opts.ReferenceSrc != "" {
			kernelName := deriveKernelName(opts.ReferenceSrc)
			if shapes, ok := wlCfg.Shapes[kernelName]; ok {
				refDst := filepath.Join(inputDir, filepath.Base(opts.ReferenceSrc))
				// In dry-run mode the file hasn't been copied yet; read from source.
				readPath := refDst
				if opts.DryRun {
					readPath, _ = filepath.Abs(opts.ReferenceSrc)
				}
				if err := applyShapeOverrides(readPath, refDst, shapes, opts.DryRun); err != nil {
					return fmt.Errorf("failed to apply shape overrides for %s: %w", kernelName, err)
				}
			} else {
				fmt.Printf("[kernelhub prepare] WARNING: no shapes defined for kernel %q in workload config\n", kernelName)
			}
		}
	}

	manifest := map[string]interface{}{
		"run_id":           runID,
		"generated_at":     time.Now().UTC().Format(time.RFC3339),
		"ako_root":         akoRoot,
		"kernel_src":       kernelSrc,
		"input_dir":        inputDir,
		"bench_custom_dir": benchCustomDir,
		"context_dir":      contextDir,
	}
	if opts.ReferenceSrc != "" {
		manifest["reference_src"] = opts.ReferenceSrc
	}
	if opts.BenchSrc != "" {
		manifest["bench_src"] = opts.BenchSrc
	}
	if opts.ContextSrc != "" {
		manifest["context_src"] = opts.ContextSrc
	}
	if wlCfg != nil {
		manifest["workload_config"] = opts.WorkloadConfig
		manifest["workload_name"] = wlCfg.Name
		if opts.ReferenceSrc != "" {
			kernelName := deriveKernelName(opts.ReferenceSrc)
			if shapes, ok := wlCfg.Shapes[kernelName]; ok {
				shapeStrs := make(map[string]string, len(shapes))
				for k, v := range shapes {
					shapeStrs[k] = strconv.FormatInt(v, 10)
				}
				manifest["applied_shapes"] = shapeStrs
			}
		}
	}

	manifestPath := filepath.Join("workspace", "latest_prepare_manifest.json")
	if opts.DryRun {
		fmt.Printf("[kernelhub prepare] dry-run manifest target: %s\n", manifestPath)
	} else {
		if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
			return err
		}
		content, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(manifestPath, content, 0o644); err != nil {
			return err
		}
	}

	if !opts.DryRun {
		if err := installGitGuardHooks(akoRoot); err != nil {
			fmt.Printf("[kernelhub prepare] WARNING: failed to install git guard hooks: %v\n", err)
		}
	}

	fmt.Printf("[kernelhub prepare] run_id=%s prepared\n", runID)
	return nil
}

func loadWorkloadConfig(path string) (*WorkloadConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg WorkloadConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("workload config missing required field: name")
	}
	if len(cfg.Shapes) == 0 {
		return nil, fmt.Errorf("workload config has no shapes defined")
	}
	return &cfg, nil
}

// deriveKernelName extracts the kernel name from a reference.py path.
// e.g. "/path/to/rms_norm_pkg/reference.py" -> "rms_norm"
//      "/path/to/rms_norm_pkg/"              -> "rms_norm"
func deriveKernelName(refPath string) string {
	abs, _ := filepath.Abs(refPath)
	dir := abs
	if filepath.Base(abs) == "reference.py" {
		dir = filepath.Dir(abs)
	}
	base := filepath.Base(dir)
	return strings.TrimSuffix(base, "_pkg")
}

// applyShapeOverrides rewrites module-level integer constant assignments in a
// Python reference.py file. Only assignments whose variable name appears as a
// key in the shapes map are touched. Lines like "M = 4096" become "M = <new>".
// Derived constants (e.g. "ROPE_DIM = HEAD_DIM - NOPE_DIM") are left alone.
//
// readPath is the file to read from; writePath is where to write.
// In dry-run mode, no write occurs but changes are printed.
func applyShapeOverrides(readPath, writePath string, shapes map[string]int64, dryRun bool) error {
	content, err := os.ReadFile(readPath)
	if err != nil {
		return err
	}

	src := string(content)
	modified := false

	// Match lines like:  NAME = 1234
	// but NOT:           NAME = OTHER_VAR - THING  (derived)
	//                    NAME = "string"
	assignRe := regexp.MustCompile(`(?m)^(\s*)([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(\d+)[ \t]*$`)

	result := assignRe.ReplaceAllStringFunc(src, func(match string) string {
		parts := assignRe.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		indent := parts[1]
		varName := parts[2]
		oldVal := parts[3]

		newVal, ok := shapes[varName]
		if !ok {
			return match
		}

		newValStr := strconv.FormatInt(newVal, 10)
		if oldVal == newValStr {
			return match
		}

		modified = true
		if dryRun {
			fmt.Printf("[dry-run] %s: %s = %s -> %s\n", writePath, varName, oldVal, newValStr)
		} else {
			fmt.Printf("[kernelhub prepare] %s: %s = %s -> %s\n", filepath.Base(writePath), varName, oldVal, newValStr)
		}
		return indent + varName + " = " + newValStr
	})

	if !modified {
		fmt.Printf("[kernelhub prepare] %s: no shape changes needed\n", filepath.Base(writePath))
		return nil
	}

	if dryRun {
		return nil
	}

	return os.WriteFile(writePath, []byte(result), 0o644)
}

func installGitGuardHooks(akoRoot string) error {
	gitDir := resolveGitDir(akoRoot)
	if gitDir == "" {
		return fmt.Errorf("cannot locate .git directory for %s", akoRoot)
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}

	postRewriteHook := `#!/bin/bash
# KernelHub guard: reject history-rewriting operations (rebase, amend)
# Installed by: kernelhub prepare
ACTION="$1"
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  ⛔ KERNELHUB GUARD: FORBIDDEN GIT OPERATION DETECTED       ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Action: $ACTION"
echo "║"
echo "║  git rebase and git commit --amend are FORBIDDEN in         ║"
echo "║  AKO4ALL optimization runs. They rewrite commit history     ║"
echo "║  and will cause kernelhub sync-git to REJECT this run.      ║"
echo "║                                                             ║"
echo "║  Use 'git revert <hash>' to undo bad iterations instead.   ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "[KernelHub] WARNING: The $ACTION has already been applied."
echo "[KernelHub] Your run history is now corrupted. You must either:"
echo "  1. Undo this manually (git reflog + git reset to restore), or"
echo "  2. Restart the optimization run from scratch."
echo ""
exit 0
`

	hookPath := filepath.Join(hooksDir, "post-rewrite")
	if err := os.WriteFile(hookPath, []byte(postRewriteHook), 0o755); err != nil {
		return err
	}

	fmt.Printf("[kernelhub prepare] installed git guard hook: %s\n", hookPath)
	return nil
}

func resolveGitDir(repoPath string) string {
	gitPath := filepath.Join(repoPath, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return gitPath
	}
	content, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(content))
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	target := strings.TrimPrefix(line, prefix)
	if !filepath.IsAbs(target) {
		target = filepath.Join(repoPath, target)
	}
	return target
}

func copyToTarget(src, dst string, dryRun bool) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Printf("[dry-run] copy %s -> %s\n", src, dst)
		return nil
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type PrepareOptions struct {
	KernelSrc    string
	AKORoot      string
	ReferenceSrc string
	BenchSrc     string
	ContextSrc   string
	RunID        string
	DryRun       bool
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

	manifest := map[string]string{
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

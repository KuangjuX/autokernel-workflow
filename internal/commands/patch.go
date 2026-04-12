package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type PatchOptions struct {
	DBPath       string
	KernelAssets string
	MMQRoot      string
	OutputDir    string
	RunsDir      string
	Verify       bool
	Apply        bool
	DryRun       bool
}

type PatchSummary struct {
	Generated int
	Skipped   int
	Errored   int
	Verified  int
	Failed    int
}

func Patch(opts PatchOptions) error {
	if opts.DBPath == "" {
		return errors.New("--db-path is required")
	}
	if opts.KernelAssets == "" {
		return errors.New("--kernel-assets is required")
	}
	if opts.MMQRoot == "" {
		return errors.New("--mmq-root is required")
	}

	dbPath, _ := filepath.Abs(opts.DBPath)
	kernelAssets, _ := filepath.Abs(opts.KernelAssets)
	mmqRoot, _ := filepath.Abs(opts.MMQRoot)
	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = "workspace/patches"
	}
	outputDir, _ = filepath.Abs(outputDir)

	for _, check := range []struct{ label, path string }{
		{"history DB", dbPath},
		{"kernel_assets", kernelAssets},
		{"MMQ root", mmqRoot},
	} {
		if _, err := os.Stat(check.path); err != nil {
			return fmt.Errorf("%s not found: %s", check.label, check.path)
		}
	}

	adapterBin, err := resolveAdapterBin()
	if err != nil {
		return err
	}

	fmt.Println("[kernelhub patch] === Patch Generation ===")
	fmt.Printf("  db-path:       %s\n", dbPath)
	fmt.Printf("  kernel-assets: %s\n", kernelAssets)
	fmt.Printf("  mmq-root:      %s\n", mmqRoot)
	fmt.Printf("  output-dir:    %s\n", outputDir)
	fmt.Printf("  adapter:       %s\n", adapterBin)
	fmt.Println()

	if opts.DryRun {
		fmt.Println("[kernelhub patch] dry-run: would invoke kernel-adapter batch-patch")
		return nil
	}

	// Step 1: Generate patches via kernel-adapter batch-patch
	fmt.Println("[kernelhub patch] --- Step 1: Generating patches ---")
	fmt.Println()

	batchArgs := []string{
		adapterBin, "batch-patch",
		"--db-path", dbPath,
		"--kernel-assets", kernelAssets,
		"--mmq-root", mmqRoot,
		"--output-dir", outputDir,
	}
	if opts.RunsDir != "" {
		runsDir, _ := filepath.Abs(opts.RunsDir)
		batchArgs = append(batchArgs, "--runs-dir", runsDir)
	}

	batchCmd := exec.Command(batchArgs[0], batchArgs[1:]...)
	batchCmd.Stdout = os.Stdout
	batchCmd.Stderr = os.Stderr
	if err := batchCmd.Run(); err != nil {
		return fmt.Errorf("kernel-adapter batch-patch failed: %w", err)
	}
	fmt.Println()

	// Step 2: Verify patches (git apply --check)
	if opts.Verify || opts.Apply {
		mmqRepo := resolveGitToplevel(mmqRoot)
		if mmqRepo == "" {
			mmqRepo = filepath.Dir(mmqRoot)
		}
		mmqSubdir := ""
		if mmqRepo != mmqRoot {
			rel, err := filepath.Rel(mmqRepo, mmqRoot)
			if err == nil && !strings.HasPrefix(rel, "..") {
				mmqSubdir = rel
			}
		}

		fmt.Println("[kernelhub patch] --- Step 2: Verifying patches ---")
		fmt.Println()

		summary := verifyPatches(outputDir, mmqRepo, mmqSubdir)
		fmt.Println()
		fmt.Printf("[kernelhub patch] Verify: %d passed, %d failed\n",
			summary.Verified, summary.Failed)

		// Step 3: Apply patches
		if opts.Apply && summary.Failed == 0 && summary.Verified > 0 {
			fmt.Println()
			fmt.Println("[kernelhub patch] --- Step 3: Applying patches ---")
			applyPatches(outputDir, mmqRepo, adapterBin)
		} else if opts.Apply && summary.Failed > 0 {
			fmt.Printf("\n[kernelhub patch] Skipping apply: %d patch(es) failed verification.\n",
				summary.Failed)
			return fmt.Errorf("%d patch(es) failed verification", summary.Failed)
		}
	}

	fmt.Printf("\n[kernelhub patch] Done. Patches saved to: %s\n", outputDir)
	return nil
}

// resolveAdapterBin finds the kernel-adapter CLI binary.
// Tries: 1) PATH lookup, 2) uv run in submodule.
func resolveAdapterBin() (string, error) {
	if bin, err := exec.LookPath("kernel-adapter"); err == nil {
		return bin, nil
	}

	for _, submodulePath := range []string{
		"third_party/kernel-adapter",
		"third_party/kernel-adapater",
	} {
		pyprojectPath := filepath.Join(submodulePath, "pyproject.toml")
		if _, err := os.Stat(pyprojectPath); err == nil {
			if uvBin, err := exec.LookPath("uv"); err == nil {
				return uvBin, nil
			}
		}
	}

	return "", fmt.Errorf(
		"kernel-adapter not found. Install it via:\n" +
			"  cd third_party/kernel-adapter && uv sync\n" +
			"Or: pip install -e third_party/kernel-adapter",
	)
}

func resolveGitToplevel(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func verifyPatches(patchDir, mmqRepo, mmqSubdir string) PatchSummary {
	summary := PatchSummary{}

	entries, err := os.ReadDir(patchDir)
	if err != nil {
		return summary
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		diffPath := filepath.Join(patchDir, entry.Name(), "patch.diff")
		info, err := os.Stat(diffPath)
		if err != nil || info.Size() == 0 {
			continue
		}

		args := []string{"-C", mmqRepo, "apply", "--check"}
		if mmqSubdir != "" {
			args = append(args, "--directory="+mmqSubdir)
		}
		args = append(args, diffPath)

		cmd := exec.Command("git", args...)
		if err := cmd.Run(); err != nil {
			fmt.Printf("  x FAIL   %s\n", entry.Name())
			summary.Failed++
		} else {
			fmt.Printf("  v PASS   %s\n", entry.Name())
			summary.Verified++
		}
	}
	return summary
}

func applyPatches(patchDir, mmqRepo, adapterBin string) {
	entries, err := os.ReadDir(patchDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		diffPath := filepath.Join(patchDir, entry.Name(), "patch.diff")
		if info, err := os.Stat(diffPath); err != nil || info.Size() == 0 {
			continue
		}

		kernelName := inferKernelNameFromPatchMeta(
			filepath.Join(patchDir, entry.Name(), "metadata.json"),
			entry.Name(),
		)

		// Use kernel-adapter patch-apply or direct git apply
		useAdapter := false
		if adapterBin != "" {
			if _, err := exec.LookPath("kernel-adapter"); err == nil {
				useAdapter = true
			}
		}

		if useAdapter {
			cmd := exec.Command("kernel-adapter", "patch-apply",
				"--patch-file", diffPath,
				"--target-repo", mmqRepo,
				"--kernel-name", kernelName,
			)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Printf("  x FAIL   %s: %v\n", entry.Name(), err)
			} else {
				fmt.Printf("  v APPLIED %s\n", entry.Name())
			}
		} else {
			cmd := exec.Command("git", "-C", mmqRepo, "apply", diffPath)
			if err := cmd.Run(); err != nil {
				fmt.Printf("  x FAIL   %s: %v\n", entry.Name(), err)
			} else {
				fmt.Printf("  v APPLIED %s\n", entry.Name())
			}
		}
	}
}

func inferKernelNameFromPatchMeta(metaPath, fallback string) string {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return fallback
	}
	var meta struct {
		KernelName string `json:"kernel_name"`
	}
	if err := json.Unmarshal(data, &meta); err != nil || meta.KernelName == "" {
		return fallback
	}
	return meta.KernelName
}

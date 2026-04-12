package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	DBPath         string
	DryRun         bool
}

type WorkloadConfig struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Model       map[string]interface{}    `json:"model"`
	Training    map[string]interface{}    `json:"training"`
	Shapes      map[string]ShapeEntry     `json:"shapes"`
}

type ShapeEntry struct {
	Category     string                          `json:"category"`
	Dims         map[string]int64                `json:"dims"`
	InputShapes  map[string]json.RawMessage      `json:"input_shapes"`
	DynamicRange map[string]DynamicDimRange      `json:"dynamic_range"`
	Notes        string                          `json:"notes"`
}

type DynamicDimRange struct {
	Min         int64 `json:"min"`
	Max         int64 `json:"max"`
	Theoretical int64 `json:"theoretical"`
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
			entry, matchedKey := lookupShapeEntry(wlCfg.Shapes, kernelName)
			if entry != nil {
				if matchedKey != kernelName {
					fmt.Printf("[kernelhub prepare] matched kernel %q -> workload key %q\n", kernelName, matchedKey)
				}
				refDst := filepath.Join(inputDir, filepath.Base(opts.ReferenceSrc))
				readPath := refDst
				if opts.DryRun {
					readPath, _ = filepath.Abs(opts.ReferenceSrc)
				}
				shapeConfigs := expandShapeConfigs(matchedKey, *entry, wlCfg.Name)
				if err := applyMultiShapeOverrides(readPath, refDst, entry.Dims, shapeConfigs, opts.DryRun); err != nil {
					return fmt.Errorf("failed to apply shape overrides for %s: %w", matchedKey, err)
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
			entry, matchedKey := lookupShapeEntry(wlCfg.Shapes, kernelName)
			if entry != nil {
				shapeStrs := make(map[string]string, len(entry.Dims))
				for k, v := range entry.Dims {
					shapeStrs[k] = strconv.FormatInt(v, 10)
				}
				manifest["applied_shapes"] = shapeStrs
				manifest["matched_kernel"] = matchedKey
				shapeConfigs := expandShapeConfigs(matchedKey, *entry, wlCfg.Name)
				manifest["shape_configs_count"] = len(shapeConfigs)
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

	// Auto-inject cross-run history context when --db-path is provided.
	if opts.DBPath != "" && opts.ReferenceSrc != "" && !opts.DryRun {
		kernelName := deriveKernelName(opts.ReferenceSrc)
		backend := ""
		if wlCfg != nil {
			entry, _ := lookupShapeEntry(wlCfg.Shapes, kernelName)
			if entry != nil {
				backend = entry.Category
			}
		}
		if err := GenerateContextForPrepare(opts.DBPath, kernelName, backend, contextDir); err != nil {
			fmt.Printf("[kernelhub prepare] WARNING: failed to inject history context: %v\n", err)
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
	for name, entry := range cfg.Shapes {
		if len(entry.Dims) == 0 {
			return nil, fmt.Errorf("shape entry %q has no dims", name)
		}
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

// lookupShapeEntry finds the best matching ShapeEntry for a kernel name.
// Tries exact match first, then tries common suffixes, then strips common
// prefixes like "cuda_", then does prefix matching against JSON keys.
// Returns the entry and the matched key, or nil if no match.
func lookupShapeEntry(shapes map[string]ShapeEntry, kernelName string) (*ShapeEntry, string) {
	candidates := []string{kernelName}
	// Strip common prefixes from asset directory names
	for _, prefix := range []string{"cuda_", "triton_"} {
		if strings.HasPrefix(kernelName, prefix) {
			candidates = append(candidates, strings.TrimPrefix(kernelName, prefix))
		}
	}
	// Strip version suffixes like _v2, _v3
	for i := len(candidates) - 1; i >= 0; i-- {
		name := candidates[i]
		trimmed := regexp.MustCompile(`_v\d+$`).ReplaceAllString(name, "")
		if trimmed != name {
			candidates = append(candidates, trimmed)
		}
	}

	for _, name := range candidates {
		if entry, ok := shapes[name]; ok {
			return &entry, name
		}
		for _, suffix := range []string{"_forward", "_backward", "_fwd", "_bwd"} {
			if entry, ok := shapes[name+suffix]; ok {
				return &entry, name + suffix
			}
		}
	}

	// Prefix match: find all JSON keys that start with any candidate
	var matches []string
	for _, name := range candidates {
		for key := range shapes {
			if strings.HasPrefix(key, name+"_") || strings.HasPrefix(key, name+"__") {
				matches = append(matches, key)
			}
		}
	}
	if len(matches) == 1 {
		entry := shapes[matches[0]]
		return &entry, matches[0]
	}
	return nil, ""
}

// ShapeConfig represents a single shape configuration entry for SHAPE_CONFIGS.
type ShapeConfig struct {
	Label string
	Dims  map[string]int64
}

// expandShapeConfigs generates one or more ShapeConfig entries from a ShapeEntry.
// If the entry has dynamic_range, it produces multiple configs for the nominal
// value plus the min/max boundaries of each dynamic dimension.
func expandShapeConfigs(kernelName string, entry ShapeEntry, workloadName string) []ShapeConfig {
	baseDims := make(map[string]int64, len(entry.Dims))
	for k, v := range entry.Dims {
		baseDims[k] = v
	}

	if len(entry.DynamicRange) == 0 {
		label := workloadName
		if label == "" {
			label = "default"
		}
		return []ShapeConfig{{Label: label, Dims: baseDims}}
	}

	var configs []ShapeConfig

	// Nominal shape
	nominalLabel := workloadName + "-nominal"
	configs = append(configs, ShapeConfig{Label: nominalLabel, Dims: cloneDims(baseDims)})

	// Min/max for each dynamic dimension
	for dimName, dr := range entry.DynamicRange {
		if dr.Min > 0 && dr.Min != baseDims[dimName] {
			minDims := cloneDims(baseDims)
			minDims[dimName] = dr.Min
			configs = append(configs, ShapeConfig{
				Label: fmt.Sprintf("%s-%s-min", workloadName, dimName),
				Dims:  minDims,
			})
		}
		if dr.Max > 0 && dr.Max != baseDims[dimName] {
			maxDims := cloneDims(baseDims)
			maxDims[dimName] = dr.Max
			configs = append(configs, ShapeConfig{
				Label: fmt.Sprintf("%s-%s-max", workloadName, dimName),
				Dims:  maxDims,
			})
		}
	}

	return configs
}

func cloneDims(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// applyMultiShapeOverrides rewrites a reference.py to contain both the
// primary dimension variables and a SHAPE_CONFIGS list that bench.py can
// iterate over. It also ensures get_inputs() accepts a shape_idx parameter.
func applyMultiShapeOverrides(readPath, writePath string, primaryDims map[string]int64, configs []ShapeConfig, dryRun bool) error {
	content, err := os.ReadFile(readPath)
	if err != nil {
		return err
	}

	src := string(content)

	// Pre-injection validation
	v := validateReferenceStructure(src)
	for _, e := range v.Errors {
		return fmt.Errorf("reference.py validation failed (%s): %s", filepath.Base(readPath), e)
	}
	for _, w := range v.Warnings {
		fmt.Printf("[kernelhub prepare] WARNING (%s): %s\n", filepath.Base(readPath), w)
	}

	// Step 1: Override primary dimension variables (M = ..., N = ..., etc.)
	assignRe := regexp.MustCompile(`(?m)^(\s*)([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(\d+)[ \t]*$`)
	src = assignRe.ReplaceAllStringFunc(src, func(match string) string {
		parts := assignRe.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		indent := parts[1]
		varName := parts[2]
		oldVal := parts[3]
		newVal, ok := primaryDims[varName]
		if !ok {
			return match
		}
		newValStr := strconv.FormatInt(newVal, 10)
		if oldVal != newValStr {
			if dryRun {
				fmt.Printf("[dry-run] %s: %s = %s -> %s\n", writePath, varName, oldVal, newValStr)
			} else {
				fmt.Printf("[kernelhub prepare] %s: %s = %s -> %s\n", filepath.Base(writePath), varName, oldVal, newValStr)
			}
		}
		return indent + varName + " = " + newValStr
	})

	// Step 2: Generate SHAPE_CONFIGS block
	shapeConfigsPy := buildShapeConfigsPython(configs)

	// Step 3: Replace or insert SHAPE_CONFIGS in the source
	scRe := regexp.MustCompile(`(?ms)^SHAPE_CONFIGS\s*=\s*\[.*?\]\s*$`)
	if scRe.MatchString(src) {
		src = scRe.ReplaceAllString(src, shapeConfigsPy)
	} else {
		// Insert after the last top-level variable assignment, before get_inputs
		getInputsRe := regexp.MustCompile(`(?m)^def get_inputs\(`)
		loc := getInputsRe.FindStringIndex(src)
		if loc != nil {
			src = src[:loc[0]] + "\n" + shapeConfigsPy + "\n\n" + src[loc[0]:]
		} else {
			src += "\n\n" + shapeConfigsPy + "\n"
		}
	}

	// Step 4: Ensure get_inputs() accepts shape_idx parameter and uses SHAPE_CONFIGS
	if !strings.Contains(src, "shape_idx") {
		src = ensureGetInputsShapeIdx(src, primaryDims)
	}

	fmt.Printf("[kernelhub prepare] %s: generated %d shape configs\n", filepath.Base(writePath), len(configs))
	for i, sc := range configs {
		dimParts := make([]string, 0, len(sc.Dims))
		for k, v := range sc.Dims {
			dimParts = append(dimParts, fmt.Sprintf("%s=%d", k, v))
		}
		fmt.Printf("[kernelhub prepare]   [%d] %s: %s\n", i, sc.Label, strings.Join(dimParts, ", "))
	}

	if dryRun {
		return nil
	}

	// Post-injection syntax check
	if err := verifySyntax(src, filepath.Base(writePath)); err != nil {
		return fmt.Errorf("shape injection produced invalid Python: %w", err)
	}

	return os.WriteFile(writePath, []byte(src), 0o644)
}

// buildShapeConfigsPython generates the Python SHAPE_CONFIGS = [...] block.
func buildShapeConfigsPython(configs []ShapeConfig) string {
	var b strings.Builder
	b.WriteString("SHAPE_CONFIGS = [\n")
	for _, sc := range configs {
		b.WriteString("    {")
		b.WriteString(fmt.Sprintf(`"label": %q`, sc.Label))

		// Sort dim keys for deterministic output
		keys := sortedKeys(sc.Dims)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf(`, %q: %d`, k, sc.Dims[k]))
		}
		b.WriteString("},\n")
	}
	b.WriteString("]")
	return b.String()
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// ensureGetInputsShapeIdx modifies the existing get_inputs() function to accept
// a shape_idx parameter and look up dimensions from SHAPE_CONFIGS.
func ensureGetInputsShapeIdx(src string, dims map[string]int64) string {
	// Replace "def get_inputs():" with "def get_inputs(shape_idx=None):"
	src = strings.Replace(src, "def get_inputs():", "def get_inputs(shape_idx=None):", 1)

	// Build the shape lookup preamble
	keys := sortedKeys(dims)
	varList := make([]string, 0, len(keys))
	for _, k := range keys {
		varList = append(varList, strings.ToLower(k))
	}

	var preamble strings.Builder
	preamble.WriteString("    if shape_idx is not None and 0 <= shape_idx < len(SHAPE_CONFIGS):\n")
	preamble.WriteString("        cfg = SHAPE_CONFIGS[shape_idx]\n")
	for _, k := range keys {
		lower := strings.ToLower(k)
		preamble.WriteString(fmt.Sprintf("        %s = cfg[%q]\n", lower, k))
	}

	// Insert after the "def get_inputs(shape_idx=None):" line
	defLine := "def get_inputs(shape_idx=None):"
	idx := strings.Index(src, defLine)
	if idx < 0 {
		return src
	}
	insertAt := idx + len(defLine)
	// Find the end of the def line (the newline)
	nlIdx := strings.Index(src[insertAt:], "\n")
	if nlIdx < 0 {
		return src
	}
	insertAt += nlIdx + 1

	// Only insert if not already present
	end := insertAt + 200
	if end > len(src) {
		end = len(src)
	}
	if !strings.Contains(src[insertAt:end], "SHAPE_CONFIGS[shape_idx]") {
		src = src[:insertAt] + preamble.String() + src[insertAt:]
	}

	return src
}

// ---------------------------------------------------------------------------
// reference.py validation
// ---------------------------------------------------------------------------

// ReferenceValidation holds the result of validating a reference.py file.
type ReferenceValidation struct {
	HasModelClass  bool
	HasGetInputs   bool
	HasGetInitInputs bool
	DimVars        []string // module-level uppercase integer assignments found
	Errors         []string
	Warnings       []string
}

// validateReferenceStructure checks that a reference.py source has the
// required structural elements before we attempt to inject SHAPE_CONFIGS.
func validateReferenceStructure(src string) ReferenceValidation {
	var v ReferenceValidation

	// Check for class Model
	modelClassRe := regexp.MustCompile(`(?m)^class Model\b`)
	v.HasModelClass = modelClassRe.MatchString(src)
	if !v.HasModelClass {
		v.Errors = append(v.Errors, "missing 'class Model(nn.Module)' definition")
	}

	// Check for get_inputs
	getInputsRe := regexp.MustCompile(`(?m)^def get_inputs\(`)
	v.HasGetInputs = getInputsRe.MatchString(src)
	if !v.HasGetInputs {
		v.Errors = append(v.Errors, "missing 'def get_inputs(...)' function")
	}

	// Check for get_init_inputs
	getInitRe := regexp.MustCompile(`(?m)^def get_init_inputs\(`)
	v.HasGetInitInputs = getInitRe.MatchString(src)
	if !v.HasGetInitInputs {
		v.Warnings = append(v.Warnings, "missing 'def get_init_inputs()' — will default to []")
	}

	// Find module-level dimension variables (UPPER_CASE = integer)
	dimVarRe := regexp.MustCompile(`(?m)^([A-Z][A-Z_0-9]*)\s*=\s*\d+\s*$`)
	for _, m := range dimVarRe.FindAllStringSubmatch(src, -1) {
		v.DimVars = append(v.DimVars, m[1])
	}
	if len(v.DimVars) == 0 {
		v.Warnings = append(v.Warnings, "no module-level dimension variables (e.g. M = 8192) found")
	}

	return v
}

// verifySyntax runs Python's compile() on the source string to check for
// syntax errors. Returns nil if the syntax is valid.
func verifySyntax(src, filename string) error {
	script := fmt.Sprintf(
		`import sys; compile(sys.stdin.read(), %q, "exec")`, filename)
	cmd := exec.Command("python3", "-c", script)
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("syntax check failed for %s: %s", filename, strings.TrimSpace(string(out)))
	}
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

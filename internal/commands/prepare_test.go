package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveKernelName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/path/to/rms_norm_pkg/reference.py", "rms_norm"},
		{"/path/to/rms_norm_pkg/", "rms_norm"},
		{"/path/to/cuda_unpermute_v2_pkg/reference.py", "cuda_unpermute_v2"},
		{"/path/to/swiglu_bf16_pkg/reference.py", "swiglu_bf16"},
		{"/path/to/some_dir/kernel.py", "kernel.py"},
	}
	for _, tt := range tests {
		got := deriveKernelName(tt.path)
		if got != tt.want {
			t.Errorf("deriveKernelName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestLookupShapeEntry(t *testing.T) {
	shapes := map[string]ShapeEntry{
		"rms_norm_forward": {Dims: map[string]int64{"M": 8192, "N": 2048}},
		"rms_norm_backward": {Dims: map[string]int64{"M": 8192, "N": 2048}},
		"topk_to_multihot": {Dims: map[string]int64{"M": 8192, "topk": 8}},
		"unpermute":         {Dims: map[string]int64{"routed_tokens": 65536, "N": 2048}},
		"residual_forward":  {Dims: map[string]int64{"M": 8192, "N": 2048}},
		"permute":           {Dims: map[string]int64{"M": 8192, "N": 2048}},
	}

	tests := []struct {
		name    string
		wantKey string
		wantNil bool
	}{
		{"rms_norm", "rms_norm_forward", false},
		{"cuda_topk_to_multihot", "topk_to_multihot", false},
		{"cuda_unpermute_v2", "unpermute", false},
		{"residual_forward", "residual_forward", false},
		{"cuda_permute_v2", "permute", false},
		{"nonexistent_kernel", "", true},
	}
	for _, tt := range tests {
		entry, key := lookupShapeEntry(shapes, tt.name)
		if tt.wantNil {
			if entry != nil {
				t.Errorf("lookupShapeEntry(%q) expected nil, got entry with key %q", tt.name, key)
			}
			continue
		}
		if entry == nil {
			t.Errorf("lookupShapeEntry(%q) returned nil, want key %q", tt.name, tt.wantKey)
			continue
		}
		if key != tt.wantKey {
			t.Errorf("lookupShapeEntry(%q) matched %q, want %q", tt.name, key, tt.wantKey)
		}
	}
}

func TestLookupShapeEntry_AmbiguousPrefix(t *testing.T) {
	shapes := map[string]ShapeEntry{
		"rms_norm_forward":  {Dims: map[string]int64{"M": 1}},
		"rms_norm_backward": {Dims: map[string]int64{"M": 2}},
	}
	// "rms_norm" with _forward suffix should match _forward first
	entry, key := lookupShapeEntry(shapes, "rms_norm")
	if entry == nil || key != "rms_norm_forward" {
		t.Errorf("expected rms_norm_forward, got key=%q entry=%v", key, entry)
	}
}

func TestExpandShapeConfigs_NoDynamicRange(t *testing.T) {
	entry := ShapeEntry{
		Dims: map[string]int64{"M": 8192, "N": 2048},
	}
	configs := expandShapeConfigs("rms_norm_forward", entry, "welm-30b")
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	if configs[0].Label != "welm-30b" {
		t.Errorf("label = %q, want %q", configs[0].Label, "welm-30b")
	}
	if configs[0].Dims["M"] != 8192 || configs[0].Dims["N"] != 2048 {
		t.Errorf("dims mismatch: %v", configs[0].Dims)
	}
}

func TestExpandShapeConfigs_EmptyWorkloadName(t *testing.T) {
	entry := ShapeEntry{
		Dims: map[string]int64{"M": 100},
	}
	configs := expandShapeConfigs("k", entry, "")
	if configs[0].Label != "default" {
		t.Errorf("label = %q, want %q", configs[0].Label, "default")
	}
}

func TestExpandShapeConfigs_WithDynamicRange(t *testing.T) {
	entry := ShapeEntry{
		Dims: map[string]int64{"routed_tokens": 65536, "N": 2048},
		DynamicRange: map[string]DynamicDimRange{
			"routed_tokens": {Min: 42880, Max: 97152, Theoretical: 65536},
		},
	}
	configs := expandShapeConfigs("unpermute", entry, "welm-30b")

	if len(configs) != 3 {
		t.Fatalf("expected 3 configs (nominal + min + max), got %d", len(configs))
	}

	// Nominal
	if configs[0].Dims["routed_tokens"] != 65536 {
		t.Errorf("nominal routed_tokens = %d, want 65536", configs[0].Dims["routed_tokens"])
	}
	if !strings.Contains(configs[0].Label, "nominal") {
		t.Errorf("nominal label = %q, want containing 'nominal'", configs[0].Label)
	}

	// Check min and max exist (order depends on map iteration)
	foundMin, foundMax := false, false
	for _, c := range configs[1:] {
		if c.Dims["routed_tokens"] == 42880 {
			foundMin = true
			if !strings.Contains(c.Label, "min") {
				t.Errorf("min label = %q, want containing 'min'", c.Label)
			}
		}
		if c.Dims["routed_tokens"] == 97152 {
			foundMax = true
			if !strings.Contains(c.Label, "max") {
				t.Errorf("max label = %q, want containing 'max'", c.Label)
			}
		}
		// Other dims should be unchanged
		if c.Dims["N"] != 2048 {
			t.Errorf("N should remain 2048, got %d", c.Dims["N"])
		}
	}
	if !foundMin {
		t.Error("missing min config")
	}
	if !foundMax {
		t.Error("missing max config")
	}
}

func TestExpandShapeConfigs_DynamicRangeEqualsNominal(t *testing.T) {
	entry := ShapeEntry{
		Dims: map[string]int64{"M": 100},
		DynamicRange: map[string]DynamicDimRange{
			"M": {Min: 100, Max: 100},
		},
	}
	configs := expandShapeConfigs("k", entry, "w")
	// Min == Max == nominal, so only nominal should appear
	if len(configs) != 1 {
		t.Errorf("expected 1 config (min/max == nominal), got %d", len(configs))
	}
}

func TestBuildShapeConfigsPython(t *testing.T) {
	configs := []ShapeConfig{
		{Label: "dense", Dims: map[string]int64{"M": 8192, "N": 2048}},
		{Label: "moe-min", Dims: map[string]int64{"M": 42880, "N": 2048}},
	}
	result := buildShapeConfigsPython(configs)

	if !strings.HasPrefix(result, "SHAPE_CONFIGS = [\n") {
		t.Error("should start with SHAPE_CONFIGS = [")
	}
	if !strings.HasSuffix(result, "]") {
		t.Error("should end with ]")
	}
	if !strings.Contains(result, `"label": "dense"`) {
		t.Error("missing dense label")
	}
	if !strings.Contains(result, `"label": "moe-min"`) {
		t.Error("missing moe-min label")
	}
	// Keys should be sorted: M before N
	mIdx := strings.Index(result, `"M": 8192`)
	nIdx := strings.Index(result, `"N": 2048`)
	if mIdx < 0 || nIdx < 0 || mIdx > nIdx {
		t.Error("dim keys should be sorted alphabetically")
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string]int64{"Z": 1, "A": 2, "M": 3, "B": 4}
	keys := sortedKeys(m)
	want := []string{"A", "B", "M", "Z"}
	if len(keys) != len(want) {
		t.Fatalf("got %d keys, want %d", len(keys), len(want))
	}
	for i, k := range keys {
		if k != want[i] {
			t.Errorf("keys[%d] = %q, want %q", i, k, want[i])
		}
	}
}

func TestEnsureGetInputsShapeIdx(t *testing.T) {
	src := `import torch

M = 8192
N = 2048

def get_inputs():
    x = torch.randn(M, N)
    return [x]
`
	dims := map[string]int64{"M": 8192, "N": 2048}
	result := ensureGetInputsShapeIdx(src, dims)

	if !strings.Contains(result, "def get_inputs(shape_idx=None):") {
		t.Error("should replace get_inputs() with get_inputs(shape_idx=None)")
	}
	if !strings.Contains(result, "SHAPE_CONFIGS[shape_idx]") {
		t.Error("should insert SHAPE_CONFIGS lookup")
	}
	if !strings.Contains(result, `cfg["M"]`) {
		t.Error("should reference cfg[\"M\"]")
	}
	if !strings.Contains(result, `cfg["N"]`) {
		t.Error("should reference cfg[\"N\"]")
	}
}

func TestEnsureGetInputsShapeIdx_AlreadyHasShapeIdx(t *testing.T) {
	src := `def get_inputs(shape_idx=None):
    if shape_idx is not None and 0 <= shape_idx < len(SHAPE_CONFIGS):
        cfg = SHAPE_CONFIGS[shape_idx]
        m = cfg["M"]
    pass
`
	dims := map[string]int64{"M": 1}
	result := ensureGetInputsShapeIdx(src, dims)
	// Should not double-insert since SHAPE_CONFIGS[shape_idx] is already present
	count := strings.Count(result, "SHAPE_CONFIGS[shape_idx]")
	if count > 1 {
		t.Errorf("SHAPE_CONFIGS[shape_idx] appears %d times, expected 1 (no double-insert)", count)
	}
}

func TestApplyMultiShapeOverrides(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "reference.py")
	dstFile := filepath.Join(dir, "reference_out.py")

	src := `import torch

M = 4096
N = 7168

def get_inputs():
    x = torch.randn(M, N)
    return [x]

def get_init_inputs():
    return []
`
	if err := os.WriteFile(srcFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	dims := map[string]int64{"M": 8192, "N": 2048}
	configs := []ShapeConfig{
		{Label: "dense", Dims: map[string]int64{"M": 8192, "N": 2048}},
		{Label: "small", Dims: map[string]int64{"M": 1024, "N": 512}},
	}

	if err := applyMultiShapeOverrides(srcFile, dstFile, dims, configs, false); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatal(err)
	}
	result := string(content)

	if !strings.Contains(result, "M = 8192") {
		t.Error("M should be overridden to 8192")
	}
	if !strings.Contains(result, "N = 2048") {
		t.Error("N should be overridden to 2048")
	}
	if !strings.Contains(result, "SHAPE_CONFIGS = [") {
		t.Error("should contain SHAPE_CONFIGS")
	}
	if !strings.Contains(result, `"label": "dense"`) {
		t.Error("should contain dense label")
	}
	if !strings.Contains(result, `"label": "small"`) {
		t.Error("should contain small label")
	}
	if !strings.Contains(result, "def get_inputs(shape_idx=None):") {
		t.Error("should have shape_idx parameter")
	}
	if strings.Contains(result, "M = 4096") {
		t.Error("old M = 4096 should be replaced")
	}
	if strings.Contains(result, "N = 7168") {
		t.Error("old N = 7168 should be replaced")
	}
}

func TestApplyMultiShapeOverrides_DryRun(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "reference.py")
	dstFile := filepath.Join(dir, "reference_out.py")

	src := "M = 100\ndef get_inputs():\n    return []\n"
	if err := os.WriteFile(srcFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	dims := map[string]int64{"M": 200}
	configs := []ShapeConfig{{Label: "test", Dims: dims}}
	if err := applyMultiShapeOverrides(srcFile, dstFile, dims, configs, true); err != nil {
		t.Fatal(err)
	}

	// Dry-run should not create dstFile
	if _, err := os.Stat(dstFile); err == nil {
		t.Error("dry-run should not write file")
	}
}

func TestLoadWorkloadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workload.json")

	cfg := map[string]interface{}{
		"name": "test-workload",
		"shapes": map[string]interface{}{
			"rms_norm_forward": map[string]interface{}{
				"category": "dense",
				"dims":     map[string]interface{}{"M": 8192, "N": 2048},
			},
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	wlCfg, err := loadWorkloadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if wlCfg.Name != "test-workload" {
		t.Errorf("name = %q, want %q", wlCfg.Name, "test-workload")
	}
	entry, ok := wlCfg.Shapes["rms_norm_forward"]
	if !ok {
		t.Fatal("missing rms_norm_forward shape")
	}
	if entry.Dims["M"] != 8192 {
		t.Errorf("M = %d, want 8192", entry.Dims["M"])
	}
}

func TestLoadWorkloadConfig_MissingName(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workload.json")

	data := []byte(`{"shapes": {"k": {"dims": {"M": 1}}}}`)
	os.WriteFile(cfgPath, data, 0o644)

	_, err := loadWorkloadConfig(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("expected error about missing name, got %v", err)
	}
}

func TestLoadWorkloadConfig_EmptyDims(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workload.json")

	data := []byte(`{"name": "x", "shapes": {"k": {"dims": {}}}}`)
	os.WriteFile(cfgPath, data, 0o644)

	_, err := loadWorkloadConfig(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "no dims") {
		t.Errorf("expected error about empty dims, got %v", err)
	}
}

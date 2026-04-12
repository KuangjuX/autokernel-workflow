package commands

import (
	"math"
	"testing"
)

func TestParseCommitBodyFields(t *testing.T) {
	body := `kernel: rms_norm_forward
agent: cursor-agent
gpu: B200
backend: triton
correctness: PASS
speedup_vs_baseline: 1.23x
latency_us: 45.6
changes: Changed block size from 64 to 128.
  Also adjusted num_warps.
analysis: Better occupancy due to
  reduced register pressure.`

	fields := parseCommitBodyFields(body)

	checks := map[string]string{
		"kernel":              "rms_norm_forward",
		"agent":               "cursor-agent",
		"gpu":                 "B200",
		"backend":             "triton",
		"correctness":         "PASS",
		"speedup_vs_baseline": "1.23x",
		"latency_us":          "45.6",
	}
	for key, want := range checks {
		got := fields[key]
		if got != want {
			t.Errorf("fields[%q] = %q, want %q", key, got, want)
		}
	}

	if changes := fields["changes"]; changes == "" {
		t.Error("changes should not be empty")
	} else if len(changes) < 20 {
		t.Errorf("changes too short, multiline content lost: %q", changes)
	}

	if analysis := fields["analysis"]; analysis == "" {
		t.Error("analysis should not be empty")
	} else if len(analysis) < 20 {
		t.Errorf("analysis too short, multiline content lost: %q", analysis)
	}
}

func TestParseCommitBodyFields_Empty(t *testing.T) {
	fields := parseCommitBodyFields("")
	if len(fields) != 0 {
		t.Errorf("expected empty map, got %v", fields)
	}
}

func TestParseCommitBodyFields_CRLFLineEndings(t *testing.T) {
	body := "kernel: gemm\r\nagent: test\r\n"
	fields := parseCommitBodyFields(body)
	if fields["kernel"] != "gemm" {
		t.Errorf("kernel = %q, want %q", fields["kernel"], "gemm")
	}
	if fields["agent"] != "test" {
		t.Errorf("agent = %q, want %q", fields["agent"], "test")
	}
}

func TestNormalizeCommitFieldKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"kernel", "kernel"},
		{"Kernel", "kernel"},
		{"speedup_vs_baseline", "speedup_vs_baseline"},
		{"Speedup vs Baseline", "speedup_vs_baseline"},
		{"GPU-Model", "gpu_model"},
		{"  latency_us  ", "latency_us"},
		{"", ""},
		{"123", "123"},
		{"a--b", "a_b"},
	}
	for _, tt := range tests {
		got := normalizeCommitFieldKey(tt.input)
		if got != tt.want {
			t.Errorf("normalizeCommitFieldKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseIteration(t *testing.T) {
	tests := []struct {
		subject  string
		fallback int
		want     int
	}{
		{"exp 7: increase block_k to 128", 0, 7},
		{"exp 12: something", 0, 12},
		{"EXP 3: uppercase", 0, 3},
		{"[baseline] Initialize", 5, 5},
		{"[iter 3] Fix num_sms", 0, 0},
		{"no exp here", 99, 99},
		{"", 0, 0},
	}
	for _, tt := range tests {
		got := parseIteration(tt.subject, tt.fallback)
		if got != tt.want {
			t.Errorf("parseIteration(%q, %d) = %d, want %d", tt.subject, tt.fallback, got, tt.want)
		}
	}
}

func TestParseHypothesis(t *testing.T) {
	tests := []struct {
		subject string
		want    string
	}{
		{"exp 7: increase block_k to 128", "increase block_k to 128"},
		{"[baseline] Initialize solution", "[baseline] Initialize solution"},
		{"no colon here", "no colon here"},
		{"", ""},
	}
	for _, tt := range tests {
		got := parseHypothesis(tt.subject)
		if got != tt.want {
			t.Errorf("parseHypothesis(%q) = %q, want %q", tt.subject, got, tt.want)
		}
	}
}

func TestParseFloat(t *testing.T) {
	tests := []struct {
		input   string
		wantVal float64
		wantOK  bool
	}{
		{"1.23x", 1.23, true},
		{"1.23X", 1.23, true},
		{"45.6", 45.6, true},
		{"2.0x", 2.0, true},
		{"", 0, false},
		{"abc", 0, false},
		{"0", 0, true},
	}
	for _, tt := range tests {
		v, ok := parseFloat(tt.input)
		if ok != tt.wantOK {
			t.Errorf("parseFloat(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			continue
		}
		if ok && math.Abs(v-tt.wantVal) > 1e-9 {
			t.Errorf("parseFloat(%q) = %f, want %f", tt.input, v, tt.wantVal)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		vals []string
		want string
	}{
		{[]string{"", "  ", "hello"}, "hello"},
		{[]string{"first", "second"}, "first"},
		{[]string{"", ""}, ""},
		{[]string{"  spaces  "}, "spaces"},
	}
	for _, tt := range tests {
		got := firstNonEmpty(tt.vals...)
		if got != tt.want {
			t.Errorf("firstNonEmpty(%v) = %q, want %q", tt.vals, got, tt.want)
		}
	}
}

func TestValidateHistoryIntegrity_CleanChain(t *testing.T) {
	records := []IterationRecord{
		{CommitHash: "aaa", ParentCommitHash: ""},
		{CommitHash: "bbb", ParentCommitHash: "aaa"},
		{CommitHash: "ccc", ParentCommitHash: "bbb"},
	}
	warnings := validateHistoryIntegrity(records)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestValidateHistoryIntegrity_BrokenChain(t *testing.T) {
	records := []IterationRecord{
		{CommitHash: "aaa", ParentCommitHash: ""},
		{CommitHash: "bbb", ParentCommitHash: "aaa"},
		{CommitHash: "ccc", ParentCommitHash: "xxx"},
	}
	warnings := validateHistoryIntegrity(records)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if len(warnings[0]) == 0 {
		t.Error("warning should not be empty")
	}
}

func TestValidateHistoryIntegrity_SingleCommit(t *testing.T) {
	records := []IterationRecord{
		{CommitHash: "aaa"},
	}
	warnings := validateHistoryIntegrity(records)
	if len(warnings) != 0 {
		t.Errorf("single commit should have no warnings, got %v", warnings)
	}
}

func TestExtractField(t *testing.T) {
	body := "kernel: gemm_bf16\nagent: cursor\ngpu: H800\n"
	got := extractField(`(?m)^kernel:\s*(.+)$`, body)
	if got != "gemm_bf16" {
		t.Errorf("extractField for kernel = %q, want %q", got, "gemm_bf16")
	}

	got = extractField(`(?m)^nonexistent:\s*(.+)$`, body)
	if got != "" {
		t.Errorf("extractField for nonexistent = %q, want empty", got)
	}
}

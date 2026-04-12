package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAdapterBin(t *testing.T) {
	bin, err := resolveAdapterBin()
	if err != nil {
		t.Skipf("kernel-adapter not installed, skipping: %v", err)
	}
	if bin == "" {
		t.Error("resolveAdapterBin returned empty string")
	}
}

func TestInferKernelNameFromPatchMeta(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "metadata.json")

	os.WriteFile(metaPath, []byte(`{"kernel_name": "rms_norm", "backend": "triton"}`), 0o644)
	got := inferKernelNameFromPatchMeta(metaPath, "fallback")
	if got != "rms_norm" {
		t.Errorf("got %q, want %q", got, "rms_norm")
	}
}

func TestInferKernelNameFromPatchMeta_Missing(t *testing.T) {
	got := inferKernelNameFromPatchMeta("/nonexistent/metadata.json", "my-fallback")
	if got != "my-fallback" {
		t.Errorf("got %q, want %q", got, "my-fallback")
	}
}

func TestInferKernelNameFromPatchMeta_EmptyName(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "metadata.json")

	os.WriteFile(metaPath, []byte(`{"kernel_name": "", "backend": "triton"}`), 0o644)
	got := inferKernelNameFromPatchMeta(metaPath, "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

func TestPatch_MissingDBPath(t *testing.T) {
	err := Patch(PatchOptions{
		KernelAssets: "/tmp",
		MMQRoot:      "/tmp",
	})
	if err == nil {
		t.Error("expected error for missing db-path")
	}
}

func TestPatch_MissingKernelAssets(t *testing.T) {
	err := Patch(PatchOptions{
		DBPath:  "/tmp/test.db",
		MMQRoot: "/tmp",
	})
	if err == nil {
		t.Error("expected error for missing kernel-assets")
	}
}

func TestPatch_MissingMMQRoot(t *testing.T) {
	err := Patch(PatchOptions{
		DBPath:       "/tmp/test.db",
		KernelAssets: "/tmp",
	})
	if err == nil {
		t.Error("expected error for missing mmq-root")
	}
}

func TestPatch_DryRun(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	assetsDir := filepath.Join(dir, "assets")
	mmqDir := filepath.Join(dir, "mmq")

	os.MkdirAll(assetsDir, 0o755)
	os.MkdirAll(mmqDir, 0o755)

	db, err := openHistoryDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	err = Patch(PatchOptions{
		DBPath:       dbPath,
		KernelAssets: assetsDir,
		MMQRoot:      mmqDir,
		DryRun:       true,
	})
	if err != nil {
		t.Errorf("dry-run should succeed: %v", err)
	}
}

func TestResolveGitToplevel(t *testing.T) {
	// Should work on the repo itself
	top := resolveGitToplevel("/home/chengqi/autokernel-workflow")
	if top == "" {
		t.Skip("not running in a git repo")
	}
	if !filepath.IsAbs(top) {
		t.Errorf("expected absolute path, got %q", top)
	}
}

func TestVerifyPatches_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	summary := verifyPatches(dir, "/tmp", "")
	if summary.Verified != 0 || summary.Failed != 0 {
		t.Errorf("empty dir should have zero results: %+v", summary)
	}
}

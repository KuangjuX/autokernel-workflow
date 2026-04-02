package commands

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const bundleFormat = "git-bundle+gzip+base64"

type ArchiveGitOptions struct {
	RepoPath string
	Branch   string
	DBPath   string
	RunID    string
	Note     string
	DryRun   bool
}

type GitArchiveRecord struct {
	ID              string `json:"id"`
	RunID           string `json:"run_id,omitempty"`
	Branch          string `json:"branch"`
	RepoPath        string `json:"repo_path"`
	HeadCommit      string `json:"head_commit"`
	CreatedAt       string `json:"created_at"`
	Note            string `json:"note,omitempty"`
	BundleFormat    string `json:"bundle_format"`
	BundleSHA256    string `json:"bundle_sha256"`
	BundleSizeBytes int    `json:"bundle_size_bytes"`
	BundleData      string `json:"bundle_data"`
}

func ArchiveGit(opts ArchiveGitOptions) error {
	if strings.TrimSpace(opts.Branch) == "" {
		return errors.New("--branch is required")
	}
	if strings.TrimSpace(opts.RepoPath) == "" {
		return errors.New("--repo-path cannot be empty")
	}
	if strings.TrimSpace(opts.DBPath) == "" {
		return errors.New("--db-path cannot be empty")
	}

	repoAbs, err := filepath.Abs(opts.RepoPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repoAbs, ".git")); err != nil {
		return fmt.Errorf("repo path is not a git repo: %w", err)
	}

	headCommit, err := runGitOutput(repoAbs, "rev-parse", opts.Branch)
	if err != nil {
		return fmt.Errorf("resolve branch head failed: %w", err)
	}
	headCommit = strings.TrimSpace(headCommit)

	bundlePath, cleanup, err := createBundle(repoAbs, opts.Branch)
	if err != nil {
		return err
	}
	defer cleanup()

	bundleRaw, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	if len(bundleRaw) == 0 {
		return errors.New("generated bundle is empty")
	}

	bundleEncoded, err := encodeBundlePayload(bundleRaw)
	if err != nil {
		return err
	}
	bundleSHA := fmt.Sprintf("%x", sha256.Sum256(bundleRaw))

	runID := strings.TrimSpace(opts.RunID)
	if runID == "" {
		runID = inferRunIDFromBranch(opts.Branch)
	}

	record := GitArchiveRecord{
		ID:              fmt.Sprintf("arc-%s-%s", time.Now().UTC().Format("20060102-150405"), shortHash(headCommit, 12)),
		RunID:           runID,
		Branch:          opts.Branch,
		RepoPath:        repoAbs,
		HeadCommit:      headCommit,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		Note:            strings.TrimSpace(opts.Note),
		BundleFormat:    bundleFormat,
		BundleSHA256:    bundleSHA,
		BundleSizeBytes: len(bundleRaw),
		BundleData:      bundleEncoded,
	}

	if opts.DryRun {
		fmt.Printf(
			"[kernelhub archive-git] dry-run branch=%s head=%s bundle_size=%d run_id=%s\n",
			record.Branch,
			shortHash(record.HeadCommit, 12),
			record.BundleSizeBytes,
			record.RunID,
		)
		return nil
	}

	if err := appendArchive(opts.DBPath, record); err != nil {
		return err
	}

	fmt.Printf(
		"[kernelhub archive-git] archived id=%s branch=%s head=%s size=%d -> %s\n",
		record.ID,
		record.Branch,
		shortHash(record.HeadCommit, 12),
		record.BundleSizeBytes,
		opts.DBPath,
	)
	return nil
}

func createBundle(repoPath, branch string) (string, func(), error) {
	tmp, err := os.CreateTemp("", "kernelhub-archive-*.bundle")
	if err != nil {
		return "", nil, err
	}
	bundlePath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(bundlePath)
		return "", nil, err
	}
	cleanup := func() {
		_ = os.Remove(bundlePath)
	}
	if err := runGit(repoPath, "bundle", "create", bundlePath, branch); err != nil {
		cleanup()
		return "", nil, err
	}
	return bundlePath, cleanup, nil
}

func encodeBundlePayload(bundle []byte) (string, error) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(bundle); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(compressed.Bytes()), nil
}

func decodeBundlePayload(encoded string) ([]byte, error) {
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return ioReadAll(zr)
}

func inferRunIDFromBranch(branch string) string {
	ref := strings.TrimSpace(branch)
	ref = strings.TrimPrefix(ref, "refs/heads/")
	if strings.HasPrefix(ref, "agent/") {
		return strings.TrimPrefix(ref, "agent/")
	}
	if strings.HasPrefix(ref, "run-") {
		return ref
	}
	return ""
}

func runGit(repoPath string, args ...string) error {
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "no stderr output"
		}
		return fmt.Errorf("git %s failed: %w (%s)", strings.Join(args, " "), err, msg)
	}
	return nil
}

func runGitOutput(repoPath string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func shortHash(hash string, width int) string {
	if width <= 0 || len(hash) <= width {
		return hash
	}
	return hash[:width]
}

func ioReadAll(r *gzip.Reader) ([]byte, error) {
	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

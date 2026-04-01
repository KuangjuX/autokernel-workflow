package commands

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type RestoreGitOptions struct {
	DBPath     string
	RunID      string
	ArchiveID  string
	OutRepoPath string
	Checkout   string
	DryRun     bool
}

func RestoreGit(opts RestoreGitOptions) error {
	if strings.TrimSpace(opts.DBPath) == "" {
		return errors.New("--db-path cannot be empty")
	}
	if strings.TrimSpace(opts.OutRepoPath) == "" {
		return errors.New("--out-repo cannot be empty")
	}

	history, err := loadHistory(opts.DBPath)
	if err != nil {
		return err
	}
	if len(history.Archives) == 0 {
		return errors.New("no git archives found in db; run archive-git first")
	}

	record, err := selectArchive(history.Archives, strings.TrimSpace(opts.ArchiveID), strings.TrimSpace(opts.RunID))
	if err != nil {
		return err
	}

	bundleRaw, err := decodeBundlePayload(record.BundleData)
	if err != nil {
		return fmt.Errorf("decode archive bundle failed: %w", err)
	}
	bundleSHA := fmt.Sprintf("%x", sha256.Sum256(bundleRaw))
	if record.BundleSHA256 != "" && !strings.EqualFold(record.BundleSHA256, bundleSHA) {
		return fmt.Errorf("bundle sha256 mismatch: expect=%s got=%s", record.BundleSHA256, bundleSHA)
	}

	if opts.DryRun {
		fmt.Printf(
			"[kernelhub restore-git] dry-run archive_id=%s run_id=%s branch=%s head=%s size=%d\n",
			record.ID,
			record.RunID,
			record.Branch,
			shortHash(record.HeadCommit, 12),
			len(bundleRaw),
		)
		return nil
	}

	outAbs, err := filepath.Abs(opts.OutRepoPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outAbs, 0o755); err != nil {
		return err
	}
	if err := ensureGitRepo(outAbs); err != nil {
		return err
	}

	bundlePath, cleanup, err := writeTempBundle(bundleRaw)
	if err != nil {
		return err
	}
	defer cleanup()

	srcRef := normalizeRef(record.Branch)
	dstBranch := buildRestoreBranch(record)
	dstRef := "refs/heads/" + dstBranch
	fetchRefspec := fmt.Sprintf("%s:%s", srcRef, dstRef)

	if err := runGit(outAbs, "fetch", "--force", bundlePath, fetchRefspec); err != nil {
		fallbackRefspec := fmt.Sprintf("%s:%s", record.HeadCommit, dstRef)
		if record.HeadCommit == "" {
			return err
		}
		if err2 := runGit(outAbs, "fetch", "--force", bundlePath, fallbackRefspec); err2 != nil {
			return fmt.Errorf("restore fetch failed by branch and head: %v | %v", err, err2)
		}
	}

	target := strings.TrimSpace(opts.Checkout)
	if target == "" {
		target = record.HeadCommit
	}
	if target == "" {
		target = dstBranch
	}
	if err := runGit(outAbs, "checkout", "--force", target); err != nil {
		return err
	}

	fmt.Printf(
		"[kernelhub restore-git] restored archive_id=%s branch=%s head=%s -> %s\n",
		record.ID,
		record.Branch,
		shortHash(record.HeadCommit, 12),
		outAbs,
	)
	return nil
}

func selectArchive(archives []GitArchiveRecord, archiveID, runID string) (GitArchiveRecord, error) {
	if archiveID != "" {
		for _, item := range archives {
			if item.ID == archiveID {
				return item, nil
			}
		}
		return GitArchiveRecord{}, fmt.Errorf("archive id not found: %s", archiveID)
	}

	for i := len(archives) - 1; i >= 0; i-- {
		item := archives[i]
		if runID == "" || item.RunID == runID {
			return item, nil
		}
	}

	return GitArchiveRecord{}, fmt.Errorf("no archive found for run_id=%s", runID)
}

func ensureGitRepo(path string) error {
	_, err := os.Stat(filepath.Join(path, ".git"))
	if errors.Is(err, os.ErrNotExist) {
		return runGit(path, "init")
	}
	return err
}

func writeTempBundle(payload []byte) (string, func(), error) {
	tmp, err := os.CreateTemp("", "kernelhub-restore-*.bundle")
	if err != nil {
		return "", nil, err
	}
	path := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		_ = os.Remove(path)
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return "", nil, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func normalizeRef(branch string) string {
	ref := strings.TrimSpace(branch)
	if strings.HasPrefix(ref, "refs/") {
		return ref
	}
	return "refs/heads/" + ref
}

func buildRestoreBranch(record GitArchiveRecord) string {
	base := strings.TrimSpace(record.RunID)
	if base == "" {
		base = strings.TrimSpace(record.Branch)
	}
	if base == "" {
		base = shortHash(record.HeadCommit, 12)
	}
	base = strings.TrimPrefix(base, "refs/heads/")
	base = sanitizeRef(base)
	return "restored/" + base
}

func sanitizeRef(input string) string {
	if input == "" {
		return "archive"
	}
	var b strings.Builder
	for _, r := range input {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '/', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "/.-")
	if out == "" {
		return "archive"
	}
	return out
}

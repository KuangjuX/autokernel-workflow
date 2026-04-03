package commands

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const sqliteMagicHeader = "SQLite format 3\x00"

func appendRun(path string, run RunRecord) error {
	db, err := openHistoryDB(path)
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertRunTx(tx, run); err != nil {
		return err
	}
	if err := setMetaTx(tx, "generated_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

func appendArchive(path string, record GitArchiveRecord) error {
	db, err := openHistoryDB(path)
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertArchiveTx(tx, record); err != nil {
		return err
	}
	if err := setMetaTx(tx, "generated_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

func loadHistory(path string) (HistoryFile, error) {
	db, err := openHistoryDB(path)
	if err != nil {
		return HistoryFile{}, err
	}
	defer db.Close()

	history := HistoryFile{}
	if generatedAt, err := getMeta(db, "generated_at"); err == nil {
		history.GeneratedAt = generatedAt
	}

	runs, err := queryRuns(db)
	if err != nil {
		return HistoryFile{}, err
	}
	history.Runs = runs

	archives, err := queryArchives(db)
	if err != nil {
		return HistoryFile{}, err
	}
	history.Archives = archives
	return history, nil
}

// OpenHistoryDB is an exported wrapper for server package reuse.
func OpenHistoryDB(path string) (*sql.DB, error) {
	return openHistoryDB(path)
}

func openHistoryDB(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("history path cannot be empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, err
	}
	if err := prepareHistoryDBFile(absPath); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", absPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := initHistorySchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureHistorySchemaCompat(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func initHistorySchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			branch TEXT NOT NULL,
			repo_path TEXT NOT NULL,
			synced_at TEXT NOT NULL,
			commit_count INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS iterations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_row_id INTEGER NOT NULL,
			iteration INTEGER NOT NULL,
			commit_hash TEXT NOT NULL,
			parent_commit_hash TEXT NOT NULL DEFAULT '',
			commit_time TEXT NOT NULL,
			subject TEXT NOT NULL,
			hypothesis TEXT NOT NULL,
			changes TEXT NOT NULL DEFAULT '',
			analysis TEXT NOT NULL DEFAULT '',
			kernel TEXT NOT NULL DEFAULT '',
			agent TEXT NOT NULL DEFAULT '',
			gpu TEXT NOT NULL DEFAULT '',
			backend TEXT NOT NULL DEFAULT '',
			correctness TEXT NOT NULL DEFAULT '',
			speedup_vs_baseline REAL,
			latency_us REAL,
			patch TEXT NOT NULL DEFAULT '',
			patch_error TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(run_row_id) REFERENCES runs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_iterations_run_row_id_id ON iterations(run_row_id, id)`,
		`CREATE TABLE IF NOT EXISTS archives (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL DEFAULT '',
			branch TEXT NOT NULL,
			repo_path TEXT NOT NULL,
			head_commit TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			bundle_format TEXT NOT NULL,
			bundle_sha256 TEXT NOT NULL,
			bundle_size_bytes INTEGER NOT NULL,
			bundle_data TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_archives_run_created ON archives(run_id, created_at)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureHistorySchemaCompat(db *sql.DB) error {
	iterationMigrations := []struct {
		column  string
		ddlType string
	}{
		{column: "changes", ddlType: "TEXT NOT NULL DEFAULT ''"},
		{column: "analysis", ddlType: "TEXT NOT NULL DEFAULT ''"},
		{column: "gpu", ddlType: "TEXT NOT NULL DEFAULT ''"},
		{column: "backend", ddlType: "TEXT NOT NULL DEFAULT ''"},
	}
	for _, migration := range iterationMigrations {
		has, err := tableHasColumn(db, "iterations", migration.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		stmt := fmt.Sprintf(
			`ALTER TABLE iterations ADD COLUMN %s %s`,
			migration.column,
			migration.ddlType,
		)
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func tableHasColumn(db *sql.DB, tableName, columnName string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, tableName))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var colType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, columnName) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func prepareHistoryDBFile(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}

	header, err := readFilePrefix(path, len(sqliteMagicHeader))
	if err != nil {
		return err
	}
	if string(header) == sqliteMagicHeader {
		return nil
	}

	legacy, err := loadLegacyHistoryJSON(path)
	if err != nil {
		return fmt.Errorf("history file is neither sqlite nor legacy json: %w", err)
	}
	return migrateLegacyJSONToSQLite(path, legacy)
}

func readFilePrefix(path string, maxBytes int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, maxBytes)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:n], nil
}

func loadLegacyHistoryJSON(path string) (HistoryFile, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return HistoryFile{}, err
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return HistoryFile{}, nil
	}

	var data HistoryFile
	if err := json.Unmarshal(content, &data); err != nil {
		return HistoryFile{}, err
	}
	return data, nil
}

func migrateLegacyJSONToSQLite(path string, legacy HistoryFile) error {
	tmpPath := path + ".migrate.tmp"
	_ = os.Remove(tmpPath)

	db, err := sql.Open("sqlite3", tmpPath)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	closeWithErr := func(inErr error) error {
		_ = db.Close()
		_ = os.Remove(tmpPath)
		return inErr
	}

	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return closeWithErr(err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return closeWithErr(err)
	}
	if err := initHistorySchema(db); err != nil {
		return closeWithErr(err)
	}

	tx, err := db.Begin()
	if err != nil {
		return closeWithErr(err)
	}

	rollbackWithErr := func(inErr error) error {
		_ = tx.Rollback()
		return closeWithErr(inErr)
	}

	if strings.TrimSpace(legacy.GeneratedAt) != "" {
		if err := setMetaTx(tx, "generated_at", strings.TrimSpace(legacy.GeneratedAt)); err != nil {
			return rollbackWithErr(err)
		}
	}

	for _, run := range legacy.Runs {
		if err := insertRunTx(tx, run); err != nil {
			return rollbackWithErr(err)
		}
	}
	for _, archive := range legacy.Archives {
		if err := insertArchiveTx(tx, archive); err != nil {
			return rollbackWithErr(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return closeWithErr(err)
	}
	if err := db.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	backupPath, err := chooseBackupPath(path + ".json.bak")
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(path, backupPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func chooseBackupPath(base string) (string, error) {
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return base, nil
	} else if err != nil {
		return "", err
	}

	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext)
	for i := 1; i <= 9999; i++ {
		candidate := fmt.Sprintf("%s-%d%s", prefix, i, ext)
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("cannot allocate backup path for %s", base)
}

func insertRunTx(tx *sql.Tx, run RunRecord) error {
	res, err := tx.Exec(
		`INSERT INTO runs (run_id, branch, repo_path, synced_at, commit_count)
		 VALUES (?, ?, ?, ?, ?)`,
		run.RunID,
		run.Branch,
		run.RepoPath,
		run.SyncedAt,
		run.CommitCount,
	)
	if err != nil {
		return err
	}

	runRowID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	for _, it := range run.Iterations {
		var speedup any
		if it.HasSpeedup || it.SpeedupVsBaseline != 0 {
			speedup = it.SpeedupVsBaseline
		}

		var latency any
		if it.HasLatency || it.LatencyUs != 0 {
			latency = it.LatencyUs
		}

		if _, err := tx.Exec(
			`INSERT INTO iterations (
				run_row_id, iteration, commit_hash, parent_commit_hash, commit_time,
				subject, hypothesis, changes, analysis, kernel, agent, gpu, backend,
				correctness, speedup_vs_baseline, latency_us, patch, patch_error
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runRowID,
			it.Iteration,
			it.CommitHash,
			it.ParentCommitHash,
			it.CommitTime,
			it.Subject,
			it.Hypothesis,
			it.Changes,
			it.Analysis,
			it.Kernel,
			it.Agent,
			it.GPU,
			it.Backend,
			it.Correctness,
			speedup,
			latency,
			it.Patch,
			it.PatchError,
		); err != nil {
			return err
		}
	}
	return nil
}

func insertArchiveTx(tx *sql.Tx, record GitArchiveRecord) error {
	_, err := tx.Exec(
		`INSERT INTO archives (
			id, run_id, branch, repo_path, head_commit, created_at, note,
			bundle_format, bundle_sha256, bundle_size_bytes, bundle_data
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.RunID,
		record.Branch,
		record.RepoPath,
		record.HeadCommit,
		record.CreatedAt,
		record.Note,
		record.BundleFormat,
		record.BundleSHA256,
		record.BundleSizeBytes,
		record.BundleData,
	)
	return err
}

func setMetaTx(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key,
		value,
	)
	return err
}

func getMeta(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func queryRuns(db *sql.DB) ([]RunRecord, error) {
	rows, err := db.Query(
		`SELECT id, run_id, branch, repo_path, synced_at, commit_count
		 FROM runs
		 ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	type runRow struct {
		rowID int64
		run   RunRecord
	}
	var pending []runRow
	for rows.Next() {
		item := runRow{}
		if err := rows.Scan(
			&item.rowID,
			&item.run.RunID,
			&item.run.Branch,
			&item.run.RepoPath,
			&item.run.SyncedAt,
			&item.run.CommitCount,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	out := make([]RunRecord, 0, len(pending))
	for _, item := range pending {
		iterations, err := queryIterationsForRun(db, item.rowID)
		if err != nil {
			return nil, err
		}
		item.run.Iterations = iterations
		out = append(out, item.run)
	}
	return out, nil
}

func queryIterationsForRun(db *sql.DB, runRowID int64) ([]IterationRecord, error) {
	rows, err := db.Query(
		`SELECT
			iteration, commit_hash, parent_commit_hash, commit_time, subject, hypothesis,
			changes, analysis, kernel, agent, gpu, backend, correctness,
			speedup_vs_baseline, latency_us, patch, patch_error
		 FROM iterations
		 WHERE run_row_id = ?
		 ORDER BY id`,
		runRowID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []IterationRecord
	for rows.Next() {
		var it IterationRecord
		var speedup sql.NullFloat64
		var latency sql.NullFloat64
		if err := rows.Scan(
			&it.Iteration,
			&it.CommitHash,
			&it.ParentCommitHash,
			&it.CommitTime,
			&it.Subject,
			&it.Hypothesis,
			&it.Changes,
			&it.Analysis,
			&it.Kernel,
			&it.Agent,
			&it.GPU,
			&it.Backend,
			&it.Correctness,
			&speedup,
			&latency,
			&it.Patch,
			&it.PatchError,
		); err != nil {
			return nil, err
		}
		if speedup.Valid {
			it.SpeedupVsBaseline = speedup.Float64
			it.HasSpeedup = true
		}
		if latency.Valid {
			it.LatencyUs = latency.Float64
			it.HasLatency = true
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func queryArchives(db *sql.DB) ([]GitArchiveRecord, error) {
	rows, err := db.Query(
		`SELECT
			id, run_id, branch, repo_path, head_commit, created_at, note,
			bundle_format, bundle_sha256, bundle_size_bytes, bundle_data
		 FROM archives
		 ORDER BY rowid`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GitArchiveRecord
	for rows.Next() {
		var item GitArchiveRecord
		if err := rows.Scan(
			&item.ID,
			&item.RunID,
			&item.Branch,
			&item.RepoPath,
			&item.HeadCommit,
			&item.CreatedAt,
			&item.Note,
			&item.BundleFormat,
			&item.BundleSHA256,
			&item.BundleSizeBytes,
			&item.BundleData,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

package server

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kernelhub/internal/commands"
)

const (
	maxIngestBodyBytes = 128 << 20 // 128 MiB
	idempotencyHeader  = "Idempotency-Key"
)

type iterationIngestRequest struct {
	RunID     string                   `json:"run_id"`
	Iteration commands.IterationRecord `json:"iteration"`
}

type ingestSuccessResponse struct {
	Resource string `json:"resource"`
	ID       string `json:"id"`
	Status   string `json:"status"`
}

type ingestAPIError struct {
	status  int
	code    string
	message string
}

func (e *ingestAPIError) Error() string {
	return e.message
}

type idempotencyRecord struct {
	RequestSHA256 string
	StatusCode    int
	ResponseBody  string
}

func (s *Server) handleRunsIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorCode(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get(idempotencyHeader))
	if idempotencyKey == "" {
		writeErrorCode(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}
	raw, err := readIngestBody(r)
	if err != nil {
		writeIngestError(w, err)
		return
	}

	var payload commands.RunRecord
	if err := decodeJSONStrict(raw, &payload); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := normalizeRunPayload(&payload); err != nil {
		writeIngestError(w, err)
		return
	}

	status, responseBody, replayed, err := s.withIdempotentWrite(
		r.URL.Path,
		idempotencyKey,
		raw,
		func(tx *sql.Tx) (int, any, error) {
			if _, err := insertRunRecordTx(tx, payload); err != nil {
				return 0, nil, err
			}
			if err := touchGeneratedAtTx(tx); err != nil {
				return 0, nil, err
			}
			return http.StatusCreated, ingestSuccessResponse{
				Resource: "run",
				ID:       payload.RunID,
				Status:   "created",
			}, nil
		},
	)
	if err != nil {
		writeIngestError(w, err)
		return
	}
	if replayed {
		w.Header().Set("X-Idempotent-Replay", "true")
	}
	writeJSONBytes(w, status, responseBody)
}

func (s *Server) handleIterationsIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorCode(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get(idempotencyHeader))
	if idempotencyKey == "" {
		writeErrorCode(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}
	raw, err := readIngestBody(r)
	if err != nil {
		writeIngestError(w, err)
		return
	}

	var payload iterationIngestRequest
	if err := decodeJSONStrict(raw, &payload); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := normalizeIterationPayload(&payload); err != nil {
		writeIngestError(w, err)
		return
	}

	status, responseBody, replayed, err := s.withIdempotentWrite(
		r.URL.Path,
		idempotencyKey,
		raw,
		func(tx *sql.Tx) (int, any, error) {
			if err := insertIterationForRunTx(tx, payload.RunID, payload.Iteration); err != nil {
				return 0, nil, err
			}
			if err := touchGeneratedAtTx(tx); err != nil {
				return 0, nil, err
			}
			return http.StatusCreated, ingestSuccessResponse{
				Resource: "iteration",
				ID:       fmt.Sprintf("%s:%d", payload.RunID, payload.Iteration.Iteration),
				Status:   "created",
			}, nil
		},
	)
	if err != nil {
		writeIngestError(w, err)
		return
	}
	if replayed {
		w.Header().Set("X-Idempotent-Replay", "true")
	}
	writeJSONBytes(w, status, responseBody)
}

func (s *Server) handleArchivesIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorCode(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get(idempotencyHeader))
	if idempotencyKey == "" {
		writeErrorCode(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}
	raw, err := readIngestBody(r)
	if err != nil {
		writeIngestError(w, err)
		return
	}

	var payload commands.GitArchiveRecord
	if err := decodeJSONStrict(raw, &payload); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := normalizeArchivePayload(&payload); err != nil {
		writeIngestError(w, err)
		return
	}

	status, responseBody, replayed, err := s.withIdempotentWrite(
		r.URL.Path,
		idempotencyKey,
		raw,
		func(tx *sql.Tx) (int, any, error) {
			if err := insertArchiveRecordTx(tx, payload); err != nil {
				return 0, nil, err
			}
			if err := touchGeneratedAtTx(tx); err != nil {
				return 0, nil, err
			}
			return http.StatusCreated, ingestSuccessResponse{
				Resource: "archive",
				ID:       payload.ID,
				Status:   "created",
			}, nil
		},
	)
	if err != nil {
		writeIngestError(w, err)
		return
	}
	if replayed {
		w.Header().Set("X-Idempotent-Replay", "true")
	}
	writeJSONBytes(w, status, responseBody)
}

func readIngestBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	limited := io.LimitReader(r.Body, maxIngestBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, &ingestAPIError{
			status:  http.StatusBadRequest,
			code:    "read_body_failed",
			message: fmt.Sprintf("cannot read request body: %v", err),
		}
	}
	if len(body) == 0 {
		return nil, &ingestAPIError{
			status:  http.StatusBadRequest,
			code:    "empty_body",
			message: "request body is required",
		}
	}
	if len(body) > maxIngestBodyBytes {
		return nil, &ingestAPIError{
			status:  http.StatusRequestEntityTooLarge,
			code:    "payload_too_large",
			message: fmt.Sprintf("request body exceeds %d bytes", maxIngestBodyBytes),
		}
	}
	return body, nil
}

func decodeJSONStrict(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain a single JSON object")
		}
		return err
	}
	return nil
}

func normalizeRunPayload(run *commands.RunRecord) error {
	run.RunID = strings.TrimSpace(run.RunID)
	run.Branch = strings.TrimSpace(run.Branch)
	run.RepoPath = strings.TrimSpace(run.RepoPath)
	run.SyncedAt = strings.TrimSpace(run.SyncedAt)
	if run.RunID == "" {
		return &ingestAPIError{status: http.StatusBadRequest, code: "run_id_required", message: "run_id is required"}
	}
	if run.Branch == "" {
		return &ingestAPIError{status: http.StatusBadRequest, code: "branch_required", message: "branch is required"}
	}
	if run.RepoPath == "" {
		return &ingestAPIError{status: http.StatusBadRequest, code: "repo_path_required", message: "repo_path is required"}
	}
	if run.SyncedAt == "" {
		run.SyncedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if run.CommitCount < 0 {
		return &ingestAPIError{status: http.StatusBadRequest, code: "invalid_commit_count", message: "commit_count cannot be negative"}
	}

	seenIterations := make(map[int]struct{}, len(run.Iterations))
	for i := range run.Iterations {
		it := &run.Iterations[i]
		normalizeIterationRecord(it)
		if err := validateIterationRecord(*it); err != nil {
			return err
		}
		if _, exists := seenIterations[it.Iteration]; exists {
			return &ingestAPIError{
				status:  http.StatusBadRequest,
				code:    "duplicate_iteration",
				message: fmt.Sprintf("iteration %d appears more than once in run payload", it.Iteration),
			}
		}
		seenIterations[it.Iteration] = struct{}{}
	}
	if run.CommitCount < len(run.Iterations) {
		run.CommitCount = len(run.Iterations)
	}
	return nil
}

func normalizeIterationPayload(req *iterationIngestRequest) error {
	req.RunID = strings.TrimSpace(req.RunID)
	if req.RunID == "" {
		return &ingestAPIError{status: http.StatusBadRequest, code: "run_id_required", message: "run_id is required"}
	}
	normalizeIterationRecord(&req.Iteration)
	return validateIterationRecord(req.Iteration)
}

func normalizeIterationRecord(it *commands.IterationRecord) {
	it.CommitHash = strings.TrimSpace(it.CommitHash)
	it.ParentCommitHash = strings.TrimSpace(it.ParentCommitHash)
	it.CommitTime = strings.TrimSpace(it.CommitTime)
	it.Subject = strings.TrimSpace(it.Subject)
	it.Hypothesis = strings.TrimSpace(it.Hypothesis)
	it.Changes = strings.TrimSpace(it.Changes)
	it.Analysis = strings.TrimSpace(it.Analysis)
	it.Kernel = strings.TrimSpace(it.Kernel)
	it.Agent = strings.TrimSpace(it.Agent)
	it.GPU = strings.TrimSpace(it.GPU)
	it.Correctness = strings.TrimSpace(it.Correctness)
	it.Patch = strings.TrimSpace(it.Patch)
	it.PatchError = strings.TrimSpace(it.PatchError)

	if it.CommitTime == "" {
		it.CommitTime = time.Now().UTC().Format(time.RFC3339)
	}
	if it.Hypothesis == "" {
		it.Hypothesis = it.Subject
	}
}

func validateIterationRecord(it commands.IterationRecord) error {
	if it.Iteration < 0 {
		return &ingestAPIError{
			status:  http.StatusBadRequest,
			code:    "invalid_iteration",
			message: "iteration must be >= 0",
		}
	}
	if it.CommitHash == "" {
		return &ingestAPIError{
			status:  http.StatusBadRequest,
			code:    "commit_hash_required",
			message: "iteration.commit_hash is required",
		}
	}
	if it.CommitTime == "" {
		return &ingestAPIError{
			status:  http.StatusBadRequest,
			code:    "commit_time_required",
			message: "iteration.commit_time is required",
		}
	}
	return nil
}

func normalizeArchivePayload(record *commands.GitArchiveRecord) error {
	record.ID = strings.TrimSpace(record.ID)
	record.RunID = strings.TrimSpace(record.RunID)
	record.Branch = strings.TrimSpace(record.Branch)
	record.RepoPath = strings.TrimSpace(record.RepoPath)
	record.HeadCommit = strings.TrimSpace(record.HeadCommit)
	record.CreatedAt = strings.TrimSpace(record.CreatedAt)
	record.Note = strings.TrimSpace(record.Note)
	record.BundleFormat = strings.TrimSpace(record.BundleFormat)
	record.BundleSHA256 = strings.TrimSpace(record.BundleSHA256)
	record.BundleData = strings.TrimSpace(record.BundleData)

	if record.ID == "" {
		return &ingestAPIError{status: http.StatusBadRequest, code: "archive_id_required", message: "id is required"}
	}
	if record.Branch == "" {
		return &ingestAPIError{status: http.StatusBadRequest, code: "branch_required", message: "branch is required"}
	}
	if record.RepoPath == "" {
		return &ingestAPIError{status: http.StatusBadRequest, code: "repo_path_required", message: "repo_path is required"}
	}
	if record.BundleFormat == "" {
		return &ingestAPIError{status: http.StatusBadRequest, code: "bundle_format_required", message: "bundle_format is required"}
	}
	if record.BundleSHA256 == "" {
		return &ingestAPIError{status: http.StatusBadRequest, code: "bundle_sha256_required", message: "bundle_sha256 is required"}
	}
	if record.BundleData == "" {
		return &ingestAPIError{status: http.StatusBadRequest, code: "bundle_data_required", message: "bundle_data is required"}
	}
	if record.BundleSizeBytes < 0 {
		return &ingestAPIError{
			status:  http.StatusBadRequest,
			code:    "invalid_bundle_size",
			message: "bundle_size_bytes cannot be negative",
		}
	}
	if record.CreatedAt == "" {
		record.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return nil
}

func (s *Server) withIdempotentWrite(
	routePath string,
	idempotencyKey string,
	requestBody []byte,
	writeFn func(tx *sql.Tx) (int, any, error),
) (int, []byte, bool, error) {
	db, err := commands.OpenHistoryDB(s.cfg.DBPath)
	if err != nil {
		return 0, nil, false, err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return 0, nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIdempotencyTableTx(tx); err != nil {
		return 0, nil, false, err
	}
	requestHash := sha256Hex(requestBody)
	inserted, existing, err := claimIdempotencyKeyTx(tx, routePath, idempotencyKey, requestHash)
	if err != nil {
		return 0, nil, false, err
	}
	if !inserted {
		if existing.RequestSHA256 != requestHash {
			return 0, nil, false, &ingestAPIError{
				status: http.StatusConflict,
				code:   "idempotency_key_conflict",
				message: "idempotency key already used with a different payload; " +
					"choose a new key",
			}
		}
		if existing.StatusCode == 0 {
			return 0, nil, false, &ingestAPIError{
				status:  http.StatusConflict,
				code:    "idempotency_key_in_progress",
				message: "same idempotency key is currently being processed; retry shortly",
			}
		}
		if err := tx.Commit(); err != nil {
			return 0, nil, false, err
		}
		return existing.StatusCode, []byte(existing.ResponseBody), true, nil
	}

	statusCode, payload, err := writeFn(tx)
	if err != nil {
		return 0, nil, false, err
	}
	responseBody, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, false, err
	}

	if err := finalizeIdempotencyKeyTx(tx, routePath, idempotencyKey, statusCode, string(responseBody)); err != nil {
		return 0, nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, false, err
	}
	return statusCode, responseBody, false, nil
}

func ensureIdempotencyTableTx(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS api_idempotency_keys (
		route_path TEXT NOT NULL,
		idempotency_key TEXT NOT NULL,
		request_sha256 TEXT NOT NULL,
		status_code INTEGER NOT NULL,
		response_body TEXT NOT NULL,
		created_at TEXT NOT NULL,
		PRIMARY KEY (route_path, idempotency_key)
	)`)
	return err
}

func claimIdempotencyKeyTx(
	tx *sql.Tx,
	routePath string,
	idempotencyKey string,
	requestSHA256 string,
) (bool, idempotencyRecord, error) {
	_, err := tx.Exec(
		`INSERT INTO api_idempotency_keys (
			route_path, idempotency_key, request_sha256, status_code, response_body, created_at
		) VALUES (?, ?, ?, 0, '', ?)`,
		routePath,
		idempotencyKey,
		requestSHA256,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err == nil {
		return true, idempotencyRecord{}, nil
	}
	if !isSQLiteUniqueConstraint(err) {
		return false, idempotencyRecord{}, err
	}

	record, found, err := loadIdempotencyRecordTx(tx, routePath, idempotencyKey)
	if err != nil {
		return false, idempotencyRecord{}, err
	}
	if !found {
		return false, idempotencyRecord{}, errors.New("idempotency key conflict detected but existing record missing")
	}
	return false, record, nil
}

func finalizeIdempotencyKeyTx(
	tx *sql.Tx,
	routePath string,
	idempotencyKey string,
	statusCode int,
	responseBody string,
) error {
	_, err := tx.Exec(
		`UPDATE api_idempotency_keys
		 SET status_code = ?, response_body = ?
		 WHERE route_path = ? AND idempotency_key = ?`,
		statusCode,
		responseBody,
		routePath,
		idempotencyKey,
	)
	return err
}

func loadIdempotencyRecordTx(
	tx *sql.Tx,
	routePath string,
	idempotencyKey string,
) (idempotencyRecord, bool, error) {
	var rec idempotencyRecord
	err := tx.QueryRow(
		`SELECT request_sha256, status_code, response_body
		 FROM api_idempotency_keys
		 WHERE route_path = ? AND idempotency_key = ?`,
		routePath,
		idempotencyKey,
	).Scan(&rec.RequestSHA256, &rec.StatusCode, &rec.ResponseBody)
	if errors.Is(err, sql.ErrNoRows) {
		return idempotencyRecord{}, false, nil
	}
	if err != nil {
		return idempotencyRecord{}, false, err
	}
	return rec, true, nil
}

func insertRunRecordTx(tx *sql.Tx, run commands.RunRecord) (int64, error) {
	runRowID, exists, err := lookupRunRowIDByRunIDTx(tx, run.RunID)
	if err != nil {
		return 0, err
	}
	if exists {
		return 0, &ingestAPIError{
			status:  http.StatusConflict,
			code:    "run_exists",
			message: fmt.Sprintf("run_id already exists: %s", run.RunID),
		}
	}

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
		return 0, err
	}
	runRowID, err = res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, it := range run.Iterations {
		if err := insertIterationTx(tx, runRowID, it); err != nil {
			return 0, err
		}
	}
	return runRowID, nil
}

func insertIterationForRunTx(tx *sql.Tx, runID string, iteration commands.IterationRecord) error {
	runRowID, exists, err := lookupRunRowIDByRunIDTx(tx, runID)
	if err != nil {
		return err
	}
	if !exists {
		return &ingestAPIError{
			status:  http.StatusNotFound,
			code:    "run_not_found",
			message: fmt.Sprintf("run_id not found: %s", runID),
		}
	}
	dup, err := iterationExistsTx(tx, runRowID, iteration.Iteration)
	if err != nil {
		return err
	}
	if dup {
		return &ingestAPIError{
			status:  http.StatusConflict,
			code:    "iteration_exists",
			message: fmt.Sprintf("iteration %d already exists for run_id %s", iteration.Iteration, runID),
		}
	}
	return insertIterationTx(tx, runRowID, iteration)
}

func insertArchiveRecordTx(tx *sql.Tx, record commands.GitArchiveRecord) error {
	exists, err := archiveExistsTx(tx, record.ID)
	if err != nil {
		return err
	}
	if exists {
		return &ingestAPIError{
			status:  http.StatusConflict,
			code:    "archive_exists",
			message: fmt.Sprintf("archive id already exists: %s", record.ID),
		}
	}
	_, err = tx.Exec(
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

func insertIterationTx(tx *sql.Tx, runRowID int64, it commands.IterationRecord) error {
	var speedup any
	if it.HasSpeedup || it.SpeedupVsBaseline != 0 {
		speedup = it.SpeedupVsBaseline
	}

	var latency any
	if it.HasLatency || it.LatencyUs != 0 {
		latency = it.LatencyUs
	}

	_, err := tx.Exec(
		`INSERT INTO iterations (
			run_row_id, iteration, commit_hash, parent_commit_hash, commit_time,
			subject, hypothesis, changes, analysis, kernel, agent, gpu, correctness,
			speedup_vs_baseline, latency_us, patch, patch_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		it.Correctness,
		speedup,
		latency,
		it.Patch,
		it.PatchError,
	)
	return err
}

func lookupRunRowIDByRunIDTx(tx *sql.Tx, runID string) (int64, bool, error) {
	var rowID int64
	err := tx.QueryRow(
		`SELECT id FROM runs WHERE run_id = ? ORDER BY id DESC LIMIT 1`,
		runID,
	).Scan(&rowID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return rowID, true, nil
}

func iterationExistsTx(tx *sql.Tx, runRowID int64, iteration int) (bool, error) {
	var one int
	err := tx.QueryRow(
		`SELECT 1 FROM iterations WHERE run_row_id = ? AND iteration = ? LIMIT 1`,
		runRowID,
		iteration,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func archiveExistsTx(tx *sql.Tx, archiveID string) (bool, error) {
	var one int
	err := tx.QueryRow(
		`SELECT 1 FROM archives WHERE id = ? LIMIT 1`,
		archiveID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func touchGeneratedAtTx(tx *sql.Tx) error {
	_, err := tx.Exec(
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		"generated_at",
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isSQLiteUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

func writeIngestError(w http.ResponseWriter, err error) {
	var apiErr *ingestAPIError
	if errors.As(err, &apiErr) {
		writeErrorCode(w, apiErr.status, apiErr.code, apiErr.message)
		return
	}
	writeErrorCode(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeJSONBytes(w http.ResponseWriter, statusCode int, payload []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	_, _ = w.Write(payload)
}

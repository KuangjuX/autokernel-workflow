#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

SNAPSHOT_PATH="${SNAPSHOT_PATH:-workspace/history.snapshot.db}"
SNAPSHOT_SHA_PATH="${SNAPSHOT_SHA_PATH:-${SNAPSHOT_PATH}.sha256}"
MANIFEST_PATH="${MANIFEST_PATH:-workspace/history.snapshot.meta.json}"
TARGET_DB_PATH="${TARGET_DB_PATH:-workspace/history.db}"
KEEP_SNAPSHOT="${KEEP_SNAPSHOT:-0}"
RENDER_EXPORT="${RENDER_EXPORT:-1}"
KERNELHUB_BIN="${KERNELHUB_BIN:-./bin/kernelhub}"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "[error] sqlite3 is required" >&2
  exit 1
fi
if [[ ! -f "${SNAPSHOT_PATH}" ]]; then
  echo "[error] snapshot DB not found: ${SNAPSHOT_PATH}" >&2
  exit 1
fi

if [[ -f "${SNAPSHOT_SHA_PATH}" ]]; then
  EXPECTED_SHA="$(awk '{print $1}' "${SNAPSHOT_SHA_PATH}" | head -n 1)"
  ACTUAL_SHA="$(shasum -a 256 "${SNAPSHOT_PATH}" | awk '{print $1}')"
  if [[ "${EXPECTED_SHA}" != "${ACTUAL_SHA}" ]]; then
    echo "[error] sha256 mismatch for snapshot" >&2
    echo "  expected=${EXPECTED_SHA}" >&2
    echo "  actual=${ACTUAL_SHA}" >&2
    exit 1
  fi
  echo "[check] sha256 verified"
else
  echo "[warn] sha file not found, skip checksum verification: ${SNAPSHOT_SHA_PATH}" >&2
fi

CHECK_RESULT="$(sqlite3 "${SNAPSHOT_PATH}" "PRAGMA integrity_check;")"
if [[ "${CHECK_RESULT}" != "ok" ]]; then
  echo "[error] snapshot integrity_check failed: ${CHECK_RESULT}" >&2
  exit 1
fi

RUN_COUNT="$(sqlite3 "${SNAPSHOT_PATH}" "SELECT COUNT(*) FROM runs;" 2>/dev/null || echo "0")"
ITER_COUNT="$(sqlite3 "${SNAPSHOT_PATH}" "SELECT COUNT(*) FROM iterations;" 2>/dev/null || echo "0")"
ARCHIVE_COUNT="$(sqlite3 "${SNAPSHOT_PATH}" "SELECT COUNT(*) FROM archives;" 2>/dev/null || echo "0")"
echo "[check] snapshot counts runs=${RUN_COUNT} iterations=${ITER_COUNT} archives=${ARCHIVE_COUNT}"

mkdir -p "$(dirname "${TARGET_DB_PATH}")"
if [[ -f "${TARGET_DB_PATH}" ]]; then
  BACKUP_PATH="${TARGET_DB_PATH}.bak-$(date -u +"%Y%m%d-%H%M%S")"
  cp -f "${TARGET_DB_PATH}" "${BACKUP_PATH}"
  echo "[backup] current DB copied to ${BACKUP_PATH}"
fi

if [[ "${KEEP_SNAPSHOT}" == "1" ]]; then
  cp -f "${SNAPSHOT_PATH}" "${TARGET_DB_PATH}"
  echo "[activate] copied snapshot to ${TARGET_DB_PATH}"
else
  mv -f "${SNAPSHOT_PATH}" "${TARGET_DB_PATH}"
  echo "[activate] moved snapshot to ${TARGET_DB_PATH}"
fi

rm -f "${TARGET_DB_PATH}-wal" "${TARGET_DB_PATH}-shm"

FINAL_CHECK="$(sqlite3 "${TARGET_DB_PATH}" "PRAGMA integrity_check;")"
if [[ "${FINAL_CHECK}" != "ok" ]]; then
  echo "[error] activated DB integrity_check failed: ${FINAL_CHECK}" >&2
  exit 1
fi

if [[ -f "${MANIFEST_PATH}" ]]; then
  echo "[info] manifest available: ${MANIFEST_PATH}"
fi

if [[ "${RENDER_EXPORT}" == "1" ]]; then
  if [[ -x "${KERNELHUB_BIN}" ]]; then
    echo "[export] regenerate local static artifacts"
    "${KERNELHUB_BIN}" export \
      --db-path "${TARGET_DB_PATH}" \
      --out "./workspace/history_snapshot.json" \
      --html-out "./workspace/history_dashboard.html" \
      --format json
  else
    echo "[warn] skip export; kernelhub binary not executable: ${KERNELHUB_BIN}" >&2
  fi
fi

echo "[done] local history DB activated and ready"

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

DB_PATH="${DB_PATH:-workspace/history.db}"
SNAPSHOT_PATH="${SNAPSHOT_PATH:-workspace/history.snapshot.db}"
MANIFEST_PATH="${MANIFEST_PATH:-workspace/history.snapshot.meta.json}"
FT_BIN="${FT_BIN:-ft}"
NO_PUT="${NO_PUT:-0}"
EXPORT_STATIC="${EXPORT_STATIC:-0}"
KERNELHUB_BIN="${KERNELHUB_BIN:-./bin/kernelhub}"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "[error] sqlite3 is required" >&2
  exit 1
fi
if ! command -v "${FT_BIN}" >/dev/null 2>&1 && [[ "${NO_PUT}" != "1" ]]; then
  echo "[error] ft command not found: ${FT_BIN}" >&2
  exit 1
fi
if [[ ! -f "${DB_PATH}" ]]; then
  echo "[error] DB file not found: ${DB_PATH}" >&2
  exit 1
fi

mkdir -p "$(dirname "${SNAPSHOT_PATH}")"
mkdir -p "$(dirname "${MANIFEST_PATH}")"

TMP_SNAPSHOT="${SNAPSHOT_PATH}.tmp"
TMP_SHA="${SNAPSHOT_PATH}.sha256.tmp"
TMP_MANIFEST="${MANIFEST_PATH}.tmp"

rm -f "${TMP_SNAPSHOT}" "${TMP_SHA}" "${TMP_MANIFEST}"

json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  printf "%s" "${s}"
}

query_or_default() {
  local q="$1"
  local d="$2"
  local v
  if v="$(sqlite3 "${TMP_SNAPSHOT}" "${q}" 2>/dev/null)" && [[ -n "${v}" ]]; then
    printf "%s" "${v}"
    return
  fi
  printf "%s" "${d}"
}

echo "[snapshot] checkpoint WAL"
sqlite3 "${DB_PATH}" "PRAGMA busy_timeout=5000; PRAGMA wal_checkpoint(TRUNCATE);" >/dev/null

echo "[snapshot] create backup -> ${TMP_SNAPSHOT}"
sqlite3 "${DB_PATH}" ".backup '${TMP_SNAPSHOT}'"

CHECK_RESULT="$(sqlite3 "${TMP_SNAPSHOT}" "PRAGMA integrity_check;")"
if [[ "${CHECK_RESULT}" != "ok" ]]; then
  echo "[error] integrity_check failed: ${CHECK_RESULT}" >&2
  rm -f "${TMP_SNAPSHOT}"
  exit 1
fi

SHA256="$(shasum -a 256 "${TMP_SNAPSHOT}" | awk '{print $1}')"
SIZE_BYTES="$(wc -c < "${TMP_SNAPSHOT}" | tr -d '[:space:]')"
SNAPSHOT_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
GENERATED_AT="$(query_or_default "SELECT value FROM meta WHERE key='generated_at' LIMIT 1;" "")"
RUN_COUNT="$(query_or_default "SELECT COUNT(*) FROM runs;" "0")"
ITER_COUNT="$(query_or_default "SELECT COUNT(*) FROM iterations;" "0")"
ARCHIVE_COUNT="$(query_or_default "SELECT COUNT(*) FROM archives;" "0")"

printf "%s  %s\n" "${SHA256}" "$(basename "${SNAPSHOT_PATH}")" > "${TMP_SHA}"
cat > "${TMP_MANIFEST}" <<EOF
{
  "snapshot_at": "$(json_escape "${SNAPSHOT_AT}")",
  "source_db_path": "$(json_escape "${DB_PATH}")",
  "snapshot_path": "$(json_escape "${SNAPSHOT_PATH}")",
  "sha256": "$(json_escape "${SHA256}")",
  "size_bytes": ${SIZE_BYTES},
  "generated_at": "$(json_escape "${GENERATED_AT}")",
  "run_count": ${RUN_COUNT},
  "iteration_count": ${ITER_COUNT},
  "archive_count": ${ARCHIVE_COUNT}
}
EOF

mv -f "${TMP_SNAPSHOT}" "${SNAPSHOT_PATH}"
mv -f "${TMP_SHA}" "${SNAPSHOT_PATH}.sha256"
mv -f "${TMP_MANIFEST}" "${MANIFEST_PATH}"

echo "[snapshot] ready: ${SNAPSHOT_PATH}"
echo "[snapshot] sha256: ${SHA256}"
echo "[snapshot] counts runs=${RUN_COUNT} iterations=${ITER_COUNT} archives=${ARCHIVE_COUNT}"

if [[ "${EXPORT_STATIC}" == "1" ]]; then
  if [[ -x "${KERNELHUB_BIN}" ]]; then
    echo "[snapshot] export static dashboard from snapshot DB"
    "${KERNELHUB_BIN}" export \
      --db-path "${SNAPSHOT_PATH}" \
      --out "./workspace/history_snapshot.json" \
      --html-out "./workspace/history_dashboard.html" \
      --format json
  else
    echo "[warn] skip static export; kernelhub binary not executable: ${KERNELHUB_BIN}" >&2
  fi
fi

if [[ "${NO_PUT}" == "1" ]]; then
  echo "[ft] NO_PUT=1, skip ft sync --put"
  exit 0
fi

FILES_TO_PUT=(
  "${SNAPSHOT_PATH}"
  "${SNAPSHOT_PATH}.sha256"
  "${MANIFEST_PATH}"
)

if [[ -f "./workspace/history_snapshot.json" ]]; then
  FILES_TO_PUT+=("./workspace/history_snapshot.json")
fi
if [[ -f "./workspace/history_dashboard.html" ]]; then
  FILES_TO_PUT+=("./workspace/history_dashboard.html")
fi

for path in "${FILES_TO_PUT[@]}"; do
  echo "[ft] sync --put ${path}"
  "${FT_BIN}" sync --put "${path}"
done

echo "[done] snapshot generated and pushed via ft"

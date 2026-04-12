# KernelHub Program

This document is the execution contract for agents operating in this repository.
Follow it strictly.

## 1) Mission

Optimize one target kernel using AKO4ALL while preserving correctness, and
produce fully traceable artifacts that can be reviewed offline and restored
later.

Required outputs per run:

- One git branch in an isolated AKO4ALL run workspace
- Structured commit history with parseable metadata
- One history sync record via `kernelhub sync-git`
- One branch archive record via `kernelhub archive-git`
- One static snapshot + HTML dashboard via `kernelhub export`

## 2) Hard Boundaries (Do Not Violate)

1. Do not modify `third_party/AKO4ALL` directly as a shared template.
2. Create a per-run workspace and operate there only.
3. Do not change benchmark/reference semantics to fake speedups.
4. Do not bypass correctness checks.
5. Do not force-push shared branches.
6. Do not delete run workspace before successful `archive-git`.

## 3) Runtime Inputs (Must Be Explicit)

Before running optimization, define:

- `RUN_ID` (e.g. `run-gemm-001`)
- target `kernel-src` path from mimikyu
- optional `reference-src`
- optional `bench-src` (recommended for fixed shape control)
- optional `context-src`
- run branch name (recommended: `agent/${RUN_ID}`)
- history path (default: `./workspace/history.db`)

If shape matters, encode shapes in bench/config files, not only in chat text.

## 4) Standard Workflow

### Step A: Build KernelHub binary

```bash
go build -o bin/kernelhub ./cmd/kernelhub
```

### Step B: Create isolated AKO4ALL run workspace

```bash
RUN_ID="run-gemm-001"
BRANCH="agent/${RUN_ID}"
DB_PATH="./workspace/history.db"
git -C third_party/AKO4ALL worktree add "workspace/runs/${RUN_ID}/ako" -b "${BRANCH}"
```

### Step C: Prepare AKO4ALL task inputs

```bash
./bin/kernelhub prepare \
  --ako-root "workspace/runs/${RUN_ID}/ako" \
  --kernel-src "/path/to/mimikyu/kernel.py" \
  --reference-src "/path/to/reference.py" \
  --bench-src "/path/to/bench_or_shapes_config" \
  --context-src "/path/to/context_dir_or_file" \
  --run-id "${RUN_ID}" \
  --db-path "${DB_PATH}"
```

Notes:

- `prepare` supports `--dry-run`.
- A manifest is written to `workspace/latest_prepare_manifest.json`.
- When `--db-path` is provided, `prepare` automatically queries history for
  prior runs of the same kernel and generates `context/history_summary.md`.
  The agent reads this during setup to avoid repeating failed experiments and
  to build on successful strategies.

### Step D: Run AKO4ALL in run workspace

```bash
cd "workspace/runs/${RUN_ID}/ako"
# run your AKO4ALL agent command
```

### Step E: Sync git history into KernelHub history file

```bash
./bin/kernelhub sync-git \
  --repo-path "workspace/runs/${RUN_ID}/ako" \
  --branch "${BRANCH}" \
  --db-path "${DB_PATH}" \
  --run-id "${RUN_ID}"
```

### Step F: Archive branch for offline recovery (new required step)

```bash
./bin/kernelhub archive-git \
  --repo-path "workspace/runs/${RUN_ID}/ako" \
  --branch "${BRANCH}" \
  --db-path "${DB_PATH}" \
  --run-id "${RUN_ID}"
```

Notes:

- `archive-git` stores a compressed git bundle into the history file.
- Optional flags: `--note`, `--dry-run`.

### Step G: Optional restore validation (recommended before cleanup)

```bash
./bin/kernelhub restore-git \
  --db-path "${DB_PATH}" \
  --run-id "${RUN_ID}" \
  --out-repo "./workspace/restored_repo/${RUN_ID}"
```

Notes:

- `restore-git` can select a specific archive via `--archive-id`.
- Optional `--checkout` and `--dry-run` are available.

### Step H: Export static artifacts

```bash
./bin/kernelhub export \
  --db-path "${DB_PATH}" \
  --out "./workspace/history_snapshot.json" \
  --html-out "./workspace/history_dashboard.html" \
  --format json
```

Notes:

- `export` supports `--format json|toml` and `--dry-run`.
- Dashboard run details support viewing per-commit patch content.
- `--db-path` is a SQLite history DB path.
- If a legacy JSON history file exists at `--db-path`, KernelHub automatically
  migrates it to SQLite and keeps a backup at `<db-path>.json.bak`.

### Step I: Generate framework patches (kernel-adapter integration)

```bash
./bin/kernelhub patch \
  --db-path "${DB_PATH}" \
  --kernel-assets "./workspace/kernel_assets" \
  --mmq-root "/path/to/mmq_kernels/mmq_kernels" \
  --output-dir "./workspace/patches" \
  --runs-dir "./workspace/runs"
```

This reads the history DB, finds the best iteration (highest speedup with
`correctness=PASS`) for each run, and invokes `kernel-adapter batch-patch` to
generate unified diffs applicable to the upstream framework.

Output per run:

```
workspace/patches/<run-id>/
├── patch.diff           # unified diff against framework source
├── metadata.json        # run_id, speedup, latency, commit_hash, etc.
└── kernel_optimized.py  # optimized kernel for reference
```

Verification and application:

```bash
# Verify only (default: --verify is on)
./bin/kernelhub patch --db-path "${DB_PATH}" --mmq-root ... --verify

# Verify and apply (creates opt/<kernel> branches in the MMQ repo)
./bin/kernelhub patch --db-path "${DB_PATH}" --mmq-root ... --apply
```

Notes:

- Requires `kernel-adapter` to be installed (`pip install -e third_party/kernel-adapter`
  or `cd third_party/kernel-adapter && uv sync`).
- `--verify` uses `git apply --check` to confirm patches apply cleanly.
- `--apply` invokes `kernel-adapter patch-apply` which creates per-kernel branches.
- `--dry-run` prints what would be done without invoking kernel-adapter.
- When multiple runs optimize the same framework file, only the highest-speedup
  patch is kept; lower-speedup patches are reported and skipped.

## 5) History Data Model and Parsing Contract

`sync-git` appends run records; `archive-git` appends archive records to the
same SQLite history DB.

For reliable parsing, include these commit body keys (exact key names):

- `kernel: ...`
- `agent: ...`
- `backend: ...` (e.g. `triton`, `cuda`)
- `correctness: ...`
- `speedup_vs_baseline: ...`
- `latency_us: ...`
- `changes: ...` (optional, supports multiline)
- `analysis: ...` (optional, supports multiline)

Additional keys are allowed for human review, but are not required by parser.

Iteration subject format recommendation:

- `exp <N>: <hypothesis>`

Example:

```text
exp 7: increase block_k to 128

kernel: gemm_bf16_nt
agent: agent-a
gpu: sm90
backend: triton
correctness: PASS
speedup_vs_baseline: 1.23x
latency_us: 142.3
```

## 6) Quality Gates

Only keep changes that satisfy all:

1. correctness is PASS
2. no benchmark/reference contract break
3. performance is better than baseline on target shape set

If any gate fails:

- revert local experiment commit in run workspace
- continue with next hypothesis

## 7) Stop Conditions

Stop run when one of these is true:

- target speedup reached
- 5+ consecutive non-improving valid iterations
- time budget exceeded
- repeated build/runtime failure without clear fix

## 8) Final Deliverables Checklist

- [ ] run workspace branch exists and contains iteration commits
- [ ] history file updated via `sync-git`
- [ ] branch archived via `archive-git` (record id available)
- [ ] optional restore verification completed or explicitly skipped
- [ ] `history_snapshot.json` exported
- [ ] `history_dashboard.html` exported
- [ ] best commit hash recorded in run summary
- [ ] framework patches generated via `patch` (optional, requires MMQ source)

## 9) Non-Goals for This Minimal System

- no centralized online dashboard service
- no distributed scheduler
- no direct auto-merge back to mimikyu main branch

Use this as a reliable baseline first, then add complexity later.

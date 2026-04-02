# KernelHub

Go minimal skeleton for AKO4ALL-based kernel optimization orchestration.

Scope intentionally kept small:

1. `prepare` - copy one mimikyu kernel task into AKO4ALL input layout
2. `sync-git` - parse AKO4ALL branch commit history into local history DB
3. `archive-git` - archive git bundles into history DB for offline restore
4. `restore-git` - restore archived git bundles from history DB
5. `serve` - start a local HTTP dashboard backed by SQLite
6. `export` - export static snapshot/dashboard for offline viewing

AKO4ALL engine is pinned as submodule:

- `third_party/AKO4ALL`

## Build

```bash
cd /Users/kuangjux/codes/autokernel-workflow
git submodule update --init --recursive
go build -o bin/kernelhub ./cmd/kernelhub
```

## Command: prepare

Prepare one kernel into `third_party/AKO4ALL/input`.

```bash
./bin/kernelhub prepare \
  --ako-root third_party/AKO4ALL \
  --kernel-src /Users/kuangjux/codes/mimikyu/mmq_kernels/mmq_kernels/gemm.py \
  --reference-src /Users/kuangjux/codes/mimikyu/mmq_kernels/mmq_kernels/naive/gemm_bf16_nt.py \
  --run-id run-gemm-001
```

Optional:

- `--bench-src`
- `--context-src`
- `--dry-run`

## Command: sync-git

Parse one AKO4ALL branch and append run history into local SQLite DB.

```bash
./bin/kernelhub sync-git \
  --repo-path ./third_party/AKO4ALL \
  --branch agent/gemm_bf16_nt/agent-a \
  --db-path ./workspace/history.db \
  --run-id run-gemm-001
```

Notes:

- `--db-path` is a SQLite history database path.
- If an old JSON history file is found at `--db-path`, KernelHub auto-migrates
  it to SQLite and keeps a backup at `<db-path>.json.bak`.
- Structured fields are parsed from commit body when present:
  - `kernel:`
  - `agent:`
  - `correctness:`
  - `speedup_vs_baseline:`
  - `latency_us:`

## Command: export

Generate static snapshot and dashboard HTML (optional for offline sharing).

```bash
./bin/kernelhub export \
  --db-path ./workspace/history.db \
  --out ./workspace/history_snapshot.json \
  --html-out ./workspace/history_dashboard.html \
  --format json
```

Open `workspace/history_dashboard.html` directly in browser.
In the run details table, click the `View` button in the `patch` column to
expand the generated commit patch.

## Command: serve

Start a local HTTP server and live dashboard backed by SQLite.

```bash
./bin/kernelhub serve \
  --db-path ./workspace/history.db \
  --listen :8080
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080) in browser.

Endpoints:

- `GET /api/snapshot` (`?include_patches=1` to embed commit patches)
- `GET /api/patch?repo_path=...&commit=...&parent=...`
- `GET /healthz`

## Command: archive-git

Archive one branch as a git bundle into `history.db` (SQLite). This allows
recovery even when `workspace/runs/*` is deleted.

```bash
./bin/kernelhub archive-git \
  --repo-path ./workspace/runs/run-gemm-001/ako \
  --branch agent/run-gemm-001 \
  --db-path ./workspace/history.db \
  --run-id run-gemm-001
```

Optional:

- `--note`
- `--dry-run`

## Command: restore-git

Restore archived git objects into a local repo from `history.db`.

```bash
./bin/kernelhub restore-git \
  --db-path ./workspace/history.db \
  --run-id run-gemm-001 \
  --out-repo ./workspace/restored_repo
```

Optional:

- `--archive-id` (pick a specific archive record)
- `--checkout` (commit/branch/tag to checkout after fetch)
- `--dry-run`

## FT sync scripts

For a remote-run/local-view workflow, use the helper scripts in `scripts/`.

Remote server (generate a consistent SQLite snapshot, then push with `ft sync --put`):

```bash
./scripts/ft_remote_snapshot_put.sh
```

Local machine (verify snapshot, backup old DB, then activate new DB):

```bash
./scripts/ft_local_activate_snapshot.sh
```

Recommended flow:

1. Remote runs optimization (`sync-git` + `archive-git`) as usual.
2. Remote runs `./scripts/ft_remote_snapshot_put.sh`.
3. Local receives files via `ft sync`.
4. Local runs `./scripts/ft_local_activate_snapshot.sh`.
5. Local runs `./bin/kernelhub serve --db-path ./workspace/history.db --listen :8080`.

Useful env vars:

- `DB_PATH` (remote source DB, default `workspace/history.db`)
- `SNAPSHOT_PATH` (snapshot file path, default `workspace/history.snapshot.db`)
- `NO_PUT=1` (remote: build snapshot only, skip `ft sync --put`)
- `EXPORT_STATIC=1` (remote: also regenerate static export files)
- `KEEP_SNAPSHOT=1` (local: keep snapshot file after activation)
- `RENDER_EXPORT=0` (local: skip static export regeneration)

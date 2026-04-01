# KernelHub

Go minimal skeleton for AKO4ALL-based kernel optimization orchestration.

Scope intentionally kept small:

1. `prepare` - copy one mimikyu kernel task into AKO4ALL input layout
2. `sync-git` - parse AKO4ALL branch commit history into local history file
3. `export` - export static snapshot/dashboard for local viewing

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

Parse one AKO4ALL branch and append run history into local data file.

```bash
./bin/kernelhub sync-git \
  --repo-path ./third_party/AKO4ALL \
  --branch agent/gemm_bf16_nt/agent-a \
  --db-path ./workspace/history.db \
  --run-id run-gemm-001
```

Notes:

- In this minimal skeleton, `--db-path` stores JSON history data.
- Structured fields are parsed from commit body when present:
  - `kernel:`
  - `agent:`
  - `correctness:`
  - `speedup_vs_baseline:`
  - `latency_us:`

## Command: export

Generate static snapshot and dashboard HTML.

```bash
./bin/kernelhub export \
  --db-path ./workspace/history.db \
  --out ./workspace/history_snapshot.json \
  --html-out ./workspace/history_dashboard.html \
  --format json
```

Open `workspace/history_dashboard.html` directly in browser.
# KernelHub Design Document

## 1. Overview

KernelHub is the orchestration layer for **agentic GPU kernel optimization**. It manages the full lifecycle of AI-agent-driven kernel optimization runs: task preparation, execution tracking, history persistence, offline archival, and result visualization.

The system is built around a core insight: coding agents (e.g., Claude Code) can iteratively optimize GPU kernels, but the optimization process needs structured scaffolding — reproducible task setup, traceable commit histories, integrity verification, and artifact management. KernelHub provides this scaffolding as a minimal Go CLI and HTTP API.

### Key Capabilities

| Capability | Description |
|---|---|
| **Task preparation** | Import kernels from framework sources into AKO4ALL format |
| **History tracking** | Parse structured git commit histories into SQLite |
| **Integrity verification** | Validate commit chain and reflog for tamper-free histories |
| **Branch archival** | Store git bundles in SQLite for offline recovery |
| **Dashboard & export** | Live HTTP dashboard and self-contained static HTML |
| **API service** | Versioned JSON API with ingest endpoints and idempotency |

### Design Principles

1. **Minimal surface** — Small Go binary with zero external services (only SQLite).
2. **Git as source of truth** — All optimization iterations live as git commits with structured metadata.
3. **Offline-first** — Every artifact can be exported, archived, and restored without network.
4. **Agent-agnostic** — Works with any coding agent; the optimization engine (AKO4ALL) is a pluggable submodule.
5. **Anti-cheat by design** — History-rewriting git operations are forbidden and detected automatically.

## 2. Architecture

### 2.1 High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         User / Agent                                │
└────────┬──────────────────────┬──────────────────────┬──────────────┘
         │ CLI                  │ HTTP API             │ Agent in
         │                      │                      │ AKO4ALL workspace
         ▼                      ▼                      ▼
┌─────────────┐         ┌──────────────┐       ┌──────────────────┐
│ cmd/kernelhub│         │ internal/    │       │ third_party/     │
│ (main.go)   │         │ server/      │       │ AKO4ALL          │
└──────┬──────┘         │ (API v1)     │       │ (optimization    │
       │                └──────┬───────┘       │  engine)         │
       ▼                       │               └────────┬─────────┘
┌──────────────┐               │                        │
│ internal/cli │               │                        │ git commits
│ (subcommand  │               │                        │ with structured
│  dispatch)   │               │                        │ metadata
└──────┬───────┘               │                        │
       │                       │                        │
       ▼                       ▼                        ▼
┌──────────────────────────────────────────────────────────────────┐
│                    internal/commands                              │
│                                                                  │
│  prepare.go ─── Task setup & input layout                        │
│  sync_git.go ── Git log parsing & domain model                   │
│  archive_git.go ── Git bundle creation & encoding                │
│  restore_git.go ── Bundle decoding & repo restoration            │
│  export.go ──── Snapshot building & HTML generation              │
│  serve.go ───── Local dashboard HTTP server                      │
│  history_store.go ── SQLite schema, migrations, CRUD             │
└──────────────────────────┬───────────────────────────────────────┘
                           │
                           ▼
                    ┌──────────────┐
                    │  SQLite DB   │
                    │ (history.db) │
                    └──────────────┘
```

### 2.2 Package Structure

```
autokernel-workflow/
├── cmd/kernelhub/main.go          # Binary entrypoint
├── internal/
│   ├── cli/cli.go                 # Subcommand routing & flag parsing
│   ├── commands/                  # Core business logic
│   │   ├── prepare.go             # Task preparation
│   │   ├── sync_git.go            # Git history parsing & domain model
│   │   ├── archive_git.go         # Git bundle archival
│   │   ├── restore_git.go         # Bundle restoration
│   │   ├── export.go              # Snapshot & HTML export
│   │   ├── serve.go               # Local dashboard server
│   │   └── history_store.go       # SQLite persistence layer
│   └── server/                    # Production API server
│       ├── server.go              # Mux, routes, config
│       ├── handlers_ingest.go     # POST endpoints for runs/iterations/archives
│       ├── ratelimit.go           # Per-IP token-bucket rate limiter
│       └── errors.go              # Unified JSON error envelope
├── third_party/                   # Git submodules (see Section 5)
│   ├── AKO4ALL/                   # Optimization engine
│   ├── kernel-adapater/           # Kernel format conversion & patch generation
│   └── ptx-isa-markdown/          # CUDA documentation as Claude Code skill
├── workspace/                     # Runtime workspace
│   ├── kernel_assets/             # 30 imported kernel packages
│   └── history_dashboard.html     # Exported dashboard
├── go.mod                         # Go 1.22, dependency: go-sqlite3
└── bin/kernelhub                  # Compiled binary
```

### 2.3 Data Flow

The standard workflow follows a linear pipeline:

```
  prepare         AKO4ALL agent        sync-git       archive-git      export
 ─────────►  ───────────────────►  ─────────────►  ──────────────►  ──────────►
 Copy kernel   Iterative            Parse commits    Bundle branch    Generate
 into AKO4ALL  optimization         into SQLite      into SQLite      HTML/JSON
 input layout  (git commits)        with integrity                    snapshot
                                    checks
```

## 3. Core Components

### 3.1 CLI Layer (`internal/cli`)

The CLI provides seven subcommands dispatched by `Run()`:

| Subcommand | Function | Description |
|---|---|---|
| `prepare` | `commands.Prepare` | Set up AKO4ALL task layout |
| `sync-git` | `commands.SyncGit` | Parse branch commits into history DB |
| `archive-git` | `commands.ArchiveGit` | Archive branch as git bundle |
| `restore-git` | `commands.RestoreGit` | Restore branch from archived bundle |
| `export` | `commands.Export` | Generate static HTML dashboard |
| `serve` | `commands.Serve` | Start local dashboard server |
| `server` | `server.New` + `ListenAndServe` | Start production API server |

The `server` subcommand is the only one that uses `internal/server`; all others delegate to `internal/commands`. Signal handling (SIGINT/SIGTERM) provides graceful shutdown for `server`.

### 3.2 Task Preparation (`prepare.go`)

`Prepare` sets up the AKO4ALL directory layout for a new optimization run:

1. Copies kernel source to `input/`
2. Optionally copies reference, benchmark, and context files
3. Writes a manifest to `workspace/latest_prepare_manifest.json`
4. Optionally installs git post-rewrite guard hooks to prevent history rewriting

The guard hook is a defense-in-depth measure: if an agent attempts `git rebase` or `git commit --amend`, the hook emits a warning. This complements the integrity verification in `sync-git`.

### 3.3 Domain Model & Git History Parsing (`sync_git.go`)

This is the conceptual heart of KernelHub. It defines the domain types used throughout the system:

**Core Types:**

- `RunRecord` — Represents one optimization run (branch, repo path, commit count, timestamps)
- `IterationRecord` — One optimization iteration (commit hash, parent, hypothesis, speedup, latency, correctness, GPU, backend)
- `HistoryFile` — Collection of runs (legacy format, now migrated to SQLite)

**Parsing Pipeline:**

1. Walk `git log` on the optimization branch from merge-base with main
2. Parse each commit's subject line for iteration number and hypothesis
3. Parse commit body for structured fields (`kernel:`, `gpu:`, `correctness:`, `speedup_vs_baseline:`, `latency_us:`, etc.)
4. Run integrity checks:
   - **Parent chain validation** — Every commit's parent must match the previous commit in the chain
   - **Reflog validation** — Detect history rewriting via `git reflog`
5. Append the assembled `RunRecord` to SQLite

### 3.4 Persistence Layer (`history_store.go`)

SQLite is the single persistence mechanism. The schema stores:

- **Runs** — Run metadata (ID, branch, repo path, timestamps)
- **Iterations** — Per-run iteration records (commit hash, metrics, hypothesis)
- **Archives** — Git bundle data (compressed, base64-encoded)

Key design decisions:

- **Legacy migration** — If a JSON history file exists at the DB path, it is automatically migrated to SQLite with a `.json.bak` backup
- **Schema compatibility** — `ensureHistorySchemaCompat` handles forward migrations
- **Transaction safety** — All writes use transactions; the ingest API adds idempotency on top
- **Exported API** — `OpenHistoryDB` is exported for use by `internal/server`

### 3.5 Archival & Restoration (`archive_git.go`, `restore_git.go`)

**Archival pipeline:**

```
git branch  ──►  git bundle create  ──►  gzip  ──►  base64  ──►  SQLite row
                                                                 (with SHA-256)
```

**Restoration pipeline:**

```
SQLite row  ──►  base64 decode  ──►  gunzip  ──►  temp bundle  ──►  git fetch
                                                                    ──►  checkout
```

The archive stores the complete branch as a self-contained git bundle, enabling recovery even when the workspace directory is deleted. SHA-256 checksums ensure integrity.

### 3.6 Snapshot & Dashboard (`export.go`, `serve.go`)

Both `export` and `serve` share the same snapshot-building logic:

- `BuildSnapshot` — Queries all runs and iterations from SQLite, computes aggregate statistics
- `BuildCommitPatch` — Generates unified diffs between consecutive commits via `git diff`
- `renderStaticHTML` / `renderServeHTML` — Embeds the JSON snapshot into a self-contained HTML page with JavaScript UI

The dashboard displays:

- Run summary with kernel names, speedup trajectories, and iteration counts
- Per-iteration detail with commit patches, hypotheses, and metrics
- Aggregate statistics across all runs

### 3.7 API Server (`internal/server`)

The production API server adds three layers on top of the core commands:

**Read endpoints** (`/api/v1/snapshot`, `/api/v1/patch`):
- Directly call `commands.BuildSnapshot` and `commands.BuildCommitPatch`
- Same data as `export` and `serve`, but served as JSON

**Ingest endpoints** (`POST /api/v1/runs`, `/api/v1/iterations`, `/api/v1/archives`):
- Accept JSON bodies with strict validation
- Body size limited to prevent abuse
- **Idempotency** via `Idempotency-Key` header:
  - Same key + same body = replay original response (`X-Idempotent-Replay: true`)
  - Same key + different body = `409 Conflict`
  - Keys stored in `api_idempotency_keys` table with request hash

**Rate limiting** (`ratelimit.go`):
- Per-IP token-bucket algorithm
- Configurable RPS and burst via CLI flags
- Periodic cleanup of stale buckets

**Error handling** (`errors.go`):
- Unified JSON error envelope: `{"error": "...", "message": "...", "code": N}`

## 4. Standard Workflow

The operational workflow for a single kernel optimization run:

```
Step A: Build KernelHub binary
  go build -o bin/kernelhub ./cmd/kernelhub

Step B: Create isolated AKO4ALL workspace (git worktree)
  git -C third_party/AKO4ALL worktree add \
    "workspace/runs/${RUN_ID}/ako" -b "agent/${RUN_ID}"

Step C: Prepare task inputs
  ./bin/kernelhub prepare \
    --ako-root "workspace/runs/${RUN_ID}/ako" \
    --kernel-src "/path/to/kernel.py" \
    --run-id "${RUN_ID}"

Step D: Run AKO4ALL agent (iterative optimization in workspace)
  cd "workspace/runs/${RUN_ID}/ako" && claude

Step E: Sync commit history into SQLite
  ./bin/kernelhub sync-git \
    --repo-path "workspace/runs/${RUN_ID}/ako" \
    --branch "agent/${RUN_ID}" \
    --db-path "./workspace/history.db" \
    --run-id "${RUN_ID}"

Step F: Archive branch for offline recovery
  ./bin/kernelhub archive-git \
    --repo-path "workspace/runs/${RUN_ID}/ako" \
    --branch "agent/${RUN_ID}" \
    --db-path "./workspace/history.db" \
    --run-id "${RUN_ID}"

Step G: Export dashboard
  ./bin/kernelhub export \
    --db-path "./workspace/history.db" \
    --html-out "./workspace/history_dashboard.html"
```

## 5. Third-Party Dependencies

KernelHub includes three git submodules under `third_party/`, each serving a distinct role in the optimization pipeline.

### 5.1 AKO4ALL — Agentic Kernel Optimization Engine

**Repository:** [github.com/KuangjuX/AKO4ALL](https://github.com/KuangjuX/AKO4ALL)

**Purpose:** AKO4ALL is the core optimization engine. It defines the task contract, iteration protocol, and anti-cheat rules that coding agents follow during kernel optimization. KernelHub uses it as a template workspace — each optimization run creates an isolated git worktree from this submodule.

**Key Files:**

| File | Role |
|---|---|
| `TASK.md` | Agent execution contract: setup steps, iteration protocol, commit body template, forbidden git commands |
| `HINTS.md` | Agent behavior controls: profiling triggers, stall detection, git safety rules |
| `ITERATIONS.md` | Iteration log template (summary table + per-iteration details) |
| `bench/kernelbench/bench.py` | Built-in KernelBench evaluator (~33KB) for correctness and performance measurement |
| `bench/kernelbench/GUIDE.md` | Evaluator usage guide: CLI arguments, output format, solution requirements, tolerances |
| `bench-wrapper.sh` | Wrapper script template for bench command generation |

**Architecture within AKO4ALL:**

```
AKO4ALL/
├── input/              # Kernel source files (populated by kernelhub prepare)
├── context/            # Reference materials for the agent (optional)
├── bench/
│   └── kernelbench/    # Built-in evaluator (KernelBench format)
├── workspace/
│   └── runs/           # Per-run worktree directories
├── TASK.md             # Agent contract (setup + iteration + anti-cheat)
├── HINTS.md            # Agent behavior directives
└── ITERATIONS.md       # Iteration log template
```

**How KernelHub uses AKO4ALL:**

1. `prepare` copies kernel sources into `input/` within an AKO4ALL worktree
2. The agent creates `solution/` and `scripts/bench.sh`, then iteratively optimizes
3. `sync-git` parses the resulting commit history and extracts structured metadata
4. `archive-git` bundles the branch for offline recovery

**Iteration Protocol:**
Each optimization attempt follows: hypothesize -> edit kernel -> benchmark -> log to `ITERATIONS.md` -> git commit with structured body -> repeat. The commit body must include fields like `kernel:`, `gpu:`, `correctness:`, `speedup_vs_baseline:`, and `latency_us:` for automated parsing.

**Anti-Cheat Enforcement:**
AKO4ALL forbids history-rewriting git commands (`reset`, `rebase`, `amend`). Only `git revert` is allowed for rollbacks. This is enforced at three layers:
1. `TASK.md` instructions to the agent
2. Git post-rewrite guard hooks (installed by `prepare`)
3. `sync-git` integrity checks (parent chain + reflog validation)

**Built-in Evaluator (KernelBench):**
The evaluator (`bench.py`) supports multiple backends (CUDA, Triton, TileLang, CuTe, HIP), timing methods (cuda_event, host_time), and precision modes (float32, float16, bfloat16). It provides anti-cheat defenses: excessive speedup flagging (>10x triggers warning) and input shape protection (solution's `get_inputs`/`get_init_inputs` are replaced by reference's).

### 5.2 kernel-adapter — Kernel Format Conversion & Patch Generation

**Repository:** [github.com/KuangjuX/kernel-adapater](https://github.com/KuangjuX/kernel-adapater)

**Purpose:** Bridges the gap between optimized kernels and upstream framework source trees. KernelHub optimizes kernels in isolated KernelBench format, but the results need to flow back to the original framework. kernel-adapter handles both directions: importing kernels from frameworks and generating patches to apply optimizations back.

**Key Modules:**

| Module | Role |
|---|---|
| `types.py` | Core data types: `KernelDescriptor`, `PatchBundle` |
| `base.py` | Abstract interfaces: `KernelImporter`, `PatchGenerator` |
| `ast_utils.py` | Python AST utilities for kernel source extraction |
| `history.py` | KernelHub SQLite database reader |
| `cli.py` | CLI entry points (`import`, `validate`, `patch-gen`, `batch-patch`, `patch-apply`) |
| `adapters/mmq.py` | MMQ adapter for Triton and CUDA kernel import/patch |
| `codegen/kernelbench.py` | KernelBench format generation and validation |
| `codegen/patch.py` | Patch application utilities |

**Data Flow:**

```
Framework Source (MMQ)
        │
        │ import (kernel-adapter import --source mmq)
        ▼
KernelBench format (kernel.py + reference.py)
        │
        │ optimize (AKO4ALL agent)
        ▼
Optimized kernel
        │
        │ patch-gen (kernel-adapter patch-gen)
        ▼
patch.diff + metadata.json
        │
        │ patch-apply (kernel-adapter patch-apply)
        ▼
Updated Framework Source
```

**CLI Commands:**

| Command | Description |
|---|---|
| `import` | Convert framework kernels into KernelBench format |
| `validate` | Check kernel_assets for KernelBench compatibility |
| `patch-gen` | Generate a unified diff from a single optimized kernel |
| `batch-patch` | Generate patches for all runs using KernelHub history DB |
| `patch-apply` | Apply a generated patch to the target framework repo |
| `import-existing` | Import an existing kernel_assets directory |

**Currently Supported Frameworks:**

- **MMQ (mimikyu/mmq_kernels):**
  - Triton kernels from `triton_kernels/` (70+ kernels)
  - CUDA C kernels from `jit_kernels/` (22+ kernels)
  - Patch generation: Triton `.py` diffs and CUDA `.cuh` diffs

**Planned Framework Support:**
- NVIDIA SOL-ExecBench
- LeetGPU challenges
- FlashInfer kernel library

**Integration with KernelHub:**
The `batch-patch` command reads the KernelHub SQLite database (`history.py`), finds the best iteration (highest speedup with `correctness=PASS`) for each run, and generates a patch bundle per run. This enables automated upstream contribution from optimization results.

**Technology:**
- Python >= 3.10, built with Hatchling
- Zero runtime dependencies (only pytest for dev)
- Uses Python AST manipulation for source extraction

### 5.3 ptx-isa-markdown — CUDA Documentation Skill

**Repository:** [github.com/KuangjuX/ptx-isa-markdown](https://github.com/KuangjuX/ptx-isa-markdown)

**Purpose:** Provides comprehensive NVIDIA GPU documentation as grep-searchable markdown files, packaged as a Claude Code skill. This gives the optimization agent access to low-level hardware documentation during kernel optimization.

**Documentation Coverage:**

| Documentation | Version | Files | Size |
|---|---|---|---|
| PTX ISA | 9.1 | 405 files | 2.3 MB |
| CUDA Runtime API | 13.1 | 107 files | 0.9 MB |
| CUDA Driver API | 13.1 | 128 files | 0.8 MB |
| **Total** | | **640 files** | **~4.2 MB** |

**Structure:**

```
ptx-isa-markdown/
├── cuda_skill/
│   ├── SKILL.md                      # Claude Code skill definition
│   └── references/
│       ├── ptx-docs/                  # PTX ISA 9.1 (405 files)
│       │   └── 9-instruction-set/     # 186 instruction files
│       ├── cuda-runtime-docs/         # CUDA Runtime API (107 files)
│       │   ├── modules/               # 41 API modules
│       │   └── data-structures/       # 66 structs/unions
│       ├── cuda-driver-docs/          # CUDA Driver API (128 files)
│       │   ├── modules/               # 50 API modules
│       │   └── data-structures/       # 80 structs
│       ├── ptx-isa.md                 # PTX search guide
│       ├── cuda-runtime.md            # Runtime API search guide
│       ├── cuda-driver.md             # Driver API search guide
│       ├── nsys-guide.md              # Nsight Systems patterns
│       ├── ncu-guide.md               # Nsight Compute metrics
│       └── debugging-tools.md         # compute-sanitizer, cuda-gdb
└── scrape_*.py                        # Documentation scrapers
```

**Skill Philosophy:**
The skill follows a "measure before guessing" philosophy with progressive disclosure:
- `SKILL.md` (~13KB) is always loaded and provides debugging/profiling workflows
- Reference files are loaded on-demand when the agent needs specific documentation
- Documentation is searched via grep/ripgrep rather than loaded into context

**Key Skill Topics:**
- Debugging workflow: printf -> compute-sanitizer -> cuda-gdb -> minimize diff
- Performance optimization: nsys timeline -> ncu deep-dive -> hypothesize -> change -> verify
- Compilation reference with architecture-specific flags
- Common performance pattern diagnosis table

**Integration with KernelHub:**
The agent operating within AKO4ALL can use this skill to look up PTX instructions, CUDA API semantics, profiling metrics interpretation, and TensorCore operations (WMMA, WGMMA, TMA) during optimization. This enables informed, hardware-aware kernel optimization rather than blind trial-and-error.

### 5.4 Third-Party Dependency Summary

```
                    ┌─────────────────────────┐
                    │       KernelHub         │
                    │    (Go CLI + API)       │
                    └────┬──────┬──────┬──────┘
                         │      │      │
              ┌──────────┘      │      └──────────┐
              ▼                 ▼                  ▼
    ┌─────────────────┐  ┌───────────────┐  ┌────────────────┐
    │    AKO4ALL      │  │kernel-adapter │  │ptx-isa-markdown│
    │                 │  │               │  │                │
    │ Optimization    │  │ Import/Export │  │ CUDA docs as   │
    │ engine &        │  │ bridge for    │  │ Claude Code    │
    │ agent contract  │  │ framework     │  │ skill for      │
    │                 │  │ integration   │  │ informed       │
    │ - Task protocol │  │               │  │ optimization   │
    │ - Benchmarking  │  │ - MMQ import  │  │                │
    │ - Anti-cheat    │  │ - Patch gen   │  │ - PTX ISA 9.1  │
    │ - Iteration log │  │ - Batch patch │  │ - Runtime API  │
    └─────────────────┘  └───────────────┘  │ - Driver API   │
                                            │ - Profiling    │
                                            └────────────────┘
```

## 6. Data Model

### 6.1 SQLite Schema

The history database (`history.db`) contains the following tables:

**`runs`** — Optimization run metadata

| Column | Type | Description |
|---|---|---|
| `run_id` | TEXT PK | Unique run identifier (e.g., `run-gemm-001`) |
| `branch` | TEXT | Git branch name |
| `repo_path` | TEXT | Path to AKO4ALL worktree |
| `synced_at` | TEXT | ISO 8601 timestamp |
| `commit_count` | INTEGER | Total commits in the run |

**`iterations`** — Per-iteration records

| Column | Type | Description |
|---|---|---|
| `run_id` | TEXT FK | References `runs.run_id` |
| `iteration` | INTEGER | Sequential iteration number |
| `commit_hash` | TEXT | Git commit SHA |
| `parent_commit_hash` | TEXT | Parent commit SHA |
| `commit_time` | TEXT | ISO 8601 timestamp |
| `subject` | TEXT | Commit subject line |
| `hypothesis` | TEXT | Optimization hypothesis |
| `kernel` | TEXT | Kernel name |
| `agent` | TEXT | Agent identifier |
| `gpu` | TEXT | GPU model (e.g., H800, B200) |
| `backend` | TEXT | Backend (triton, cuda) |
| `correctness` | TEXT | PASS or FAIL |
| `speedup_vs_baseline` | TEXT | Speedup ratio (e.g., 1.23x) |
| `latency_us` | TEXT | Latency in microseconds |
| `changes` | TEXT | Description of changes |
| `analysis` | TEXT | Analysis of results |

**`archives`** — Git bundle archive records

| Column | Type | Description |
|---|---|---|
| `id` | TEXT PK | Archive identifier |
| `run_id` | TEXT | Associated run ID |
| `branch` | TEXT | Archived branch name |
| `repo_path` | TEXT | Source repository path |
| `head_commit` | TEXT | Branch HEAD at archive time |
| `created_at` | TEXT | ISO 8601 timestamp |
| `bundle_format` | TEXT | Always `git-bundle+gzip+base64` |
| `bundle_sha256` | TEXT | SHA-256 checksum |
| `bundle_size_bytes` | INTEGER | Original bundle size |
| `bundle_data` | TEXT | Base64-encoded gzipped bundle |

**`api_idempotency_keys`** — Idempotency tracking for API ingest

| Column | Type | Description |
|---|---|---|
| `idempotency_key` | TEXT PK | Client-provided key |
| `request_hash` | TEXT | SHA-256 of request body |
| `response_body` | TEXT | Cached response JSON |
| `response_code` | INTEGER | HTTP status code |
| `created_at` | TEXT | ISO 8601 timestamp |

### 6.2 Commit Message Format

Each iteration commit follows a structured format for automated parsing:

```
[iter N] Short description of optimization direction

kernel: <kernel_name>
agent: <agent_id>
gpu: <H800|B200|...>
backend: <triton|cuda>
correctness: <PASS|FAIL>
speedup_vs_baseline: <1.23x>
latency_us: <45.6>
changes: <brief summary>
analysis: <brief summary>
```

## 7. Kernel Assets

The `workspace/kernel_assets/` directory contains 30 imported kernel packages, each following the KernelBench format:

```
<kernel_name>_pkg/
├── kernel.py         # Kernel implementation to optimize
└── reference.py      # Golden reference for correctness verification
```

**Kernel Categories:**

| Category | Kernels |
|---|---|
| **Activation** | `quick_gelu_fwd`, `quick_gelu_bwd`, `swiglu_bf16`, `swiglu_bf16_bwd`, `swiglu_clamp_bf16` |
| **Quantization** | `act_quant`, `weight_quant`, `weight_dequant`, `act_dequant`, `act_transpose_quant` |
| **Attention / RoPE** | `cuda_qkv_part_rope`, `cuda_varlen_qkv_part_rope` |
| **MoE routing** | `cuda_topk_to_multihot`, `topk_to_multihot`, `cuda_topk_expert_bias`, `cuda_group_limited_topk` |
| **Normalization** | `rms_norm` |
| **Embedding** | `embedding_backward` |
| **Residual** | `residual_forward`, `residual_backward` |
| **Permutation** | `cuda_permute_v2`, `cuda_unpermute_v2` |
| **Fused ops** | `cuda_swiglu_mul_probs_fwd`, `cuda_swiglu_mul_probs_bwd`, `cuda_swiglu_quant` |
| **CUDA-specific** | `cuda_add`, `cuda_cumsum`, `cuda_select_topk_grad`, `cuda_act_quant_transpose`, `cuda_act_quant_transpose_for_gg` |

These kernels originate from the MMQ (mimikyu/mmq_kernels) framework and were imported using `kernel-adapter import --source mmq`.

## 8. Go Dependencies

The Go binary has a single external dependency:

| Dependency | Version | Purpose |
|---|---|---|
| `github.com/mattn/go-sqlite3` | v1.14.38 | SQLite3 driver (CGo-based) |

Go version: **1.22.0**

The minimal dependency footprint is intentional — KernelHub avoids web frameworks, ORM layers, and configuration libraries in favor of the standard library.

## 9. Security & Integrity

### 9.1 Git History Integrity

KernelHub enforces a tamper-free commit history through three layers:

1. **Agent instructions** (`TASK.md`) — Explicitly forbid `git reset`, `git rebase`, `git commit --amend`
2. **Git hooks** — Post-rewrite guard installed by `prepare` warns on history rewriting
3. **Automated verification** (`sync-git`) — Validates parent chain continuity and inspects reflog for rewriting operations

### 9.2 API Security

- **Rate limiting** — Per-IP token-bucket prevents abuse
- **Body size limits** — Ingest requests have maximum body size
- **Strict JSON** — Disallows unknown fields in ingest requests
- **Idempotency** — Safe retry semantics prevent duplicate writes
- **SHA-256 checksums** — Archive bundles are integrity-checked

### 9.3 Anti-Cheat

The system prevents reward hacking at multiple levels:
- Benchmark evaluator flags excessive speedups (>10x)
- Input shapes are sourced from the reference, not the solution
- Optimization history must form a linear chain with no gaps
- Static analysis can be added via custom bench scripts

## 10. Future Directions

Based on the current architecture and planned framework support:

1. **Additional framework adapters** — SOL-ExecBench, LeetGPU, FlashInfer (via kernel-adapter)
2. **Distributed scheduling** — Multi-agent parallel optimization across GPU clusters
3. **Auto-merge pipeline** — Automated PR generation from optimization patches back to upstream frameworks
4. **Online dashboard** — Centralized multi-run monitoring (currently offline-first by design)
5. **Extended metrics** — Roofline model data, memory bandwidth utilization, occupancy tracking in iteration records

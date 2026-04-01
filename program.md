# KernelHub Program

This document is the execution contract for agents operating in this repository.
Follow it strictly.

## 1) Mission

Optimize one target kernel using AKO4ALL, keep correctness, and produce
traceable history artifacts that can be reviewed offline.

Required outputs per run:

- One git branch in the AKO4ALL run workspace
- Structured commit history
- One history file update via `kernelhub sync-git`
- One static snapshot + HTML dashboard via `kernelhub export`

## 2) Hard Boundaries (Do Not Violate)

1. Do not modify `third_party/AKO4ALL` directly as a shared template.
2. Create a per-run workspace and operate there only.
3. Do not change benchmark/reference semantics to fake speedups.
4. Do not bypass correctness checks.
5. Do not force-push shared branches.

## 3) Runtime Inputs (Must Be Explicit)

Before running optimization, define:

- `RUN_ID` (e.g. `run-gemm-001`)
- target `kernel-src` path from mimikyu
- optional `reference-src`
- optional `bench-src` (recommended if you need fixed shape control)
- optional `context-src`
- branch name for AKO4ALL run repo

If shape matters, encode shapes in bench/config files, not only in chat text.

## 4) Standard Workflow

### Step A: Build KernelHub binary

```bash
go build -o bin/kernelhub ./cmd/kernelhub
```

### Step B: Create isolated AKO4ALL run workspace

```bash
RUN_ID="run-gemm-001"
git -C third_party/AKO4ALL worktree add "workspace/runs/${RUN_ID}/ako" -b "agent/${RUN_ID}"
```

### Step C: Prepare AKO4ALL task inputs

```bash
./bin/kernelhub prepare \
  --ako-root "workspace/runs/${RUN_ID}/ako" \
  --kernel-src "/path/to/mimikyu/kernel.py" \
  --reference-src "/path/to/reference.py" \
  --bench-src "/path/to/bench_or_shapes_config" \
  --context-src "/path/to/context_dir_or_file" \
  --run-id "${RUN_ID}"
```

### Step D: Run AKO4ALL in run workspace

```bash
cd "workspace/runs/${RUN_ID}/ako"
# run your AKO4ALL agent command
```

### Step E: Sync git history to KernelHub history file

```bash
./bin/kernelhub sync-git \
  --repo-path "workspace/runs/${RUN_ID}/ako" \
  --branch "agent/${RUN_ID}" \
  --db-path "./workspace/history.db" \
  --run-id "${RUN_ID}"
```

Important:

- In the current minimal skeleton, `--db-path` is a JSON history file path
  (name may still end with `.db` for compatibility).

### Step F: Export static artifacts

```bash
./bin/kernelhub export \
  --db-path "./workspace/history.db" \
  --out "./workspace/history_snapshot.json" \
  --html-out "./workspace/history_dashboard.html" \
  --format json
```

## 5) Commit Contract (For History Parsing)

When possible, include these fields in commit body (exact keys):

- `kernel: ...`
- `agent: ...`
- `correctness: ...`
- `speedup_vs_baseline: ...`
- `speedup_vs_best: ...`
- `latency_us: ...`
- `gpu: ...`

Iteration subject format recommendation:

- `exp <N>: <hypothesis>`

Example:

```text
exp 7: increase block_k to 128

kernel: gemm_bf16_nt
agent: agent-a
correctness: PASS
speedup_vs_baseline: 1.23x
speedup_vs_best: 1.05x
latency_us: 142.3
gpu: sm90
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
- [ ] `history_snapshot.json` exported
- [ ] `history_dashboard.html` exported
- [ ] best commit hash recorded in run summary

## 9) Non-Goals for This Minimal System

- no centralized online dashboard service
- no distributed scheduler
- no direct auto-merge back to mimikyu main branch

Use this as a reliable baseline first, then add complexity later.

---

## 中文执行版（简化）

本节是上面英文合同的中文版本，语义一致，可直接给 Agent 执行。

### 目标

使用 AKO4ALL 优化单个目标 kernel，并保证：

1. 正确性不退化  
2. 过程可追溯（commit + 历史文件）  
3. 输出可离线查看（静态快照与 HTML）

### 强约束（必须遵守）

1. 不直接修改共享模板 `third_party/AKO4ALL`。  
2. 每次 run 必须在独立工作区执行。  
3. 不允许通过改 benchmark/reference 伪造加速。  
4. 正确性失败必须回滚本地实验提交。  
5. 不对共享分支做 force push。  

### 必填输入

- `RUN_ID`（例如 `run-gemm-001`）  
- `kernel-src`（来自 mimikyu 的目标 kernel）  
- 可选 `reference-src`  
- 可选 `bench-src`（建议包含 shape 约束）  
- 可选 `context-src`  
- AKO4ALL run 分支名  

> shape 不要只写在对话里，必须落在 bench/config 文件里。

### 标准步骤

#### A. 编译 KernelHub

```bash
go build -o bin/kernelhub ./cmd/kernelhub
```

#### B. 创建独立 run 工作区

```bash
RUN_ID="run-gemm-001"
git -C third_party/AKO4ALL worktree add "workspace/runs/${RUN_ID}/ako" -b "agent/${RUN_ID}"
```

#### C. 准备本次任务输入

```bash
./bin/kernelhub prepare \
  --ako-root "workspace/runs/${RUN_ID}/ako" \
  --kernel-src "/path/to/mimikyu/kernel.py" \
  --reference-src "/path/to/reference.py" \
  --bench-src "/path/to/bench_or_shapes_config" \
  --context-src "/path/to/context_dir_or_file" \
  --run-id "${RUN_ID}"
```

#### D. 在 run 工作区运行 AKO4ALL

```bash
cd "workspace/runs/${RUN_ID}/ako"
# 执行你的 AKO4ALL Agent 命令
```

#### E. 同步 git 历史到 KernelHub 历史文件

```bash
./bin/kernelhub sync-git \
  --repo-path "workspace/runs/${RUN_ID}/ako" \
  --branch "agent/${RUN_ID}" \
  --db-path "./workspace/history.db" \
  --run-id "${RUN_ID}"
```

#### F. 导出静态结果

```bash
./bin/kernelhub export \
  --db-path "./workspace/history.db" \
  --out "./workspace/history_snapshot.json" \
  --html-out "./workspace/history_dashboard.html" \
  --format json
```

### commit 规范（用于解析）

建议在 commit body 中包含：

- `kernel: ...`
- `agent: ...`
- `correctness: ...`
- `speedup_vs_baseline: ...`
- `speedup_vs_best: ...`
- `latency_us: ...`
- `gpu: ...`

标题建议：

- `exp <N>: <hypothesis>`

### 停止条件

满足任一条件即可停止：

- 达到目标加速比  
- 连续 5 次以上无有效提升  
- 超出时间预算  
- 连续构建/运行失败且无明确修复路径  

### 交付检查清单

- [ ] run 分支与实验提交存在  
- [ ] `sync-git` 完成历史同步  
- [ ] `history_snapshot.json` 已生成  
- [ ] `history_dashboard.html` 已生成  

# KernelHub 中文执行合同

本文件是 `program.md` 的中文独立版本，用于中文 Agent 直接执行。

## 1. 目标

使用 AKO4ALL 优化单个目标 kernel，并保证：

1. 正确性不退化
2. 过程可追溯（commit + 历史文件）
3. 结果可离线查看（静态快照与 HTML）

## 2. 强约束（必须遵守）

1. 不直接修改共享模板 `third_party/AKO4ALL`。
2. 每次 run 必须在独立工作区执行。
3. 不允许通过修改 benchmark/reference 伪造加速。
4. 正确性失败时必须回滚当前实验提交。
5. 不对共享分支执行 force push。

## 3. 必填输入

- `RUN_ID`（例如 `run-gemm-001`）
- `kernel-src`（来自 mimikyu 的目标 kernel 路径）
- 可选 `reference-src`
- 可选 `bench-src`（建议包含 shape 约束）
- 可选 `context-src`
- run 分支名

> 注意：shape 不要只写在对话里，必须落到 bench/config 文件。

## 4. 标准流程

### A) 编译 KernelHub

```bash
go build -o bin/kernelhub ./cmd/kernelhub
```

### B) 创建独立 run 工作区

```bash
RUN_ID="run-gemm-001"
git -C third_party/AKO4ALL worktree add "workspace/runs/${RUN_ID}/ako" -b "agent/${RUN_ID}"
```

### C) 准备任务输入

```bash
./bin/kernelhub prepare \
  --ako-root "workspace/runs/${RUN_ID}/ako" \
  --kernel-src "/path/to/mimikyu/kernel.py" \
  --reference-src "/path/to/reference.py" \
  --bench-src "/path/to/bench_or_shapes_config" \
  --context-src "/path/to/context_dir_or_file" \
  --run-id "${RUN_ID}"
```

### D) 在 run 工作区运行 AKO4ALL

```bash
cd "workspace/runs/${RUN_ID}/ako"
# 执行你的 AKO4ALL Agent 命令
```

### E) 同步 git 历史

```bash
./bin/kernelhub sync-git \
  --repo-path "workspace/runs/${RUN_ID}/ako" \
  --branch "agent/${RUN_ID}" \
  --db-path "./workspace/history.db" \
  --run-id "${RUN_ID}"
```

### F) 导出静态结果

```bash
./bin/kernelhub export \
  --db-path "./workspace/history.db" \
  --out "./workspace/history_snapshot.json" \
  --html-out "./workspace/history_dashboard.html" \
  --format json
```

## 5. Commit 规范（建议）

建议在 commit body 中包含以下字段（便于解析）：

- `kernel: ...`
- `agent: ...`
- `correctness: ...`
- `speedup_vs_baseline: ...`
- `speedup_vs_best: ...`
- `latency_us: ...`
- `gpu: ...`

commit 标题建议：

- `exp <N>: <hypothesis>`

示例：

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

## 6. 停止条件

满足任一条件即可停止：

- 达到目标加速比
- 连续 5 次以上无有效提升
- 超出时间预算
- 连续构建/运行失败且无明确修复路径

## 7. 交付检查清单

- [ ] run 分支与实验提交存在
- [ ] `sync-git` 完成历史同步
- [ ] `history_snapshot.json` 已生成
- [ ] `history_dashboard.html` 已生成

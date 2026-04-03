# Agent Prompt Templates

下面提供两种 prompt：**批量模式**（一次优化 N 个）和**循环模式**（持续优化直到用完或叫停）。

---

## 模式一：批量 Prompt

```
请你按照 @program.md 为我从 $MMQ_KERNELS 里选择 {{N}} 个未被优化的 Triton kernel 进行优化，并将结果提交到 KernelHub。

具体要求：

1. 选择 kernel
   - 通过 workspace/runs/ 和 history.db 确认哪些 kernel 已有优化分支，跳过它们
   - 优先选择结构简单、element-wise 或 memory-bound 的 kernel（更容易出成果）
   - 确认每个 kernel 有配套的 kernel_assets（kernel.py + reference.py），没有的话先制作

2. 工作流执行
   - 严格按 program.md 的 Step A → Step H 完整流程执行
   - 每个 kernel 用独立 worktree + 独立分支，互不干扰
   - 可并行启动多个 subagent 分别优化不同 kernel
   - 每个 subagent 必须在 AKO4ALL 工作目录内独立完成全部优化迭代

3. Subagent 行为约束（最重要，必须写入 subagent prompt）
   - 必须先读 TASK.md 和 HINTS.md，遵守其中所有规则
   - 每次迭代必须 commit，即使失败也要 commit 后 git revert
   - ⛔ 绝对禁止：git reset、git rebase、git commit --amend
   - 唯一允许的回退方式是 git revert
   - 告知 subagent：kernelhub sync-git 会做 reflog 扫描，违规会导致整个 run 被拒绝
   - 每个 commit body 必须包含结构化字段（kernel/agent/gpu/correctness/speedup_vs_baseline/latency_us/changes/analysis）

4. 优化策略指引（写入 subagent prompt 供参考）
   - Tier 1：修正 num_sms 为实际值、去掉 int64 offset、调 block_size/num_warps/num_stages
   - Tier 2：eviction_policy、make_block_ptr、persistent vs non-persistent grid
   - Tier 3：精度优化、向量化、寄存器压力
   - 卡住时跑 ncu 或 WebSearch 找新思路
   - 连续 5 次无提升就停止

5. 提交到 KernelHub
   - 所有优化完成后，对每个 run 执行 sync-git → archive-git → export
   - 如果 sync-git 报完整性警告，说明 subagent 违规了，必须修复后重新 sync
   - 最终确认 dashboard HTML 中能看到所有 run 的完整迭代历史

不要修改 third_party/ 下的任何内容。workspace 在项目目录下创建。
```

### 变量

| 变量 | 含义 | 示例 |
|---|---|---|
| `{{N}}` | 要优化的 kernel 数量 | `3`、`所有未优化的` |

---

## 模式二：循环 Prompt（持续优化）

```
请你按照 @program.md 持续从 $MMQ_KERNELS 里挑选未优化的 Triton kernel 进行优化，并将结果提交到 KernelHub。

执行模式：

每轮选 {{BATCH}} 个 kernel 并行优化（subagent），完成后立即 sync-git → archive-git → export，然后汇报本轮结果并自动开始下一轮，直到满足退出条件。

退出条件（满足任一即停）：
- 所有 Triton kernel 都已有优化记录
- 连续 {{MAX_SKIP}} 个 kernel 因复杂度过高（如依赖外部模块、有复杂控制流）被跳过
- 我主动说"停"

每轮开始前：
1. 查询 history.db 获取已优化 kernel 列表
2. 扫描 $MMQ_KERNELS/triton_kernels/ 获取全部 kernel
3. 差集即为待优化列表
4. 从待优化列表中选 {{BATCH}} 个（优先选简单、self-contained 的）
5. 跳过需要外部依赖（如 import 了 dsa 子目录）或签名过于复杂的 kernel，记录跳过原因

每轮结束后汇报：
- 本轮优化了哪些 kernel
- 各 kernel 的 baseline / best latency / speedup
- sync-git 是否全部通过完整性检查
- 剩余未优化 kernel 数量
- 累计统计（总优化数、总跳过数、平均 speedup）

具体要求：

1. 选择 kernel
   - 通过 workspace/runs/ 和 history.db 确认哪些 kernel 已有优化分支，跳过它们
   - 优先选择结构简单、element-wise 或 memory-bound 的 kernel（更容易出成果）
   - 确认每个 kernel 有配套的 kernel_assets（kernel.py + reference.py），没有的话先制作

2. 工作流执行
   - 严格按 program.md 的 Step A → Step H 完整流程执行
   - 每个 kernel 用独立 worktree + 独立分支，互不干扰
   - 每轮内可并行启动多个 subagent 分别优化不同 kernel
   - 每个 subagent 必须在 AKO4ALL 工作目录内独立完成全部优化迭代

3. Subagent 行为约束（最重要，必须写入 subagent prompt）
   - 必须先读 TASK.md 和 HINTS.md，遵守其中所有规则
   - 每次迭代必须 commit，即使失败也要 commit 后 git revert
   - ⛔ 绝对禁止：git reset、git rebase、git commit --amend
   - 唯一允许的回退方式是 git revert
   - 告知 subagent：kernelhub sync-git 会做 reflog 扫描，违规会导致整个 run 被拒绝
   - 每个 commit body 必须包含结构化字段（kernel/agent/gpu/correctness/speedup_vs_baseline/latency_us/changes/analysis）

4. 优化策略指引（写入 subagent prompt 供参考）
   - Tier 1：修正 num_sms 为实际值、去掉 int64 offset、调 block_size/num_warps/num_stages
   - Tier 2：eviction_policy、make_block_ptr、persistent vs non-persistent grid
   - Tier 3：精度优化、向量化、寄存器压力
   - 卡住时跑 ncu 或 WebSearch 找新思路
   - 连续 5 次无提升就停止

5. 提交到 KernelHub
   - 每轮所有优化完成后，对每个 run 执行 sync-git → archive-git → export
   - 如果 sync-git 报完整性警告，说明 subagent 违规了，必须修复后重新 sync
   - 确认 dashboard HTML 中能看到所有 run 的完整迭代历史

不要修改 third_party/ 下的任何内容。workspace 在项目目录下创建。
```

### 变量

| 变量 | 含义 | 建议值 |
|---|---|---|
| `{{BATCH}}` | 每轮并行优化数量 | `3`（受 GPU 显存和 context 长度限制） |
| `{{MAX_SKIP}}` | 连续跳过多少个就停 | `5` |

---

## 两种模式的区别

| | 批量模式 | 循环模式 |
|---|---|---|
| 控制方式 | 你指定数量 | AI 自动循环 |
| 适用场景 | 想做几个试试 | 想尽可能覆盖所有 kernel |
| 退出方式 | 做完就停 | 做完/跳过太多/你叫停 |
| 汇报频率 | 最后一次 | 每轮汇报 |
| 复杂 kernel 处理 | 你自己选 | AI 自动跳过并记录原因 |

## 共通的关键设计点

**为什么第 3 点要单独强调 git 规则？**

subagent 拿到的上下文是独立的，它不会自动继承你对主 agent 说的约束。如果主 agent 不把这些规则显式写入 subagent 的 prompt 里，subagent 就可能用 `git reset --hard` 来"清理"历史。

三层防线：
- **Prompt 层**：TASK.md 里有禁令表 + 主 agent prompt 里要求传达给 subagent
- **Hook 层**：`kernelhub prepare` 自动安装 `post-rewrite` hook
- **验证层**：`sync-git` 做 parent-chain + reflog 扫描，违规直接拒绝

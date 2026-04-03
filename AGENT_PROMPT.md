# Agent Prompt Templates

下面提供 Triton 和 CUDA C 两类 kernel 的优化 prompt 模板，每类分**批量模式**和**循环模式**，另有**混合模式**同时覆盖两类。

---

## 模式一：Triton 批量 Prompt

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
   - 每个 commit body 必须包含结构化字段（kernel/agent/gpu/backend/correctness/speedup_vs_baseline/latency_us/changes/analysis）

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

## 模式二：Triton 循环 Prompt（持续优化）

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
   - 每个 commit body 必须包含结构化字段（kernel/agent/gpu/backend/correctness/speedup_vs_baseline/latency_us/changes/analysis）

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

## 模式三：CUDA C 批量 Prompt

```
请你按照 @program.md 为我从 $MMQ_KERNELS/jit_kernels/ 里选择 {{N}} 个未被优化的 CUDA C kernel 进行优化，并将结果提交到 KernelHub。

⚠️ CUDA C 与 Triton 的关键区别：

A. kernel_assets 制作方式不同
   - Triton kernel 的 kernel.py 直接 import Python 函数
   - CUDA C kernel 通过 mmq_kernels.jit.jitter.call_jit 调用 .cuh 头文件
   - kernel.py 中的 ModelNew 需要用 torch.utils.cpp_extension.load_inline 编译 CUDA 源码
   - reference.py 中的 Model 使用对应的 Python 接口（调用原始 jit_kernels 函数）作为正确性基准

B. kernel.py 制作策略（按依赖复杂度二选一）

   mmq_kernels 中的 CUDA C kernel 分为两类，kernel.py 的写法不同：

   ■ 策略一：自包含 kernel（无外部依赖）— 直接内联源码

   以下 kernel 的 .cuh 不依赖 kittens/ThunderKittens 或其他 submodule，可以将源码
   直接内联到 kernel.py 的字符串中：

   | jit_kernels 文件        | .cuh 头文件                              | 外部依赖       |
   |-------------------------|------------------------------------------|----------------|
   | topk_to_multihot.py     | topk_to_multihot/topk_to_multihot.cuh    | 仅 <climits>   |
   | cumsum.py (cumsum)      | cumsum/cumsum.cuh                        | 无             |

   模板：

   ```python
   import torch
   import torch.nn as nn
   from torch.utils.cpp_extension import load_inline

   cuda_source = """
   // 直接内联 .cuh 的完整源码（因为不依赖任何外部头文件）
   #pragma once
   #include <climits>
   namespace mmq::kernels::topk_to_multihot {
   // ... 完整 kernel 代码 ...
   }
   """

   cpp_source = "torch::Tensor kernel_func(torch::Tensor input, ...);"

   module = load_inline(
       name="custom_kernel",
       cpp_sources=cpp_source,
       cuda_sources=cuda_source,
       functions=["kernel_func"],
       verbose=True,
   )

   class ModelNew(nn.Module):
       def __init__(self):
           super().__init__()
           self.custom_op = module

       def forward(self, *args):
           return self.custom_op.kernel_func(*args)
   ```

   ■ 策略二：依赖 kittens 的 kernel — 使用 extra_include_paths

   mmq_kernels 中绝大多数 CUDA C kernel 都直接或间接依赖 ThunderKittens（kittens.cuh）。
   这些 kernel 不能内联源码，但可以通过 load_inline 的 extra_include_paths 参数传递
   头文件搜索路径，让 nvcc 在编译时找到所有依赖。

   前提条件：ThunderKittens submodule 必须已初始化。如果
   $MMQ_KERNELS/../3rd/ThunderKittens/ 为空，需要先执行：
     cd $MMQ_KERNELS/.. && git submodule update --init 3rd/ThunderKittens
   或者直接使用共享目录中的副本。

   依赖 kittens 的 kernel 包括（不完整列表）：
   - add.py → hello/add.cuh（通过 utils/lcs.cuh → kittens.cuh）
   - cumsum.py (cumsum_cg) → cumsum/cumsum_cg.cuh（直接 kittens.cuh）
   - gemm.py → gemm/*.cuh（通过 utils/lcf.cuh → kittens.cuh）
   - swiglu.py → swiglu/*.cuh（直接 kittens.cuh）
   - permute.py → permute/*.cuh（直接 kittens.cuh）
   - unpermute.py → unpermute/*.cuh（直接 kittens.cuh）
   - fp8_permute.py → permute/fp8_permute*.cuh（直接 kittens.cuh）
   - permute_and_quant.py → permute/permute_and_quant.cuh（直接 kittens.cuh）
   - qkv_part_rope.py → rope/qkv_part_rope.cuh（直接 kittens.cuh）
   - varlen_qkv_part_rope.py → rope/varlen_qkv_part_rope.cuh（直接 kittens.cuh）
   - embedding.py → overencode/embedding_*.cuh（通过 utils/clc.cuh → kittens.cuh）
   - oe.py → overencode/oe_*.cuh（通过 utils/clc.cuh → kittens.cuh）
   - select_topk_grad.py → router/select_topk_grad.cuh（通过 topk_scheduler.cuh → kittens.cuh）
   - topk_with_expert_bias.py → router/topk_with_expert_bias*.cuh（通过 topk_scheduler.cuh → kittens.cuh）
   - group_limited_topk.py → router/group_limited_topk.cuh（直接 kittens.cuh）
   - act_quant_transpose_quant.py → act_quant_transpose_quant/*.cuh（直接 kittens.cuh）
   - swiglu_mul_probs.py → swiglu_mul_probs/*.cuh（直接 kittens.cuh）

   应跳过的 kernel（即使用了 extra_include_paths 也无法处理）：
   - blackwell_gemm.py, mxfp8_gemm.py, mxfp8_adamw.py, clc_test.py → 需要 sm100+ 架构
   - ulysses.py → 需要 nvshmem 多节点环境
   - gemm_multi_out.py → 需要 Blackwell 架构

   模板：

   ```python
   import os
   import torch
   import torch.nn as nn
   from torch.utils.cpp_extension import load_inline

   MMQ_ROOT = os.environ.get(
       "MMQ_KERNELS",
       "/home/chengqi/mimikyu/mmq_kernels/mmq_kernels"
   )
   INCLUDE_DIR = os.path.join(MMQ_ROOT, "include")
   TK_DIR = os.path.join(MMQ_ROOT, "..", "3rd", "ThunderKittens")
   TK_INCLUDE = os.path.join(TK_DIR, "include")
   TK_PROTO = os.path.join(TK_DIR, "prototype")

   cuda_source = """
   #include "kittens.cuh"
   #include "hello/add.cuh"   // 直接 #include 原始 .cuh，不内联

   // C wrapper 供 PyTorch 调用
   torch::Tensor add_cuda(torch::Tensor a, torch::Tensor b) {
       auto o = torch::empty_like(a);
       const int M = a.size(0), N = a.size(1);
       using namespace kittens;
       mmq::kernels::test::Add</* M, N, BLOCK_M, BLOCK_N */>::run(
           (bf16*)a.data_ptr<at::BFloat16>(),
           (bf16*)b.data_ptr<at::BFloat16>(),
           (bf16*)o.data_ptr<at::BFloat16>(),
           at::cuda::getCurrentCUDAStream(), 132
       );
       return o;
   }
   """

   cpp_source = "torch::Tensor add_cuda(torch::Tensor a, torch::Tensor b);"

   module = load_inline(
       name="custom_kernel",
       cpp_sources=cpp_source,
       cuda_sources=cuda_source,
       functions=["add_cuda"],
       extra_include_paths=[INCLUDE_DIR, TK_INCLUDE, TK_PROTO],
       extra_cuda_cflags=[
           "-O3", "-std=c++20",
           "-arch=compute_90a", "-code=sm_90a",  # Hopper; 按实际 GPU 调整
           "-DKITTENS_HOPPER",
           "--expt-relaxed-constexpr",
           "--extended-lambda",
       ],
       verbose=True,
   )

   class ModelNew(nn.Module):
       def __init__(self):
           super().__init__()
           self.custom_op = module

       def forward(self, a, b):
           return self.custom_op.add_cuda(a, b)
   ```

   关键点：
   - extra_include_paths 传入 mmq include 目录 + ThunderKittens 目录
   - extra_cuda_cflags 必须与 jitter.py 中的 _nvcc_args 保持一致
   - cuda_source 中用 #include 引用原始 .cuh，不复制源码
   - 优化时修改 cuda_source 中的 wrapper 或 launch 参数，不动 include/ 下原文件

C. bench.sh 使用 --backend cuda（不是 triton）

   ```bash
   python bench/kernelbench/bench.py \
     --kernel bench/kernelbench/kernel.py \
     --reference bench/kernelbench/reference.py \
     --backend cuda \
     --num-correct-trials 3 \
     --num-perf-trials 10 \
     --verbose
   ```

D. commit body 中 backend 字段必须写 cuda

   ```text
   kernel: <kernel_name>
   agent: <agent_id>
   gpu: <H800|B200|...>
   backend: cuda
   correctness: <PASS|FAIL>
   speedup_vs_baseline: <1.23x>
   latency_us: <45.6>
   changes: <...>
   analysis: <...>
   ```

E. 选择 kernel 的优先级
   1. 先选自包含 kernel（topk_to_multihot、cumsum）— 最简单
   2. 再选依赖 kittens 但结构简单的 kernel（select_topk_grad、permute 等）
   3. 跳过 Blackwell 专用 kernel（需要 sm100 架构）
   4. 跳过 nvshmem 相关的 kernel（需要多节点环境）

具体要求：

1. 选择 kernel
   - 通过 workspace/runs/ 和 history.db 确认哪些 kernel 已有优化分支，跳过它们
   - 检查目标 .cuh 的 #include 依赖链，判断用策略一（内联）还是策略二（include paths）
   - 如果用策略二，先确认 ThunderKittens submodule 已初始化
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
   - 每个 commit body 必须包含结构化字段（kernel/agent/gpu/backend/correctness/speedup_vs_baseline/latency_us/changes/analysis）

4. CUDA C 优化策略指引（写入 subagent prompt 供参考）
   - Tier 1：线程块大小（blockDim）、grid 维度、shared memory 大小
   - Tier 2：向量化访存（float4/int4 loads）、warp shuffle、shared memory bank conflict 消除
   - Tier 3：寄存器压力（__launch_bounds__）、指令级并行（ILP）、循环展开（#pragma unroll）
   - Tier 4：异步拷贝（cp.async / memcpy_async）、TMA、pipeline（多阶段 prefetch）
   - 卡住时跑 ncu 或 WebSearch 找新思路
   - 连续 5 次无提升就停止

5. 提交到 KernelHub
   - 所有优化完成后，对每个 run 执行 sync-git → archive-git → export
   - 如果 sync-git 报完整性警告，说明 subagent 违规了，必须修复后重新 sync
   - 最终确认 dashboard HTML 中能看到所有 run 的完整迭代历史

不要修改 third_party/ 下的任何内容，也不要修改 $MMQ_KERNELS/include/ 下的原始 .cuh 文件。
workspace 在项目目录下创建。
```

### 变量

| 变量 | 含义 | 示例 |
|---|---|---|
| `{{N}}` | 要优化的 kernel 数量 | `3`、`所有未优化的` |

---

## 模式四：CUDA C 循环 Prompt（持续优化）

```
请你按照 @program.md 持续从 $MMQ_KERNELS/jit_kernels/ 里挑选未优化的 CUDA C kernel 进行优化，并将结果提交到 KernelHub。

⚠️ CUDA C kernel.py 制作策略（按依赖复杂度二选一，必须遵守）：

■ 自包含 kernel（.cuh 不依赖 kittens）→ 直接内联源码到 kernel.py 字符串中
  仅有两个：topk_to_multihot（topk_to_multihot.cuh）、cumsum（cumsum.cuh）

■ 依赖 kittens 的 kernel → 使用 load_inline 的 extra_include_paths 传递头文件路径
  cuda_source 中用 #include 引用原始 .cuh，不复制源码。
  extra_include_paths 需包含：
    - $MMQ_KERNELS/include/（mmq 自有头文件）
    - $MMQ_KERNELS/../3rd/ThunderKittens/include/（kittens.cuh）
    - $MMQ_KERNELS/../3rd/ThunderKittens/prototype/
  extra_cuda_cflags 需匹配 jitter.py 的编译参数：
    -O3 -std=c++20 -arch=compute_90a -code=sm_90a -DKITTENS_HOPPER
    --expt-relaxed-constexpr --extended-lambda

  前提：ThunderKittens submodule 已初始化（3rd/ThunderKittens/ 非空）。

应跳过的 kernel（无法在当前环境编译）：
- blackwell_gemm.py, mxfp8_gemm.py, mxfp8_adamw.py, clc_test.py, gemm_multi_out.py → 需要 sm100+
- ulysses.py → 需要 nvshmem 多节点环境

其他规则：
- bench.sh 使用 --backend cuda
- commit body 中 backend 字段写 cuda
- 优化时修改 kernel.py 中的 cuda_source（launch 参数、算法），不动 include/ 下原文件

执行模式：

每轮选 {{BATCH}} 个 kernel 并行优化（subagent），完成后立即 sync-git → archive-git → export，然后汇报本轮结果并自动开始下一轮，直到满足退出条件。

退出条件（满足任一即停）：
- 所有可优化的 CUDA C kernel 都已有优化记录
- 连续 {{MAX_SKIP}} 个 kernel 因依赖复杂或架构不支持被跳过
- 我主动说"停"

每轮开始前：
1. 查询 history.db 获取已优化 kernel 列表
2. 扫描 $MMQ_KERNELS/jit_kernels/ 获取全部 CUDA C kernel（使用 call_jit 的 .py 文件）
3. 对每个候选 kernel，检查 $MMQ_KERNELS/include/ 下的 .cuh 头文件依赖链
4. 排除 Blackwell/nvshmem kernel，判断其余 kernel 用策略一还是策略二
5. 差集即为待优化列表，从中选 {{BATCH}} 个

每轮结束后汇报：
- 本轮优化了哪些 kernel
- 各 kernel 的 baseline / best latency / speedup
- sync-git 是否全部通过完整性检查
- 剩余未优化 kernel 数量
- 累计统计（总优化数、总跳过数、平均 speedup）

具体要求：

1. 选择 kernel
   - 通过 workspace/runs/ 和 history.db 确认哪些 kernel 已有优化分支，跳过它们
   - 检查目标 .cuh 的 #include 依赖链，判断用策略一还是策略二
   - 优先选择自包含 kernel，然后选依赖 kittens 但结构简单的 kernel
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
   - 每个 commit body 必须包含结构化字段（kernel/agent/gpu/backend/correctness/speedup_vs_baseline/latency_us/changes/analysis）

4. CUDA C 优化策略指引（写入 subagent prompt 供参考）
   - Tier 1：线程块大小（blockDim）、grid 维度、shared memory 大小
   - Tier 2：向量化访存（float4/int4 loads）、warp shuffle、shared memory bank conflict 消除
   - Tier 3：寄存器压力（__launch_bounds__）、指令级并行（ILP）、循环展开（#pragma unroll）
   - Tier 4：异步拷贝（cp.async / memcpy_async）、TMA、pipeline（多阶段 prefetch）
   - 卡住时跑 ncu 或 WebSearch 找新思路
   - 连续 5 次无提升就停止

5. 提交到 KernelHub
   - 每轮所有优化完成后，对每个 run 执行 sync-git → archive-git → export
   - 如果 sync-git 报完整性警告，说明 subagent 违规了，必须修复后重新 sync
   - 确认 dashboard HTML 中能看到所有 run 的完整迭代历史

不要修改 third_party/ 下的任何内容，也不要修改 $MMQ_KERNELS/include/ 下的原始 .cuh 文件。
workspace 在项目目录下创建。
```

### 变量

| 变量 | 含义 | 建议值 |
|---|---|---|
| `{{BATCH}}` | 每轮并行优化数量 | `2`（CUDA C 编译慢，建议少于 Triton） |
| `{{MAX_SKIP}}` | 连续跳过多少个就停 | `5` |

---

## 模式五：混合 Prompt（Triton + CUDA C 同时扫描）

```
请你按照 @program.md 持续从 $MMQ_KERNELS 里挑选未优化的 kernel 进行优化，并将结果提交到 KernelHub。同时扫描 Triton kernel（triton_kernels/）和 CUDA C kernel（jit_kernels/）。

选择规则：
- Triton kernel：从 triton_kernels/ 选择，kernel.py 直接 import Python 函数，bench 用 --backend triton
- CUDA C kernel：从 jit_kernels/ 选择，bench 用 --backend cuda
  - 自包含 kernel（topk_to_multihot、cumsum）：kernel.py 直接内联 .cuh 源码
  - 依赖 kittens 的 kernel：kernel.py 用 #include + extra_include_paths 引用原始 .cuh
    （需 ThunderKittens submodule 已初始化，extra_cuda_cflags 匹配 jitter.py 的编译参数）
  - 跳过 Blackwell（sm100+）和 nvshmem kernel
- 每个 kernel 的 commit body 中 backend 字段必须准确填写（triton 或 cuda）
- 优先选简单、self-contained 的 kernel

其余要求同模式二（循环模式），包括：
- 退出条件、每轮汇报格式
- subagent 行为约束（git 规则、commit body 字段）
- 优化策略指引：Triton kernel 用 Triton Tier 1-3，CUDA C kernel 用 CUDA Tier 1-4
- sync-git → archive-git → export 流程

不要修改 third_party/ 下的任何内容，也不要修改 $MMQ_KERNELS/include/ 下的原始 .cuh 文件。
workspace 在项目目录下创建。
```

---

## 共通的关键设计点

**为什么第 3 点要单独强调 git 规则？**

subagent 拿到的上下文是独立的，它不会自动继承你对主 agent 说的约束。如果主 agent 不把这些规则显式写入 subagent 的 prompt 里，subagent 就可能用 `git reset --hard` 来"清理"历史。

三层防线：
- **Prompt 层**：TASK.md 里有禁令表 + 主 agent prompt 里要求传达给 subagent
- **Hook 层**：`kernelhub prepare` 自动安装 `post-rewrite` hook
- **验证层**：`sync-git` 做 parent-chain + reflog 扫描，违规直接拒绝

---

## CUDA C kernel 依赖分类速查表

| 分类 | kernel | .cuh | 依赖 | kernel.py 策略 |
|---|---|---|---|---|
| 自包含 | topk_to_multihot | topk_to_multihot/topk_to_multihot.cuh | 仅 `<climits>` | 内联源码 |
| 自包含 | cumsum | cumsum/cumsum.cuh | 无 | 内联源码 |
| kittens | add | hello/add.cuh | utils/lcs.cuh → kittens | extra_include_paths |
| kittens | cumsum_cg | cumsum/cumsum_cg.cuh | kittens (laneid/warpid) | extra_include_paths |
| kittens | gemm (6 variants) | gemm/*.cuh | utils/lcf.cuh → kittens | extra_include_paths |
| kittens | swiglu (3 variants) | swiglu/*.cuh | kittens | extra_include_paths |
| kittens | swiglu_mul_probs (4 variants) | swiglu_mul_probs/*.cuh | kittens | extra_include_paths |
| kittens | permute (2 variants) | permute/permute*.cuh | kittens | extra_include_paths |
| kittens | unpermute (2 variants) | unpermute/*.cuh | kittens | extra_include_paths |
| kittens | fp8_permute (2 variants) | permute/fp8_permute*.cuh | kittens | extra_include_paths |
| kittens | permute_and_quant | permute/permute_and_quant.cuh | kittens | extra_include_paths |
| kittens | qkv_part_rope | rope/qkv_part_rope.cuh | kittens | extra_include_paths |
| kittens | varlen_qkv_part_rope | rope/varlen_qkv_part_rope.cuh | kittens | extra_include_paths |
| kittens | embedding (fwd+bwd) | overencode/embedding_*.cuh | utils/clc.cuh → kittens | extra_include_paths |
| kittens | oe (fwd+bwd) | overencode/oe_*.cuh | utils/clc.cuh → kittens | extra_include_paths |
| kittens | select_topk_grad | router/select_topk_grad.cuh | topk_scheduler → kittens | extra_include_paths |
| kittens | topk_with_expert_bias (2v) | router/topk_with_expert_bias*.cuh | topk_scheduler → kittens | extra_include_paths |
| kittens | group_limited_topk | router/group_limited_topk.cuh | kittens | extra_include_paths |
| kittens | act_quant_transpose_quant | act_quant_transpose_quant/*.cuh | kittens | extra_include_paths |
| ⛔ 跳过 | blackwell_gemm, mxfp8_* | blackwell/*.cuh | sm100+ 架构 | N/A |
| ⛔ 跳过 | gemm_multi_out | blackwell/*.cuh | sm100+ 架构 | N/A |
| ⛔ 跳过 | clc_test | blackwell/clc_test.cuh | sm100+ 架构 | N/A |
| ⛔ 跳过 | mxfp8_adamw | blackwell/mxfp8_adamw.cuh | sm100+ 架构 | N/A |
| ⛔ 跳过 | ulysses | ap/ulysses.cuh | nvshmem 多节点 | N/A |
| 无价值 | hello | hello/hello.cuh | `<iostream>` | N/A（仅打印测试） |

## Triton 与 CUDA C 对照表

| 维度 | Triton | CUDA C（自包含） | CUDA C（依赖 kittens） |
|---|---|---|---|
| 源码位置 | `triton_kernels/*.py` | `jit_kernels/*.py` + `include/**/*.cuh` | 同左 |
| kernel.py 加载方式 | `import` Python 函数 | `load_inline`（内联源码） | `load_inline`（#include + extra_include_paths） |
| 外部依赖 | 无 | 无 | ThunderKittens submodule |
| bench.sh `--backend` | `triton` | `cuda` | `cuda` |
| commit body `backend` | `triton` | `cuda` | `cuda` |
| 编译速度 | 快（JIT） | 中（nvcc） | 慢（nvcc + 大量头文件） |
| 可优化范围 | 全部 Triton kernel | 仅 2 个 | 约 20 个（排除 Blackwell/nvshmem） |
| 优化手段 | block_size/warps/stages/eviction | blockDim/smem/vectorize/unroll | 同左 + kittens API 层面调优 |
| 建议并行数 | 3 | 2 | 1-2（编译慢，头文件多） |

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

B. kernel.py 制作策略（核心原则：复制源码到 cuda_source，让 agent 可以修改算法）

   与 Triton 优化类似，CUDA C 优化的关键是让 agent 能修改 kernel 算法本身，
   而不仅仅是 launch 参数。做法是：将目标 .cuh 的核心源码**复制**到 kernel.py
   的 cuda_source 字符串中，agent 在这个副本上迭代优化，原始 .cuh 不受影响。

   mmq_kernels 中的 CUDA C kernel 分为两类，制作细节不同：

   ■ 策略一：自包含 kernel（无外部依赖）— 纯内联，无需 extra_include_paths

   以下 kernel 的 .cuh 不依赖 kittens/ThunderKittens，直接复制源码即可：

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
   // ========== 从 cumsum.cuh 复制的完整源码（agent 可自由修改） ==========
   #pragma once
   namespace mmq::kernels {

   static __device__ int32_t load_global_volatile(volatile int32_t *lock) { ... }

   template <uint32_t cols, uint32_t num_warps>
   static __global__ void __launch_bounds__(num_warps * 32, 1)
       cumsum_kernel(volatile int32_t *output, volatile int32_t *x, ...) {
       // ... agent 可以修改这里的算法：访存模式、循环结构、同步策略 ...
   }

   template <uint32_t cols, uint32_t num_warps> struct CumSum {
       static void run(...) { cumsum_kernel<cols, num_warps><<<...>>>(...);}
   };
   } // namespace

   // ========== C wrapper ==========
   torch::Tensor cumsum_cuda(torch::Tensor x, int num_sms) {
       auto output = torch::empty_like(x);
       auto lock = torch::zeros({num_sms}, x.options().dtype(torch::kInt32));
       mmq::kernels::CumSum</* cols, num_warps */>::run(
           output.data_ptr<int32_t>(), x.data_ptr<int32_t>(),
           lock.data_ptr<int32_t>(), x.size(0),
           at::cuda::getCurrentCUDAStream(), num_sms
       );
       return output;
   }
   """

   cpp_source = "torch::Tensor cumsum_cuda(torch::Tensor x, int num_sms);"

   module = load_inline(
       name="custom_kernel",
       cpp_sources=cpp_source,
       cuda_sources=cuda_source,
       functions=["cumsum_cuda"],
       verbose=True,
   )

   class ModelNew(nn.Module):
       def __init__(self):
           super().__init__()
           self.custom_op = module
       def forward(self, x):
           return self.custom_op.cumsum_cuda(x, 132)
   ```

   ■ 策略二：依赖 kittens 的 kernel — 复制 .cuh 源码 + extra_include_paths 提供依赖

   核心思路：把目标 .cuh（如 select_topk_grad.cuh）的源码**复制**到 cuda_source 中，
   agent 可以自由修改里面的算法。但它依赖的上游头文件（kittens.cuh、topk_scheduler.cuh
   等）仍通过 extra_include_paths + #include 引用，不需要复制。

   这样 agent 能改的是：
   - kernel 函数体内的算法（访存模式、warp 分工、循环展开、向量化等）
   - launch 参数（blockDim、grid、shared memory）
   - __launch_bounds__ 参数
   不能改的是：
   - kittens 库本身（kittens.cuh）
   - 上游工具类（topk_scheduler.cuh 等，除非也复制进来）

   前提条件：ThunderKittens submodule 必须已初始化。如果
   $MMQ_KERNELS/../3rd/ThunderKittens/ 为空，需要先执行：
     cd $MMQ_KERNELS/.. && git submodule update --init 3rd/ThunderKittens
   或者直接使用共享目录中的副本。

   模板（以 select_topk_grad 为例）：

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
   // 上游依赖通过 #include 引用（不修改）
   #include "kittens.cuh"
   #include "router/topk_scheduler.cuh"
   #include <cstdint>

   // ========== 从 select_topk_grad.cuh 复制的源码（agent 可自由修改） ==========
   namespace mmq::kernels::router {

   template <uint32_t K>
   static __device__ uint32_t get_k_select(uint32_t *indices, uint32_t fea_idx) {
       #pragma unroll
       for (uint32_t i = 0; i < K; ++i) {
           if (indices[i] == fea_idx) return i;
       }
       return K;
   }

   template <uint32_t FeaDim, uint32_t K, uint32_t NWarps>
   static void __global__ select_topk_grad_optimized(
       const float *topk_values_grad,
       const int64_t *topk_indices,
       float *scores_grad,
       int32_t n_rows) {
       // *** agent 在这里修改算法 ***
       // 例如：向量化 load/store、改 warp 调度、加 shared memory 缓存
       TopKScheduler<NWarps> scheduler(n_rows);
       while (scheduler.valid()) {
           // ... 原始逻辑或 agent 的新实现 ...
           scheduler.next();
       }
   }

   } // namespace

   // ========== C wrapper ==========
   torch::Tensor select_topk_grad_cuda(
       torch::Tensor topk_values_grad,
       torch::Tensor topk_indices,
       int n_rows, int num_sms) {
       constexpr int FeaDim = 256, K = 8, NWarps = 4;
       auto scores_grad = torch::zeros({n_rows, FeaDim}, topk_values_grad.options());
       mmq::kernels::router::select_topk_grad_optimized<FeaDim, K, NWarps>
           <<<num_sms, NWarps * 32>>>(
               topk_values_grad.data_ptr<float>(),
               topk_indices.data_ptr<int64_t>(),
               scores_grad.data_ptr<float>(),
               n_rows
           );
       return scores_grad;
   }
   """

   cpp_source = """
   torch::Tensor select_topk_grad_cuda(
       torch::Tensor topk_values_grad,
       torch::Tensor topk_indices,
       int n_rows, int num_sms);
   """

   module = load_inline(
       name="custom_kernel",
       cpp_sources=cpp_source,
       cuda_sources=cuda_source,
       functions=["select_topk_grad_cuda"],
       extra_include_paths=[INCLUDE_DIR, TK_INCLUDE, TK_PROTO],
       extra_cuda_cflags=[
           "-O3", "-std=c++20",
           "-arch=compute_90a", "-code=sm_90a",
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
       def forward(self, topk_values_grad, topk_indices):
           n_rows = topk_values_grad.size(0)
           return self.custom_op.select_topk_grad_cuda(
               topk_values_grad, topk_indices, n_rows, 132)
   ```

   关键点：
   - 目标 kernel 的源码**复制**到 cuda_source 中，agent 可以自由修改算法
   - 上游依赖（kittens.cuh、topk_scheduler.cuh 等）通过 #include 引用，不复制
   - extra_include_paths 提供 mmq include + ThunderKittens 路径
   - extra_cuda_cflags 必须与 jitter.py 中的 _nvcc_args 保持一致
   - 不修改 $MMQ_KERNELS/include/ 下的任何原始文件
   - 如果上游工具类（如 topk_scheduler.cuh）也需要修改，可以一并复制到 cuda_source 中

   依赖 kittens 的 kernel 完整列表见文末「CUDA C kernel 依赖分类速查表」。

   应跳过的 kernel（当前环境无法编译）：
   - blackwell_gemm.py, mxfp8_gemm.py, mxfp8_adamw.py, clc_test.py, gemm_multi_out.py → 需要 sm100+
   - ulysses.py → 需要 nvshmem 多节点环境

   ■ 参考资料：PTX ISA 和 CUDA API 文档

   优化 CUDA C kernel 时，可以查阅 third_party/ptx-isa-markdown/cuda_skill/references/ 目录：
   - PTX 指令语义（ld.global.cs、cp.async 等）→ references/ptx-docs/
   - ncu 性能指标解读 → references/ncu-guide.md
   - nsys 时间线分析 → references/nsys-guide.md
   - 常见性能陷阱（bank conflict、coalescing）→ references/performance-traps.md

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

⚠️ CUDA C kernel.py 制作策略（核心原则：复制源码，让 agent 能改算法）：

■ 目标 .cuh 的核心源码必须**复制**到 kernel.py 的 cuda_source 字符串中，
  agent 在这个副本上迭代优化（修改算法、访存模式、launch 参数等），
  原始 .cuh 文件不受影响。这和 Triton 优化修改 kernel.py 中的 @triton.jit
  函数是同样的道理。

■ 自包含 kernel（topk_to_multihot、cumsum）→ 纯内联，无需 extra_include_paths
■ 依赖 kittens 的 kernel → 复制目标 .cuh 源码到 cuda_source +
  extra_include_paths 提供上游依赖（kittens.cuh 等通过 #include 引用，不复制）
  extra_include_paths 需包含：
    - $MMQ_KERNELS/include/
    - $MMQ_KERNELS/../3rd/ThunderKittens/include/
    - $MMQ_KERNELS/../3rd/ThunderKittens/prototype/
  extra_cuda_cflags 需匹配 jitter.py 的编译参数：
    -O3 -std=c++20 -arch=compute_90a -code=sm_90a -DKITTENS_HOPPER
    --expt-relaxed-constexpr --extended-lambda
  前提：ThunderKittens submodule 已初始化（3rd/ThunderKittens/ 非空）。

应跳过的 kernel：
- blackwell_gemm.py, mxfp8_gemm.py, mxfp8_adamw.py, clc_test.py, gemm_multi_out.py → sm100+
- ulysses.py → nvshmem 多节点环境

其他规则：
- bench.sh 使用 --backend cuda
- commit body 中 backend 字段写 cuda
- 优化时修改 kernel.py 中 cuda_source 里的 kernel 算法，不动 $MMQ_KERNELS/include/ 下原文件
- 需要查 PTX 指令时搜索 third_party/ptx-isa-markdown/cuda_skill/references/ptx-docs/
- 需要查 ncu 指标时搜索 third_party/ptx-isa-markdown/cuda_skill/references/ncu-guide.md

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
  - 将目标 .cuh 源码**复制**到 kernel.py 的 cuda_source 中（agent 可修改算法）
  - 自包含 kernel（topk_to_multihot、cumsum）：纯内联即可
  - 依赖 kittens 的 kernel：复制目标 .cuh + extra_include_paths 提供上游依赖
    （需 ThunderKittens submodule 已初始化，extra_cuda_cflags 匹配 jitter.py 的编译参数）
  - 跳过 Blackwell（sm100+）和 nvshmem kernel
  - 需要查 PTX 指令时搜索 third_party/ptx-isa-markdown/cuda_skill/references/
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
| 自包含 | topk_to_multihot | topk_to_multihot/topk_to_multihot.cuh | 仅 `<climits>` | 复制源码到 cuda_source（纯内联） |
| 自包含 | cumsum | cumsum/cumsum.cuh | 无 | 复制源码到 cuda_source（纯内联） |
| kittens | add | hello/add.cuh | utils/lcs.cuh → kittens | 复制源码 + extra_include_paths |
| kittens | cumsum_cg | cumsum/cumsum_cg.cuh | kittens (laneid/warpid) | 复制源码 + extra_include_paths |
| kittens | gemm (6 variants) | gemm/*.cuh | utils/lcf.cuh → kittens | 复制源码 + extra_include_paths |
| kittens | swiglu (3 variants) | swiglu/*.cuh | kittens | 复制源码 + extra_include_paths |
| kittens | swiglu_mul_probs (4 variants) | swiglu_mul_probs/*.cuh | kittens | 复制源码 + extra_include_paths |
| kittens | permute (2 variants) | permute/permute*.cuh | kittens | 复制源码 + extra_include_paths |
| kittens | unpermute (2 variants) | unpermute/*.cuh | kittens | 复制源码 + extra_include_paths |
| kittens | fp8_permute (2 variants) | permute/fp8_permute*.cuh | kittens | 复制源码 + extra_include_paths |
| kittens | permute_and_quant | permute/permute_and_quant.cuh | kittens | 复制源码 + extra_include_paths |
| kittens | qkv_part_rope | rope/qkv_part_rope.cuh | kittens | 复制源码 + extra_include_paths |
| kittens | varlen_qkv_part_rope | rope/varlen_qkv_part_rope.cuh | kittens | 复制源码 + extra_include_paths |
| kittens | embedding (fwd+bwd) | overencode/embedding_*.cuh | utils/clc.cuh → kittens | 复制源码 + extra_include_paths |
| kittens | oe (fwd+bwd) | overencode/oe_*.cuh | utils/clc.cuh → kittens | 复制源码 + extra_include_paths |
| kittens | select_topk_grad | router/select_topk_grad.cuh | topk_scheduler → kittens | 复制源码 + extra_include_paths |
| kittens | topk_with_expert_bias (2v) | router/topk_with_expert_bias*.cuh | topk_scheduler → kittens | 复制源码 + extra_include_paths |
| kittens | group_limited_topk | router/group_limited_topk.cuh | kittens | 复制源码 + extra_include_paths |
| kittens | act_quant_transpose_quant | act_quant_transpose_quant/*.cuh | kittens | 复制源码 + extra_include_paths |
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
| kernel.py 中 agent 改什么 | `@triton.jit` kernel 函数体 | 复制到 `cuda_source` 中的 kernel 算法 | 同左（上游依赖通过 `#include` 引用） |
| kernel.py 加载方式 | `import` Python 函数 | `load_inline`（内联源码） | `load_inline`（复制目标源码 + extra_include_paths） |
| 外部依赖 | 无 | 无 | ThunderKittens submodule |
| bench.sh `--backend` | `triton` | `cuda` | `cuda` |
| commit body `backend` | `triton` | `cuda` | `cuda` |
| 编译速度 | 快（JIT） | 中（nvcc） | 慢（nvcc + 大量头文件） |
| 可优化范围 | 全部 Triton kernel | 2 个 | 约 20 个（排除 Blackwell/nvshmem） |
| 优化手段 | block_size/warps/stages/eviction | 算法改写 + blockDim/smem/vectorize/unroll | 同左 + kittens API 层面调优 |
| 参考资料 | — | PTX ISA + ncu guide | 同左 |
| 建议并行数 | 3 | 2 | 1-2（编译慢，头文件多） |

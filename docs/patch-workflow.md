# Patch Workflow: 从优化结果到框架 Patch

本文档说明如何将 KernelHub 优化产出的 kernel 转化为可直接应用到上游框架（MMQ/mimikyu）的 patch。

## 前置条件

1. **已完成至少一次优化 run**，且 `history.db` 中存在 `correctness=PASS` 的记录
2. **MMQ 源码**可访问（`mmq_kernels/mmq_kernels`，包含 `triton_kernels/`、`jit_kernels/`、`include/`）
3. **kernel-adapter 已安装**：

```bash
# 方式 1：uv（推荐）
cd third_party/kernel-adapter && uv sync

# 方式 2：pip
pip install -e third_party/kernel-adapter
```

验证安装：

```bash
kernel-adapter --help
```

## 整体流程

```
history.db          kernel_assets/       MMQ 源码
    │                    │                  │
    └──── kernelhub patch ─────────────────┘
              │
              ▼
    workspace/patches/<run-id>/
    ├── patch.diff            ← 可直接 git apply 的 unified diff
    ├── metadata.json         ← run_id, speedup, kernel_name, commit 等
    └── kernel_optimized.py   ← 优化后的 kernel 源码（供参考）
```

## Step 1: 批量生成 Patch

```bash
./bin/kernelhub patch \
  --db-path ./workspace/history.db \
  --kernel-assets ./workspace/kernel_assets \
  --mmq-root ~/mimikyu/mmq_kernels/mmq_kernels \
  --runs-dir ./workspace/runs \
  --output-dir ./workspace/patches
```

该命令会：

1. 查询 `history.db`，为每个 run 找到 **最高 speedup 且 correctness=PASS** 的 iteration
2. 定位优化后的 `solution/kernel.py`
3. 调用 `kernel-adapter batch-patch`，对比优化代码与原始框架源码，生成 unified diff
4. 自动验证 patch 是否能干净应用（`git apply --check`）
5. 当多个 run 优化了同一个框架文件时，**只保留 speedup 最高的 patch**

### 输出示例

```
  OK   run-rms-norm-001 (rms_norm, triton) speedup=1.83x
  OK   run-residual-bwd-001 (residual_backward, triton) speedup=1.45x
  SKIP run-rms-norm-002 (rms_norm, triton): superseded by higher-speedup patch

2 patch(es) generated, 1 skipped, 0 error(s)
```

## Step 2: 检查生成的 Patch

查看某个 run 的 patch 内容：

```bash
cat workspace/patches/run-rms-norm-001/patch.diff
```

查看元信息：

```bash
cat workspace/patches/run-rms-norm-001/metadata.json
```

`metadata.json` 包含：

```json
{
  "run_id": "run-rms-norm-001",
  "kernel_name": "rms_norm",
  "backend": "triton",
  "speedup": 1.83,
  "latency_us": 45.6,
  "commit_hash": "abc123...",
  "iteration": 7,
  "files": [
    {"path": "triton_kernels/rms_norm.py", "description": "Update Triton kernels"}
  ]
}
```

## Step 3: 验证 Patch

独立验证 patch 是否能应用到 MMQ 仓库（不修改任何文件）：

```bash
# 方式 1：通过 kernelhub（会自动推断 MMQ repo 路径）
./bin/kernelhub patch \
  --db-path ./workspace/history.db \
  --mmq-root ~/mimikyu/mmq_kernels/mmq_kernels \
  --verify

# 方式 2：手动 git apply --check
cd ~/mimikyu
git apply --check workspace/patches/run-rms-norm-001/patch.diff
```

如果 patch 是相对于 `mmq_kernels/mmq_kernels` 子目录生成的，需要指定 `--directory`：

```bash
cd ~/mimikyu
git apply --check --directory=mmq_kernels/mmq_kernels \
  /path/to/workspace/patches/run-rms-norm-001/patch.diff
```

## Step 4: 应用 Patch

### 方式 A：通过 kernelhub 自动应用

```bash
./bin/kernelhub patch \
  --db-path ./workspace/history.db \
  --mmq-root ~/mimikyu/mmq_kernels/mmq_kernels \
  --apply
```

`--apply` 会在 MMQ 仓库中为每个 kernel 创建 `opt/<kernel_name>` 分支并应用 patch。

### 方式 B：手动应用到指定分支

```bash
cd ~/mimikyu

# 创建优化分支
git checkout -b opt/rms_norm

# 应用 patch
git apply workspace/patches/run-rms-norm-001/patch.diff

# 检查修改
git diff --stat

# 提交
git add -A
git commit -m "perf(rms_norm): apply KernelHub optimization (1.83x speedup)"
```

### 方式 C：通过 kernel-adapter CLI 应用

```bash
kernel-adapter patch-apply \
  --patch-file workspace/patches/run-rms-norm-001/patch.diff \
  --target-repo ~/mimikyu \
  --kernel-name rms_norm \
  --dry-run  # 先 dry-run 确认，去掉此行即正式应用
```

## 单个 Kernel 生成 Patch

如果只想为某个特定 kernel 生成 patch（不走 batch 流程）：

```bash
kernel-adapter patch-gen \
  --source mmq \
  --original ~/mimikyu/mmq_kernels/mmq_kernels/triton_kernels/rms_norm.py \
  --optimized ./workspace/runs/run-rms-norm-001/ako/solution/kernel.py \
  --kernel-name rms_norm \
  --backend triton \
  --output ./patches/rms_norm.diff
```

CUDA kernel 需要额外指定 `--include-file`（.cuh 头文件路径）：

```bash
kernel-adapter patch-gen \
  --source mmq \
  --original ~/mimikyu/mmq_kernels/mmq_kernels/jit_kernels/unpermute.py \
  --optimized ./workspace/runs/run-cuda-unpermute-001/ako/solution/kernel.py \
  --kernel-name unpermute \
  --backend cuda \
  --include-file ~/mimikyu/mmq_kernels/mmq_kernels/include/unpermute/unpermute.cuh \
  --output ./patches/unpermute.diff
```

## Patch 的结构说明

### Triton Kernel Patch

对于 Triton kernel，patch 修改的是 `triton_kernels/<name>.py`。kernel-adapter 会：

1. 从优化后的 `kernel.py` 中提取所有 `@triton.jit` 函数
2. 按函数名匹配，替换原始文件中的对应函数
3. 如果 kernel 签名发生变化（增删参数），自动更新 wrapper 函数中的调用
4. 如果 grid 策略发生变化（persistent → per-block），标注 WARNING 提醒手动检查

示例 diff：

```diff
--- a/triton_kernels/rms_norm.py
+++ b/triton_kernels/rms_norm.py
@@ -12,8 +12,10 @@
 @triton.jit
-def rms_norm_kernel(x_ptr, gamma_ptr, out_ptr, M, N, ...):
-    # original implementation
+def rms_norm_kernel(x_ptr, gamma_ptr, out_ptr, M, N, ...):
+    # optimized: larger tile size, vectorized loads
+    BLOCK_SIZE: tl.constexpr = 2048
+    ...
```

### CUDA Kernel Patch

对于 CUDA kernel，patch 可能修改两个文件：

1. **`include/<subdir>/<name>.cuh`** — CUDA kernel 实现
2. **`jit_kernels/<name>.py`** — Python wrapper（launch 配置、模板参数）

kernel-adapter 会：

1. 从优化后的 `kernel.py` 的 `cuda_source` 字符串中提取 kernel 函数
2. 与原始 `.cuh` 对比，生成 diff
3. 检测 launch 参数变化（`num_warps`、模板参数等），更新 `.py` wrapper
4. 检测 kernel 函数重命名，自动同步到 struct dispatch 和 Python 调用

## 常见问题

### Patch 应用失败（冲突）

如果 MMQ 源码自优化以来有过修改，patch 可能无法干净应用：

```bash
# 查看冲突
git apply --check --verbose patch.diff

# 尝试 3-way merge
git apply --3way patch.diff
```

### 多个 Run 修改同一文件

`kernelhub patch` 会自动检测冲突并只保留 speedup 最高的 patch。输出中会显示：

```
CONFLICT WARNING: multiple patches target the same file(s):
  triton_kernels/rms_norm.py: run-rms-norm-001(1.83x), run-rms-norm-002(1.45x)
    -> keeping best: run-rms-norm-001
```

### 验证优化后的 kernel 仍然正确

应用 patch 后，建议在框架内重新运行对应的测试：

```bash
cd ~/mimikyu
python -m pytest tests/ -k "rms_norm" -v
```

### 回滚 patch

```bash
cd ~/mimikyu
git apply --reverse patch.diff
# 或者直接
git checkout -- triton_kernels/rms_norm.py
```

## 完整端到端示例

```bash
# 1. 确保有优化结果
./bin/kernelhub export --db-path ./workspace/history.db \
  --html-out ./workspace/dashboard.html

# 2. 生成 patch
./bin/kernelhub patch \
  --db-path ./workspace/history.db \
  --kernel-assets ./workspace/kernel_assets \
  --mmq-root ~/mimikyu/mmq_kernels/mmq_kernels \
  --runs-dir ./workspace/runs \
  --output-dir ./workspace/patches

# 3. 检查 patch
ls workspace/patches/
cat workspace/patches/run-rms-norm-001/metadata.json

# 4. 验证 + 应用
cd ~/mimikyu
git checkout -b opt/rms_norm
git apply ~/autokernel-workflow/workspace/patches/run-rms-norm-001/patch.diff
git diff --stat
git add -A && git commit -m "perf(rms_norm): KernelHub 1.83x"

# 5. 运行测试确认
python -m pytest tests/ -k "rms_norm"
```

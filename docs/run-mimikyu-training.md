# 在 B200 上运行 Mimikyu 训练并捕获 Kernel Shape

> **目标**：在不占用所有 GPU 的前提下，用少量空闲卡跑几步训练，捕获所有 kernel 的真实 tensor shape。
>
> **机器**：8 × NVIDIA B200 (183GB)，CUDA 13.0，PyTorch 2.9.1
>
> **Mimikyu 源码**：`/home/chengqi/mimikyu`
>
> **已验证**：2025-04-09 单卡成功运行 3 步训练，捕获到 27 个 kernel 共 660 条调用记录。

---

## Shape 与模型参数的关系

捕获到的 kernel shape 分为两类：

### 固定 shape（只由模型超参数决定）

这些 kernel 的 shape 在训练过程中完全不变，改变模型超参数会改变 shape：

| 维度来源 | 公式 | 当前值 |
|----------|------|--------|
| tokens (M) | `micro_batch_size × seq_length` | 1 × 4096 = **4096** |
| hidden_dim (N) | `n_embd` | **2048** |
| n_inner | `n_inner` | **6144** (dense) / **512** (MoE expert) |
| qkv_dim | `(n_head + 2 × n_head_kv) × head_dim` | (32+8)×128 = **5120** |
| head_dim | `head_dim` | **128** |
| n_heads_total | `n_head + n_head_kv` | 32+4+4 = **40** |
| rope_dim | `qk_rope_head_dim` | **64** → sin/cos shape = [4096, 32] |
| num_experts | `num_experts` | **16** |
| moe_topk | `moe_topk` | **4** |
| vocab_size | `vocab_size` | **155648** |

**受影响的 kernel 举例**：

- `rms_norm_forward`: shape = [**4096**, **2048**] — 由 tokens × hidden_dim 决定
- `qk_rms_norm_forward`: shape = [1, **4096**, **40**, **128**] — 由 tokens × heads × head_dim 决定
- `swiglu_and_input_quant`: shape = [**4096**, **12288**] — 其中 12288 = n_inner × 2（dense 层 SwiGLU）
- `persistent_matmul` (router): shape = [**4096**, **16**] — 由 tokens × num_experts 决定

### 动态 shape（每步训练可能不同）

MoE 路由后的 permute/unpermute 和 grouped GEMM 的第一个维度 **每步都不同**，因为每个 expert 分到的 token 数量取决于路由决策：

```
grouped_gemm_bf16_nt 第一个维度: 17408, 17280, 17664, 17536 ...
                                 ↑ 这不是固定值，是 sum(tokens_per_expert)
```

理论上 `sum = tokens × moe_topk = 4096 × 4 = 16384`，但实际略大是因为 padding。

### 改变模型参数时 shape 如何变化

如果要模拟 WeLM-30B 真实训练，需要修改模型超参数：

| 参数 | 当前配置 | WeLM-30B |
|------|----------|----------|
| `n_embd` | 2048 | 2048 |
| `n_inner` | 6144 | 6144 |
| `num_experts` | 16 | **128** |
| `moe_topk` | 4 | **8** |
| `moe_n_inner` | 512 | **768** |
| `seq_length` | 4096 | 4096 |
| `micro_batch_size` | 1 | **2** |

改成 30B 配置后，shape 变化举例：
- `rms_norm`: [4096, 2048] → [**8192**, 2048]（因为 mbs 从 1 变 2）
- `router persistent_matmul`: [4096, 16] → [**8192**, **128**]
- `grouped_gemm 的 b`: [16, 1024, 2048] → [**128**, **1536**, 2048]
- MoE 层的 moe_n_inner: 512 → **768**，影响 swiglu / grouped_gemm 的 K 维度

---

## 共享存储上已有的资源

```
训练数据（Megatron .bin/.idx 格式）:
  /mnt/lustre/data/fineweb-edu_soft_dedup_by_5_0-3_train_text_document  (978GB)
  /mnt/lustre/data/ceval_train_text_document                            (5.7MB, 快速测试用)

Tokenizer:
  /mnt/lustre/welm-tokenizer/welm-v5-bpe/

模型权重（可选）:
  /mnt/lustre/models/welm_80b_a3b_pretrain
  /mnt/lustre/models/welm_30b_a3b_sft
```

不需要下载或生成任何数据。

---

## Step 1: 激活已有环境

这台 B200 机器上 `/envs/train` 虚拟环境已经预装了 `mmq_kernels`、`mmq_io`、`mmq` 以及 `mmq_train_megatron_causal_lm` 命令行工具，无需重新编译安装。

```bash
source /envs/train/bin/activate

# 验证
python3 -c "import mmq_kernels; print('mmq_kernels OK')"
python3 -c "import mmq; print('mmq OK')"
which mmq_train_megatron_causal_lm
```

---

## Step 2: 选择空闲 GPU

```bash
nvidia-smi --query-gpu=index,memory.used,memory.free,utilization.gpu --format=csv,noheader
```

选择空闲显存最多的卡。即使显存已部分占用（~95GB/183GB），我们的小模型只需约 5GB，可以共存：

```bash
export CUDA_VISIBLE_DEVICES=0    # 捕获 shape 只需 1 张卡
```

---

## Step 3: 创建训练配置

```bash
mkdir -p /home/chengqi/shape_capture/index_cache
mkdir -p /home/chengqi/shape_capture/kernel_logs
cd /home/chengqi/shape_capture
```

创建 `config.yaml`（已在该路径下提供，也可以手动创建）：

```yaml
gpt:
  activation_function: swiglu
  swiglu_with_input_quant: true
  n_embd: 2048
  n_head: 32
  n_head_kv: 4
  head_dim: 128
  n_inner: 6144
  n_layer: 4
  n_position: 0
  rotary_compress: 1.0
  residual_in_fp32: true
  sequence_parallel: false
  rotary_emb_fraction: 1.0
  tie_word_embeddings: false
  vocab_size: 155648
  dtype: bfloat16
  deepseek_fp8: false
  fuse_rmsnorm_residual: false
  enable_return_residual: false
  num_experts: 16
  moe_topk: 4
  moe_n_inner: 512
  num_shared_experts: 1
  qkv_proj_bias: false
  out_proj_bias: false
  lm_head_bias: false
  mlp_fc1_bias: false
  mlp_fc2_bias: false
  rms_norm: true
  prenorm: true
  moe_routing_type: expert_bias
  router_score_func: sigmoid
  qk_norm: true
  qk_rope_head_dim: 64
  qk_nope_head_dim: 64
  dense_layer_idx: [0]
  recompute_method: uniform
  recompute_num_layers: 1

optimizer:
  type: adam
  adam:
    beta1: 0.9
    beta2: 0.95
    fused: true
  learning_rate: 1e-4
  weight_decay: 0.1
  lr_scheduler: cosine
  warmup_iters: 2
  min_lr: 1e-6
  lr_decay_iters: 100
  grad_clip: 1.0

initializer:
  seed: 42
  layer_method: small_init
  output_layer_method: wang_init
  use_cpu: false

tokenizer:
  type: huggingface
  vocab_file: /mnt/lustre/welm-tokenizer/welm-v5-bpe
  trust_remote_code: true
  use_fast: true

data:
  train:
    num_workers: 2
    micro_batch_size: 1
    global_batch_size: 1
    stop_iteration: 3
    seq_length: 4096
    varlen: false
    data_root: /home/chengqi/shape_capture/index_cache
    index_files:
      - "1"
      - /mnt/lustre/data/fineweb-edu_soft_dedup_by_5_0-3_train_text_document

checkpoint:
  save_dir: /home/chengqi/shape_capture/ckpt
  save_step_interval: 0
  save_epoch_interval: 0
  load_dir: null

log:
  level: INFO
  also_log_to_stderr: rank0_last_pp_stage
  log_interval: 1
  wandb:
    enable: disabled
```

### 配置要点

| 参数 | 值 | 说明 |
|------|-----|------|
| `n_layer: 4` | 4 层 | shape 不受层数影响，省显存和时间 |
| `num_experts: 16` | 16 experts | 单卡承载所有 expert，触发 MoE kernel |
| `deepseek_fp8: false` | 关闭 FP8 | bf16 模式，避免 JIT 兼容问题 |
| `global_batch_size: 1` | 1 | 单卡 = 1 |
| `stop_iteration: 3` | 3 步 | 捕获 shape 只需 1 步，多跑几步看 MoE 动态分布 |
| `data_root` | 本地目录 | `/mnt/lustre/data/` 只读，索引缓存写到本地 |
| `recompute_num_layers: 1` | 全层 recompute | 减少显存占用 |

---

## Step 4: 运行训练并捕获 Shape

```bash
cd /home/chengqi/shape_capture
source /envs/train/bin/activate

# ====== GPU 选择 ======
export CUDA_VISIBLE_DEVICES=0

# ====== Kernel Shape 捕获 ======
export MMQ_KERNEL_LOG_PATTERN=".*"
export MMQ_ENABLE_CUDA_TIMER=1
export MMQ_LOG_DIR=/home/chengqi/shape_capture/kernel_logs

# ====== GEMM 后端（绕过 B200 JIT wgmma 兼容问题）======
export MMQ_GEMM_BACKEND=cublaslt
export MMQ_GROUPGEMM_BACKEND=third

# ====== MMQ 杂项 ======
export MMQ_SKIP_SET_CPU_AFFINITY=1

# ====== 网络配置 ======
export GLOO_SOCKET_IFNAME=eth0
unset LD_PRELOAD
unset NCCL_SOCKET_IFNAME

# ====== NCCL Shim 要求的环境变量（B200 机器特有）======
export NCCL_IB_TC=52
export NCCL_IB_QPS_PER_CONNECTION=4
export NCCL_P2P_NET_CHUNKSIZE=131072
export NCCL_IB_FIFO_TC=84
export NCCL_NET_GDR_LEVEL=PIX
export NCCL_NVLS_CHUNKSIZE=524288
export NCCL_CROSS_NIC=0
export NCCL_IB_ADAPTIVE_ROUTING=1
export NCCL_TUNER_CONFIG_PATH=/usr/local/gib/configs/tuner_config_a4.txtpb

# ====== 启动训练 ======
python -m torch.distributed.run --standalone \
    --nproc-per-node 1 \
    --no_python \
    mmq_train_megatron_causal_lm \
    --config config.yaml \
    --distributed.tp_size 1 \
    --distributed.pp_size 1 \
    --data.train.global_batch_size 1
```

预计 30-60 秒完成。

---

## Step 5: 查看捕获结果

Shape 日志输出到 `kernel_logs/cuda_event_timer_log_0.log`，每行一条 JSON：

```bash
head -5 kernel_logs/cuda_event_timer_log_0.log
```

输出示例：

```json
{"label": "call kernel: mmq_kernels.triton_kernels.rms_norm_forward:rms_norm_forward [hidden_states=[dtype=torch.bfloat16, shape=[4096, 2048], device=cuda:0], gamma=[dtype=torch.bfloat16, shape=[2048], device=cuda:0], ...]", "elapsed_time_ms": 2.99}
```

### 查看捕获到了哪些 kernel

```bash
python3 -c "
import json
kernels = set()
with open('kernel_logs/cuda_event_timer_log_0.log') as f:
    for line in f:
        name = json.loads(line)['label'].split(' ')[2]
        kernels.add(name)
for k in sorted(kernels):
    print(k)
print(f'\nTotal: {len(kernels)} kernels')
"
```

### 提取每个 kernel 的典型 shape

```bash
python3 -c "
import json, re
kernels = {}
with open('kernel_logs/cuda_event_timer_log_0.log') as f:
    for line in f:
        entry = json.loads(line)
        label = entry['label']
        name = label.split(' ')[2]
        if name not in kernels:
            shapes = re.findall(r'shape=\[([^\]]+)\]', label)
            kernels[name] = ' | '.join(shapes[:4])
for name in sorted(kernels):
    print(f'{name:56s} | {kernels[name]}')
"
```

---

## 已捕获的 Kernel 清单（2025-04-09 实测）

两组配置均已成功运行并捕获 shape：

| 配置 | experts | topk | moe_n_inner | mbs | EP | 参数量 |
|------|---------|------|-------------|-----|-----|--------|
| 小模型 | 16 | 4 | 512 | 1 | 1 | 0.9B |
| **30B-like** | **128** | **8** | **768** | **2** | **4** | **1.2B**（4层，真实 30B 是 49 层） |

### Shape 对比：小模型 vs 30B-like

| Kernel | 16 experts (0.9B) | **128 experts (30B-like)** | 变化原因 |
|--------|-------------------|---------------------------|----------|
| `rms_norm_forward` | [4096, 2048] | **[8192, 2048]** | mbs 1→2 |
| `qk_rms_norm_forward` | [1, 4096, 40, 128] | **[2, 4096, 40, 128]** | mbs 1→2 |
| `qkv_part_rope` | [1, 4096, 40, 128] | **[2, 4096, 40, 128]** | mbs 1→2 |
| `swiglu (dense)` | [4096, 12288] | **[8192, 12288]** | mbs 1→2 |
| `persistent_matmul (router)` | [4096, 16] | **[8192, 128]** | mbs + experts 16→128 |
| `topk_to_multihot` | [4096, 4] | **[29913, 8]** | tokens + topk 4→8 |
| `cumsum_cg` | [4096, 16] | **[29913, 32]** | tokens + experts/EP |
| `grouped_gemm (fc1) b` | [16, 1024, 2048] | **[32, 1536, 2048]** | exp/EP=32, 2×moe_n_inner 768 |
| `grouped_gemm (fc2) b` | [16, 2048, 512] | **[32, 2048, 768]** | exp/EP=32, moe_n_inner 768 |
| `swiglu (MoE)` | [17408, 1024] | **[62080, 1536]** | routed_tokens + 2×moe_n_inner |
| `hidden_states_mul_probs` | [17408, 512] | **[62080, 768]** | routed_tokens + moe_n_inner |
| `unpermute` | [17408, 2048] | **[62080, 2048]** | routed_tokens |

> 注：`gemm_bf16_nt`（qkv_proj, out_proj, dense fc1/fc2）的 b 矩阵 shape 不变（由 n_embd, n_inner 等决定），a 矩阵的第一维随 mbs 从 4096→8192。

### 30B-like 配置完整 Kernel 清单（Rank 0）

#### Dense 层 kernel（shape 固定）

| Kernel | 典型 Shape | 维度含义 |
|--------|-----------|----------|
| `rms_norm_forward` | [8192, 2048] | [tokens, hidden] |
| `residual_forward` | [8192, 2048] | [tokens, hidden] |
| `residual_add_rms_norm_forward` | [8192, 2048], [8192, 2048] | [tokens, hidden] |
| `gemm_bf16_nt` (qkv_proj) | a=[8192, 2048], b=[5120, 2048] | tokens×hidden, qkv_dim×hidden |
| `gemm_bf16_nt` (out_proj) | a=[8192, 4096], b=[2048, 4096] | tokens×n_head×head_dim, hidden×... |
| `gemm_bf16_nt` (dense fc1) | a=[8192, 2048], b=[12288, 2048] | tokens×hidden, 2×n_inner×hidden |
| `gemm_bf16_nt` (dense fc2) | a=[8192, 6144], b=[2048, 6144] | tokens×n_inner, hidden×n_inner |
| `qk_rms_norm_forward` | [2, 4096, 40, 128] | [batch, seq, heads, head_dim] |
| `qkv_part_rope` | [2, 4096, 40, 128] | [batch, seq, heads, head_dim] |
| `swiglu_and_input_quant` (dense) | [8192, 12288] | [tokens, 2×n_inner] |

#### MoE 层 kernel（第一个维度动态变化）

| Kernel | 典型 Shape | 维度含义 |
|--------|-----------|----------|
| `persistent_matmul` (router) | a=[8192, 2048], b=[128, 2048] | tokens×hidden, experts×hidden |
| `topk_to_multihot` | [29913, 8] | [tokens_after_route, topk] |
| `cumsum_cg` | [29913, 32] | [tokens_after_route, local_experts] |
| `permute` | [29913, 2048] | [tokens_after_route, hidden] |
| `grouped_gemm_bf16_nt` (expert fc1) | a=[62080, 2048], b=[32, 1536, 2048] | [routed_tokens, hidden], [local_experts, 2×moe_n_inner, hidden] |
| `swiglu_and_input_quant` (MoE) | [62080, 1536] | [routed_tokens, 2×moe_n_inner] |
| `dequant_swiglu_forward` | [57728, 1536] | [routed_tokens, 2×moe_n_inner] |
| `hidden_states_mul_probs` | [62080, 768] | [routed_tokens, moe_n_inner] |
| `grouped_gemm_bf16_nt` (expert fc2) | a=[62080, 768], b=[32, 2048, 768] | [routed_tokens, moe_n_inner], [local_experts, hidden, moe_n_inner] |
| `unpermute` | [62080, 2048] | [routed_tokens, hidden] |

#### Backward kernel

| Kernel | 典型 Shape |
|--------|-----------|
| `rms_norm_backward` | [8192, 2048] |
| `residual_add_rms_norm_backward` | [8192, 2048] |
| `residual_backward_only_d_in` | [8192, 2048] |
| `residual_backward_with_d_residual_in` | [8192, 2048] |
| `qk_rms_norm_backward` | [2, 4096, 40, 128] |
| `swiglu_backward_with_input_quant` | [57728, 1536] |
| `hidden_states_mul_probs_bwd` | [57728, 768] |
| `gemm_bf16_nn` / `gemm_bf16_tn_o_fp32` | 各种 wgrad/dgrad shape |
| `adam` | 1D 参数向量 |

---

## Step 6（可选）: 用 2 张卡运行

如果要用 2 张卡做数据并行：

```bash
export CUDA_VISIBLE_DEVICES=0,1
# config.yaml 中改：
#   data.train.global_batch_size: 2
python -m torch.distributed.run --standalone \
    --nproc-per-node 2 \
    --no_python \
    mmq_train_megatron_causal_lm \
    --config config.yaml \
    --distributed.tp_size 1 \
    --distributed.pp_size 1 \
    --data.train.global_batch_size 2
```

数据并行不改变 kernel shape（每卡各自独立计算相同 shape 的 tensor）。

---

## Step 7（可选）: 接近 WeLM-30B 真实配置

需要更多卡来启用 EP（expert parallelism），至少 4 卡：

```bash
export CUDA_VISIBLE_DEVICES=0,1,2,3
```

修改 config.yaml：

```yaml
gpt:
  num_experts: 128
  moe_topk: 8
  moe_n_inner: 768

data:
  train:
    micro_batch_size: 2
    global_batch_size: 8
    seq_length: 4096
```

运行：

```bash
python -m torch.distributed.run --standalone \
    --nproc-per-node 4 \
    --no_python \
    mmq_train_megatron_causal_lm \
    --config config.yaml \
    --distributed.tp_size 1 \
    --distributed.pp_size 1 \
    --distributed.ep_size 4 \
    --data.train.global_batch_size 8
```

128 experts + ep_size=4 = 每卡 32 experts。显存需求更大，如果 OOM 先用小配置验证。

---

## 调试记录（已解决的问题）

| 问题 | 原因 | 解决 |
|------|------|------|
| `OSError: No such device` | 默认网络接口 `bond1` 不存在 | `export GLOO_SOCKET_IFNAME=eth0` + `unset NCCL_SOCKET_IFNAME` |
| `NCCL/NET (shim) mismatch enforced` | B200 机器的 NCCL shim 要求特定配置 | 设置 8 个 NCCL 环境变量 + `NCCL_TUNER_CONFIG_PATH` |
| `PermissionError: /mnt/lustre/data/...doc_idx.npy` | `/mnt/lustre/data/` 只读 | config 中设置 `data_root` 指向本地可写目录 |
| `wgmma instruction cannot be compiled for sm_100a` | ThunderKittens JIT 用 Hopper 的 wgmma 指令编译到 Blackwell | `MMQ_GEMM_BACKEND=cublaslt` + `MMQ_GROUPGEMM_BACKEND=third` |
| `call kernel:` 日志没有出现在 stdout | `enable_kernel_log` 只做标记，输出需要 cuda_timer | `MMQ_ENABLE_CUDA_TIMER=1` + `MMQ_LOG_DIR=<path>` |

---

## 常见问题

### Q: 数据文件 978GB 会不会加载很慢？

不会。Megatron 用 mmap，只读实际需要的部分。3 步训练只读几 MB。

### Q: OOM 怎么办？

缩小模型：

```yaml
gpt:
  n_embd: 1024
  n_inner: 2048
  num_experts: 8
  moe_topk: 2
```

或者把 `seq_length` 从 4096 改到 2048。

### Q: 想用更小的数据文件？

```yaml
data:
  train:
    index_files:
      - "1"
      - /mnt/lustre/data/ceval_train_text_document
```

5.7MB，够跑 1-2 步。

### Q: 如何同时捕获 fused kernel 的 shape？

在运行前设置 fusion 开关：

```bash
export MMQ_FUSE_QUANT_AND_MUL_PROBS=1
export MMQ_FUSE_RMS_NORM_AND_RESIDUAL=1
export MMQ_FUSE_ROUTER_TOPK=1
export MMQ_FUSE_SWIGLU_AND_MUL_PROBS=1
export MMQ_FUSE_SWIGLU_WITH_JIT=1
export MMQ_SWIGLU_CAPTURE_FP8=1
export MMQ_RECOMPUTE_QKV_PROJECTION_QK_NORM_AND_ROPE=1
export MMQ_RECOMPUTE_RMS_NORM_OUTPUT=1
```

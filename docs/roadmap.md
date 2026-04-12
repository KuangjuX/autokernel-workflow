# KernelHub Roadmap

按优先级排列的改进计划。

---

## P0 — 必须完成

### 1. Go 单元测试 ✅

**状态**: 已完成

为核心 Go 模块编写单元测试，覆盖以下函数：

| 文件 | 测试覆盖 |
|------|----------|
| `prepare.go` | `deriveKernelName`, `lookupShapeEntry`, `expandShapeConfigs`, `buildShapeConfigsPython`, `sortedKeys`, `ensureGetInputsShapeIdx`, `applyMultiShapeOverrides`, `loadWorkloadConfig` |
| `sync_git.go` | `parseCommitBodyFields`, `normalizeCommitFieldKey`, `parseIteration`, `parseHypothesis`, `parseFloat`, `firstNonEmpty`, `validateHistoryIntegrity`, `extractField` |
| `history_store.go` | DB round-trip (open → insert → query), 多 run 插入, NULL speedup/latency 处理, meta 读写, 目录自动创建 |
| `export.go` | `buildStats`, `buildSnapshot`, `clampPatch`, `cloneRuns`, `renderStaticHTML`, `shortHash` |

**测试命令**:
```bash
CGO_ENABLED=1 go test ./internal/commands/ -v -count=1
```

### 2. 修复 kernel-adapter 集成

**状态**: 待开始

当前 `third_party/kernel-adapter` submodule 不完整，`kernelhub` 中的 patch 提交流程缺少实际的集成代码。

**需要做的**:
- 确认 kernel-adapter 的 API / CLI 接口
- 在 `kernelhub` 中添加 `patch` 子命令，调用 kernel-adapter 提交优化后的 kernel
- 端到端测试：从 AKO4ALL 产出 → 数据库 → patch 提交

### 3. 自动化 kernel 选择

**状态**: 待开始

当前 `kernelhub prepare` 需要用户手动指定 `--kernel-src` 和 `--reference-src`。应支持自动从 `kernel_assets/` 目录中选择待优化的 kernel。

**需要做的**:
- 添加 `kernelhub list-kernels` 子命令，列出所有可用 kernel 及其当前最佳 speedup
- 添加 `--auto-select` 选项，按优先级自动选择下一个待优化 kernel（例如：从未优化过的 > speedup 最低的）
- 支持 `--kernel-name` 参数，按名字选择 kernel（替代完整路径）

---

## P1 — 重要改进

### 4. 增强 kernel 名称匹配的健壮性

**状态**: 待开始

当前 `lookupShapeEntry` 使用启发式匹配（去前缀、去后缀、加后缀），在 kernel 命名不规范时可能匹配错误或遗漏。

**需要做的**:
- 在 workload JSON 中添加可选的 `aliases` 字段，显式声明 kernel 名称映射
- 当启发式匹配产生多个候选时，输出更清晰的警告
- 添加 `kernelhub validate-workload` 子命令验证 workload JSON 与 kernel_assets 的一致性

### 5. `get_inputs()` 注入逻辑加固

**状态**: 待开始

当前 `ensureGetInputsShapeIdx` 和 `applyMultiShapeOverrides` 使用字符串替换和正则来修改 Python 源码，对于非标准格式的 `reference.py` 可能出错。

**需要做的**:
- 建立 `reference.py` 的标准模板，新增 kernel 时从模板生成
- 在注入前验证 `reference.py` 的结构（是否包含 `get_inputs`、是否有标准格式的维度变量）
- 添加注入后的自动验证（`python -c "import reference; reference.get_inputs(0)"` ）

### 6. 跨 shape 运行对比

**状态**: 待开始

当同一个 kernel 使用不同 shape 优化后，缺少便捷的对比机制。

**需要做的**:
- `kernelhub compare` 子命令：输入两个 run_id，输出 speedup 对比表格
- 在 HTML dashboard 中添加 kernel 视图：按 kernel 名聚合所有 run，展示不同 shape 下的表现
- 考虑在数据库中添加 `kernel_name` 和 `shape_label` 索引字段

---

## P2 — 效率提升

### 7. Agent 优化效率提升

**状态**: 待开始

当前 AKO4ALL agent 无 autotuning 能力，每次迭代依赖 agent 推理来调参。

**可能的方向**:
- 集成 Triton autotuner (`triton.autotune`)，在 bench 阶段自动搜索最佳配置
- 支持在 `context/` 目录中注入先前优化的经验（HINTS.md 自动更新）
- 为 agent prompt 生成基于硬件特性的建议（L2 cache size、SM count、shared memory capacity）

### 8. 跨 run 知识积累

**状态**: 待开始

目前每次 AKO4ALL run 独立进行，不会参考之前相同 kernel 的优化经验。

**需要做的**:
- 优化开始前，自动从 history.db 提取同一 kernel 的历史 iterations
- 生成 `context/history_summary.md`，包含之前尝试过的优化方向和结果
- Agent 可以避免重复失败的优化路径

### 9. GPU 架构参数自动检测

**状态**: 待开始

当前 bench.py 和 agent prompt 中的 GPU 参数（SM count、shared memory size 等）是硬编码或手动指定的。

**需要做的**:
- 在 `bench.py` 或 `prepare` 阶段自动检测当前 GPU 架构并注入参数
- 在 workload config 中添加 `target_arch` 字段，`prepare` 时校验当前 GPU 是否匹配

---

## P3 — 长期愿景

### 10. CI/CD 集成

- 定期自动运行 kernel 优化，提交到 dashboard
- PR check：在 kernel 修改的 PR 上自动运行 bench 并报告 speedup 变化

### 11. 多 GPU 架构支持

- 同一 kernel 在 H800、B200 等多种 GPU 上的 shape 和优化策略可能不同
- 在 workload config 中支持 per-arch shape 定义
- Dashboard 按 GPU 架构分组展示

### 12. 优化策略模板库

- 将成功的优化模式（persistent kernel、warp specialization、pipeline 等）抽象为模板
- Agent 可以从模板库中选择策略作为起点

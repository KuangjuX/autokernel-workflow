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

### 2. 修复 kernel-adapter 集成 ✅

**状态**: 已完成

通过轻量级 wrapper 方式集成 kernel-adapter：

- `kernelhub patch` 子命令：调用 `kernel-adapter batch-patch` 生成 patch
- 自动解析 history.db，查找每个 run 的最佳 iteration
- 支持 `--verify`（git apply --check 验证）和 `--apply`（实际应用 patch）
- `program.md` 新增 Step I 文档
- 添加 `third_party/kernel-adapter` 符号链接修复 submodule 拼写

**相关文件**:
- `internal/commands/patch.go` — Patch() 函数实现
- `internal/commands/patch_test.go` — 单元测试
- `internal/cli/cli.go` — patch 子命令注册
- `program.md` — Step I 文档

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

### 5. `get_inputs()` 注入逻辑加固 ✅

**状态**: 已完成

通过三层防护加固 `reference.py` 的注入流程：

- `templates/reference.py.tmpl` — 标准模板文件，定义结构契约（Model 类、维度变量、SHAPE_CONFIGS、get_inputs/get_init_inputs）
- `validateReferenceStructure()` — 注入前结构校验：检查 Model 类、get_inputs 函数、维度变量声明是否存在；缺失则报错或警告
- `verifySyntax()` — 注入后调用 `python3 compile()` 做语法检查，防止错误的 reference.py 流入 agent

**相关文件**:
- `templates/reference.py.tmpl` — 标准模板
- `internal/commands/prepare.go` — `validateReferenceStructure()`, `verifySyntax()`, 集成到 `applyMultiShapeOverrides()`
- `internal/commands/prepare_test.go` — 12 个新测试覆盖各种边界场景

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

### 8. 跨 run 知识积累 ✅

**状态**: 已完成

- `kernelhub generate-context` 子命令：从 history.db 提取同一 kernel 的历史优化经验
- `kernelhub prepare --db-path` 自动注入 `context/history_summary.md`
- 生成文档包含：成功策略表、失败策略表、关键词趋势分析、run 时间线
- HINTS.md 和 AGENT_PROMPT.md 已更新，要求 agent 在第一轮迭代前读取

**相关文件**:
- `internal/commands/generate_context.go` + `generate_context_test.go`
- `internal/cli/cli.go` — generate-context 子命令注册
- `third_party/AKO4ALL/HINTS.md` — 新增 history context 读取指引

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

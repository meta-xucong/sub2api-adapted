# 实现完成后的发布、配置迁移与回滚

本文件是**未来实施完成后的 runbook**，不是当前授权部署。

## 1. 发布前冻结

- 固定 Git commit、dirty-tree 状态、Go 测试日志、binary/image digest、配置摘要。
- 确认 `aiself` 与 `404token` 的模型账号/分组来源一致性；逐项核验 DeepSeek/GLM 等同源账号的上游 provider identity，而非只比显示名称。
- 备份只需备份受影响的 Sub2API 配置/数据库快照；不得导出或写入明文 API key、口令或授权头。
- 在每个目标组记录当前 `/v1/models` IDs、manifest IDs、model allowlist、生效 account mappings、compact config 和刷新状态，保存 hash/计数及脱敏样本。
- 磁盘空间检查；使用单一镜像构建阶段和 VPS 本地 tag，不在服务器保留多套大型源码/层缓存。部署命令必须用已验证 SSH wrapper 与脚本路径，避免长命令/引号导致连接会话断掉。

## 2. 两段发布

### Phase A：代码默认安全，不扩大动态权限

1. 两台实例部署相同 commit/image digest。
2. scheduler 配置启用但目标 account/group 仍处于旧/manual catalog policy；refresh 只写 snapshot 与报告，不改变现有 effective access。
3. 确认容器 config 中 `openai_compact_model` resolved empty，scheduler 每个实例只有一个 leader，单账号并发不超过上限。
4. 只读比较新旧 `/v1/models` 与 manifest。此阶段如出现 surprise additions/removals，停止，不切 follow policy。

### Phase B：经批准的 scope enablement

1. 先选择测试账号，显式设置 `extra.upstream_model_policy=follow_upstream`；保持现有 group allowlist。若要自动准入新增模型，用现有 wildcard 限定命名空间，不关闭 allowlist 作为捷径。
2. 只读确认新增 canonical models 与 raw route bindings，然后发一个低成本文本请求；再核验 compact 透传。
3. 通过后逐账号/来源批次启用。不得对所有账号批量打开 follow；每个变更前记下明确 owner、group allowlist 命中情况与权限影响。
4. 上线首 24h 观测 refresh errors、quarantine IDs、404 model_not_found、compact rewrite reason、usage 和 wallet delta。

## 3. 两台 VPS 一致性

最终状态要求：

- image digest 和应用 commit 一致。
- alias registry/normalizer version 一致。
- scheduler 开关、timezone、timeout、stale grace、concurrency 一致。
- 同类 provider accounts 的 source family 与 policy 一致（账号凭证不要求也不应相同）。
- 每台各自的上游目录 snapshot 可不同；一致性判据是同源账号对同一供应商的成功目录差异可解释、组授权契约一致，而不是强行要求所有 provider 的模型集合相等。
- 最近刷新 run 的状态/时间符合每日 04:00 + due catch-up contract；“scheduler started”不能代替 account refresh success。

## 4. 回滚

### Catalog/alias 回滚

1. 对异常 provider/account/group 将 policy 退回 `manual`；不删除快照，保留证据。旧版本不识别 fencing token 时不得写回旧 snapshot；回滚步骤保留 per-account `latest_issued_token` / `last_applied_token`，不能删表、降值或让旧实例继续刷新写入。
2. 恢复 pre-release group access policy 和 `model_mapping` backup（仅当被错误变更且经双重确认）；本实现原则上不写 mapping。
3. 失效共享目录/ownership cache，确认下游不再展示意外 ID。
4. 如正常历史 ID 被 quarantine，临时显式 provider registry patch 也需 source + test，不允许用通用 suffix stripping 紧急绕过。

### Compact 回滚

1. 首选回到前一个固定 image digest 并恢复对应的原有配置快照。
2. 若需临时启用 `gpt-5.5`，优先只对已验证需要它的账号设置 account-level `compact_model_mapping`；该字段不是 group-level。`GATEWAY_OPENAI_COMPACT_MODEL` 是全局 `gateway.openai_compact_model` override，只能在运维明确接受影响**所有**匹配 `/v1/responses/compact` 请求时设置；不能声称它可按 account/group 限定。
3. 核对普通 Responses/native v2 未被改动；比对 upstream request `model`、response、usage 和钱包流水。

### 代码级回滚

- 首选部署上一个固定 image digest；不得 `git reset --hard` 覆盖工作树用户内容。
- 保留 `accounts.extra` 新快照并向后兼容读取；回滚 binary 不删除 JSONB keys。
- 版本回退后验健康、`/v1/models`、一个授权文本请求、compact error contract、usage/wallet。没有验收通过前标 `ROLLBACK_INCOMPLETE`。

## 5. 发布中止条件

任一项成立立即停止扩大：refresh leader 重复运行、account credentials 被改写、group allowlist 被绕过、同一正式 ID 对应不稳定 raw route、公开目录包含 `codex-auto-review`/bare GPT/未批准 exp ID、compact 默认仍将 GPT 模型改成 gpt5.5、任一端账单或 model usage 归属变化无法解释、VPS 磁盘/内存快速恶化。

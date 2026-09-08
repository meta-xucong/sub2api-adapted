# 模型目录、引导页与 GPT-6 Astra 升级验收

日期：2026-09-09

本次变更的目标是让用户侧的 API 引导、模型选择和实际分组配置保持一致，并把已经确认支持 `gpt-6-astra` 的上游账号接入活动模型目录。变更顺序为先 aiself，再 404token；两台 VPS 均保留了旧镜像、旧 compose 和数据库/Redis 快照。

## 处置原则

- 只清理活动配置、账号的 `model_mapping` 和用户可见模型目录，不删除历史用量、账单、价格记录或已删除分组。
- 从活动账号映射中移除 `gpt-5.4`、`gpt-5.4-mini`；aiself 同时移除历史遗留的 `gpt-5.4-nano`。
- 仅把实时上游 `/v1/models` 已确认返回 `gpt-6-astra` 的活动、可调度账号加入映射；没有验证通过的账号不强行加入。
- 后端中仍可能存在图片兼容、历史计费或旧协议的 GPT-5.4 字符串，它们不是活动公开目录，不在本次删除范围内，以避免破坏历史数据和兼容路径；GPT-5.4-mini 的生图桥接按用户要求留待后续统一处理。

## 生产模型目录

### aiself.vip

- OpenAI 主分组（group `2`）最终公开目录：
  `gpt-5.5`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`、`gpt-6-astra`、`gpt-image-2`。
- Ark/Doubao 分组（group `6`）保持独立目录，不混入 GPT-6：
  `ark-code-latest`、`deepseek-v4-flash`、`deepseek-v4-pro`、Doubao Seed 2.x、GLM、Kimi、MiniMax 等原有模型。
- OpenAI 账号 `83`、`85`、`103`、`104`、`105`、`106`、`111` 已确认支持 GPT-6 Astra 并加入映射。
- 活动分组与非删除账号中的 GPT-5.4 残留核验结果为 `0`。

### 404token.xyz

- OpenAI 活动分组（groups `7`、`8`、`9`、`10`）最终公开目录：
  `gpt-5.5`、`codex-auto-review`、`gpt-5.6`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-6-astra`、`gpt-image-2`。
- 生图分组（group `12`）只保留 `gpt-image-2`。
- 已禁用的历史分组（group `14`）移除 GPT-5.4/mini，但保持禁用状态，未加入 GPT-6。
- 活动账号 `4`、`5`、`6`、`8`、`13`、`15`、`16`、`20` 已确认支持 GPT-6 Astra 并加入映射。
- 活动/可见分组与非删除账号中的 GPT-5.4 残留核验结果为 `0`。

## 前端引导适配

代码层面新增安全的公开字段：普通用户分组 DTO 返回 `models_list_config` 和 `default_mapped_model`，不返回账号凭据、路由内部结构或上游密钥。

引导页现在会：

- 根据所选 API Key 的分组模型目录选择默认模型；例如 Ark 分组会优先使用 `deepseek-v4-flash`，不会再显示为 GPT-5.5。
- OpenCode 配置只生成该分组实际公开的模型；未知但已由分组配置的模型会生成通用模型条目。
- OpenAI/Codex、Grok 和 CCSwitch 的 OpenAI-compatible 地址统一归一化为 `/v1`，不会重复生成 `/v1/v1`。
- Gemini CLI/CCSwitch 地址使用 `/v1beta`。
- Anthropic 客户端继续使用域名根地址，由 Anthropic 客户端自身补 `/v1`；Antigravity 的 Claude 与 Gemini 路径分别保持对应协议。
- CCSwitch usage 脚本使用归一化后的 `/v1/usage`，避免旧设置仍指向域名根路径。

## 代码与测试

- `frontend/src/utils/gatewayBaseUrl.ts`：统一 URL 归一化。
- `frontend/src/components/keys/UseKeyModal.vue`：按分组目录生成模型与客户端配置。
- `frontend/src/views/user/KeysView.vue`、`EndpointPopover.vue`、`ccswitchImport.ts`：更新端点显示和导入脚本。
- `backend/internal/handler/dto/types.go`、`mappers.go`：安全下放分组模型目录。
- `backend/internal/pkg/openai/constants.go`：默认 OpenAI 目录移除 GPT-5.4/mini，账号测试默认切换为 GPT-5.5。
- `backend/internal/config/config.go`、`openai_messages_dispatch.go`、`smart_router_calibration_service.go`：Compact、Messages 分发和 SmartRouter 的活动默认/回退切换为 GPT-5.5，并优先探测已映射的 GPT-6 Astra；旧 API 中显式保存的非活动映射仍按兼容逻辑读取。

已完成验证：

- `pnpm run typecheck`：通过。
- `pnpm run build`：通过。
- `pnpm exec vitest run`：254 个测试文件、1856 个测试全部通过。
- Docker 多阶段构建：通过，包含 Go 后端编译和前端嵌入；r2 重新构建后，容器内 Go 核心包测试通过。
- aiself：`/`、`/health` 返回 200；`/v1/models` 返回 GPT-6 Astra；GPT-6 Astra、DeepSeek-v4 Flash、Doubao Seed 2.0 Code 实际请求均返回 200/OK。
- 404token：`/`、`/health` 返回 200；有效分组 key 的 `/v1/models` 返回新的公开目录；GPT-6 Astra 实际请求返回 200/OK。
- 用户已确认 GPT-5.4-mini 的上游下架属于预期情况，本次不把它作为验收失败条件。

## 镜像、备份与回滚

新镜像：`sub2api-adapted:model-catalog-20260909-r2`

本地镜像 manifest digest：

`sha256:395dd9f670fba6c6fbb928bbac735574ac4405fd18ddfcbd022e98e13fe6c405`

传输 tar 的 SHA-256：

`a29f44e9e0176852f90df2195ca06a505598c5f6097f930746490ebbdd3447ae`

aiself 备份目录：

`/opt/sub2api/deploy/backup/model-catalog-20260909-aiself/`

404token 备份目录：

`/opt/sub2api-deploy/backup/model-catalog-20260909-404token/`

两个目录均含 PostgreSQL custom dump、Redis RDB、应用 inspect 信息和变更前 compose；两个 VPS 的旧镜像 `sub2api-adapted:upgrade-v021-merged-20260908` 以及上一轮模型目录镜像仍保留。每台 VPS 另保存了切换前 r1 compose：`docker-compose.model-catalog-20260909-r1.yml`；原有 `docker-compose.pre-image.yml` 是更早切换前的应用配置。

仅回滚应用镜像时，恢复对应的变更前 compose，然后执行：

```bash
docker compose -f /opt/sub2api/deploy/docker-compose.yml up -d --no-deps sub2api
```

404token 使用 `/opt/sub2api-deploy/docker-compose.yml`。数据库快照只在需要回滚数据库模型目录变更时使用，并应先停止应用、确认业务窗口，再执行 PostgreSQL 恢复；不能把数据库恢复作为普通镜像回滚步骤。

## 后续维护规则

新增上游模型前，先对活动账号服务端执行 `/v1/models` 能力审计，再同时更新账号映射、分组 `models_list_config`、前端测试和价格配置。上游下架模型先从公开目录和活动映射移除，历史账单/用量和兼容代码单独保留，避免把历史数据清理误当成模型下架。

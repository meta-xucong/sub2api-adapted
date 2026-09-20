# 本地统一 API Key 与多 Provider 探测记录（2026-09-19）

## 1. 范围和安全边界

本次只操作 aiself 的**本地 PostgreSQL 复刻库**，没有连接或写入 aiself VPS，也没有修改任何远端账号、凭据、分组、余额或 API Key。

- 本地 PostgreSQL：`127.0.0.1:55432/sub2api`
- 本地快照目录：`E:\AI\_vps_snapshots\aiself-20260919`
- 写入前数据库备份：`pre-local-unified-test.dump`
- 写入前备份 SHA-256：`327BB9F380DB29498626C51F5794537B843DF182BA6A6A036590DABF3CEE78FB`
- 备份文件不进入仓库，保留用于回滚/对比。

测试数据使用固定的 `local-unified-*` 前缀，便于后续单独清理或复核。临时 Go 探测程序在完成探测后已删除；本地测试用户、统一分组、API Key 和路由物化数据按当前验收需要保留。

## 2. 本地测试身份

目标邮箱 `197286814@qq.com` 在本地快照中已经存在，且已有明确的本地测试标记，因此本轮没有重复创建用户。

| 项目 | 结果 |
|---|---|
| 用户 ID | `18` |
| 用户状态 | `active` |
| 本地统一分组 ID | `15` |
| 本地统一分组名 | `local-unified-test-197286814-20260919` |
| 统一测试 API Key ID | `54` |
| API Key 名称 | `local-unified-all-providers` |
| API Key 状态 | `active` |
| API Key 长度 | `81` |
| API Key 前缀 | `sk-local-unified-` |

完整 API Key 不写入仓库文档；交付时仅通过当前会话单独提供。数据库中的 Key 仍属于敏感数据，不能复制到日志、fixture 或 GitHub。

以下两条本地鉴权路径均已成功解析到同一身份：

1. `APIKeyRepository.GetByKeyForAuth`：`key_id=54, user_id=18, group_id=15, active`；
2. `APIKeyService.GetByKey`：`key_id=54, user_id=18, group_id=15, active`。

## 3. 统一分组和模型目录

本地统一分组纳入了当前快照中所有“已有活动分组、账号为 active 且 schedulable”的账号，不修改这些账号原来的分组归属。当前结果：

| 指标 | 数量 |
|---|---:|
| 纳入统一分组的可调度账号 | 35 |
| 已启用路由目标 | 141 |
| 已启用账号绑定 | 255 |
| 已发布模型目录 | 119 |
| 配置模型记录 | 119 |

Provider/端点覆盖：

| 端点 | Provider | 路由数 | 模型数 |
|---|---|---:|---:|
| `chat_completions` | `anthropic-api-key` | 43 | 43 |
| `chat_completions` | `gemini` | 50 | 50 |
| `chat_completions` | `openai-compatible` | 23 | 23 |
| `chat_completions` | `volcengine-ark` | 23 | 23 |
| `images_generations` | `openai-compatible` | 1 | 1 |
| `videos` | `grok-media` | 1 | 1 |

模型族覆盖包括：Claude 22 个、DeepSeek 9 个、Doubao 8 个、Gemini 31 个、GPT 7 个、Grok 视频 1 个、Kimi 4 个，以及其他模型 37 个。DeepSeek、Kimi、Doubao 均能在统一目录中出现，且部分公共模型同时有多个 Provider 路由。

## 4. 低成本真实上游探测

探测通过统一运行时执行，使用内存价格快照和内存扣费 ledger，避免改变本地测试用户的持久余额。请求体为极短文本请求，成功才产生测试 ledger 扣费；失败路径明确检查为不扣费。

| 探测 | 公共模型 | Provider | 结果 | 选中账号 | 扣费结论 |
|---|---|---|---|---:|---|
| OpenAI-compatible 国模 | `qwen3.8-max` | `openai-compatible` | PASS | 121 | 成功，按请求计费 |
| 火山 Ark / Doubao | `doubao-seed-2.0-pro` | `volcengine-ark` | PASS | 6 | 成功，按该 Provider 的独立规则计费 |
| Anthropic Claude | `claude-opus-4-6` | `anthropic-api-key` | PASS | 123 | 成功，按次计费 |
| OpenAI 文本 | `gpt-5.6` | `openai-compatible` | PASS | 23 | 成功，按请求计费 |
| Gemini 文本 | `gemini-3-flash-preview` | `gemini` | FAIL | 5 | 代理连接超时，确认 `no_charge=true` |

Gemini 失败后没有继续重复轰测；失败原因是本地 SOCKS 代理到 Google 上游连接超时，不足以判定 Gemini 凭据或统一路由实现错误。

在前一阶段的目录探测中没有提交 Grok 视频任务；完整服务启动后已按第 9 节补做一次最小真实提交。视频生成是有成本、异步且会产生外部任务的操作，因此只提交一次，不把异步创建成功冒充成最终视频交付成功。

### 多 Provider 同名模型的专项说明

第一次不加 Provider 隔离的 Doubao 探测，运行时按当前路由优先级选中了同名模型的 `anthropic-api-key` 账号 8。这不是凭据泄漏或计费串线，而是证明同一公共模型确实存在多个 Provider 候选。

随后将**仅用于测试的内存目录**限定为 `volcengine-ark`，再次执行相同低成本请求，正确选中账号 6 并成功返回。生产运行时仍按数据库中目标优先级和账号绑定调度；如果管理员希望 Doubao 默认优先走 Ark，应在管理面把 Ark 目标的优先级设置在前面，而不是依赖测试过滤。

## 5. 价格和扣费检查

本地测试路由采用手动兜底价格，只用于验证“路由绑定的价格独立生效”，不是生产价格建议：

| 路由类型 | `billing_mode` | `rate_basis` | 测试最终单价 |
|---|---|---|---:|
| 普通文本请求 | `per_request` | `per_request` | `0.000001` |
| 火山 Ark 文本请求 | `per_request` | `per_request` | `0.0000011` |
| 图片生成 | `image` | `image` | `0.01 / 次` |
| Grok 视频 | `video` | `video` | `0.10 / 次` |

所有测试路由的价格语义为 `final_user_price`，未知倍率没有回退为统一默认生产价。成功探测的内存 ledger 有扣费；Gemini 失败探测余额差为零。由于本轮没有接入本地 Redis 和生产持久结算 worker，不能把这次内存 ledger 结果表述为生产数据库余额结算验收。

## 6. 隔离审计

- 原有分组 `2、3、4、5、6、10、11、13、14` 仍保留；其当前账号绑定数分别为 `24、1、1、1、1、2、3、1、6`，原有成员关系没有被测试脚本改写。
- 原有分组之外只增加/复用了独立的本地测试分组 `15`；统一分组当前包含 35 个可调度账号。
- 已将写入前 custom dump 恢复到临时审计数据库，并对原有分组行、`account_groups`（排除测试分组）以及原有 API Key 非凭据元数据（排除测试 Key）逐项比较，三项差异均为 `NONE`；临时审计数据库已删除。
- 统一路由的 255 个启用绑定全部指向当前 `active + schedulable` 账号；不合格绑定数为 0。
- 本地统一目标中禁用残留数为 0，价格结构缺失数为 0。
- 旧 `/v1` 没有被测试脚本调用或改写；统一入口仍是独立 `/unified/v1` 命名空间。
- 本地候选程序、测试数据库和备份均未上传 GitHub，VPS 没有产生写操作。

## 7. 证据边界和后续动作

本轮可以确认：本地快照中的多 Provider、多模型、OpenAI-shaped 统一入口、Anthropic 适配、账号调度和路由级价格快照已经能在同一测试 API Key 身份下闭环运行。

追加的本地 HTTP 层验收（2026-09-20）使用候选 server graph 中的真实 `APIKeyAuthMiddleware`、`UnifiedGatewayRuntimeHandler` 和统一 runtime，在本地 `httptest` 路由上执行：

- `GET /unified/v1/models`：HTTP `200`，返回 119 个模型；
- `POST /unified/v1/chat/completions`，模型 `qwen3.8-max`：HTTP `200`，返回非空响应；
- 两次请求均使用本地测试 Key `id=54`，解析到 `group_id=15`；
- HTTP 探测结束后临时程序已删除，数据库测试对象保留。

这证明 API Key → 鉴权中间件 → `/unified/v1` handler → 统一调度器 → 上游 → 响应回传的本地 HTTP 路径可用。该次 handler-level 探测发生在完整服务启动前，扣费 ledger 使用的是本地内存实现；随后已按第 8 节补做完整服务验收。

## 8. 完整本地服务验收（2026-09-20）

本地 PostgreSQL 快照实例、候选 Sub2API 服务和隔离 Redis 均已启动：

- Sub2API：`http://127.0.0.1:18080`
- Redis：容器 `sub2api-local-unified-redis-20260920`，仅映射本机 `127.0.0.1:6379`，使用 Redis DB `15`；
- PostgreSQL：`127.0.0.1:55432/sub2api`；
- 本地运行配置位于 `E:\AI\sub2api\.local-unified-runtime-20260920`，不属于仓库提交内容。

完整进程验收结果：

| 请求 | 结果 |
|---|---|
| `GET /unified/v1/models` | HTTP `200`，119 个模型 |
| `POST /unified/v1/chat/completions`，`qwen3.8-max` | HTTP `200`，非空响应 |
| `POST /unified/v1/chat/completions`，`deepseek-v4-flash-0731` | HTTP `200`，快照选中 `openai-compatible` |
| `POST /unified/v1/chat/completions`，`doubao-seed-2.0-pro` 默认优先级 | HTTP `200`，快照选中 `anthropic-api-key` |
| `POST /unified/v1/chat/completions`，`claude-opus-4-6` | HTTP `200`，快照选中 `anthropic-api-key` |
| 将 Doubao 的 Ark 目标设为优先后再次请求 | HTTP `200`，快照选中 `volcengine-ark` |
| Ark 优先且价格精度为 8 位的最终请求 | HTTP `200`，持久化扣费 `0.0000011` |
| 同一成功请求的持久化快照 | `captured`，`measured_units=1`，用户扣费 `0.000001` |
| 同一成功请求的持久化账本 | `captured`，`captured_amount=0.000001` |
| 成功请求的恢复任务 | 下一轮 worker 后 `completed` |
| `POST /unified/v1/chat/completions`，`gemini-3-flash-preview` | HTTP `502`，上游代理超时 |
| Gemini 失败快照 | `released`，`user_charge=0` |
| Gemini 失败账本 | `released`，用户余额无变化 |
| Gemini 失败恢复任务 | 下一轮 worker 后 `completed` |
| 旧 `GET /v1/models` | HTTP `200`，46 个旧入口模型 |

启动时模型价格远程同步因当前网络超时，自动回退到仓库内置价格文件并加载 198 个模型；这只影响价格同步提示，不影响统一路由的手动物化价格和本次请求。

### Provider 优先级与计费精度经验

同一个公共模型可以存在多个 Provider 目标。默认所有本地测试目标优先级相同，因此 Doubao 首先命中 `anthropic-api-key`；将同一模型的 Ark 目标设为 `priority=-10`、兼容目标设为 `priority=10` 后，真实 HTTP 请求改为命中 `volcengine-ark`。

测试还验证了金额精度边界：Ark 测试单价为 `0.0000011`，若 `rounding_precision=6`，最终金额会被舍入为 `0.000001`，不同 Provider 的小倍率差异会消失；将该链路精度设为 8 位后，账本和快照均保留 `0.0000011`。生产管理员配置倍率时，必须按实际价格粒度设置精度，不能机械统一使用 6 位。

本轮尚不能确认每一个上游账号都在当前时刻可用。真实低成本探测覆盖的是 Provider/协议代表链路，而不是无成本地逐个消耗所有账号。Grok 视频的真实提交、轮询和内容读取结果详见第 9 节；该代表链路已完成一次完整闭环。

因此当前测试 Key 已适合本地完整验收，但不应直接拿去做 VPS 生产发布。生产发布仍需独立的灰度、回滚和真实结算证据。

## 9. 媒体链路真实验收（2026-09-20）

### 图片生成

使用完整本地服务和同一测试 Key 调用：

```text
POST /unified/v1/images/generations
model = gpt-image-2
```

结果为 HTTP `200`，响应体约 `1,046,809` 字节；持久化快照选中 `openai-compatible`、目标 `109`、账号 `12`，状态为 `captured`，`measured_units=1`，用户扣费 `0.01`，持久化账本同步为 `captured`。这证明图片模型的“按次计费、成功捕获”路径已通过真实 HTTP 层闭环。

同一图片模型的前序失败尝试（上游超时或 404）均为 `released` 且 `user_charge=0`，证明失败不收费。此前一次客户端超时留下的图片预留仍处于恢复宽限期，快照状态为 `reserved`、账本状态为 `reserved`，金额 `0.01`，尚未计入已捕获扣费；这是为避免把可能仍在上游执行的请求误判为失败而保留的冻结，不是第二次成功扣费。

### Grok 视频

使用 Wokey Grok 视频账号的统一路线只提交一次最小异步任务：

```text
POST /unified/v1/videos/generations
model = grok-imagine-video-1.5
resolution = 480p
duration = 6
aspect_ratio = 16:9
```

创建请求返回 HTTP `202`，任务 ID 为上游返回的异步任务 ID；快照选中目标 `110`、`grok-media`、账号 `114`（Wokey），状态为 `pending`，视频路线用户价格快照为 `0.10`。最初对同一任务轮询 3 次均得到 HTTP `502 / UPSTREAM_FAILED`。随后对同一个任务 ID 直接做只读上游核验，Wokey 返回 HTTP `200`、`status=completed`、`content_status=available`，真实响应使用的是当前字段 `content_url`，而不是旧适配器只识别的 `video_url`。根因确认后只做了两处最小兼容：Wokey 状态计费归一化同时识别 `content_url` 并映射 `duration_seconds`；统一入口的内容代理路径保留 `/unified/v1` 前缀。

不重复创建任务，重新通过统一 API 轮询原任务返回 HTTP `200`、`status=completed`；随后调用 `GET /unified/v1/videos/{task_id}/content`，返回 HTTP `200`、`video/mp4`，实际读取 `522,433` 字节且未落盘。持久化快照只有 1 条视频记录，状态为 `captured`、`measured_units=1`、用户扣费 `0.10`；账本为 `captured`，恢复任务已完成。由此 Grok 视频的创建、轮询、内容交付和按次结算均已通过，且没有重复生成或重复扣费。

### 媒体验收后的本地结算状态

- 图片成功已捕获：`0.01`；
- 图片前序失败全部释放：`0`；
- Grok 视频成功已捕获：`0.10`；
- 当前测试用户冻结余额为 `0.01`，只包括图片超时遗留预留；Grok 视频预留已正常转为捕获，不再重复冻结；
- 统一分组启用绑定仍全部指向 `active + schedulable` 账号，不合格绑定数为 `0`；本次没有修改 VPS 或原有分组。

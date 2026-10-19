# 原生 PostgreSQL 证据继承映射：447f188 → a3f0fc956

## 结论

**可继承此前选定 PostgreSQL 测试的功能结论，无需仅因本次提交重跑这 14 项。** 本文件是只读源码/证据审查，不是 a3f0fc956 的新执行结果。真实执行仍绑定到 447f188：14 个顶层测试、20 个子测试通过，0 失败、0 跳过，53 个自有进程全部清理。

- 已执行提交：`447f188437a895abf333e857cdabcde7f20d6273`，树 `a69e252cd14269cde401a892bb341e1b66b9575a`。
- 审查提交：`a3f0fc956e9a9a43660cd44bd3b3992e61cde144`，直接父提交就是上述已执行提交。
- 工作区干净；完整比较没有新增、删除或重命名文件。
- 本次只读取 Git 对象、文件及原证据；没有编译、测试、数据库启动、网络请求或仓库源码修改。

## 全部变更及对 PG 结论的影响

1. `backend/internal/server/http.go`：6 处 `//lint:ignore SA1019 ...` 改为逐行 `//nolint:staticcheck // SA1019: ...`。HTTP/2 wrapper、配置参数、分支和可执行语句不变。精确还原这 6 处注释语法后，与旧文件字节相同。
2. `backend/internal/service/openai_codex_models_service.go`：1 处同类 lint 注释语法更改，兼容理由不变。service 可以作为 repository/子进程的运行时依赖，因此不将此文件错误标为字节完全相同；但精确还原该注释后文件字节相同，运行时代码及依赖不变。
3. `backend/internal/service/openai_codex_models_service_test.go`：2 处同类 lint 注释语法更改，精确还原后字节相同。此 service 包专属 `_test.go` 不会因为 repository 或 smartrouter 导入 service 而编入其依赖；原 PG 命令没有执行 service 包测试。
4. `backend/internal/service/grok_custom_voice_test.go`：`TestGrokCustomVoiceRealtimeBlocksFrameAndContinues` 内两个 `defer conn/client.CloseNow()` 改为 `defer func() { _ = conn/client.CloseNow() }()`，显式忽略清理返回值。将旧文件中这两处精确替换即得到新文件，没有其他变化。此文件也是 service 专属测试，不参与原 repository 14 项或两个指定子进程测试；未改变生产代码、SQL 或 PG 断言。本判断不替代该 WS 测试自身的新回归结果。
5. `docs/hardening-phase3-20261018/00_PLAN_AND_GATES.md`：1 行替换，补充逐行 lint 注释语法和范围，无运行时作用。

共 5 个文件，12 行加入/12 行删除：9 处 lint 注释语法、2 处测试清理写法、1 处文档行。没有 SQL、迁移、所有权、事务/并发逻辑、配置值、模块依赖或选定 PG 断言变更。两个发生变化的生产 Go 文件均只改变普通 lint 注释，没有 Go build/embed 等编译指令变更。

## 完整字节级核验

按真实执行保存的 `code/all-frozen-source-manifest.json` 重新计算当前文件 SHA256，且核对文件集合：

- 4,468 个文件中 4,463 个完全相同；另 5 个恰好是上述完整 Git 变更清单。
- repository：356/356 文件相同，包含实际 SQL、全部选定断言、fixture、TestMain/harness helper 和 upstreamrefreshprobe 子包。
- migrations：320/320 文件相同，含 `247_grok_custom_voice_ownership.sql` 的严格 ID CHECK 和 profile JSONB 上限。
- Ent：331/331 文件相同。
- smartrouter：14/14 文件相同，含原 fresh-process 子测试。
- `backend/go.mod`、`backend/go.sum`、`grok_custom_voice.go` 及其他文件，除完整列出的 5 个之外全部相同。

不能宣称整个源码树字节相同；本结论是把已执行的 SQL/迁移/断言结果映射到新提交中保持相同的功能代码。

## 14 项执行范围映射

- `grok_custom_voice_repo_integration_test.go`：6 项、11 个子测试，覆盖隔离/分页/成员撤销、跨实例不可变所有权、并发唯一 ID 冲突、并发删除/更新不可复活、用户/账户删除后墓碑保留、实际迁移约束。测试及 SQL/迁移/fixture 全部相同。
- `smart_router_health_repo_integration_test.go`：2 项、2 个子测试，覆盖真实 PostgreSQL 持久化/新进程恢复和旧 ledger 迁移 fixture。断言和 core 子进程代码全部相同。
- `upstream_model_refresh_persistence_integration_test.go`：6 项、7 个子测试，覆盖持久化、fence/回滚、单次确认、原子性、group membership 变化拒绝和软删除账户绑定拒绝。断言、repository helper、probe 子进程代码全部相同。
- 原命令明确选择 `./internal/repository/` 的 14 个名称；两个 fresh-process 测试仅调用 `./internal/smartrouter/core`、`./internal/repository/upstreamrefreshprobe` 的指定测试，没有调用 `./internal/service` 测试。
- 原 bootstrap-only overlay、harness helper、运行器、输出和清理收据完整保留；本次没有以新结果替换它们。

## 原证据与保留限制

以下为独立证据归档 `native-postgres-447f188-evidence.tar.gz` 内的相对路径，不是仓库内可点击链接。原始归档尚未随GitHub发布，不能把此清单当作已交付的附件。

- 原结果汇总：`summary.json`
- 原源文件清单：`code/all-frozen-source-manifest.json`
- 原执行收据：`execution/raw-receipt.json`
- 原 JSONL 输出：`execution/tests.jsonl`
- 全部原证据 `SHA256SUMS` 再次验证通过。
- 原归档 SHA256：`40f0d84e4358052e30fd836dda05683bcb12868e005d876fb44957ea9b1b099e`。
- 原 summary SHA256：`7cfc282e15ec1c3e8f0c3f466ce3b5d063d48523d7ad6d6bddec552aef7e9186`。

仍仅证明选定 PostgreSQL18.1 Debian 原生行为。Docker/Testcontainers 启动、Alpine 等价性、Redis（既有 HTTP403/1010 阻塞）、真实供应商、生产部署和 PostgreSQL14–16 未被覆盖。不可将映射表述成“a3f0fc956 已重新执行原生 PG”或“全量未改动集成套件通过”。后续若更改 SQL、迁移、helper/断言、模块依赖或相关运行时语义，应重新评估并在受影响时重跑。

## SHA256 映射

### 全部变更文件

- `backend/internal/server/http.go`
  - 447f188：`659a23182629b591ddea3ca941a0cef1688b7d248c13c1d649d607d6e138ebe2`
  - a3f0fc956：`cefe9db6446e72aead7d5b92857e6e59fc15ceb3c8ded8b8d72884611e027bde`
- `backend/internal/service/grok_custom_voice_test.go`
  - 447f188：`2d5313a0401887cf1294ab634b69a7b560bf61fe04d217983c2817ea69875609`
  - a3f0fc956：`ee2afb0455fcc8aca354fb57a169ed35ad231fd9a1dc8938c4763b7383b61985`
- `backend/internal/service/openai_codex_models_service.go`
  - 447f188：`029ec4d6ee504b58300b702c1332222008aae64d00df36fec0905928f4b4753a`
  - a3f0fc956：`661127224311d1d2f032c01791b98e5288ccd01208e17adb63ea09d7b253a17e`
- `backend/internal/service/openai_codex_models_service_test.go`
  - 447f188：`ccbfce4c46b39c3f48a2324f9392c591494db47bbc13eacec5050b26b2f08bc0`
  - a3f0fc956：`bde8dbc3f865a41f605b1df537c698e470dc3131f8b2d4156be55aa7a8d07d35`
- `docs/hardening-phase3-20261018/00_PLAN_AND_GATES.md`
  - 447f188：`62f05d709ff17c35fc471031902d58b83d18fe40a87d1f6ab5bb54d5df7c77a0`
  - a3f0fc956：`c59a0259f77fa4f92289120bdb24d6da17f9ae55485aa22c29887d215667e414`

### 关键 PG 源文件（两提交相同）

- `backend/internal/repository/grok_custom_voice_repo_integration_test.go`：`4b220fc7bcccadff3496db2dbd14a7180a7a5a8d55c95de1757ac7ae182bce7a`
- `backend/internal/repository/grok_custom_voice_repo.go`：`5fc0617a962c129bd1326ac9c6c5d51d0e345d621c83c670694501e6a79c7a24`
- `backend/migrations/247_grok_custom_voice_ownership.sql`：`0a696e82dc993787fbca9fb1fc339c2ba14022bbaaa624efa5d0f81a9d41a958`
- `backend/internal/service/grok_custom_voice.go`：`67142b8ada79e73c706f5c8db241b89cb17a068c5a0d247faaa4230f2ff06c27`
- `backend/internal/repository/integration_harness_test.go`：`692369520058dffd8e088205e5985595949ab444ff40b6cdaacf3c4265c49489`
- `backend/internal/repository/fixtures_integration_test.go`：`34b4d877fa3ceb17e866588f68da96a940a6f9c967fa3ac0c17fb52cb4dd164a`
- `backend/internal/repository/account_repo.go`：`458e5b47aac51fca07130f23a05452eae28b10d7b9ef748f938e74d0510f7591`
- `backend/internal/repository/smart_router_health_repo_integration_test.go`：`497f5c3bf4c3998446bf9b6d0b3681766e26e53fb9c5b152e2a456fa9e05e5e5`
- `backend/internal/repository/upstream_model_refresh_persistence_integration_test.go`：`511cd7457ba2ca17531e114468b6f7e458ceb501d013602355a2831a616afdc0`
- `backend/internal/repository/upstreamrefreshprobe/refresh_persistence_child_integration_test.go`：`8b3a1e2dd52d2c54987b2b7d5acf648741a0b3ad3334e395e097ed4366743fd6`
- `backend/internal/smartrouter/core/smart_router_postgres_restore_integration_test.go`：`d4468c80f145721dcbe345c9ba2849846ace794868e7e54f8a448671f3fec89b`
- `backend/go.mod`：`2f5528892a99bfbf64547eb4dac7b0ff05a6eea9bc038b261ef573d9eebeab6d`
- `backend/go.sum`：`3bf02ceeb49d0db8db6c932c8a20b25be31bd3b5799075b66e6c92b6fa98ebb3`

基准清单 SHA256：`8c92ae2ca7a9ee96b424106568d5c2c9bc9cce0eeda0c11ecb45c379a7874e59`。
新提交树：`d2f2d2f15f9d5ec0a729c617ce8b0e62be49a9a1`。
核验时间：2026-10-18T11:01:11Z。

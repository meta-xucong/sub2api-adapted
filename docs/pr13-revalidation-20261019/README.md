# PR13 独立复审与最终提交复验

- 起点：`ddee35cd7e4110b60fc76c7d91a87528d250675e`。
- [先行计划、发现与最小修复](PLAN.md)。
- [部署、消费端、代理、财务与兼容边界](BOUNDARIES.md)。
- 发布位置：[Draft PR #13](https://github.com/meta-xucong/sub2api-adapted/pull/13)。未授权合并、部署或收费 Provider 请求。

## 当前源码范围

1. Redis 票据消费新增原始 payload 比较和 Redis TIME 截止时间检查；通用 OAuth 的 TryConsume 不变。
2. 票据兑换通过既有账号 reader 重查 active/存在性；保留外部 JSON 字段和旧 ticket-only 请求。
3. SmartRouter 图片 sticky、普通及最后兜底共用已有探查预算；不新增冷却状态或独立超时开关。
4. 图片转存总像素上限有界调整，容纳官方支持的非方形 4K；SSRF、代理拒绝和字节上限不放宽。

## 最终证据约定

先提交冻结候选，再执行本轮测试和独立复审。最终完整 SHA、tree、变更文件、精确命令/退出码/跳过/日志摘要及五项 PASS / FAIL / INSUFFICIENT_EVIDENCE 结论发布在 PR 描述或本轮审查回执中，必须与该候选匹配。CI 的 PR merge SHA 与直接 head 必须区分。旧阶段日志只作历史解释，不能代替本轮执行。文档中的计划或测试源文件存在不代表通过。

本轮执行矩阵：
- 核心健康/LastResort、sticky/预算、半开租约与取消、输出后禁止重试、计量、精确模型与非图片回归；unit 与 race。
- 票据逻辑/物理 TTL 差异、原子期限、payload 替换、并发、防重放、错误 intent、停用/删除及账号查询失败。
- 图片所有编码入口、已支持格式、字节/几何边界、公共 IP 绑定、重定向、代理 fail-closed。
- 隔离 PostgreSQL 的真实扣费事务/并发/冲突/清理/回滚；Docker TestMain 提前退出不计为 PostgreSQL 通过。
- 前端 lint/typecheck/关键测试、嵌入式构建；新提交 CI 的完整 unit/integration/lint、安全检查与22项浏览器回归。

## 必须保留的评估边界

- LastResort 对允许的 RecoveryCooling 分类生效，不局限于 image_poll_timeout；RecoveryModelUnavailable 和原生账号硬限制不可越过。
- 排队后准入拒绝的账号会进入当前请求 failed IDs，不在同一请求内重新纳入兜底；下一次独立请求才可按正常规则重新选择。不能为了尽量成功而清空失败账号排除。
- 租约是进程内有界协调，不是分布式全局唯一。持久化失败和旧版本清理器仍需运维约束。
- 旧共享内部 token 是跨用户可信服务权限；本轮没有新增消费端最小权限凭证。跨项目 intent、来源会话绑定与部署配置未验证项必须单列。
- 账号 active 重查不等于已签发票据绑定原 JWT/session 撤销。没有该绑定的会话撤销传播不能给 PASS。
- 未做生产、收费上游或部署验证，不能承诺所有原有功能在任意部署均不受影响。

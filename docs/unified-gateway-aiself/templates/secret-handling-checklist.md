# Secret handling checklist

- [ ] API key 只使用安全引用，不写入文档、截图、日志或 git diff。
- [ ] SSH 私钥、解密口令、JWT、cookie 和数据库口令不进入材料。
- [ ] credential JSON 只记录 provider 类型、字段 schema 和 hash/引用，不记录值。
- [ ] Authorization header、query token、provider task secret 全部脱敏。
- [ ] video URL 若包含签名参数，报告中只保留 host、路径摘要或 hash。
- [ ] 测试报告使用 request reference，不使用可重放的真实凭据。
- [ ] 导出数据库前去除 credential、password、secret、token、key 等字段。
- [ ] 发布/审计前执行 secret scan，并保留扫描时间和工具版本。
- [ ] 发现泄露时立即停止发布、撤回材料并轮换相关凭据；不要在聊天中复制泄露值。

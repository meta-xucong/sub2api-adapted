# 目录摘要说明

`inventory-summary` 将一份完整的 inspect 报告与一份 discover 报告汇总为本地报告，帮助人工查看：

- 每个账号的公开模型路由；
- 同一上游模型 ID 被哪些账号发现或映射；
- 账号内部映射冲突；
- 当前上游目录与已发现目录的覆盖情况。

运行格式：

```powershell
.\model-catalog-manager.exe inventory-summary `
  --inspect .\reports\inspect.json `
  --discover .\reports\discover.json `
  --out-dir .\reports\summary
```

输入报告必须完整、有效且在大小限制内。摘要仅写到指定本地目录，不会调用 Sub2API 管理 API，也不会修改账号或映射。报告仍可能含有账号 ID 和模型目录信息，请不要公开发布。

# Sub2API Smart Router 文档包

Smart Router 是一个模块化、可插拔的 Sub2API 调度增强层。它由独立核心模块、Sub2API 适配层、策略示例和运维手册组成，目标是让多条上游线路按成本、健康度、并发和恢复状态稳定分摊请求。

文档入口：

- `../SMART_ROUTER_DESIGN.md`: 产品设计和原则。
- `../SMART_ROUTER_DEVELOPMENT.md`: 开发规格、接口、测试计划和兼容性审计。
- `install.md`: 安装、启用、回滚与部署方式。
- `policy-examples.md`: 可复制的策略示例。
- `operations.md`: 运维、观测、排障和生产试运行流程。

第一阶段以 core patch mode 为主，即把独立 core 模块接入 Sub2API 自身的 OpenAI load-balance scheduler。默认关闭，不改变原行为。

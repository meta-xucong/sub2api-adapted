> 历史收据：本文件对应 a3f0fc956，最新云端候选请看 [PREUPLOAD_ACCEPTANCE.md](PREUPLOAD_ACCEPTANCE.md)。

# Phase3 代码候选执行收据：a3f0fc956

更新于2026-10-18 11:44 UTC。云端可执行的本轮代码验证已结束；在线扫描及 GitHub 发布仍待授权闭环，真实本地/生产验收未完成。

## 候选与结论

- 原始main：2bedb713b6b0b9cf968468decae36732f390574a；Phase3起点：ff27dd9c0cb5171b448c36d6851e7b275c1c759f。
- 最终代码候选：a3f0fc956e9a9a43660cd44bd3b3992e61cde144；tree：d2f2d2f15f9d5ec0a729c617ce8b0e62be49a9a1。
- 已识别范围：voice持久化隔离/ID/Realtime事件边界，Images终止失败已观察用量传播，订阅计价ctx，Wokey既有8 MiB解码分配边界，lint分类与HTTP2兼容。
- 未合并、未部署；未做真实付费生成、真实金额验收、VPS切换或生产数据迁移。远端branch/SHA/Draft PR/CI：[待实际发布核验]。

## 本候选已完成自动结果

证据：phase3-evidence/a3f0fc956/的command、exit、JSONL、summary；计数是顶层/子项，不能简单相加称独立场景。

| Gate | 结果 | 重要限定 |
| --- | --- | --- |
| golangci-lint | exit0，0 issues | 下述9处逐行兼容抑制仍存在 |
| unit | exit0；12138顶层/11650子项通过；18显式skip | 另3项AF_UNIX命令排除；60packagepass，51无测试文件 |
| integration标签 | exit0；6804顶层/6195子项通过；20顶层+2子项skip | 同3项AF_UNIX排除；55packagepass，57无测试文件；repository TestMain因缺Docker提前退出，不能计作该包测试已跑 |
| 定向race | exit0；23顶层/139子项通过，无skip | 仅service/handler中voice、builtin/handler、Images failed usage、subscription ctx选择器；非全库race |
| Linux amd64 embedded build | exit0；CGO_ENABLED=0，trimpath，tags embed | 二进制 SHA256：274b00a2a51424a0af25cfdfc4b1ff8cbc849c4c133c73f37b72923a7b5c25b5；构建不是启动健康检查 |

精确skip清单及日志hash见final-test-limitations-a3f0fc956.md。unit18项包含sentinel、TLS网络opt-in、Redis/PG资源、legacy NULL/sqlite差异、真实Provider和插件包条件；integration另有Docker限流、外部TLS不可达和parent-only probes。51/57个package skip全部是no-test-files，不能混成环境跳过。被命令排除的3项为TestOfferPluginHostServices_BrokerRoundtrip、UnimplementedIsGraceful、NilHostServicesNoop，未执行，不在显式skip计数内。

Integration外部tls.peet.ws请求因网络不可达跳过；没有以外网缺测作为TLS兼容通过，也未重试绕过限制。选定native PG证据不抵销prompt audit DSN缺失、Redis、插件、IPC或live-provider缺测。

## Native PostgreSQL：原执行及继承

- 实际执行SHA：447f188437a895abf333e857cdabcde7f20d6273，2026-10-18 10:27:53Z–10:32:07Z。
- PostgreSQL18.1 Debian/glibc临时loopback实例，14顶层/20子项全过，无失败/skip；其中voice6/11，既有PG回归8/9，含两个fresh-process断言。53个自有进程全部清理。
- SQL覆盖：用户/组/live account过滤及分页、跨连接池不可变归属、并发唯一ID、删除/更新不复活、墓碑、migration247约束、refresh fence/原子preview和Smart Router恢复。
- Bootstrap-only Go overlay替换Docker启动，保留选定测试/断言；不是完整未修改integration通过，不验证Docker/Alpine等价、Redis、PG14–16或生产迁移。
- native-pg-binding-a3f0fc956.md核对447→a3直接父子变更：4468文件中4463相同，另5仅9条lint注释语法、2条service测试清理、1条文档；repository356、migrations320、Ent331、smartrouter14文件全同，运行时代码/依赖及所选PG断言未变。可继承这14项功能结论，**不是a3重新执行PG测试**，也不能声称全树字节相同。
- 原摘要SHA256：7cfc282e15ec1c3e8f0c3f466ce3b5d063d48523d7ad6d6bddec552aef7e9186。
- native-postgres-447f188-evidence.tar.gz SHA256：40f0d84e4358052e30fd836dda05683bcb12868e005d876fb44957ea9b1b099e。归档不含数据库目录、server二进制或缓存。

## 历史失败与最终lint兼容说明

- 原始历史107位置=106主诊断+1关联位置，均匹配原始main五行上下文；仅29位置匹配记录官方父提交。机械清理不全是功能bug，不能把剩余78称官方遗留。
- 较早phase3末轮尚有11项：2个WS测试CloseNow未显式处理返回值，以及9处SA1019兼容注释未按golangci-lint识别的方式生效。a3将两个defer改为显式忽略清理返回值；将9处注释换成逐行 `//nolint:staticcheck // SA1019: <具体理由>`，不改变对应运行时代码。
- 9处=server call/配置6、生产GoAway类型1、测试fixture2。实际抑制选择器为该行staticcheck，SA1019是说明文字，不应声称工具仅对该行的SA1019代码施加过滤；没有文件级或全局排除。理由是独立H1/H2 idle配置及隐藏Error文本的typed GoAway兼容。0 issues不代表这9处弃用被删除。
- f940完整unit历史exit1、5失败记录保留：Aliyun3项因继承HTTP代理使loopback SDK请求超时，447仅在测试内t.Setenv并自动恢复；voice2项因OAuth cache-hit夹具落入一小时refresh窗口，447仅将合成有效期设到2*refreshSkew。生产代理/刷新策略不变；a3最终unit已通过，历史失败不改写。
- 更早Images fixture修正真实并发服务/slot fake并断言释放与无recover panic；102定向场景、三文件原实现overlay的66失败/36控制是有限反例证据，不是完整历史树运行。

## 前端和历史收据

Phase1/2是历史执行，不冒充本轮重跑；最终状态以本收据及最终SHA为准。phase3未改frontend（ff27dd9c至447比较无差异，447至a3五文件也不含frontend）。复用阶段二 06_IMPLEMENTATION_RECEIPT.md 已绑定的前端收据：345文件/2666测试通过，lint/typecheck/production build均exit0；实际运行早于1487候选提交，原始日志未单列started/ended，不虚构时间。本轮已核对ff27dd9c至a3的frontend字节无变化，工具和锁文件沿用。浏览器阻塞、在线漏洞数据及授权不会因源码不变而自动变PASS；旧backend通过也不能套用至已改代码。

## 必须保留的未测/限制

- 在线npm/Go扫描及推送自动触发扫描仍等待明确确认；不关闭工作流规避。真实启动/健康、浏览器CSP、缺失Redis/Docker/IPC及其他上述skip继续按真实证据填，未做就标未测。
- 未知legacy voice不自动认领；provider成功/SQL失败可能孤儿，需对账。账号ID维持同一provider namespace，换主体/origin应新账号及迁移评估。Realtime只接受已选账号上的owned voice，不迁移socket，多账号组不保证任意voice可用。
- 授权客户端音频在验证后、写入前观察，不等于provider确认接收；拒绝帧不启用计费。Images保留现有错误/重试优先级和schema，不做跨尝试聚合或新价格。
- 局部超时/metadata/目录/8MiB上限不构成全局内存容量保证；IP兼容默认未迁移，必须查实际持久化模式/可信代理/源站限制。
- 旧二进制回滚可能再开放共享voice；保留表/墓碑，另行评估接口隔离。真实Provider、金额、生产proxy、容量、部署PG版本与VPS均需独立授权验收。

## 最小本地接手顺序

1. 发布后核对branch/最终SHA/Draft PR；保留主工作区，用独立worktree，锁定repo工具和lockfile，不reset/clean用户文件。
2. 新建隔离空PG/Redis与合成配置，确认绝不连接生产；跑原unit/integration、迁移和缺测IPC，记录真实执行/skip。
3. lint、前端测试/typecheck/build、后端build；隔离启动健康检查和真实浏览器CSP正负案例。
4. 复验voice A/B隔离/撤权/墓碑/取消/Realtime账号边界、Images三路径三计价/重试、订阅ctx、8MiB边界和H1/H2独立超时。
5. 盘点legacy voice、部署IP设置及回滚方案；明确小样本目标、测试账号和费用额度后另行批准真实provider与计费验收。再评估合并/上线，当前不自动合并部署。

交付建议：“本轮已识别缺陷的范围内修复已形成候选，lint、明确范围unit/integration及定向race通过；选定真实PG结果按源绑定继承。build也已通过；列明的环境/授权门槛仍保留。这是隔离验收候选，不承诺所有可能缺陷已消除或可直接上线。”

## 构建身份

该构建未注入发行ldflags，--version退出0并显示0.2.12、commit/built为unknown；不能用这个显示值证明发行身份。go version -m已确认vcs.revision=a3f0fc956e9a9a43660cd44bd3b3992e61cde144、vcs.modified=false、linux/amd64、CGO_ENABLED=0。它是云端验证产物，不是已发布镜像；正式镜像应由最终SHA构建并核对版本和摘要。

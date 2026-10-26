# Aliyun Bailian → YeToken → 404token Price Ledger

> Snapshot date: 2026-10-26  
> Scope: the active model mappings currently recorded for the YeToken Bailian account (#25) on 404token. This is not a complete catalog of every model offered by Bailian. Append new routes when they are added.  
> Base prices: CNY per 1 million input/output tokens, using the Bailian mainland/global prices shown in the official price page unless a row notes peak/off-peak pricing.

## Calculation rules

For a confirmed Bailian model and matching YeToken route:

- **Bailian list price** = official Bailian input/output rate.
- **YeToken reference price** = Bailian list price × **0.4**.
- **404token group #16 reference price** = YeToken reference price × **1.25** (the current group multiplier).
- Therefore, for this account/group and these routes, the calculated 404token reference price is Bailian list price × **0.5**.

These are per-token tariff references, before any separate UI currency/unit conversion. They do not include batch discounts. Cache-hit input is listed separately only where its official source price is confirmed. DeepSeek busy/idle periods follow Bailian's Beijing-time schedule: busy 08:00–22:00; idle 22:00–08:00.

## Current mapped routes

All prices below are CNY per 1M tokens. In each rate cell, input/output are shown in that order.

| 404token model / route alias | YeToken upstream model ID or likely SKU | Bailian official input / output | YeToken ×0.4 input / output | 404token ×1.25 input / output | Evidence |
|---|---|---:|---:|---:|---|
| `deepseek-v4-flash` | `deepseek-v4-flash-0731` | Busy 3 / 9; idle 1.5 / 4.5 | Busy 1.2 / 3.6; idle 0.6 / 1.8 | Busy 1.5 / 4.5; idle 0.75 / 2.25 | SKU identified; list price confirmed |
| `deepseek-v4-flash-0731` | `deepseek-v4-flash-0731` | Busy 3 / 9; idle 1.5 / 4.5 | Busy 1.2 / 3.6; idle 0.6 / 1.8 | Busy 1.5 / 4.5; idle 0.75 / 2.25 | SKU and list price confirmed |
| `deepseek-v4-flash-vision-exp` | `deepseek-v4-flash-vision-exp` | **Unverified** | **Unverified** | **Unverified** | No exact Bailian SKU/price match found |
| `deepseek-v4-pro` | `deepseek-v4-pro-0813` | Busy 9 / 27; idle 4.5 / 13.5 | Busy 3.6 / 10.8; idle 1.8 / 5.4 | Busy 4.5 / 13.5; idle 2.25 / 6.75 | SKU identified; list price confirmed |
| `deepseek-v4-pro-0813` | `deepseek-v4-pro-0813` | Busy 9 / 27; idle 4.5 / 13.5 | Busy 3.6 / 10.8; idle 1.8 / 5.4 | Busy 4.5 / 13.5; idle 2.25 / 6.75 | SKU and list price confirmed |
| `deepseek-v4.1-flash` | `deepseek-v4.1-flash` | Busy 2 / 8; idle 1 / 4 | Busy 0.8 / 3.2; idle 0.4 / 1.6 | Busy 1 / 4; idle 0.5 / 2 | SKU and list price confirmed |
| `glm-5.2` | `glm-5.2` | 8 / 28 | 3.2 / 11.2 | 4 / 14 | SKU and list price confirmed |
| `glm-5.3` | `glm-5.3` | 8 / 28 | 3.2 / 11.2 | 4 / 14 | SKU and list price confirmed |
| `glm-5.3-flash` | `ZHIPU/GLM-5.3-Flash` | 0.8 / 2.8 | 0.32 / 1.12 | 0.4 / 1.4 | Exact third-party model SKU and price confirmed |
| `hy4` | `hy4` | **Unverified** | **Unverified** | **Unverified** | Exact Bailian SKU not identified |
| `hy4-preview` | `hy4-preview` | **Unverified** | **Unverified** | **Unverified** | Exact Bailian SKU not identified |
| `kimi-k3` | `kimi-k3` | 20 / 100; cache-hit input 2 | 8 / 40; cache-hit input 0.8 | 10 / 50; cache-hit input 1 | Exact SKU/price confirmed; current 404token live billing tests matched calculated prices |
| `minimax-m3` | `MiniMax/MiniMax-M3` (mapping by model name; confirm exact upstream ID) | Input ≤512K: 2.1 / 8.4; input >512K: 4.2 / 16.8 | ≤512K: 0.84 / 3.36; >512K: 1.68 / 6.72 | ≤512K: 1.05 / 4.2; >512K: 2.1 / 8.4 | Official SKU/price found; route SKU mapping needs confirmation |
| `muse-spark-1.3-contributor-free` | `muse-spark-1.3-contributor-free` | **Unverified** | **Unverified** | **Unverified** | No exact Bailian SKU/price match found |
| `qwen3.8-flash` | `qwen3.8-flash` | 0.8 / 2.7 | 0.32 / 1.08 | 0.4 / 1.35 | SKU and list price confirmed |
| `qwen3.8-max` | `qwen3.8-max` | 12 / 36 | 4.8 / 14.4 | 6 / 18 | SKU and list price confirmed |

## Verification notes

- Four recent live 404token Kimi-K3 test requests (Chat Completions/Responses, default/high reasoning) matched the expected group-#16 calculation exactly: input 20 × 0.4 × 1.25 = 10; output 100 × 0.4 × 1.25 = 50. These tests had no cache-hit tokens, so cache pricing is based on the official card and formula, not a live cache-hit test.
- The other numeric 404token values above are **formula-derived reference prices**, not individually live-reconciled usage rows. Their exact upstream route SKU, regional tariff, cache handling, and actual request billing should be verified before using them to reconcile a specific charge.
- The current 404token route metadata may still describe some entries as manual fallbacks. A row marked “confirmed” here means the official SKU and list price were found; it does not certify every runtime billing branch or account's region.
- `deepseek-v4-flash-vision-exp`, `hy4`, `hy4-preview`, and `muse-spark-1.3-contributor-free` remain unresolved. Do not apply a similarly named model's tariff until the upstream SKU is confirmed.
- Bailian prices can vary by region, peak/off-peak window, input-length tier, batch mode, or cache hit. Keep those conditions attached to a model when updating this ledger.

## Official references

1. [Bailian model pricing (Chinese)](https://help.aliyun.com/zh/model-studio/model-pricing) — Kimi, DeepSeek, Qwen, GLM, MiniMax and other model tariffs; accessed 2026-10-26.
2. [Qwen3.8-Flash model page](https://help.aliyun.com/zh/model-studio/qwen3-8-flash) — confirms the original Qwen3.8-Flash input/output and cache-hit rates.
3. [Bailian model pricing (English)](https://help.aliyun.com/en/model-studio/model-pricing) — corroborating price listing.

## Update procedure

When a new YeToken Bailian route is added or a price changes, append/update the route using this sequence:

1. Record the 404token visible model and exact YeToken upstream model ID.
2. Match that ID to the exact Bailian official Model ID and region.
3. Record the official input/output rate, plus cache, tier, time-window, or batch conditions if applicable.
4. Calculate YeToken as official × 0.4.
5. Calculate the current 404token reference as YeToken × the applicable group multiplier. Do not assume 1.25 if group pricing changes.
6. Mark the row **live verified** only after matching a request's model ID, token usage, route/account and 404token charge. Otherwise keep it **formula-derived**.
+

---

# 阿里云百炼 → YeToken → 404token 计价台账（中文版）

> 快照日期：2026-10-26  
> 范围：当前 404token 上记录的 YeToken 百炼账号（#25）已启用模型映射。此文档不是百炼全部模型目录；新增线路时应追加对应记录。  
> 基准价格：每 100 万输入/输出 Token 的人民币价格；除注明忙时/闲时的行外，采用百炼官方价格表中的中国大陆/全球价格。

## 计算规则

针对已确认的百炼模型及其 YeToken 对应线路：

- **百炼原价** = 百炼官方输入/输出单价。
- **YeToken 参考价** = 百炼原价 × **0.4**。
- **404token #16 组参考价** = YeToken 参考价 × **1.25**（当前组倍率）。
- 因此，对该账号和组内这些线路，推算的 404token 参考价 = 百炼原价 × **0.5**。

以上是每百万 Token 的计价参考值，未计入 UI 另行进行的货币/单位换算，也未包含 Batch 折扣。只有官方来源价格已确认的模型才单独列缓存命中输入价。DeepSeek 忙时/闲时按北京时间：忙时 08:00–22:00，闲时 22:00–次日 08:00。

## 当前模型映射

以下价格单位均为人民币/百万 Token。每个价格单元格按“输入/输出”顺序排列。

| 404token 模型/路由别名 | YeToken 上游模型 ID 或可能的 SKU | 百炼官方输入/输出 | YeToken ×0.4 输入/输出 | 404token ×1.25 输入/输出 | 证据 |
|---|---|---:|---:|---:|---|
| `deepseek-v4-flash` | `deepseek-v4-flash-0731` | 忙时 3 / 9；闲时 1.5 / 4.5 | 忙时 1.2 / 3.6；闲时 0.6 / 1.8 | 忙时 1.5 / 4.5；闲时 0.75 / 2.25 | 已识别 SKU，官方价格已确认 |
| `deepseek-v4-flash-0731` | `deepseek-v4-flash-0731` | 忙时 3 / 9；闲时 1.5 / 4.5 | 忙时 1.2 / 3.6；闲时 0.6 / 1.8 | 忙时 1.5 / 4.5；闲时 0.75 / 2.25 | SKU 及官方价格已确认 |
| `deepseek-v4-flash-vision-exp` | `deepseek-v4-flash-vision-exp` | **未核实** | **未核实** | **未核实** | 尚未找到精确的百炼 SKU/价格 |
| `deepseek-v4-pro` | `deepseek-v4-pro-0813` | 忙时 9 / 27；闲时 4.5 / 13.5 | 忙时 3.6 / 10.8；闲时 1.8 / 5.4 | 忙时 4.5 / 13.5；闲时 2.25 / 6.75 | 已识别 SKU，官方价格已确认 |
| `deepseek-v4-pro-0813` | `deepseek-v4-pro-0813` | 忙时 9 / 27；闲时 4.5 / 13.5 | 忙时 3.6 / 10.8；闲时 1.8 / 5.4 | 忙时 4.5 / 13.5；闲时 2.25 / 6.75 | SKU 及官方价格已确认 |
| `deepseek-v4.1-flash` | `deepseek-v4.1-flash` | 忙时 2 / 8；闲时 1 / 4 | 忙时 0.8 / 3.2；闲时 0.4 / 1.6 | 忙时 1 / 4；闲时 0.5 / 2 | SKU 及官方价格已确认 |
| `glm-5.2` | `glm-5.2` | 8 / 28 | 3.2 / 11.2 | 4 / 14 | SKU 及官方价格已确认 |
| `glm-5.3` | `glm-5.3` | 8 / 28 | 3.2 / 11.2 | 4 / 14 | SKU 及官方价格已确认 |
| `glm-5.3-flash` | `ZHIPU/GLM-5.3-Flash` | 0.8 / 2.8 | 0.32 / 1.12 | 0.4 / 1.4 | 第三方模型精确 SKU 和价格已确认 |
| `hy4` | `hy4` | **未核实** | **未核实** | **未核实** | 尚未识别精确的百炼 SKU |
| `hy4-preview` | `hy4-preview` | **未核实** | **未核实** | **未核实** | 尚未识别精确的百炼 SKU |
| `kimi-k3` | `kimi-k3` | 20 / 100；缓存命中输入 2 | 8 / 40；缓存命中输入 0.8 | 10 / 50；缓存命中输入 1 | SKU/价格已确认；近期 404token 实际计费测试与推算价一致 |
| `minimax-m3` | `MiniMax/MiniMax-M3`（按模型名匹配；需确认精确上游 ID） | 输入 ≤512K：2.1 / 8.4；输入 >512K：4.2 / 16.8 | ≤512K：0.84 / 3.36；>512K：1.68 / 6.72 | ≤512K：1.05 / 4.2；>512K：2.1 / 8.4 | 官方 SKU/价格已找到；仍需确认路由 SKU 映射 |
| `muse-spark-1.3-contributor-free` | `muse-spark-1.3-contributor-free` | **未核实** | **未核实** | **未核实** | 尚未找到精确的百炼 SKU/价格 |
| `qwen3.8-flash` | `qwen3.8-flash` | 0.8 / 2.7 | 0.32 / 1.08 | 0.4 / 1.35 | SKU 及官方价格已确认 |
| `qwen3.8-max` | `qwen3.8-max` | 12 / 36 | 4.8 / 14.4 | 6 / 18 | SKU 及官方价格已确认 |

## 核验说明

- 最近 4 次 404token Kimi-K3 实测（Chat Completions/Responses、默认/高推理）均与 #16 组预期计算完全一致：输入 20 × 0.4 × 1.25 = 10，输出 100 × 0.4 × 1.25 = 50。这些测试没有缓存命中 Token，因此缓存价来自百炼官方价格卡和公式推算，未经过缓存命中的实时请求验证。
- 上表其他模型的 404token 数值是**按公式推算的参考价**，并未逐一与线上用量明细核销。用于核对某笔具体消费前，应确认实际上游 SKU、地域价格、缓存处理和请求计费路径。
- 404token 当前部分路由元数据可能仍标记为手动回退价。此处“已确认”表示找到了官方 SKU 和标价，不代表已验证每种运行时计费分支或账号地域。
- `deepseek-v4-flash-vision-exp`、`hy4`、`hy4-preview` 和 `muse-spark-1.3-contributor-free` 仍未解决映射问题。确认上游 SKU 前，不要直接套用名字相似的模型价格。
- 百炼价格可能因地域、忙闲时段、输入长度、Batch 模式或缓存命中而变化。更新台账时应保留这些条件。

## 官方参考链接

1. [百炼模型价格（中文）](https://help.aliyun.com/zh/model-studio/model-pricing) —— Kimi、DeepSeek、Qwen、GLM、MiniMax 等模型价格；查询日期 2026-10-26。
2. [Qwen3.8-Flash 模型页面](https://help.aliyun.com/zh/model-studio/qwen3-8-flash) —— 核对 Qwen3.8-Flash 原始输入、输出及缓存命中价格。
3. [百炼模型价格（英文）](https://help.aliyun.com/en/model-studio/model-pricing) —— 交叉核对官方价格表。

## 更新步骤

新增 YeToken 百炼模型或价格发生变化时，按以下步骤更新：

1. 记录 404token 前端模型名和 YeToken 上游的精确模型 ID。
2. 将该 ID 与百炼官方 Model ID 及地域精确匹配。
3. 记录官方输入/输出单价，并注明缓存、阶梯、时段或 Batch 条件。
4. YeToken 价按官方价格 × 0.4 计算。
5. 404token 参考价按 YeToken 价格 × 当前适用的分组倍率计算；分组倍率变更后不能继续默认使用 1.25。
6. 只有把请求的模型 ID、Token 用量、路由/账号和 404token 扣费对应起来后，才能标记为**已实测核验**；否则标为**公式推算**。




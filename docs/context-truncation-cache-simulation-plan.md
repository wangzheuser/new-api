# 上下文截断与缓存用量模拟实施方案

- 作者：wangqiupei
- 状态：已实施并完成本地验证（2026-09-08）；详细证据见第 13 节。
- 约定：正文是唯一实施规则；历史备选仅在文末保留，不同时实现。
- 结论：两项功能已落地为独立配置、纯算法、请求接入、计费调整和共享编辑器；默认关闭，无新增业务表、外部缓存或通用插件框架。

## 1. 已确认范围

1. 发送前裁剪最早完整历史；保留当前问题、system/developer、工具定义和必要工具依赖。
2. 实际发生截断时，按截断前完整输入本地估算计费；仅对有效客户端 usage 补全逻辑输入以恢复自动压缩感知。原始上游用量与结算副本独立保存，模拟缓存不写入客户端响应。
3. 缓存创建、读取独立概率触发；任意上游缓存字段出现（包括显式零）就不模拟整组。
4. 截断按模型 ID 配置，缓存按全局/渠道整体配置；渠道编辑都在高级设置中。
5. 先做 default UI；classic 只验证并修复必要的未知配置字段保留，不新增编辑界面。
6. 四种主要文本协议是完整开发目标，逐条 HTTP/SSE 验收；每项功能独立判断适用性，不共用一个笼统跳过开关。
7. 全局默认策略和全站紧急停用区分；功能分别停用，计费结算遵循请求已冻结的规则。

## 2. 当前代码核查

### 2.1 kiro.rs 可借鉴的内容

本轮读取的是 `/Users/777java/777/projects/github/kiro.rs` 当前工作区，不只是 Git 提交。HEAD 为 `0b881768edcecafb594234b32d555037cf708674`，该项目存在包括截断、handler 在内的未提交修改，本轮保持原样。

| 文件 | 当前实现与借鉴点 |
|---|---|
| `/Users/777java/777/projects/github/kiro.rs/src/anthropic/truncation.rs` | 已有独立截断模块；计算输入预算时考虑模型窗口、输出预留和余量；按轮次及工具依赖组织保留范围；还包含更激进的裁剪策略。 |
| `/Users/777java/777/projects/github/kiro.rs/src/anthropic/retry.rs` | 上下文超限后的截断重试与逐步收紧，不宜整套直接移植到多渠道网关。 |
| `/Users/777java/777/projects/github/kiro.rs/src/anthropic/handlers.rs` | `pre_truncation_tokens` 保存截断前输入；非流式最终输入取上游上下文估算与保存输入的较大值，然后分配缓存 token。 |
| `/Users/777java/777/projects/github/kiro.rs/src/anthropic/cache_metering.rs` | 按 `tools + system + 历史 messages` 累积前缀哈希，查询最长命中前缀；使用 TTL、容量限制和成功后提交；`split_against_total` 保证普通输入、创建、读取互斥且总量守恒。 |
| `/Users/777java/777/projects/github/kiro.rs/src/anthropic/stream.rs` | 将模拟缓存用量写入 Messages 流式 usage；仍然请求上游。 |

**重要区别：kiro.rs 当前缓存模拟是“前缀命中驱动”，不是“按百分比随机触发”。** 本需求可以借鉴它的用量拆分与独立文件设计，但概率策略需要另行实现。kiro.rs 保存的是缓存计量元数据，不是真正的模型推理缓存或回答缓存。

### 2.2 new-api 已有的基础

第一轮代码基线：`d4f2450a6bc7b991eec426744abbb178583439aa`。工作区已有未跟踪目录、文档和日志，本轮不调整它们。

以下路径均以 `/Users/777java/777/projects/github/new-api` 为根目录；路径写为完整路径，便于后续开发直接定位。

| 文件 | 已核查的行为及影响 |
|---|---|
| `/Users/777java/777/projects/github/new-api/controller/relay.go` | 请求计数 → 上下文兜底预演 → 价格/预扣 → 渠道重试 → 协议 handler。预扣发生在发送前，不能只修改最终 usage。 |
| `/Users/777java/777/projects/github/new-api/controller/context_fallback.go` | 已有超长上下文切换模型/渠道功能，不是截断；目标仍超窗时目前直接返回错误，需要明确与新截断功能的先后关系。 |
| `/Users/777java/777/projects/github/new-api/relay/system_prompt.go` | 已有不改变原请求的系统提示词预演及注入 token 计算，可复用。 |
| `/Users/777java/777/projects/github/new-api/relay/compatible_handler.go`、`/Users/777java/777/projects/github/new-api/relay/claude_handler.go` | 已对请求深拷贝；分别处理模型映射、提示词注入、透传和转换；适合作为截断的薄接入点。参数覆盖可能在更后面改写请求。 |
| `/Users/777java/777/projects/github/new-api/relay/common/relay_info.go` | 已区分 requested、routing、attempt、upstream 模型；渠道重试会重置本地输入估算，新增计费基线需要独立保存。 |
| `/Users/777java/777/projects/github/new-api/service/token_counter.go` | `EstimateRequestToken` 受 `CountToken` 开关影响，关闭时返回 0；`CountRequestToken` 可显式计数，截断开启时应复用它，而不是修改进程全局开关。 |
| `/Users/777java/777/projects/github/new-api/dto/billing_usage.go`、`/Users/777java/777/projects/github/new-api/service/billing_usage.go` | 已保存和恢复协议转换前的上游计费 usage。只改外层 `PromptTokens`，可能被原始 `BillingUsage` 覆盖。 |
| `/Users/777java/777/projects/github/new-api/service/text_quota.go` | `PostTextConsumeQuota` 先恢复有效计费 usage，再走普通倍率/表达式计费；两条路径必须使用同一份调整结果。缓存用量还参与渠道亲和性观测，模拟数据不得混入真实观测。 |
| `/Users/777java/777/projects/github/new-api/dto/openai_response.go`、`/Users/777java/777/projects/github/new-api/dto/claude.go` | 缓存计数大多是普通整数；反序列化后单看数值 0，区分不了“字段没返回”和“明确返回 0”。 |
| `/Users/777java/777/projects/github/new-api/pkg/billingexpr/expr.md` | 已完整阅读。`len` 是完整输入上下文；`p/cr/cc/cc1h` 按表达式使用情况进行归一化，不能重复计价。 |
| `/Users/777java/777/projects/github/new-api/model/channel.go`、`/Users/777java/777/projects/github/new-api/dto/channel_settings.go` | 已有 `setting` 和 `settings` 两套 JSON 配置；`ChannelOtherSettings` 实际保存在渠道 `settings` 字段。新配置可复用现有列，无需增加业务表。 |
| `/Users/777java/777/projects/github/new-api/web/default/src/features/channels/components/drawers/sections/channel-advanced-section.tsx` | default 主题已有高级设置容器，可加入独立配置组件。 |
| `/Users/777java/777/projects/github/new-api/web/default/src/features/system-settings/models/global-settings-card.tsx` | 已有全局模型设置入口，新功能宜独立卡片，避免继续堆大组件。 |

第一轮属于静态分析：没有请求外部模型、修改运行配置或运行功能回归；后面的测试列表是开发验收要求，不是已通过结果。

## 3. 截断规则与默认值

### 3.1 配置

| 字段 | 默认/校验 | 含义 |
|---|---|---|
| `window_tokens` | 必填正整数，无跨模型通用默认值；上限 2147483647 | 管理员填写模型上下文窗口 |
| `threshold_percent` | 90；整数 1–100 | 输入阈值占窗口的比例 |
| `safety_tokens` | 缺失/null 表示自动：`min(ceil(window_tokens × 2 / 100), 8192)`；手动整数 0–window-1 | 防止估算贴近硬上限，0 不被默认覆盖 |
| `keep_recent_turns` | 1；整数 1–256 | 至少保留最近完整轮次数，依赖保护可能多保留 |
| `output_reserve_tokens` | 缺失/null 表示使用有效请求上限或已知协议默认；手动正整数且小于窗口 | 无可确认输出上限时，管理员提供本地预算预留，不改写输出参数 |

窗口不代表真实可用性的自动保证。`output_reserve_tokens` 是预算，不是新价格倍率；采用它会增加配置，但解决 Chat/Responses/Gemini 未传输出上限、服务端默认又未知的实际缺口。

有效输出预留 R：取实际生效请求输出上限/现有明确协议默认与管理员预留中的较大值。Chat 同时出现两个 max-tokens 字段时沿用现有计数的较大值；Messages 使用现有模型默认逻辑；Responses/Gemini 不猜测未知上游默认。两者都缺失时，本次返回 `context_truncation_output_reserve_required`，不修改原请求强加一个生成上限。输出上限与请求 validator 的既有上界一致。

```text
S = 自动安全余量或显式 safety_tokens
输入预算 L = min(floor(window_tokens × threshold_percent / 100), window_tokens - R - S)
```

L≤0 返回 `context_length_exceeded`。已填固定参数彼此冲突在保存时拒绝；与本次 R 的冲突在请求发送前拒绝。采用 64 位中间整数并检查上界，不裸转 quota。

### 3.2 裁剪与完整性

- 只有输入估算大于 L 才删；等于 L 原样通过。开启不等于每次都裁剪或每次按本地估算收费。
- 从最早可删轮次开始；user 开始的业务轮次包含其 assistant 和工具交互，工具结果不是无条件的新业务轮次。
- 全局 system/developer 和工具定义独立保护；当前轮、最近 N 轮及其工具依赖闭包固定保留。
- 调用 ID/结果 ID 形成依赖约束；批量工具、跨轮结果、assistant 前缀补全均完整保留。原请求 ID 缺失/重复/不合法时沿用原协议校验，不尝试拼造修复。
- JSON、思考签名、工具参数块作为原子内容；不裁当前文字、不压缩工具结果、不用摘要模型。
- 没有可删内容仍超限时明确返回错误，不静默发超预算请求。
- `CountToken=false` 时仅本次有效截断路径调用 `CountRequestToken`，不修改全局开关。
- 按轮次索引和依赖分组，减少全量重复计数；最终完整验证。异常计数/耗时取消时不进入上游，禁止删一条重算全部的平方级实现。

### 3.3 与路由、提示词、参数覆盖的唯一顺序

1. 保留客户端原始请求；现有敏感内容校验仍对原请求执行，截断不能绕过它。
2. 已有上下文 fallback 先完成候选选择，目标仍超窗但有有效截断规则时进入截断预演，不在旧提前拒绝点直接终止。
3. 当前候选处理模型映射、已有提示词注入和可支持的内容/输出参数变换，得到未裁剪准备副本。策略按**当前尝试的逻辑模型 ID（渠道映射前）**精确匹配；价格身份仍使用已冻结的 routing/billing 模型。
4. 冻结截断前计费估算，预演裁剪；在真正发送前完成必要预扣/补充预扣。复用准备结果，注入与覆盖不执行两遍。
5. 在当前尝试副本上裁剪，转换并最终检查实际发送形态；最终检查只验证，不重新决定原始计费基线。
6. 影响内容/输出上限的参数覆盖若在该路径只能后置执行，第一版确定返回 `context_truncation_override_conflict`。不得保留“也可以重算”的第二实现分支；不影响内容的温度等覆盖保留现有顺序。
7. 转换器新增已知系统内容应在预算预演计入；无法在既有协议路径准确准备/检查的路径记录 `unsupported_request_transform` 并按截断配置冲突失败，不宣称已支持。不新增通用可插拔执行流水线。
8. A 失败换 B 时，从原始请求重建 B 副本，重新读取 B 的配置快照与注入内容，不拿 A 的裁剪结果继续删。计费模型与价格快照不随重试改变。

系统提示词收费不因本功能新增规则：延续项目既有注入计费语义，冻结的 E_before 包含该候选正常计费会纳入的注入内容；不得重复加上旧 `InjectedPromptTokenDelta`。发送预算需计入所有实际发送内容。路由预演的计费向量与真正发送前基线一致性纳入回归。

## 4. 缓存模拟、概率和来源

### 4.1 默认值与计算

| 输入框 | 初始默认值 | 范围 |
|---|---:|---|
| 创建触发概率 `creation_trigger_percent` | 20% | 整数 0–100 |
| 读取触发概率 `read_trigger_percent` | 60% | 整数 0–100 |
| 创建 token 比例 `creation_token_percent` | 30% | 整数 0–100 |
| 读取 token 比例 `read_token_percent` | 50% | 整数 0–100 |

两个触发概率分别使用独立随机值；禁止同时配置 100%。两个 token 比例之和≤100；显式 0 有意义。每次客户端请求生成一对服务端随机值并冻结，重试复用这对值、套用最终候选策略；不受客户端 request-id 控制，不逐 SSE 帧抽签。客户端重新提交是新请求，不提供跨请求随机结果幂等承诺。无需 Redis、前缀哈希缓存或跨请求计数器。

```text
C = 创建触发 ? floor(B × creation_token_percent / 100) : 0
R = 读取触发 ? floor(B × read_token_percent / 100) : 0
N = B - C - R
N + C + R = B
```

B 是本次计费输入总量：有截断时采用第 5 节口径，无截断时采用原有有效上游用量总量。模拟创建使用通用/5m 类别，不制造 1h 子项。

默认独立分布：仅创建 8%、仅读取 48%、同时 12%、均无 32%。创建少、读取多是面向多轮对话的工程初始假设，**不是本站生产实测或真实命中率**。允许连续同时命中，不强制交替。长期期望 token 分类（忽略取整）为普通 64%、创建 6%、读取 30%，不等于固定费用折扣；价格继续走原有倍率或表达式。

### 4.2 上游缓存字段存在性

缓存解析元数据至少记录 `unknown / absent / present / invalid`；数值为 0 不等于 absent。元数据放独立类型，不批量把现有 DTO 的整数改成指针；不将内部元数据序列化给客户端。

| 原始上游事实 | 模拟行为 |
|---|---|
| 合格成功响应、可信正输入总量、全程无任何缓存字段 | 可以抽样模拟 |
| 任意缓存字段存在，数值为 0 或正数 | 整组不模拟，保留原有真实缓存处理 |
| 只返回创建或读取一项 | 整组不模拟，不补另一项 |
| null、负数、错误类型、冲突导致用量无效 | 不模拟，记录 invalid，沿用上游异常处理 |
| 未接入字段采集的解析器 | unknown，不模拟 |
| 无 usage、输入仅为本地估算或输入总量为 0 | 不模拟，原有估算计费单独继续 |
| 转换器/本地估算自行补出的零字段 | 不能据此认定上游已返回缓存数据 |
| 流式早期缺字段、末帧返回缓存字段 | 按完整流聚合事实，不提前模拟 |
| 失败或部分输出后断流 | 不模拟，真实缓存仍按既有规则使用 |

presence 在原始 JSON/SSE 解析入口采集，之后再做转换或用量补零；一旦发现 present/invalid，后续缺字段不能把状态改回 absent。缓存模拟默认关闭时不额外进行此项解析。

现有 OpenRouter 成本反推等派生缓存如果已生成非零用量，也不叠加模拟；派生值保留单独来源，不冒充上游字段存在。真实上游元数据和原有派生用量优先于新增随机模拟。

## 5. 计费与失败行为

### 5.1 输入与输出

- `E_before`：最终候选、截断前本地计费输入估算；`E_after`：截断后发送估算；`U`：上游归一化实际输入总量。
- 发生截断：B=E_before。没有实际截断：保留原有有效计费输入，不因开启开关而一律换成本地估算。
- 本功能支持的纯文本、图片及工具请求中，真实缓存 token 原值保留，普通输入=B-真实创建-真实读取；若 B 小于真实缓存子项合计，最低提升到子项合计并记录 `estimate_conflict`，绝不产生负输入。
- 真实缓存创建总量与 5m/1h 明细不是彼此独立相加的三项，延续现有合并算法；不重复计费。
- 仅在符合第 4 节门禁时用 B 拆模拟类别。普通倍率和表达式必须接收同一个调整后的副本。
- 输出沿用上游真实计数或既有缺失估算。免费、按次、工具附加费、分组、订阅、钱包和令牌额度不改变现有合同。
- `len` 使用完整 B；普通/创建/读取按实际 usage semantic 编码，再交给现有表达式归一化。不能将 N 写成 OpenAI 总输入，也不能将 B 写成 Anthropic 普通输入后再重复加缓存。
- 表达式 `param()`、价格和计费模型快照仍遵循原始已冻结规则，不读取裁剪后的正文来悄悄换价格条件。
- 图片请求发生实际截断时，同样按截断前估算计费；图片 token 属于本地估算，不保证与上游完全一致。音频、文件等未支持内容在命中截断规则时本地报错。缓存模拟仍跳过多模态，不把可重叠模态计数简单相加当下界。

### 5.2 预扣与补扣

沿用 `BillingSession`；`Reserve(targetQuota)` 是现有补充预扣入口，不创建第二个资金会话。普通倍率对可发生的分类组合计算预扣估算；任意表达式只是候选向量试算，**不宣称是最终金额的严格上限**。不改变已有信任额度旁路、免费模型行为，普通受预扣约束的请求余额不足在发送前失败。最终实际金额仍按既有 Settle 差额结算。

先保留现有预扣保障，再在候选准备结果形成后、发出请求前补到目标；补充预扣失败需清理本次准备资源并沿现有 finalizer 退款。不为裁剪被删除的历史额外二次扣费。

### 5.3 失败/断流决策表

本轮已核查 `textBillingFinalization`：**仅 HTTP 头或 ping 发出不算业务内容提交；流式发生错误且 `ClientPayloadIsCommitted()` 才部分结算**。复用该状态机，而不是根据 HTTP 200 或是否有 usage 自行创建第二套结算条件。

| 场景 | 本次模型用量结算 | 输入/输出 | 缓存模拟 | 预扣处理 |
|---|---|---|---|---|
| 本地裁剪/校验失败，尚未发送 | 不结算 | 不产生消费输入 | 否 | 无预扣则无动作；已有预扣退回 |
| 上游失败或客户端断开，尚未提交业务 payload | 不结算 | 不因估算/上游头存在而计费 | 否 | 终止全部尝试后退回 |
| 流式提交业务 payload 后失败/客户端断开 | 按现有部分结算 | 实际已截断则输入 B；否则既有口径；输出取已有部分 usage/估算 | 否 | 按部分账单结算差额，不再整笔退款 |
| A 未提交业务内容而失败，B 成功 | 仅 B 结算一次 | B 的输入基线、真实输出 | B 符合门禁才模拟 | 共享一次会话，补充预扣后最终调差 |
| 完整上游成功，但响应覆盖规则改成客户端 4xx/5xx | 保留原有成功计费语义 | 同成功账单 | 按原始上游成功与 usage 门禁 | 正常结算，不按客户端状态误退 |
| 非流式失败 | 沿现有退款分支 | 不引入非流式部分收费 | 否 | 退回未结算预扣 |
| 完整成功但上游无 usage | 既有估算计费 | 有截断则输入 B；输出沿现有可得估算 | 否 | 正常结算差额 |

既有违规费用是独立业务规则，上表的“不结算模型用量”不取消既有违规费用；本功能生成的本地配置/超限错误不得误标成上游违规。重复 finalizer、重复结算调用均幂等；已资金结算而 token 更新失败时保留现有审计/补偿语义，不再次退款。

## 6. 配置存储、优先级和编辑行为

### 6.1 明确开关含义

每项功能各有两个层次：

- 全站 `force_disabled`：默认 false；true 强制停用该功能，渠道自定义也不生效。
- 全局默认策略：默认 disabled；只影响继承者，渠道可以自行自定义开启。

截断：总停用 > 渠道模型显式 off/custom > 全局模型 off/custom > 关闭。
缓存：总停用 > 渠道 off/custom > 全局 enabled/disabled > 关闭。

不新增渠道全体模型截断开关来增加第三层继承。单条自定义完整覆盖，不逐字段混合继承。模型 ID 精确匹配，单套规则最多 256 条，模型 ID 非空、无首尾空格、长度≤255。相关 JSON 64 KiB 上限与现有配置限制保持一致；以 common JSON 包装函数读写，复用 settings/options 列。

总停用与配置变更在该节点同步完成后，对**尚未开始的新尝试**生效；已发出请求不取消、不改账。跨渠道重试是新尝试，重新取候选快照。多节点沿现有配置同步机制，不承诺全节点瞬时停用；UI 显示“已保存”，不误称所有节点已即时生效。

### 6.2 存储样例

全局采用两个独立 option JSON 值，拟定键为 `context_truncation.policy` 和 `cache_usage_simulation.policy`。每个值整体校验并原子发布不可变快照，避免将同一功能四个字段分别写入产生中间态。以下是两个值展开后的说明样例，`MODEL_ID` 必须替换为实际逻辑模型 ID；窗口仅示例，不代表某个实际模型能力。

```json
{
  "context_truncation.policy": {
    "force_disabled": false,
    "models": {
      "MODEL_ID": {
        "mode": "custom",
        "window_tokens": 200000,
        "threshold_percent": 90,
        "safety_tokens": null,
        "keep_recent_turns": 1,
        "output_reserve_tokens": 8192
      }
    }
  },
  "cache_usage_simulation.policy": {
    "force_disabled": false,
    "enabled": false,
    "creation_trigger_percent": 20,
    "read_trigger_percent": 60,
    "creation_token_percent": 30,
    "read_token_percent": 50
  }
}
```

真正出厂全局 `models` 为空，不自动启用示例模型。渠道 `settings` JSON 内挂两项独立配置：

```json
{
  "context_truncation": {
    "models": {
      "MODEL_ID": { "mode": "off" }
    }
  },
  "cache_usage_simulation": {
    "mode": "custom",
    "creation_trigger_percent": 20,
    "read_trigger_percent": 60,
    "creation_token_percent": 30,
    "read_token_percent": 50
  }
}
```

渠道截断规则缺失或 `mode: inherit` 表示继承，自定义使用与全局 custom 相同参数；缓存块缺失或 `mode: inherit` 表示继承，`mode: off` 显式关闭。全局模型只接受 off/custom。null 仅在明确声明自动语义的字段接受；不得把概率 null 当成合法 0。

### 6.3 表单与更新

- default 全局模型设置页新增两张独立卡片；渠道高级设置新增两个独立编辑组件。
- 四个缓存输入框实际预填 20/60/30/50，非仅 placeholder；整数步进 1，全局无配置时开关关闭。
- 缺字段才补默认，显式 0、已保存值优先；刷新/重启/开关切换不覆盖手动输入。读取旧配置时整项补全后再校验，非法旧组合不自动改数，返回配置错误并停用该项。
- 继承态显示全局值和来源、输入只读；首次自定义复制当前全局值；已有自定义草稿切换后可在当前编辑会话恢复。持久 inherit/off 不保存隐藏的有效 custom；离开未保存页面遵循既有草稿行为，不新增后台草稿系统。
- 全局总停用开启时显示“全站已停用”；渠道仍可编辑草稿，但不能误显示已生效。
- 保存、复制、批量编辑保留无关及未知字段；删除规则即恢复继承，off 才是持续关闭。
- 保存成功回读检查；接口沿用管理员权限，不向普通用户暴露渠道配置、规则详情或密钥。
- 六语言 i18n、default 的表单/组件约定必须遵守；classic 仅数据保留兼容。

## 7. 两项功能各自的支持矩阵

下表是适用规则；实际验证层次与例外见第 13 节。两项均仅覆盖文本/工具对话，不扩张到图像生成、音频实时流等任务。

| 请求/路由 | 截断 | 缓存模拟 |
|---|---|---|
| Chat Completions、Messages 的完整文本/工具历史 | 支持 HTTP 与 SSE | 完整成功且可信总输入、缓存 absent 时支持 |
| 无状态 Responses 完整 input | 支持 HTTP 与 SSE | 同上 |
| Gemini generateContent 完整 contents | 支持 HTTP 与 SSE | 同上 |
| Responses previous_response_id/conversation、Gemini 上游缓存/会话引用 | 跳过：本地历史不完整 | 独立判断；纯文本且可信总输入、完整 presence 才支持，引用缓存本身不可当成 absent 的证明 |
| 纯透传 | 跳过：保留原样透传 | 已采集可信上游 usage/presence 的路径可独立支持 |
| 显式图片（含已支持协议的工具结果图片） | 按完整业务轮次裁剪，保留近期图片及工具依赖，不修改保留的内容块 | 仍跳过多模态，不模拟 |
| 文件、音视频、不透明压缩块或未知内容 | 命中已启用规则时返回本地 400，不请求上游；未启用时保持原行为 | 保持原有适用性判断 |
| 原生、归一化、转换路由 | 上述支持范围逐条验证 | 按上游协议采集，再转换；客户端协议不代表来源 |
| 未适配的 provider 特有请求体 | 跳过并记录适用性；宣称支持但变换无法保障的路径按第 3.3 节报冲突 | unknown，不模拟 |
| compact、embedding、rerank、图片/视频任务、实时音频 | 保持原有行为 | 保持原有行为 |

### 7.1 首期上游解析入口

- `/Users/777java/777/projects/github/new-api/relay/channel/openai/relay-openai.go`：Chat HTTP/SSE；配合同目录 `/Users/777java/777/projects/github/new-api/relay/channel/openai/usage.go` 的非标准用量位置。
- `/Users/777java/777/projects/github/new-api/relay/channel/openai/relay_responses.go`：Responses HTTP/SSE。
- `/Users/777java/777/projects/github/new-api/relay/channel/claude/relay-claude.go`：Messages HTTP/SSE。
- `/Users/777java/777/projects/github/new-api/relay/channel/gemini/relay-gemini.go`：Gemini HTTP/SSE。
- `/Users/777java/777/projects/github/new-api/relay/protocol_route_handler.go`：转换路由原始 body/chunk，在 `decodeProtocolResponse`、`decodeProtocolStreamChunk` 附近复用同一 presence 解析，不从转换后的零判断。

标准字段覆盖 Chat `usage.prompt_tokens_details.cached_tokens/cache_write_tokens/cached_creation_tokens`、Responses `usage.input_tokens_details` 对应字段、Messages `cache_read_input_tokens/cache_creation_input_tokens/cache_creation`、Gemini `usageMetadata.cachedContentTokenCount`，包含各协议 SSE 的真实嵌套位置。

现有 DeepSeek/Zhipu/Moonshot/llama 非标准 `prompt_cache_hit_tokens`、`usage.cached_tokens`、`choices[].usage.cached_tokens`、`timings.cache_n` 等都纳入 existence 检测，显式零也屏蔽模拟；复用现有字段识别，勿照搬只识别正数的 helper 作为 presence 判断。其他独立适配器默认 unknown，逐个接入后才声明支持；复用同一解析器的渠道不再重复一套模拟算法。

## 8. 解耦设计：小模块、窄接线

### 8.1 硬性边界

- **截断不调用缓存模拟，缓存模拟不调用截断。** 二者只接收明确输入及策略，返回结果和审计数据；由现有调用层顺序组合。
- 纯算法不访问 Gin、数据库、Redis、全局设置，不依赖整个 `RelayInfo`。计数采用现有计数器的函数参数，抽样采用已冻结数值参数；不用一个实现也做接口工厂。
- 配置解析不参与扣款；解析器只报告上游事实，不决定价格；日志只序列化结果，不重新计算或重新抽样。
- `RelayInfo` 仅保存两个独立状态指针；请求原文、原始上游 usage、客户端逻辑用量、结算副本明确区分，不相互覆盖。
- 原有 handler 只增加“准备/调用/传递状态”；计费入口只在一个位置组合调整函数。不得为每个 provider 拷贝相同比例算法、裁剪算法或结算逻辑。
- 不为达到独立文件数量而建立通用中间件流水线、策略注册器、插件系统或新的全量协议 IR；按实际复用提取共同轮次算法，各协议只处理内容边界/引用映射。

### 8.2 文件职责

以下为已实现的职责拆分；测试紧邻实现，原有文件仅保留必要接线。

| 路径 | 独立职责 |
|---|---|
| `/Users/777java/777/projects/github/new-api/dto/context_truncation.go` | 截断配置/决策类型、校验 |
| `/Users/777java/777/projects/github/new-api/dto/cache_usage_simulation.go` | 缓存配置、校验和纯比例分配；presence 位于 relay/common |
| `/Users/777java/777/projects/github/new-api/setting/model_setting/context_truncation.go` | 截断全局策略与快照 |
| `/Users/777java/777/projects/github/new-api/setting/model_setting/cache_usage_simulation.go` | 缓存全局策略与快照 |
| `/Users/777java/777/projects/github/new-api/relay/contexttruncate/` | 轮次/依赖算法及四协议边界处理；传入计数函数 |
| `/Users/777java/777/projects/github/new-api/relay/context_truncation.go` | 现有请求流程的薄适配，准备与最终检查，不承载裁剪算法 |
| `/Users/777java/777/projects/github/new-api/relay/common/context_truncation_state.go` | 当前尝试截断事实，独立生命周期 |
| `/Users/777java/777/projects/github/new-api/relay/common/cache_usage_state.go` | 请求级随机值、尝试级配置和上游字段状态 |
| `/Users/777java/777/projects/github/new-api/relay/common/cache_usage_presence.go` | 原始上游字段存在性采集，接收协议和 bytes，不引用 service |
| `/Users/777java/777/projects/github/new-api/service/context_truncation_billing.go` | 截断前计费副本调整，不裁剪请求 |
| `/Users/777java/777/projects/github/new-api/service/cache_usage_simulation.go` | 门禁、纯比例分摊，不直接扣费或读取数据库 |
| `/Users/777java/777/projects/github/new-api/service/input_policy_log.go` | 两项调整的日志附加字段 |
| `/Users/777java/777/projects/github/new-api/web/default/src/features/input-policies/context-policy-editor.tsx` | 截断规则编辑器；全局和渠道复用 |
| `/Users/777java/777/projects/github/new-api/web/default/src/features/input-policies/cache-policy-editor.tsx` | 缓存数字输入；全局和渠道复用 |

编辑器仅通过 value/onChange/来源展示参数工作，不直接请求保存 API；两个入口沿用各自现有表单提交。全局外壳不反向耦合整个渠道 drawer。

### 8.3 结算接入和重试状态

```text
原始上游 usage + 原始 presence
→ 现有响应覆盖规则 / 原始用量判定
→ effectiveBillingUsage 提取真实/既有派生计费口径
→ 原有真实缓存亲和性观测
→ 结算副本：截断计费调整（如实际发生）
→ 结算副本：缓存模拟（独立门禁）
→ 普通倍率与表达式使用同一份调整结果
→ 原有 BillingSession 结算与日志
```

不要调整后再次从原始 `BillingUsage` 还原覆盖结果，也不要把模拟结果写回上游对象。无原始 usage 但已有本地估算/B 基线时，结算副本与表达式一致处理；不因 originUsage=nil 跳过应执行的表达式，再单独保留“上游无 usage”审计事实。

重试开始重置：候选策略、presence、裁剪计数、变换结果及候选审计；保留：原始请求、请求抽样值、资金会话、计费模型/价格快照。最终失败日志可记录历次候选摘要，不保存多份正文。

### 8.4 默认关闭的成本

快速返回只检查快照开关/规则及请求类型。两项关闭时不新增全文计数、深拷贝、随机抽样、额外 JSON 解析、响应缓冲或外部请求。仅缓存开启时不构建裁剪轮次图；仅截断开启时不采集缓存模拟专用元数据。已有流程的解析/复制不在本任务无关重构。

## 9. 日志显示合同

普通用户不展示截断标记或“截断前估算计费”说明，仍可查看计费输入、输出、普通/创建/读取分项和金额；模拟缓存说明维持原行为。普通日志接口读取时过滤历史及新日志的 context_truncated 与 admin_info，不重写数据库历史。实际截断后的客户端输入是逻辑上下文估算，不是上游实际处理量。

管理员在 `other.admin_info` 下查看独立 `context_truncation`、`cache_usage_simulation` 记录：E_before、E_after、U、B、reported（客户端逻辑输入投影）、client_usage_reason、删除轮数、规则来源、匹配模型、候选渠道、实际创建/读取数、触发结果、字段来源、跳过/冲突原因。渠道 ID、策略细节不加入普通用户可见摘要。

用量来源分别表示 upstream、既有 derived、local_estimate、simulated；不能把模拟日志写成真实命中，也不能把普通字段缺失全标为估算失败。空/零保留其区别。

展示示例：截断前 10000，发送估算 6000，上游 5800，最终计费输入 10000；仅模拟读取触发时普通 5000、读取 5000、创建 0。用户摘要仅保留正常计费量、金额及原有“缓存读取为模拟”说明；管理员另见截断前后、上游、回传投影及计费数字和规则来源。客户端输入回传 10000，真实上游输入仍记录为 5800，模拟缓存不进入回传。

失败而没有消费日志时，将本地裁剪失败/跳过摘要附加既有错误日志。不得为这些审计字段保存被删除正文、原始密钥或完整请求副本；既有可选对话日志不在本任务扩大采集范围。

## 10. 编码验收表

下列保留原始验收合同；已执行的测试分层、实际覆盖和未实测环境见第 13 节，不将合同表等同于每项全链路测试。固定计数器/随机样本构造边界；Go 使用 testify require/assert，真实 HTTP/SSE mock 上游同时核对发送体、客户端响应、账单和日志。

| 编号 | 输入/前提 | 精确预期 |
|---|---|---|
| T01 | window=200000，threshold=90，自动 S，R=8192 | S=4000，L=180000；输入≤180000不裁剪 |
| T02 | window=200000，R=30000，自动 S | L=166000；不是简单按窗口90%放行 |
| T03 | 无可确认输出上限、未填预留 | 发送前 output_reserve_required，0 次上游调用 |
| T04 | 超预算，有旧轮次和跨轮工具依赖 | 删除最早可删完整组，当前轮与依赖仍完整，无孤儿调用/结果 |
| T05 | 只剩保护内容仍超预算 | context_length_exceeded，未发送、不产生模型消费 |
| T06 | CountToken=false，规则开启 | 当前请求有效计数，全局开关不变 |
| T07 | A 裁剪后失败，B 成功 | B 从完整原文准备；一次成功结算，无 A 残留状态 |
| T08 | fallback 目标超窗且有规则 / 无规则 | 前者进入裁剪验证；后者保留原拒绝 |
| T09 | 后置内容覆盖冲突 | override_conflict，0 次该候选上游调用，预扣按表处理 |
| C01 | 默认20/60，随机整数分别(10,80)/(30,10)/(10,10)/(30,80)，范围0–99 | 依次仅创建/仅读取/同时/均无；比较使用 sample<percent |
| C02 | B=10000，默认30/50，两项触发 | N=2000，C=3000，R=5000，总量10000 |
| C03 | 缓存缺失/显式0/一项正值/null/unknown | 仅第一个且成功有可信输入时可能模拟 |
| C04 | SSE末帧出现真实缓存字段 | 不模拟；前面无字段不能提前得出结论 |
| C05 | 真实缓存先存在，转换器补字段/反推缓存已有值 | 保留真实/既有派生结果，不重复叠加 |
| B01 | E_before=10000，E_after=6000，U=5800，有截断 | B=10000，原始上游usage不变 |
| B02 | 规则开启但未裁剪，U=5800 | 沿现有计费输入5800，不强制用原始本地估算 |
| B03 | B=10000，真实读取3000、创建2000 | 普通5000，不模拟，真实缓存不变 |
| B04 | B=1000，真实读取1500、创建500 | 输入最低2000、普通0，estimate_conflict审计 |
| B05 | SSE仅头/ping后失败 / 业务payload后失败 | 前者退款，后者部分结算；两者均不模拟 |
| B06 | 完整成功且缺上游usage，有截断和可用输出估算 | 输入B与输出估算进入统一计费，表达式不被nil origin绕过；不模拟 |
| B07 | cr/cc在表达式使用/未使用，len跨档 | 对应现有语义归一化，不重复扣分类；len保持B |
| B08 | 非信任路径预扣不足；重复finalizer | 不发送；只执行一次资金结算/退款 |
| B09 | 真实5m/1h创建、免费/按次/工具附加费、饱和 | 不重复合计，原有合同不变，无负扣款，checked审计保留 |
| P01 | 默认关闭、规则缺失 | 旧行为不变，无新增全文计数/复制/响应缓冲 |
| P02 | 全局默认关、渠道custom / force_disabled=true | 前者渠道可生效；后者任何新尝试都不生效 |
| P03 | 保存概率0、刷新/重启；缺字段 | 0原样恢复；只有缺字段补默认值 |
| P04 | 概率100+100，token比例>100，非法窗口/JSON | 整项拒绝，不发布半套快照 |
| P05 | 继承→首次custom、编辑已有custom、复制/批量编辑 | 分别复制全局/保留手动值；无关未知字段保留 |
| P06 | 进行中请求改配置、随后新尝试 | 已发送者用旧快照；新尝试用该节点最新快照 |
| P07 | 模型mapped/fallback | 用当前逻辑模型匹配策略，计费模型不漂移 |
| U01 | 每种承诺协议HTTP/SSE | 模拟字段不进客户端usage/上游请求，账单及日志对应同一结算副本 |
| U02 | default编辑、六语言、classic保存其他字段 | 表单与i18n正确，新配置不丢失 |

数据库设计要求兼容 SQLite/MySQL/PostgreSQL；本轮实际运行 SQLite 配置与账单测试，未启动 MySQL/PostgreSQL 实例；新功能不加表/列。每个协议以真实转发行为而非仅 HTTP 200 判定通过。

## 11. 恢复、开发顺序与编码准备结论

### 11.1 恢复方式

两项分别通过 force_disabled 停止后续尝试，不取消已发送请求或重写账单。代码回退只涉及本功能文件及接线；保留配置备份，验证旧版本不会在编辑时抹除 JSON。已发生的消费/退款不随配置回退倒推重算，不回滚共享钱包或数据库。

### 11.2 开发顺序

1. 配置类型/整体校验/默认值、两个独立状态、配置读写及关闭行为测试。
2. 纯裁剪算法和四协议边界测试；先 Chat/Messages，再无状态 Responses/Gemini。
3. 原始缓存presence与独立比例函数，原生/转换路由逐条验证。
4. 请求准备/预扣接线和唯一结算调整点；完成失败、断流、表达式及重试账单验证。
5. default全局卡片/渠道编辑器、日志展示、i18n，classic只处理字段保留兼容。
6. 运行新增验收表和原有回归、真实HTTP/SSE E2E；按实测更新支持矩阵。

不并行创建子代理，不提交/推送/切换分支；部署是独立任务。实施时补具体协议接线位置与测试，不用一次性搬运 kiro.rs 或创建所有空文件占位。

### 11.3 编码前核查记录（历史）

- 当前 HEAD：`71bab08133f80fc5aefd84d7e62841ab1cccfebc`，与第一轮不同；本轮重新检查失败结算、预扣 Reserve、原始响应解析入口和前端测试配置。
- `BillingSession.Reserve` 已存在，且保留 trusted 旁路；`FinalizeTextBilling` 已有 settle/partial/refund 和幂等判定，二者可复用。
- 四种上游解析与转换入口已定位；nonstandard缓存字段和“派生缓存不再模拟”列入实现合同。
- 当前工具链：Go 1.26.0（go.mod声明1.25.1），Bun 1.3.4；default node_modules存在。
- 本轮仅更新方案，运行以下既有回归；这些结果证明相关基线可执行，不代表新功能、全量测试或生产E2E完成。

执行目录 `/Users/777java/777/projects/github/new-api`：

```sh
rtk proxy go test ./dto ./service ./controller ./service/relayconvert -run "Test(.*BillingUsage|CalculateTextQuotaSummary|TextBillingFinalization|FinalizeTextBilling|PrepareContextFallback|TryTieredSettle|ConvertResponse)" -count=1
```

输出（退出码0）：

```text
ok  github.com/QuantumNous/new-api/dto                  0.015s
ok  github.com/QuantumNous/new-api/service              0.039s
ok  github.com/QuantumNous/new-api/controller           0.028s
ok  github.com/QuantumNous/new-api/service/relayconvert  0.014s
```

执行目录 `/Users/777java/777/projects/github/new-api/web/default`：

```sh
rtk proxy bun test src/features/channels/lib/model-context-fallback.test.ts
```

结果（退出码0）：`4 pass / 0 fail / 17 expect() calls`。

**准备结论：可以开始编码，没有新的业务决策阻塞。** 请求变换一致性、上游字段presence传播、部分流结算是开发重点，已明确处理规则和验收场景；完成基线不等于跳过这三项验证。本轮止于文档定稿和准备分析，没有实现业务代码。

## 12. 历史取舍与确认记录（非实施选项）

- 第1轮：对比 kiro.rs，确定它是前缀计量模拟，不是概率模拟；列出备选。
- 第2轮（历史决定，响应合同已由第14节替代）：用户选择发送前截断、完整原始估算计费且当时不改客户端usage、创建读取可独立同时触发、缓存不按模型、先default。
- 第3轮：用户要求可编辑默认概率；采用创建20%、读取60%，token占比30%/50%，全部可改，功能默认关闭。
- 第4轮：用户确认完善建议并强调尽可能解耦；本版收口截断默认值、总停用语义、独立支持矩阵、执行顺序、失败计费、日志合同与编码准入。
- 未采用：报错后再截断、裁当前问题/工具结果、摘要模型、前缀状态缓存、互斥随机、截断后输入计费、按缺失项混补真实/模拟缓存。
- 上游用量加回删除估算、先截断再fallback、固定客户端原始模型匹配等均为历史备选；现行客户端逻辑输入取截断前估算与有效原始总输入的较大值，计费公式不随回传投影变化。

## 13. 实施结果与本地验收记录（2026-09-08）

### 13.1 实际实现

实施基线 HEAD：`7840a83888bfa7dea3f8c02d33660ffa90c3a024`。本节取代历史“尚未编码”的状态描述；未进行 Git 提交、推送或部署。

- 配置：全局 `context_truncation.policy`、`cache_usage_simulation.policy`；渠道保存在既有 `settings` JSON 下的 `context_truncation` 和 `cache_usage_simulation`。整项先校验后写入，配置快照原子替换；全局非法存量配置按该功能全站停用处理。
- 截断：`relay/contexttruncate` 只管理完整轮次与工具依赖；`relay/context_truncation.go` 负责按尝试复制、模型匹配、现有计数器、预扣及最终发送体复检。后置参数覆盖内容或转换后再次超预算时，本地拒绝且不请求该候选上游。
- 缓存：`dto/cache_usage_simulation.go` 的纯函数接收冻结样本；`relay/common/cache_usage_presence.go` 单独采集原始字段。HTTP 在现有响应语义入口观察，SSE 在共享 scanner 的转换前观察，不在每个 provider 重写概率算法。
- 计费：`service/context_truncation_billing.go` 和 `service/cache_usage_simulation.go` 独立工作，由既有结算入口依次组合；复用 BillingSession 的预扣、部分结算、退款及幂等终结。真实缓存亲和性观测先于模拟。客户端 usage 的现行补全合同见第14节，模拟缓存仍不写入响应。
- 前端：`web/default/src/features/input-policies/` 包含共享编辑器、配置 schema、全局/渠道薄外壳及表单测试；全局入口为“模型与路由 → 输入处理策略”，渠道入口为编辑抽屉“高级设置”。概率 20%/60%、分配比例 30%/50% 均为可编辑默认值。
- 渠道继承值只读，首次自定义复制全局；当前编辑会话保存自定义草稿，切换继承/关闭不持久化隐藏自定义参数。全站紧急停用时显示具体停用功能。
- 日志追加计费来源和管理员审计信息，不保存删除的正文。default 六种源语言已补齐，繁体中文由现有同步脚本生成。

标准 OpenAI、Anthropic、Gemini 适配器及其兼容渠道中的四种文本协议进入本期截断支持范围。其他 provider 特有请求变换若开启截断而未适配，会在本地返回 `context_truncation_unsupported_conversion`，不带着未经验证的请求发出。透传、媒体、不透明历史、上游会话引用按第 7 节独立判断跳过；不存在“所有 40+ provider 已完整验收”的结论。

### 13.2 业务验证分层

证据目录：`/Users/777java/777/projects/github/new-api/artifacts/input-policy-implementation`。

| 层次 | 实测范围与结果 | 证据 |
|---|---|---|
| 修改前基线 | dto/service/controller/relay/relayconvert/model_setting 全部通过 | BASELINE.log |
| 后端功能合同 | 79 个测试/子场景通过；覆盖预算、完整工具轮次、模型 fallback、逐尝试复制、原始 presence、显式零/异常值、部分流、表达式、预扣和幂等 | FEATURE_TESTS.jsonl |
| 转换请求矩阵 | 4 种客户端协议 × 4 种上游协议 × HTTP/SSE，共 32 条真实本地 HTTP 转发路径通过；检查发送体截断、客户端 usage 和原始字段采集 | HTTP_MATRIX.log、relay/input_policy_http_test.go |
| 完整应用业务 E2E | 4 种协议 × HTTP/SSE × 缓存缺失/显式零/真实缓存，共 24 条通过；使用真实本地路由、鉴权、渠道选择、SQLite 钱包/令牌/消费日志 | APPLICATION_E2E_FINAL.log、RUN_E2E.sh、business_e2e.py |
| 原有后端回归 | go test ./... 全部通过 | ALL_GO_FINAL.log |
| 并发检测 | dto/model_setting/contexttruncate/relaycommon/relay/service/relayconvert/controller 的 -race 回归通过 | RACE_FINAL.log |
| default 前端 | 72 项测试通过；typecheck、功能目录 lint、生产构建及入口资源校验通过 | FRONTEND_FINAL.log |
| 浏览器操作 | 全局编辑/保存/刷新、非法 100+100 禁止保存、显式 0、渠道高级设置继承与自定义回读通过；补测草稿模式切换 | UI_GLOBAL.txt、UI_GLOBAL.png、UI_CHANNEL.txt |
| classic | 未添加新编辑器；核查现有 settings 合并保留未知字段，生产构建通过 | CLASSIC_BUILD.log |

以下为初版历史验收（当时客户端 usage 不补全，当前期望由第14节替代）。完整应用测试不仅检查 HTTP 200。例如 Chat SSE 输入从 3 条截成 1 条，上游报告输入 100，客户端仍收到 100；账单采用截断前 4343。测试固定创建概率 0%、读取概率 100%、读取比例 50%，在缓存缺失时读缓存 2171、扣款 2409；显式零时不模拟、扣款 4363；真实读取 40 时保留 40、扣款 4327。每项金额均同时核对钱包差额、令牌差额和消费日志。0%/100% 只用于可重复的测试配置，出厂默认仍为 20%/60%。

缓存创建及双触发由固定样本合同测试验证，不用随机请求“碰概率”作断言。完整 24 条应用 E2E 采用仅读取触发；32 条转换矩阵验证协议转发，资金闭环由原生四协议 E2E 与独立 SQLite 结算测试承担，不把它们混称为 32 条跨协议钱包 E2E。

### 13.3 测试发现的问题与最小修复

1. Responses 的真实 `input_tokens_details.cached_tokens` 在恢复计费副本时丢失：仅在 Responses 计费恢复入口同步到既有 `PromptTokensDetails`，增加真实缓存回归测试。
2. 原生 Claude SSE 的末尾 `message_delta` 常常只有输出计数，原流程覆盖后丢失开头的输入/缓存：新增独立 `service/relayconvert/native_claude_usage.go`，复用现有累积器合并稀疏 usage，该稀疏合并本身不修改原始客户端帧、不重复缓冲正文；现行最终用量补全在独立模块执行。
3. 原有任务轮询测试在 GORM 写对象时并发读取该对象的任务 ID，race 检测报错：测试断言改为不可变 fixture ID，生产轮询代码保持不变。
4. 渠道缓存模式切换会覆盖当前自定义草稿：编辑会话内暂存草稿，继承/关闭只提交 mode；恢复自定义回填手动值。继承数字禁用编辑并显示全局值。

表达式维持既有协议语义：Claude 的普通输入 `p` 不含缓存，不因本次功能重定义历史表达式；`len` 保持完整计费输入。真实 5m/1h 创建分类、按次/免费和原有饱和防护沿用已有结算体系。

### 13.4 可重复执行与回退

执行根目录：`/Users/777java/777/projects/github/new-api`。

```sh
rtk proxy go test ./... -count=1
rtk proxy go test -race ./dto ./setting/model_setting ./relay/contexttruncate ./relay/common ./relay ./service ./service/relayconvert ./controller -count=1
rtk proxy bash "artifacts/input-policy-implementation/RUN_E2E.sh"
rtk proxy bash -c 'cd "web/default" && bun test "src/features/input-policies" "src/features/channels/lib" && bun run typecheck && bun run build'
```

RUN_E2E.sh 使用临时目录、随机空闲本地端口、独立 SQLite 与本地 mock 上游；自动初始化测试账户、检查并补建缺失前端嵌入资源，结束后关闭其进程并删除临时数据库。不连接生产模型、数据库或 Redis。

`MODIFIED_FILE.tar.gz` 保存本次修改后的代码/方案副本；`DIFF_FILE.patch` 保存相对实施前内容的差异；`VERIFICATION.txt` 记录精确命令、结果及哈希；`ROLLBACK.sh` 仅按清单恢复本次文件，执行前校验当前文件哈希，避免覆盖后续编辑。回退演练在另一份副本进行，当前工作区保留实现。已发生的账单不随代码回退重算。

### 13.5 验证边界与下一步

- 本地业务资金闭环已验证 SQLite；MySQL/PostgreSQL 本轮未启动真实实例。代码复用现有 GORM、options/settings 列且无 DDL；跨数据库实测仍应列入发布前验证。
- default 页面完成浏览器操作验证；classic 仅构建和字段合并兼容检查，未宣称完整 classic 浏览器回归。
- 现有默认关闭路径由全量回归与静态入口检查覆盖，未给出性能压测或生产收益数字。
- 本次没有提交、推送或上线，也没有修改生产策略。上线前使用实际模型窗口配置，并在目标 provider/计费表达式组合上做小流量验收。

## 13. 图片上下文最小修复（2026-09-08）

### 13.1 行为与解耦
- 截断使用独立的图片适用性判断；原有纯文本 Shape 判断继续用于缓存模拟和上下文 fallback，避免改变二者行为。
- 复用现有整轮裁剪、工具依赖保护、输出预留与安全余量公式；只有完整保护内容满足预算才发送。
- 截断专用计数补充 Claude 工具结果图片、Gemini 文件引用图片和函数结果图片；不改全局计数器。
- 原始图片仍按协议原样保留；不引入压图、OCR、摘要模型或上游失败后再次截断。
- 图片转换回归发现 Gemini → Chat → Claude 链式转换丢失类型化媒体缓存，局部修复 Claude 转换器的媒体复制，避免发出空内容；不修改重试或渠道选择。
- 本地截断错误保留 400 和具体原因，不被默认最终错误覆盖成 502；仅豁免已启用截断且无真实上游状态的本地截断错误，不改变上游错误分类、重试或禁用规则。
- 不支持的内容使用本地 context_truncation_unsupported_content 错误并记录具体原因。透传与 provider_state_reference 保留原有旁路边界。
- 未命中、显式关闭和紧急关闭同样记录管理员审计：enabled=false、source、model、reason；已启用时记录预算及已完成的前后估算。未进入计数的分支为 0，不能解读成实际零 token。
- rule_not_matched 表示该逻辑模型未命中；rule_disabled 表示规则关闭，source 区分全局、渠道及紧急停止。审计不包含正文、图片或密钥。

### 13.2 验收与发布边界
- 本地验证图片整轮删除、近期图片/工具依赖保留、短请求不变、保护内容超限和不支持内容的发送前失败。
- 覆盖全局继承、渠道覆盖、逻辑模型映射、四种协议 HTTP/SSE 及实际 legacy Chat → Claude 链路。
- 按截断前估算计费、唯一消费、失败退款保持原合同；图片即使上游缺少缓存字段也不启用缓存模拟。
- 本次不改上游错误分类、Key 自动禁用、重试及渠道切换，不恢复生产 Key，不执行发布。
- 剩余风险：本地图片计数只是估算；上游若仍返回 401，现有自动禁用机制仍可能触发。历史故障请求是否命中当时的规则仍以历史证据为准，新增诊断不反向补造历史结论。

## 14. 恢复客户端自动压缩（2026-09-08，替代旧响应与普通账单展示约定）

### 14.1 根因与合同

网关只删除本次尝试副本中的旧轮次，客户端历史并未缩短。若回传截断后的上游输入量，依赖 usage 的客户端会持续低估历史长度。线上案例 E_before=275003、E_after=34690（本地估算）、U=114482、B=275003；本次回传逻辑输入恢复为 275003，不把本地估算描述为上游精确计数。

- 原始上游：原对象及 BillingUsage 快照用于真实缓存识别、计费基础和排障。
- 客户端：仅 Applied=true、有效成功 usage 时补全输入。逻辑总输入 C=max(E_before, 有效原始输入总量, 真实缓存总量下界)。
- 结算：沿用 B=max(E_before, 真实缓存创建+读取)，未实际截断仍按原有用量结算。C 不反向进入计费；当原始输入比 E_before 更大时，C 与 B 可以不同。
- 客户端完成摘要后，下一请求重新估算 E_before，不保存跨请求历史最大值。不增加摘要模型或压缩服务费；客户端摘要请求作为普通请求计费。
- 不更改窗口、预算、配置结构、错误分类、自动禁用、重试或渠道选择。

### 14.2 最小代码结构

- 独立模块：relay/common/client_context_usage.go，仅处理响应字段与尝试级稀疏计数，不访问数据库、计费模块或 provider。
- 薄接入：controller/context_fallback_response.go 复用现有 HTTP/SSE 最终 writer、分帧、模型名称回写与尝试重置；不新增第二套响应缓冲器。
- Chat/Responses 补全协议输入字段及已有输入别名；已有总量仅增加输入差额，输出、推理和真实缓存细项不变。Gemini 同样仅调整 promptTokenCount 和总量中的输入贡献，不重复累计 thoughtsTokenCount。
- Claude 普通 input_tokens=C−真实缓存创建−读取。message_start 与含输入/缓存的 message_delta 使用绝对值；只有输出的稀疏 delta 不重复写入输入。缓存分项总计与 TTL 拆分取现有总量保护口径。
- 不新增缺失的整个 usage、输出计数或总量字段；不强制恢复客户端关闭的 usage。无效/负数/分数/越界计数不改写。
- 管理员审计新增 reported 和 client_usage_reason。reported 表示成功响应进入最终 writer 的逻辑用量投影，不作为网络送达确认；usage_missing / input_usage_missing / invalid_usage 等解释未补全原因，不记录正文或图片。
- 回归中发现独立的既有稀疏用量遗漏：现代 Claude → 其他协议 SSE 的末尾输出-only delta 覆盖了起始输入/缓存 BillingUsage。最小修复复用已有 Claude 累积器，并只补全转换副本中的稀疏 delta；原始帧不改动，计费公式、模拟概率和缓存分配算法不变。

### 14.3 账单可见性

model/log.go 的普通日志读取过滤 context_truncated，连同现有 admin_info 过滤覆盖历史记录。default UI 同时以管理员条件显示截断说明及完整审计。普通用户计费量、金额和缓存说明保持正常；不删除或重写原始日志。

### 14.4 验证与边界

- 本地 HTTP 网关矩阵覆盖 4 客户端 × 4 上游 × HTTP/SSE × 缓存 absent/显式零/真实值。Claude ↔ Gemini 属于现有 discouraged 路由，公共选路应保持拒绝、上游零调用及零收费；不为测试放宽生产选路。底层 64 路由（含图片）测试另行验证转换器。
- 按原始上游、客户端回传、最终账单、钱包/令牌差额和唯一消费日志分别断言，覆盖图片、模型映射、旧 Chat → Claude 流式链路、本地保护拒绝与上游失败退款。
- 运行当前 ZCode.app 3.11.2 随附运行时，在独立 HOME、合成历史与本地 mock 上游中验证自动摘要及继续对话；用户现有客户端配置与会话保持不变。结果以本地验收记录为准，不能仅凭回传 JSON 判定真实客户端自动压缩通过。
- 补全不保证客户端在首次截断前压缩，也不会恢复已删除的历史；摘要请求仍受预算及内容适用性约束。图片 token 仍是估算；通过本地预算不等于上游保证接受。
- 本阶段只做本地实施和验证，生产发布另行执行。验收记录：artifacts/client-context-usage/VERIFICATION.txt。


### 14.5 本轮已验证结果

- ZCode 3.11.2 内置运行时（CLI 0.16.5）使用同一隔离会话连续四轮，未修改运行时、用户客户端配置或生产服务。原版本对照没有自动摘要（4 次模型请求）；修复版观察到 compact.auto.started / compact.auto.completed，第三轮先自动摘要再继续回答，第四轮仅一次普通请求。
- 修复版第二轮回传逻辑输入约 239218（mock 上游原始输入 100）；第三轮完成摘要后使用新的较短请求，后续输入估算约 95343，不再沿用历史高值。摘要请求依旧受网关截断预算约束。本地 mock 只验证触发、协议与结算行为，不验证真实摘要模型的语义保真或供应商计数。
- 公共路由矩阵 84 次成功转发 + 12 次既有 discouraged 路由拒绝均符合预期，另有图片 8、映射旧链路 2、保护/文件/音频/失败退款 4 个场景通过。普通/管理员日志 API、压缩后新请求计费和非法配置原子拒绝均通过。
- 原始上游、客户端逻辑用量和结算副本隔离通过测试；真实缓存、模拟缓存、倍率/表达式、唯一扣费、部分结算与失败退款通过相关回归。详细命令、环境、退出码及源码回滚结果见本轮验收记录。
- 全项目 Go 检查只统计第一方包，排除 artifacts 内的历史测试文件与 web/node_modules 内附带的 Go 示例；保留首次直接 go test ./... 因这些非工程包编译失败的原始输出，不把过滤后通过写成原命令通过。

# 多密钥故障隔离与恢复

## 决策和选路

多密钥健康决策只读取真实上游 HTTP 状态及保留的结构化错误，不使用最终公共错误映射或单密钥旧关键词兜底。渠道覆盖配置优先于全局配置；持久规则优先于临时规则。默认 401 持久禁用凭据，429 临时隔离；普通 403、模型不存在和参数错误不因泛化关键词禁用凭据。单密钥仍使用原策略。

有限的提供商规则包括每日模型额度（限定消息格式）、`INFERENCE_CAP_ERROR`、`insufficient_quota`、套餐权益耗尽和工作区额度耗尽。明确模型额度按实际映射后的上游模型隔离；套餐与工作区额度按整把密钥隔离，不推断其他密钥的账户归属。同一上游模型的别名共享隔离，其他模型继续选路。

首次转发复用中间件选钥后的完整渠道快照，不额外轮换。真实失败密钥在当前请求中排除，即使重试预算为零也记录健康状态；有预算时优先同渠道其他密钥。跨渠道选择仍受原分组、协议、模型、指定渠道、任务锁定及亲和约束。已提交响应不重放，计费及退款路径不变。

持久状态写入在事务中读取当前渠道，按密钥内容重新定位，并同时更新渠道状态与能力表；MySQL/PostgreSQL 使用行锁，SQLite 使用进程内互斥。删除、替换或人工禁用后的旧请求不覆盖新身份或人工状态。Redis 隔离使用密钥 SHA-256 而非索引，不存明文凭据。

## 冷却与半开探测

- 有效 `Retry-After` 支持秒数与 HTTP 日期；与提供商明确恢复时间取较晚值，接受未来七天内的提示。异常提示回退并记录。
- 无有效提示时以配置分钟数为基值，连续失败指数退避并添加 0～10% 正向抖动，上限 24 小时；并发失败不缩短已有恢复时间。
- 冷却到期后由下一条符合条件的真实请求探测，不创建后台付费任务。Redis 租约每项 60 秒、每 20 秒续期，请求结束或取消释放，进程退出依赖过期。
- 成功只清除该探测拥有的租约及相同状态版本；旧成功不清除较新的失败。失败记录保留至恢复时间后 24 小时。
- Redis 读写失败限频告警；请求内排除仍有效，跨请求隔离和分布式探测降级为可用性优先。

## 接口兼容与管理

原整数状态与统计仅表示整把密钥；模型隔离不增加整钥不可用计数。`get_key_status` 增加可选 `cooldowns[]`，包含 `scope`、`model`、`category`、`disabled_until`、`state`、`source`；旧客户端可以忽略这些字段。`pending_probe` 表示等待下一条真实请求验证，并非已经恢复。

整钥冷却沿用原 Redis 键及字段；模型冷却使用独立后缀。管理列表可扫描指定密钥的状态，转发热路径只读取该密钥与请求模型的状态，不扫描全库。

原启用操作清除该密钥全部冷却。`clear_model_cooldown` 需要 `channel_id`、`key_index`、实际上游 `model`，沿用 `channel.sensitive_write` 权限；两套前端均要求确认。此操作仅清除指定模型，不清除整钥凭据错误或其他模型隔离。

## 回归与发布

关键测试：`TestFirstAttemptRetainsMultiKeyMetadata`、`TestMultiKeyRelayHTTP`、`TestMultiKeyPolicyScopesAndRecovery`、`TestMultiKeyModelIsolationAndHalfOpen`、`TestMultiKeyProbeLeaseLifecycle`、`TestMultiKeyCooldownBackoff`、`TestMultiKeyDatabaseCompatibility`。首轮测试在旧提交上应失败，在修复后通过。HTTP 测试使用仅监听本机的模拟上游和隔离数据库/Redis。

运行 `go test ./...` 和 `go test -race ./service ./controller ./model -run 'MultiKey|FirstAttempt|KeyCooldown' -count=1`。外部数据库测试只接受隔离实例，通过 `MULTIKEY_TEST_DRIVER` 与 `MULTIKEY_TEST_DSN` 指定 MySQL 或 PostgreSQL；常规测试不得连接生产数据库。

发布遵循 [本地构建蓝绿流程](dev-blue-green-deployment-plan.md)。不进行数据库迁移、历史密钥批量恢复或候选共享状态故障注入。生产切流及停止旧槽位分别确认；观察至少 600 秒，没有自然故障样本时必须注明未覆盖对应生产场景。

回滚只恢复旧应用、地址和稳定别名，不恢复共享数据库/Redis。旧版本忽略模型级冷却，回滚恢复的是旧版行为，而不是新隔离能力。旧镜像和受限备份保留，禁止全局 Docker prune。

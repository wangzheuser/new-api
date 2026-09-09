# US 服务器本地构建蓝绿部署方案

## 1. 权威约定

本文是 `dev` 分支在 US 服务器的唯一应用发布流程：**从已推送的精确提交在本机构建
`linux/amd64` 镜像，通过 SSH Manager 上传不可变制品，再由仓库内固定脚本完成备份、
候选门禁、切流、观察和回滚。**

- 不使用 GitHub Actions 或 GHCR 产物，不依赖浮动 Tag。
- 只替换应用容器；PostgreSQL、Redis、日志库及其卷保持共享且不重建。
- Nginx Proxy 不 reload；切流只交接 Docker 网络地址和历史稳定别名 `new-api-green`。
- 生产切流、停止旧槽位和生产回滚分别需要明确确认。
- 服务器地址、域名、账号、密码、Token、Cookie 和 DSN 只保存在受限配置中。

仓库实现：

```text
deploy/blue-green/build-local.sh         本地构建和不可变制品
deploy/blue-green/release-remote.sh       服务器发布状态机
deploy/blue-green/protocol-stability-gate.sh 最终业务结果门禁
deploy/blue-green/docker-compose.slot.yml 应用槽位模板
Dockerfile                               runtime-local 镜像目标
```

## 2. 术语和不变量

- **物理槽位**：`blue`、`green`。
- **逻辑角色**：`production`、`candidate`、`standby`；不得根据颜色猜角色。
- **稳定代理别名**：`new-api-green`，名称是历史约定，不表示物理 Green 永远承载生产。
- **source-production-clean-dist**：切流前线上版本最初构建的干净前端资源。
- **target-clean-dist**：本次构建且尚未合并历史资源的前端资源。
- **target-runtime-dist**：target-clean-dist 加上一代 source-production-clean-dist，最终嵌入二进制。

两个槽位必须共享 PostgreSQL、Redis、`SESSION_SECRET`、业务配置和应用网络；必须使用
不同容器名、端口、日志目录、数据目录和 `NODE_NAME`。候选端口只绑定 `127.0.0.1`。
槽位容器统一使用 `unless-stopped` 重启策略，确保生产容器异常退出时自动恢复，同时让已经
完成观察并由发布脚本主动停止的旧槽位在 Docker daemon 重启后仍保持停止。

槽位 Compose 只能加入应用网络，禁止声明 Nginx Proxy 网络。代理网络仅由
`release-remote.sh cutover|rollback` 管理。发布流程禁止执行：

```text
docker compose down
docker compose down -v
docker volume rm
docker system prune --volumes
```

## 3. 发布状态机

```text
NEW
 ├─> BUILT --------┐
 └─> BACKED_UP ----┤
                   v
                UPLOADED
                   v
                 STAGED
                   v
                  GATED
                   v
                CUTOVER
                   v
              OBSERVING_10M
                   v
                OBSERVED
                   v
                FINALIZED
```

`BUILT` 与 `BACKED_UP` 可以并行。target-clean-dist 只服务下一次发布，可在镜像上传后
异步上传，但必须在 `FINALIZED` 前完成校验。每个远端子命令都必须可重复执行；已有产物
只有在实际 SHA、版本和运行状态复核通过后才能快速返回。

## 4. 一次性服务器配置

每个发布目录包含由部署端生成、权限为 `600` 的 `release.env`，以及服务器预置、权限为
`600` 的 `server.env`。`release.env` 不含凭据；`server.env` 只保存路径和受限运行配置
引用，不复制运行环境内容。

`server.env` 必须提供：

```text
BACKUP_ROOT APP_NETWORK PROXY_NETWORK PROXY_ALIAS PROXY_CONTAINER PUBLIC_STATUS_URL
POSTGRES_CONTAINER POSTGRES_USER POSTGRES_DB REDIS_CONTAINER NGINX_ACCESS_LOG
BLUE_PORT BLUE_DATA_DIR BLUE_LOG_DIR BLUE_NODE_NAME BLUE_PROJECT BLUE_RUNTIME_ENV_FILE
GREEN_PORT GREEN_DATA_DIR GREEN_LOG_DIR GREEN_NODE_NAME GREEN_PROJECT GREEN_RUNTIME_ENV_FILE
```

蓝绿槽位复用同一份 `docker-compose.slot.yml`，通过上述变量区分。运行配置、Compose
模板和发布脚本不得包含明文凭据。

## 5. 本地不可变构建

### 5.1 预检

```bash
deploy/blue-green/build-local.sh self-check
```

目标提交必须已经推送到 `origin/dev`。脚本使用 `git archive`，工作区未提交文件不会进入
镜像。工具链版本、镜像 SHA 和构建日志写入发布制品，不在脚本中静默切换工具链。

### 5.2 准备上一生产版本资源

上一生产版本必须提供 Default 和 Classic 的 clean-dist 目录或 `.tar.zst`。读取顺序：

1. 本地按完整提交 SHA 缓存且 SHA 正确；
2. 服务器发布制品存在且 SHA 正确，下载到本地缓存；
3. 最后才重新构建上一生产提交。

不得把已经合并历史资源的 runtime-dist 当作 clean-dist，避免旧资源逐代累积。

### 5.3 构建

```bash
deploy/blue-green/build-local.sh prepare \
  --commit <完整提交SHA> \
  --previous-default <上一生产版本Default clean-dist目录或归档> \
  --previous-classic <上一生产版本Classic clean-dist目录或归档>
```

脚本固定执行：

1. Default 类型检查和构建、Classic 测试和构建；
2. 在合并旧资源前归档两套 target-clean-dist；
3. 合并一代 source-production-clean-dist；
4. 验证入口资源、精确版本和 chunk reload 标记；
5. 执行资源合并回归测试和 `go test ./...`；
6. 使用固定 Go 工具链交叉编译：

   ```text
   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOEXPERIMENT=greenteagc
   ```

7. 通过 Dockerfile 的 `runtime-local` 目标组装镜像，验证 OCI revision 和 `linux/amd64`；
8. 启动临时 SQLite 容器，验证精确版本和首页；
9. 使用 `docker save | zstd -T0 -3` 生成 `.tar.zst`，执行 `zstd -t` 和 SHA-256；
10. 写入不含凭据的 `release.env`。

同一提交的完整制品和 SHA 均正确时直接复用；只有显式 `--force` 才重新构建。

## 6. 上传与备份

只通过 SSH Manager 上传：

- `release.env`；
- 镜像 `.tar.zst`；
- `release-remote.sh`、`protocol-stability-gate.sh`、`low-traffic-evidence.py`（必须同提交、同目录上传）；
- `docker-compose.slot.yml`；
- 两套 target-clean-dist 归档。

远端文件权限设为 `600`，脚本设为 `700`。镜像是候选启动的关键路径；target-clean-dist
可以稍后上传。

脚本上传后先执行：

```bash
./release-remote.sh self-check
./release-remote.sh status
```

数据库备份可以与本地构建并行，但候选启动前必须通过：

```bash
./release-remote.sh backup
```

备份包括 PostgreSQL custom-format dump、SHA-256、`pg_restore -l`、脱敏运行快照、容器、
镜像、应用网络、代理网络和 Nginx 配置。数据库 dump 保留 `public.logs` 和
`public.conversation_logs` 的表结构、索引及约束，但通过 `--exclude-table-data` 排除这两张
高容量日志表的行数据；恢复后这两张表为空，其余表的结构和数据正常恢复。排除清单随备份
保存并与 restore list 交叉校验，清单变化时不得复用同一 release 的旧备份。新备份校验成功
前不得删除旧备份；发布过程保留已有受限备份，不自动清理其他 release 的恢复资产。备份保留期清理由独立确认的维护任务处理。

## 7. 候选槽位与门禁

```bash
./release-remote.sh stage
./release-remote.sh gate
./release-remote.sh cutover --dry-run
./release-remote.sh rollback --dry-run
```

`stage` 自动识别 production，重建另一物理槽位。候选必须只加入应用网络，并满足：

- 精确镜像 revision、版本和 `linux/amd64`；
- `running/healthy`、零重启、未 OOM；
- 独立端口、目录和 `NODE_NAME`；
- PostgreSQL、Redis 和共享运行配置可用；
- 当前入口资源可访问；
- 缺失 `/static/` 资源返回 `404` 和 `Cache-Control: no-store`；
- 去除 `nginx -T` 成功诊断行后，Nginx 配置 SHA 与备份一致；
- 最近日志没有 panic、fatal、OOM 或连接失败。

全量旧资源使用本地文件和 SHA 校验，不通过 HTTP 批量请求，避免触发
`GlobalWebRateLimit`。HTTP 只验证入口资源和少量代表性旧资源。

此外使用真实浏览器访问候选首页、登录页和至少一个懒加载深层路由，确认没有
`pageerror`、错误级控制台日志、静态资源失败或 ErrorBoundary 页面。提交级 Go、前端、
订阅、日志等业务回归在本地自动化测试中完成，不在生产候选重复运行。

## 8. 切流

取得明确确认后：

```bash
CONFIRM_CUTOVER=<release-id> ./release-remote.sh cutover --execute
```

脚本使用 `flock`、动态生产 IP、角色状态文件、有界 IPAM 重试和错误恢复。正常切流和
回滚复用相同网络函数。若目标版本已经持有稳定代理别名，脚本复核状态后返回
`already-complete`，不会重复网络交接。

切流后必须验证：

- 候选持有原生产 IP 和稳定别名；
- 旧槽位保持运行但断开代理网络，避免物理槽位名与稳定别名同名时出现 Docker DNS 轮询；
- Nginx 内部和公网均返回目标版本；
- Nginx 配置哈希未变；
- 真实公网浏览器门禁通过。

网络地址交接会中断仍连接旧容器的长连接或流式请求，应在低峰执行。

## 9. 观察与完成

切流成功后，旧槽位继续运行，进入不少于十分钟的观察窗口：

```bash
./release-remote.sh observe --seconds 600 --interval 30
```

脚本以单调递进的截止时间控制观察窗口，在切流后立即检查，并每 30 秒检查健康、重启、
OOM、Nginx 内部版本、有限公网版本和配置哈希；到达 600 秒后再执行最后一次检查，避免以
“检查次数 × 间隔”代替真实持续时间。观察状态和实际耗时写入固定结果文件。任何检查失败
都会把观察结果标记为失败并保留旧槽位运行。实际 access log 的样本数和 5xx 应记录在
发布结果中；共享日志没有 `$host` 时，非零 5xx 只能视为无法归因，不能推断为本应用错误。

观察保留原始全量 HTTP 5xx 指标，并调用现有 `protocol-stability-gate.sh`。
仅经独立证据确认非本项目导致的上游503，可从回归比较中排除；不得凭503状态、
“上游不可用”文本或某个渠道名称自动豁免。审查记录写入本次发布的
`state/verified-upstream-503.json`，每项含 `request_id` 和非空 `evidence`：
evidence 应引用对应上游 trace、原始记录位置和排除本项目原因的核验结论，不含凭据。
脚本还要求该请求在实际观察窗口内、最终数据库记录为失败、收到真实上游HTTP503，
且未提交业务负载、未部分结算；缺少证据或本地错误仍阻断。已确认外部错误单独计数，
不转成成功，不用于补足业务样本。旧日志没有传输状态证明时不自动豁免。

业务门禁以两个槽位的访问日志 request_id 确定窗口，再读取对应的尝试和最终记录，
避免数据库写入时间与访问日志边界不一致造成漏算。额外等待300秒结算，不计入600秒
健康观察时长。选路前明确拒绝单独记录，不标记为待结算，不改变客户端模型名称或返回。
本次主动探测的 request_id 必须逐条写入 `state/synthetic-request-ids.txt`，不用于稀释自然业务。

业务比较保留 Chat、Responses、Messages、Gemini 协议、流式标志、首个渠道及历史模型维度；
同一请求只归属一个窗口和首个触达渠道，其最终结果可来自重试后的其他渠道。
仅非中间最终记录参与结果统计；部分流失败消费不是成功，旧版正常 EOF 不强求终止事件。
取消、429、转换错误、流式错误和原始错误是否留存分别记录；仅有原始错误文本不等于
已证实上游归因。应用日志中的显式取消不充当消费成功，无取消证据的 HTTP 200 缺少最终
记录则阻断。未结算记录和观察期本地转换错误继续阻断。
任一窗口缺少历史上游模型时，仅将对应协议、流式、渠道、客户端模型的两个窗口对称
合并为 legacy_client_model；保留原始明细，不猜测历史映射，也不只比较有模型名的成功记录。
模型记录完整且两侧样本均不少于10的分组继续独立比较，阈值仍为2个百分点。
协议/流式原始汇总始终展示；仅两个窗口的渠道/模型请求权重完全一致时参与门禁。
权重变化时标记 diagnostic/traffic_mix_changed，避免辛普森悖论；仍逐个比较足量分组，
不把消失渠道视为成功。若没有足量可比分组，继续返回 inconclusive。
候选未结算及本地转换错误优先处理，不因流量权重变化而豁免。
缺少自然流量的协议标记 not_observed，稀疏分组标记 insufficient_samples，而不是业务失败；
两者均不宣称生产覆盖。整个窗口没有任何足量可比分组仍阻断；已足量分组回归仍阻断，
不得用其他模型改善抵消。全协议兼容能力由本地四协议HTTP矩阵覆盖，生产另行验证真实
Chat/Responses流式与非流式业务；主动探测不混入自然流量统计。
未映射的正常消费复用既有 model_name 契约；不根据当前渠道配置推算历史错误的上游模型。

修订门禁的本地回归使用专用 PostgreSQL 容器（禁止指向业务数据库）：

```bash
GATE_TEST_POSTGRES=<隔离测试容器> python3 -m unittest discover -s deploy/blue-green/tests -v
```

只有当前 release、当前生产容器和当前版本存在成功结果，且请求观察时间与实际耗时均不少
于 600 秒，`finalize` 才允许继续。取得停止旧槽位确认后：

```bash
CONFIRM_FINALIZE=<release-id> ./release-remote.sh finalize --execute
```

`finalize` 首先把旧槽位重启策略校正为 `unless-stopped`，再停止旧槽位容器，立即释放其
CPU 和内存占用，并确保 Docker daemon 重启后不会意外拉起；容器元数据、可写层和旧镜像
继续保留用于快速回滚。停止后连续验证新槽位健康、零重启、未 OOM、Nginx 内部版本和
公网版本。验证通过后，`finalize` 保留两个槽位镜像，不执行全局 image/builder prune。
部署执行端按本次 manifest 精确清理上传归档、本地镜像标签和专用 Builder，保留生产镜像、
回滚镜像与受限备份；不得清理其他服务的 dangling 镜像或共享构建缓存。

## 10. 回滚

### 观察证据分级（2026-09-08）

协议比较退出码为 `0=passed`、`1=failed`、`3=inconclusive`。
样本充分且观察下降未超过既有 2 个百分点时沿用通过规则；超过阈值时，使用
Bonferroni 校正的双侧 Wilson 比例区间，仅当前窗口上界低于基线下界
且差值仍超过阈值，才认定有统计支持的下降；否则待判定而非通过。
具体比较为 `pre_lower - post_upper > 0.02`，同时控制所有分组及两个比例区间。
此计算假设请求近似独立，不构成代码因果证明；不得将缺少显著性当作等价性证明。
仅基线记录不完整归为待判定，候选缺失结算或本地转换错误仍失败。
覆盖不足继续展示，整个窗口无可比分组归为待判定；`finalize` 仍只接受 passed。

发布端上传同提交的 `observe-bounded.sh`，执行 `bash ./observe-bounded.sh`。
执行端对退出码 1 或其他错误立即执行已授权回滚；对退出码 3 记录证据不足回退。
包装脚本默认只执行预定的一轮 600 秒观察，证据不足返回 3，不自动重复等待。
仅当执行前设置 `ADDITIONAL_DIAGNOSTIC_OBSERVATION=1` 时，才追加一次
600 秒诊断观察及结算等待，保留原结果，不覆盖首次记录、不循环重试直到通过。
第二次观察仍需独立标注；它用于补充诊断而不是重复显著性检验获取放行机会。
首次待判定的发布不得仅凭第二窗口碰巧通过而 finalize：未获得独立对照证据时，
到期回退旧版并记录 `evidence_inconclusive`，不称为新版代码故障。
确定性业务回归、健康、版本、账务和整体 HTTP 门禁保持原有强度。


取得明确确认后：

```bash
CONFIRM_ROLLBACK=<release-id> ./release-remote.sh rollback --execute
```

回滚读取切流时的角色状态文件，不根据颜色猜测。旧槽位已停止时自动启动并等待健康，
再恢复原生产 IP 和稳定别名。PostgreSQL、Redis 和 Nginx 配置始终不回滚。

## 11. 发布记录与恢复

每次发布保留：提交 SHA、版本、工具链、镜像 ID、OCI revision、各归档 SHA、备份目录、
阶段结果、角色/IP 状态、浏览器门禁、观察区间、旧槽位状态和回滚命令。所有记录脱敏。

失败后先执行 `status`，从最近已验证阶段继续。禁止重新执行已经验证的数据库备份、镜像
导入或生产切流。发布脚本、Compose 模板、`release.env`、阶段结果和 SHA 都作为本次制品
保留，服务器终端不得临时拼接另一套发布逻辑。

### 低流量交付证据（显式启用，2026-09-08）

没有生产样本不等于代码故障，也不等于成功率已验证。默认仍保持 strict/inconclusive。
本次发布需要低流量交付时，可仅在该 release 的 `server.env` 设置
`ALLOW_LOW_TRAFFIC_RELEASE=1`，不修改应用运行配置。必须提前选定观察时长，不能循环检验直到通过。

只有协议门禁明确 `no_comparable_traffic`，且不存在分组成功率下降待判定、未结算、
本地转换错误、候选自然业务失败或任何观察期 HTTP 5xx，才校验交付证据：
Chat/Responses各流式与非流式四项真实探测均有非空输出、完成标记、唯一成功消费记录；
流式状态ok且settled。每项request_id与切流后当前候选容器的同路径HTTP200访问记录对应。
缺失文件、旧槽位探测、重复请求、部分结算、错误或覆盖不全均不接受。

通过后记录 `observation=passed evidence_mode=verified_low_traffic`，原业务统计仍保留
inconclusive/not_observed，不能宣称自然流量成功率无回归。健康、版本、账务、授权、浏览器、
至少十分钟真实观察和回滚资产要求不变；没有基线时不允许任何HTTP5xx。

执行端上传浏览器完成标记时先写 `public-browser.exit.pending`，成功后在服务器同目录
原子rename为`public-browser.exit`；读取端只接受非空完整标记，避免SFTP创建空文件的竞争。

### 固定门禁、专项验收与 AI 审查（2026-09-08）

采用三层判断，不引入额外模型服务：固定门禁保护健康、版本、协议和结算；
每次发布根据 diff 选择专项验收；发布执行者依据证据做 AI 归因并保存审查记录。
使用 [发布审查模板](release-ai-review.md)，AI 不得把固定门禁失败改为通过。

鉴权模块只增加固定原因诊断，不改权限条件、客户端响应、错误分类或重试。
HTTP 原始 403 仍记录；只有同 request_id 的应用固定原因日志与实际 GIN 403 对应，
且本次发布事先审查该原因，才可在低流量交付中作为预期拒绝，不再一律误判成业务故障。
正常授权请求仍必须成功；权限相关变更还需旧版/候选相同条件对照，不能用拒绝标记自证正确。

本次 release 的 `server.env` 可设置 `EXPECTED_POLICY_REJECTION_REASONS`，逗号分隔，
默认空、不排除任何拒绝。固定可审查原因：`auth_user_disabled`、`auth_ip_not_allowed`、
`auth_group_not_allowed`、`auth_group_retired`、`auth_channel_override_denied`。
只允许经对照验证且记录证据的原因；禁止按整个状态码或模糊日志文案豁免。
未知原因、缺失/重复 request_id、数量不一致、未知 403、401/429/5xx、转换错误、
失败或缺失结算继续阻断；已有独立上游故障审查规则不扩大。
原始计数与统计覆盖不足保留，交付通过不等于自然流量统计无回归。
拒绝诊断不采集对话正文、图片、令牌或 IP。所有设置只作用于该次发布的验收，不改业务配置。


### 已审查上游 503 的 SSE 传输边界（2026-09-09）

已确认独立上游故障的逐请求审查，同样适用于 HTTP 已提交为200、但尚未向客户端输出业务负载的SSE失败。
必须同时存在固定窗口内的最终失败记录、admin_info.upstream_status_code=503、stream_status.status=error、
app_http_committed=true，并保持转换错误、已提交业务负载、部分结算、缺失传输证据继续阻断。
外部原因仍需独立对照证据和非空审查记录；不能凭状态码、模型名称或错误关键词自动豁免。

这类请求保留在 verified-upstream-503.tsv，新增 http_status 列区分HTTP503和SSE内503，
仅从回归归因中排除，不算业务成功、不补足样本。HTTP原始200/503数量保持不变；
verified-upstream-counts.txt只统计真实HTTP503，避免从零HTTP5xx中错误减去SSE失败数。
低流量交付其他条件、事前固定观察窗口、退款与真实错误阻断不变。应用错误分类和重试规则不变。

# US 服务器本地构建蓝绿部署方案

## 1. 权威约定

本文是 `dev` 分支在 US 服务器的唯一应用发布流程。以后发布固定采用：**本地从干净提交构建 `linux/amd64` 镜像 → 导出压缩归档并校验 SHA-256 → 通过 SSH Manager 上传 → 启动候选槽位 → 真实端到端验证 → 交接 Docker 网络静态地址**。

- 不使用 GitHub Actions 构建额度，也不依赖远端浮动 Tag。
- Nginx Proxy 配置文件保持原样，不执行 reload 或重启；切流只交接其已解析的应用静态 IP 和 `new-api-green` 网络别名。
- 只替换应用容器。PostgreSQL、Redis、日志库及其卷保持独立和共享。
- 文档不记录服务器地址、域名、账号、密码、Token、Cookie、DSN 等凭据；凭据只来自本机备忘和服务器受限配置。
- 生产切换、停止旧槽位、生产回滚均须取得明确确认。

## 2. 不变量

两个槽位必须共享相同的 PostgreSQL、Redis、`SESSION_SECRET`、业务配置、回调域名和应用网络；必须使用不同的容器名、`NODE_NAME`、本机端口、日志目录和 `/data` 目录。候选端口只绑定 `127.0.0.1`。

应用网络与 Nginx Proxy 网络是两个独立概念。候选槽位启动和重建时只能加入应用网络，
不得在 Compose 服务的 `networks` 中声明 Nginx Proxy 网络；生产/待机静态地址只允许由
切换脚本通过 `docker network connect` 管理。候选门禁必须断言其 Nginx Proxy 网络
地址为空，两个槽位的 `NODE_NAME` 必须不同。

应用 Compose 只描述应用服务，严禁借发布重建数据库或 Redis。以下命令不属于发布流程：

```text
docker compose down
docker compose down -v
docker volume rm
docker system prune --volumes
```

## 3. 本地不可变构建

1. 确认目标提交已推送到远端 `dev`，记录完整 SHA。
2. 执行全仓 Go 测试、Default/Classic 前端构建、类型检查和相关回归测试。
   两套前端使用独立的干净依赖目录；Classic 固定执行
   `bun install --filter ./classic --frozen-lockfile`，不得复用 Default 的
   `node_modules`。
3. 使用 `git archive <commit>` 导出干净源码，避免把工作区未提交文件带入镜像。
4. 发布制品必须同时保存当前生产版本的 `web/default/dist` 和
   `web/classic/dist`，作为下一次发布的兼容资源；两个压缩包及其 SHA-256
   必须随镜像归档上传并保留。
5. 在归档源码中构建 Default 和 Classic 前端，并把版本写为：

```text
dev-<12位提交SHA>-local-amd64
```

6. 两套前端构建完成后分别执行以下命令，把上一生产版本的哈希静态资源合并到
   新 `dist`；命令只补充缺失文件，不覆盖新版本文件，也不复制旧
   `index.html`。上一版本目录缺少 `index.html`、没有静态资源或误指向当前
   `dist` 时命令必须失败：

```text
cd web/default
bun run assets:merge-previous -- <上一生产版本default-dist绝对路径>

cd ../classic
bun run assets:merge-previous -- <上一生产版本classic-dist绝对路径>
```

7. `bun run build` 已内置恢复门禁；合并完成后仍须对两套前端再次验证入口
   资源存在、主包包含本次精确版本号及 `newapi:chunk-reload:` 恢复标记；
   任一项不满足即终止发布：

```text
cd web/default
bun run assets:verify-recovery -- <本次精确版本号>

cd ../classic
bun run assets:verify-recovery -- <本次精确版本号>
```

8. 在 arm64 Mac 上使用固定 Go 工具链交叉编译：

```text
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOEXPERIMENT=greenteagc
```

9. 使用仓库 Dockerfile 相同的固定 Debian runtime digest 打包；镜像必须通过 `docker image inspect` 验证为 `linux/amd64`。
10. 用临时 SQLite 容器验证 `/api/status` 返回精确版本，并验证首页可访问。
11. 解析上一生产版本主包的异步分块映射，逐个请求候选槽位，所有旧哈希分块
    必须返回 `200` 且为 JavaScript/CSS；缺失资源的 `404` 必须携带
    `Cache-Control: no-store`，避免边缘节点缓存发布窗口中的瞬时缺失。
    批量请求必须限速，或直接在构建产物中逐文件验证，避免触发
    `GlobalWebRateLimit` 后把 `429` 误判为资源缺失。
12. 执行 `docker save | gzip`，生成 `.tar.gz` 和 SHA-256 文件；`gzip -t`、本地 SHA-256 均须通过。

构建物、元数据和验证脚本保存在本地部署制品目录，文件名必须包含短提交 SHA。

## 4. 上传、备份与候选槽位

1. 只通过 SSH Manager 上传镜像归档到服务器受限目录，权限设为 `600`。
2. 服务器再次执行 SHA-256 和 `gzip -t`，再用 `gzip -dc | docker load` 导入。
3. 发布前创建权限为 `700` 的独立备份目录，至少包含：
   - PostgreSQL custom-format 在线备份、SHA-256 和 `pg_restore -l` 校验；
   - 当前应用 Compose、运行环境的脱敏快照、容器和镜像 inspect；
   - Nginx Proxy 配置快照及网络 inspect。
4. 更新闲置槽位 Compose 的不可变本地镜像 Tag，仅执行该应用服务的 `docker compose up -d --force-recreate`。
5. 重建后核对候选槽位只加入应用网络，Nginx Proxy 网络地址为空；如果 Compose
   自动把候选加入代理网络，先修正 Compose 再重建，不得把手工
   `docker network disconnect` 当作长期修复。
6. 核对候选槽位健康、版本、重启次数、OOM、CPU、内存、memory+swap、PID 上限和
   独立 `NODE_NAME`。数据库、Redis 不参与重建。

## 5. 切流前真实门禁

候选槽位通过独立本机端口完成：

- `/api/status` 精确版本、首页及所有引用静态资源；
- Root/管理员 API、普通用户 API；
- 普通用户日志始终只返回每个请求的最终记录；
- 管理员默认只返回最终记录，显式关闭时返回完整重试过程；
- 真实路由重试，最终日志内容必须与客户端最终响应完全一致；
- 订阅更新成功路径及非法额度事务回滚；
- PostgreSQL、Redis、缓存、登录状态和管理页面；
- Default 和 Classic 两套主题的分块恢复单元测试、构建恢复标记及旧资源兼容性；
- 使用真实浏览器访问首页、登录页和至少一个懒加载深层路由，确认没有
  `pageerror`、错误级控制台日志或前端 ErrorBoundary 的“500”页面；
- 容器 `healthy`、零重启、未 OOM；
- Nginx Proxy 当前配置 SHA-256 与发布前快照一致。

任一门禁失败即保留现网槽位，不交接流量。

旧槽位版本尚不识别 `phase: final_error` 时，不得在切流前把该阶段写入渠道
`param_override`：旧版本会把它当作请求阶段执行。回滚窗口内优先只配置系统级
`general_setting.default_final_error_override`；渠道级最终错误规则应在旧版本退出
回滚窗口后启用，或在回滚脚本中同时恢复发布前的渠道参数覆盖快照。

## 6. 不改 Nginx 的静态 IP 交接

Nginx 已把 `new-api-green:3000` 解析为生产应用静态地址。切流时动态读取现网槽位 IP，严禁在脚本中硬编码：

1. 使用 `flock` 获取本次发布的独占锁，避免两次切换或回滚并发操作同一网络。
2. 记录现网槽位生产 IP、候选待机 IP、两个容器、两个版本和当前网络 inspect；
   同时写入权限为 `600` 的状态文件，后续回滚只读取该文件，不根据颜色猜测角色。
3. 断言候选槽位尚未加入 Nginx Proxy 网络，生产 IP 和待机 IP 均不硬编码。
4. 从 Nginx 网络断开候选槽位。
5. 从 Nginx 网络断开现网槽位。
6. 使用有界重试把候选槽位以现网 IP、`new-api-green` 别名接回 Nginx 网络；Docker
   IPAM 在断开后可能短暂保留地址，单次 `docker network connect` 失败不能直接判定
   发布失败。
7. 使用同样的有界重试把旧槽位接到候选待机 IP；观察窗口内保留待机地址可以提前
   验证旧槽位并缩短回滚时间。
8. 从明确配置的 Nginx 容器内请求
   `http://new-api-green:3000/api/status`，再从公网域名请求 `/api/status`，两者必须返回新版本。
   不得用“容器名包含 nginx 的第一个容器”推断代理容器。
9. 核对候选持有生产 IP、旧槽位持有待机 IP、两个地址唯一且 Nginx 配置 SHA-256
   未变化。

切换脚本必须设置失败陷阱：任何验证失败时，断开两个槽位，把原生产 IP 和别名交还
旧槽位，并把候选恢复到待机地址或隔离状态。正常路径和失败恢复路径必须共用同一个
带重试的网络连接函数。脚本先经 `bash -n` 和只读 dry-run 验证，并作为制品留存；
日志必须记录失败行和失败命令，禁止只有非零退出码而没有步骤信息。

该方案不修改或 reload Nginx，但 Docker 网络地址交接会中断当时仍连接旧容器的长连接或流式请求；应在低峰执行，并把交接窗口控制在数秒内。

## 7. 切流后验证与停止旧槽位

切流后立即通过真实公网入口重复第 5 节端到端场景。随后至少观察十分钟，定时核对：

- 公网版本和 Nginx 容器内上游版本始终为新版本；
- 候选槽位持续 `running/healthy`、零重启、未 OOM；
- CPU、内存、数据库连接、Redis、5xx 和延迟无异常；
- Nginx Proxy 配置哈希保持不变。

5xx 统计必须读取 Nginx 配置中实际启用的 `access_log`，同时记录统计区间和总请求数。
只有 `500=0` 而总样本数也是 `0` 时，门禁结果无效；`docker logs` 没有访问记录时必须
改查持久化访问日志。HTTP 500 统计还不能覆盖前端 ErrorBoundary：真实浏览器门禁需要
额外检查 DOM、`pageerror`、控制台错误以及静态资源失败。

共享 Nginx 同时承载多个域名时，访问日志必须包含 `$host`，或为该应用配置独立
`access_log`；否则出现非零 5xx 时无法归因到本应用。存量共享日志尚未包含域名时，
“全局 500 为零”只能用于排除 Nginx 层 500，不能用其他状态码分布推断本应用质量。

源站错误契约和 CDN 公网行为分开验证。源站直连门禁负责断言 502/503/504 的精确响应体、
请求 ID 和日志内容；公网门禁负责页面、资源、版本和可用性。CDN 可能接管 504 并替换
响应体或响应头，不应把 CDN 的预期改写误判成应用版本回归，也不应因此跳过源站契约测试。
观察账号具备 CDN Analytics Read 权限时还应记录边缘 5xx；缺少该权限时明确记录覆盖
缺口，不能用源站日志推断所有边缘节点都没有 CDN 自身生成的错误。

观察通过并已取得停止确认后，使用带超时的 `docker stop` 停止旧槽位。旧容器、旧镜像、Compose、运行配置和数据库备份至少保留 24 小时，不执行删除或 prune。停止后再次验证公网版本和新槽位健康状态。
停止前必须先验证回滚脚本可以自动启动已停止的旧槽位；停止后至少连续执行五次公网和
Nginx 内部版本检查。

## 8. 回滚

回滚只处理应用网络，不回滚共享数据库：

1. 启动旧槽位并确认其本机健康。
2. 记录当前生产 IP。
3. 使用与切流相同的独占锁、网络断开函数和有界连接重试。
4. 从 Nginx 网络断开新旧槽位。
5. 将旧槽位以生产 IP 和 `new-api-green` 别名接回。
6. 使用有界重试验证 Nginx 容器内上游与公网入口均恢复旧版本，避免 CDN 短暂传播
   延迟造成单次误判。
7. 将新槽位接回待机地址并保留用于分析。

回滚脚本不得假设旧槽位仍在运行：如果旧槽位已停止，必须先启动并等待健康。回滚失败
陷阱也必须使用带重试的恢复函数，确保新版本重新取得生产 IP，而不是在 IPAM 竞争时
留下两个均未接入生产地址的槽位。

回滚窗口内不启用只受新版本支持、且会写入旧版本不兼容数据的功能。Nginx Proxy 配置、PostgreSQL 和 Redis 始终保持原样。

## 9. 发布记录

每次发布记录：提交 SHA、版本、镜像 ID、归档 SHA-256、备份目录、候选门禁结果、
切换时间、角色/IP 状态文件、观察区间、访问日志总样本数及 5xx 分布、浏览器运行时
门禁、旧槽位停止时间和回滚命令。记录中只写脱敏标识，不写任何凭据。

发布完成前必须存在并验证以下制品：

- 可重复执行的候选门禁脚本；
- 带独占锁、失败陷阱和网络连接重试的切流脚本；
- 能自动启动旧槽位的回滚脚本；
- 读取真实访问日志且校验样本数的观察脚本；
- 最终结果文件及所有脚本的 SHA-256。

同一发布中的 dry-run、正式切流、观察和回滚必须复用这些制品，禁止在服务器终端临时
拼接另一套逻辑。每次脚本失败后先记录失败步骤，再从已确认状态继续，不能重复执行已经
完成的数据库备份、镜像导入或生产切换。

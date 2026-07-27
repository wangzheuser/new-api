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
3. 使用 `git archive <commit>` 导出干净源码，避免把工作区未提交文件带入镜像。
4. 在归档源码中构建 Default 和 Classic 前端，并把版本写为：

```text
dev-<12位提交SHA>-local-amd64
```

5. 在 arm64 Mac 上使用固定 Go 工具链交叉编译：

```text
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOEXPERIMENT=greenteagc
```

6. 使用仓库 Dockerfile 相同的固定 Debian runtime digest 打包；镜像必须通过 `docker image inspect` 验证为 `linux/amd64`。
7. 用临时 SQLite 容器验证 `/api/status` 返回精确版本，并验证首页可访问。
8. 执行 `docker save | gzip`，生成 `.tar.gz` 和 SHA-256 文件；`gzip -t`、本地 SHA-256 均须通过。

构建物、元数据和验证脚本保存在本地部署制品目录，文件名必须包含短提交 SHA。

## 4. 上传、备份与候选槽位

1. 只通过 SSH Manager 上传镜像归档到服务器受限目录，权限设为 `600`。
2. 服务器再次执行 SHA-256 和 `gzip -t`，再用 `gzip -dc | docker load` 导入。
3. 发布前创建权限为 `700` 的独立备份目录，至少包含：
   - PostgreSQL custom-format 在线备份、SHA-256 和 `pg_restore -l` 校验；
   - 当前应用 Compose、运行环境的脱敏快照、容器和镜像 inspect；
   - Nginx Proxy 配置快照及网络 inspect。
4. 更新闲置槽位 Compose 的不可变本地镜像 Tag，仅执行该应用服务的 `docker compose up -d --force-recreate`。
5. 核对候选槽位健康、版本、重启次数、OOM、CPU、内存、memory+swap 和 PID 上限。数据库、Redis 不参与重建。

## 5. 切流前真实门禁

候选槽位通过独立本机端口完成：

- `/api/status` 精确版本、首页及所有引用静态资源；
- Root/管理员 API、普通用户 API；
- 普通用户日志始终只返回每个请求的最终记录；
- 管理员默认只返回最终记录，显式关闭时返回完整重试过程；
- 真实路由重试，最终日志内容必须与客户端最终响应完全一致；
- 订阅更新成功路径及非法额度事务回滚；
- PostgreSQL、Redis、缓存、登录状态和管理页面；
- 容器 `healthy`、零重启、未 OOM；
- Nginx Proxy 当前配置 SHA-256 与发布前快照一致。

任一门禁失败即保留现网槽位，不交接流量。

旧槽位版本尚不识别 `phase: final_error` 时，不得在切流前把该阶段写入渠道
`param_override`：旧版本会把它当作请求阶段执行。回滚窗口内优先只配置系统级
`general_setting.default_final_error_override`；渠道级最终错误规则应在旧版本退出
回滚窗口后启用，或在回滚脚本中同时恢复发布前的渠道参数覆盖快照。

## 6. 不改 Nginx 的静态 IP 交接

Nginx 已把 `new-api-green:3000` 解析为生产应用静态地址。切流时动态读取现网槽位 IP，严禁在脚本中硬编码：

1. 记录现网槽位生产 IP和候选槽位原 IP。
2. 从 Nginx 网络断开候选槽位。
3. 从 Nginx 网络断开现网槽位。
4. 将候选槽位以现网 IP、`new-api-green` 别名接回 Nginx 网络。
5. 从 Nginx 容器内请求 `http://new-api-green:3000/api/status`，再从公网域名请求 `/api/status`，两者必须返回新版本。
6. 核对候选持有生产 IP、旧槽位已脱离 Nginx 网络、配置 SHA-256 未变化。

切换脚本必须设置失败陷阱：任何验证失败时，断开候选槽位，把原生产 IP 和别名交还旧槽位，并把候选接回其待机地址。脚本先经 `bash -n` 验证并作为制品留存。

该方案不修改或 reload Nginx，但 Docker 网络地址交接会中断当时仍连接旧容器的长连接或流式请求；应在低峰执行，并把交接窗口控制在数秒内。

## 7. 切流后验证与停止旧槽位

切流后立即通过真实公网入口重复第 5 节端到端场景。随后至少观察十分钟，定时核对：

- 公网版本和 Nginx 容器内上游版本始终为新版本；
- 候选槽位持续 `running/healthy`、零重启、未 OOM；
- CPU、内存、数据库连接、Redis、5xx 和延迟无异常；
- Nginx Proxy 配置哈希保持不变。

观察通过并已取得停止确认后，使用带超时的 `docker stop` 停止旧槽位。旧容器、旧镜像、Compose、运行配置和数据库备份至少保留 24 小时，不执行删除或 prune。停止后再次验证公网版本和新槽位健康状态。

## 8. 回滚

回滚只处理应用网络，不回滚共享数据库：

1. 启动旧槽位并确认其本机健康。
2. 记录当前生产 IP。
3. 从 Nginx 网络断开新槽位。
4. 将旧槽位以生产 IP 和 `new-api-green` 别名接回。
5. 验证 Nginx 容器内上游与公网入口均恢复旧版本。
6. 将新槽位接回待机地址并保留用于分析。

回滚窗口内不启用只受新版本支持、且会写入旧版本不兼容数据的功能。Nginx Proxy 配置、PostgreSQL 和 Redis 始终保持原样。

## 9. 发布记录

每次发布记录：提交 SHA、版本、镜像 ID、归档 SHA-256、备份目录、候选门禁结果、切换时间、观察结果、旧槽位停止时间和回滚命令。记录中只写脱敏标识，不写任何凭据。

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
- `release-remote.sh`；
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
镜像、应用网络、代理网络和 Nginx 配置。新备份校验成功前不得删除旧备份；校验成功后按
当前策略只保留最近一份。备份清理不进入切流关键路径。

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
- Nginx 配置 SHA 与备份一致；
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

默认观察十分钟：

```bash
./release-remote.sh observe --seconds 600 --interval 30
```

脚本定时检查健康、重启、OOM、Nginx 内部版本、有限公网版本和配置哈希，并写入固定结果
文件。实际 access log 的样本数和 5xx 应记录在发布结果中；共享日志没有 `$host` 时，
非零 5xx 只能视为无法归因，不能推断为本应用错误。

取得停止旧槽位确认后：

```bash
CONFIRM_FINALIZE=<release-id> ./release-remote.sh finalize --execute
```

旧容器和旧镜像保留用于回滚。停止后连续验证新槽位健康、Nginx 内部版本和公网版本。

## 10. 回滚

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

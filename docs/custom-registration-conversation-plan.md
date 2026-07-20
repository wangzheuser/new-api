# 注册码、对话采集与自定义镜像实施方案

## 1. 目标

在长期跟随官方上游的代码库中，只增加以下能力：

1. 独立注册码及新用户注册限制。
2. 按渠道启用的完整文本对话采集。
3. 从官方镜像平滑切换到当前 `dev` 分支构建的自定义镜像，保留现有数据库和挂载数据。

`main` 始终作为官方上游镜像分支，所有自定义实现只进入 `dev`。不合并 Futureppo 的整套二改代码，只参考其业务语义并按当前代码结构实现。

## 2. 分支与上游同步

- `main`：只允许 fast-forward 到 `upstream/main`，不提交自定义功能。
- `dev`：长期集成、自测和生产部署分支。
- 功能开发从 `dev` 创建短分支，完成后合并回 `dev`。
- `dev` 发布后只使用 `merge main` 同步上游，不重写历史。

推荐同步流程：

```bash
git fetch upstream
git switch main
git merge --ff-only upstream/main
git push origin main
git switch dev
git merge main
```

自定义代码优先放入新文件；对注册、OAuth、Relay、迁移和前端路由等上游热点文件只增加必要接入点。

## 3. 注册码和注册限制

### 3.1 业务规则

- 现有 `aff_code` 继续表示邀请关系，新增 `registration_code` 只负责注册准入，两者不得复用。
- 新增全局配置 `RegistrationCodeRequired`，默认关闭。
- 开启后，密码注册和所有 OAuth 新用户注册必须消费有效注册码。
- 已存在的 OAuth 用户登录、登录后绑定 OAuth 不需要注册码。
- 注册码支持启用/禁用、生效时间、过期时间、最大使用次数和使用记录。
- 注册码校验、用户创建、使用次数增加、使用记录写入必须处于同一数据库事务。

### 3.2 数据模型

主数据库新增：

- `registration_codes`
  - 名称、注册码、状态、最大使用次数、已使用次数
  - 生效时间、过期时间、创建时间、最后使用时间、创建人
- `registration_code_usages`
  - 注册码 ID、新用户 ID、用户名、注册来源、使用时间

所有迁移只新增表，不删除或修改现有用户数据列。并发消费使用带条件的原子 `UPDATE`，通过 `RowsAffected` 判断是否抢占成功，兼容 SQLite、MySQL 和 PostgreSQL。

### 3.3 注册流程

- 密码注册：解析 `registration_code`，在用户创建事务内消费。
- 通用 OAuth：注册码由 `POST /api/oauth/state` 保存到服务端 Session；回调确认需要创建新用户后再消费。
- 保留现有 `GET /api/oauth/state`，避免破坏已有客户端。
- 微信旧入口复用相同 Session 注册码和事务消费逻辑。
- 后端是最终准入边界，不能依赖前端必填校验。

### 3.4 管理能力

Root API 提供：

- 分页、搜索和查看使用记录
- 单个/批量生成
- 更新名称、状态、次数和有效期
- 删除注册码

default 前端增加管理页面；default/classic 注册入口都增加注册码输入，避免切换主题后无法注册。

## 4. 完整对话采集

### 4.1 首版范围

采集文本/JSON Relay：

- OpenAI Chat Completions
- OpenAI Responses
- Claude Messages
- Gemini GenerateContent
- 流式和非流式响应

每条记录包含：客户端请求、转换后的上游请求、上游响应、转换后的客户端响应，以及用户、Token、渠道、模型、请求 ID、状态码、流式标记等元数据。

首版不保存音频、视频、文件和图片 Base64 二进制内容，也不采集 HTTP Header 或渠道密钥。Realtime WebSocket 不在首版范围。

### 4.2 开关和权限

- 新增全局总开关 `ConversationCaptureEnabled`，默认关闭。
- `ChannelOtherSettings` 新增 `conversation_log_enabled`，默认关闭。
- 两个开关同时开启才采集。
- 只有 Root 可以启用、查询、导出或删除对话记录。

### 4.3 采集方式

- 客户端请求复用现有 BodyStorage。
- 在公共上游 HTTP 请求层包装 `req.Body` 和 `resp.Body`，采集实际传输内容，避免逐个修改渠道适配器。
- 包装 Gin ResponseWriter 采集客户端响应，兼容普通 JSON 和 SSE。
- 捕获对象设置硬性大小上限；超过上限保留截断数据、原始字节数和截断标记，禁止无限制内存增长。
- 一个客户端请求只保存最终成功尝试或最终失败尝试，元数据记录重试次数和已尝试渠道。
- 日志写入失败不得影响正常 Relay 请求。

### 4.4 存储与清理

关系型 `LOG_DB` 新增 `conversation_logs` 表，支持 SQLite、MySQL、PostgreSQL。首版检测到 ClickHouse 日志库时禁止开启该功能，不引入未使用的第二套实现。

默认限制：

- 每段 Body 最大 2 MiB
- 每条记录最多四段捕获数据
- 保留 7 天
- 总量上限 5 GiB

增加 Root 查询、详情、删除、JSONL 导出和存储统计 API。清理复用现有 SystemTask 运行机制，优先删除最旧记录。

## 5. Docker 发布和生产迁移

新增 fork 专用 GitHub Actions 工作流，将 `dev` 的精确提交构建到：

```text
ghcr.io/<repository-owner>/new-api:dev-<short-sha>
```

生产只部署不可变标签或镜像 digest，不修改官方发布工作流，也不覆盖 `calciumion/new-api`。

服务器保留原 Compose 目录、project name、数据库服务、环境变量和 Volume，只增加应用镜像覆盖文件：

```yaml
services:
  new-api:
    image: ghcr.io/<repository-owner>/new-api@sha256:<digest>
```

切换时只执行 `up -d --no-deps new-api`，不得执行 `docker compose down -v`，也不得使用会创建 `dev_pg_data` 的 `docker-compose.dev.yml`。

正式迁移顺序：只读盘点、数据库和 `/data` 备份、生产备份恢复演练、构建镜像、只重建应用容器、健康检查。由于本次数据库变化只有新增表和新增配置，回滚到原官方镜像时额外表会被忽略；异常时仍以数据库备份为最终恢复点。

## 6. 验证标准

### 注册码

- 缺失、无效、禁用、未生效、过期、耗尽均拒绝注册。
- 并发注册不会超过最大次数。
- 用户创建失败不消耗注册码。
- 密码、通用 OAuth、自定义 OAuth、微信新用户都不能绕过。
- 已有 OAuth 用户不受影响。

### 对话采集

- 全局或渠道任一开关关闭时不产生记录。
- JSON 与 SSE 都能保存四段内容。
- 超大内容被安全截断并记录原始大小。
- 日志数据库失败不影响 Relay 响应。
- 非 Root 无权读取或管理记录。

### 本地服务

- Go 单元测试通过。
- default/classic 前端构建通过。
- Docker 镜像构建通过。
- 使用本地 PostgreSQL/Redis Compose 启动后，完成状态接口、管理员登录、注册码注册和对话采集端到端验证。

其中 PostgreSQL/Redis 与自定义镜像验证是生产发布前置门禁；开发机可以先用上游默认的 SQLite/内存模式完成业务端到端验证，但不能替代正式迁移前的备份恢复演练。

## 7. 本次落地结果（2026-07-20）

### 7.1 已实现

- 注册码：独立数据表、使用审计、原子消费、Root 管理 API、全局注册限制、default 管理页面、default/classic 注册入口。
- 注册链路：密码注册、内置/自定义 OAuth 新用户、微信新用户均进入同一事务消费逻辑；已有 OAuth 用户登录不消费。
- 对话采集：全局和渠道双开关、四段 Body、单段 2 MiB 截断、最终重试记录、Root 查询/详情/删除/导出、保留期和容量清理任务。
- 数据兼容：迁移仅新增 `registration_codes`、`registration_code_usages`、`conversation_logs` 表；独立 `LOG_SQL_DSN` 和主库兼作日志库两种模式均有迁移入口。
- 发布：新增 `dev` 镜像工作流和 Compose 镜像覆盖示例，不修改官方镜像发布流程。

### 7.2 已完成验证

- `go test ./...`：通过。
- default：`bun run typecheck`、`bun run build` 通过。
- classic：`bun run build` 通过。
- Compose 合并配置：`docker compose ... config --quiet` 通过。
- 本地服务：使用当前源码构建二进制，在 SQLite/内存模式启动于 `http://127.0.0.1:33001`。
- 注册码端到端：缺失注册码被拒绝；有效注册码可创建用户；最大次数为 1 的注册码被消费后再次注册被拒绝；管理页显示 `1 / 1`。
- 对话采集端到端：本地 OpenAI 兼容 Mock 返回 200，管理页产生 1 条记录，详情可查看客户端请求、上游请求、上游返回和客户端响应四段内容。
- 开关验证：关闭全局开关后 Relay 仍返回 200，记录总数保持不变；重新开启后配置恢复。
- 权限和导出：匿名访问管理 API 返回 401；Root JSONL 导出返回一行完整记录并包含 Body 字段。

### 7.3 尚未通过的环境门禁

本机 Docker 构建已实际重试，但未得到镜像：Debian Security 仓库连续返回 HTTP 502，同时 Docker Desktop 在主机磁盘使用率约 98% 时出现构建缓存 `input/output error`。这不是源码编译失败；不得通过修改项目 Dockerfile 绕过。发布前应先释放 Docker/主机空间，再在干净 CI Runner 执行新增工作流，并用 PostgreSQL/Redis 的生产备份副本完成一次恢复演练。

### 7.4 注册码列表与渠道采集入口增强

- 注册码查询 API 增加名称/注册码关键词、启停状态、使用状态和有效期筛选，并保留服务端分页；默认每页 20 条。
- default 注册码页面改用统一 `DataTablePage`，筛选和分页状态写入 URL，刷新后可恢复。
- 表格首列支持当前页全选；分页或筛选变化时清空选择，不实现跨页全选。
- 批量工具栏支持逐行复制注册码、批量启用、批量禁用和二次确认后批量软删除；成功提示使用数据库实际影响条数，失败时保留选择。
- Root 批量 API 对 ID 做正整数、去重和 1–100 条边界校验，状态仅允许启用或禁用；批量启停和删除分别使用单条 GORM `UPDATE ... IN`、软删除语句，并写入管理审计日志。
- 渠道级完整对话采集从 OpenAI/Claude 字段透传区域移至高级设置的独立“对话采集”区域，对所有渠道类型统一展示，但只允许 Root 查看和修改。
- 对话采集说明明确要求同时开启全局开关；渠道开关开启后，高级设置在重新编辑时自动展开并显示已配置状态。非 Root 不渲染该区域，后端仍保留原值并拒绝越权覆盖。

本轮补充验证结果：

- 新增筛选、有效期边界、分页总数、批量实际影响数、软删除保留使用记录和请求参数边界测试；`go test ./...` 通过。
- default `bun run typecheck`、目标文件 oxlint、`bun run build` 通过；classic `bun run build` 通过。全量 oxlint 仍存在仓库原有错误，本轮目标文件无新增 lint 问题。
- 本地当前源码服务已启动并完成浏览器验证：筛选 URL 可刷新恢复，20 条当前页全选后翻页会清空选择，批量禁用返回并刷新状态，删除取消不发请求，确认删除执行批量 API。
- Root 编辑渠道时可在高级设置发现独立采集区域，保存后 `settings.conversation_log_enabled=true`，重新编辑会自动展开且开关保持开启；普通管理员编辑同一渠道时不显示采集区域。

### 7.5 对话采集复审

- 四段 Body 仍只记录最终尝试；客户端响应以实际成功写出的字节为准，重试元数据包含全部已尝试渠道。
- JSON、SSE 和截断文本中的 Base64 Data URI 以及常见多模态二进制字段在入库前替换为占位符，不保存图片、音频或文件正文。
- 容量清理按最旧顺序只删除达到上限所需的记录，避免按批次过量删除。

## 8. 本地复验命令

```bash
cd web/default
bun run typecheck
bun run build

cd ../classic
bun run build

cd ../..
go test ./...
go build -o /tmp/new-api-dev-local .

mkdir -p /tmp/new-api-dev-runtime
cd /tmp/new-api-dev-runtime
/tmp/new-api-dev-local --port 33001 --log-dir /tmp/new-api-dev-runtime/logs
```

Docker 环境恢复后再执行：

```bash
docker build -t new-api:dev-local .
docker compose \
  -f docker-compose.yml \
  -f deploy/docker-compose.image.override.example.yml \
  config --quiet
```

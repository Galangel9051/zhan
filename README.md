# Compose Manager

轻量级 Docker Compose Web 管理与 Git 自动部署平台。React + TypeScript + Vite + Tailwind CSS + TanStack Query + Lucide；Go + Gin + `database/sql` + 纯 Go SQLite。生产运行仅需要一个包含前端资源的 Go 二进制，以及宿主机 Docker Compose 和 Git。

## 已实现

- 首次启动自动创建 `admin / 123456` 并强制改密、Argon2id 密码哈希、HttpOnly/SameSite Cookie、CSRF 校验、登录限流、操作审计。
- Compose 自动发现、服务器目录/文件选择器、只读导入、多文件绑定、Stack / Service / Container 状态与启停重启。
- Git 检查、仅拉取代码、拉取并部署、基于当前代码重新构建；拒绝脏工作区与非快进更新。
- SQLite 持久化任务队列、默认全局串行、项目与工作区互斥、取消、超时与重启中断恢复。
- 独立保存远程、本地、最近尝试、最近成功 Commit；失败不会误记部署成功。
- 按项目轮询，自动部署，失败后 5/15 分钟退避重试；达到阈值暂停，新的 Commit 可恢复。
- 持久化任务日志、SSE、游标读取、下载、容器最近 N 行日志与过期清理。
- SMTP 设置、测试邮件、按失败阶段通知、独立发送结果与邮件冷却去重。
- 一键构建、Go embed、Linux systemd 模板。

手动停止整个项目将持久化 `desired_state=stopped`：Git 仍可检查、手动拉取，但自动部署不会启动服务。手动“部署并启动”需显式勾选确认。

## 快速开发

环境：Go 1.26+、Node.js 24+、Docker Engine + Compose Plugin（需支持 `up --wait`、`--pull`）、Git。不需要 C 编译器。

```sh
nvm install
nvm use
cd frontend
npm ci
```

首次启动会自动创建管理员：用户名 `admin`，初始密码 `123456`。第一次登录后必须设置新密码（至少 6 个字符）才能进入后台。修改后全部旧会话失效，需要重新登录。已有账户不受影响。

终端一，从 `backend/` 启动后端：

```sh
go run ./cmd/server
```

终端二，从仓库根目录启动前端：

```sh
cd frontend
npm run dev
```

访问 [http://127.0.0.1:5173](http://127.0.0.1:5173)。后端默认 `127.0.0.1:18080`，Vite 代理 `/api`。默认不设置目录白名单，可选择服务账户有权限访问的任意服务器路径。导入窗口提供“选择目录”和“选择文件”，支持浏览目录、粘贴路径、隐藏目录切换及按顺序多选 Compose 文件。只有显式配置 `allowed_roots` 或 `CM_ALLOWED_ROOTS` 时才会限定范围。

```sh
# 使用独立配置文件启动。
go run ./cmd/server --config ../config.yaml
```

配置示例见 [configs/config.example.yaml](configs/config.example.yaml)。可通过 `CM_LISTEN`、`CM_DATA_DIR`、`CM_ALLOWED_ROOTS` 覆盖；根目录列表使用系统路径分隔符（Linux/macOS 为冒号）。`.env` 不自动加载。

## 构建单文件应用

在仓库根目录执行：

```sh
./scripts/build.sh
./bin/compose-manager
```

构建脚本安装前端锁定依赖、生成生产页面、同步 embed 资源，并以 `CGO_ENABLED=0` 编译。首次访问使用 `admin / 123456` 登录并设置新密码。访问 [http://127.0.0.1:18080](http://127.0.0.1:18080)。前端不需要 Node.js 常驻，也不需要 Nginx 才能运行。

生产部署步骤、SSH 凭据、备份、HTTPS 及 systemd 配置详见 [安装文档](docs/install.md)。设计原文保存在 [docs/design-v1.md](docs/design-v1.md)。

## 核心命令语义

| 操作 | 行为 |
| --- | --- |
| 检查更新 | 查询本地版本和 `git ls-remote`，不修改工作区 |
| 拉取代码 | 干净工作区中 fetch + merge --ff-only，不部署 |
| 拉取并部署 | 更新 Git，按服务构建/拉取镜像，up --no-build --pull never，最多等待 120 秒验证容器运行与健康状态 |
| 重新构建 | 使用当前本地代码，随后部署，不拉取 Git |
| 启动 | up -d --no-build --pull never；不主动拉取 Git、镜像或构建 |
| 停止 | compose stop；不执行 down，不删除容器或数据卷 |

所有 Compose 命令明确指定 `-p`、`--project-directory`、每个 `-f`。Git/Docker 使用固定模板和参数数组执行，无任意 Shell API。服务启动使用 `--no-deps`，依赖未运行时拒绝。容器操作会核对其所属 Stack。

Git 绑定使用现有 `origin` 和当前分支，推荐为服务账户配置最小权限 SSH Deploy Key；不在界面上传私钥、不跳过 SSH 主机校验、不执行 `reset --hard`。仅将可信项目纳入自动构建。

## API

统一前缀 `/api/v1`，登录后取得 `csrf_token`，写请求使用 `X-CSRF-Token` 与会话 Cookie。

- 目录浏览：`GET /filesystem?path=/opt/projects&offset=0&limit=200&hidden=0`（已登录且已完成首次改密，只返回目录/文件名，不返回文件内容）
- 认证：`POST /auth/login`、`POST /auth/logout`、`GET /auth/me`、`GET /auth/status`、`POST /auth/password`
- 项目：`GET /stacks`、`GET /stacks/discover`、`POST /stacks/import`、`GET/PUT /stacks/:id`
- 操作：`POST /stacks/:id/{start,stop,restart,rebuild,deploy}`
- 服务：`GET /stacks/:id/services`、`POST /stacks/:id/services/:name/{start,stop,restart}`
- 容器：`POST /stacks/:id/containers/:container/{start,stop,restart}`
- 日志：`GET /stacks/:id/logs`、`GET /stacks/:id/services/:name/logs`、`GET /stacks/:id/containers/:container/logs`，支持 `tail=200`
- Git：`POST /stacks/:id/git/{check,pull}`、`GET /stacks/:id/git/status`、`PUT /stacks/:id/polling`
- 任务：`GET /tasks?stack_id=1&before=100&limit=50`、`GET /tasks/:id`、`POST /tasks/:id/{retry,cancel}`
- 任务输出：`GET /tasks/:id/logs?offset=0&limit=65536`、`GET /tasks/:id/logs?download=1`、`GET /tasks/:id/stream`
- 设置：`GET/PUT /settings`、`PUT /settings/smtp`、`POST /settings/smtp/test`、`GET /audit`、`GET /notifications`

长操作返回 `202 {"data":{"id":...}}`，成功读取返回 `{"data":...}`，失败为 `{"error":"..."}`。任务 SSE 支持 `Last-Event-ID` / `offset` 续读；未指定从日志末尾最多 64 KiB 开始。会话失效后断开日志流。

## 验证

```sh
./scripts/check.sh
# 并发检查：
cd backend
go test -race ./...
```

测试覆盖本地临时 Git 远程仓库、脏工作区保护、Commit 记账、失败构建不部署、停止期望状态、有限重试、任务取消互斥、SQLite 持久化恢复、路径/符号链接边界、认证/CSRF、只读导入、SSE/日志下载、SMTP 加密/去重/独立错误记录。自动测试使用模拟 Docker，不操作现有业务项目。

## 目录

```text
backend/cmd/server/             程序入口、管理员初始化、进程锁
backend/internal/auth/         密码、Session、凭据加密
backend/internal/database/     SQLite 存储与版本化 SQL 迁移
backend/internal/docker/       路径校验、发现、Compose 固定命令
backend/internal/deploy/       Git、队列、调度、任务与日志
backend/internal/notify/       SMTP 与去重
backend/internal/httpapi/      REST / SSE 与鉴权
backend/internal/webui/        嵌入的前端构建资源
frontend/src/                  概览、项目、部署、设置页面
scripts/                       构建与验证
configs/                       YAML、systemd 示例
```

原基础示例的 Dockerfile 和 Compose 配置保留在 [docs/legacy-starter](docs/legacy-starter/) 作为历史参考，当前 V1 使用宿主机部署。不会自动停掉先前已运行的示例容器。

V1 不承诺自动回滚或零停机部署。部署阶段失败可能已影响旧服务；构建失败会停止后续部署。内存目标尚待目标 Linux 服务器实测，不能当作已验证结果。

可选的真实 Docker 验证仅创建一个随机命名、无公开端口、无宿主机挂载的临时 Nginx 项目，结束后只清理该测试项目；不会操作现有项目。需预先准备本地 `nginx:stable-alpine` 镜像：

```sh
cd backend
CM_DOCKER_INTEGRATION=1 go test -v ./internal/deploy -run TestRealDockerLifecycle
```

Git 合并额外使用 `--no-overwrite-ignore`，避免快进更新覆盖被忽略的本地文件。参考 [Git merge 官方文档](https://git-scm.com/docs/git-merge)。

未完成首次改密时，仅允许读取当前用户、修改密码及退出登录。`POST /auth/password` 接收 `current_password`、`new_password`、`confirm_password`，须通过会话与 CSRF 校验。`--init-admin` 保留为可选命令，只创建默认初始密码账户，不再读取终端密码，也不覆盖已有账户。

外观：右上角可切换明亮/夜间模式，从点击位置以圆形扩散到整页，登录页、管理页和弹窗统一生效；键盘切换从按钮中心扩散，系统开启减少动态效果时直接切换。浏览器会保存选择；首次访问跟随系统外观。

SMTP 配置顶部提供“发送测试邮件”，使用当前表单，无需先保存；连接信息未变时可留空密码复用已保存凭据。测试显示发送中/成功/失败原因，并写入通知记录，不自动保存当前配置。

交互过渡：页面与标签内容淡入、服务列表高度展开/收起、箭头旋转、弹窗进入/退出及控件悬停反馈统一使用短时过渡，系统设置“减少动态效果”时自动简化。数据后台刷新不会触发整页动画。

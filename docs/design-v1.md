# Compose Manager 开发设计文档

**项目名称：** Compose Manager（暂定）

**文档版本：** V1.0

**项目定位：** 轻量级 Docker Compose Web 管理与 Git 自动部署平台

**技术栈：** React + TypeScript + Vite + Gin + SQLite

**部署环境：** 本地 Linux 服务器，无公网 IP

**开发目标：** 以 Docker Desktop 风格管理现有 Docker Compose 项目，并为其提供 Git 手动更新、定时轮询、自动构建部署、日志查看和邮件告警能力。

---

# 1. 项目概述

## 1.1 项目背景

目前服务器上存在多个使用 Docker Compose 部署的项目。

现有 CI/CD 方式是通过定时脚本执行以下操作：

1. 定时检查 Git 远程仓库。
2. 检测主分支是否存在新提交。
3. 有更新时执行 `git pull`。
4. 执行 `docker compose up -d --build`。
5. 等待下一轮定时任务。

现有方案存在以下问题：

- 缺少 Web 管理界面。
- 无法统一查看多个 Compose 项目状态。
- 无法方便地一键启动、停止、重启项目。
- 无法在 Web 页面手动触发 Git 更新。
- 无法查看完整的构建历史。
- 构建失败后没有邮件通知。
- 构建失败可能导致后续更新被跳过。
- 多个项目需要分别维护定时脚本。
- 缺乏统一的日志管理。
- 不能清晰地区分 Git 更新、构建和容器运行状态。

因此计划开发一个轻量级自托管平台，替代原有的分散式部署脚本。

## 1.2 核心目标

平台需要实现：

1. 自动发现并导入本机已有的 Docker Compose 项目。
2. 使用类似 Docker Desktop 的页面管理 Compose Stack、Service 和 Container。
3. 支持项目或服务级的一键启动、停止、重启。
4. 支持 Git 手动检查、拉取代码。
5. 支持按项目配置 Git 定时轮询间隔。
6. 支持代码更新后的自动构建与部署。
7. 支持手动重新构建。
8. 支持构建过程和容器日志查看。
9. 支持构建失败、部署失败时发送邮件。
10. 尽可能降低平台常驻内存和运行依赖。

## 1.3 非功能目标

- 单机部署，无需公网 IP。
- 不依赖 Kubernetes。
- 不依赖 Redis、PostgreSQL 等独立服务。
- 不依赖 Node.js 常驻服务。
- 后端以一个 Go 进程运行。
- 支持通过 systemd 管理。
- 支持已有 Compose 项目的无损导入。
- 自动部署任务具备串行控制和错误恢复能力。
- Web 后台必须具备登录认证。

**内存目标：**

| 场景 | 目标内存 |
|---|---|
| 空闲运行 | 40～100MB |
| 管理 10～30 个项目 | 60～150MB |
| 普通管理操作 | 尽可能低于 200MB |

以上为优化目标，并非实测保证值。不包含 Docker 引擎、业务容器、Git 子进程和 Docker 构建进程的资源消耗。

---

# 2. 系统架构

## 2.1 技术选型

| 层级 | 技术 | 用途 |
|---|---|---|
| 前端 | React + TypeScript | Web 管理界面 |
| 构建工具 | Vite | 前端构建 |
| UI | Tailwind CSS | 页面样式 |
| 图标 | Lucide React | 界面图标 |
| 数据请求 | TanStack Query | 服务端状态管理和缓存 |
| 后端 | Go + Gin | HTTP API |
| 数据库 | SQLite | 项目配置、任务记录 |
| 数据库访问 | database/sql + SQLite 驱动 | 持久化 |
| Docker 管理 | Docker Compose CLI | 容器编排操作 |
| Git 管理 | Git CLI | 仓库检查、更新 |
| 实时日志 | SSE | 服务端日志推送 |
| 调度器 | Go 定时调度 | 多项目轮询 |
| 通知 | SMTP | 邮件告警 |
| 静态资源 | Go embed | 打包 React 静态文件 |
| 部署管理 | systemd | 服务启动与守护 |

SQLite 推荐先采用纯 Go 驱动，减少 CGO 和跨平台构建负担。

第一版不需要单独的消息队列。任务信息保存到 SQLite，Go 内部使用受控 Worker 执行即可。

## 2.2 架构关系

```text
                 Browser
                    |
          React + TypeScript
                    |
               HTTP / SSE
                    |
              Gin Backend
                    |
    +---------------+---------------+
    |               |               |
 Project API    Task Scheduler   Docker API
    |               |               |
    |          Git Polling           |
    |               |               |
    +------- Task Executor ----------+
                    |
      +-------------+-------------+
      |             |             |
    Git CLI    Compose CLI      SMTP
      |             |             |
 Git Repository   Docker       Email
                    |
               Containers

          SQLite Persistence
```

## 2.3 架构原则

系统采用单体架构。

所有核心功能集中在同一个 Go 服务中，通过内部模块实现职责隔离。

前端只负责展示与发送操作请求，不直接执行 Docker 命令。

后端负责：

- 管理数据库。
- 读取 Docker 状态。
- 执行 Docker Compose 操作。
- 检查 Git 仓库更新。
- 调度构建任务。
- 保存日志和执行结果。
- 发送邮件。
- 提供 API 和静态资源。

---

# 3. 管理对象与粒度

## 3.1 管理层级

平台使用以下管理模型：

```text
Git Repository
      |
Deployment Configuration
      |
Compose Stack
      |
Compose Service
      |
Docker Container
```

### Git Repository

代表一个代码仓库，包含：

- 仓库地址
- 本地路径
- 分支
- 认证方式
- 远程最新 Commit

### Deployment Configuration

代表一套部署配置，包含：

- 关联的 Git 仓库
- 关联的 Compose Stack
- 自动部署开关
- 轮询间隔
- 构建策略
- 最近成功部署版本

### Compose Stack

代表一个 Docker Compose 项目，包含：

- Compose Project Name
- Compose 文件路径
- 项目基础目录
- 所属服务器
- 所有服务

### Compose Service

代表 Compose 文件中的服务，例如：

- api
- web
- redis
- postgres

### Docker Container

代表具体运行的容器实例。

## 3.2 操作粒度

| 操作 | Stack | Service | Container |
|---|---|---|---|
| 启动 | 支持 | 支持 | 支持 |
| 停止 | 支持 | 支持 | 支持 |
| 重启 | 支持 | 支持 | 支持 |
| 构建 | 支持 | 支持 | 不支持 |
| Git 更新部署 | 支持 | 后续支持 | 不支持 |
| 查看日志 | 支持 | 支持 | 支持 |
| 查看运行状态 | 支持 | 支持 | 支持 |

第一版默认以 Compose Stack 作为自动部署单位。

Service 级别实现启动、停止、重启和日志查看，按服务自动构建可以作为后续增强功能。

不将具体 Container ID 作为 Git 自动部署目标，因为容器重建后 ID 可能变化。

---

# 4. 前端功能设计

## 4.1 页面布局

界面整体参考 Docker Desktop 的 Containers 页面。

采用：

- 左侧导航栏
- 顶部状态栏
- 主内容区域
- Compose 项目分组列表
- 可展开的 Service / Container 列表

建议导航结构：

```text
Compose Manager
│
├── Dashboard
│
├── Containers / Apps
│   ├── Compose Stack A
│   │   ├── Service api
│   │   ├── Service redis
│   │   └── Service db
│   │
│   └── Compose Stack B
│       ├── Service frontend
│       └── Service backend
│
├── Deployments
│   ├── 构建历史
│   └── 构建日志
│
└── Settings
    ├── Git 配置
    ├── SMTP 配置
    └── 系统设置
```

## 4.2 Dashboard

展示：

- Compose 项目总数
- 正在运行的项目数
- 已停止的项目数
- 构建失败的项目数
- 最近部署任务
- 最近失败任务

数据应区分两个维度：

**容器运行状态：**

- Running
- Stopped
- Partial
- Unhealthy
- Unknown

**最近部署状态：**

- Pending
- Running
- Success
- Failed
- Cancelled

例如，某项目构建失败但旧容器仍正常运行，应同时显示：

`Running / Last Build Failed`

不能将构建失败误判为容器已停止。

## 4.3 Containers / Apps 页面

每个 Compose Stack 显示：

| 字段 | 说明 |
|---|---|
| Name | Compose 项目名称 |
| Status | 当前运行状态 |
| Services | 服务数量 |
| Git Branch | 关联分支 |
| Commit | 最近成功部署版本 |
| Auto Update | 是否开启定时更新 |
| Last Deployment | 最近部署结果 |

每个项目提供：

- 启动
- 停止
- 重启
- 手动检查更新
- 手动拉取 Git
- 拉取并部署
- 重新构建
- 查看日志
- 设置

项目列表支持展开，查看对应 Service 和 Container。

## 4.4 项目详情

建议包含四个标签页：

### Overview

- 项目运行状态
- Compose 文件
- Git 仓库与分支
- 最新远程 Commit
- 当前本地 Commit
- 最近成功部署 Commit
- 自动更新状态

### Services

- 服务名称
- 容器数量
- 运行状态
- 健康状态
- 使用的镜像
- 启动、停止、重启操作

### Logs

- 构建日志
- 部署日志
- 容器日志
- 自动滚动
- 日志搜索
- 日志下载

### Settings

- Git 仓库信息
- Compose 配置
- 轮询间隔
- 自动部署开关
- 失败邮件通知开关

---

# 5. Docker Compose 管理

## 5.1 发现已有项目

平台启动时扫描当前 Docker 环境中的 Compose 项目。

可以使用：

```bash
docker compose ls --all --format json
```

同时通过 Docker 容器标签补充服务和容器信息。

常见标签：

```text
com.docker.compose.project
com.docker.compose.service
```

自动发现的项目可以先加入管理列表，再由用户确认并绑定：

- 项目目录
- Compose 文件路径
- Git 仓库
- Git 分支

无法可靠识别的原始 Compose 文件或目录不得凭空推断，要求用户手动指定。

## 5.2 导入现有项目

支持绑定已运行的 Compose Stack。

用户填写：

```text
项目名称：backend-api
项目路径：/opt/projects/backend-api
Compose 文件：compose.yaml
Compose Project Name：backend-api
Git 分支：main
```

导入时进行只读检查：

1. 确认项目目录存在。
2. 确认 Compose 文件存在。
3. 校验 Compose 配置。
4. 查询同名 Compose Stack。
5. 比对服务信息。
6. 保存绑定关系。

导入过程不能自动执行 `up`、`down` 或重新构建。

应保留原有 Compose Project Name，避免误创建另一套容器。

## 5.3 Compose 执行规则

所有命令必须明确指定：

- `-p`：Compose Project Name
- `-f`：Compose 文件
- `--project-directory`：项目基础目录

示例：

```bash
docker compose \
  -p backend-api \
  --project-directory /opt/projects/backend-api \
  -f /opt/projects/backend-api/compose.yaml \
  ps --all
```

禁止通过字符串拼接并执行任意 Shell 命令。

Go 使用 `exec.CommandContext`，以参数数组方式执行。

## 5.4 启动

执行：

```bash
docker compose up -d
```

启动项目时默认不主动拉取 Git，也不强制构建新镜像。

如果镜像缺失或者必须构建，前端提示用户进行构建。

## 5.5 停止

执行：

```bash
docker compose stop
```

不得将普通“停止”操作实现为 `docker compose down`。

停止后保留：

- 容器
- 持久化卷
- Compose 配置
- Git 仓库

## 5.6 重启

执行：

```bash
docker compose restart
```

## 5.7 手动构建

推荐分阶段执行：

```bash
docker compose config -q
docker compose build
docker compose up -d --no-build \
  --wait --wait-timeout 120
```

完整实现应支持配置构建参数，并根据 Compose 使用 `build:` 还是 `image:` 来选择构建或镜像拉取策略。

对于仅使用 `image:` 的项目，不应该默认把源码构建作为必要步骤。

## 5.8 Service 级操作

示例：

启动指定服务：

```bash
docker compose up -d --no-deps api
```

停止指定服务：

```bash
docker compose stop api
```

重启指定服务：

```bash
docker compose restart api
```

服务级操作需要检查依赖关系。

---

# 6. Git 更新系统

## 6.1 更新方式

支持两种触发方式：

1. 手动更新
2. 定时轮询

不依赖外部 Git Webhook。

因此可以在没有公网 IP 的内网服务器上使用。

## 6.2 手动操作语义

为了避免误操作，需要明确区分：

| 操作 | 行为 |
|---|---|
| 检查更新 | 只检查远程 Commit |
| 拉取代码 | 更新本地 Git 工作区，不部署 |
| 拉取并部署 | 更新 Git、构建、部署 |
| 重新构建 | 使用当前本地代码重新构建 |

界面不得将 Git Pull 和 Docker Image Pull 混为一谈。

## 6.3 定时轮询

每个项目独立配置轮询间隔。

示例：

```yaml
git:
  repository: git@example.com:team/backend.git
  branch: main

polling:
  enabled: true
  interval_seconds: 300
  auto_deploy: true
```

调度器按照项目配置检查远程分支。

执行：

```bash
git ls-remote origin refs/heads/main
```

取得远程 Commit。

如果发现未部署的新提交，则按照项目自动部署配置创建任务。

## 6.4 Commit 状态

必须分别记录：

```text
remote_commit
local_commit
last_attempted_commit
last_success_commit
```

说明：

- `remote_commit`：远程最新版本。
- `local_commit`：本地工作区对应版本。
- `last_attempted_commit`：最近尝试构建部署的版本。
- `last_success_commit`：最近成功完成部署的版本。

不能将 Git 拉取成功视为部署成功。

如果某个 Commit 构建失败，下次轮询不能简单因为 Git 没有新变更而忽略它。

## 6.5 Git 更新规则

实际部署前：

1. 获取远程最新版本。
2. 检查本地仓库状态。
3. 拒绝覆盖未经确认的本地修改。
4. 使用 `git fetch` 更新远程引用。
5. 使用安全的快进更新策略。
6. 确认本地代码版本。
7. 执行构建任务。

可以使用 `git pull --ff-only`，避免定时任务意外生成 Merge Commit。

平台不能默认执行破坏性的 `git reset --hard`。

对于工作区存在修改、分支分叉或者认证失败的情况，应记录明确错误并停止部署。

## 6.6 手动停止与自动部署

必须维护项目的期望状态：

```text
desired_state = running | stopped
```

用户手动停止项目后：

- Git 定时检查可以继续。
- 可以发现远程新版本。
- 可以按配置拉取代码。
- 不能自动重新启动项目。
- 自动部署任务应暂停或跳过启动阶段，并清晰记录状态。

只有用户主动启动，或显式选择部署并启动，才能恢复运行。

---

# 7. 构建与部署任务系统

## 7.1 任务类型

第一版需要支持：

```text
git_check
git_pull
deploy
rebuild
start
stop
restart
```

## 7.2 任务状态

```text
pending
running
success
failed
cancelled
```

任务保存到 SQLite。

执行历史不能仅保存在进程内存中。

## 7.3 构建流程

```text
用户手动触发 / 定时任务
          |
       创建任务
          |
       获取部署锁
          |
       检查 Git
          |
       更新工作区
          |
       验证 Compose
          |
       构建镜像
          |
       更新容器
          |
       健康检查
          |
       记录部署结果
          |
    成功 / 失败通知
          |
       释放部署锁
```

只读 Git 检查任务不必占用完整部署锁，但不能与工作区变更发生冲突。

## 7.4 并发控制

默认：

```text
全局同时构建数：1
同一 Stack 同时部署数：1
```

同一个 Compose Stack 不能同时执行构建、更新和生命周期变更操作。

遇到重复任务时，应合并、排队或拒绝，避免 Docker Compose 命令相互冲突。

可根据服务器资源配置全局构建并发数。

## 7.5 构建失败处理

发生失败时：

1. 标记任务为 Failed。
2. 保存失败阶段。
3. 保存退出码。
4. 保存错误摘要和完整日志。
5. 记录本次尝试的 Commit。
6. 不更新 `last_success_commit`。
7. 根据设置发送邮件通知。
8. 根据重试策略决定是否再次执行。

构建失败时不得继续执行部署。

如果旧容器仍在运行，应保留旧容器。

需要注意：构建成功并不代表运行阶段一定安全。容器更新失败可能已经影响旧服务，所以 V1 不承诺完整自动回滚，回滚功能列入后续阶段。

## 7.6 失败重试

建议采用可配置退避策略：

```text
第一次失败：记录并通知
第二次重试：5 分钟后
第三次重试：15 分钟后
达到阈值：暂停自动重试
```

新的 Commit 出现后，可按策略重新创建任务。

避免每次 Git 轮询都对同一个失败 Commit 无限构建。

---

# 8. 日志系统

## 8.1 日志类型

包含：

- Git 检查日志
- Git 拉取日志
- Docker 构建日志
- Docker 部署日志
- 容器运行日志
- 系统错误日志

## 8.2 实时日志

使用 Server-Sent Events（SSE）。

执行任务时：

1. 后端启动子进程。
2. 分别读取 stdout 和 stderr。
3. 将日志写入持久化文件。
4. 同时通过 SSE 推送前端。
5. 前端实时追加输出。
6. 任务结束后记录执行状态。

不能将完整日志无限累积在 Go 内存中。

## 8.3 日志持久化

目录示例：

```text
data/
├── manager.db
└── logs/
    ├── task-1001.log
    ├── task-1002.log
    └── task-1003.log
```

SQLite 存储日志索引和元数据。

日志文件存储详细内容。

需要支持：

- 分页读取
- 实时查看
- 最近 N 行
- 下载日志
- 日志保留期限
- 自动清理旧日志

日志保留时间默认可设置为 30 天。

日志必须脱敏，不得泄漏 Git 令牌、SMTP 密码或其他密钥。

---

# 9. 邮件通知系统

## 9.1 SMTP 设置

Web 界面提供：

- SMTP Host
- SMTP Port
- Username
- Password / App Password
- TLS 模式
- 发件人
- 收件人列表

需要支持发送测试邮件。

密码不能以明文形式通过 API 返回。

## 9.2 通知触发条件

第一版支持：

- Docker Build 失败
- Compose 部署失败
- Git 拉取失败
- 健康检查失败

允许用户分别开启和关闭。

构建成功邮件暂时不是必要功能。

## 9.3 邮件内容

标题示例：

```text
[Compose Manager] backend-api 构建失败
```

正文包含：

```text
项目：backend-api
分支：main
Commit：a8b92c1
执行时间：2026-11-04 14:30:00
失败阶段：Docker Build
退出码：1

错误摘要：
ERROR: build failed

请登录 Compose Manager 查看完整日志。
```

邮件发送失败不能覆盖原始构建失败信息。

通知任务需要单独记录执行结果。

## 9.4 邮件去重

相同项目、相同 Commit、相同失败阶段，在指定时间窗口内不重复发送大量告警。

建议支持：

```text
notification_cooldown = 30 minutes
```

新的失败类型或新的 Commit 可以触发新通知。

---

# 10. 数据库设计

采用 SQLite。

建议启用 WAL 模式，并设置合理的 busy timeout。

第一版核心数据表如下。

## 10.1 git_repositories

管理 Git 仓库配置。

主要字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | INTEGER | 主键 |
| name | TEXT | 仓库名称 |
| url | TEXT | Git 地址 |
| local_path | TEXT | 本地工作区 |
| branch | TEXT | 目标分支 |
| auth_ref | TEXT | 凭据引用 |
| created_at | DATETIME | 创建时间 |

## 10.2 compose_stacks

管理 Docker Compose 绑定。

| 字段 | 类型 | 说明 |
|---|---|---|
| id | INTEGER | 主键 |
| name | TEXT | 显示名称 |
| project_name | TEXT | Compose Project Name |
| project_directory | TEXT | 项目基础目录 |
| compose_files | TEXT | Compose 文件路径集合 |
| desired_state | TEXT | running / stopped |
| created_at | DATETIME | 创建时间 |

第一版单机部署，后续可增加 server_id。

## 10.3 deployment_configs

保存自动部署配置。

| 字段 | 类型 | 说明 |
|---|---|---|
| id | INTEGER | 主键 |
| repository_id | INTEGER | Git 仓库 |
| stack_id | INTEGER | 关联 Stack |
| polling_enabled | BOOLEAN | 是否轮询 |
| poll_interval | INTEGER | 秒 |
| auto_deploy | BOOLEAN | 是否自动部署 |
| notify_on_failure | BOOLEAN | 是否邮件通知 |
| remote_commit | TEXT | 远程版本 |
| local_commit | TEXT | 本地版本 |
| last_attempted_commit | TEXT | 最近尝试版本 |
| last_success_commit | TEXT | 最近成功版本 |

## 10.4 tasks

记录所有操作任务。

| 字段 | 类型 | 说明 |
|---|---|---|
| id | INTEGER | 主键 |
| stack_id | INTEGER | 所属 Stack |
| task_type | TEXT | 任务类型 |
| trigger_type | TEXT | manual / scheduled |
| commit_sha | TEXT | Git Commit |
| status | TEXT | 状态 |
| stage | TEXT | 当前或失败阶段 |
| exit_code | INTEGER | 退出码 |
| log_path | TEXT | 日志文件 |
| error_message | TEXT | 错误摘要 |
| started_at | DATETIME | 开始时间 |
| finished_at | DATETIME | 结束时间 |

## 10.5 notification_logs

记录邮件发送情况。

字段包括：

- id
- task_id
- recipient
- notification_type
- status
- sent_at
- error_message

## 10.6 其他数据表

还需提供：

- users：管理员登录
- app_settings：系统配置
- credentials：加密凭据或凭据引用
- audit_logs：关键操作审计记录

数据库必须实现外键约束、必要的唯一索引以及迁移机制。

---

# 11. 后端 API 设计

采用 RESTful API。

统一前缀：

```text
/api/v1
```

## 11.1 身份认证

| Method | Endpoint | 功能 |
|---|---|---|
| POST | /auth/login | 登录 |
| POST | /auth/logout | 退出 |
| GET | /auth/me | 当前用户 |

## 11.2 Compose Stack

| Method | Endpoint | 功能 |
|---|---|---|
| GET | /stacks | 获取 Stack 列表 |
| GET | /stacks/discover | 发现现有 Stack |
| POST | /stacks/import | 导入 Stack |
| GET | /stacks/:id | 获取详情 |
| PUT | /stacks/:id | 更新配置 |
| POST | /stacks/:id/start | 启动 |
| POST | /stacks/:id/stop | 停止 |
| POST | /stacks/:id/restart | 重启 |
| POST | /stacks/:id/rebuild | 重新构建 |
| GET | /stacks/:id/services | 获取服务 |

## 11.3 Service

| Method | Endpoint | 功能 |
|---|---|---|
| POST | /stacks/:id/services/:name/start | 启动服务 |
| POST | /stacks/:id/services/:name/stop | 停止服务 |
| POST | /stacks/:id/services/:name/restart | 重启服务 |
| GET | /stacks/:id/services/:name/logs | 获取日志 |

## 11.4 Git

| Method | Endpoint | 功能 |
|---|---|---|
| POST | /stacks/:id/git/check | 检查更新 |
| POST | /stacks/:id/git/pull | 拉取 Git |
| POST | /stacks/:id/deploy | 拉取并部署 |
| GET | /stacks/:id/git/status | Git 状态 |
| PUT | /stacks/:id/polling | 修改轮询配置 |

## 11.5 Tasks

| Method | Endpoint | 功能 |
|---|---|---|
| GET | /tasks | 任务列表 |
| GET | /tasks/:id | 任务详情 |
| GET | /tasks/:id/logs | 历史日志 |
| GET | /tasks/:id/stream | SSE 实时日志 |
| POST | /tasks/:id/retry | 重试任务 |
| POST | /tasks/:id/cancel | 取消任务 |

## 11.6 Settings

| Method | Endpoint | 功能 |
|---|---|---|
| GET | /settings | 获取设置 |
| PUT | /settings | 修改设置 |
| PUT | /settings/smtp | SMTP 配置 |
| POST | /settings/smtp/test | 测试邮件 |

长时间运行的操作不应阻塞 HTTP 请求。

启动、停止、构建、Git 更新等操作应创建任务，并返回任务 ID，由前端查询执行结果。

---

# 12. 项目目录结构

建议采用 Monorepo。

```text
compose-manager/
│
├── cmd/
│   └── server/
│       └── main.go
│
├── internal/
│   ├── api/
│   │   ├── handlers/
│   │   ├── middleware/
│   │   └── routes.go
│   │
│   ├── auth/
│   ├── config/
│   ├── database/
│   │   ├── migrations/
│   │   └── repository/
│   │
│   ├── docker/
│   │   ├── compose.go
│   │   └── discovery.go
│   │
│   ├── git/
│   │   ├── client.go
│   │   └── polling.go
│   │
│   ├── deploy/
│   │   ├── executor.go
│   │   ├── queue.go
│   │   └── manager.go
│   │
│   ├── logs/
│   ├── notify/
│   ├── scheduler/
│   └── service/
│
├── web/
│   ├── src/
│   │   ├── api/
│   │   ├── components/
│   │   ├── hooks/
│   │   ├── layouts/
│   │   ├── pages/
│   │   │   ├── Dashboard/
│   │   │   ├── Containers/
│   │   │   ├── Deployments/
│   │   │   └── Settings/
│   │   ├── types/
│   │   ├── App.tsx
│   │   └── main.tsx
│   │
│   ├── package.json
│   └── vite.config.ts
│
├── data/
│   ├── manager.db
│   └── logs/
│
├── configs/
├── scripts/
├── docs/
│
├── go.mod
├── Makefile
└── README.md
```

React 前端编译后生成静态文件，由 Go embed 嵌入后端二进制文件。

`data/` 不提交到 Git 仓库。

---

# 13. 运行与部署

## 13.1 开发环境

需要：

- Go
- Node.js（仅前端构建和开发使用）
- Docker Engine
- Docker Compose Plugin
- Git

开发时分别启动前后端。

生产环境不需要 Node.js 常驻。

## 13.2 生产部署

推荐直接运行在 Linux 宿主机。

使用 systemd：

```text
compose-manager.service
```

最终部署目录示例：

```text
/opt/compose-manager/
├── compose-manager
├── config.yaml
└── data/
    ├── manager.db
    └── logs/
```

服务通过内网地址访问：

```text
http://192.168.1.100:8080
```

建议实际运行时使用可信内网 HTTPS 反向代理或私有 VPN，避免管理凭据明文传输。

第一版不需要 Docker 化部署管理平台。

这样能够直接使用宿主机 Docker CLI 和已有项目路径，减少 Docker Socket 映射与宿主机路径不一致的问题。

---

# 14. 安全设计

虽然系统运行在内网，但控制 Docker 的管理程序具有很高权限。

必须实现基本安全措施。

## 14.1 登录认证

- 首次启动创建管理员。
- 密码使用 Argon2id 等安全算法进行哈希。
- 使用安全的 HttpOnly Session Cookie。
- 设置 SameSite 和合理的 Session 有效期。
- 采用 Cookie 会话时实现 CSRF 防护。
- 登录接口实现限流。

## 14.2 执行安全

- 禁止前端提交任意 Shell 命令。
- Docker 操作使用固定命令模板。
- 使用 `exec.CommandContext` 执行。
- 校验项目目录与 Compose 路径。
- 限制允许操作的工作目录。
- 防止路径穿越与符号链接绕过。
- 不允许任意用户修改 Docker Host 地址。
- 不将 Docker Socket 暴露在公网。

## 14.3 Git 安全

- Git 凭据不明文存储在数据库中。
- 推荐使用限定权限的 SSH Deploy Key。
- 校验 SSH 主机身份。
- 不自动覆盖存在本地修改的工作区。
- 定时任务只操作显式配置的仓库和分支。
- 仅构建可信来源的代码。

## 14.4 操作审计

需要记录：

- 用户
- 操作类型
- 目标项目
- 执行时间
- 执行结果

重点记录停止、重启、部署和配置修改等操作。

---

# 15. 性能优化

## 15.1 后端

- Gin 保持最少中间件。
- SQLite 使用受控连接池。
- Docker 查询结果短时间缓存。
- 不频繁执行重量级 Docker 命令。
- 日志流式处理。
- 避免无界 Goroutine。
- 使用 Context 控制任务取消和超时。

## 15.2 Git

- 根据项目独立调度。
- 未检测到新 Commit 时不执行完整 Git Pull。
- 使用 `git ls-remote` 进行轻量检测。
- 对任务添加随机时间偏移，避免大量项目同一时刻查询。
- 失败重试使用退避策略。

## 15.3 Docker

- 构建任务默认串行。
- 缓存项目和服务元数据。
- 不在每次页面刷新时读取全部日志。
- 使用按需查询与周期刷新。
- 不主动删除用户镜像、容器或卷。

## 15.4 前端

- Vite 生产构建。
- React 页面按需加载。
- 容器列表采用合理的状态缓存。
- 日志按需加载。
- SSE 只在需要查看实时任务时连接。
- 避免全局高频轮询。

---

# 16. 开发计划

## Phase 1：基础 Docker 管理

实现：

- Gin 后端初始化
- React 前端初始化
- SQLite 数据库
- 用户登录
- Docker Compose 自动发现
- 导入已有项目
- Compose Stack 列表
- Service / Container 展示
- 一键启动、停止、重启

交付目标：

能够在 Web 页面管理现有 Docker Compose 项目。

## Phase 2：Git 集成

实现：

- 绑定 Git 仓库
- 绑定分支
- 检查远程 Commit
- 手动 Git Pull
- 拉取并部署
- 本地工作区状态检查
- Git 错误展示

交付目标：

能够在页面手动完成代码更新和 Docker 构建部署。

## Phase 3：自动部署

实现：

- Git 定时调度
- 各项目独立轮询间隔
- 自动部署开关
- 任务队列
- 项目部署锁
- 构建日志持久化
- SSE 实时日志
- Commit 状态记录
- 失败退避重试

交付目标：

可以替换目前已有的定时部署脚本。

## Phase 4：告警和完善

实现：

- SMTP 配置
- 测试邮件
- 构建失败邮件
- 部署失败邮件
- 邮件去重
- 任务历史
- 日志下载与清理
- UI 状态优化
- 操作审计

交付目标：

形成可以长期运行的轻量级管理平台。

---

# 17. 测试与验收

## 17.1 Docker 功能验收

- 能发现已运行的 Compose Stack。
- 能导入指定 Compose 项目。
- 导入时不影响已有容器。
- 能一键启动项目。
- 能一键停止项目。
- 能一键重启项目。
- 能按 Service 操作。
- 能正确显示容器运行状态。

## 17.2 Git 功能验收

- 能检测远程新 Commit。
- 无变化时不重复部署。
- 手动 Pull 不自动重启容器。
- 手动部署可以成功更新代码和镜像。
- Git 认证失败能正确提示。
- 工作区存在本地修改时不会自动覆盖。
- Git Pull 成功但构建失败时不会标记部署成功。

## 17.3 自动化验收

- 多个项目可独立设置轮询间隔。
- 自动部署可以单独关闭。
- 同一个项目不会并发构建。
- 项目被手动停止后，不会被定时任务自动启动。
- 失败任务不会因为 Git 无新提交而永久丢失。
- 服务重启后历史任务仍可查询。
- 服务异常退出后，遗留的 Running 任务能被恢复或标记中断。

## 17.4 通知验收

- SMTP 测试邮件正常发送。
- 构建失败触发邮件。
- 部署失败触发邮件。
- 邮件包含错误摘要。
- 连续失败不会无限重复发送通知。
- SMTP 错误不会影响构建错误记录。

## 17.5 性能验收

在实际目标 Linux 服务器上进行测试：

- 管理 10～30 个 Compose 项目。
- 在无构建任务的空闲情况下测量 RSS。
- 在 Git 轮询期间测量内存变化。
- 验证连续运行时没有明显内存泄漏。
- 检查 SQLite 文件和日志增长情况。
- 验证并发构建限制是否生效。

性能目标应以真实测试结果为准。

---

# 18. V1 不实现的功能

为保持轻量化，以下功能暂不进入第一版：

- Kubernetes 管理
- 多服务器 Agent
- Docker Swarm 管理
- 完整 CI 流水线编辑器
- Git Webhook
- 镜像仓库管理
- 多租户组织管理
- 复杂 RBAC
- 全自动零停机部署
- 全自动版本回滚
- 复杂监控与告警规则
- 分布式任务调度
- 多数据库支持

后续可根据实际需要逐步扩展。

---

# 19. 关键设计决策

| 决策项 | 最终方案 |
|---|---|
| 前端 | React + TypeScript |
| 后端 | Go + Gin |
| 数据库 | SQLite |
| 部署形式 | Go 单体 + systemd |
| 页面风格 | Docker Desktop |
| Docker 管理 | Docker Compose CLI |
| 管理粒度 | Stack / Service / Container |
| Git 更新 | 手动 + 定时轮询 |
| Webhook | V1 不需要 |
| 自动构建 | 每项目独立配置 |
| 默认构建并发 | 1 |
| 实时日志 | SSE |
| 失败通知 | SMTP |
| 前端发布 | Vite 构建 + Go embed |
| 最小部署环境 | 单台本地 Linux 服务器 |
| 第一版范围 | 轻量 Compose 管理 + Git 自动更新 |

# 20. 最终交付要求

开发完成后应提供：

1. 完整 Go + Gin 后端源码。
2. 完整 React + TypeScript 前端源码。
3. SQLite 初始化与迁移脚本。
4. 一键构建脚本。
5. systemd 服务配置示例。
6. 管理员初始化流程。
7. 项目导入和 Git 绑定功能。
8. Docker Compose 启停和构建管理功能。
9. Git 定时轮询和任务队列。
10. 实时日志与构建历史。
11. SMTP 邮件通知。
12. README、配置说明和安装文档。
13. 核心模块测试和基础端到端测试。

**项目最终目标：**

构建一个可以部署在本地 Linux 服务器上的轻量级 Docker Compose 管理平台，让用户通过类似 Docker Desktop 的 Web 界面管理现有服务，并使用 Git 手动或定时更新项目，实现自动构建、部署日志记录与失败邮件通知，同时尽量降低内存占用和维护成本。

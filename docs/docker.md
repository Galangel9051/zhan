# Docker 构建与部署

镜像构建包含 React 前端、Go 后端和嵌入页面，运行时包含 Docker CLI、Compose、Buildx、Git、SSH 和根证书。不需要在宿主机安装 Go 或 Node，也不需要 Docker-in-Docker。

## 启动

在仓库根目录执行：

```sh
./scripts/docker-up.sh
# 或 make docker-up
```

访问 http://127.0.0.1:12345。首次安装使用 `admin / 123456`，之后必须改密。已有 `data/` 会沿用账户、项目、SQLite 数据库和密钥。若此前通过二进制启动，请先停止旧进程：同一数据目录不能同时运行两个管理实例，端口也不能重复占用。

脚本默认挂载仓库的**上一级目录**，因此同级项目可以继续直接选择。例如仓库为 `/Users/you/dev/docker-cicd`，将挂载 `/Users/you/dev` 到容器里的 `/Users/you/dev`。默认数据目录为仓库的 `data/`，也以同一路径挂载，避免受管 Nginx 在宿主机找不到其配置和证书。

```sh
# 在运行本命令的 shell 中设置；后续管理命令保持相同设置。
export ZHAN_HOST_PROJECTS_ROOT=/opt/projects
export ZHAN_DATA_DIR=/opt/zhan/data
./scripts/docker-up.sh
```

路径不必在本仓库内，但目录需要存在并可由 Docker 访问。脚本创建数据目录，并解析路径的符号链接。`ZHAN_DATA_DIR` 与本地运行共用；兼容旧变量 `ZHAN_HOST_DATA_DIR`，同时设置时必须一致，否则拒绝启动。直接运行 `docker compose` 时，需要自行设置 `ZHAN_HOST_PROJECTS_ROOT` 和 `ZHAN_HOST_DATA_DIR` 两个绝对路径变量；推荐使用脚本自动处理。

## 如何管理宿主机的容器与挂载

- 挂载 `/var/run/docker.sock`：管理工具调用宿主机 Docker，看到的是已有容器、网络和数据卷。无需重新创建或迁移已有业务容器。
- 挂载项目目录：同一路径双向映射，允许 Compose 读文件和 Git 更新。项目里的相对 bind mount 依旧根据原始项目目录解析，已有命名数据卷继续由宿主机 Docker 管理。
- 挂载数据目录：SQLite、会话、加密密钥、日志、Nginx 配置和证书持久化。新 Nginx 是宿主机上的独立兄弟容器，其 bind mount 能找到管理工具写出的文件。
- 容器不会自动发现未挂载的宿主机文件夹。如果项目不在默认上级目录内，可将 `ZHAN_HOST_PROJECTS_ROOT` 改为共同父目录，或在 `compose.override.yaml` 给 manager 增加额外的**同路径**挂载，例如 `/srv/apps:/srv/apps`。项目引用的外部 env_file、配置、build context 和符号链接目标也必须以同路径挂载。
- Docker bind mount 的源目录由 Docker daemon 所在宿主机解析，不是管理容器。因此不要把宿主机 `/opt/projects` 映射为 `/projects`，否则受管容器的挂载会指向错误的宿主机目录。参考 [Docker bind mount 文档](https://docs.docker.com/engine/storage/bind-mounts/)。

Docker socket 赋予管理容器对宿主机 Docker 的完整管理权限，运行用户因此默认为 root。默认只将管理页面发布到本机；不要将未改密的实例开放给外部。服务不需要 `privileged`、宿主机 PID 命名空间或挂载整个根目录。

## 网络与 HTTPS

管理页面默认 `127.0.0.1:12345`。需要局域网访问时：

```sh
ZHAN_BIND_IP=0.0.0.0 ./scripts/docker-up.sh
```

`ZHAN_PORT` 可调整宿主机发布端口，容器内部始终监听 12345。Nginx 仍在「反向代理」页面配置自己的监听 IP、HTTP/HTTPS 端口，直接发布在 Docker 宿主机上。反向代理本机服务使用 `host.docker.internal:端口`；访问局域网用该服务的实际 IP。Linux 中仅监听宿主机 127.0.0.1 的服务通常需调整监听地址，才能从 Docker bridge 网关访问。

Mac Docker Desktop 需要允许共享相应宿主机文件夹；Linux rootless Docker 的 socket 可以通过 `ZHAN_DOCKER_SOCKET=/run/user/1000/docker.sock` 指定。当前方案面向本机 Docker Engine / Docker Desktop，未配置远程 Docker 主机的路径转换。

## Git 与 SSH

Git HTTPS 凭据、SSH 身份不会自动从宿主机带入。需要通过 SSH 更新的项目可在 `compose.override.yaml` 为 app 挂载专用部署私钥与已验证的 known_hosts，例如：

```yaml
services:
  app:
    volumes:
      - /opt/zhan-ssh:/root/.ssh:ro
```

确保该目录只包含所需部署身份，配置的私钥权限正确，容器内可找到 SSH config 中引用的文件。Git 仍使用 BatchMode 和严格主机校验。每次 Git 调用只为已选中的工作目录设置 safe.directory，兼容容器与宿主机 UID 不同，不全局信任所有仓库。

## 维护

```sh
./scripts/docker-up.sh logs -f app
./scripts/docker-up.sh ps
./scripts/docker-up.sh restart app
./scripts/docker-up.sh down
./scripts/docker-up.sh up -d --build --wait
```

停止管理容器不会停止已有业务容器或独立 Nginx 网关；自动部署、DNS 更新和证书续期随管理服务停止。`down` 保留宿主机数据目录。备份整个数据目录，包括 `secret.key`，不要把运行数据、Token 或私钥写入镜像。

构建使用 `.dockerignore` 排除 Git、本地配置、数据、数据库、密钥、依赖与构建输出。镜像启动自带健康检查，启动脚本默认等待健康状态。

## 挂载集成检查

构建镜像后可运行：

```sh
python3 scripts/check-docker.py
```

此检查创建随机命名的临时管理容器、临时业务容器和独立数据目录。验证 Docker/Compose/Buildx、项目发现与导入、相对目录挂载、命名数据卷持久化、通过管理 API 停止/启动、独立 Nginx 配置挂载及实际 HTTP 转发、管理容器重启后保留账户和项目。结束时只清理测试自身的容器、网络和数据卷，不修改现有业务项目。

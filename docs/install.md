# Linux 宿主机安装

栈长直接运行在 Linux 上，使用宿主机的 Docker、Compose 插件和 Git，不需要 Node.js 常驻或独立数据库。

## 1. 构建

构建环境需要 Go 1.26+、Node.js 24+。在仓库根目录运行：

```sh
nvm install
nvm use
./scripts/build.sh
```

结果为 `bin/zhan`，包含前端静态文件，SQLite 驱动为纯 Go。数据默认固定在仓库的 `data/`；复制到 `/opt/zhan/zhan` 后默认固定在 `/opt/zhan/data/`，从其他目录启动也不会另建一份。Linux 上原生构建即可；macOS 交叉编译 Linux 示例：

```sh
# 先完成 ./scripts/build.sh，再选择目标架构。
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o ../bin/zhan-linux-amd64 ./cmd/server
```

ARM64 服务器将 `GOARCH` 改为 `arm64`。平台目前支持 Linux、macOS；进程锁和子进程组取消实现依赖 Unix。

## 2. 账户与目录

由服务器管理员创建专用 `zhan` 账户，将其加入 Docker 组，并赋予受管项目所需的读写权限。Docker 组本身具有很高的宿主机权限，务必仅授予可信管理员管理平台访问权。

```sh
sudo useradd --system --create-home --shell /usr/sbin/nologin zhan
sudo usermod -aG docker zhan
sudo mkdir -p /opt/zhan/data
sudo install -m 0755 bin/zhan /opt/zhan/zhan
sudo cp configs/config.example.yaml /opt/zhan/config.yaml
sudo chown -R zhan:zhan /opt/zhan
```

默认 `allowed_roots: []` 不限定目录，导入窗口可浏览选择服务账户有权限访问的任意目录和 Compose 文件。若管理员显式填写目录列表，则按该列表校验，符号链接解析后也必须符合配置。Compose 覆盖文件可位于项目目录之外。

平台不会自动提升系统权限、修改项目权限或安装 systemd 服务。

## 3. Git 身份与 SSH

以服务账户配置每个 Git 仓库的最小权限 SSH Deploy Key、SSH config 和已验证的 `known_hosts`。平台采用 BatchMode 与 StrictHostKeyChecking，不弹出认证提示，不跳过主机身份验证。

确保服务账户可访问 Docker 和仓库：

```sh
sudo -u zhan docker compose version
sudo -u zhan git -C /opt/projects/example status
sudo -u zhan git -C /opt/projects/example ls-remote origin refs/heads/main
```

界面绑定现有 `origin`，不自动克隆仓库、创建凭据、切换分支或修改 remote。不接受在 HTTP(S) 仓库地址内嵌密码或令牌；Git 自身的授权凭据助手也可由服务器管理员配置。

## 4. 首次登录与强制修改密码

服务首次启动时自动创建管理员：

- 用户名：`admin`
- 初始密码：`123456`

首次登录只显示密码设置页面。必须输入当前密码、新密码与确认密码；新密码至少 6 个字符，不能与当前密码相同。完成前，项目、任务、发现和设置接口均由后端拒绝访问。

修改成功后，所有旧会话立即失效，使用新密码重新登录即可进入管理后台。已有管理员账户不会被迁移或启动流程重置为默认密码。

可选的预初始化命令（通常不需要）：

```sh
sudo -u zhan /opt/zhan/zhan \
  --config /opt/zhan/config.yaml --init-admin
```

该命令仅创建同样需要首次改密的默认账户；不读取终端密码，不覆盖已有账户。

## 5. systemd

检查并调整 `configs/zhan.service` 的账户、安装目录和 Docker 组设置，然后安装：

```sh
sudo install -m 0644 configs/zhan.service /etc/systemd/system/zhan.service
sudo systemctl daemon-reload
sudo systemctl enable --now zhan
sudo systemctl status zhan
sudo journalctl -u zhan -f
```

默认仅监听 `127.0.0.1:12345`。通过 SSH 隧道访问，或放在可信 HTTPS 内网反向代理后；配置 `secure_cookies: true` 后 Cookie 仅能经 HTTPS 使用。反向代理需保持 `Host`，SSE 路径关闭缓冲并允许长连接：

```nginx
location / {
    proxy_pass http://127.0.0.1:12345;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_read_timeout 1h;
}
```

默认不信任转发的客户端地址；登录限流可能把同一反向代理的客户端视为同一来源，此为保守行为。

## 6. 导入项目

1. 在“项目与容器”中点击“发现 / 导入项目”。
2. 从 `docker compose ls --all` 返回的候选中选择，或手动填写。
3. 填写原 Compose Project Name，通过“选择目录”“选择文件”浏览服务器路径，或直接填写项目绝对路径与有序 Compose 文件列表。选择目录时会自动识别常见 Compose 文件，多选文件按选择顺序覆盖。
4. 填写或选择项目根目录后，自动读取本地分支及已获取的 origin 远程分支，下拉框默认选中当前分支。可选择“不绑定 Git”以仅管理 Compose。Git 功能需要配置 origin，工作区须已检出所选分支；如选择其他分支，请先在服务器切换，再点击“刷新分支”。分支识别不会 fetch 或修改工作区。
5. 导入仅验证配置并保存绑定，不执行 up、stop、restart、build 或 Git 更新。

导入后的运行状态来自 Docker 实际查询，不根据历史部署结果推测。无法确定的路径要求手动填写，不从容器标签臆测目录。

## 7. 备份与升级

停止服务后备份整个 `data/`，包含 `zhan.db`、`secret.key` 和日志。`secret.key` 丢失会导致已保存 SMTP 凭据无法解密；只复制数据库不足以完整恢复。

升级前备份数据，然后替换二进制、重启服务。迁移在启动时按版本事务执行，文件位于 `backend/internal/database/migrations/`。原待办示例的 `app.db` 不会被读取或删除。

进程异常退出后，遗留 Running 任务标记为中断取消，不会盲目重复执行；Pending 任务保留并在下次启动继续。请先检查实际服务状态，再手动重试中断任务。

## 8. 日志与资源

详细任务输出写到 `data/logs/task-ID.log`，每任务默认上限 20 MiB，单行最大 64 KiB，超限省略。每小时清理超过保留期限的已结束任务日志，历史索引继续保留。任务列表按 ID 游标分页，前端实时窗口最多保留约 300 KB。

常见令牌键值、带凭据 URL 和 Bearer 令牌会脱敏；构建脚本仍须避免主动打印任意未标识的秘密值。平台不能识别所有自定义输出格式。

内存目标来自设计文档，尚未在目标 Linux 服务器上完成 10–30 项目持续运行测试。Git、Docker 构建、镜像及容器本身的资源占用不计入平台内存目标。

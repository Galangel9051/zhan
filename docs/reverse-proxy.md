# 图形化 Nginx / HTTPS

在左侧「反向代理」管理域名转发。支持本机容器已发布的 TCP 端口，以及任意可达的局域网 HTTP / HTTPS 服务。每个域名可映射到不同主机和端口，支持 WebSocket、HTTPS、HTTP 跳转。

## 第一次使用

1. 打开「网关 / DuckDNS」。网关默认监听 `127.0.0.1:8088/8443`。供局域网使用时，监听地址改为网关局域网 IP 或 `0.0.0.0`，通常端口用 `80/443`。确保端口未被其他服务占用。
2. 在 [DuckDNS](https://www.duckdns.org/) 登录并注册免费主域名，例如 `myhome.duckdns.org`。工具使用更新 API，不能替你创建账户或抢注主域名。在配置页填入该域名和 Token。
3. 填写解析 IP：局域网使用填写**网关**的局域网 IP，如 `192.168.1.10`；留空由 DuckDNS 检测公网 IPv4。保存后点击「更新解析 IP」。可开启每 10 分钟更新，失败原因在执行记录查看。
4. 新增规则，例如 `app.myhome.duckdns.org → http://192.168.1.20:3000`、`nas.myhome.duckdns.org → http://192.168.1.30:5000`。编辑完整域名前缀就能更换子域名。所有子域名到达同一个网关，由 Nginx 再分发到对应端口。自有域名也可配置 HTTP 转发，需自行设置 DNS；当前自动签发证书仅支持配置的 DuckDNS 主域名及其子域名。
5. 如需 HTTPS，先在配置页填写联系邮箱并同意 Let's Encrypt 服务条款，然后在规则中开启 HTTPS 并保存，点击规则旁的「申请证书」。任务成功后点击「应用并启动」。进入证书最后三分之一有效期时自动续期；失败最多每天自动重试一次，也可手动重试。

DNS-01 验证不要求公网开放 80 端口，可用于局域网服务。但服务器需要连接 DuckDNS、Let's Encrypt 和公共 DNS（1.1.1.1 / 8.8.8.8）。域名仍需从访问设备解析并能到达网关；部分路由器的 DNS 重绑定保护会阻止公网域名解析到私网地址，需要在你管理的 DNS 中适当放行该域名。公网访问还需要公网可达地址、路由器端口映射或已有 VPN，不会自动穿透 NAT。

DuckDNS TXT 在同一主域名的子域名间共享，证书任务在工具内串行执行。请避免同时用其他证书客户端操作同一 DuckDNS 主域名。测试环境与正式环境的账户和证书分开存放；测试证书不被浏览器信任，切回正式环境后需要重新申请、应用。

## 容器与本机服务

编辑规则可切换「容器已发布端口」，从已有 Compose 项目中选取已发布的 TCP 端口；也可直接填写 `http://host.docker.internal:3000`。Nginx 运行在独立容器里，`localhost` 指向它自己，不能用来访问宿主机。Linux 通过 Docker `host-gateway` 访问宿主机，只监听宿主机 `127.0.0.1` 的上游需调整为可从 Docker 网关访问的监听地址。本机和局域网访问都应由上游自身执行身份验证。

上游填写 `http(s)://主机:端口`，不附带路径、查询参数、账户或任意 Nginx 指令。转发保留请求路径，设置 Host、客户端地址与协议头；HTTPS 上游启用证书验证和 SNI，需提供有效证书。

## 应用与运行

- 需要 Docker Engine / Docker Desktop 及 Compose v2，工具无需安装宿主机 Nginx、Certbot。首次使用会下载官方 `nginx:stable-alpine` 镜像。
- 配置保存在 SQLite，生成的配置、Compose 和证书放在 `data/proxy/`。DuckDNS Token 与 ACME 账户私钥加密保存，证书私钥使用仅属主可读的文件。备份整个数据目录及 `secret.key`，不要只备份数据库。
- 保存仅更新草稿。预览展示保存的 Nginx 配置，校验在一次性容器中运行 `nginx -t`。通过后才替换受管网关的配置并更新容器，应用会短暂中断代理连接。
- 更新启动失败时尝试恢复旧配置与容器；恢复也失败会在执行记录明确提示。首次启动失败会清理本次受管容器。
- 不读取或覆盖宿主机其他 Nginx 配置，不重启上游业务容器。停止网关后保留规则、证书与上游服务；网关容器有 Docker 自动重启策略。
- 签发/续期后校验并重载已经运行的网关，不会顺带应用尚未发布的规则。程序需持续运行以执行自动 DNS 更新和证书续期。
- 所有 API 经过现有登录、首次改密和 CSRF 校验；保存、执行结果写入审计。

## 接口与验证

- `GET /api/v1/proxy`：配置、证书有效期、运行状态、配置预览和最近 30 条记录，不返回 Token 或私钥。
- `PUT /api/v1/proxy/config`：保存 `{settings, hosts, revision}`，旧 revision 拒绝覆盖新配置。
- `POST /api/v1/proxy/jobs`：`{action, host_id}`，action 为 `test / apply / stop / dns / certificate`。返回 202，页面轮询执行结果。同一时刻只运行一个代理任务。

```sh
./scripts/check.sh
cd backend
# 独立临时网关：仅随机的本机端口、临时证书与临时上游，结束后清理。
CM_PROXY_DOCKER_TEST=1 go test -v ./internal/proxy -run TestManagedNginxHTTPAndHTTPS
```

测试包括上游/域名注入防护、配置版本冲突、凭据加密与不回显、失败任务、模拟完整 ACME DNS-01 签发及 TXT 清理、证书原子替换、校验失败不替换配置、启动失败恢复旧配置。真实 Docker 测试验证 HTTP、带域名验证的 HTTPS、转发头、非标准端口跳转、重载以及错误配置下原服务仍可访问。没有修改真实 DuckDNS 解析或签发真实证书；需配置实际账户后验收。

参考：[DuckDNS API](https://www.duckdns.org/spec.jsp)、[Let's Encrypt DNS-01](https://letsencrypt.org/docs/challenge-types/)、[Nginx 命令行](https://nginx.org/en/docs/switches.html)。

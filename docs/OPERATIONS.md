# 部署与运维

[返回 README](../README.md) · [English](OPERATIONS.en.md)

脚本自动拉取 `ghcr.io/nyakooo/meldmail:latest`，启动容器，等待最多 90 秒的 HTTP 健康检查，再清理本项目未被容器引用的悬空镜像。拉取失败时不会替换正在运行的容器；健康检查失败时不清理镜像。已有 `.env` 和 `data/` 不会被覆盖。

默认监听 `127.0.0.1:18085`，由宿主机 Nginx 提供外部 HTTPS 访问。容器内部仍使用 `8080`，不会直接占用宿主机的 8080。默认资源限制为 256MiB 内存、0.5 CPU。若 GHCR 镜像为私有，先通过 `docker login ghcr.io` 登录有读取权限的账号。

## 配置说明

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MELDMAIL_HTTP_PORT` | `18085` | 宿主机端口，例如改为 `54321`；更改后也要修改 Nginx 上游端口。 |
| `MELDMAIL_MASTER_KEY` | 必填 | 64 位十六进制加密密钥；复用已有数据时必须保持原值。 |
| `MELDMAIL_ADMIN_PASSWORD` | 首次启动必填 | 只初始化尚未设置密码的数据库，不会覆盖已有密码。 |
| `MELDMAIL_IMAGE` | `ghcr.io/nyakooo/meldmail:latest` | 可指定发布标签、`sha-<完整提交 SHA>` 或 digest。 |

默认 Compose 在 YAML 中固定了容器监听地址、数据库路径和代理信任设置：`MELDMAIL_ADDR=:8080`、`MELDMAIL_DB_PATH=/data/meldmail.db`、`MELDMAIL_TRUST_PROXY=true`。仅修改 `.env` 中这三个值不会覆盖默认 Compose；需要自定义时修改 Compose 或使用本地 override 文件。脚本会读取本地 `docker-compose.override.yml`。

## Nginx 与 HTTPS

完整示例见 [nginx.conf.example](../nginx.conf.example)。以下配置适用于 **HTTPS 由这台 Nginx 终结** 的场景；替换域名、证书路径和上游端口。证书需事先签发，可使用 Certbot。

<!-- nginx-example:start -->
```nginx
server {
    listen 80;
    server_name mail.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name mail.example.com;
    ssl_certificate /etc/letsencrypt/live/mail.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mail.example.com/privkey.pem;
    client_max_body_size 8m;

    location / {
        proxy_pass http://127.0.0.1:18085;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_read_timeout 86400s;
    }
}
```
<!-- nginx-example:end -->

`Host $http_host` 保留浏览器访问的域名及端口；`X-Forwarded-Proto $scheme` 告诉后端外部协议。后端会校验请求 Origin 是否与协议、Host 一致。关闭缓冲和缓存用于 SSE、MCP、正文及附件流式传输。不要通过清空 `Origin` 来绕过校验。

检查后重载：

```sh
sudo nginx -t && sudo systemctl reload nginx
```

如果 HTTPS 在 CDN 或其他前置代理终结，而这台 Nginx 接收 HTTP，`$scheme` 会是 `http`。此时需仅接受可信前置代理传递的外部协议并转发；本例不能原样套用。容器端口应保持仅本机可访问，避免客户端伪造代理头。

## 登录与密码排查

| 现象 | 排查方向 |
| --- | --- |
| 首页就返回 403 | 检查域名是否命中正确的 Nginx `server`、是否走 `proxy_pass`，以及访问限制或前置代理。 |
| 登录返回 `403 invalid origin` | 检查 `Host`、HTTPS 协议、外部自定义端口，以及是否有其他 `location` 覆盖了代理头。 |
| 登录返回 `401 invalid credentials` | 已到达应用认证；核对登录密码。它不是邮箱授权码，也不是 GHCR 登录密码。 |
| 修改 `.env` 后仍是旧密码 | 数据库已有密码；环境变量不会覆盖它。删除容器或镜像也不会删除挂载的 `data/`。 |
| 返回 502 或连接拒绝 | 检查容器健康状态、监听端口和 Nginx 的上游端口是否一致。 |

基础检查：

```sh
docker compose ps
curl -I http://127.0.0.1:18085/
docker compose logs --tail=50 meldmail
```

能登录时，在侧栏选择“修改登录密码”。修改后所有 Web 会话失效，需要重新登录。

### 忘记密码：保留邮箱数据

先在 `.env` 中设置新的 `MELDMAIL_ADMIN_PASSWORD`，**保持 `MELDMAIL_MASTER_KEY` 不变**。以下操作需要 Python 3，且在项目目录执行。它会备份数据库，只移除登录密码设置，不删除邮箱、缓存或 MCP 令牌。

```sh
(
set -eu
docker compose stop meldmail
python3 - <<'PY'
import sqlite3
import time
from pathlib import Path

path = Path('data/meldmail.db')
if not path.is_file():
    raise SystemExit('Database not found; check the project directory and volume mount')
backup = path.with_name(f'meldmail-password-backup-{time.time_ns()}.db')
backup.touch(mode=0o600, exist_ok=False)
with sqlite3.connect(f'file:{path}?mode=rw', uri=True) as db:
    with sqlite3.connect(backup) as target:
        db.backup(target)
    db.execute('DELETE FROM settings WHERE key = ?', ('admin_password',))
print(f'Backup: {backup}; sign-in password cleared')
PY
./deploy.sh
)
```

重启后使用 `.env` 中的新登录密码登录。备份包含邮箱凭据和已缓存的正文，应与原数据一样妥善保管。

### 全新初始化

只有确定不再保留当前邮箱配置、邮件缓存、MCP 令牌和审计记录时才使用。原邮箱服务商上的邮件不会被此操作删除。先改名备份，避免直接删除 `data/`：

```sh
(
set -eu
docker compose stop meldmail
mv data "data.backup.$(date +%Y%m%d-%H%M%S)"
mkdir -p data
./deploy.sh
)
```

新安装使用 `.env` 中的密码，邮箱需要重新添加。备份目录包含敏感数据，不要提交或公开。

## 配置导入与导出

打开“邮箱与连接 → 导入 / 导出”。源实例设置一个至少 12 个字符的独立配置密码，下载加密 JSON；在目标实例选择该文件，填写同一配置密码，然后点击“导入并合并”。目标实例使用自己的主密钥重新加密保存，无需复制原主密钥或重新填写仍有效的邮箱授权码。

配置包包括邮箱连接、现有授权码 / OAuth 令牌及代理，不包括邮件内容、登录密码或 MCP 令牌。相同邮箱按去首尾空格、忽略大小写匹配；重复导入更新原账号，保留已有邮件。包内重复项以最后一项连接设置为准，空凭据不覆盖前一项。校验失败不会写入部分配置；包内共用代理非空时会更新目标共用代理，仅影响选择共用代理的邮箱。

配置密码不保存到服务器；请妥善保存文件和密码。OAuth 令牌仍受服务商有效期限制，导入不会自动刷新或延长有效期。

侧栏邮箱数字跟随当前分类即时切换，并随邮件事件更新。连接异常可点击“待重连”查看对应设置与诊断。

## 添加邮箱与代理

登录后打开“邮箱与连接”，填写邮箱地址和授权码，先测试连接，再保存。Gmail 使用 Google 应用专用密码；普通 Google 账号密码不能替代它。Outlook 的 OAuth2 自动刷新仍是扩展接口。

每个邮箱独立选择连接方式，**新邮箱默认直接连接**。只有确实需要代理的邮箱，才在自己的设置中选择 HTTP、HTTPS 或 SOCKS5 并填写 URL。如果多个邮箱使用同一代理，可在“邮箱与连接 → 共用代理（可选）”保存地址，再由这些邮箱主动选择“使用共用代理”；直连和独立代理邮箱不会受共用设置变化影响。测试的网络路径是**容器到邮箱服务商**，不是浏览器到 Gmail 的路径。Docker 内的 `127.0.0.1` 指向容器自身，连接宿主机代理时需要容器可达的地址。

重新打开编辑时会回填已保存的完整代理 URL，可直接核对和修改，无需重新输入邮箱授权码。

- TLS 或连接重置错误发生在认证之前，不应直接归因于授权码。
- 宿主机连接成功而容器失败时，分别检查 DNS、IPv4/IPv6 和代理可达性。

## 阅读和管理邮件

- 正文中的 HTTP(S) 链接在新标签页打开。浅色主题会调整对比度不足的链接文字。深色主题可通过「显示原始配色」查看发件人配色。
- 归档和废纸篓中的邮件可以恢复到收件箱。打开废纸篓中的邮件可「彻底删除」，选择邮箱后可「清空废纸篓」。确认后会从该邮箱的服务器永久删除邮件，清空也包括尚未同步到本地的邮件。此功能需要邮箱服务器支持定向永久删除。
- 在「设置 → AI 与 MCP → 访问令牌」中可撤销令牌。删除已撤销的令牌时会同时删除其审批记录，审计日志会保留。

## 宿主机代理端口（Linux，可选）

如果 Mihomo/Clash 的 HTTP 或混合端口监听在部署服务器的 `127.0.0.1:7890`，在 `.env` 中设置：

```dotenv
MELDMAIL_HOST_PROXY_PORT=7890
```

运行 `./deploy.sh`，脚本会自动叠加 `docker-compose.host-proxy.yml`，切换为 host 网络，让容器访问宿主机回环代理。网页仍仅监听 `127.0.0.1:${MELDMAIL_HTTP_PORT:-18085}`，Nginx 配置不变；无需公开代理端口或开启允许局域网连接。此端口必须属于部署服务器，不能填浏览器所在电脑的本地端口。

需要代理的邮箱选择“使用共用代理”，不需要的继续“直接连接”。该选项不强制覆盖独立邮箱代理。每次应用启动，环境中的端口优先更新数据库里的共用代理；值相同时不重复写入。启用期间网页显示配置来源并只读。清空该变量并重新部署，会清除之前由环境生成的共用代理；从未启用时，不影响网页手动保存的共用代理。登录密码和主密钥不参与同步。

修改 `.env` 后执行 `./deploy.sh` 重新创建容器，单独 `docker restart` 不会读取更新的 `.env`。源码构建时手动叠加：

```sh
docker compose -f docker-compose.yml -f docker-compose.host-proxy.yml up -d --build
```

未配置端口时，默认 Compose 继续使用桥接网络。host 模式下 `docker compose ps` 不显示端口映射，直接访问原来的宿主机端口即可。此方案面向 Linux Docker Engine，并要求 Compose 支持 `!reset`；若提示未知标签，请升级 Compose 插件。

## 版本发布

打标签前，在 `releases/<标签>.md` 编写面向用户的发布说明，并将文件提交到待打标签的版本。把修订后的说明推送到 `master`，也会更新已有的 GitHub Release。

推送 `v*` 标签后，Actions 在 GHCR 镜像推送成功后创建对应的 GitHub Release。若旧标签已有镜像但缺少 Release 页面，在 Actions 中手动运行 **Publish GHCR image and release**，选择 `master` 并填写 `release_tag`（如 `v0.1.0`）。工作流会先检查对应的 GHCR 镜像。

## 更新、回滚与清理

更新前确认对应 GHCR 工作流已成功，再在服务器项目目录执行：

```sh
git pull --ff-only
./deploy.sh
```

需要回滚时，在 `.env` 设置 `MELDMAIL_IMAGE=ghcr.io/nyakooo/meldmail:sha-<完整提交 SHA>` 或已知 digest，再执行 `./deploy.sh`。这只切换镜像，不回滚数据库；如有不兼容的数据迁移，应使用对应备份。

只清理本项目悬空镜像：

```sh
./deploy.sh --cleanup-only
```

脚本保留其他项目镜像、被容器引用的镜像和构建缓存，不使用全局 `docker system prune`。修改文档或宿主机端口映射本身不需要重建应用镜像；当前工作流仍会在推送 `master` 时触发镜像构建。

## 数据与备份

`./data` 保存 SQLite 数据库、WAL/SHM 文件、加密邮箱凭据、元数据、已读正文缓存和设置。正文缓存上限为 100MiB / 1000 封，单封正文不超过 5MiB；附件内容、原始 MIME 和未发送审批正文不落盘。

备份时停止容器，将 `data/` 和 `.env` 一起复制到受保护的位置，再启动容器。保留原主密钥；单独复制运行中的 `.db` 可能遗漏 WAL 中的数据。未发送的审批正文仅在内存中，重启后需要重新提交。

## 诊断日志（保留 7 天）

在「设置 → 诊断日志」中可查看日志。右上角的图标可最大化或还原窗口。

程序按 UTC 日期写入 JSON 日志，默认目录为数据库旁的 `logs/`。默认 Docker 部署对应宿主机 `./data/logs/meldmail-YYYY-MM-DD.jsonl`，随现有数据卷持久化；同时输出到 `docker logs meldmail`。保留当天及前 6 天，在启动、跨日写入及每小时检查时清理过期日志。只清理该目录下符合命名规则的日志文件，不需要额外服务。

记录后台同步、IDLE 建立与续接、断开与重试模式、账号 ID、连接方式、耗时和安全错误分类。`account_id` 可与邮箱列表 API 的账号 ID 对应；`stage` 标明操作阶段，`reason` 区分 EOF、超时、连接重置等，IMAP 拒绝响应只记录受限的状态及错误代码。不会记录密码、授权码、Token、主密钥、代理 URL、邮件正文或服务端原始错误文本。

```bash
# 查看最新诊断
 tail -n 100 ./data/logs/meldmail-*.jsonl
# 按账号筛选（示例账号 ID 为 1）
 grep '"account_id":1[,}]' ./data/logs/meldmail-*.jsonl
```

独立运行可用 `MELDMAIL_LOG_DIR` 自定义目录。默认 Compose 已自动持久化日志；如需更改容器中的目录，请在 Compose 中传入该变量并挂载对应目录。更新镜像并重新创建容器后，这套日志才会开始记录；此前的错误无法追溯。

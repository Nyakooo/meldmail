<p align="center">
  <img src="assets/brand/meldmail-mark.svg" alt="MeldMail Logo" width="72" height="45">
</p>
<h1 align="center">MeldMail</h1>
<p align="center">自托管多邮箱客户端 · 每个邮箱独立配置代理 · 单容器部署</p>
<p align="center">
  <strong>简体中文</strong> · <a href="README.en.md">English</a>
  <br>
  <a href="#快速部署">快速部署</a> · <a href="docs/OPERATIONS.md">部署与运维</a>
</p>

**每个邮箱可以走不同的网络。** 例如，Gmail 走 SOCKS5 代理，QQ 邮箱直连，工作邮箱走另一条 HTTP(S) CONNECT 代理。新邮箱默认直连，也可按需选择共用代理。[代理配置说明](docs/OPERATIONS.md#添加邮箱与代理)

![MeldMail 桌面端明暗主题预览](docs/design/desktop-light-dark.svg)

桌面端明暗主题设计预览，具体功能以当前实现为准。[设计说明](docs/DESIGN.md)

## 功能

- 统一收件箱与搜索，支持后台 IMAP 同步和实时更新。
- 阅读、发送、回复、转发和流式附件下载。正文链接在新标签页打开。
- 支持归档、恢复、单封彻底删除和按邮箱清空废纸篓。
- MCP 提供可撤销的访问令牌、发信审批和审计日志。
- 支持中英文界面、明暗主题和移动端布局。

## 快速部署

需要 Docker Engine、Docker Compose v2.20+、Git 和 OpenSSL。下面命令在服务器上执行。

```sh
git clone https://github.com/Nyakooo/meldmail.git
cd meldmail
[ -f .env ] || cp .env.example .env
chmod 600 .env
openssl rand -hex 32
```

编辑 `.env`，将生成的密钥填入 `MELDMAIL_MASTER_KEY`，并设置登录密码：

```dotenv
MELDMAIL_HTTP_PORT=18085
MELDMAIL_MASTER_KEY=填入刚生成的64位十六进制密钥
MELDMAIL_ADMIN_PASSWORD='设置一个强密码'
# MELDMAIL_HOST_PROXY_PORT=7890
```

`MELDMAIL_HOST_PROXY_PORT` 是可选项，用于 Linux 部署服务器本机的 HTTP 或混合代理端口。只有选择「使用共用代理」的邮箱才会使用它。每个邮箱的独立代理在网页里配置。[宿主机代理配置](docs/OPERATIONS.md#宿主机代理端口linux可选)

密码最多 **72 字节**，不是 72 个汉字。用单引号包住密码，可避免 `$`、`#` 等字符被 Compose 插值或解释为注释。不要将 `.env` 加入 Git。

确认 [GHCR 发布工作流](https://github.com/Nyakooo/meldmail/actions/workflows/ghcr.yml)成功后执行：

```sh
./deploy.sh
```

服务默认监听 `127.0.0.1:18085`。对外提供 HTTPS、更新或备份时，请参阅运维说明。

## 本地开发

需要 Go 1.24+、Node.js 20.19+ 和 npm。先构建前端，再编译 Go：

```sh
npm ci --prefix web
npm run build --prefix web
# 在 shell 中单独设置主密钥及首次登录密码；go run 不会自动读取 .env。
go run ./cmd/meldmail
```

源码 Docker 构建使用 `docker compose up --build -d`。测试按 [AGENTS.md](AGENTS.md) 增量执行；最终验收及底层公共模块变更才运行全量 `go test -race ./...`。

## 文档

- [配置、Nginx、排障、更新和备份](docs/OPERATIONS.md)
- [API 与开发文档（英文）](docs/DEVELOPMENT.en.md) · [架构规范](SPEC.md) · [验收记录](docs/ACCEPTANCE.md)
- [Apache-2.0 许可证](LICENSE) · [第三方依赖声明](THIRD_PARTY_NOTICES.md)

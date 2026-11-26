# MeldMail 产品与技术架构全量规范（SPEC.md）

> **致 AI 编程引擎（Codex）的执行指令**：
> 1. 本文档是 MeldMail 项目的唯一权威设计基准（Gold Standard）。请严格按 **Milestone 0 → Milestone 6** 的顺序推进开发。
> 2. **阶段门禁**：每完成一个 Milestone，必须编写并运行单元测试与集成验收命令，全部通过后方可进入下一阶段。
> 3. **严禁越界**：严禁引入 Redis/Kafka/RabbitMQ/独立数据库；严禁违反 256MB 内存红线；完整正文仅允许按第 4.3 节的阅读缓存规则写入 SQLite；原始 MIME 和附件仍不得落盘。

---

## 1. 项目定位与硬性红线

- **产品定位**：轻量、自托管、单进程的多邮箱聚合 Web 客户端 + 标准 MCP Server。将 Gmail、Outlook、163、QQ 等 5～10 个主流邮箱整合到一个响应式 Web 界面中，支持后台持续同步、毫秒级搜索、流畅收发、独立代理连接，并向外部 AI Agent 开放受控邮件能力。
- **技术栈锁定**：
  - **后端**：Go 1.22+（单二进制，通过 `embed.FS` 内嵌前端构建产物）。
  - **前端**：Vue 3 + TypeScript + Vite（PC / 平板 / 手机自适应）。
  - **数据库**：SQLite（`modernc.org/sqlite` 纯 Go 驱动或 CGO，强制开启 `WAL` + `FTS5`）。
  - **通信**：REST API + SSE（`/api/events`）+ MCP（Streamable HTTP `/mcp`）。
- **部署红线**：
  - **单 Docker 容器**：仅 1 个容器，通过 `docker-compose.yml` 部署与升级。
  - **纯 HTTP 监听**：容器内仅监听 HTTP（默认 `:8080`），宿主机默认绑定 `127.0.0.1:18085`（通过 `MELDMAIL_HTTP_PORT` 自定义），由用户宿主机 Nginx + Certbot 终结 HTTPS。
- **资源红线（2C2G 宿主机共享环境）**：
  - Docker 容器硬限制：`mem_limit: 256m`，`cpus: 0.5`。
  - Go 运行时强制环境变量：`GOMEMLIMIT=200MiB`，`GOGC=50`。
  - 内存目标：空闲 60～120MB；读取大邮件/下载 20MB 附件时峰值严禁超过 180MB。
- **存储边界（2026-11-26 按用户要求修订）**：列表同步仅保存元数据与最多 **500 字符**摘要。按需阅读或后台预取成功后，将经清洗的 HTML 或解码后的纯文本正文及附件元信息保存到 SQLite，供后续优先本地读取。正文总量上限 **100MiB / 1000 封**，单封正文 ≤ **5MiB**；附件元信息单封 ≤ **256KiB**。原始 MIME、附件内容和未发送的审批正文仍不得落盘。
- **开源合规**：Apache License 2.0。所有核心代码独立实现，严禁复制 GPL/LGPL/AGPL 协议邮件项目代码。

---

## 2. 仓库目录结构

```text
meldmail/
├── cmd/meldmail/main.go            # 入口：配置加载、DB迁移、Worker启动、HTTP/SSE/MCP服务、优雅退出
├── internal/
│   ├── config/                     # 环境变量解析（MELDMAIL_ADDR, DB_PATH, MASTER_KEY, TRUST_PROXY等）
│   ├── crypto/                     # AES-256-GCM 凭据加解密
│   ├── storage/                    # SQLite 初始化、PRAGMA、Migration、CRUD、FTS5 检索
│   ├── proxy/                      # 统一 ProxyDialer（Direct / HTTP CONNECT / HTTPS / SOCKS5）
│   ├── mail/
│   │   ├── auth/                   # Authenticator 接口（Password/授权码 + 预留 XOAUTH2）
│   │   ├── adapter/                # 邮箱协议适配（163 IMAP ID、QQ Re-IDLE、Gmail、Outlook）
│   │   ├── imapclient/             # go-imap v2 封装（两段式拉取、BODYSTRUCTURE 流式解析）
│   │   ├── smtpclient/             # go-mail 封装（复用 ProxyDialer 发送/回复/转发）
│   │   └── syncer/                 # 后台 Worker 管理器（IDLE + Polling + 退避重连 + 并发信号量）
│   ├── sse/                        # SSE Broker（多端推送新邮件、状态变更、MCP 审批通知）
│   ├── mcp/                        # MCP Server（Streamable HTTP、Token 鉴权、审批流、审计日志）
│   └── api/                        # REST API 路由、登录鉴权中间件、Handler、embed.FS 静态托管
├── web/                            # Vue 3 + TypeScript + Vite 前端工程
├── migrations/001_init.sql         # SQLite 初始元数据模型
├── migrations/002_email_bodies.sql # 按需阅读的本地正文缓存
├── Dockerfile                      # 多阶段构建（Node -> Go -> Alpine）
├── docker-compose.yml              # 单容器生产配置（含 256MB / 0.5 CPU 限制）
├── LICENSE                         # Apache License 2.0
├── README.md                       # 部署、Nginx 反代与使用文档
└── SPEC.md                         # 本文档
```

---

## 3. 数据库与安全设计（SQLite）

### 3.1 连接初始化参数（每次打开 DB 必须执行）
```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA cache_size = -16000;      -- 限制页缓存上限为 16MB
PRAGMA temp_store = MEMORY;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
```

### 3.2 完整建表脚本（`migrations/001_init.sql`）
```sql
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL,                     -- gmail | 163 | qq | outlook | custom
    auth_type TEXT NOT NULL DEFAULT 'password', -- password | oauth2
    imap_host TEXT NOT NULL,
    imap_port INTEGER NOT NULL DEFAULT 993,
    imap_tls TEXT NOT NULL DEFAULT 'implicit',  -- implicit | starttls
    smtp_host TEXT NOT NULL,
    smtp_port INTEGER NOT NULL DEFAULT 465,
    smtp_tls TEXT NOT NULL DEFAULT 'implicit',  -- implicit | starttls
    credentials_enc BLOB NOT NULL,              -- AES-256-GCM 加密的 JSON
    proxy_mode TEXT NOT NULL DEFAULT 'direct',  -- global | direct | http | https | socks5
    proxy_url_enc BLOB,                         -- AES-256-GCM 加密的代理 URL
    status TEXT NOT NULL DEFAULT 'active',      -- active | error | paused
    last_error TEXT NOT NULL DEFAULT '',
    last_sync_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS folders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'other',         -- inbox | sent | archive | trash | drafts | other
    uid_validity INTEGER NOT NULL DEFAULT 0,
    last_uid INTEGER NOT NULL DEFAULT 0,
    unread_count INTEGER NOT NULL DEFAULT 0,
    UNIQUE(account_id, name)
);

CREATE TABLE IF NOT EXISTS emails (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    uid INTEGER NOT NULL,
    message_id TEXT NOT NULL DEFAULT '',
    in_reply_to TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    from_name TEXT NOT NULL DEFAULT '',
    from_addr TEXT NOT NULL DEFAULT '',
    to_addrs TEXT NOT NULL DEFAULT '[]',
    cc_addrs TEXT NOT NULL DEFAULT '[]',
    snippet TEXT NOT NULL DEFAULT '',           -- 纯文本摘要，最多 500 字符
    has_attachments INTEGER NOT NULL DEFAULT 0,
    is_read INTEGER NOT NULL DEFAULT 0,
    is_starred INTEGER NOT NULL DEFAULT 0,
    date INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(account_id, folder_id, uid)
);

CREATE INDEX IF NOT EXISTS idx_emails_date ON emails(date DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_emails_account_date ON emails(account_id, date DESC);
CREATE INDEX IF NOT EXISTS idx_emails_unread ON emails(is_read, date DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS emails_fts USING fts5(
    subject, from_name, from_addr, snippet,
    content='emails', content_rowid='id', tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS emails_ai AFTER INSERT ON emails BEGIN
    INSERT INTO emails_fts(rowid, subject, from_name, from_addr, snippet)
    VALUES (new.id, new.subject, new.from_name, new.from_addr, new.snippet);
END;

CREATE TRIGGER IF NOT EXISTS emails_ad AFTER DELETE ON emails BEGIN
    INSERT INTO emails_fts(emails_fts, rowid, subject, from_name, from_addr, snippet)
    VALUES ('delete', old.id, old.subject, old.from_name, old.from_addr, old.snippet);
END;

CREATE TRIGGER IF NOT EXISTS emails_au AFTER UPDATE OF subject, from_name, from_addr, snippet ON emails BEGIN
    INSERT INTO emails_fts(emails_fts, rowid, subject, from_name, from_addr, snippet)
    VALUES ('delete', old.id, old.subject, old.from_name, old.from_addr, old.snippet);
    INSERT INTO emails_fts(rowid, subject, from_name, from_addr, snippet)
    VALUES (new.id, new.subject, new.from_name, new.from_addr, new.snippet);
END;

CREATE TABLE IF NOT EXISTS mcp_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,            -- SHA-256
    scope TEXT NOT NULL DEFAULT 'readonly',     -- readonly | readwrite
    require_approval INTEGER NOT NULL DEFAULT 1,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_used_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS pending_actions (
    id TEXT PRIMARY KEY,
    token_id INTEGER NOT NULL REFERENCES mcp_tokens(id) ON DELETE CASCADE,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    action_type TEXT NOT NULL,                  -- send | reply | forward
    payload_json TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',     -- pending | approved | rejected | executed | failed
    error_msg TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token_id INTEGER,
    tool_name TEXT NOT NULL,
    args_summary TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,                       -- success | denied | pending | error
    created_at INTEGER NOT NULL
);
```

### 3.2.1 阅读缓存扩展

`migrations/002_email_bodies.sql` 定义 `email_bodies`：以 `email_id` 外键关联邮件，存储来源指纹 `source_key`、清洗后的 `html`、纯文本 `text`、`attachments_json`（仅元信息）、有效负载字节数 `size_bytes`、写入时间 `stored_at` 与访问时间 `last_access`。保留最近访问的正文，超出总字节或条目上限时淘汰旧缓存；总量限制针对内容字节数，SQLite 页、WAL 与索引另有开销。

删除邮件或账号、UIDVALIDITY 重置时级联清理；邮件移动或账号邮箱地址/IMAP 来源变化时使对应缓存失效。来源指纹还包含清洗策略版本，策略升级时应提升版本。正文缓存不进入 FTS；搜索仍仅检索元数据和摘要。

### 3.3 安全规范
- **凭据加密**：通过环境变量 `MELDMAIL_MASTER_KEY`（64 位 Hex / 32 字节）使用 `AES-256-GCM` 加密 `credentials_enc` 与 `proxy_url_enc`，密文格式 `nonce(12B) || ciphertext`。未设置时启动报错退出。
- **Web 认证**：单用户管理员登录（首次启动通过环境变量 `MELDMAIL_ADMIN_PASSWORD` 或 Web 初始化向导设置 bcrypt 哈希存入 `settings`），使用 `HttpOnly` + `SameSite=Lax` Session Cookie；当 `MELDMAIL_TRUST_PROXY=true` 且 `X-Forwarded-Proto: https` 时自动启用 `Secure` 标志。登录接口增加简单内存级速率限制（防暴力破解）。

---

## 4. 核心引擎设计

### 4.1 统一代理层（`internal/proxy`）
封装统一接口 `DialContext(ctx context.Context, network, addr string) (net.Conn, error)`，支持：
- `direct`：标准 `net.Dialer`（15s 超时，30s KeepAlive）。
- `socks5`：`golang.org/x/net/proxy`，支持用户名/密码认证。
- `http`：TCP 连接代理后发送 `CONNECT target:port HTTP/1.1`（含可选 `Proxy-Authorization: Basic`），校验 `200` 后返回 `net.Conn`。
- `https`：先与代理建立 TLS 连接，再在 TLS 内执行 `CONNECT` 握手。
- `global`：仅显式选择共用代理的邮箱读取 `settings` 中的共用代理配置，未配置则回退 `direct`。新邮箱与省略连接方式的创建请求默认 `direct`；修改共用代理不得重启或改变直连及独立代理邮箱。
- **强制要求**：`go-imap`（收）与 `go-mail`（发）均必须通过此 `Dialer` 建立底层连接后再执行隐式 TLS 或 STARTTLS。

### 4.2 主流邮箱适配（`internal/mail/adapter`）
- **163 / 126**：IMAP `LOGIN` 成功后必须立即发送 RFC 2971 `ID ("name" "MeldMail" "version" "1.0" "vendor" "MeldMail")`。
- **QQ 邮箱**：IDLE 长连接每 **12 分钟** 主动退出并重新进入 IDLE（Re-IDLE），防止被服务端静默掐断。
- **Gmail**：一期支持应用专用密码，预留 `Authenticator` 接口用于二期 OAuth2；归档映射为从 `INBOX` 移除标签。
- **Outlook**：预留 `XOAUTH2` 认证接口与 Refresh Token 刷新钩子。

### 4.3 两段式流式邮件引擎（防 OOM 核心）
1. **并发控制**：全局信号量 `make(chan struct{}, 3)`，最多允许 3 个账号同时执行拉取同步。
2. **连接隔离**：每个账号维护 1 条常驻 `IDLE` 连接只盯 `INBOX`；用户点开正文、下载附件、修改状态均通过独立短连接执行，**绝不阻塞 IDLE**。
3. **阶段 1（后台同步）**：
   - `UIDVALIDITY` 变更时清空该文件夹本地记录并重置 `last_uid = 0`。
   - 首次同步默认拉取最近 **100 封**；增量同步仅拉取 `UID > last_uid`。
   - 单封仅拉取 `UID`、`FLAGS`、`INTERNALDATE`、`ENVELOPE`、`BODYSTRUCTURE` 及 **`BODY.PEEK[TEXT]<0.4096>`**（局部拉取前 4KB，解码清洗后截断至 **500 字符** 存入 `snippet`）。
   - 每 **45 分钟** 执行一次轻量状态校验（同步已读/删除）；断线采用指数退避重连（5s -> 15s -> 30s -> 60s），连续失败 5 次降级为 3 分钟 Polling。
4. **阶段 2（正文预取、按需阅读与流式下载）**：
   - **后台正文预取**：列表同步成功后唤醒预取，服务启动及运行期间定期补查。候选为启用账号收件箱中最近的未读邮件，优先代理邮箱，每轮检查最多 16 封候选且全局串行处理。仅在内存用量低于预算的 50% 时开始下一封，预算不超过容器限制及 256MiB，非容器环境按进程内存评估。前台正文请求取消正在进行的预取并优先获得正文内存槽。预取复用按需阅读的 IMAP PEEK、清洗、缓存容量和失效规则，不改变已读状态。预取失败由后续轮次重试，网页按需阅读仍可用。已认证网页通过 `/api/events` 实时显示后台列表同步与正在预取的邮箱、邮件主题，任务结束后显示空闲。
   - **正文阅读**：优先读取来源指纹匹配的 SQLite 正文缓存，命中时不建立 IMAP 连接，重启后仍可复用；未命中时拉取 `BODYSTRUCTURE`，定位 `text/html`（优先）和 `text/plain` 的 `PartID`，定向执行 `BODY.PEEK[<PartID>]`（单封正文硬上限 **5MB**）。服务端使用 `bluemonday` 清洗 HTML（防 XSS）后保存缓存并返回前端。保留通过属性白名单校验的行内 CSS 和普通选择器样式（最多 128KiB），禁用脚本、外部样式、网络型 CSS 与远程图片；清洗策略变化时提升正文缓存版本。缓存写入失败不阻止本次阅读，返回固定的 `cache_warning` 提示；不记录正文或数据库错误细节。正文读取与 HTTP 输出共同持有内存限额，慢客户端不能积压完整正文。
   - **附件下载**：根据 `PartID` 定向拉取附件流（`io.Reader`），实时解码并直接 `io.Copy` 写入 HTTP `ResponseWriter`，**严禁 `io.ReadAll` 读入内存**。

---

## 5. 接口契约（REST + SSE + MCP）

### 5.1 REST API & SSE
- `POST /api/auth/login` / `POST /api/auth/logout` / `GET /api/auth/me`
- `GET/POST/PUT/DELETE /api/accounts` 及 `POST /api/accounts/test`（测试代理 + IMAP/SMTP 连通性，返回清晰诊断信息）
- `POST /api/accounts/export`：管理员认证后提交独立配置密码（至少 12 个字符），返回版本化加密配置包，最多 100 个邮箱、512KiB 明文配置。PBKDF2-HMAC-SHA256（600000 次迭代、随机 16 字节 salt）派生 AES-256-GCM 密钥；包内包括现有邮箱凭据和代理，不包括主密钥、管理员密码、MCP 令牌或邮件正文。配置操作单并发，HTTP 请求上限 1MiB，禁止明文凭据日志与临时文件。
- `POST /api/accounts/import`：使用配置密码解密并校验整个包；按去除首尾空格、忽略大小写的邮箱地址去重。重复条目以后出现的连接配置为准，空凭据不覆盖前一项；与已有邮箱合并时保留账号 ID 与邮件，缓存仍按来源变化规则失效；整批账号与默认代理在同一短事务写入，并用目标实例主密钥重新加密。文件中共用代理非空时更新本地共用代理，为空时保留已有共用代理；仅主动选择共用代理的账号使用它。导入不生成或刷新服务商授权码与 OAuth 令牌。
- `GET/PUT /api/settings/proxy`（可选共用代理配置，仅影响显式选择 global 的邮箱）
- `GET /api/emails?account_id=&folder=&unread=&q=&cursor=&limit=50`（统一收件箱列表 + FTS5 检索；返回 `emails`、`next_cursor` 和不随游标缩小的筛选总数 `total`；前端使用前后翻页的游标历史；待确认的归档/删除/恢复在目标类目中返回有效 `folder_role` 和 `pending_action`，在确认前保留原始来源定位用于恢复）
- 恢复通过 flags 接口的 `{"restore":true}` 将废纸篓或归档邮件移回 `INBOX`；`restore`、`archive`、`delete` 互斥。恢复复用持久化移动检查点、失败重试与 SSE，确认前保留原始来源定位。废纸篓提供已读、星标与恢复操作，不显示归档或重复删除。
- `GET /api/emails/:id`（优先返回本地正文；未命中则拉取、清洗并保存，含附件元信息与 `cached` / `local` 状态）
- `GET /api/emails/:id/attachments/:part_id`（流式下载附件）
- `PATCH /api/emails/:id/flags`（已读/未读/星标/归档/删除/恢复到收件箱：先更新本地 DB 并推送 SSE，再异步同步 IMAP）
- `POST /api/emails/send`（撰写/回复/转发，走对应账号代理与 SMTP）
- `GET /api/stats`（管理员会话鉴权；本地已同步总数/未读数、各账号汇总、内存与配额、CPU 采样、SQLite/WAL/SHM 占用和所在磁盘可用容量；无监控中间件）
- `GET /api/events`（SSE 长连接，推送 `new_email`、`flag_updated`、`sync_status`、`mcp_approval_required`）
- `GET/POST/DELETE /api/mcp/tokens` 及 `GET /api/mcp/approvals`、`POST /api/mcp/approvals/:id/approve`、`POST /api/mcp/approvals/:id/reject`

### 5.2 MCP Server（`/mcp` Streamable HTTP）
- 鉴权：Header `Authorization: Bearer <token>`，比对 `mcp_tokens.token_hash`，记录 `audit_logs`。
- 开放 9 个标准工具：
  - **只读（`readonly` / `readwrite`）**：`list_accounts`、`list_emails`、`search_emails`、`get_email`。
  - **状态（仅 `readwrite`）**：`mark_email_read`、`archive_email`。
  - **发信（仅 `readwrite`）**：`send_email`、`reply_email`、`forward_email`。若 Token 的 `require_approval=1`，调用后写入 `pending_actions` 并通过 SSE 通知 Web 端弹窗审批，工具返回 `{"status":"pending_approval","action_id":"..."}`；用户在 Web 端点击批准后由后端执行 SMTP 发送。

---

## 6. 单容器部署与 Nginx 反代规范

### 6.1 `docker-compose.yml`
```yaml
services:
  meldmail:
    build: .
    image: ghcr.io/nyakooo/meldmail:latest
    container_name: meldmail
    restart: unless-stopped
    ports:
      - "127.0.0.1:${MELDMAIL_HTTP_PORT:-18085}:8080"
    volumes:
      - ./data:/data
    environment:
      - MELDMAIL_ADDR=:8080
      - MELDMAIL_DB_PATH=/data/meldmail.db
      - MELDMAIL_MASTER_KEY=${MELDMAIL_MASTER_KEY}
      - MELDMAIL_ADMIN_PASSWORD=${MELDMAIL_ADMIN_PASSWORD}
      - MELDMAIL_TRUST_PROXY=true
      - GOMEMLIMIT=200MiB
      - GOGC=50
    deploy:
      resources:
        limits:
          cpus: "0.5"
          memory: 256M
```

### 6.2 宿主机 Nginx 反代要求（写入 `README.md`）
- `/` 常规反代至 `http://127.0.0.1:18085`，透传 `Host`、`X-Real-IP`、`X-Forwarded-For`、`X-Forwarded-Proto`。
- `/api/events` 与 `/mcp` 必须配置 `proxy_http_version 1.1`、`proxy_set_header Connection ""`、`proxy_buffering off`、`proxy_cache off`、`proxy_read_timeout 86400s`。

---

## 7. 阶段性开发与验收门禁（Milestones）

Codex 必须严格按顺序实现，每阶段提供可复现的测试命令并全部通过：

| 阶段 | 核心交付物 | 阶段性验收标准（Definition of Done） |
| :--- | :--- | :--- |
| **M0：工程底座** | Go + Vue 3 目录初始化、`001_init.sql`、SQLite 封装、AES-256-GCM 加解密、登录鉴权、`Dockerfile`、`docker-compose.yml`。 | 1. `docker compose up --build` 成功启动单容器。<br>2. SQLite `PRAGMA journal_mode` 为 `wal`。<br>3. 访问 `http://127.0.0.1:18085` 返回 Vue 页面，空闲内存 < 30MB。<br>4. `go test ./...` 通过。 |
| **M1：代理与首封同步** | `ProxyDialer`（含单元测试）、163 `IMAP ID` 与主流邮箱预设、账号 CRUD + 连接测试 API、手动触发增量同步。 | 1. `ProxyDialer` 单元测试覆盖 Direct/HTTP/SOCKS5。<br>2. `/api/accounts/test` 诊断逻辑完整。<br>3. 同步后 `emails` 表写入元数据与 ≤500 字 `snippet`，`credentials_enc` 为密文。 |
| **M2：统一收件箱与搜索** | 统一列表 API、FTS5 `trigram` 搜索、前端响应式三栏/移动端列表与账号筛选界面。 | 1. 多账号邮件混合按时间倒序展示，支持账号/未读筛选。<br>2. FTS5 单元测试验证中文、英文、6 位数字验证码模糊搜索耗时 < 50ms。 |
| **M3：后台实时同步与 SSE** | 后台 Worker（IDLE + QQ Re-IDLE + 45min 校验 + 指数退避 + 并发信号量 3）与 SSE Broker 推送。 | 1. 浏览器关闭状态下后台持续同步新邮件。<br>2. 前端接入 `/api/events` 自动无刷新插入新邮件。<br>3. 模拟断网恢复后 Worker 自动重连，无 Goroutine 泄漏。 |
| **M4：完整收发与流式防 OOM** | 按需拉取正文 + `bluemonday` 清洗、流式附件下载、SMTP 撰写/回复/转发、已读/星标/归档双向同步。 | 1. 附件下载采用纯流式 `io.Copy`，大附件测试内存峰值 < 180MB。<br>2. 网页端可正常阅读 HTML 邮件、下载附件、发送/回复/转发邮件。 |
| **M5：MCP Server 闭环** | `/mcp` Streamable HTTP 服务、9 个标准工具、Token 权限隔离、Web 端审批流弹窗与审计日志。 | 1. 外部 MCP 客户端可列出并调用 9 个工具。<br>2. `readonly` Token 调用 `send_email` 返回 403。<br>3. `readwrite` + `require_approval` 调用 `send_email` 触发 Web 端审批，批准后执行发送。 |
| **M6：压测与发布收尾** | 竞态检测、内存/并发压测、完善 `README.md`（含 Docker Compose 与 Nginx 反代模板）与 `LICENSE`。 | 1. `go test -race ./...` 100% 通过。<br>2. 前端 `npm run build` 无 TS 报错且正确嵌入 Go 二进制。<br>3. Docker 镜像在 256MB 限制下稳定运行。 |

## 8. 配置迁移与界面一致性补充（2026-11-25）

- 侧栏邮箱计数随当前分类（收件箱、全部、未读、归档、废纸篓）切换；未读排除废纸篓。预取分类计数以即时切换，并通过 SSE 更新，按待确认移动的有效目标分类统计，失败回滚后恢复。
- 连接错误不显示孤立叹号；使用可点击的状态说明，进入对应邮箱设置查看诊断。状态不得假装连接成功或掩盖认证错误。
- 桌面端模态框优先横向多列，窄屏单列；统一主题化细滚动条，保持键盘焦点与移动端可访问性。同步与主题切换使用一致的圆形按钮、图标尺寸、线宽和主题色。默认代理与邮箱代理 URL 编辑框明文显示，便于核对连接地址。共用代理位于独立“共用代理（可选）”入口，新邮箱默认直连；邮箱表单只显示其独立连接方式。仅显式选择 global 的账号使用共用代理。不同时展示两个代理编辑区域。
- 加密配置包的口令派生参数参考 [OWASP PBKDF2-HMAC-SHA256 工作因子](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html#pbkdf2)，不修改数据库已有凭据的主密钥加密方式。

- 模态框采用稳定的视口高度、固定标题与操作栏，内容区独立滚动；切换邮箱、代理模式或标签时外框不随内容跳动。连接方式选项具有完整显示宽度，窄屏改为单列。
- 已认证管理员编辑时，通过 `GET /api/accounts/{id}/proxy` 与 `GET /api/settings/proxy/edit` 回填完整已保存代理 URL（包含代理认证信息）；响应禁止缓存。普通邮箱列表与代理摘要继续隐藏凭据，邮箱密码和令牌不返回。切换编辑目标取消上一请求，避免代理地址串入其他邮箱。

- Linux 部署支持可选 `MELDMAIL_HOST_PROXY_PORT`（HTTP/混合端口，1–65535），`deploy.sh` 自动叠加 host 网络配置以访问宿主机回环代理，网页仍绑定本机 HTTP 端口。留空保留默认部署方式；每个邮箱仍独立选择直连/共用/独立代理。
- 环境代理在每次启动时优先同步至加密的共用代理设置，相同值不重写；运行期间标明环境管理并禁止网页覆盖。移除环境端口时只清除此前由其管理的代理，未启用过环境代理时保留手动设置。管理员密码和主密钥不参与此同步。修改 `.env` 需重新创建容器。

- 桌面设置入口保留侧栏底部，删除顶部工具栏重复入口。底部实时更新状态使用独立非交互状态行；账户操作统一 SVG 图标、文字起点、间距与点击区域，移动端保留可用导航入口。

## 9. 轻量诊断日志（2026-11-26）

按用户要求增加本地 JSON 诊断日志，无外部服务或新依赖。默认在数据库旁的 `logs/` 按 UTC 日轮转，保留当天和前 6 天；启动、跨日和每小时清理过期文件，同时保留控制台输出。目录可由 `MELDMAIL_LOG_DIR` 配置。记录进程生命周期、后台同步、IDLE 续接、重连模式、账号 ID、连接方式、耗时及安全错误分类。禁止日志包含邮件内容、凭据、代理 URL 或原始服务端错误文本；仅允许固定分类及白名单内的 IMAP 状态/错误码。

已登录的管理员可在「设置 → 诊断日志」查看最近最多 100 条完整记录，按账号及日志级别筛选、手动刷新或复制当前显示内容。窗口支持最大化与还原，清屏仅清空当前视图，刷新可重新加载日志。只读接口为 `GET /api/settings/logs`，沿用网页会话鉴权并禁止缓存。

## 10. 浏览器系统通知

设置中的「系统通知」默认关闭，由用户主动开启并授权，偏好保存在当前浏览器。MeldMail 保持登录和实时连接时，后台标签页可对近期同步到收件箱的未读邮件和待审批的发信请求显示桌面通知。系统通知仅显示通用标题。已打开的页面继续通过现有 SSE 更新。

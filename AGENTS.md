# AGENTS.md — MeldMail 开发执行规范

本项目以根目录 `SPEC.md` 为唯一权威架构与需求基准。所有代码生成、重构、测试与提交必须严格遵守以下规则。

---

## 1. 核心执行纪律
1. **严格按阶段推进**：按 `SPEC.md` 第 7 节定义的 `M0 → M1 → M2 → M3 → M4 → M5 → M6` 顺序执行。当前 Milestone 的专属验收项通过后再进入下一阶段。
2. **红线零容忍**：
   - 严禁引入 Redis、Kafka、RabbitMQ、独立数据库或任何重型中间件。
   - 严禁将完整邮件 HTML 正文、原始 MIME 或附件写入磁盘。
   - 严禁在邮件正文或附件处理中使用 `io.ReadAll` 全量读入内存；附件下载必须采用 `io.Reader` 流式透传。
   - 严禁复制 GPL / LGPL / AGPL 协议项目代码（本项目为 Apache-2.0）。
3. **增量测试，拒绝重复**：
   - **不重复写测试**：已覆盖的核心逻辑不要在不同阶段编写重复用例。
   - **不重复跑全量**：`M0～M5` 阶段仅运行**本阶段新增或被修改包**的单元测试（例如 `go test ./internal/proxy/...`），不重复验收上一阶段已通过的功能。
   - **全量回归时机**：仅在 **M6 最终收尾**，或**修改了 `storage` / `crypto` / `proxy` 等底层公共模块**时，才运行全量 `go test -race ./...`。

---

## 2. 代码风格与工程规范

### Go（后端）
- **格式化与静态检查**：变更文件必须通过 `gofmt` 与 `go vet`。
- **错误处理**：严禁忽略错误（`_`）；跨层传递错误使用 `fmt.Errorf("module action: %w", err)`；日志中严禁打印明文密码、授权码、OAuth Token 或 `MELDMAIL_MASTER_KEY`。
- **并发与资源**：网络 IO、IMAP/SMTP 操作、后台 Worker 必须接收 `context.Context` 并支持优雅退出；严禁 Goroutine 泄漏；并发同步必须经过全局信号量（上限 3）。
- **SQLite 操作**：必须使用参数化查询；事务及时提交，避免长事务阻塞 WAL 写入。

### Vue 3 + TypeScript（前端）
- **语法标准**：统一使用 `<script setup lang="ts">` 与 Composition API；严禁滥用 `any`。
- **轻量原则**：保持依赖精简，确保构建产物能通过 `embed.FS` 轻量嵌入 Go 二进制；页面适配桌面端与移动端浏览器。
- **状态同步**：收件箱与阅读状态通过 `/api/events` SSE 更新，避免不必要的全量轮询。

---

## 3. Git 提交规范
- 采用 **Conventional Commits** 格式：`<type>(<scope>): <subject>`
  - `type`：`feat` | `fix` | `refactor` | `test` | `docs` | `chore`
  - `scope`：阶段或模块名，如 `m0`、`proxy`、`imap`、`storage`、`mcp`、`web`
- **提交边界**：每个 Milestone 至少对应一个清晰的收尾提交；严禁提交 `.env`、`*.db`、`*.db-wal`、构建产物或敏感凭据。

---

## 4. 阶段验收与汇报格式（强制）
每完成一个 Milestone，仅针对**本阶段新增/变更部分**运行测试，并按以下精简模板汇报：

```markdown
## Milestone [X] 验收报告

### 1. 本阶段交付物
- [文件路径/模块]：简述核心实现

### 2. 增量验证结果
- 变更模块测试（`go test ./internal/<changed>/...` 或 `npm run build`）：PASS / FAIL
- 本阶段专属验收项（对照 SPEC.md 第 7 节，不重复汇报往期项）：
  - [x] 验收项 1：验证方式与实测结果
  - [x] 验收项 2：验证方式与实测结果

### 3. 红线自检
- 无违规中间件 / 无全量落盘与 `io.ReadAll` / 无敏感信息泄露

### 4. 下一步
- 准备进入 Milestone [X+1]（若为 M6，附全量 `go test -race ./...` 结果）
```
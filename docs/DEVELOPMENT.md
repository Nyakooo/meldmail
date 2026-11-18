# MeldMail Development Specification

> Version: 0.1  
> Status: Product and architecture baseline  
> License: Apache-2.0  
> Repository: https://github.com/Nyakooo/meldmail

This document is the product boundary, engineering specification, architecture baseline, and implementation contract for MeldMail. Implementations should follow this document unless a later Architecture Decision Record (ADR) explicitly changes it.

---

## 1. Product definition

MeldMail is a lightweight, self-hosted, unified email client. It connects to users' existing email accounts and presents them through one responsive web interface.

MeldMail is an email client and aggregation layer. It is **not** an email hosting service, SMTP relay, or replacement for Gmail, Outlook, 163, QQ Mail, or other mailbox providers.

### 1.1 Product goals

1. Combine 5–10 personal email accounts into one unified inbox.
2. Continue syncing in the background even when no browser is open.
3. Show new mail quickly through IMAP IDLE where supported, with polling and reconciliation as fallbacks.
4. Support reading, replying, forwarding, composing, sending, archiving, moving, starring, and read/unread operations.
5. Support per-account network routing: Direct, HTTP CONNECT, HTTPS proxy, and SOCKS5.
6. Expose a standard, permission-controlled MCP Server so external MCP-compatible clients and AI agents can access authorized email capabilities.
8. Keep resource usage low enough to share a 2 vCPU / 2 GB server with several unrelated services.
9. Use a single responsive web application for desktop, tablet, and mobile browsers.

### 1.2 Non-goals for the first release

- Do not implement an IMAP or SMTP server.
- Do not host mailboxes or provide mail addresses.
- Do not become a full Gmail API / Microsoft Graph clone.
- Do not build native desktop applications or Android/iOS applications in v1.
- Do not mirror all message bodies and attachments to the server.
- Do not require Redis, RabbitMQ, Kafka, PostgreSQL, Elasticsearch, or another always-on infrastructure service.
- Do not implement enterprise multi-tenant administration in v1.
- Do not promise that every provider supports IDLE, OAuth, or identical folder semantics.

### 1.3 Target users and deployment

- Primary use: one owner operating a self-hosted personal instance.
- Initial account capacity: 5–10 connected mailboxes.
- Primary deployment: Linux server, Docker Compose, behind an existing HTTPS reverse proxy.
- Initial resource envelope: 2 vCPU / 2 GB RAM shared with four or five other services.
- MCP consumers are external applications or AI agents. MeldMail provides the server interface and does not embed or depend on a particular MCP client.

---

## 2. Functional requirements

### 2.1 Account management

- Add, edit, test, disable, reconnect, and remove an account.
- Store incoming and outgoing server settings independently.
- Support provider presets for Gmail, Outlook, 163 Mail, and QQ Mail, while allowing manual IMAP/SMTP settings.
- Support password or app-password authentication where the provider allows it.
- Design the authentication abstraction for OAuth 2.0 / XOAUTH2. Gmail OAuth2 must be supported before claiming production-ready Gmail compatibility.
- Display connection state, last successful sync, last error, proxy route, and authentication method.
- Never display or return stored passwords, OAuth access tokens, or refresh tokens after saving.

### 2.2 Unified inbox and folders

- Combine messages from enabled accounts into a chronological unified inbox.
- Allow filtering by account, folder, unread, starred, date, sender, and attachment presence.
- Preserve the source account and folder for every message.
- Support account-specific inboxes and folder navigation.
- Support Gmail label/folder behavior without assuming every provider has identical folder semantics.
- Use stable message identity based on account, folder, UIDVALIDITY, and UID. Message-ID is useful for correlation but must not be the only primary key.
- Avoid duplicate display of a message where provider-specific labels expose the same logical message in multiple folders; implement provider-aware grouping only when identity is reliable.

### 2.3 Message reading

- Fetch message headers and envelope data for the inbox list.
- Fetch full MIME content only when a user opens a message or explicitly requests it.
- Parse text/plain, text/html, multipart/alternative, multipart/mixed, encoded headers, charsets, inline resources, and attachments.
- Sanitize HTML before rendering. Never render remote email HTML as trusted application UI.
- Load remote images only under an explicit user-controlled policy; default to blocking or proxy-safe placeholders.
- Stream attachment downloads from the upstream provider where practical. Do not persist attachments by default.
- Bound message size, MIME nesting depth, attachment size, and parser resource usage.

### 2.4 Sending and replying

- Support compose, reply, reply-all, forward, recipients, CC, BCC, subject, plain text, HTML, and attachments.
- Send through the SMTP server configured for the selected account.
- Use the selected account's identity and signature.
- Preserve In-Reply-To and References headers for replies.
- After successful sending, reconcile the Sent folder. Do not assume every provider automatically saves a sent copy.
- Treat a timeout after SMTP DATA as an unknown delivery outcome. Do not blindly retry and risk duplicate mail.
- Show a clear delivery state: queued, sending, sent, failed, or outcome unknown.
- Never send mail through MCP without an explicit authorization and confirmation flow.

### 2.5 Search

- v1 local search covers persisted metadata: sender, recipients, subject, Message-ID, account, folder, and date.
- Use SQLite FTS5 for metadata search only.
- Full-body search is not part of the metadata-only baseline. It may be added later through provider-side search or explicit on-demand retrieval.
- Search must not silently download every message body.

### 2.6 Mail state and folder operations

- Support mark read/unread, star/unstar, archive, move, and delete.
- Operations must be applied to the upstream mailbox first or tracked as pending operations with visible failure state.
- Update local metadata only after a confirmed upstream result, or record a pending state that can be reconciled.
- Handle provider-specific folder names and special-use folders such as INBOX, Sent, Drafts, Archive, and Trash.

### 2.7 Synchronization and notifications

- Run account synchronization independently of browser sessions.
- Use IMAP IDLE when advertised by the server.
- Use IDLE notifications as change hints, then fetch and reconcile authoritative state using UIDs.
- End and re-enter IDLE periodically; use a conservative refresh interval (approximately 25 minutes) to avoid server-side idle timeouts.
- Fall back to configurable polling when IDLE is unsupported or unreliable.
- Default to monitoring INBOX only. Other folders are synchronized on demand or through lower-frequency checks unless the user explicitly enables them.
- Recover after network interruption, process restart, UIDVALIDITY changes, and provider disconnects.
- Provide a manual sync action.
- Push changes to browsers through Server-Sent Events (SSE). A disconnected browser must not stop mailbox synchronization.

---

## 3. Network and proxy requirements

### 3.1 Supported routing modes

Each account must support:

- Direct connection.
- HTTP proxy using CONNECT for IMAP/SMTP TCP tunnels.
- HTTPS proxy using TLS to the proxy followed by CONNECT.
- SOCKS5, including optional username/password authentication.

Proxy settings should be configurable per account. A global default may be provided, with per-account overrides.

### 3.2 Connection rules

- Proxy routing applies to both IMAP and SMTP connections.
- TLS remains end-to-end between MeldMail and the destination mail server after the proxy tunnel is established.
- Use the system certificate pool and verify certificates and hostnames.
- Never use insecure TLS verification bypasses in production.
- Configure dial, TLS handshake, authentication, command, and overall operation timeouts.
- Respect context cancellation.
- Make DNS behavior explicit for SOCKS5: support remote DNS resolution when the selected dialer provides it.
- Do not proxy the web UI or inbound API by default; proxy settings are for outbound mailbox connections.
- Redact proxy credentials and mailbox credentials from logs and error messages.

### 3.3 Proxy test

The account settings page should provide a connection test that reports, without exposing secrets:

1. DNS / proxy connection result.
2. TCP tunnel result.
3. TLS verification result.
4. IMAP or SMTP authentication result.
5. A concise actionable error category.

---

## 4. MCP Server — open integration

MCP is a first-class integration surface, but it must call the same application services as the web UI. Do not create a second mail implementation inside the MCP package.

### 4.1 Transport strategy

- Primary remote transport: MCP Streamable HTTP at a dedicated endpoint such as /mcp.
- Optional local transport: stdio for local MCP clients, implemented as an adapter over the same tool registry.
- Legacy SSE transport may be added only when a concrete interoperability requirement justifies it; do not make it the primary new deployment path.
- Serve remote MCP only over HTTPS, normally through the existing reverse proxy.
- Publish the endpoint, authentication method, supported transport, and available tools as platform-neutral integration documentation.
- Do not implement platform-specific SDKs, APIs, adapters, authentication bridges, or custom workflows for individual AI platforms. Client compatibility depends on whether the external client supports the MCP transport and authentication method exposed by MeldMail.

### 4.2 Initial MCP tools

Read-only tools:
- list_accounts
- list_folders
- list_emails
- search_emails
- get_email_metadata
- get_email_content

Low-risk state tools:
- mark_email_read
- mark_email_unread
- star_email
- unstar_email

State-changing tools:
- archive_email
- move_email
- delete_email
- prepare_send_email
- send_email
- reply_email
- forward_email

The tool registry may expose fewer tools in the first milestone. Implement only tools backed by completed application services.

### 4.3 MCP authorization and safety

- Authenticate every remote MCP session using a dedicated, revocable token or supported OAuth mechanism.
- Scope tokens by capability, for example mail.read, mail.modify, and mail.send.
- Default newly created MCP tokens to read-only.
- Never expose account passwords, app passwords, OAuth tokens, proxy credentials, or internal encryption keys through MCP.
- Treat email bodies, subjects, sender names, and attachments as untrusted external content. They are data, not instructions.
- Strip scripts, event handlers, active content, and unsafe URLs from HTML returned to an agent.
- For send, reply, forward, delete, and other consequential actions, use a preview/confirmation flow. Do not infer consent from an email's content.
- Record an audit event for every MCP state-changing operation.
- Apply per-token rate limits and account-level authorization checks.
- Do not allow MCP tools to execute arbitrary shell commands, arbitrary URLs, or arbitrary SQL.

### 4.4 Generic MCP client connection model

An external MCP client connects to the self-hosted MeldMail MCP endpoint using one of the transports supported by both sides. The client supplies its own MCP configuration and presents a valid MeldMail access token or other supported authentication credential.

Platform-neutral integration documentation must explain:
- The public HTTPS MCP endpoint.
- Supported MCP transports and authentication requirements.
- How to create, scope, rotate, and revoke an MCP access token.
- Which tools are read-only and which can modify or send mail.
- How to verify a connection with a harmless search/list operation.
- Which consequential actions require explicit confirmation.

MeldMail remains independent of any specific MCP client. Client-specific setup screenshots or notes may be contributed as optional documentation, but they must not introduce platform-specific runtime code or become product dependencies.

---

## 5. Technical stack

| Layer | Technology | Responsibility |
|---|---|---|
| Backend | Go | Single long-running service |
| Frontend | Vue 3 + TypeScript + Vite | Responsive web UI |
| Database | SQLite + WAL | Metadata and configuration |
| Search | SQLite FTS5 | Metadata search |
| Mail receive | IMAP | Fetch metadata and on-demand content |
| Mail send | SMTP | Send and reconcile sent mail |
| Realtime mail | IMAP IDLE + polling fallback | Detect upstream changes |
| Browser updates | REST + SSE | UI data and live updates |
| Agent integration | MCP | AI tool access |
| Deployment | Docker Compose | Single-container deployment |
| Reverse proxy | Existing Nginx / HTTPS proxy | TLS termination and routing |

Build the Vue frontend into static assets and embed them into the Go binary where practical. Production deployment must not require a separate Node.js runtime.

### 5.1 Library selection policy

Do not implement IMAP, SMTP, OAuth, MIME, SOCKS5, or MCP wire protocols from scratch.

- IMAP: evaluate the emersion/go-imap library. Its v2 branch is an IMAP4rev2 implementation and has been described by its project as still in development; pin a tested version and isolate it behind an internal interface. Do not let application code depend directly on library-specific types.
- SMTP and message composition: evaluate a maintained Go mail package such as wneessen/go-mail for MIME composition, SMTP authentication, TLS, XOAUTH2, attachments, and custom dialing.
- MCP: prefer the official modelcontextprotocol/go-sdk, with transport selected according to client compatibility.
- OAuth: use maintained OAuth 2.0 libraries and provider documentation.
- Proxy: use Go's standard networking abstractions and maintained SOCKS5 / HTTP CONNECT dialers.
- MIME: use a maintained MIME parser with explicit size and nesting limits.

Before adding a dependency, verify its maintenance status, tagged version, license, transitive dependencies, security history, and compatibility with the project's Go version. Pin dependencies in go.mod and review updates deliberately.

---

## 6. Architecture

Use a modular monolith. The project is one deployable Go process, not a microservice system.

### 6.1 Logical architecture

~~~text
┌───────────────────────────────────────────────────────────────┐
│                         Web Browser                            │
│                 Vue 3 / TypeScript / Responsive                │
└──────────────────────────────┬────────────────────────────────┘
                               │ REST + SSE
┌──────────────────────────────▼────────────────────────────────┐
│                         Go Application                         │
│                                                               │
│  HTTP API       Application Services        MCP Server         │
│  ─────────      ────────────────────        ──────────         │
│  Auth           Account Service             Tool Registry      │
│  Mail API       Mail Query Service           Streamable HTTP   │
│  SSE            Sync Service                 Optional stdio    │
│                 Send Service                                    │
│                 Settings Service                                 │
│                                                               │
│  Account Manager ── Per-account Sync Workers                   │
│       │                 │                                      │
│       │                 ├── IMAP IDLE / Polling                 │
│       │                 ├── Incremental UID Sync                │
│       │                 └── Reconnect / Reconcile               │
│       │                                                        │
│  Proxy Manager ── Direct / HTTP CONNECT / HTTPS / SOCKS5        │
│  Event Bus ────── In-process bounded event distribution         │
│  Repository ──── SQLite WAL                                    │
└──────────────────────────────┬────────────────────────────────┘
                               │ outbound TLS connections
                 ┌─────────────┴──────────────┐
                 │                            │
              IMAP servers                 SMTP servers
          Gmail / Outlook / 163 / QQ     Gmail / Outlook / 163 / QQ
~~~

### 6.2 Required internal boundaries

- Domain: account, folder, message metadata, sync state, and operation models.
- Application: use cases shared by HTTP and MCP.
- Mail adapters: IMAP and SMTP provider connections.
- Proxy: outbound dialer construction and routing.
- Storage: SQLite repositories and migrations.
- Transport: HTTP API, SSE, and MCP.
- Frontend: views, components, API client, and state.

MCP handlers must call application services, never access SQLite or IMAP clients directly.

### 6.3 Suggested project structure

~~~text
meldmail/
├── cmd/
│   └── meldmail/
│       └── main.go
├── internal/
│   ├── app/
│   │   ├── accounts/
│   │   ├── mail/
│   │   ├── sync/
│   │   ├── send/
│   │   └── settings/
│   ├── domain/
│   ├── mail/
│   │   ├── imap/
│   │   ├── smtp/
│   │   ├── mime/
│   │   └── providers/
│   ├── sync/
│   ├── proxy/
│   ├── mcp/
│   ├── http/
│   ├── storage/
│   │   └── sqlite/
│   ├── security/
│   └── config/
├── web/
│   ├── src/
│   ├── public/
│   └── package.json
├── migrations/
├── docs/
│   ├── DEVELOPMENT.md
│   ├── MCP.md
│   ├── SECURITY.md
│   └── THIRD_PARTY_NOTICES.md
├── deploy/
│   └── docker-compose.yml
├── .github/
│   └── workflows/
├── Dockerfile
├── go.mod
├── go.sum
├── LICENSE
├── README.md
└── NOTICE
~~~

The structure is a target, not a requirement to create empty packages in advance. Add packages when a working feature needs them.

---

## 7. Synchronization design

### 7.1 Worker model

- One managed worker per enabled account.
- One long-lived IMAP connection per account for the initially monitored INBOX.
- Do not create one container or process per account.
- Serialize operations on a given IMAP connection.
- Use bounded concurrency for body fetches and initial history sync.
- SMTP connections are created on demand and closed after the operation unless measured reuse is beneficial.
- Account workers must support pause, resume, reconnect, and graceful shutdown.

### 7.2 Incremental sync

Persist, per account and folder:
- UIDVALIDITY
- UIDNEXT where available
- highest successfully reconciled UID
- HIGHESTMODSEQ where supported
- last successful sync time
- last error and retry state

Use UID-based commands, never message sequence numbers as durable identifiers.

If UIDVALIDITY changes, invalidate the folder's UID cursor and rebuild its metadata index. If CONDSTORE/QRESYNC is supported, use it to reduce reconciliation work; otherwise use UID SEARCH / UID FETCH and periodic reconciliation.

An IDLE notification is only a hint that the mailbox changed. It is not the source of truth. Fetch authoritative state after leaving IDLE, then re-enter IDLE.

### 7.3 First sync limits

- Default to INBOX.
- Initial import: latest 90 days, capped at 1,000 messages per account; use whichever limit is reached first.
- Import headers and metadata only.
- Older history is opt-in and processed in bounded batches.
- Do not run all accounts' full initial imports simultaneously.
- Other folders are loaded on demand unless the user enables their monitoring.

### 7.4 Suggested timing defaults

| Situation | Default behavior |
|---|---|
| IDLE supported | Persistent listener |
| IDLE unsupported | Poll every 3–5 minutes |
| IDLE reconnect | Exponential backoff with jitter |
| IDLE refresh | Re-enter around every 25 minutes |
| Reconciliation | Every 30–60 minutes, staggered by account |
| Browser update | SSE event after committed DB change |
| Manual sync | Enqueue a bounded sync for the selected account |

These are starting defaults. Provider-specific rate limits and behavior take precedence.

---

## 8. Metadata-only storage model

The upstream mailbox remains the authoritative source for message content.

### 8.1 Persisted data

- Account and provider settings.
- Folder identifiers and display names.
- UID, UIDVALIDITY, and synchronization cursors.
- Message-ID, sender, recipients, CC, subject, date, size, flags, and folder.
- A bounded set of normalized headers needed for display and reply threading.
- Application preferences and audit events.

### 8.2 Not persisted by default

- Full plain-text or HTML message body.
- Raw MIME message.
- Attachment bytes.
- Remote images.
- Full copies of mailbox folders.

Message content is fetched on demand and kept only in bounded transient memory while needed. Attachment downloads should be streamed. If temporary files are unavoidable, enforce a strict size limit, private permissions, and automatic cleanup.

### 8.3 Database rules

- SQLite in WAL mode with foreign keys enabled.
- Use schema migrations; never mutate schema implicitly at runtime.
- Use prepared statements and bounded connection pools.
- Keep writes short and serialize write transactions through a repository layer.
- Add indexes for account, folder, date, flags, UID, and search fields.
- FTS5 indexes metadata only.
- Run periodic WAL checkpoints.
- Provide a supported SQLite backup procedure.
- Never copy only the main database file while ignoring an active WAL.

Suggested tables:
- accounts
- folders
- messages
- sync_states
- app_settings
- mcp_tokens
- audit_events

Use a composite uniqueness constraint that includes account, folder, UIDVALIDITY, and UID. Do not assume Message-ID is always present or unique.

---

## 9. Resource budget and deployment

The server is shared with four or five other services. MeldMail must be conservative by default.

### 9.1 Initial resource targets

| Metric | Initial target |
|---|---:|
| Connected accounts | 5–10 |
| Normal idle RSS | target 60–120 MB |
| Normal workload RSS | target below 180 MB |
| Container memory limit | 256 MB |
| CPU limit | 0.5 vCPU |
| GOMAXPROCS | 1 |
| GOMEMLIMIT | 160 MiB starting point |
| Database | SQLite, local persistent volume |
| Extra infrastructure | none |

These are engineering targets, not measured guarantees. Confirm them with 10-account, reconnect, MIME, and history-sync tests. If the shared host has insufficient available memory, reduce concurrency or postpone deployment rather than starving existing services.

### 9.2 Required resource controls

- Bounded worker and fetch concurrency.
- Bounded in-memory message and MIME buffers.
- Maximum message and attachment sizes.
- No unbounded event queues or caches.
- Stagger account polling and reconciliation.
- Limit log volume and enable rotation.
- Respect context cancellation and deadlines.
- Graceful shutdown closes connections and completes or safely abandons transactions.

### 9.3 Deployment principles

- One Go service/container.
- Frontend assets embedded in the Go binary for production.
- One persistent data directory.
- Reuse the host's existing Nginx and HTTPS certificate.
- Do not expose SQLite, IMAP, SMTP, or internal admin ports publicly.
- Publish only the web application port to the reverse proxy.
- Include health and readiness endpoints.
- Support amd64 first; add arm64 when CI and runtime tests pass.

Example deployment resource constraints:

~~~yaml
services:
  meldmail:
    image: ghcr.io/nyakooo/meldmail:latest
    container_name: meldmail
    restart: unless-stopped
    mem_limit: 256m
    mem_reservation: 96m
    cpus: 0.50
    pids_limit: 64
    environment:
      TZ: Asia/Shanghai
      GOMAXPROCS: "1"
      GOMEMLIMIT: 160MiB
    volumes:
      - ./data:/app/data
~~~

The image name is illustrative until a release is published. Do not copy this compose file into production without configuring authentication, secrets, ports, and the reverse proxy.

---

## 10. Security requirements

Email accounts contain highly sensitive personal data. Security is a release blocker, not a later enhancement.

1. Encrypt passwords, app passwords, OAuth access tokens, and refresh tokens at rest using authenticated encryption such as AES-256-GCM.
2. Keep the encryption key outside the SQLite database. Provide a documented backup and recovery process for the key.
3. Restrict data directory permissions.
4. Require application authentication; use secure, HttpOnly, SameSite cookies and CSRF protection where cookie sessions are used.
5. Use TLS for all remote mailbox connections and validate certificates.
6. Never log credentials, message bodies, attachment contents, OAuth codes, or full proxy URLs containing secrets.
7. Sanitize HTML mail and defend against script execution, phishing links, and remote tracking images.
8. Validate attachment names and never trust paths supplied by email content.
9. Apply request size limits, timeouts, rate limits, and authentication throttling.
10. Protect MCP with separate revocable tokens and least-privilege scopes.
11. Require confirmation for sending, deleting, forwarding, and other consequential actions.
12. Do not expose arbitrary SQL, shell, filesystem, or network-fetch tools through MCP.
13. Provide account removal that deletes locally stored credentials and metadata after confirmation.
14. Provide secure backup/restore documentation.
15. Maintain a SECURITY.md with a private vulnerability reporting path before public launch.

---

## 11. UI and interaction requirements

### 11.1 Desktop

- Clean three-column layout: navigation/accounts, message list, reading pane.
- Unified inbox is the default landing view.
- Clear unread state, sender, subject, preview, timestamp, source account, and attachment indicator.
- Reading pane supports reply, forward, archive, star, move, and more actions.
- Global search and account/folder filters.
- Sync status should be informative but quiet: healthy, syncing, delayed, needs attention.
- Account proxy status is visible in account settings and diagnostics, not as a distracting permanent banner.

### 11.2 Mobile

- Responsive single-column message list.
- Message detail opens as a separate view.
- Navigation collapses into a compact bottom bar or drawer.
- Compose and primary actions remain reachable.
- Avoid desktop three-column compression on narrow screens.

### 11.3 Visual principles

- Modern, restrained, clean, readable.
- Neutral surfaces with one restrained primary accent.
- No decorative dashboards, animated charts, heavy glassmorphism, or unnecessary gradients.
- Use consistent spacing, typography, focus states, and accessible contrast.
- Support keyboard navigation and reduced-motion preferences.
- The interface must remain usable with long subjects, long sender names, large unread counts, and empty/error/loading states.

---

## 12. API and event contracts

Use versioned JSON REST endpoints. Exact routes may evolve, but contracts must be documented and tested.

Suggested endpoints:
- GET /api/v1/accounts
- POST /api/v1/accounts
- PATCH /api/v1/accounts/{id}
- POST /api/v1/accounts/{id}/test
- POST /api/v1/accounts/{id}/sync
- GET /api/v1/folders
- GET /api/v1/messages
- GET /api/v1/messages/{id}
- POST /api/v1/messages/{id}/read
- POST /api/v1/messages/{id}/star
- POST /api/v1/messages/{id}/archive
- POST /api/v1/messages/{id}/move
- POST /api/v1/send
- GET /api/v1/events (SSE)

SSE event names should be stable and versioned, for example:
- message.created
- message.updated
- folder.updated
- account.sync_state
- account.connection_state
- notification

Events should contain identifiers and minimal metadata, not full message bodies or secrets. The browser fetches the latest authorized representation through REST.

---

## 13. Testing and quality gates

### 13.1 Required tests

- Unit tests for provider configuration, metadata normalization, UID cursor logic, and error classification.
- IMAP integration tests for login, CAPABILITY, SELECT, UID FETCH, IDLE, reconnect, UIDVALIDITY reset, flags, move, and delete.
- SMTP integration tests for TLS, STARTTLS, authentication, attachments, reply headers, and unknown delivery outcome.
- Proxy tests for Direct, HTTP CONNECT, HTTPS proxy, SOCKS5, proxy authentication, DNS behavior, and connection timeout.
- MIME tests for encoded headers, multipart messages, malformed content, nested parts, oversized attachments, and HTML sanitization.
- SQLite migration, transaction, backup, and recovery tests.
- MCP tests for initialization, tool discovery, authorization scopes, invalid arguments, confirmation, audit records, and MCP Streamable HTTP interoperability.
- Frontend tests for desktop/mobile layout, unread state, error states, and SSE reconnect.
- End-to-end tests using test mailboxes. Never use production mailbox credentials in CI.

### 13.2 Release acceptance criteria

A release candidate must:
- Connect 10 test accounts without creating one process per account.
- Continue syncing with all browser sessions closed.
- Recover after network loss and process restart.
- Avoid duplicate metadata after repeated sync and UIDVALIDITY changes.
- Avoid blind SMTP retries after ambiguous delivery outcomes.
- Pass the proxy test matrix.
- Expose only authorized mail data through REST and MCP.
- Stay within the resource budget under the documented test workload.
- Run for at least 72 hours without memory growth that indicates a leak.
- Pass Go tests, static analysis, frontend lint, and production frontend build.

Do not claim provider compatibility based only on a successful login. Test folder semantics, IDLE, flags, sending, Sent-folder behavior, and reconnect behavior separately.

---

## 14. Implementation milestones

### Milestone 0 — Repository foundation
- README, Apache-2.0 LICENSE, development specification, security and contribution policies.
- Go module, Vue 3 + TypeScript + Vite project, Docker build.
- CI for Go tests and frontend build.
- No placeholder microservices or unnecessary infrastructure.

### Milestone 1 — IMAP proof of concept
- One account configuration.
- Direct and proxy connection.
- IMAP capability detection and login.
- Fetch INBOX headers and metadata.
- Persist metadata in SQLite.
- Demonstrate Gmail and 163 connectivity with test accounts.

### Milestone 2 — Multi-account unified inbox
- Account manager and per-account workers.
- 5–10 account support.
- Unified message query and source-account display.
- Initial sync limits and metadata search.
- Connection diagnostics.

### Milestone 3 — Background sync and realtime UI
- IDLE listener and polling fallback.
- UID-based incremental reconciliation.
- Reconnect and UIDVALIDITY recovery.
- SSE events and browser reconnect.
- Verify sync continues without an open browser.

### Milestone 4 — Read and send mail
- On-demand MIME retrieval and safe rendering.
- Reply, forward, compose, SMTP send, attachments.
- Sent-folder reconciliation.
- Read/unread, star, archive, move, and delete.

### Milestone 5 — Open MCP Server
- Shared application-service tool registry.
- Streamable HTTP transport.
- MCP tokens and capability scopes.
- Read-only tools first, then confirmed state-changing tools.
- Platform-neutral MCP endpoint, authentication, and client configuration documentation.

### Milestone 6 — Hardening and release
- Security review and dependency audit.
- Resource and 72-hour soak tests.
- Docker image and Linux amd64/arm64 builds.
- English README and user documentation.
- Release notes and upgrade/backup guide.

Each milestone must produce a runnable, testable result. Do not start later-stage features before the preceding milestone's acceptance criteria pass.

---

## 15. Open-source project rules

- Project license: Apache License 2.0.
- Keep the full license text in the repository root as LICENSE.
- Add SPDX license identifiers to source files where appropriate.
- Preserve third-party copyright and license notices.
- Maintain docs/THIRD_PARTY_NOTICES.md or an equivalent generated dependency notice for shipped releases.
- Do not copy implementation code from Cypht, Roundcube, SnappyMail, or other projects into MeldMail unless the exact license and compatibility have been reviewed and the required obligations can be met.
- Use open-source projects as architectural references; write MeldMail's implementation independently.
- Prefer MIT, BSD-2-Clause, BSD-3-Clause, and Apache-2.0 dependencies, subject to review of transitive dependencies.
- Review AGPL/GPL/LGPL dependencies before adoption; do not add them casually to a distributable binary.
- Do not include proprietary provider logos, fonts, or icons without checking their trademark and asset terms.
- Contributions should include tests and follow the project's license. A future CONTRIBUTING.md should explain whether contributors are expected to sign a DCO.

Apache-2.0 is selected because it permits commercial use, modification, redistribution, and patent grants while requiring preservation of license, copyright, and relevant NOTICE information. It does not grant trademark rights.

---

## 16. Open-source implementation references

These projects are references for design decisions, not code to copy.

### Cypht
- Repository: https://github.com/cypht-org/cypht
- Reference: unified views across multiple accounts, protocol adapters, and module-based extensibility.
- MeldMail adaptation: use a provider/adapter boundary and a unified query model, but keep the runtime as a small Go modular monolith.
- Cypht is licensed under LGPL-2.1; do not copy its implementation into this Apache-2.0 project without a deliberate license review.

### Roundcube
- Repository: https://github.com/roundcube/roundcubemail
- Reference: IMAP-centered webmail, server-side cache, configurable refresh interval, and selective folder checking.
- Its default configuration uses a 60-second refresh interval and does not check all folders by default. MeldMail uses persistent IDLE where available and keeps polling as a fallback.
- Reference configuration: https://github.com/roundcube/roundcubemail/blob/master/config/defaults.inc.php

### SnappyMail
- Repository: https://github.com/the-djmaze/snappymail
- Reference: lightweight webmail interaction and conservative configurable mail-check interval.
- Its changelog records a 15-minute default check interval. This is a useful reminder that frequent polling has a server cost.
- SnappyMail is AGPL-3.0; use it as a product/UX reference only unless license obligations are intentionally accepted.

### Go IMAP library
- Repository: https://github.com/emersion/go-imap
- Reference: use a maintained IMAP implementation instead of writing the protocol parser and command engine from scratch.
- The v2 branch describes itself as an IMAP4rev2 implementation and has been marked as still in development. Pin and test the selected version behind MeldMail's own adapter interface.

### Go mail composition and SMTP
- Repository: https://github.com/wneessen/go-mail
- Reference: MIME composition, SMTP authentication, TLS, attachments, XOAUTH2, context handling, and custom dialing.
- Verify the selected release's Go version and dependency license before adoption.

### Official MCP Go SDK
- Repository: https://github.com/modelcontextprotocol/go-sdk
- Reference: MCP server implementation and Streamable HTTP / stdio transports.
- Keep MCP as a transport adapter over application services.

### MCP protocol and SDK
- Official MCP specification: https://modelcontextprotocol.io/specification
- Official Go SDK: https://github.com/modelcontextprotocol/go-sdk
- Reference: implement the standard MCP server protocol and expose supported transports without coupling the server to a specific client platform.

### Gmail OAuth / XOAUTH2
- Documentation: https://developers.google.com/workspace/gmail/imap/xoauth2-protocol
- Reference: Gmail uses SASL XOAUTH2 for IMAP and SMTP authentication. A public application requesting Gmail mail access may need Google's OAuth verification process.

---

## 17. Definition of done

A feature is not done merely because the UI renders or a protocol command succeeds once.

A feature is done only when:
1. It has a documented user-visible behavior.
2. It uses the shared application layer.
3. It handles timeout, cancellation, and provider errors.
4. It does not leak credentials or message content.
5. It has tests for normal and failure paths.
6. It respects the resource budget.
7. It works with the responsive web UI.
8. Its API and MCP permissions are documented.
9. It has been checked against the open-source license policy.

**Primary engineering principle:** MeldMail should remain a small, reliable, self-hosted mail client. Prefer boring, testable components and provider adapters over a complex distributed architecture.

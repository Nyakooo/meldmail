# MeldMail

**A lightweight, self-hosted unified email client built with Go.**

MeldMail brings multiple IMAP email accounts into one clean, responsive web interface. It is designed for personal self-hosting, low resource consumption, reliable background synchronization, and AI-agent integration through MCP.

**Core principles**
- One unified inbox for multiple email accounts.
- Background synchronization that continues when the browser is closed.
- Metadata-first storage: message bodies and attachments stay in the source mailbox and are fetched on demand.
- Per-account Direct / HTTP(S) CONNECT / SOCKS5 proxy settings.
- REST + SSE for the web application, and a standard MCP Server for external MCP-compatible clients and AI agents.
- A single Go service and SQLite database; no Redis, message broker, or separate database server.

## Project status

MeldMail is in the planning and early implementation stage. Features, commands, configuration formats, and provider compatibility may change. Check the development specification and release notes before deploying.

## Documentation

- [Development specification](SPEC.md)
- [License](LICENSE)

## License

MeldMail is licensed under the Apache License 2.0. See [LICENSE](LICENSE).

Third-party dependencies retain their own licenses. See the dependency notices shipped with each release.

## M0 local development

Requires Go 1.24+, Node.js 20.19+ and npm. Build the embedded web assets before compiling Go:

```sh
npm ci --prefix web
npm run build --prefix web
go test ./...
go vet ./...
go run ./cmd/meldmail
```

Set `MELDMAIL_MASTER_KEY` to a random 32-byte key encoded as 64 hexadecimal characters (`openssl rand -hex 32`). Set `MELDMAIL_ADMIN_PASSWORD` on first startup; only its bcrypt hash is stored, and subsequent starts do not overwrite it. Passwords must fit bcrypt's 72-byte limit. Keep the master key securely backed up. Sessions expire after 24 hours and are revoked on restart.

For Docker, copy `.env.example` to `.env`, set a strong password and master key, then run `docker compose up --build -d`. The service binds to `127.0.0.1:8080`, stores SQLite metadata under `./data`, and runs with a 256MB / 0.5 CPU limit. Do not commit `.env` or `data`. Only trust proxy headers when access is restricted to your reverse proxy.

Implementation currently covers M0–M4: responsive inbox, search, background sync, on-demand reading, streaming downloads and SMTP compose/reply/forward. MCP and final release validation remain in later milestones.

After starting the M0 Compose container, run `python3 scripts/check-container.py` to verify the embedded Vue entry, WAL database, idle RSS below 30MiB, loopback binding and resource limits. The script uses Python standard-library modules and the Docker CLI.

## Account and proxy API (M1)

Authenticate with `POST /api/auth/login` (`{"password":"..."}`), retaining the session cookie. Account endpoints require this cookie. `POST /api/accounts` accepts `email`, `provider`, and `password`; provider presets include `gmail`, `163`, `126`, `qq`, and `outlook`. For `custom`, also provide `imap_host`, `imap_port`, `imap_tls`, `smtp_host`, `smtp_port`, and `smtp_tls`. TLS modes are `implicit` and `starttls`; certificate verification is always enabled in production. OAuth2 authentication accepts an existing `access_token`; automatic provider token refresh remains an extension hook.

- `GET /api/accounts` lists accounts without credentials.
- `PUT /api/accounts/:id` replaces configuration; omit the password to retain current credentials.
- `DELETE /api/accounts/:id` removes the account and its local metadata.
- `POST /api/accounts/test` accepts a saved `id` or an unsaved account configuration. It returns separate proxy, IMAP and SMTP diagnostics without sending mail.
- `POST /api/accounts/:id/sync` manually synchronizes the latest 100 messages initially, then new UIDs only.
- `GET/PUT /api/settings/proxy` inspects or sets the global proxy (`{"url":"http://user:password@host:port"}`). GET removes user credentials from the response. An empty URL disables the global proxy.

An account's `proxy_mode` can be `global`, `direct`, `http`, `https`, or `socks5`. Explicit proxy modes use `proxy_url`. Both proxy URLs and account credentials are encrypted with the master key before storage. Only bounded snippets and metadata are stored; no message bodies or attachments are persisted.

## Inbox and search (M2)

The web inbox combines connected accounts in descending date order, with account, unread and folder filters. Search matches subject, sender and the bounded snippet. Queries with at least three characters use SQLite FTS5 trigram; shorter queries use a parameterized substring search. `GET /api/emails` accepts `account_id`, `folder` (default `inbox`, or `all`), `unread`, `q`, `limit` (1–100, default 50) and an opaque `cursor`. The JSON response contains `emails` and `next_cursor`. Pagination uses `(date,id)` rather than offsets so equal timestamps remain stable.

## Background updates (M3)

Enabled accounts synchronize without a browser. Each account has a dedicated INBOX IDLE connection; metadata fetches and state checks use separate short connections and share a global three-account limit. QQ renews IDLE every 12 minutes. Flags/deletions are checked every 45 minutes. Reconnection delays are 5, 15, 30 and 60 seconds; five consecutive failures switch to three-minute polling. Editing, pausing or removing an account restarts or stops its worker.

The authenticated `/api/events` stream broadcasts `new_email`, `flag_updated` and `sync_status`. The web UI inserts events directly; reconnecting refreshes the current snapshot to catch missed events. Slow subscribers are disconnected rather than allowed to accumulate an unbounded queue. Server shutdown cancels HTTP streams and joins the workers before closing SQLite.

Optional real-browser SSE acceptance uses an installed Chrome and Playwright without adding browser dependencies to the application:

```sh
npm install --prefix /tmp/meldmail-browser playwright
MELDMAIL_PLAYWRIGHT_MODULE=/tmp/meldmail-browser/node_modules/playwright go test ./internal/api -run TestLiveInboxBrowser -v
```

## Reading and sending (M4)

Opening a message fetches its BODYSTRUCTURE and selected text/html (preferred) or text/plain part through an independent short-lived IMAP connection. Decoded and sanitized bodies are bounded to 5MiB, and bluemonday removes active content. HTML is rendered in a sandboxed frame; remote tracking images are blocked. Message bodies, MIME and attachments are never saved by the service.

Attachments are fetched by the exact PartID advertised in BODYSTRUCTURE, transfer-decoded and streamed to the HTTP response with `io.Copy`. Forwarded attachments also stream from IMAP into SMTP without a disk cache or full buffering. The forward action automatically includes the source text and attachments; its `text` field is an optional introductory note. Replies set threading headers and default to the original sender when `to` is omitted. Outgoing messages use the sending account's SMTP settings and proxy.

- `GET /api/emails/:id` returns metadata, sanitized `html` or `text`, and `attachments`.
- `GET /api/emails/:id/attachments/:part_id` returns a streamed attachment download.
- `POST /api/emails/send` accepts `account_id`, `to` (address array), optional `cc`, `subject`, `text` or `html`, `action` (`send`, `reply`, `forward`), and `email_id` for replies/forwards.
- `PATCH /api/emails/:id/flags` accepts `is_read`, `is_starred`, `archive`, or `delete`. A 202 response means the local change is applied and remote synchronization is queued.

Pending flag changes are bounded to 128 metadata-only records in SQLite settings and survive restarts. Reconciliation cannot overwrite them. Failed flag operations retry with bounded backoff and restore the previous local state after five failures. Moves retain durable destination checkpoints; uncertain outcomes are recovered without blindly repeating MOVE or rolling back to an obsolete UID. Archive moves preserve the destination UID; delete moves to Trash rather than expunging unrelated messages. Gmail archive removes the `\Inbox` label using the Gmail IMAP extension and resolves the message in the advertised All Mail folder.

M4 acceptance commands:

```sh
go test ./internal/mail -run 'TestReadSanitizeAndDecodeAttachment|TestStreaming20MiBAttachment|TestRecoverAcknowledgedMove' -v
go test ./internal/mail/adapter -run TestGmailArchiveRemovesInboxLabel -v
go test ./internal/storage -run TestDurableOptimisticFlagAction -v
MELDMAIL_PLAYWRIGHT_MODULE=/tmp/meldmail-browser/node_modules/playwright go test ./internal/api -run TestMailActionsBrowser -v
```

The 20MiB RSS acceptance runs without race instrumentation; functional protocol tests also run in the race suite. Browser acceptance uses only local in-memory IMAP/SMTP fixtures and never sends mail to an external recipient.

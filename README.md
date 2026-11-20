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

Implementation currently covers M0–M3, including the responsive unified inbox, search and background real-time sync. Complete mail reading/sending and MCP are implemented in later milestones.

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

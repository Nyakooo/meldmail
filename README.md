# MeldMail

**A lightweight, self-hosted unified email client built with Go.**

MeldMail brings multiple IMAP email accounts into one clean, responsive web interface. It is designed for personal self-hosting, low resource consumption, reliable background synchronization, and AI-agent integration through MCP.

**Core principles**
- One unified inbox for multiple email accounts.
- Background synchronization that continues when the browser is closed.
- Metadata-first synchronization with on-demand local reading: opened, sanitized bodies are saved in SQLite; attachments remain streamed from the source mailbox.
- Per-account Direct / HTTP(S) CONNECT / SOCKS5 proxy settings.
- REST + SSE for the web application, and a standard MCP Server for external MCP-compatible clients and AI agents.
- A single Go service and SQLite database; no Redis, message broker, or separate database server.

## Project status

M0–M6 are implemented and locally accepted. Provider behavior is covered by local protocol fixtures; connecting a real mailbox requires its own IMAP/SMTP access and application password. OAuth token refresh remains an extension hook.

## Visual identity & interface

MeldMail uses a compact, three-pane desktop mail workspace and a focused mobile inbox. Both Light and Dark themes are first-class design targets.

![MeldMail official logo](assets/brand/meldmail-logo.svg)

### Desktop concept

![MeldMail desktop Light and Dark design](docs/design/desktop-light-dark.svg)

### Mobile concept

![MeldMail mobile Light and Dark design](docs/design/mobile-concept.svg)

The interface is intentionally limited to personal email workflows: unified inbox, unread, starred, sent, drafts, archive, trash, account switching, search, reading, composing, replies, forwards and attachments. It does not include calendars, contacts, groups, team collaboration or unrelated productivity modules.

See [DESIGN.md](docs/DESIGN.md) for the brand assets, layout rules, theme direction and mobile interaction guidelines. These images are visual references; they do not imply that every pictured UI detail is already implemented.

## Documentation

- [Development specification](SPEC.md)
- [Visual design and brand guide](docs/DESIGN.md)
- [License](LICENSE)

## License

MeldMail is licensed under the Apache License 2.0. See [LICENSE](LICENSE).

Third-party dependencies retain their own licenses. See the dependency notices shipped with each release.

## Local development

Requires Go 1.24+, Node.js 20.19+ and npm. Build the embedded web assets before compiling Go:

```sh
npm ci --prefix web
npm run build --prefix web
go test -race ./...
go vet ./...
go run ./cmd/meldmail
```

Set `MELDMAIL_MASTER_KEY` to a random 32-byte key encoded as 64 hexadecimal characters (`openssl rand -hex 32`). Set `MELDMAIL_ADMIN_PASSWORD` on first startup; only its bcrypt hash is stored, and subsequent starts do not overwrite it. Passwords must fit bcrypt's 72-byte limit. Keep the master key securely backed up. Sessions expire after 24 hours and are revoked on restart.

For Docker, copy `.env.example` to `.env`, set a strong password and master key, then run `docker compose up --build -d`. The service binds to `127.0.0.1:18085`, stores SQLite metadata and opened-body caches under `./data`, and runs with a 256MB / 0.5 CPU limit. Do not commit `.env` or `data`. Only trust proxy headers when access is restricted to your reverse proxy.

The responsive mail client, background synchronization and controlled MCP access are implemented. See [acceptance evidence](docs/ACCEPTANCE.md) for the final test scope and measured resource use.

After starting the M0 Compose container, run `python3 scripts/check-container.py` to verify the embedded Vue entry, WAL database, idle RSS below 30MiB, loopback binding and resource limits. The script uses Python standard-library modules and the Docker CLI.

## Account and proxy API (M1)

Authenticate with `POST /api/auth/login` (`{"password":"..."}`), retaining the session cookie. Account endpoints require this cookie. `POST /api/accounts` accepts `email`, `provider`, and `password`; provider presets include `gmail`, `163`, `126`, `qq`, and `outlook`. For `custom`, also provide `imap_host`, `imap_port`, `imap_tls`, `smtp_host`, `smtp_port`, and `smtp_tls`. TLS modes are `implicit` and `starttls`; certificate verification is always enabled in production. OAuth2 authentication accepts an existing `access_token`; automatic provider token refresh remains an extension hook.

- `GET /api/accounts` lists accounts without credentials.
- `PUT /api/accounts/:id` replaces configuration; omit the password to retain current credentials.
- `DELETE /api/accounts/:id` removes the account and its local metadata.
- `POST /api/accounts/test` accepts a saved `id` or an unsaved account configuration. It returns separate proxy, IMAP and SMTP diagnostics without sending mail.
- `POST /api/accounts/:id/sync` manually synchronizes the latest 100 messages initially, then new UIDs only.
- `GET/PUT /api/settings/proxy` inspects or sets the global proxy (`{"url":"http://user:password@host:port"}`). GET removes user credentials from the response. An empty URL disables the global proxy.

An account's `proxy_mode` can be `global`, `direct`, `http`, `https`, or `socks5`. Explicit proxy modes use `proxy_url`. Both proxy URLs and account credentials are encrypted with the master key before storage. Background sync stores bounded snippets and metadata; opening a message also stores its sanitized body in the bounded SQLite reading cache. Raw MIME and attachment contents are not persisted.

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

Opening a message fetches its BODYSTRUCTURE and selected text/html (preferred) or text/plain part through an independent short-lived IMAP connection. Decoded and sanitized bodies are bounded to 5MiB, and bluemonday removes active content. HTML is rendered in a sandboxed frame; remote tracking images are blocked. After a successful on-demand read, the sanitized HTML or decoded plain text and attachment metadata are saved in SQLite. Later reads, including after restart, use that local body without contacting IMAP. The payload budget is 100MiB / 1000 messages with least-recently-used eviction, and each body remains limited to 5MiB. Deleted/invalidated messages and changed mail sources invalidate their cached bodies. Raw MIME, attachment bytes and pending outgoing approval bodies are not saved. A failed cache write leaves the current body readable and returns a `cache_warning`.

Attachments are fetched by the exact PartID advertised in BODYSTRUCTURE, transfer-decoded and streamed to the HTTP response with `io.Copy`. Forwarded attachments also stream from IMAP into SMTP without a disk cache or full buffering. The forward action automatically includes the source text and attachments; its `text` field is an optional introductory note. Replies set threading headers and default to the original sender when `to` is omitted. Outgoing messages use the sending account's SMTP settings and proxy.

- `GET /api/emails/:id` returns metadata, sanitized `html` or `text`, `attachments`, and cache flags: `cached` means stored locally and `local` means this response was loaded from SQLite.
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

## MCP access and approvals (M5)

Open **AI 与 MCP** in the web sidebar to create an access token. Tokens default to read-only, and write tokens default to requiring approval for sends. The raw token is displayed only once; the server stores its SHA-256 hash. Revocation disables the token immediately and rejects its unapproved requests while preserving audit history.

Configure your MCP client with the Streamable HTTP endpoint `https://your-mail-host/mcp` and header `Authorization: Bearer <your-token>`. The transport is stateless and returns JSON responses. It negotiates MCP versions `2025-03-26`, `2025-06-18`, and `2025-11-25`; standalone GET streams and session DELETE return 405. Clients must advertise `Accept: application/json, text/event-stream` and send JSON. Requests have an 8MiB envelope limit and bounded read/write deadlines. The official Go MCP SDK is used as an external interoperability client in acceptance tests; production request handling uses bounded JSON decoding.

| Permission | Tools |
| --- | --- |
| readonly or readwrite | `list_accounts`, `list_emails`, `search_emails`, `get_email` |
| readwrite | `mark_email_read`, `archive_email` |
| readwrite, optionally requiring approval | `send_email`, `reply_email`, `forward_email` |

`tools/list` publishes each tool's input schema. Mail IDs use `email_id`; send tools use `account_id`, an address-array `to`, optional `cc`, `subject`, `text` or `html`. Replies and forwards also require `email_id`. When send/reply provides HTML, HTML is the effective body shown in approval; forward accepts a text introduction and includes the original message. Read-only tokens receive HTTP 403 for write tools. Email contents remain untrusted input rather than instructions to the consuming agent.

For approval-enabled tokens, send tools return JSON text content containing `{"status":"pending_approval","action_id":"..."}`. The web application receives an SSE notification and opens a review dialog showing sender, recipients, subject and body. Forward/reply requests can also preview the original mail. **批准并发送** claims the action once and performs SMTP; duplicate approval requests cannot send it twice. Rejection never sends. An uncertain SMTP outcome is recorded for manual checking, never automatically retried. If only post-delivery audit recording fails, the completed result is retained with `audit_warning`; it is not reported as an ordinary send failure.

Approval content is kept only in memory: at most 32 actions and 16MiB total payload. SQLite `pending_actions.payload_json` contains metadata and a body digest, never the body. The approval deadline is 15 minutes; expired requests are rejected on review/access. After restart, pending requests fail because their body was deliberately not persisted; interrupted approved requests are marked as an uncertain outcome requiring an outbox check. Approval history and audit summaries omit bodies and raw tokens.

Admin endpoints require the web session cookie:

- `GET/POST /api/mcp/tokens`, `DELETE /api/mcp/tokens/:id`.
- `GET /api/mcp/approvals` returns body-free status metadata; `GET /api/mcp/approvals/:id` returns one review payload.
- `POST /api/mcp/approvals/:id/approve` or `/reject` decides a pending action.
- `GET /api/mcp/audit` returns the latest 100 redacted audit entries.

M5 acceptance commands:

```sh
go test ./internal/mcp -v
MELDMAIL_PLAYWRIGHT_MODULE=/tmp/meldmail-browser/node_modules/playwright go test ./internal/api -run TestMCPApprovalBrowser -v
```

These tests discover and call all nine tools through the official SDK, verify permission boundaries and concurrency, and exercise real browser approval/rejection against local SMTP fixtures.

## Production deployment

Copy `.env.example` to `.env` and fill in `MELDMAIL_MASTER_KEY` (generate with `openssl rand -hex 32`) and a strong `MELDMAIL_ADMIN_PASSWORD`. Restrict the file with `chmod 600 .env`. Then:

```sh
./deploy.sh
docker compose logs --tail=50 meldmail
```

`deploy.sh` pulls `ghcr.io/nyakooo/meldmail:latest`, starts the service without building on the server, waits up to 90 seconds for its HTTP health check, and removes only unused dangling MeldMail images. A failed pull leaves the running service in place; a failed health check exits without cleaning images. Existing `.env` and `data/` are preserved. Run the script from an updated repository checkout; it also honors your local Compose override file. Docker Compose v2.20+ is required. If GHCR access is private, run `docker login ghcr.io` with a token that has package read access first.

To select a particular release, set `MELDMAIL_IMAGE=ghcr.io/nyakooo/meldmail:sha-<full-commit-sha>` (or a digest) in `.env` and run `./deploy.sh`. To clean only this project's dangling images without deploying, run `./deploy.sh --cleanup-only`. Images referenced by existing containers, other projects' images, and build caches are retained.

Pushes to `master` publish `latest` and a full commit SHA tag through [Publish GHCR image](https://github.com/Nyakooo/meldmail/actions/workflows/ghcr.yml). Version tags matching `v*` publish the matching image tag. Images support Linux amd64 and arm64. Wait for the workflow to finish successfully before running the server deployment script.

The default host port is `18085`. Set `MELDMAIL_HTTP_PORT=54321` in `.env` to use another port, then rerun `./deploy.sh`; update your Nginx `proxy_pass` to the same port. The container continues listening on port `8080`.

For a local source build, use `docker compose up --build -d`.

The deployment consists of one container. The host endpoint is `http://127.0.0.1:18085`; only the host reverse proxy should expose it. Compose sets both Docker's `mem_limit: 256m` / `cpus: 0.5` and deployment resource limits, with `GOMEMLIMIT=200MiB` and `GOGC=50`. SQLite uses WAL, a 16MiB page cache and in-memory temporary storage. The master key is required at every startup; changing it makes existing encrypted account credentials unreadable.

### Host Nginx and Certbot

Replace `mail.example.com` and certificate paths below. Obtain a certificate on the host (for example with `certbot --nginx -d mail.example.com` after configuring DNS and an HTTP server block). The certificate and Nginx stay on the host; the container listens only on HTTP.

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

    # SSE and MCP must pass through without buffering or caching.
    location ~ ^/(api/events|mcp)$ {
        proxy_pass http://127.0.0.1:18085;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_read_timeout 86400s;
    }

    location / {
        proxy_pass http://127.0.0.1:18085;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        # Keep request bodies and attachments streaming without temporary files.
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_read_timeout 300s;
    }
}
```

Run `sudo nginx -t` before reloading Nginx. Compose enables `MELDMAIL_TRUST_PROXY=true`; restrict container access to the host proxy so clients cannot forge forwarded headers. HTTPS requests receive Secure session cookies. For direct HTTP development, the default trust-proxy setting is false. Certbot renews certificates independently of the app.

### Upgrades and backups

Keep the master key together with a protected metadata backup. Stop the service before copying `data/` so the database and any WAL files form a consistent snapshot, then restart it. Credentials, tokens, account settings, snippets, pending flag changes and locally cached opened bodies are in SQLite. Backups now contain email bodies and should be protected accordingly; attachments and raw MIME remain in the source mailbox. Outstanding MCP approval bodies are intentionally lost on restart and must be resubmitted.

```sh
docker compose stop
# Copy data/ and .env to your protected backup location.
docker compose up --build -d
```

For a rollback, use the previous source revision/image together with its matching metadata backup. Never change or lose the master key during upgrades. The repository provides a source-build deployment; an image tag in Compose does not imply that a registry release has been published.

## Final acceptance (M6)

```sh
npm ci --prefix web
npm run build --prefix web
go test -race ./...
go vet ./...
docker compose build
python3 scripts/stress-container.py
```

`stress-container.py` requires Docker Compose 2.24.4+ (or newer), Python 3 and OpenSSL. It starts one isolated app container using the real Compose resource/runtime settings, a temporary metadata directory and an ephemeral loopback HTTP port. Local TLS IMAP fixtures run in the test process on the host; the container reaches them through `host-gateway`. No real mailbox is contacted. The script checks 10 background accounts and IDLE connections, a maximum of three simultaneous syncs, repeated REST/MCP 5MiB body reads, 24 concurrent slow-reader requests with body-response memory serialized, and 20MiB streaming downloads while 15MiB of approval bodies remain in memory, RSS below 180MiB, absence of OOM/restarts, embedded frontend assets, sanitized-body-only persistence (no raw MIME, attachment bytes or outgoing approval bodies) and graceful shutdown with SSE open. Its temporary container and data are removed on exit; the regular `meldmail` deployment is untouched.

Real-browser checks can also be included in the final race suite:

```sh
npm install --prefix /tmp/meldmail-browser playwright
MELDMAIL_PLAYWRIGHT_MODULE=/tmp/meldmail-browser/node_modules/playwright go test -race ./...
```

They use `/usr/bin/google-chrome` by default (`MELDMAIL_CHROME` overrides it). Dependency notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md); the official MCP SDK is a test-only dependency and is not linked into the production server.

The accepted M6 run measured **98.28MiB peak process RSS**, three concurrent syncs and six simultaneously paused slow responses, with no OOM or container restart. Shutdown with SSE open took 0.44 seconds. Measurements describe the local fixture workload, not a guarantee for arbitrary mailbox servers or unlimited clients. Run `scripts/update-notices.py` after changing Go or Vue runtime dependencies.

### Provider partial-response compatibility

Some QQ IMAP responses exceed the requested 4KiB snippet partial. MeldMail still requests `BODY.PEEK[TEXT]<0.4096>`, retains only the first 4KiB and discards any extra literal bytes as a stream before reading the next message. The connection context bounds stalled transfers, and the stored snippet remains limited to 500 characters. The IMAP library includes the upstream fix that releases literal readers on connection errors, so aborting a body download cannot deadlock the account worker.

Focused regression checks after account-form or snippet compatibility changes:

```sh
npm run build --prefix web
node scripts/check-account-form.cjs
go test ./internal/mail/imapclient -run 'TestSyncProviderIgnoresPartialLength|TestOversizedBodyAbortDoesNotDeadlock' -v
```

The account-form browser check uses isolated API fixtures and the same optional Playwright/Chrome installation described above; it never saves or contacts a real mailbox.

## Changing the administrator login password

Sign in with the current MeldMail administrator password, then choose **修改登录密码** in the sidebar. Enter the current password, new password and confirmation. Saving stores a bcrypt hash, signs out all web sessions (including existing SSE streams), and asks you to sign in with the new password. Mail accounts, encrypted credentials, metadata and MCP tokens are preserved. Passwords must be non-empty and fit bcrypt's 72-byte limit.

`MELDMAIL_ADMIN_PASSWORD` in `.env` is used only to initialize an empty installation. Editing it, restarting Docker or recreating the container does not replace the existing database password. Likewise, `docker exec meldmail printenv MELDMAIL_ADMIN_PASSWORD` displays the container's startup value, not necessarily the current login password. After initialization, change the password through the web entry above. Keep the existing `MELDMAIL_MASTER_KEY`; it encrypts mailbox credentials and is unrelated to the login password.

The authenticated endpoint is `POST /api/auth/password` with `current_password` and `new_password`. Wrong current passwords do not end the caller's session; repeated attempts are rate limited. Passwords and hashes are never returned in API responses or logged.

### Local reading cache

The 2026-11-24 requirement update permits on-demand sanitized body storage in `email_bodies`; prior metadata-only acceptance records describe the earlier design. No complete bodies are fetched during background sync. A first successful read saves the selected body, and subsequent reads prefer SQLite while the entry is retained, even offline or after restart. Capacity eviction may require an older message to be fetched again. The 100MiB limit covers cached content, not the physical SQLite/WAL file size. Attachments remain on the source mailbox and require a network connection when downloaded. HTML keeps the same sanitizer and sandbox protections when loaded locally. This is a reading cache, not a complete email archive; search still uses headers and the 500-character snippet.

## Runtime usage, mailbox counts and pagination

The inbox displays local mail counts, memory and disk usage in a compact strip, plus a count beside each mailbox. CPU counters remain available through the API for diagnostics and are not displayed in the inbox. Counts mean **messages already synchronized into local SQLite**, not the remote mailbox's full history; initial synchronization still fetches the most recent 100. The global count includes cached metadata in archive/trash folders; the list's filtered total follows its current folder, account, unread and search filters.

The authenticated `GET /api/stats` endpoint returns mailbox totals/unread counts and memory, CPU and disk counters. Linux containers use cgroup memory usage/limits and CPU quota when available; CPU 100% represents the configured quota (0.5 cores in Compose). The first CPU sample has no percentage until another sample is collected. Native process counters are used when container counters are unavailable; unsupported platforms identify unavailable counters instead of inventing values. Disk data usage includes the SQLite database, WAL and SHM files; filesystem available/total space is also shown. It does not count Docker image size. Metrics refresh approximately every 15 seconds while the page is visible, using on-demand OS reads with no metrics daemon or monitoring dependency.

Mail lists now expose `total` alongside `emails` and `next_cursor`. Previous/next controls retain cursor history at 50 messages per page; changing filters/search resets to the first page. SSE updates revalidate the active page and aggregates, so a new arrival does not insert a newer message into a later page or leave an invalid next-page boundary.

The favicon uses the current approved mark, with a versioned URL to replace previously cached icons.

### Appearance

The workspace follows `docs/DESIGN.md`: a compact blue three-column desktop layout and mobile bottom navigation. Choose Light, Dark or System from the toolbar (also available on the login screen); explicit choices are retained in this browser. The inbox status line shows mail counts, memory and disk usage; CPU is available only through the diagnostics API.

### Simplified categories and reading feedback

Unread is an independent category across accounts and folders (excluding Trash); it no longer inherits the previous folder. There is no additional unread checkbox. The toolbar uses a one-click Light/Dark button and an EN/中文 switch; both choices persist in this browser. HTML message colors follow the selected theme while the existing sanitizer, sandbox and remote-image blocking remain active.

Reading shows whether the body was saved or loaded locally. A queued archive/delete remains visible in its destination category with a pending marker until the provider UID is confirmed; no message is represented as permanently deleted or successfully moved before confirmation. Standard fixture move flows are verified; a provider returning an unusable destination UID can remain pending for recovery.

Saving an account closes settings, clears stale search/category filters, selects that account and shows synchronization status. A failed save keeps the form open with entered values intact. Saving configuration does not prove connectivity: test results distinguish TLS/network failure from password authentication. Gmail app passwords are accepted; default proxy mode with no configured endpoint connects directly.

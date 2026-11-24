# MeldMail acceptance evidence

Accepted locally on 2026-11-23 against `SPEC.md` and `AGENTS.md`. Each milestone was implemented, tested and committed before the next began. No registry image, GitHub release or public deployment was published.

| Stage | Delivery commits | Evidence |
| --- | --- | --- |
| M0 | `2bc68d8`, `3498147` | Single Compose service, embedded Vue HTTP 200, SQLite WAL, encrypted credentials, bcrypt session authentication; initial idle RSS 9.55MiB. |
| M1 | `27d6bfd` | Direct/HTTP/HTTPS CONNECT/SOCKS5 protocol tests, account diagnostics, 163 ID, metadata/snippet limits, initial and incremental sync, UIDVALIDITY reset. |
| M2 | `a94b5e9` | Mixed-account stable pagination and filters; FTS5 Chinese/English/six-digit searches under 50ms in non-race tests; responsive inbox. |
| M3 | `51e8378` | IDLE/re-IDLE, scheduled reconciliation, retry/polling transitions, global sync limit, disconnect recovery and shutdown; Chrome SSE insertion. |
| M4 | `45144a2`, `47079c2` | Sanitized bounded bodies, streaming attachment and forwarding paths; Chrome read/download/send/reply/forward/flags/archive/trash; durable MOVE recovery and Gmail label archive. |
| M5 | `7cdd04f`, `88deb72` | Official MCP Go SDK lists and calls all nine tools; readonly write calls HTTP 403; Chrome SSE approval sends exactly once, rejection never sends, token revocation; HTML effective-body preview and post-send audit-failure handling. |
| M6 | See current M6 commit | Final race suite, TypeScript/Vite build, production-image resource acceptance, deployment documentation and dependency notices. |

## Final commands and results

- `MELDMAIL_PLAYWRIGHT_MODULE=/tmp/meldmail-browser/node_modules/playwright go test -race ./...`: PASS for every package with tests, including the three real-Chrome flows. Packages with no test files compile successfully. The separate non-race streaming RSS test is intentionally excluded from race instrumentation; M6 tests the production binary's streaming path in Docker.
- `go vet ./...`: PASS.
- `npm run build --prefix web`: PASS (`vue-tsc --noEmit` and Vite). JavaScript 111.83kB / gzip 41.45kB; CSS 16.19kB / gzip 4.19kB. The Docker multi-stage build embeds these assets; the acceptance script fetches the served JavaScript and compares it to local assets when present.
- `docker compose build`: PASS. The runtime image contains the Go binary, Apache-2.0 license and third-party notices.
- `python3 scripts/stress-container.py`: PASS, using the real deployment settings with only test data/port/CA isolated.

## Production-image stress result

- Docker hard limits: 256MiB memory, 0.5 CPU; `GOMEMLIMIT=200MiB`, `GOGC=50`; HTTP published only on loopback.
- Ten independently configured accounts synchronized in the background and retained ten IDLE connections; metadata sync concurrency reached three and never exceeded it.
- Three unapproved 5MiB messages retained 15MiB in the bounded MCP approval memory queue, with no SMTP operation.
- Three load waves completed 24 REST and 12 MCP reads of 5MiB HTML bodies, plus twelve streamed 20MiB attachment downloads. A further 24 response readers each paused for three seconds after receiving headers; six pauses overlapped at peak.
- Kernel process RSS high-water mark: **98.28MiB**, below the 180MiB ceiling. No OOM or restart occurred; all ten IDLE connections survived interactive traffic.
- An open SSE stream did not prevent graceful shutdown: exit code 0 in **0.44 seconds**.
- SQLite remained in WAL mode and held ten metadata rows with 18-character snippets. All table columns, including FTS/approval tables, were checked for fixture body/attachment markers. The data volume contained only SQLite files; `docker diff` showed only the fixture CA mount point in the writable layer. Source inspection found no `io.ReadAll` or body/attachment filesystem writes in production code.
- The test container, isolated network, temporary metadata and fixture certificates were removed after acceptance. The existing local development deployment was not used as test data.

## Scope and reproducibility

Go 1.24.13, Vue 3/TypeScript/Vite, Docker Engine 29.10.0 and Compose 5.8.0 were used locally. See the README for setup and exact commands. Memory uses the non-race production process's Linux `VmHWM`, not the Go test process or a browser's RSS.

Protocol and browser acceptance use generated local TLS IMAP/SMTP fixtures. No external recipient receives test mail. Real Gmail/Outlook/163/QQ accounts and host DNS/Certbot require the operator's credentials/domain; those live services were not claimed as verified. Password/application-password authentication is implemented; XOAUTH2 and refresh hooks are reserved as specified, not an automatic OAuth enrollment/refresh product.

## 2026-11-24 requirement update: on-demand local bodies

The user authorized sanitized body persistence after the original M0–M6 metadata-only design. `SPEC.md` and `AGENTS.md` now permit this one bounded reading-cache path; the earlier evidence above remains a historical record.

- `go test -race ./...` and `go vet ./...`: PASS after the storage/read-path change. The authenticated API browser scenarios remain covered.
- `npm run build --prefix web`: PASS. The UI layout is retained; a failed cache write can show a reading warning.
- New tests prove: background sync leaves the body cache empty; first read stores sanitized content; a reopened store/service reads locally without a network attempt, even when network slots are busy; slow response consumption retains its memory slot; cache write/maintenance failures leave the body readable with a warning.
- Storage tests cover the actual 100MiB byte budget, 1000-entry LRU budget, 5MiB body limit, migration of an existing database, policy/source mismatch, UID moves, UIDVALIDITY resets, account-source changes and cascading deletion.
- Updated production-image stress: 10 accounts/IDLE connections, 15MiB pending approval bodies, 24 REST + 12 MCP body reads, 24 concurrent slow-reader requests (14 paused responses at peak) and twelve 20MiB streamed downloads. Ten sanitized bodies were present in SQLite and stayed within the 100MiB payload budget. Outgoing approval bodies, raw MIME and attachment bytes were not persisted. RSS peak **128.66MiB**, maximum sync concurrency 3, no OOM/restart, graceful SSE shutdown **1.60s**.
- New migration `002_email_bodies.sql` preserves existing mailbox metadata and administrator settings. The 100MiB limit covers body-cache payloads; SQLite pages, indexes and WAL add filesystem overhead.

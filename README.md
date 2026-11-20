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

Implementation currently covers M0. Later milestone features are not available yet.

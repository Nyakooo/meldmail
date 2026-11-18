# MeldMail

**A lightweight, self-hosted unified email client built with Go.**

MeldMail brings multiple IMAP email accounts into one clean, responsive web interface. It is designed for personal self-hosting, low resource consumption, reliable background synchronization, and AI-agent integration through MCP.

**Core principles**
- One unified inbox for multiple email accounts.
- Background synchronization that continues when the browser is closed.
- Metadata-first storage: message bodies and attachments stay in the source mailbox and are fetched on demand.
- Per-account Direct / HTTP(S) CONNECT / SOCKS5 proxy settings.
- REST + SSE for the web application, and MCP for AI agents such as Coze.
- A single Go service and SQLite database; no Redis, message broker, or separate database server.

## Project status

MeldMail is in the planning and early implementation stage. Features, commands, configuration formats, and provider compatibility may change. Check the development specification and release notes before deploying.

## Documentation

- [Development specification](docs/DEVELOPMENT.md)
- [License](LICENSE)

## License

MeldMail is licensed under the Apache License 2.0. See [LICENSE](LICENSE).

Third-party dependencies retain their own licenses. See the dependency notices shipped with each release.

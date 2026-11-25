# MeldMail

[简体中文](README.md) | **English**

A self-hosted email web client built with Go, Vue 3 and SQLite. Runs in one container and provides an MCP endpoint.

- Unified inbox, search, read status, stars, archive and restore from Trash.
- Background IMAP synchronization and live updates over SSE.
- Reading, sending, replies, forwards and streamed attachment downloads.
- Per-account direct, HTTP(S) CONNECT or SOCKS5 connections.
- MCP token permissions, send approvals and audit records.
- Chinese/English UI, light/dark themes and a mobile layout.

## Quick deployment

Requires Docker Engine, Docker Compose v2.20+, Git and OpenSSL. Run these commands on the server:

```sh
git clone https://github.com/Nyakooo/meldmail.git
cd meldmail
[ -f .env ] || cp .env.example .env
chmod 600 .env
openssl rand -hex 32
```

Edit `.env`: put the generated key in `MELDMAIL_MASTER_KEY` and set the administrator password:

```dotenv
MELDMAIL_HTTP_PORT=18085
MELDMAIL_MASTER_KEY=replace_with_the_generated_64_hex_characters
MELDMAIL_ADMIN_PASSWORD='replace_with_a_strong_password'
```

Passwords are limited to **72 bytes**, not 72 Unicode characters. Single quotes preserve characters such as `$` and `#` instead of interpreting them as Compose interpolation or comments. Do not commit `.env`.

Once the [GHCR publishing workflow](https://github.com/Nyakooo/meldmail/actions/workflows/ghcr.yml) has succeeded, run:

```sh
./deploy.sh
```

The script pulls `ghcr.io/nyakooo/meldmail:latest`, starts the container, waits up to 90 seconds for its HTTP health check, then removes this project's unreferenced dangling images. A failed pull leaves the running container in place; a failed health check skips image cleanup. Existing `.env` and `data/` are preserved.

The default host listener is `127.0.0.1:18085`; use host Nginx for external HTTPS access. The container still listens on `8080`, without reserving host port 8080. Default limits are 256MiB of memory and 0.5 CPU. If the GHCR package is private, first run `docker login ghcr.io` with an account that has package read access.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `MELDMAIL_HTTP_PORT` | `18085` | Host port; for example `54321`. Also update the Nginx upstream when changing it. |
| `MELDMAIL_MASTER_KEY` | Required | A 64-character hexadecimal encryption key. Preserve the original key when reusing data. |
| `MELDMAIL_ADMIN_PASSWORD` | Required on first startup | Initializes a database that has no administrator password; never overwrites an existing password. |
| `MELDMAIL_IMAGE` | `ghcr.io/nyakooo/meldmail:latest` | A release tag, `sha-<full-commit-sha>` tag or digest. |

The default Compose YAML explicitly sets `MELDMAIL_ADDR=:8080`, `MELDMAIL_DB_PATH=/data/meldmail.db` and `MELDMAIL_TRUST_PROXY=true`. Changing these three values only in `.env` does not override that YAML; edit Compose or use a local override when needed. The deployment script honors `docker-compose.override.yml`.

## Nginx and HTTPS

See [nginx.conf.example](nginx.conf.example). The following configuration assumes **this Nginx instance terminates HTTPS**. Replace the domain, certificate paths and upstream port. Obtain certificates first, for example with Certbot.

<!-- nginx-example:start -->
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

    location / {
        proxy_pass http://127.0.0.1:18085;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;
        proxy_read_timeout 86400s;
    }
}
```
<!-- nginx-example:end -->

`Host $http_host` preserves the browser's hostname and port; `X-Forwarded-Proto $scheme` reports its external protocol. The backend checks the request Origin against the protocol and Host. Buffering and caching are disabled for SSE, MCP, body responses and streamed attachments. Do not bypass origin validation by clearing `Origin`.

Validate and reload:

```sh
sudo nginx -t && sudo systemctl reload nginx
```

If a CDN or another proxy terminates HTTPS and connects to this Nginx over HTTP, `$scheme` is `http`. Forward the external protocol only from trusted upstream proxies; do not apply this example unchanged. Keep the application port restricted to loopback so clients cannot forge trusted proxy headers.

## Login and password troubleshooting

| Symptom | Check |
| --- | --- |
| The home page itself returns 403 | Domain/server matching, whether the request reaches `proxy_pass`, access restrictions and any frontend proxy. |
| Login returns `403 invalid origin` | `Host`, the external HTTPS protocol, nonstandard external ports and other `location` blocks overriding headers. |
| Login returns `401 invalid credentials` | Application administrator authentication. This is not a mailbox app password or GHCR login. |
| Editing `.env` does not change the password | The database already has one. Removing containers or images does not remove mounted `data/`. |
| 502 or connection refused | Container health, its host listener and the Nginx upstream port. |

Basic checks:

```sh
docker compose ps
curl -I http://127.0.0.1:18085/
docker compose logs --tail=50 meldmail
```

When signed in, choose **Change password** in the sidebar. A successful change revokes every Web session and requires a new login.

### Forgotten password: preserve mailbox data

Set the new `MELDMAIL_ADMIN_PASSWORD` in `.env` first, and **preserve the original `MELDMAIL_MASTER_KEY`**. Run the following from the project directory with Python 3 installed. It backs up the database and removes only the administrator password setting; accounts, cached messages and MCP tokens remain.

```sh
(
set -eu
docker compose stop meldmail
python3 - <<'PY'
import sqlite3
import time
from pathlib import Path

path = Path('data/meldmail.db')
if not path.is_file():
    raise SystemExit('Database not found; check the project directory and volume mount')
backup = path.with_name(f'meldmail-password-backup-{time.time_ns()}.db')
backup.touch(mode=0o600, exist_ok=False)
with sqlite3.connect(f'file:{path}?mode=rw', uri=True) as db:
    with sqlite3.connect(backup) as target:
        db.backup(target)
    db.execute('DELETE FROM settings WHERE key = ?', ('admin_password',))
print(f'Backup: {backup}; administrator password cleared')
PY
./deploy.sh
)
```

After restart, sign in with the password from `.env`. The backup includes encrypted mailbox credentials and cached bodies; protect it like the original data.

### Start a new installation

Use this only when you no longer need the current accounts, message cache, MCP tokens or audit records. It does not delete messages at your email provider. Rename the data directory as a backup instead of deleting it:

```sh
(
set -eu
docker compose stop meldmail
mv data "data.backup.$(date +%Y%m%d-%H%M%S)"
mkdir -p data
./deploy.sh
)
```

The new installation uses the password in `.env`; add your mailboxes again. Backup directories contain sensitive data and must not be committed or published.

## Mailboxes and proxies

Open **Accounts & connections** after signing in. Enter the address and application password, test connectivity, then save. Gmail requires a Google app password rather than the normal Google account password. Automatic Outlook OAuth2 token refresh remains an extension hook.

When no global proxy is configured, the default proxy mode connects directly. Connectivity tests exercise the path **from the container to the provider**, not from your browser to Gmail. Inside Docker, `127.0.0.1` refers to the container itself; a host proxy needs an address reachable from that container.

- TLS failures or connection resets happen before authentication; they do not by themselves prove the password is wrong.
- If the host connects but the container cannot, check DNS, IPv4/IPv6 and proxy reachability separately.
- Trash and archived messages can be restored to Inbox. Move operations stay disabled while awaiting provider confirmation to prevent duplicate submissions.

## Updates, rollback and cleanup

Wait for the relevant GHCR workflow to succeed, then run in the server checkout:

```sh
git pull --ff-only
./deploy.sh
```

To roll back, set `MELDMAIL_IMAGE=ghcr.io/nyakooo/meldmail:sha-<full-commit-sha>` or a known digest in `.env`, then run `./deploy.sh`. This switches the image, not the database. Use a matching backup if a data migration is incompatible.

To clean only this project's dangling images:

```sh
./deploy.sh --cleanup-only
```

Other projects' images, images referenced by containers and build caches are retained. The script does not run global `docker system prune`. Documentation and host port mapping changes do not themselves require a new application image; the current workflow still builds on pushes to `master`.

## Data and backups

`./data` holds SQLite, WAL/SHM files, encrypted mailbox credentials, metadata, opened-body caches and settings. Body cache limits are 100MiB / 1000 messages, with a 5MiB per-message limit. Attachment contents, raw MIME and unsent approval bodies are not persisted.

Stop the container, copy `data/` and `.env` to protected storage, then start it again. Preserve the original master key. Copying only a live `.db` can miss data in the WAL. Unsent approval bodies exist only in memory and must be resubmitted after restart.

## Local development

Requires Go 1.24+, Node.js 20.19+ and npm. Build the frontend before compiling Go:

```sh
npm ci --prefix web
npm run build --prefix web
# Set the master key and initial password in your shell; go run does not load .env.
go run ./cmd/meldmail
```

For a Docker source build, run `docker compose up --build -d`. Follow [AGENTS.md](AGENTS.md) for incremental testing. Run the full `go test -race ./...` for final acceptance or shared foundation changes.

## Developer documentation and license

- [API, protocol and acceptance reference](docs/DEVELOPMENT.en.md)
- [Architecture and requirements (Chinese)](SPEC.md)
- [Acceptance evidence](docs/ACCEPTANCE.md)
- [Interface design reference](docs/DESIGN.md)
- [Third-party notices](THIRD_PARTY_NOTICES.md)
- [Apache-2.0 license](LICENSE)

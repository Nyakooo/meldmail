# Operations

[Back to README](../README.en.md) · [简体中文](OPERATIONS.md)

The script pulls `ghcr.io/nyakooo/meldmail:latest`, starts the container, waits up to 90 seconds for its HTTP health check, then removes this project's unreferenced dangling images. A failed pull leaves the running container in place; a failed health check skips image cleanup. Existing `.env` and `data/` are preserved.

The default host listener is `127.0.0.1:18085`; use host Nginx for external HTTPS access. The container still listens on `8080`, without reserving host port 8080. Default limits are 256MiB of memory and 0.5 CPU. If the GHCR package is private, first run `docker login ghcr.io` with an account that has package read access.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `MELDMAIL_HTTP_PORT` | `18085` | Host port; for example `54321`. Also update the Nginx upstream when changing it. |
| `MELDMAIL_MASTER_KEY` | Required | A 64-character hexadecimal encryption key. Preserve the original key when reusing data. |
| `MELDMAIL_ADMIN_PASSWORD` | Required on first startup | Initializes a database that has no MeldMail sign-in password; never overwrites an existing password. |
| `MELDMAIL_IMAGE` | `ghcr.io/nyakooo/meldmail:latest` | A release tag, `sha-<full-commit-sha>` tag or digest. |

The default Compose YAML explicitly sets `MELDMAIL_ADDR=:8080`, `MELDMAIL_DB_PATH=/data/meldmail.db` and `MELDMAIL_TRUST_PROXY=true`. Changing these three values only in `.env` does not override that YAML; edit Compose or use a local override when needed. The deployment script honors `docker-compose.override.yml`.

## Nginx and HTTPS

See [nginx.conf.example](../nginx.conf.example). The following configuration assumes **this Nginx instance terminates HTTPS**. Replace the domain, certificate paths and upstream port. Obtain certificates first, for example with Certbot.

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
| Login returns `401 invalid credentials` | Check the MeldMail sign-in password configured for this instance. |
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

Set the new `MELDMAIL_ADMIN_PASSWORD` in `.env` first, and **preserve the original `MELDMAIL_MASTER_KEY`**. Run the following from the project directory with Python 3 installed. It backs up the database and removes only the MeldMail sign-in password setting; accounts, cached messages and MCP tokens remain.

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
print(f'Backup: {backup}; MeldMail sign-in password cleared')
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

## Configuration import and export

Open **Accounts & connections → Import / export**. On the source instance, choose a separate configuration password of at least 12 characters and download the encrypted JSON. On the destination, choose that file, enter the same password and select **Import and merge**. The destination encrypts credentials with its own master key; you do not need to copy the source master key or re-enter still-valid app passwords.

Bundles include mailbox connections, existing app passwords / OAuth tokens and proxies. They exclude messages, the MeldMail sign-in password and MCP tokens. Email addresses are matched after trimming whitespace and ignoring case. Repeated imports update the original account and retain its messages. Within a bundle, the last connection wins, while missing credentials retain the earlier values. Validation failures do not write partial configuration. A nonempty shared proxy in the bundle replaces the destination shared proxy, affecting only mailboxes that explicitly use it.

The configuration password is not stored on the server. Keep both the file and its password safe. OAuth tokens remain subject to provider expiry; import does not refresh or extend their lifetime.

Sidebar counts switch immediately with the selected category and update through mail events. Select **Reconnecting** to open the corresponding connection settings and diagnosis.

## Mailboxes and proxies

Open **Accounts & connections** after signing in. Enter the address and application password, test connectivity, then save. Gmail requires a Google app password rather than the normal Google account password. Automatic Outlook OAuth2 token refresh remains an extension hook.

Each mailbox chooses its own connection, and **new mailboxes default to direct connections**. Configure an HTTP, HTTPS or SOCKS5 URL only for mailboxes that need a proxy. To reuse one URL, save it under **Accounts & connections → Shared proxy (optional)**, then explicitly select **Use shared proxy** for those mailboxes. Shared proxy edits do not affect direct or mailbox-specific connections. Connectivity tests exercise the path **from the container to the provider**, not from your browser to Gmail. Inside Docker, `127.0.0.1` refers to the container itself; a host proxy needs an address reachable from that container.

Reopening an editor restores the complete saved proxy URL for review and editing, without requiring a new mailbox app password.

- TLS failures or connection resets happen before authentication; they do not by themselves prove the password is wrong.
- If the host connects but the container cannot, check DNS, IPv4/IPv6 and proxy reachability separately.

## Reading and managing mail

- HTTP(S) links in message bodies open in a new tab. Light mode adjusts link text when it has insufficient contrast. In dark mode, choose **Show original colors** to see the sender’s palette.
- Restore archived or trashed messages to Inbox. Open a trashed message to **Delete permanently**, or select an account and choose **Empty Trash**. After confirmation, the server permanently removes the selected mail. Emptying Trash also removes messages that have yet to sync locally. The mail server must support targeted permanent deletion.
- Open **Settings → AI & MCP → Access token** to revoke a token. Deleting a revoked token also removes its approval records. Audit logs are retained.
- Open **Settings → Diagnostic logs** to view logs. Use the icon in the upper-right corner to maximize or restore the window.

## Optional host proxy port (Linux)

For Mihomo/Clash listening on the deployment server's loopback HTTP or mixed port, set:

```dotenv
MELDMAIL_HOST_PROXY_PORT=7890
```

Run `./deploy.sh`. It automatically adds `docker-compose.host-proxy.yml` and uses host networking to reach the host's loopback proxy. The web server still binds only to `127.0.0.1:${MELDMAIL_HTTP_PORT:-18085}`; Nginx stays unchanged. No public proxy listener is needed. This must be a port on the deployment server, not on the browser's computer.

Select **Use shared proxy** only for mailboxes that need it. Direct and independent mailbox connections remain independent. On each application startup, the environment port takes precedence over the stored shared proxy; unchanged values are not rewritten. The web editor shows the source and becomes read-only. Clearing the variable and redeploying removes the previously environment-managed proxy. If never enabled, manual shared proxy settings remain untouched. MeldMail sign-in passwords and master keys are unaffected.

After editing `.env`, rerun `./deploy.sh` to recreate the container. `docker restart` does not reload `.env`. For source builds:

```sh
docker compose -f docker-compose.yml -f docker-compose.host-proxy.yml up -d --build
```

With no port configured, the default deployment keeps bridge networking. Host mode has no published port mapping in `docker compose ps`; access the same host HTTP port. This option targets Linux Docker Engine and requires Compose support for `!reset`; upgrade the Compose plugin if it reports an unknown tag.

## Releases

Before tagging a version, add user-facing notes in `releases/<tag>.md` and include the file in the tagged commit. Updating that file on `master` also updates an existing GitHub Release.

Pushing a `v*` tag creates the corresponding GitHub Release after the GHCR image is pushed. To create a missing release page for a tag with an existing image, manually run **Publish GHCR image and release** from `master` and enter the tag, such as `v0.1.0`, in `release_tag`. The workflow checks the tagged GHCR image first.

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

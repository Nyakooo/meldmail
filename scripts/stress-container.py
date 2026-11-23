#!/usr/bin/env python3
"""M6 production-image acceptance against local, generated TLS IMAP fixtures.

Requires Docker Compose, Python 3 and openssl. Never touches ./data or sends mail.
Message/attachment bytes are generated in memory and streamed; only certificates,
temporary Compose configuration and the app's metadata database use a temp dir.
"""
import base64
import concurrent.futures
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import re
import secrets
import socketserver
import sqlite3
import ssl
import subprocess
import tempfile
import threading
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
MIB = 1024 * 1024
BODY_SIZE = 5 * MIB
ATTACHMENT_SIZE = 20 * MIB
STRUCTURE = ('(("TEXT" "HTML" ("CHARSET" "UTF-8") NIL NIL "7BIT" '
             f'{BODY_SIZE} 1 NIL NIL NIL NIL)'
             '("APPLICATION" "OCTET-STREAM" NIL NIL NIL "BASE64" '
             f'{4 * ((ATTACHMENT_SIZE + 2) // 3)} NIL '
             '("ATTACHMENT" ("FILENAME" "large.bin")) NIL NIL) '
             '"MIXED" ("BOUNDARY" "fixture") NIL NIL NIL)')
CAPABILITIES = b'IMAP4rev1 IDLE'


class Fixture(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True

    def __init__(self, tls):
        super().__init__(("0.0.0.0", 0), IMAP)
        self.tls = tls
        self.lock = threading.Lock()
        self.syncs = self.peak_syncs = self.idles = 0
        self.errors = []

    def get_request(self):
        conn, address = super().get_request()
        conn.settimeout(120)
        return self.tls.wrap_socket(conn, server_side=True), address


class IMAP(socketserver.StreamRequestHandler):
    def handle(self):
        try:
            self.serve()
        except (ConnectionError, TimeoutError, ssl.SSLError):
            pass  # Expected when the app cancels or shuts down an IDLE connection.
        except Exception as err:
            with self.server.lock:
                self.server.errors.append(str(err))

    def serve(self):
        self.wfile.write(b'* OK [CAPABILITY ' + CAPABILITIES + b'] fixture\r\n')
        while line := self.rfile.readline(8192):
            tag, command = line.rstrip(b'\r\n').split(b' ', 1)
            upper = command.upper()
            done = tag + b' OK done\r\n'
            if upper.startswith(b'LOGIN '):
                self.wfile.write(tag + b' OK [CAPABILITY ' + CAPABILITIES + b'] authenticated\r\n')
            elif upper == b'CAPABILITY':
                self.wfile.write(b'* CAPABILITY ' + CAPABILITIES + b'\r\n' + done)
            elif upper.startswith(b'SELECT '):
                self.wfile.write(b'* FLAGS (\\Seen)\r\n* 1 EXISTS\r\n'
                                 b'* OK [UIDVALIDITY 1] valid\r\n'
                                 b'* OK [UIDNEXT 2] next\r\n' + done)
            elif upper == b'IDLE':
                with self.server.lock:
                    self.server.idles += 1
                try:
                    self.wfile.write(b'+ idling\r\n')
                    if self.rfile.readline(8192).strip().upper() == b'DONE':
                        self.wfile.write(done)
                finally:
                    with self.server.lock:
                        self.server.idles -= 1
            elif upper.startswith((b'FETCH ', b'UID FETCH ')):
                if b'BODY.PEEK[TEXT]' in upper:
                    with self.server.lock:
                        self.server.syncs += 1
                        self.server.peak_syncs = max(self.server.peak_syncs, self.server.syncs)
                    try:
                        time.sleep(0.2)  # Make overlapping metadata fetches observable.
                        snippet = (b'--fixture\r\nContent-Type: text/html; charset=UTF-8\r\n'
                                   b'\r\n<p>M6 fixture snippet</p>\r\n--fixture--\r\n')
                        envelope = ('("20 Nov 2026 12:00:00 +0000" "M6 stress fixture" '
                                    '(("Sender" NIL "sender" "example.com")) NIL NIL '
                                    '((NIL NIL "recipient" "example.com")) NIL NIL NIL "<m6@example.com>")')
                        head = (f'* 1 FETCH (UID 1 FLAGS () INTERNALDATE "20-Nov-2026 12:00:00 +0000" '
                                f'ENVELOPE {envelope} BODYSTRUCTURE {STRUCTURE} '
                                f'BODY[TEXT]<0> {{{len(snippet)}}}\r\n').encode()
                        self.wfile.write(head + snippet + b')\r\n' + done)
                    finally:
                        with self.server.lock:
                            self.server.syncs -= 1
                elif b'BODYSTRUCTURE' in upper:
                    self.wfile.write(f'* 1 FETCH (UID 1 BODYSTRUCTURE {STRUCTURE})\r\n'.encode() + done)
                elif b'BODY.PEEK[1]' in upper:
                    self.wfile.write(f'* 1 FETCH (UID 1 BODY[1]<0> {{{BODY_SIZE}}}\r\n'.encode())
                    self.wfile.write(b'<p>')
                    self.repeat(b'a', BODY_SIZE - 7)
                    self.wfile.write(b'</p>)\r\n' + done)
                elif b'BODY.PEEK[2]' in upper:
                    size = 4 * ((ATTACHMENT_SIZE + 2) // 3)
                    self.wfile.write(f'* 1 FETCH (UID 1 BODY[2] {{{size}}}\r\n'.encode())
                    chunk = bytes(3 * 16384)
                    full, remainder = divmod(ATTACHMENT_SIZE, len(chunk))
                    encoded = base64.b64encode(chunk)
                    for _ in range(full):
                        self.wfile.write(encoded)
                    if remainder:
                        self.wfile.write(base64.b64encode(bytes(remainder)))
                    self.wfile.write(b')\r\n' + done)
                else:
                    self.wfile.write(b'* 1 FETCH (UID 1 FLAGS ())\r\n' + done)
            elif upper.startswith(b'LIST '):
                self.wfile.write(b'* LIST (\\Inbox) "/" "INBOX"\r\n' + done)
            elif upper == b'LOGOUT':
                self.wfile.write(b'* BYE closing\r\n' + done)
                return
            elif upper == b'NOOP':
                self.wfile.write(done)
            else:
                raise ValueError('unexpected IMAP command: ' + upper[:64].decode())

    def repeat(self, byte, count):
        chunk = byte * 65536
        full, remainder = divmod(count, len(chunk))
        for _ in range(full):
            self.wfile.write(chunk)
        self.wfile.write(chunk[:remainder])


def docker(*args, **kwargs):
    return subprocess.check_output(['docker', *args], text=True, timeout=30, **kwargs).strip()


def main():
    image = os.getenv('MELDMAIL_TEST_IMAGE', 'ghcr.io/nyakooo/meldmail:latest')
    with tempfile.TemporaryDirectory(prefix='meldmail-m6-') as directory:
        tmp = Path(directory)
        os.chmod(tmp, 0o755)  # The runtime can read the mounted fixture CA.
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                        '-days', '1', '-subj', '/CN=meldmail-fixture',
                        '-addext', 'subjectAltName=DNS:meldmail-fixture',
                        '-keyout', str(tmp / 'key.pem'), '-out', str(tmp / 'ca.pem')],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.load_cert_chain(tmp / 'ca.pem', tmp / 'key.pem')
        with Fixture(tls) as fixture:
            serving = threading.Thread(target=fixture.serve_forever, daemon=True)
            serving.start()
            try:
                run_container(tmp, fixture, image)
            finally:
                fixture.shutdown()
                serving.join()


def run_container(tmp, fixture, image):
    name = 'meldmail-m6-' + secrets.token_hex(4)
    password, master = secrets.token_urlsafe(24), secrets.token_hex(32)
    environment = dict(os.environ, MELDMAIL_MASTER_KEY=master, MELDMAIL_ADMIN_PASSWORD=password)
    (tmp / 'data').mkdir()
    # Inherit the real deployment's resource/runtime settings; isolate only name,
    # image, host port, DB directory and the CA for our local TLS fixture.
    # Reset inherited lists with Compose tags, preventing the original data/port
    # from being mounted or bound. Temporary configuration contains no secrets.
    config = tmp / 'compose.yml'
    config.write_text('services:\n  meldmail:\n'
                      f'    image: {json.dumps(image)}\n    container_name: {name}\n'
                      '    ports: !override\n      - "127.0.0.1::8080"\n'
                      '    volumes: !override\n'
                      f'      - {json.dumps(str(tmp / "data") + ":/data")}\n'
                      f'      - {json.dumps(str(tmp / "ca.pem") + ":/fixture-ca.pem:ro")}\n'
                      '    extra_hosts:\n      - "meldmail-fixture:host-gateway"\n'
                      '    environment:\n      SSL_CERT_FILE: /fixture-ca.pem\n')
    compose = ['docker', 'compose', '-p', name, '-f', str(ROOT / 'docker-compose.yml'), '-f', str(config)]
    stop_samples = threading.Event()
    samples = []
    sampling_errors = []
    sampler = None
    try:
        subprocess.run(compose + ['up', '-d', '--no-build', '--pull', 'never'], env=environment, check=True)
        host_config = json.loads(docker('inspect', '--format', '{{json .HostConfig}}', name))
        assert host_config['Memory'] == 256 * MIB
        assert host_config['NanoCpus'] == 500_000_000
        binding = host_config['PortBindings']['8080/tcp'][0]
        assert binding['HostIp'] == '127.0.0.1'
        env = json.loads(docker('inspect', '--format', '{{json .Config.Env}}', name))
        assert 'GOMEMLIMIT=200MiB' in env and 'GOGC=50' in env
        docker('exec', name, 'sh', '-c', 'test -s /app/LICENSE && test -s /app/THIRD_PARTY_NOTICES.md')
        port = docker('port', name, '8080/tcp').rsplit(':', 1)[1]
        base = 'http://127.0.0.1:' + port
        jar = http.cookiejar.CookieJar()
        client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

        def request(path, data=None, method=None):
            body = None if data is None else json.dumps(data).encode()
            req = urllib.request.Request(base + path, data=body, method=method,
                                         headers={'Content-Type': 'application/json'})
            with client.open(req, timeout=90) as response:
                return json.load(response)

        deadline = time.monotonic() + 30
        while True:
            try:
                with urllib.request.urlopen(base, timeout=1) as response:
                    html = response.read().decode()
                asset = re.search(r'src="([^"]+\.js)"', html).group(1)
                with urllib.request.urlopen(base + asset, timeout=5) as response:
                    javascript = response.read()
                    assert len(javascript) > 1000, 'embedded asset missing'
                local_asset = ROOT / 'internal' / 'api' / 'dist' / asset.lstrip('/')
                if local_asset.exists():
                    assert hashlib.sha256(local_asset.read_bytes()).digest() == hashlib.sha256(javascript).digest(), 'stale embedded asset'
                break
            except (OSError, AttributeError):
                if time.monotonic() >= deadline:
                    raise
                time.sleep(0.1)
        request('/api/auth/login', {'password': password})

        def sample():
            while not stop_samples.is_set():
                try:
                    status = docker('exec', name, 'cat', '/proc/1/status')
                    rss = int(re.search(r'VmRSS:\s+(\d+)', status).group(1)) * 1024
                    hwm = int(re.search(r'VmHWM:\s+(\d+)', status).group(1)) * 1024
                    samples.append(max(rss, hwm))  # Kernel high-water mark catches brief peaks.
                except Exception as err:
                    sampling_errors.append(str(err))
                    return
                stop_samples.wait(0.05)

        sampler = threading.Thread(target=sample)
        sampler.start()
        for i in range(10):
            request('/api/accounts', {
                'email': f'fixture{i}@example.com', 'provider': 'custom', 'password': 'fixture',
                'imap_host': 'meldmail-fixture', 'imap_port': fixture.server_address[1],
                'imap_tls': 'implicit', 'smtp_host': 'meldmail-fixture',
                'smtp_port': 465, 'smtp_tls': 'implicit', 'proxy_mode': 'direct',
            })
        deadline = time.monotonic() + 40
        while True:
            emails = request('/api/emails')['emails']
            with fixture.lock:
                idles = fixture.idles
            if len(emails) == 10 and idles == 10:
                break
            if time.monotonic() >= deadline:
                raise AssertionError(f'10-account background sync failed: emails={len(emails)}, IDLE={idles}')
            time.sleep(0.1)
        print('PASS: 10 background accounts synchronized, 10 independent IDLE connections', flush=True)
        cookie = '; '.join(f'{item.name}={item.value}' for item in jar)
        token = request('/api/mcp/tokens', {'name': 'M6 memory pressure', 'scope': 'readwrite', 'require_approval': True})['token']

        def mcp(tool, arguments):
            payload = {'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call',
                       'params': {'name': tool, 'arguments': arguments}}
            req = urllib.request.Request(base + '/mcp', data=json.dumps(payload).encode(), headers={
                'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json',
                'Accept': 'application/json, text/event-stream',
                'MCP-Protocol-Version': '2025-11-25',
            })
            with urllib.request.urlopen(req, timeout=120) as response:
                value = json.load(response)
            assert 'error' not in value and not value['result'].get('isError'), 'MCP stress call failed'
            return json.loads(value['result']['content'][0]['text'])

        for i in range(3):
            pending = mcp('send_email', {'account_id': emails[0]['account_id'],
                                       'to': ['unsent@example.com'], 'subject': f'Pending memory {i}',
                                       'text': 'b' * BODY_SIZE})
            assert pending['status'] == 'pending_approval'
        print('PASS: 15MiB pending MCP bodies retained only in bounded memory', flush=True)
        slow_lock = threading.Lock()
        slow = {'active': 0, 'peak': 0, 'completed': 0}

        def read_body(index, pause=False):
            req = urllib.request.Request(base + '/api/emails/' + str(emails[index % 10]['id']),
                                         headers={'Cookie': cookie})
            with urllib.request.urlopen(req, timeout=120) as response:
                if pause:
                    with slow_lock:
                        slow['active'] += 1
                        slow['peak'] = max(slow['peak'], slow['active'])
                    try:
                        time.sleep(3)  # Each response is paused after its own headers arrive.
                    finally:
                        with slow_lock:
                            slow['active'] -= 1
                            slow['completed'] += 1
                value = json.load(response)
            assert len(value['html']) == BODY_SIZE, 'body was truncated or changed'

        def download(index):
            req = urllib.request.Request(base + f'/api/emails/{emails[index % 10]["id"]}/attachments/2',
                                         headers={'Cookie': cookie})
            count = 0
            with urllib.request.urlopen(req, timeout=120) as response:
                while chunk := response.read(65536):
                    count += len(chunk)
            assert count == ATTACHMENT_SIZE, f'incomplete attachment: {count}'

        def mcp_body(index):
            content = mcp('get_email', {'email_id': emails[index % 10]['id']})
            assert len(content['html']) == BODY_SIZE

        # Three waves include contention between body reads and downloads, with
        # more callers than the interactive connection limit.
        with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
            for wave in range(3):
                futures = [pool.submit(read_body, i) for i in range(8)]
                futures += [pool.submit(download, i) for i in range(4)]
                futures += [pool.submit(mcp_body, i) for i in range(4)]
                for future in futures:
                    future.result()
                print(f'PASS: concurrent read/download wave {wave + 1}/3', flush=True)
        # Slow HTTP readers must not retain an unbounded collection of fully
        # decoded bodies after releasing the IMAP connection slot.
        with concurrent.futures.ThreadPoolExecutor(max_workers=24) as pool:
            futures = [pool.submit(read_body, i, True) for i in range(24)]
            for future in futures:
                future.result()
        assert slow['completed'] == 24
        assert slow['peak'] >= 2, 'slow-reader requests did not actually overlap'
        assert request('/api/emails')['emails'], 'API stopped responding after stress'
        with fixture.lock:
            assert fixture.idles == 10, 'interactive IO disrupted IDLE'
            assert 1 <= fixture.peak_syncs <= 3, f'sync limit exceeded: {fixture.peak_syncs}'
            assert not fixture.errors, fixture.errors
        stop_samples.set()
        sampler.join()
        assert not sampling_errors, sampling_errors
        peak = max(samples)
        assert peak < 180 * MIB, f'RSS peak {peak / MIB:.2f}MiB exceeds 180MiB'
        state = json.loads(docker('inspect', '--format', '{{json .State}}', name))
        assert state['Running'] and not state['OOMKilled']
        assert docker('inspect', '--format', '{{.RestartCount}}', name) == '0'
        layer = docker('diff', name).splitlines()
        assert all(line[2:] == '/fixture-ca.pem' for line in layer), f'unexpected writable-layer files: {layer}'
        # A single persistent SSE connection must not prevent graceful exit.
        stream = client.open(base + '/api/events', timeout=10)
        try:
            started = time.monotonic()
            docker('exec', name, 'chown', '-R', f'{os.getuid()}:{os.getgid()}', '/data')
            docker('stop', '--time', '20', name)
            shutdown = time.monotonic() - started
        finally:
            stream.close()
        state = json.loads(docker('inspect', '--format', '{{json .State}}', name))
        assert state['ExitCode'] == 0 and not state['OOMKilled'], state
        with sqlite3.connect(f'file:{tmp}/data/meldmail.db?mode=ro', uri=True) as db:
            assert db.execute('PRAGMA journal_mode').fetchone()[0] == 'wal'
            summary = db.execute('SELECT count(*), max(length(snippet)) FROM emails').fetchone()
            assert summary == (10, 18), f'unexpected metadata/snippet result: {summary}'
            # Inspect all stored values, including FTS and approval metadata.
            # Body/attachment markers must not appear in any table or column.
            tables = db.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall()
            markers = (b'a' * 4096, b'b' * 4096, b'A' * 4096, bytes(4096))
            for (table,) in tables:
                quoted = '"' + table.replace('"', '""') + '"'
                for row in db.execute('SELECT * FROM ' + quoted):
                    for value in row:
                        data = value.encode() if isinstance(value, str) else value if isinstance(value, bytes) else b''
                        assert not any(marker in data for marker in markers), f'mail content persisted in {table}'
            assert db.execute('SELECT count(*) FROM pending_actions').fetchone()[0] == 3
        files = {path.name for path in (tmp / 'data').iterdir()}
        assert files <= {'meldmail.db', 'meldmail.db-wal', 'meldmail.db-shm'}, files
        print(f'PASS: 24 REST + 12 MCP × 5MiB body reads, 24 slow requests '
              f'(peak {slow["peak"]} paused responses), 12 × 20MiB streamed downloads, 15MiB pending bodies; '
              f'RSS peak {peak / MIB:.2f}MiB; max concurrent syncs {fixture.peak_syncs}; '
              f'256MiB/0.5CPU, no OOM/restarts; graceful SSE shutdown {shutdown:.2f}s', flush=True)
    finally:
        stop_samples.set()
        if sampler is not None:
            sampler.join()
        # The production image runs as root. Give only this isolated fixture DB
        # back to the test user before deleting its temporary directory.
        running = subprocess.run(['docker', 'inspect', '--format', '{{.State.Running}}', name],
                                 text=True, capture_output=True)
        try:
            if running.returncode == 0 and running.stdout.strip() == 'true':
                docker('exec', name, 'chown', '-R', f'{os.getuid()}:{os.getgid()}', '/data')
        finally:
            subprocess.run(compose + ['down', '--timeout', '20'], env=environment, check=True)


if __name__ == '__main__':
    main()

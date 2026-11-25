#!/usr/bin/env python3
"""Check the running M0 Compose container without printing its environment."""
import json
import sqlite3
import subprocess
import urllib.request
from pathlib import Path


def docker(*args: str) -> str:
    return subprocess.check_output(['docker', *args], text=True).strip()


container = json.loads(docker('inspect', 'meldmail'))[0]
limits = container['HostConfig']
assert limits['Memory'] == 256 * 1024 * 1024, 'expected 256MiB container limit'
assert limits['NanoCpus'] == 500_000_000, 'expected 0.5 CPU limit'
if limits['NetworkMode'] == 'host':
    environment = dict(entry.split('=', 1) for entry in container['Config']['Env'])
    address = environment['MELDMAIL_ADDR']
    assert address.startswith('127.0.0.1:'), 'expected loopback-only listener'
else:
    binding = container['NetworkSettings']['Ports']['8080/tcp'][0]
    assert binding['HostIp'] == '127.0.0.1'
    address = '127.0.0.1:' + binding['HostPort']
with urllib.request.urlopen('http://' + address, timeout=5) as response:
    assert response.status == 200
    assert b'<div id="app">' in response.read(), 'missing embedded Vue entry'
status = docker('exec', 'meldmail', 'cat', '/proc/1/status')
rss = next(int(line.split()[1]) for line in status.splitlines() if line.startswith('VmRSS:'))
assert rss < 30 * 1024, f'idle RSS exceeds M0 limit: {rss} KiB'
path = Path(__file__).resolve().parents[1] / 'data' / 'meldmail.db'
with sqlite3.connect(f'file:{path}?mode=ro', uri=True) as database:
    mode = database.execute('PRAGMA journal_mode').fetchone()[0]
    assert mode == 'wal', f'expected WAL, got {mode}'
print(f'PASS: Vue HTTP 200, SQLite WAL, idle RSS {rss / 1024:.2f} MiB, 256MiB / 0.5 CPU')

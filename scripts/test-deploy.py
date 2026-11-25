#!/usr/bin/env python3
"""Exercise deployment ordering and image cleanup with a fake Docker CLI."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
FAKE_DOCKER = r'''#!/usr/bin/env python3
import json,os,sys
args=sys.argv[1:]
with open(os.environ['DOCKER_TRACE'],'a') as log: log.write(json.dumps(args)+'\n')
if args[:1]==['compose'] and '--environment' in args:
 print('MELDMAIL_HOST_PROXY_PORT='+os.environ.get('TEST_PROXY_PORT',''))
if args[:2]==['image','ls']:
 print('sha256:project\nsha256:legacy\nsha256:other\nsha256:used')
elif args[:2]==['image','inspect']:
 print({'sha256:project':'meldmail|["/app/meldmail"]|/app','sha256:legacy':'<no value>|["/app/meldmail"]|/app','sha256:other':'other|["/app/other"]|/app','sha256:used':'meldmail|["/app/meldmail"]|/app'}[args[2]])
elif args[:1]==['ps'] and 'ancestor=sha256:used' in args:
 print('existing-container')
if args[:1]==['compose'] and os.environ.get('FAIL_STAGE') in ('pull','up') and os.environ['FAIL_STAGE'] in args:
 sys.exit(1)
'''


class DeployTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        shutil.copy2(ROOT / 'deploy.sh', self.directory / 'deploy.sh')
        shutil.copy2(ROOT / 'docker-compose.host-proxy.yml', self.directory / 'docker-compose.host-proxy.yml')
        (self.directory / 'docker-compose.yml').write_text('services: {}\n')
        (self.directory / '.env').write_text('MELDMAIL_MASTER_KEY=fixture\n')
        binary = self.directory / 'bin'
        binary.mkdir()
        docker = binary / 'docker'
        docker.write_text(FAKE_DOCKER)
        docker.chmod(0o755)
        self.env = {**os.environ, 'PATH': str(binary) + os.pathsep + os.environ['PATH'],
                    'DOCKER_TRACE': str(self.directory / 'trace')}

    def run_script(self, *args, fail=''):
        result = subprocess.run(['sh', str(self.directory / 'deploy.sh'), *args],
                                cwd='/tmp', env={**self.env, 'FAIL_STAGE': fail},
                                text=True, capture_output=True, check=False)
        trace = self.directory / 'trace'
        calls = [json.loads(line) for line in trace.read_text().splitlines()] if trace.exists() else []
        return result, calls

    def test_pull_start_wait_then_project_only_cleanup(self):
        result, calls = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        pull = calls.index(['compose', '--env-file', '.env', 'pull', 'meldmail'])
        up = next(i for i, call in enumerate(calls) if call[0] == 'compose' and 'up' in call)
        cleanup = next(i for i, call in enumerate(calls) if call[:2] == ['image', 'ls'])
        self.assertLess(pull, up)
        self.assertLess(up, cleanup)
        self.assertIn('--no-build', calls[up])
        self.assertIn('--wait', calls[up])
        self.assertEqual([call[2] for call in calls if call[:2] == ['image', 'rm']],
                         ['sha256:project', 'sha256:legacy'])
        self.assertEqual((self.directory / '.env').read_text(), 'MELDMAIL_MASTER_KEY=fixture\n')

    def test_pull_failure_does_not_replace_container_or_clean(self):
        result, calls = self.run_script(fail='pull')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('up' in call or call[:1] == ['image'] for call in calls))

    def test_unhealthy_deployment_does_not_clean(self):
        result, calls = self.run_script(fail='up')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(call[:1] == ['image'] for call in calls))

    def test_optional_host_proxy_uses_override_for_entire_deployment(self):
        self.env['TEST_PROXY_PORT'] = '7890'
        (self.directory / 'docker-compose.override.yml').write_text('services: {}\n')
        result, calls = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        for operation in ('pull', 'up', 'ps'):
            call = next(call for call in calls if call[0] == 'compose' and operation in call)
            self.assertEqual(call[3:9], ['-f', 'docker-compose.yml', '-f', 'docker-compose.override.yml', '-f', 'docker-compose.host-proxy.yml'])

    def test_invalid_port_stops_before_pull(self):
        self.env['TEST_PROXY_PORT'] = '65536'
        result, calls = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('pull' in call or 'up' in call for call in calls))

    def test_cleanup_only_needs_no_env_or_compose(self):
        (self.directory / '.env').unlink()
        (self.directory / 'docker-compose.yml').unlink()
        result, calls = self.run_script('--cleanup-only')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(call[:1] == ['compose'] for call in calls))


if __name__ == '__main__':
    unittest.main()

"""Exercise stable ingress and release admission without production data."""

import json
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest
import urllib.request
import uuid
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("gateway_control", ROOT / "gateway_control.py")
control = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(control)


class GatewayAdmissionTest(unittest.TestCase):
    def test_legacy_standby_cannot_reclaim_gateway_port(self):
        """Rollback must reject a legacy slot still configured to publish 19001."""
        definitions = (ROOT / 'release-remote.sh').read_text().split('ACTION="${1:-}"')[0]
        with tempfile.TemporaryDirectory() as tmp:
            script = Path(tmp) / 'functions.sh'
            script.write_text(definitions)
            for port, accepted in [(None, True), ('19000', True), ('19001', False)]:
                with self.subTest(port=port):
                    bindings = {} if port is None else {'3000/tcp': [{'HostPort': port}]}
                    response = json.dumps([{'HostConfig': {'PortBindings': bindings}}])
                    harness = 'source "$1"\ndocker() { echo \'' + response + '\'; }\ncheck_rollback_port old\n'
                    result = subprocess.run(['bash', '-c', harness, 'test', str(script)], capture_output=True, text=True)
                    self.assertEqual(result.returncode == 0, accepted, result.stderr)

    def test_gateway_failure_or_wrong_version_blocks_release(self):
        """A healthy public proxy cannot hide a broken internal entrance."""
        definitions = (ROOT / 'release-remote.sh').read_text().split('ACTION="${1:-}"')[0]
        with tempfile.TemporaryDirectory() as tmp:
            script = Path(tmp) / 'functions.sh'
            script.write_text(definitions)
            for version, status, accepted in [('current', 0, True), ('old', 0, False), ('current', 7, False)]:
                with self.subTest(version=version, status=status):
                    harness = '''source "$1"
PROXY_CONTAINER=proxy; PROXY_ALIAS=new-api-green
docker() { echo '{"data":{"version":"current"}}'; }
gateway_control() { echo '{"slot":"new-api-blue","version":"''' + version + '''"}'; return ''' + str(status) + '''; }
proxy_version
'''
                    result = subprocess.run(['bash', '-c', harness, 'test', str(script)], capture_output=True, text=True)
                    self.assertEqual(result.returncode == 0, accepted, result.stderr)


class GatewayTransactionTest(unittest.TestCase):
    def test_reload_failure_restores_target_and_reports_failure(self):
        """A failed change must restore the original durable target and verify its response."""
        with tempfile.TemporaryDirectory() as tmp:
            config = Path(tmp) / 'gateway.conf'
            config.write_text((ROOT / 'gateway.conf').read_text())
            target = Path(tmp) / 'active/target.conf'
            target.parent.mkdir()
            original = 'set $deployment_slot new-api-blue;\n'
            target.write_text(original)
            calls = []

            def run(*args, **kwargs):
                calls.append(args)
                if args[-2:] == ('-s', 'reload') and sum(c[-2:] == ('-s', 'reload') for c in calls) == 1:
                    raise RuntimeError('fixture_reload_failed')
                return ''

            with patch.multiple(control, CONFIG=config, TARGET=target, JOURNAL=target.parent / 'transaction.json'), \
                 patch.object(control, 'doctor', return_value={'slot': 'new-api-blue', 'version': 'blue'}), \
                 patch.object(control, 'check_slot', side_effect=lambda s: {'id': s, 'version': s.split('-')[-1]}), \
                 patch.object(control, 'run', side_effect=run), patch.object(control, 'wait_for') as wait:
                with self.assertRaisesRegex(RuntimeError, 'fixture_reload_failed'):
                    control.switch('new-api-green', 'green')
                self.assertEqual(target.read_text(), original)
                self.assertEqual(json.loads(control.JOURNAL.read_text())['state'], 'restored')
                wait.assert_called_once_with('new-api-blue', 'blue')

    def test_drain_timeout_preserves_standby(self):
        with patch.object(control, 'run', return_value='nginx: worker process is shutting down') as run:
            with self.assertRaisesRegex(TimeoutError, 'standby_preserved'):
                control.drain(0)
            self.assertTrue(all(call.args[:2] == ('docker', 'top') for call in run.call_args_list))


@unittest.skipUnless(os.environ.get('GATEWAY_TEST_IMAGE'), 'Set GATEWAY_TEST_IMAGE to a local nginx image')
class GatewayDockerTest(unittest.TestCase):
    def test_only_gateway_publishes_a_host_port(self):
        """Rendered slot configs reserve the stable port exclusively for the gateway."""
        env = dict(os.environ, IMAGE_TAG='new-api:fixture', RUNTIME_ENV_FILE='/dev/null',
                   NODE_NAME='fixture', DATA_DIR='/tmp/data', LOG_DIR='/tmp/logs',
                   APP_NETWORK='fixture', PROXY_NETWORK='fixture',
                   GATEWAY_IMAGE=os.environ['GATEWAY_TEST_IMAGE'], GATEWAY_PORT='19001',
                   GATEWAY_BIND_IP='0.0.0.0')
        for slot in ('blue', 'green'):
            env['SLOT'] = slot
            result = subprocess.check_output(['docker', 'compose', '-f', str(ROOT / 'docker-compose.slot.yml'),
                                              'config', '--format', 'json'], env=env)
            self.assertFalse(json.loads(result)['services']['new-api'].get('ports'))
        result = subprocess.check_output(['docker', 'compose', '-f', str(ROOT / 'docker-compose.gateway.yml'),
                                          'config', '--format', 'json'], env=env)
        ports = json.loads(result)['services']['gateway']['ports']
        self.assertEqual([(p['host_ip'], str(p['published']), p['target']) for p in ports],
                         [('0.0.0.0', '19001', 3000)])

    def test_cutover_rollback_and_request_contract(self):
        """Real release functions switch/recover while streams and cold-start ordering remain valid."""
        image = os.environ['GATEWAY_TEST_IMAGE']
        prefix = 'new-api-gateway-test-' + uuid.uuid4().hex[:10]
        network = prefix + '-net'
        containers = []
        runner = os.environ.get('GATEWAY_TEST_RUNNER')

        def docker(*args):
            return subprocess.check_output(['docker', *args], text=True, stderr=subprocess.STDOUT).strip()

        # These tests must never attach to an existing application's physical slot.
        for slot in ('new-api-blue', 'new-api-green'):
            if subprocess.run(['docker', 'inspect', slot], capture_output=True).returncode == 0:
                self.skipTest('physical slot already exists; use an isolated local Docker engine')
        with tempfile.TemporaryDirectory(prefix=prefix) as tmp:
            try:
                docker('network', 'create', network)
                if runner:
                    docker('network', 'connect', network, runner)
                for color in ('blue', 'green'):
                    conf = Path(tmp) / (color + '.conf')
                    conf.write_text('''events {} http { server { listen 3000;
location = /api/status { default_type application/json; return 200 '{"success":true,"data":{"version":"''' + color + '''"}}'; }
location /echo { return 200 "$request_method $request_uri $http_authorization"; }
location /headers { return 200 "$http_x_real_ip|$http_x_forwarded_for|$http_x_forwarded_proto"; }
location /sse { default_type text/event-stream; return 200 'data: hello\\n\\ndata: [DONE]\\n\\n'; }
location /slow { default_type text/event-stream; limit_rate 8; return 200 'data: begin\\n\\ndata: keep-stream-open\\n\\ndata: [DONE]\\n\\n'; }
} }''')
                    name = 'new-api-' + color
                    containers.append(name)
                    docker('run', '-d', '--name', name, '--network', network,
                           '--label', 'org.opencontainers.image.version=' + color,
                           '--restart', 'unless-stopped', '--health-cmd', "wget -qO- http://127.0.0.1:3000/api/status || exit 1",
                           '--health-interval', '1s', '--health-retries', '10',
                           '-v', str(conf) + ':/etc/nginx/nginx.conf:ro', image)
                blue, green = containers
                config = Path(tmp) / 'gateway.conf'
                config.write_text((ROOT / 'gateway.conf').read_text())
                active = Path(tmp) / 'active'
                active.mkdir()
                target = active / 'target.conf'
                target.write_text('set $deployment_slot new-api-blue;\n')
                (active / 'trusted-proxies.conf').write_text((ROOT / 'trusted-proxies.conf').read_text())
                gateway = prefix + '-gateway'
                containers.append(gateway)
                docker('run', '-d', '--name', gateway, '--network', network,
                       '-p', '127.0.0.1::3000', '-v', str(config) + ':/etc/nginx/nginx.conf:ro',
                       '-v', str(active) + ':/etc/nginx/active:ro', image)
                address = docker('port', gateway, '3000/tcp') if not runner else (
                    json.loads(docker('inspect', gateway))[0]['NetworkSettings']['Networks'][network]['IPAddress'] + ':3000')
                env = dict(os.environ, GATEWAY_CONFIG=str(config), GATEWAY_CONTAINER=gateway,
                           APP_NETWORK=network, INTERNAL_GATEWAY_URL='http://' + address)

                def wait_version(expected):
                    deadline = time.monotonic() + 25
                    while time.monotonic() < deadline:
                        try:
                            with urllib.request.urlopen('http://' + address + '/api/status', timeout=2) as response:
                                if json.load(response)['data']['version'] == expected:
                                    return
                        except (OSError, ValueError):
                            pass
                        time.sleep(0.2)
                    self.fail('gateway did not reach ' + expected)

                wait_version('blue')
                for slot in (blue, green):
                    deadline = time.monotonic() + 15
                    while docker('inspect', '-f', '{{.State.Health.Status}}', slot) != 'healthy':
                        self.assertLess(time.monotonic(), deadline)
                        time.sleep(.2)
                initial = {slot: json.loads(docker('inspect', slot))[0]['NetworkSettings']['Networks'] for slot in (blue, green)}
                started = docker('inspect', '-f', '{{.State.StartedAt}}', gateway)

                def controller(*args, success=True):
                    result = subprocess.run(['python3', str(ROOT / 'gateway_control.py'), *args],
                                            env=env, text=True, capture_output=True)
                    self.assertEqual(result.returncode == 0, success, result.stderr)
                    return result

                headers = {'X-Forwarded-For': '203.0.113.42', 'X-Forwarded-Proto': 'https'}
                request = urllib.request.Request('http://' + address + '/headers', headers=headers)
                with urllib.request.urlopen(request, timeout=3) as response:
                    untrusted = response.read().decode().split('|')
                self.assertNotEqual(untrusted[0], '203.0.113.42')
                self.assertEqual(untrusted[2], 'http')
                trust = active / 'trusted-proxies.conf'
                trust.write_text('set_real_ip_from ' + untrusted[0] + '/32;\n'
                                 'geo $realip_remote_addr $trusted_ingress { default 0; '
                                 + untrusted[0] + '/32 1; }\n')
                docker('exec', gateway, 'nginx', '-t')
                docker('exec', gateway, 'nginx', '-s', 'reload')
                deadline = time.monotonic() + 5
                while True:
                    with urllib.request.urlopen(request, timeout=3) as response:
                        forwarded = response.read().decode()
                    if forwarded == '203.0.113.42|203.0.113.42|https':
                        break
                    self.assertLess(time.monotonic(), deadline, forwarded)
                    time.sleep(.1)
                trust.write_text((ROOT / 'trusted-proxies.conf').read_text())
                docker('exec', gateway, 'nginx', '-s', 'reload')
                controller('drain', '--seconds', '10')

                request = urllib.request.Request('http://' + address + '/echo/a?x=1&y=2', data=b'payload',
                                                 headers={'Authorization': 'Bearer fixture'})
                with urllib.request.urlopen(request, timeout=3) as response:
                    self.assertEqual(response.read(), b'POST /echo/a?x=1&y=2 Bearer fixture')
                with urllib.request.urlopen('http://' + address + '/sse', timeout=3) as response:
                    self.assertIn('text/event-stream', response.headers['Content-Type'])
                    self.assertEqual(response.read(), b'data: hello\n\ndata: [DONE]\n\n')
                # Keep a genuine throttled stream alive across the real shell cutover.
                stream = urllib.request.urlopen('http://' + address + '/slow', timeout=20)
                self.assertEqual(stream.readline(), b'data: begin\n')
                definitions = Path(tmp) / 'functions.sh'
                definitions.write_text((ROOT / 'release-remote.sh').read_text().split('ACTION="${1:-}"')[0])
                state = Path(tmp) / 'state'
                state.mkdir()
                (state / 'stage.env').write_text('PRODUCTION=new-api-blue\nCANDIDATE=new-api-green\n')
                (state / 'gate.result').write_text('gate=passed candidate=new-api-green version=green\n')
                backup = Path(tmp) / 'backups/fixture'
                backup.mkdir(parents=True)
                (backup / 'nginx-config.sha256').write_text('fixture\n')
                harness = '''source "$1/functions.sh"
SCRIPT_DIR="$2"; STATE_DIR="$1/state"; BACKUP_ROOT="$1/backups"
RELEASE_ID=fixture; VERSION=green; CONFIRM_CUTOVER=fixture; CONFIRM_ROLLBACK=fixture
load_config() { :; }
check_ingress() { :; }
nginx_hash_matches() { :; }
proxy_version() { gateway_control status | python3 -c 'import json,sys; print(json.load(sys.stdin)["version"])'; }
public_version() { proxy_version; }
"$3" --execute
'''
                for action in ('action_cutover', 'action_cutover'):
                    result = subprocess.run(['bash', '-c', harness, 'fixture', tmp, str(ROOT), action],
                                            env=env, capture_output=True, text=True)
                    self.assertEqual(result.returncode, 0, result.stderr)
                wait_version('green')
                self.assertIn('gateway_drain_timeout', controller('drain', '--seconds', '0', success=False).stderr)
                self.assertIn(b'data: [DONE]', stream.read())
                stream.close()
                controller('drain', '--seconds', '10')
                result = subprocess.run(['bash', '-c', harness, 'fixture', tmp, str(ROOT), 'action_rollback'],
                                        env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                wait_version('blue')
                self.assertEqual(started, docker('inspect', '-f', '{{.State.StartedAt}}', gateway))
                self.assertEqual(docker('inspect', '-f', '{{.RestartCount}}', gateway), '0')
                for slot in (blue, green):
                    self.assertEqual(initial[slot], json.loads(docker('inspect', slot))[0]['NetworkSettings']['Networks'])

                # Retrying a completed rollback must not depend on the retired candidate running.
                controller('drain', '--seconds', '10')
                docker('stop', green)
                result = subprocess.run(['bash', '-c', harness, 'fixture', tmp, str(ROOT), 'action_rollback'],
                                        env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)

                # Simulate termination after the durable target is published but before acknowledgement.
                journal = json.loads((active / 'transaction.json').read_text())
                journal.update(state='prepared', old={'slot': blue, 'version': 'blue'},
                               old_id=docker('inspect', '-f', '{{.Id}}', blue))
                (active / 'transaction.json').write_text(json.dumps(journal))
                target.write_text('set $deployment_slot new-api-green;\n')
                controller('status', success=False)
                controller('recover')
                wait_version('blue')

                # The gateway must start before either backend exists, then recover without another reload.
                docker('stop', blue, green)
                docker('restart', gateway)
                self.assertEqual(docker('inspect', '-f', '{{.State.Running}}', gateway), 'true')
                if runner:
                    # The isolated runner uses the bridge directly; production clients use the stable host port.
                    address = json.loads(docker('inspect', gateway))[0]['NetworkSettings']['Networks'][network]['IPAddress'] + ':3000'
                with self.assertRaises(urllib.error.HTTPError) as failed:
                    urllib.request.urlopen('http://' + address + '/api/status', timeout=5)
                self.assertEqual(failed.exception.code, 502)
                docker('start', blue)
                wait_version('blue')
                docker('start', green)
                deadline = time.monotonic() + 15
                while docker('inspect', '-f', '{{.State.Health.Status}}', green) != 'healthy':
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(.2)
                # A release that crashes must not block the rollback's admission.
                if runner:
                    env['INTERNAL_GATEWAY_URL'] = 'http://' + address
                controller('switch', '--slot', green, '--version', 'green')
                docker('stop', green)
                result = subprocess.run(['bash', '-c', harness, 'fixture', tmp, str(ROOT), 'action_rollback'],
                                        env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                wait_version('blue')
            finally:
                for name in reversed(containers):
                    subprocess.run(['docker', 'rm', '-f', name], capture_output=True)
                if runner:
                    subprocess.run(['docker', 'network', 'disconnect', network, runner], capture_output=True)
                subprocess.run(['docker', 'network', 'rm', network], capture_output=True)

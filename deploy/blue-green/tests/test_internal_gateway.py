"""Exercise stable ingress and release admission without production data."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[1]


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
curl() { echo '{"data":{"version":"''' + version + '''"}}'; return ''' + str(status) + '''; }
proxy_version
'''
                    result = subprocess.run(['bash', '-c', harness, 'test', str(script)], capture_output=True, text=True)
                    self.assertEqual(result.returncode == 0, accepted, result.stderr)


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
        """The same host port follows alias changes without restarting the gateway."""
        image = os.environ['GATEWAY_TEST_IMAGE']
        prefix = 'new-api-gateway-test-' + uuid.uuid4().hex[:10]
        network = prefix + '-net'
        containers = []

        def docker(*args):
            return subprocess.check_output(['docker', *args], text=True, stderr=subprocess.STDOUT).strip()

        with tempfile.TemporaryDirectory(prefix=prefix) as tmp:
            try:
                docker('network', 'create', network)
                for color in ('blue', 'green'):
                    conf = Path(tmp) / (color + '.conf')
                    conf.write_text('''events {} http { server { listen 3000;
location = /api/status { default_type application/json; return 200 '{"success":true,"data":{"version":"''' + color + '''"}}'; }
location /echo { return 200 "$request_method $request_uri $http_authorization"; }
location /sse { default_type text/event-stream; return 200 'data: hello\\n\\ndata: [DONE]\\n\\n'; }
} }''')
                    name = prefix + '-' + color
                    containers.append(name)
                    docker('run', '-d', '--name', name, '--network', network,
                           '-v', str(conf) + ':/etc/nginx/nginx.conf:ro', image)
                blue, green = containers
                docker('network', 'disconnect', network, blue)
                docker('network', 'connect', '--alias', 'new-api-green', network, blue)
                gateway = prefix + '-gateway'
                containers.append(gateway)
                docker('run', '-d', '--name', gateway, '--network', network,
                       '-p', '127.0.0.1::3000', '-v', str(ROOT / 'gateway.conf') + ':/etc/nginx/nginx.conf:ro', image)
                address = docker('port', gateway, '3000/tcp')

                def wait_version(expected):
                    deadline = time.monotonic() + 15
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
                request = urllib.request.Request('http://' + address + '/echo/a?x=1&y=2', data=b'payload',
                                                 headers={'Authorization': 'Bearer fixture'})
                with urllib.request.urlopen(request, timeout=3) as response:
                    self.assertEqual(response.read(), b'POST /echo/a?x=1&y=2 Bearer fixture')
                with urllib.request.urlopen('http://' + address + '/sse', timeout=3) as response:
                    self.assertIn('text/event-stream', response.headers['Content-Type'])
                    self.assertEqual(response.read(), b'data: hello\n\ndata: [DONE]\n\n')
                docker('network', 'disconnect', network, blue)
                docker('network', 'disconnect', network, green)
                docker('network', 'connect', '--alias', 'new-api-green', network, green)
                wait_version('green')
                docker('network', 'disconnect', network, green)
                docker('network', 'connect', '--alias', 'new-api-green', network, blue)
                wait_version('blue')
                self.assertEqual(docker('inspect', '-f', '{{.RestartCount}}', gateway), '0')
            finally:
                for name in reversed(containers):
                    subprocess.run(['docker', 'rm', '-f', name], capture_output=True)
                subprocess.run(['docker', 'network', 'rm', network], capture_output=True)

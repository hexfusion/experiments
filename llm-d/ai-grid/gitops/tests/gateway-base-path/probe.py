"""Exercise a Grid gateway binary against two path-prefixed streaming backends."""
import argparse
import http.client
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('binary', type=Path)
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
release_stream = threading.Event()
requests = []

class Backend(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *_):
        pass

    def do_POST(self):
        body = b''
        if self.headers.get('Transfer-Encoding', '').lower() == 'chunked':
            while True:
                size = int(self.rfile.readline().split(b';')[0], 16)
                if not size:
                    self.rfile.readline()
                    break
                body += self.rfile.read(size)
                assert self.rfile.read(2) == b'\r\n'
        else:
            body = self.rfile.read(int(self.headers['Content-Length']))
        payload = json.loads(body)
        requests.append({'path': self.path, 'model': payload['model'], 'stream': payload['stream']})
        self.send_response(200)
        if payload['stream']:
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Transfer-Encoding', 'chunked')
            self.end_headers()
            frame = b'data: {"choices":[{"delta":{"content":"hello"}}]}\n\n'
            self.wfile.write(f'{len(frame):x}\r\n'.encode() + frame + b'\r\n')
            self.wfile.flush()
            assert release_stream.wait(10), 'gateway did not forward the first SSE frame before completion'
            frame = b'data: [DONE]\n\n'
            self.wfile.write(f'{len(frame):x}\r\n'.encode() + frame + b'\r\n0\r\n\r\n')
        else:
            response = json.dumps({'model': payload['model'], 'choices': [{'message': {'content': 'hello'}}]}).encode()
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(response)))
            self.end_headers()
            self.wfile.write(response)
        self.wfile.flush()

def free_port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        return listener.getsockname()[1]

backend = ThreadingHTTPServer(('127.0.0.1', 0), Backend)
threading.Thread(target=backend.serve_forever, daemon=True).start()
listen = free_port()
metrics = free_port()
backend_port = backend.server_address[1]
results = []
with tempfile.TemporaryDirectory(prefix='grid-base-path-') as directory:
    work = Path(directory)
    config = work / 'praxis.yaml'
    config.write_text(f'''insecure_options:
  allow_private_endpoints: true
admin:
  metrics_address: "127.0.0.1:{metrics}"
listeners:
- name: inference
  address: "127.0.0.1:{listen}"
  filter_chains: [inference]
filter_chains:
- name: inference
  filters:
  - filter: model_to_header
    header: X-Model
  - filter: intelligent_route
    model_header: X-Model
    local_site: local
    candidates:
    - kind: inference_model
      name: model-a
      site: local
      cluster: provider-a
    - kind: inference_model
      name: model-b
      site: local
      cluster: provider-b
  - filter: load_balancer
    clusters:
    - name: provider-a
      endpoints: ["127.0.0.1:{backend_port}"]
      http:
        base_path: /models/a
    - name: provider-b
      endpoints: ["127.0.0.1:{backend_port}"]
      http:
        base_path: /models/b/
''')
    env = dict(os.environ, RUST_LOG='warn')
    env.pop('GRID_SERVING_CONFIG', None)
    log_path = work / 'gateway.log'
    with log_path.open('w') as log:
        gateway = subprocess.Popen([str(args.binary.resolve()), '--config', str(config)], stdout=log, stderr=log, env=env)
        try:
            deadline = time.monotonic() + 20
            while True:
                try:
                    with socket.create_connection(('127.0.0.1', listen), timeout=0.2):
                        break
                except OSError:
                    if gateway.poll() is not None or time.monotonic() >= deadline:
                        raise RuntimeError(log_path.read_text())
                    time.sleep(0.05)
            health = http.client.HTTPConnection('127.0.0.1', metrics, timeout=3)
            health.request('GET', '/healthy')
            response = health.getresponse()
            assert response.status == 200
            response.read()
            health.close()
            for model, prefix in [('model-a', '/models/a'), ('model-b', '/models/b')]:
                for streaming in (False, True):
                    for chunked in (False, True):
                        release_stream.clear()
                        payload = json.dumps({'model': model, 'stream': streaming, 'messages': [{'role': 'user', 'content': 'hello'}]}).encode()
                        body = iter([payload[:17], payload[17:]]) if chunked else payload
                        conn = http.client.HTTPConnection('127.0.0.1', listen, timeout=8)
                        started = time.monotonic()
                        conn.request('POST', '/v1/chat/completions?trace=path%2Fcheck', body, {'Content-Type': 'application/json'}, encode_chunked=chunked)
                        response = conn.getresponse()
                        assert response.status == 200, (response.status, response.read())
                        first_ms = None
                        if streaming:
                            assert response.getheader('Content-Type').startswith('text/event-stream')
                            first = response.readline()
                            first_ms = round((time.monotonic() - started) * 1000, 2)
                            assert b'hello' in first, first
                            release_stream.set()
                            assert b'data: [DONE]' in response.read()
                        else:
                            assert json.loads(response.read())['model'] == model
                        conn.close()
                        expected = prefix + '/v1/chat/completions?trace=path%2Fcheck'
                        assert requests[-1]['path'] == expected, requests[-1]
                        results.append({'model': model, 'stream': streaming, 'chunkedRequest': chunked, 'status': 200, 'backendPath': expected, 'firstFrameBeforeBackendCompletion': streaming, 'firstFrameMs': first_ms})
        finally:
            release_stream.set()
            gateway.terminate()
            try:
                gateway.wait(timeout=5)
            except subprocess.TimeoutExpired:
                gateway.kill()
                gateway.wait()
            backend.shutdown()
args.output.write_text(json.dumps({'scope': 'standalone Grid binary with mock final-hop backends; no Envoy or Kubernetes', 'cases': results}, indent=2) + '\n')
print(json.dumps({'passed': len(results), 'output': str(args.output)}))

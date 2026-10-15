#!/usr/bin/env python3
"""Reduced ExternalModel transport reproduction using Dagobah's exact Envoy image."""
import argparse
import datetime
import hashlib
import http.client
import http.server
import json
import os
import pathlib
import secrets
import signal
import socket
import ssl
import subprocess
import threading
import time
import uuid

import yaml

IMAGE = ('registry.redhat.io/openshift-service-mesh/istio-proxyv2-rhel9'
         '@sha256:d518f3d1539f45e1253c5c9fa22062802804601d4998cd50344e476a3cc388fe')
VERSION = '06f9604a19dcdece315aea66ce7941746c826a6d/1.34.2-dev/3.1.0/RELEASE/OpenSSL'
MODEL = 'streaming-probe'
SELECTION = MODEL + '/provider-grid'


def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def wait_port(port, process):
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError('Process exited before its listener was ready')
        try:
            with socket.create_connection(('127.0.0.1', port), timeout=.2):
                return
        except OSError:
            time.sleep(.1)
    raise TimeoutError('Listener did not start')


def stop(process):
    if process and process.poll() is None:
        process.send_signal(signal.SIGTERM)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    processor = parser.add_mutually_exclusive_group(required=True)
    processor.add_argument('--binary')
    processor.add_argument('--image')
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    out = pathlib.Path(args.output)
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    ports = {key: free_port() for key in ('envoy', 'admin', 'pre', 'post', 'backend')}
    provider_key = secrets.token_urlsafe(32)
    caller_key = secrets.token_urlsafe(32)
    credential = out / 'provider-key'
    credential.write_text(provider_key)
    credential.chmod(0o600)
    arrivals, requests = {}, {}
    state_lock = threading.Lock()

    class Mock(http.server.BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'

        def log_message(self, *_):
            pass

        def do_POST(self):
            if self.headers.get('Transfer-Encoding', '').lower() == 'chunked':
                parts = []
                while True:
                    size = int(self.rfile.readline().split(b';')[0], 16)
                    if not size:
                        while self.rfile.readline() not in (b'\r\n', b'\n', b''):
                            pass
                        break
                    parts.append(self.rfile.read(size))
                    self.rfile.read(2)
                body = b''.join(parts)
            else:
                body = self.rfile.read(int(self.headers.get('Content-Length', '0')))
            payload = json.loads(body)
            request_id = self.headers['x-probe-id']
            record = {
                'providerCredentialCorrect': self.headers.get('Authorization') == 'Bearer ' + provider_key,
                'callerCredentialRemoved': self.headers.get('Authorization') != 'Bearer ' + caller_key,
                'routingHeadersRemoved': all(self.headers.get(k) is None for k in
                    ('x-ai-routing-candidate', 'x-ai-routing-request-id', 'x-ai-routing-revision')),
                'modelCorrect': payload.get('model') == MODEL,
                'pathCorrect': self.path == '/v1/chat/completions',
            }
            with state_lock:
                requests[request_id] = record
            if not all(record.values()):
                self.send_response(401)
                self.send_header('Content-Length', '0')
                self.end_headers()
                return
            if not payload.get('stream'):
                data = json.dumps({'model': MODEL, 'choices': [{'message': {'content': 'streaming probe'}}]}).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                self.wfile.write(data)
                return
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Transfer-Encoding', 'chunked')
            self.end_headers()

            def chunk(data):
                self.wfile.write(f'{len(data):x}\r\n'.encode() + data + b'\r\n')
                self.wfile.flush()

            first = b'data: {"choices":[{"delta":{"content":"first"}}]}\n\n'
            chunk(first[:11])
            chunk(first[11:])
            record['firstEventObservedBeforeCompletion'] = arrivals[request_id].wait(5)
            time.sleep(.15)
            chunk(b'data: {"choices":[{"delta":{"content":"second"}}]}\n\n')
            time.sleep(.15)
            chunk(b'data: [DONE]\n\n')
            self.wfile.write(b'0\r\n\r\n')
            self.wfile.flush()

    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                    '-subj', '/CN=localhost', '-addext', 'subjectAltName=DNS:localhost',
                    '-keyout', str(out/'mock.key'), '-out', str(out/'mock.crt')],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    (out/'mock.key').chmod(0o600)
    backend = http.server.ThreadingHTTPServer(('127.0.0.1', ports['backend']), Mock)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(out/'mock.crt', out/'mock.key')
    backend.socket = tls.wrap_socket(backend.socket, server_side=True)
    threading.Thread(target=backend.serve_forever, daemon=True).start()

    payload = {'network': 'externalmodel-probe', 'local_site': 'test', 'candidates': [{
        'cluster': 'provider-grid', 'stable_id': SELECTION, 'kind': 'inference_model',
        'name': MODEL, 'site': 'test', 'fresh': True,
        'credential': {'strategy': 'bearer_token', 'secretRef': {
            'name': 'provider-key', 'namespace': 'test', 'key': 'api-key'}}}]}
    digest = hashlib.sha256(json.dumps(payload, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    envelope = {'schema_version': '1.0.0',
        'revision': {'kind': 'content_addressed', 'algorithm': 'sha256', 'value': digest},
        'content_digest': {'algorithm': 'sha256', 'value': digest},
        'scope': {'network': 'externalmodel-probe', 'gateway': 'test', 'namespace': 'test', 'local_site': 'test'},
        'provenance': {'producer': 'isolated-test', 'producer_version': '1', 'source_name': 'externalmodel-probe',
                       'source_uid': str(uuid.uuid4()), 'source_generation': 1,
                       'rendered_at': datetime.datetime.now(datetime.timezone.utc).isoformat()},
        'overlay': payload}
    (out/'overlay.json').write_text(json.dumps(envelope))
    common = {'health_address': '127.0.0.1:0', 'metrics_address': '127.0.0.1:0',
              'shutdown_drain_timeout_secs': 1, 'tls': {'mode': 'none'}}
    pre = {'server': dict(common, grpc_address=f"127.0.0.1:{ports['pre']}"),
           'filter_chains': [{'name': 'bbr', 'filters': [
               {'filter': 'model_to_header', 'header': 'X-Gateway-Model-Name'}]}]}
    post = {'server': dict(common, grpc_address=f"127.0.0.1:{ports['post']}"),
            'filter_chains': [{'name': 'post-auth', 'filters': [
                {'filter': 'intelligent_route', 'overlay_file': str(out/'overlay.json'),
                 'model_header': 'X-Gateway-Model-Name', 'provider_hop_clusters': ['provider-grid']},
                {'filter': 'credential_inject', 'credentials': [{'name': 'provider-key', 'namespace': 'test',
                   'key': 'api-key', 'strategy': 'bearer_token', 'file': str(credential)}]}]}]}
    (out/'pre.yaml').write_text(yaml.safe_dump(pre))
    (out/'post-baseline.yaml').write_text(yaml.safe_dump(post))
    post['server']['processing_mode'] = {'request_body_mode': 'streamed', 'response_body_mode': 'none'}
    (out/'post-candidate.yaml').write_text(yaml.safe_dump(post))

    def cluster(name, port, h2=False, backend_tls=False):
        c = {'name': name, 'type': 'STATIC', 'connect_timeout': '2s', 'load_assignment': {
            'cluster_name': name, 'endpoints': [{'lb_endpoints': [{'endpoint': {'address': {
                'socket_address': {'address': '127.0.0.1', 'port_value': port}}}}]}]}}
        if h2:
            c['http2_protocol_options'] = {}
        if backend_tls:
            c['transport_socket'] = {'name': 'envoy.transport_sockets.tls', 'typed_config': {
                '@type': 'type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext',
                'sni': 'localhost', 'common_tls_context': {'validation_context': {
                    'trusted_ca': {'filename': '/probe/mock.crt'},
                    'match_typed_subject_alt_names': [{'san_type': 'DNS', 'matcher': {'exact': 'localhost'}}]}}}}
        return c

    post_name = 'envoy.filters.http.ext_proc.external-model'
    mode = {'request_header_mode': 'SEND', 'response_header_mode': 'SEND',
            'request_body_mode': 'STREAMED', 'response_body_mode': 'NONE',
            'request_trailer_mode': 'SKIP', 'response_trailer_mode': 'SKIP'}
    per_route = {post_name: {'@type': 'type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExtProcPerRoute',
                           'overrides': {'processing_mode': mode}}}
    route = {'match': {'prefix': '/', 'headers': [
        {'name': 'X-Gateway-Model-Name', 'string_match': {'exact': MODEL}},
        {'name': 'X-AI-Routing-Candidate', 'string_match': {'exact': SELECTION}}]},
        'route': {'cluster': 'provider-grid', 'timeout': '30s'},
        'request_headers_to_remove': ['x-ai-routing-candidate', 'x-ai-routing-request-id', 'x-ai-routing-revision'],
        'typed_per_filter_config': per_route}
    sink = {'match': {'prefix': '/'}, 'route': {'cluster': 'selection-required', 'timeout': '30s'},
            'typed_per_filter_config': per_route}
    def extproc(name, upstream, modes):
        return {'name': name, 'typed_config': {
            '@type': 'type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor',
            'grpc_service': {'envoy_grpc': {'cluster_name': upstream}, 'timeout': '10s'},
            'message_timeout': '10s', 'failure_mode_allow': False, 'processing_mode': modes}}
    filters = [extproc('envoy.filters.http.ext_proc.external-model-pre', 'pre', {
        'request_header_mode': 'SEND', 'response_header_mode': 'SKIP',
        'request_body_mode': 'BUFFERED', 'response_body_mode': 'NONE'}),
        extproc(post_name, 'post', {'request_header_mode': 'SEND', 'response_header_mode': 'SEND',
                                 'request_body_mode': 'NONE', 'response_body_mode': 'NONE'}),
        {'name': 'envoy.filters.http.router', 'typed_config': {
            '@type': 'type.googleapis.com/envoy.extensions.filters.http.router.v3.Router'}}]
    envoy = {'admin': {'address': {'socket_address': {'address': '127.0.0.1', 'port_value': ports['admin']}}},
        'static_resources': {'listeners': [{'name': 'externalmodel-test', 'address': {'socket_address': {
            'address': '127.0.0.1', 'port_value': ports['envoy']}}, 'filter_chains': [{'filters': [{
            'name': 'envoy.filters.network.http_connection_manager', 'typed_config': {
              '@type': 'type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager',
              'stat_prefix': 'probe', 'route_config': {'name': 'externalmodel', 'virtual_hosts': [{
                  'name': 'probe', 'domains': ['*'], 'routes': [route, sink]}]}, 'http_filters': filters}}]}]}],
            'clusters': [cluster('pre', ports['pre'], h2=True), cluster('post', ports['post'], h2=True),
                         cluster('provider-grid', ports['backend'], backend_tls=True),
                         cluster('selection-required', free_port())]}}
    config = out/'envoy.json'
    config.write_text(json.dumps(envoy, indent=2))
    config_hash = hashlib.sha256(config.read_bytes()).hexdigest()
    report = {'envoyImage': IMAGE, 'envoyConfigSha256': config_hash,
              'liveClusterModified': False, 'scope': 'Reduced ExternalModel transport; actual route/credential filters; no MaaS auth policies or Grid',
              'baseline': 'Patched binary with explicit modes disabled; previously reproduced separately against deployed image',
              'cases': []}
    report['praxisImage'] = args.image
    def processor_command(config):
        if args.binary:
            return [args.binary, '--config', str(config)]
        return ['podman', 'run', '--rm', '--network', 'host',
                '--userns', 'keep-id', '--user', str(os.getuid()),
                '--security-opt', 'label=disable', '--volume', str(out)+':'+str(out)+':ro',
                args.image, '--config', str(config)]
    processes, logs = [], []
    container = 'externalmodel-probe-' + uuid.uuid4().hex[:8]
    post_process = None
    try:
        def start(binary_args, log_name, port):
            log = (out/log_name).open('w')
            logs.append(log)
            env = dict(os.environ, RUST_LOG='info,praxis_extproc::server=debug')
            proc = subprocess.Popen(binary_args, stdout=log, stderr=log, env=env)
            processes.append(proc)
            wait_port(port, proc)
            return proc
        start(processor_command(out/'pre.yaml'), 'pre.log', ports['pre'])
        start(['podman', 'run', '--rm', '--name', container, '--platform', 'linux/amd64',
               '--network', 'host', '--user', '0', '--security-opt', 'label=disable',
               '--volume', str(out)+':/probe:ro', '--entrypoint', '/usr/local/bin/envoy', IMAGE,
               '-c', '/probe/envoy.json', '--concurrency', '2', '--disable-hot-restart', '--log-level', 'warning'],
              'envoy.log', ports['envoy'])
        conn = http.client.HTTPConnection('127.0.0.1', ports['admin'], timeout=5)
        conn.request('GET', '/server_info')
        report['envoyVersion'] = json.loads(conn.getresponse().read())['version']
        conn.close()
        assert report['envoyVersion'] == VERSION, 'Must use the exact deployed Envoy build'
        for phase in ('baseline', 'candidate'):
            post_process = start(processor_command(out/f'post-{phase}.yaml'), f'post-{phase}.log', ports['post'])
            for streaming, chunked in ((False, False), (False, True), (True, False), (True, True)):
                request_id = str(uuid.uuid4())
                arrivals[request_id] = threading.Event()
                body = json.dumps({'model': MODEL, 'stream': streaming,
                                  'messages': [{'role': 'user', 'content': 'probe'}]}).encode()
                headers = {'Content-Type': 'application/json', 'Authorization': 'Bearer '+caller_key,
                           'x-probe-id': request_id, 'x-ai-routing-candidate': 'spoofed',
                           'x-ai-routing-request-id': 'spoofed', 'x-ai-routing-revision': 'spoofed'}
                connection = http.client.HTTPConnection('127.0.0.1', ports['envoy'], timeout=15)
                if chunked:
                    data = iter(body[i:i+17] for i in range(0, len(body), 17))
                    connection.request('POST', '/v1/chat/completions', data, headers, encode_chunked=True)
                else:
                    connection.request('POST', '/v1/chat/completions', body, headers)
                start_time = time.monotonic()
                response = connection.getresponse()
                event_times, lines = [], []
                if response.status == 200 and streaming:
                    while line := response.readline():
                        lines.append(line)
                        if line.startswith(b'data:'):
                            event_times.append(round(time.monotonic()-start_time, 4))
                            arrivals[request_id].set()
                    response_body = b''.join(lines)
                else:
                    response_body = response.read()
                record = {'phase': phase, 'stream': streaming, 'chunkedUpload': chunked,
                          'status': response.status, 'eventArrivalSeconds': event_times,
                          'backendChecks': requests.get(request_id)}
                report['cases'].append(record)
                connection.close()
                print(json.dumps(record), flush=True)
                assert response.status == (500 if phase == 'baseline' else 200), record
                if phase == 'candidate':
                    assert requests.get(request_id) and all(requests[request_id].values()), record
                    if streaming:
                        assert response.getheader('Content-Type').startswith('text/event-stream')
                        assert response_body.endswith(b'data: [DONE]\n\n'), response_body
                        assert len(event_times) == 3, event_times
            stop(post_process)
            post_process = None
        assert hashlib.sha256(config.read_bytes()).hexdigest() == config_hash, 'Envoy config changed'
        report['passed'] = True
    except Exception as exc:
        report['passed'] = False
        report['error'] = str(exc)
        raise
    finally:
        for proc in reversed(processes):
            stop(proc)
        subprocess.run(['podman', 'stop', '--time', '1', '--ignore', container],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        backend.shutdown()
        backend.server_close()
        for log in logs:
            log.close()
        credential.unlink(missing_ok=True)
        (out/'mock.key').unlink(missing_ok=True)
        (out/'result.json').write_text(json.dumps(report, indent=2)+'\n')


if __name__ == '__main__':
    main()

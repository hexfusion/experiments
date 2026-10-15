#!/usr/bin/env python3
"""Exercise an isolated MaaS ExternalModel against a TLS mock provider."""
import argparse
import datetime
import http.client
import json
import pathlib
import secrets
import socket
import ssl
import subprocess
import time
import uuid

import yaml


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--values', required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--expect', choices=['bug', 'fixed'], default='bug')
    args = parser.parse_args()
    cfg = yaml.safe_load(pathlib.Path(args.values).read_text())['tests']['maasExtproc']
    if '@sha256:' not in cfg['mockImage']:
        raise SystemExit('mockImage must be pinned by digest')
    out = pathlib.Path(args.output)
    out.mkdir(parents=True, exist_ok=False)
    name = 'extproc-repro-' + uuid.uuid4().hex[:8]
    ns, gns = cfg['tenantNamespace'], cfg['gatewayNamespace']
    host = f"{cfg['gatewayService']}.{gns}.svc"
    kube = ['kubectl', '--context', cfg['context']]
    label = {'experiments.hexfusion.io/run': name}
    created, results = [], []
    pf, api_id, sa_token, tls = None, None, None, None
    port = None

    def command(*parts, data=None, check=True):
        r = subprocess.run(kube + list(parts), input=data, text=True, capture_output=True, timeout=45)
        if check and r.returncode:
            # Do not print stdin or command arguments: some requests carry credentials.
            raise RuntimeError(r.stderr.strip())
        return r

    def get(kind, obj, namespace=ns):
        return json.loads(command('get', kind, obj, '-n', namespace, '-o', 'json').stdout)

    def resource(api, kind, obj=name, namespace=ns, **fields):
        return {'apiVersion': api, 'kind': kind,
                'metadata': {'name': obj, 'namespace': namespace, 'labels': label}, **fields}

    def create(obj):
        command('create', '-f', '-', data=json.dumps(obj))
        created.append((obj['kind'], obj['metadata']['name'], obj['metadata']['namespace']))

    def wait_for(description, fn, seconds=240):
        deadline = time.monotonic() + seconds
        last = 0
        while time.monotonic() < deadline:
            if fn():
                return
            if time.monotonic()-last > 25:
                print('Waiting for ' + description, flush=True)
                last = time.monotonic()
            time.sleep(2)
        raise TimeoutError(description)

    def request(method, path, credential=None, body=None, stream_response=False, trace=None):
        conn = http.client.HTTPConnection(host, timeout=30)
        conn.sock = tls.wrap_socket(socket.create_connection(('127.0.0.1', port), timeout=30), server_hostname=host)
        headers = {'Content-Type': 'application/json', 'x-request-id': str(uuid.uuid4())}
        if credential:
            headers['Authorization'] = 'Bearer ' + credential
        encoded = json.dumps(body).encode() if body is not None else None
        # Exercise chunked client uploads as well as streamed model responses.
        if stream_response:
            chunks = iter(encoded[i:i+31] for i in range(0, len(encoded), 31))
            conn.request(method, path, body=chunks, headers=headers, encode_chunked=True)
        else:
            conn.request(method, path, body=encoded, headers=headers)
        start = time.monotonic()
        response = conn.getresponse()
        status = response.status
        if stream_response and status == 200:
            lines, arrivals = [], []
            while line := response.readline():
                lines.append(line)
                if line.startswith(b'data:'):
                    arrivals.append(round(time.monotonic()-start, 4))
            payload = b''.join(lines).decode()
            if trace is not None:
                trace.update(contentType=response.getheader('Content-Type'), dataEventArrivalSeconds=arrivals)
        else:
            payload = response.read().decode()
        conn.close()
        return status, payload

    def envoy_dump():
        return json.loads(command('exec', '-n', gns, 'deployment/'+cfg['gatewayDeployment'], '--',
                                  'pilot-agent', 'request', 'GET', 'config_dump').stdout)

    def route_modes():
        found = []
        def visit(value):
            if isinstance(value, dict):
                if value.get('name', '').startswith(f'{ns}.external-model-{name}.'):
                    f = value.get('typed_per_filter_config', {}).get('envoy.filters.http.ext_proc.external-model', {})
                    found.append(f.get('overrides', {}).get('processing_mode', {}).get('request_body_mode', 'NONE'))
                for v in value.values():
                    visit(v)
            elif isinstance(value, list):
                for v in value:
                    visit(v)
        visit(envoy_dump())
        return found

    report = {'run': name, 'expected': args.expect, 'startedAt': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'path': 'MaaS gateway -> tenant Praxis extproc -> TLS Katan mock; no Grid or vLLM', 'cases': results}
    try:
        create(resource('v1', 'ServiceAccount', automountServiceAccountToken=False))
        # One random key shared only between the mock backend and its ExternalProvider.
        provider_key = secrets.token_urlsafe(32)
        obj = resource('v1', 'Secret', stringData={'api-key': provider_key})
        obj['metadata']['labels']['inference.llm-d.ai/ipp-managed'] = 'true'
        create(obj)
        svc = resource('v1', 'Service', spec={'selector': label, 'ports': [{'name': 'https', 'port': 443, 'targetPort': 8443}]})
        svc['metadata']['annotations'] = {'service.beta.openshift.io/serving-cert-secret-name': name+'-tls'}
        create(svc)
        create(resource('apps/v1', 'Deployment', spec={'replicas': 1, 'selector': {'matchLabels': label},
            'template': {'metadata': {'labels': label}, 'spec': {'automountServiceAccountToken': False,
            'containers': [{'name': 'mock', 'image': cfg['mockImage'], 'command': ['llm-katan'],
                'args': ['--model', name, '--backend', 'echo', '--providers', 'openai', '--validate-keys',
                         '--api-keys', 'openai=$(PROVIDER_KEY)', '--port', '8443', '--tls-cert', '/tls/tls.crt', '--tls-key', '/tls/tls.key'],
                'env': [{'name': 'HOME', 'value': '/tmp'}, {'name': 'PROVIDER_KEY', 'valueFrom': {'secretKeyRef': {'name': name, 'key': 'api-key'}}}],
                'ports': [{'containerPort': 8443}], 'readinessProbe': {'tcpSocket': {'port': 8443}, 'periodSeconds': 2},
                'resources': {'requests': {'cpu': '50m', 'memory': '128Mi'}, 'limits': {'memory': '512Mi'}},
                'volumeMounts': [{'name': 'tls', 'mountPath': '/tls', 'readOnly': True}, {'name': 'tmp', 'mountPath': '/tmp'}]}],
            'volumes': [{'name': 'tls', 'secret': {'secretName': name+'-tls'}}, {'name': 'tmp', 'emptyDir': {}}]}}}))
        provider = resource('inference.opendatahub.io/v1alpha1', 'ExternalProvider', spec={'provider': 'openai',
            'endpoint': f'{name}.{ns}.svc', 'auth': {'type': 'apikey', 'secretRef': {'name': name}},
            'config': {'tls.caCertificates': cfg['providerCAPath']}})
        create(provider)
        create(resource('inference.opendatahub.io/v1alpha1', 'ExternalModel', spec={'modelName': name,
            'externalProviderRefs': [{'ref': {'name': name}, 'targetModel': name, 'apiFormat': 'openai-chat', 'path': '/v1/chat/completions'}]}))
        create(resource('maas.opendatahub.io/v1alpha1', 'MaaSModelRef', spec={'modelRef': {'kind': 'ExternalModel', 'name': name}}))
        user = f'system:serviceaccount:{ns}:{name}'
        model_ref = {'name': name, 'namespace': ns}
        create(resource('maas.opendatahub.io/v1alpha1', 'MaaSAuthPolicy', spec={'modelRefs': [model_ref], 'subjects': {'users': [user]}}))
        create(resource('maas.opendatahub.io/v1alpha1', 'MaaSSubscription', spec={'owner': {'users': [user]},
            'modelRefs': [{**model_ref, 'tokenRateLimits': [{'limit': 10000, 'window': '1m'}]}], 'priority': 10}))
        create(resource('v1', 'ConfigMap', obj=name+'-ca', data={}))
        command('annotate', 'configmap', name+'-ca', '-n', ns, 'service.beta.openshift.io/inject-cabundle=true')
        wait_for('mock backend', lambda: get('deployment', name).get('status', {}).get('readyReplicas', 0) == 1)
        wait_for('MaaS model registration', lambda: get('maasmodelref', name).get('status', {}).get('phase') == 'Ready')
        def processor_ready():
            d = get('deployment', 'payload-processing-external-model')
            status = d.get('status', {})
            replicas = d['spec'].get('replicas', 1)
            if status.get('observedGeneration', 0) < d['metadata']['generation']:
                return False
            if any(status.get(field, 0) != replicas for field in ('replicas', 'updatedReplicas', 'readyReplicas')):
                return False
            r = command('exec', '-n', ns, 'deployment/payload-processing-external-model', '--',
                        'cat', '/etc/praxis/routing/routing-overlay.json', check=False)
            return r.returncode == 0 and name in r.stdout
        wait_for('tenant processor rollout and projected routing overlay', processor_ready)

        wait_for('service CA', lambda: bool(get('configmap', name+'-ca').get('data', {}).get('service-ca.crt')))
        tls = ssl.create_default_context(cadata=get('configmap', name+'-ca')['data']['service-ca.crt'])
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        pf = subprocess.Popen(kube+['port-forward', '-n', gns, 'service/'+cfg['gatewayService'], f'{port}:443'],
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        def listening():
            try:
                with socket.create_connection(('127.0.0.1', port), timeout=1):
                    return True
            except OSError:
                return False
        wait_for('local gateway connection', listening, 30)
        sa_token = command('create', 'token', name, '-n', ns, '--duration=15m').stdout.strip()
        status, data = request('POST', '/maas-api/v1/api-keys', sa_token,
                               {'name': name, 'subscription': name, 'expiresIn': '15m', 'ephemeral': True})
        if status != 201:
            raise RuntimeError(f'MaaS key creation returned {status}')
        key = json.loads(data)
        api_id = key['id']
        model_payload = {'model': name, 'messages': [{'role': 'user', 'content': 'isolated-extproc-probe'}], 'max_tokens': 32}
        report['envoy'] = json.loads(command('exec', '-n', gns, 'deployment/'+cfg['gatewayDeployment'], '--',
                                           'pilot-agent', 'request', 'GET', 'server_info').stdout)['version']
        for case, mode, expected in [('baseline', 'STREAMED', 500 if args.expect == 'bug' else 200),
                                     ('matching-none-control', 'NONE', 200),
                                     ('restored-baseline', 'STREAMED', 500 if args.expect == 'bug' else 200)]:
            if case == 'matching-none-control':
                patches = []
                for i in range(4):
                    patches.append({'applyTo': 'HTTP_ROUTE', 'match': {'context': 'GATEWAY', 'routeConfiguration': {'vhost': {
                        'route': {'name': f'{ns}.external-model-{name}.{i}'}}}}, 'patch': {'operation': 'MERGE', 'value': {
                        'typed_per_filter_config': {'envoy.filters.http.ext_proc.external-model': {
                            '@type': 'type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExtProcPerRoute',
                            'overrides': {'processing_mode': {'request_header_mode': 'SEND', 'request_body_mode': 'NONE',
                                'response_header_mode': 'SEND', 'response_body_mode': 'NONE', 'request_trailer_mode': 'SKIP', 'response_trailer_mode': 'SKIP'}}}}}}})
                create(resource('networking.istio.io/v1alpha3', 'EnvoyFilter', namespace=gns, spec={'priority': 30,
                    'workloadSelector': {'labels': {'gateway.networking.k8s.io/gateway-name': cfg['gatewayName']}}, 'configPatches': patches}))
            elif case == 'restored-baseline':
                command('delete', 'envoyfilter', name, '-n', gns, '--wait=false')
            wait_for(case+' route mode', lambda: len(route_modes()) == 4 and all(m == mode for m in route_modes()), 90)
            for stream in (False, True):
                payload = dict(model_payload)
                if stream:
                    payload.update(stream=True, stream_options={'include_usage': True})
                trace = {}
                deadline = time.monotonic() + 60
                warmup_statuses = []
                while True:
                    status, body = request('POST', '/v1/chat/completions', key['key'], payload,
                                           stream_response=stream, trace=trace)
                    # Mounted overlay propagation and watcher reload are asynchronous.
                    # Only a no-route 404 during initial warmup may be retried.
                    if case != 'baseline' or stream or status != 404 or time.monotonic() >= deadline:
                        break
                    warmup_statuses.append(status)
                    print('Waiting for tenant processor routing snapshot (404)', flush=True)
                    time.sleep(2)
                record = {'case': case, 'routeMode': mode, 'stream': stream, 'chunkedUpload': stream,
                          'status': status, 'expectedStatus': expected, 'body': body, 'warmupStatuses': warmup_statuses, **trace}
                results.append(record)
                print(json.dumps({'case': case, 'stream': stream, 'status': status, 'expected': expected}), flush=True)
                if status != expected:
                    raise AssertionError(f'{case}: expected {expected}, got {status}')
                if status == 200 and stream:
                    events = [line[5:].strip() for line in body.splitlines() if line.startswith('data:')]
                    if not events or events[-1] != '[DONE]':
                        raise AssertionError('SSE stream did not terminate with [DONE]')
                    chunks = [json.loads(event) for event in events[:-1]]
                    if not any(choice.get('delta', {}).get('content') for chunk in chunks for choice in chunk.get('choices', [])):
                        raise AssertionError('SSE stream contained no completion content')
                    if not (trace.get('contentType') or '').startswith('text/event-stream'):
                        raise AssertionError('Streaming response had the wrong content type')
                elif status == 200 and not json.loads(body).get('choices'):
                    raise AssertionError('Mock did not return an OpenAI completion')
        logs = command('logs', '-n', ns, 'deployment/payload-processing-external-model', '--since=10m').stdout
        report['processorErrors'] = [line for line in logs.splitlines() if 'body mode is NONE' in line]
        if args.expect == 'bug' and not report['processorErrors']:
            raise AssertionError('Expected body-mode error was not observed')
        report['passed'] = True
    except BaseException as exc:
        report['passed'] = False
        report['error'] = str(exc)
        raise
    finally:
        errors = []
        if api_id and pf and pf.poll() is None:
            try:
                status, _ = request('DELETE', '/maas-api/v1/api-keys/'+api_id, sa_token)
                report['keyRevocationStatus'] = status
                if status not in (200, 204):
                    errors.append('MaaS API key revocation returned '+str(status))
            except Exception as exc:
                errors.append(str(exc))
        if pf:
            pf.terminate()
            pf.wait(timeout=5)
        for kind, obj, namespace in reversed(created):
            try:
                command('delete', kind, obj, '-n', namespace, '--ignore-not-found', '--wait=false')
            except Exception as exc:
                errors.append(f'{kind}/{obj}: {exc}')
        # OpenShift creates this Secret for the run-owned Service.
        try:
            command('delete', 'secret', name+'-tls', '-n', ns, '--ignore-not-found', '--wait=false')
        except Exception as exc:
            errors.append(str(exc))
        report['cleanupErrors'] = errors
        (out/'result.json').write_text(json.dumps(report, indent=2)+'\n')
        print('Evidence: '+str(out/'result.json'), flush=True)
        if errors:
            raise RuntimeError('Cleanup incomplete; see result.json')


if __name__ == '__main__':
    main()

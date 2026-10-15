"""Probe the existing Grid model through MaaS without changing routing resources."""
from pathlib import Path
import datetime
import http.client
import json
import socket
import ssl
import subprocess
import time
import uuid

root = Path(__file__).parent
context = 'default/api-dagobah-hexfusion-local:6443/kube:admin'
kube = ['kubectl', '--context', context]
namespace = 'models-as-a-service'
gateway_namespace = 'openshift-ingress'
gateway = 'maas-default-gateway-maas-gateway-class'
host = gateway + '.' + gateway_namespace + '.svc'
model = 'qwen3-coder-30b-a3b'
report = {'checkedAt': datetime.datetime.now(datetime.timezone.utc).isoformat(),
          'path': 'MaaS -> hub Grid -> site-d Grid -> existing Qwen vLLM; protected KServe/EPP hop not included',
          'envoyConfigurationModified': False, 'model': model, 'cases': []}
forward = None
api_id = None
token = None
port = None
tls = None

def command(*args):
    proc = subprocess.run(kube + list(args), text=True, capture_output=True, timeout=45)
    if proc.returncode:
        raise RuntimeError(proc.stderr.strip())
    return proc.stdout

def request(method, path, credential=None, payload=None, chunked=False, stream=False):
    conn = http.client.HTTPConnection(host, timeout=60)
    conn.sock = tls.wrap_socket(socket.create_connection(('127.0.0.1',port),timeout=30), server_hostname=host)
    headers = {'Content-Type':'application/json', 'x-request-id':str(uuid.uuid4())}
    if credential:
        headers['Authorization'] = 'Bearer ' + credential
    body = json.dumps(payload).encode() if payload is not None else None
    start = time.monotonic()
    if chunked:
        conn.request(method,path,body=iter(body[i:i+31] for i in range(0,len(body),31)),headers=headers,encode_chunked=True)
    else:
        conn.request(method,path,body=body,headers=headers)
    response = conn.getresponse()
    record = {'status':response.status,'contentType':response.getheader('Content-Type')}
    if stream and response.status == 200:
        lines, arrivals = [], []
        while line := response.readline():
            lines.append(line)
            if line.startswith(b'data:'):
                arrivals.append(round(time.monotonic()-start,6))
        record['dataEventArrivalSeconds'] = arrivals
        data = b''.join(lines).decode()
    else:
        data = response.read().decode()
    record['elapsedSeconds'] = round(time.monotonic()-start,6)
    conn.close()
    return record, data

try:
    external = json.loads(command('get','externalmodel',model,'-n',namespace,'-o','json'))
    providers = [p['ref']['name'] for p in external['spec']['externalProviderRefs']]
    assert providers == ['grid'], providers
    report['maasProviders'] = providers
    ca = json.loads(command('get','configmap','grid-upstream-ca','-n',gateway_namespace,'-o','json'))['data']['service-ca.crt']
    tls = ssl.create_default_context(cadata=ca)
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0))
        port = sock.getsockname()[1]
    forward = subprocess.Popen(kube+['port-forward','-n',gateway_namespace,'service/'+gateway,f'{port}:443'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    deadline = time.monotonic()+30
    while True:
        try:
            with socket.create_connection(('127.0.0.1',port),timeout=1):
                break
        except OSError:
            if time.monotonic()>deadline or forward.poll() is not None:
                raise RuntimeError('Gateway port-forward failed')
            time.sleep(.2)
    token = command('create','token','grid-integration-probe','-n',namespace,'--duration=15m').strip()
    result, body = request('POST','/maas-api/v1/api-keys',token,{'name':'streaming-rollout-'+uuid.uuid4().hex[:8],
                          'subscription':'grid-canary','expiresIn':'15m','ephemeral':True})
    if result['status'] != 201:
        raise RuntimeError('MaaS key creation returned '+str(result['status']))
    issued = json.loads(body)
    api_id, key = issued['id'], issued['key']
    report['envoyVersion'] = json.loads(command('exec','-n',gateway_namespace,'deployment/'+gateway,'--','pilot-agent','request','GET','server_info'))['version']
    payload = {'model':model,'messages':[{'role':'user','content':'Count from one to ten, spelling each number. Do not add explanation.'}], 'max_tokens':64}
    for name, credential, expected in [('missing-key',None,401),('invalid-key','invalid-'+uuid.uuid4().hex,401)]:
        result, _ = request('POST','/v1/chat/completions',credential,payload)
        report['cases'].append({'case':name,**result,'expectedStatus':expected})
        assert result['status']==expected, (name,result['status'])
    for stream, chunked in [(False,False),(False,True),(True,False),(True,True)]:
        body = dict(payload)
        if stream:
            body.update(stream=True,stream_options={'include_usage':True})
        result, data = request('POST','/v1/chat/completions',key,body,chunked=chunked,stream=stream)
        record = {'case':'inference','stream':stream,'chunkedUpload':chunked,**result,'body':data}
        report['cases'].append(record)
        print(json.dumps({k:v for k,v in record.items() if k!='body'}),flush=True)
        assert result['status']==200, result['status']
        if stream:
            events = [line[5:].strip() for line in data.splitlines() if line.startswith('data:')]
            assert events[-1]=='[DONE]'
            assert result['contentType'].startswith('text/event-stream')
            assert any(c.get('delta',{}).get('content') for e in events[:-1] for c in json.loads(e).get('choices',[]))
            arrivals = result['dataEventArrivalSeconds']
            assert len(arrivals)>1 and arrivals[0]<arrivals[-1]
            record['incrementalEventsObserved'] = True
        else:
            assert json.loads(data).get('choices')
    report['passed'] = True
except BaseException as exc:
    report['passed'] = False
    report['error'] = str(exc)
    raise
finally:
    if api_id and forward and forward.poll() is None:
        try:
            result, _ = request('DELETE','/maas-api/v1/api-keys/'+api_id,token)
            report['keyRevocationStatus'] = result['status']
        except Exception as exc:
            report['keyRevocationError'] = str(exc)
    if forward:
        forward.terminate()
        forward.wait(timeout=5)
    (root/'live-grid-streaming-result.json').write_text(json.dumps(report,indent=2)+'\n')
    print('Evidence: '+str(root/'live-grid-streaming-result.json'),flush=True)

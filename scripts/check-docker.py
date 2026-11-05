#!/usr/bin/env python3
"""Isolated end-to-end test of the built zhan image and host bind mounts."""
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request
import urllib.error

ROOT = Path(__file__).resolve().parent.parent
IMAGE = os.environ.get('ZHAN_TEST_IMAGE', 'zhan:local')

def run(*args, check=True):
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
    if check and result.returncode:
        raise RuntimeError(f'{args[0]} failed: {result.stderr[-3000:]}')
    return result.stdout.strip()

def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]

def main():
    parent = ROOT / '.test-runtime'
    parent.mkdir(exist_ok=True)
    base = Path(tempfile.mkdtemp(prefix='docker-', dir=parent)).resolve()
    name = 'cm-test-' + secrets.token_hex(5)
    project = name + '-app'
    data = base / 'data'
    app = base / 'application'
    data.mkdir(); app.mkdir(); (app / 'html').mkdir()
    (app / 'html' / 'index.html').write_text('zhan-bind-ok\n')
    compose = app / 'compose.yaml'
    compose.write_text('''services:
  web:
    image: nginx:stable-alpine
    network_mode: bridge
    volumes:
      - ./html:/usr/share/nginx/html:ro
      - content:/fixture-volume
volumes:
  content: {}
''')
    run('git','init','-b','main',str(app))
    run('git','-C',str(app),'config','user.email','fixture@example.invalid')
    run('git','-C',str(app),'config','user.name','Docker fixture')
    run('git','-C',str(app),'add','compose.yaml','html')
    run('git','-C',str(app),'commit','-m','fixture')
    run('git','-C',str(app),'remote','add','origin',str(app))
    proxy_project = ('cm-proxy-' + hashlib.sha256(str(data / 'proxy').encode()).hexdigest())[:25]
    try:
        run('docker', 'run', '-d', '--name', name,
            '-p', '127.0.0.1::12345',
            '--mount', 'type=bind,src=/var/run/docker.sock,dst=/var/run/docker.sock',
            '--mount', f'type=bind,src={base},dst={base}',
            '-e', f'ZHAN_DATA_DIR={data}', IMAGE)
        endpoint = run('docker', 'port', name, '12345/tcp').splitlines()[0]
        url = 'http://' + endpoint
        jar = http.cookiejar.CookieJar()
        client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
        csrf = ''
        def api(method, path, body=None):
            encoded = json.dumps(body).encode() if body is not None else None
            req = urllib.request.Request(url + '/api/v1' + path, encoded, method=method,
                                         headers={'Content-Type':'application/json', 'X-CSRF-Token':csrf})
            try:
                with client.open(req, timeout=30) as response:
                    raw = response.read()
                    return json.loads(raw) if raw else None
            except urllib.error.HTTPError as error:
                raise RuntimeError(f'{method} {path}: {error.code} {error.read().decode()}') from error
        deadline = time.monotonic() + 45
        while True:
            try:
                api('GET', '/health'); break
            except (OSError, RuntimeError):
                if time.monotonic() > deadline:
                    raise RuntimeError('zhan did not become healthy')
                time.sleep(.5)
        csrf = api('POST','/auth/login',{'username':'admin','password':'123456'})['data']['csrf_token']
        password = secrets.token_urlsafe(18)
        api('POST','/auth/password',{'current_password':'123456','new_password':password,'confirm_password':password})
        csrf = api('POST','/auth/login',{'username':'admin','password':password})['data']['csrf_token']
        print('PASS image health, login and SQLite data mount')
        for tool in [('docker','compose','version'),('docker','buildx','version'),('git','--version'),('ssh','-V')]:
            run('docker','exec',name,*tool)
        # Start only this fixture through the zhan container's Docker CLI.
        def fixture(*args):
            return run('docker','exec',name,'docker','compose','-p',project,'-f',str(compose),*args)
        fixture('up','-d','--wait')
        cid = fixture('ps','-q','web')
        assert run('docker','exec',cid,'wget','-qO-','http://127.0.0.1') == 'zhan-bind-ok'
        run('docker','exec',cid,'sh','-c','echo volume-kept > /fixture-volume/sentinel')
        fixture('up','-d','--force-recreate','--wait')
        cid = fixture('ps','-q','web')
        assert run('docker','exec',cid,'cat','/fixture-volume/sentinel') == 'volume-kept'
        print('PASS sibling container relative bind and named volume persistence')
        discovered = api('GET','/stacks/discover')['data']
        assert any(x['name'] == project for x in discovered), 'host project not discovered'
        stack = api('POST','/stacks/import',{'name':'Docker integration fixture','project_name':project,
                    'project_directory':str(app),'compose_files':[str(compose)],'branch':'main'})['data']
        assert stack['branch'] == 'main', 'mounted Git repository was not bound'
        stack_id = stack['id']
        def task(action):
            task_id = api('POST',f'/stacks/{stack_id}/{action}',{})['data']['id']
            deadline = time.monotonic() + 60
            while True:
                value = api('GET',f'/tasks/{task_id}')['data']
                if value['status'] not in ('pending','running'):
                    assert value['status'] == 'success', value
                    break
                if time.monotonic() > deadline: raise RuntimeError('task timed out')
                time.sleep(.3)
        task('stop'); task('start')
        cid = fixture('ps','-q','web')
        assert run('docker','exec',cid,'cat','/fixture-volume/sentinel') == 'volume-kept'
        assert run('docker','exec',cid,'wget','-qO-','http://127.0.0.1') == 'zhan-bind-ok'
        print('PASS discovery, import, stop/start API with original bind and volume')
        # The proxy creates host-side binds from files written by the zhan container.
        upstream_ip = json.loads(run('docker','inspect',cid))[0]['NetworkSettings']['Networks']
        upstream_ip = next(iter(upstream_ip.values()))['IPAddress']
        cfg = api('GET','/proxy')['data']
        settings = cfg['settings']; settings['http_port'] = free_port(); settings['https_port'] = free_port()
        host = {'id':'0123456789abcdef','name':'Fixture','domain':'fixture.example.test',
                'upstream':'http://' + upstream_ip + ':80','enabled':True,'https':False,'redirect':False,'websocket':True}
        api('PUT','/proxy/config',{'settings':settings,'hosts':[host],'revision':cfg['revision']})
        jid = api('POST','/proxy/jobs',{'action':'apply','host_id':''})['data']['id']
        deadline = time.monotonic() + 90
        while True:
            state = api('GET','/proxy')['data']
            job = next(x for x in state['jobs'] if x['id'] == jid)
            if job['status'] != 'running':
                assert job['status'] == 'success', job
                break
            if time.monotonic() > deadline: raise RuntimeError('proxy task timed out')
            time.sleep(.5)
        request = urllib.request.Request(f"http://127.0.0.1:{settings['http_port']}", headers={'Host':host['domain']})
        with urllib.request.urlopen(request,timeout=10) as response:
            assert response.read().strip() == b'zhan-bind-ok'
        print('PASS managed Nginx config/certificate-directory mounts and HTTP forwarding')
        run('docker','restart',name)
        # Docker may reassign an automatically allocated host port on restart.
        url = 'http://' + run('docker','port',name,'12345/tcp').splitlines()[0]
        deadline = time.monotonic()+30
        while True:
            try:
                api('GET','/health'); break
            except (OSError,RuntimeError):
                if time.monotonic()>deadline: raise
                time.sleep(.5)
        # Re-login avoids relying on restart timing and verifies persisted credentials.
        csrf = api('POST','/auth/login',{'username':'admin','password':password})['data']['csrf_token']
        assert len(api('GET','/stacks')['data']) == 1
        print('PASS zhan restart preserves account and imported project')
    except Exception:
        print(run('docker','logs','--tail','30',name,check=False))
        raise
    finally:
        run('docker','compose','-p',proxy_project,'-f',str(data/'proxy'/'compose.yaml'),'down','--remove-orphans',check=False)
        run('docker','compose','-p',project,'-f',str(compose),'down','-v','--remove-orphans',check=False)
        run('docker','rm','-f',name,check=False)
        shutil.rmtree(base)

if __name__ == '__main__':
    main()

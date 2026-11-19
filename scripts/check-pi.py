#!/usr/bin/env python3
"""Real-model Pi recovery test against a running local Zhan (uses saved provider).

Requires Docker and Python; creates only a uniquely named fixture project.
ZHAN_TEST_URL defaults to http://127.0.0.1:12345.
ZHAN_TEST_PASSWORD_FILE defaults to .test-runtime/admin-password.txt.
"""
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
import urllib.request

ROOT = Path(__file__).resolve().parent.parent

def main():
    token = secrets.token_hex(4)
    project = 'zhan-pi-test-' + token
    local = ROOT / '.test-runtime' / project
    local.mkdir(parents=True)
    if os.name == 'nt':
        workspace = '/run/desktop/mnt/host/' + local.drive[0].lower() + local.as_posix()[2:]
    else:
        workspace = str(local)
    (local / 'app.py').write_text('def add(a, b):\n    return a + b\n', encoding='utf-8')
    (local / 'test_app.py').write_text('import unittest\nfrom app import add\nclass Tests(unittest.TestCase):\n    def test_add(self):\n        self.assertEqual(add(2, 3), 5)\n        self.assertEqual(add(-1, 1), 0)\nif __name__ == "__main__":\n    unittest.main()\n', encoding='utf-8')
    (local / 'Dockerfile').write_text('FROM python:3.13-alpine\nWORKDIR /app\nCOPY app.py test_app.py ./\nRUN python -m unittest -v\nCMD ["python", "-m", "http.server", "8000"]\n', encoding='utf-8')
    (local / 'compose.yaml').write_text('services:\n  app:\n    build: .\n', encoding='utf-8')
    preserved = {name: (local / name).read_bytes() for name in ('test_app.py', 'Dockerfile', 'compose.yaml')}
    jar = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    base = os.environ.get('ZHAN_TEST_URL', 'http://127.0.0.1:12345') + '/api/v1'
    csrf = ''
    def api(method, path, body=None):
        req = urllib.request.Request(base + path, None if body is None else json.dumps(body).encode(), method=method,
            headers={'Content-Type': 'application/json', 'X-CSRF-Token': csrf})
        with client.open(req, timeout=30) as response:
            data = response.read()
            return json.loads(data)['data'] if data else None
    password = Path(os.environ.get('ZHAN_TEST_PASSWORD_FILE', str(ROOT / '.test-runtime/admin-password.txt'))).read_text(encoding='utf-8-sig').strip()
    csrf = api('POST', '/auth/login', {'username': 'admin', 'password': password})['csrf_token']
    subprocess.run(['docker', 'compose', '-p', project, '-f', str(local / 'compose.yaml'), 'up', '-d', '--build', '--wait'], check=True, capture_output=True, timeout=120)
    stack = api('POST', '/stacks/import', {'name': project, 'project_name': project, 'project_directory': workspace, 'compose_files': [workspace + '/compose.yaml']})
    (local / 'app.py').write_text('def add(a, b):\n    return a - b\n', encoding='utf-8')
    job = api('POST', '/ai/jobs', {
        'engine': 'pi', 'name': project, 'prompt': '项目构建失败。请自主检查关联项目状态和最近任务日志，检查工作区代码并运行现有测试，修复根本问题。不要删除或弱化测试，不要绕过 Dockerfile 的测试步骤。修复后通过 stack_action rebuild 重新构建部署，验证真实成功再报告。',
        'workspace': workspace, 'stack_id': stack['id'], 'enabled': True, 'writable': True,
        'allow_actions': True, 'allow_commands': True, 'allow_network': False,
        'test_image': 'python:3.13-alpine', 'schedule': 'event', 'event_name': 'task.failed',
        'timeout_seconds': 300, 'max_steps': 25
    })
    run = None
    try:
        task = api('POST', f"/stacks/{stack['id']}/rebuild", {})
        deadline = time.monotonic() + 360
        while time.monotonic() < deadline:
            runs = [r for r in api('GET', '/ai/runs') if r['job_id'] == job['id']]
            if runs:
                run = api('GET', f"/ai/runs/{runs[0]['id']}")
                if run['status'] not in ('pending', 'running'):
                    break
            time.sleep(2)
        assert run, 'failure event did not trigger Pi'
        (local / 'run.json').write_text(json.dumps(run, ensure_ascii=False, indent=2), encoding='utf-8')
        assert run['status'] == 'success', run['error']
        assert 'run_command' in run['output'] and 'write_file' in run['output'] and 'stack_action' in run['output'], run['output']
        original = api('GET', f"/tasks/{task['id']}")
        assert original['status'] == 'failed', original
        actions = [json.loads(line.split(' → ', 1)[1]) for line in run['output'].splitlines() if line.startswith('工具 stack_action → ')]
        successful = [a for a in actions if a.get('status') == 'success' and a.get('task_id') != task['id']]
        assert successful, 'Pi did not complete a successful deployment task'
        recovered = api('GET', f"/tasks/{successful[-1]['task_id']}")
        assert recovered['status'] == 'success' and recovered.get('ai_run_id') == run['id'], recovered
        check = subprocess.run(['docker', 'run', '--rm', '--network=none', '--mount', f'type=bind,source={local},target=/workspace,readonly', '-w', '/workspace', 'python:3.13-alpine', 'python', '-B', '-m', 'unittest', '-v'], capture_output=True, text=True, encoding='utf-8', timeout=30)
        assert check.returncode == 0, check.stderr
        for name, content in preserved.items():
            assert (local / name).read_bytes() == content, f'{name} changed instead of fixing application code'
        output = subprocess.check_output(['docker', 'compose', '-p', project, '-f', str(local / 'compose.yaml'), 'ps', '--format', 'json'], text=True, encoding='utf-8', timeout=30)
        assert 'running' in output, output
        print(f'PASS real model: build failure -> automatic event -> Pi diagnosis -> file repair -> tests -> successful rebuild/deployment (run {run["id"]})')
        print(f'Evidence: {local / "run.json"}')
    finally:
        if run and run['status'] in ('pending', 'running'):
            api('POST', f"/ai/runs/{run['id']}/cancel", {})
        job['enabled'] = False
        try:
            api('PUT', f"/ai/jobs/{job['id']}", job)
        finally:
            subprocess.run(['docker', 'compose', '-p', project, '-f', str(local / 'compose.yaml'), 'down', '--remove-orphans'], check=False, capture_output=True)

if __name__ == '__main__':
    main()

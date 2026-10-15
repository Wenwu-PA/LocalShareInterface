"""Build a local Git clone and smoke-test it using only Python's standard library."""
from pathlib import Path
import http.cookiejar
import json
import os
import re
import secrets
import shutil
import socket
import ssl
import subprocess
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    folder = ROOT / '.verification' / ('clean-clone-' + uuid.uuid4().hex[:12])
    folder.mkdir(parents=True)
    clone = folder / 'source'
    environment = {k: v for k, v in os.environ.items() if not k.startswith('LANBRIDGE_')}
    go = shutil.which('go') or str(Path.home() / '.cache/lanbridge-toolchain/go/bin/go.exe')
    if not Path(go).is_file():
        raise RuntimeError('Go SDK is required in PATH or the portable toolchain cache')
    environment['LANBRIDGE_GO_TOOLCHAIN'] = go
    shell = shutil.which('pwsh') or shutil.which('powershell')
    if not shell:
        raise RuntimeError('This clean-clone verifier requires Windows PowerShell')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    with (folder / 'build.log').open('w', encoding='utf-8') as log:
        subprocess.run(['git', 'clone', '--no-local', str(ROOT), str(clone)], check=True, stdout=log, stderr=log)
        subprocess.run(['git', 'checkout', '--detach', revision], cwd=clone, check=True, stdout=log, stderr=log)
        assert not (clone / 'web/node_modules').exists()
        assert not (clone / 'runtime').exists()
        subprocess.run([shell, '-NoProfile', '-File', str(clone / 'build.ps1')], cwd=clone, env=environment, check=True, stdout=log, stderr=log)
        for args in (['test', './...'], ['vet', './...']):
            subprocess.run([go, *args], cwd=clone, env=environment, check=True, stdout=log, stderr=log)
    config = clone / 'config.go.yaml'
    shutil.copyfile(clone / 'go-config.example.yaml', config)
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        port = probe.getsockname()[1]
    environment['LANBRIDGE_GO_PORT'] = str(port)
    base = f'https://127.0.0.1:{port}'
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
    process = None
    with (folder / 'server.log').open('w', encoding='utf-8') as log:
        def call(path, body=None):
            headers = {}
            if body is not None:
                headers = {'Content-Type': 'application/json', 'X-CSRF-Token': next(c.value for c in jar if c.name == '__Host-lanbridge_go_csrf')}
            request = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
            with opener.open(request, timeout=10) as response:
                return json.load(response)

        def start():
            nonlocal process
            process = subprocess.Popen([str(clone / 'dist/lanbridge.exe'), '-config', str(config)], cwd=clone, env=environment, stdout=log, stderr=log)
            for _ in range(100):
                if process.poll() is not None:
                    raise RuntimeError('Server exited; inspect ' + str(folder / 'server.log'))
                try:
                    call('/healthz')
                    return
                except OSError:
                    time.sleep(.1)
            raise RuntimeError('Server startup timeout')

        def stop():
            if process is not None and process.poll() is None:
                process.terminate()
                process.wait(timeout=15)

        try:
            start()
            assert call('/api/status')['setup_required']
            with opener.open(base, timeout=10) as response:
                html = response.read().decode()
            assets = re.findall(r'(?:src|href)="(/assets/[^"<>]+)"', html)
            assert assets, 'Embedded production UI is missing'
            for asset in assets:
                with opener.open(base + asset, timeout=10) as response:
                    assert response.status == 200 and response.read(1)
            call('/api/setup', {'username': 'cleanadmin', 'password': secrets.token_urlsafe(24)})
            assert call('/api/me')['role'] == 'admin'
            assert call('/api/resources')['database_bytes'] > 0
            call('/api/files/mkdir', {'path': 'clean-start'})
            assert (clone / 'runtime/files/clean-start').is_dir()
            stop()
            start()
            assert call('/api/me')['username'] == 'cleanadmin'
            assert not call('/api/status')['setup_required']
            assert (clone / 'runtime/files/clean-start').is_dir()
        finally:
            stop()
    status = subprocess.check_output(['git', 'status', '--porcelain'], cwd=clone, text=True)
    assert not status, 'Startup generated unignored files: ' + status
    result = {'result': 'PASS', 'revision': revision, 'clone': str(clone), 'build': True, 'go_tests_and_vet': True, 'embedded_assets': len(assets), 'setup': True, 'restart_session_and_files': True, 'clean_git_status': True, 'port_override': port, 'fresh_os': False}
    (folder / 'result.json').write_text(json.dumps(result, indent=2), encoding='utf-8')
    print(json.dumps(result))


if __name__ == '__main__':
    main()

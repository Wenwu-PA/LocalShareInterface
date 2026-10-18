"""Build a local Git clone and smoke-test it using only Python's standard library."""
from pathlib import Path
import http.cookiejar
import json
import os
import re
import secrets
import shutil
import socket
import struct
import zlib
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
        subprocess.run(['node','--test',*[str(p) for p in sorted((clone/'web/src').glob('*.test.js'))]], cwd=clone, env=environment, check=True, stdout=log, stderr=log)
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
        def call(path, body=None, method=None):
            headers = {}
            if body is not None or method in ('PUT','DELETE'):
                headers = {'Content-Type': 'image/png' if isinstance(body,bytes) else 'application/json', 'X-CSRF-Token': next(c.value for c in jar if c.name == '__Host-lanbridge_go_csrf')}
            data = body if isinstance(body,bytes) else None if body is None else json.dumps(body).encode()
            request = urllib.request.Request(base + path, data=data, headers=headers, method=method)
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
            # Lazy PDF chunks and licensing must also be embedded in the executable.
            optional_assets=list((clone/'web/dist/assets').glob('pdf*'))+list((clone/'web/dist/licenses').glob('pdfjs-*'))
            assert len(optional_assets)>=4, 'PDF chunks or licenses missing from build'
            for asset_file in optional_assets:
                asset_path='/'+asset_file.relative_to(clone/'web/dist').as_posix()
                with opener.open(base+asset_path,timeout=10) as response:
                    assert response.status==200 and response.read()==asset_file.read_bytes(),asset_path
            call('/api/setup', {'username': 'cleanadmin', 'password': secrets.token_urlsafe(24)})
            assert call('/api/me')['role'] == 'admin'
            assert call('/api/resources')['database_bytes'] > 0
            preferences = call('/api/settings')
            preferences['card_order'] = ['devices','checks','internet','availability']
            assert preferences['monitor_interval'] == 20
            preferences['monitor_interval'] = 120
            call('/api/settings', preferences, 'PUT')
            def png_chunk(kind,data):
                return struct.pack('!I',len(data))+kind+data+struct.pack('!I',zlib.crc32(kind+data)&0xffffffff)
            logo = b'\x89PNG\r\n\x1a\n'+png_chunk(b'IHDR',struct.pack('!2I5B',1,1,8,6,0,0,0))+png_chunk(b'IDAT',zlib.compress(b'\0\xff\0\0\xff'))+png_chunk(b'IEND',b'')
            saved_logo = call('/api/branding',logo,'PUT')['logo']
            assert saved_logo.startswith('data:image/png;base64,')
            call('/api/files/mkdir', {'path': 'clean-start'})
            assert (clone / 'runtime/files/clean-start').is_dir()
            stop()
            start()
            assert call('/api/me')['username'] == 'cleanadmin'
            assert not call('/api/status')['setup_required']
            assert call('/api/settings')['card_order']==preferences['card_order']
            assert call('/api/settings')['monitor_interval']==120
            assert call('/api/branding')['logo']==saved_logo
            assert (clone / 'runtime/files/clean-start').is_dir()
        finally:
            stop()
    status = subprocess.check_output(['git', 'status', '--porcelain'], cwd=clone, text=True)
    assert not status, 'Startup generated unignored files: ' + status
    result = {'result': 'PASS', 'revision': revision, 'clone': str(clone), 'build': True, 'go_tests_and_vet': True, 'javascript_tests':True, 'embedded_assets': len(assets), 'pdf_assets_and_licenses':len(optional_assets), 'setup': True, 'restart_session_and_files': True, 'restart_logo_and_card_order':True, 'restart_monitor_interval':True, 'clean_git_status': True, 'port_override': port, 'fresh_os': False}
    (folder / 'result.json').write_text(json.dumps(result, indent=2), encoding='utf-8')
    print(json.dumps(result))


if __name__ == '__main__':
    main()

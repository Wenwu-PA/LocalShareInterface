"""Interrupt actual HTTPS file streams against an isolated Go server."""
from pathlib import Path
import hashlib
import http.client
import json
import os
import secrets
import socket
import ssl
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]


def main():
    binary = ROOT / 'dist/lanbridge.exe'
    context = ssl._create_unverified_context()
    process = connection = None
    with tempfile.TemporaryDirectory(prefix='lanbridge-disconnect-') as folder:
        root = Path(folder)
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        config = root / 'config.yaml'
        config.write_text(f'server:\n  host: 127.0.0.1\n  port: {port}\n', encoding='utf-8')
        cookies, csrf = {}, ''

        def connect():
            return http.client.HTTPSConnection('127.0.0.1', port, context=context, timeout=15)

        def headers():
            return {'Cookie': '; '.join(k+'='+v for k, v in cookies.items()), 'X-CSRF-Token': csrf}

        def call(path, method='GET', body=None, extra=None):
            nonlocal csrf
            hdr = headers()
            if isinstance(body, dict):
                body = json.dumps(body).encode()
                hdr['Content-Type'] = 'application/json'
            hdr.update(extra or {})
            connection.request(method, path, body, hdr)
            response = connection.getresponse()
            raw = response.read()
            assert response.status < 400, (path, response.status, raw[:300])
            for key, value in response.getheaders():
                if key.lower() == 'set-cookie':
                    name, value = value.split(';', 1)[0].split('=', 1)
                    cookies[name] = value
            result = json.loads(raw)
            if isinstance(result, dict) and 'csrf' in result:
                csrf = result['csrf']
            return result

        with (root / 'server.log').open('w', encoding='utf-8') as log:
            try:
                process = subprocess.Popen([str(binary), '-config', str(config)], stdout=log, stderr=log,
                    env={k: v for k, v in os.environ.items() if not k.startswith('LANBRIDGE_')},
                    creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
                for _ in range(100):
                    connection = connect()
                    try:
                        call('/healthz')
                        break
                    except OSError:
                        connection.close()
                        if process.poll() is not None:
                            raise RuntimeError('Server exited during startup')
                        time.sleep(.1)
                else:
                    raise RuntimeError('Startup timeout')
                call('/api/status')
                call('/api/setup', 'POST', {'username': 'disconnectadmin', 'password': secrets.token_urlsafe(24)})
                chunk = bytes(range(256)) * 4096
                total = 16 * len(chunk)
                digest = hashlib.sha256()
                for _ in range(16):
                    digest.update(chunk)
                expected = digest.hexdigest()
                upload = call('/api/uploads', 'POST', {'path': 'disconnect.bin', 'size': total,
                    'sha256': expected, 'conflict': 'skip'})
                endpoint = '/api/uploads/' + upload['id']
                call(endpoint, 'PATCH', chunk, {'Upload-Offset': '0'})
                # Declare a full chunk, send only 64 KiB, then close the transport
                # without TLS close_notify or a complete HTTP request body.
                for _ in range(3):
                    broken = connect()
                    try:
                        broken.putrequest('PATCH', endpoint)
                        for key, value in headers().items():
                            broken.putheader(key, value)
                        broken.putheader('Upload-Offset', str(len(chunk)))
                        broken.putheader('Content-Length', str(len(chunk)))
                        broken.endheaders()
                        broken.send(b'x' * 65536)
                    finally:
                        broken.close()
                # The normal PATCH waits for any previous holder of the upload
                # lock, proving rollback and reuse of the last committed offset.
                for offset in range(len(chunk), total, len(chunk)):
                    state = call(endpoint, 'PATCH', chunk, {'Upload-Offset': str(offset)})
                    assert state['offset'] == offset + len(chunk), state
                assert call(endpoint + '/finish', 'POST', {})['state'] == 'complete'
                broken = connect()
                try:
                    broken.request('GET', '/api/files/download?path=disconnect.bin', headers=headers())
                    response = broken.getresponse()
                    assert response.status == 200
                    prefix = response.read(65536)
                    assert len(prefix) == 65536
                    response.close()
                finally:
                    broken.close()
                # Resume the download through a new authenticated connection.
                digest = hashlib.sha256(prefix)
                connection.request('GET', '/api/files/download?path=disconnect.bin',
                    headers={**headers(), 'Range': f'bytes={len(prefix)}-'})
                response = connection.getresponse()
                assert response.status == 206
                assert response.getheader('Content-Range') == f'bytes {len(prefix)}-{total-1}/{total}'
                received = len(prefix)
                while data := response.read(len(chunk)):
                    received += len(data)
                    digest.update(data)
                assert received == total and digest.hexdigest() == expected
                for _ in range(50):
                    history = [item for item in call('/api/transfers')
                        if item['path'] == 'disconnect.bin' and item['direction'] == 'download']
                    if len(history) == 2:
                        break
                    time.sleep(.1)
                assert sorted(item['status'] for item in history) == ['interrupted', 'partial'], history
                interrupted = next(item for item in history if item['status'] == 'interrupted')
                assert 0 < interrupted['size'] < total, interrupted
                resumed = next(item for item in history if item['status'] == 'partial')
                assert resumed['size'] == total - len(prefix), resumed
                call('/healthz')
                result = {'result': 'PASS', 'interrupted_uploads': 3, 'download_resume_sha256': expected,
                    'bytes': total, 'download_history': 'PASS',
                    'scope': 'local HTTPS disconnects; not physical LAN loss',
                    'revision': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()}
            finally:
                if connection:
                    connection.close()
                if process and process.poll() is None:
                    process.terminate()
                    process.wait(timeout=15)
            logs = (root / 'server.log').read_text(encoding='utf-8')
            assert 'panic' not in logs.lower(), logs[-2000:]
            assert '"level":"ERROR"' not in logs, logs[-2000:]
            output = ROOT / '.verification' / 'go-disconnects.json'
            output.parent.mkdir(exist_ok=True)
            output.write_text(json.dumps(result, indent=2), encoding='utf-8')
            print(json.dumps(result))


if __name__ == '__main__':
    main()

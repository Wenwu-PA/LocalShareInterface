"""Measure cold initial / resources from the actual embedded application."""
from pathlib import Path
import gzip
import hashlib
import json
import os
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.request
from urllib.parse import urlparse
from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]


def main():
    binary = ROOT / 'dist/lanbridge.exe'
    with tempfile.TemporaryDirectory(prefix='lanbridge-initial-') as folder:
        root = Path(folder)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
        config = root / 'config.yaml'
        config.write_text(f'server:\n  port: {port}\n', encoding='utf-8')
        env = {k:v for k,v in os.environ.items() if not k.startswith('LANBRIDGE_')}
        base = f'https://127.0.0.1:{port}'
        with (root/'server.log').open('w', encoding='utf-8') as log:
            process = subprocess.Popen([str(binary), '-config', str(config)], env=env, stdout=log, stderr=log)
            try:
                for _ in range(100):
                    try:
                        urllib.request.urlopen(base+'/healthz', context=ssl._create_unverified_context(), timeout=1).close()
                        break
                    except OSError: time.sleep(.1)
                else: raise RuntimeError('startup failed')
                with sync_playwright() as pw:
                    browser = pw.chromium.launch()
                    page = browser.new_page(ignore_https_errors=True)
                    responses, errors = [], []
                    page.on('response', lambda response: responses.append(response))
                    page.on('pageerror', lambda e: errors.append(str(e)))
                    page.goto(base+'/')
                    page.get_by_role('button', name='Создать администратора', exact=True).wait_for()
                    page.evaluate('document.fonts.ready')
                    page.wait_for_load_state('networkidle')
                    rows = []
                    for response in responses:
                        if response.request.resource_type not in ('document','script','stylesheet','font','image'): continue
                        assert response.url.startswith(base+'/'), response.url
                        assert response.ok, response.url
                        body = response.body()
                        rows.append({'path':urlparse(response.url).path,'type':response.request.resource_type,
                                     'bytes':len(body),'gzip_bytes':len(gzip.compress(body,mtime=0))})
                    assert not errors, errors
                    assert not any('pdf' in r['path'].lower() for r in rows), rows
                    total = sum(r['gzip_bytes'] for r in rows)
                    result = {'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),
                              'browser':browser.version,'cold_context':True,'route':'/', 'screen':'first setup',
                              'gzip_level':9,'rows':rows,'initial_gzip_bytes_including_fonts':total,
                              'budget_bytes':150000,'pass':total<=150000,
                              'note':'Recompressed decoded response bodies; not measured HTTP wire compression.'}
                    out=ROOT/'.verification/go-initial-load.json';out.parent.mkdir(exist_ok=True)
                    out.write_text(json.dumps(result,indent=2),encoding='utf-8')
                    print(json.dumps(result,indent=2))
                    browser.close()
                    assert result['pass'], 'Initial load exceeds 150000 gzip bytes'
            finally:
                process.terminate();process.wait(timeout=15)


if __name__ == '__main__': main()


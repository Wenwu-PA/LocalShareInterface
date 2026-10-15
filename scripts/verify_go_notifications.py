"""Disposable browser/API regression: webhook retry, restart, observer and notes."""
from pathlib import Path
from contextlib import ExitStack
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import re
import secrets
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import urllib.request
from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]


def main():
    healthy = threading.Event()
    observer = threading.Event()
    received = []

    class Receiver(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            observer.set()
            self.send_response(204)
            self.end_headers()

        def do_POST(self):
            body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
            if healthy.is_set():
                received.append(json.loads(body))
            self.send_response(204 if healthy.is_set() else 503)
            self.end_headers()

    receiver = ThreadingHTTPServer(('127.0.0.1', 0), Receiver)
    threading.Thread(target=receiver.serve_forever, daemon=True).start()
    process = None
    try:
        with tempfile.TemporaryDirectory(prefix='lanbridge-notify-') as folder:
            root = Path(folder)
            with socket.socket() as sock:
                sock.bind(('127.0.0.1', 0))
                port = sock.getsockname()[1]
            config = root / 'config.yaml'
            config.write_text(f'server:\n  port: {port}\n', encoding='utf-8')
            target = f'http://127.0.0.1:{receiver.server_port}'
            (root / '.env.go').write_text(f'LANBRIDGE_WEBHOOK_URL="{target}"\nLANBRIDGE_OBSERVER_URL={target}\n', encoding='utf-8')
            env = {k: v for k, v in os.environ.items() if not k.startswith('LANBRIDGE_')}
            base = f'https://127.0.0.1:{port}'
            with (root / 'server.log').open('w', encoding='utf-8') as log, ExitStack() as cleanup:
                def stop():
                    if process is not None and process.poll() is None:
                        process.terminate()
                        process.wait(timeout=10)
                cleanup.callback(stop)
                def start():
                    proc = subprocess.Popen([str(ROOT / 'dist/lanbridge.exe'), '-config', str(config)], env=env, stdout=log, stderr=log)
                    for _ in range(100):
                        try:
                            with urllib.request.urlopen(base+'/healthz', context=ssl._create_unverified_context(), timeout=1):
                                return proc
                        except OSError:
                            time.sleep(.1)
                    proc.terminate()
                    proc.wait(timeout=10)
                    raise RuntimeError('Startup timeout')

                process = start()
                with sync_playwright() as p:
                    browser = p.chromium.launch()
                    context = browser.new_context(ignore_https_errors=True, viewport={'width': 1440, 'height': 1000})
                    page = context.new_page()
                    errors = []
                    page.on('pageerror', lambda e: errors.append(str(e)))
                    page.goto(base)
                    page.locator('[name=username]').fill('notifyadmin')
                    password = secrets.token_urlsafe(24)
                    page.locator('[name=password]').fill(password)
                    page.locator('[name=confirm]').fill(password)
                    page.get_by_role('button', name='Создать администратора', exact=True).click()
                    page.locator('.workspace').wait_for()
                    page.goto(base+'/#notifications')
                    page.get_by_role('button', name='Добавить канал', exact=True).click()
                    page.get_by_label('Название канала', exact=True).fill('Local test')
                    page.get_by_label('Включить канал', exact=True).check()
                    page.get_by_label('Сообщать о диске, лимите БД и восстановлении ресурсов', exact=True).check()
                    page.get_by_role('button', name='Сохранить канал', exact=True).click()
                    page.get_by_role('button', name='Отправить тест', exact=True).click()
                    page.get_by_text('канал вернул HTTP 503', exact=False).wait_for(timeout=20000)
                    process.terminate()
                    process.wait(timeout=10)
                    healthy.set()
                    process = start()
                    page.reload()
                    page.get_by_text('Доставлено', exact=True).wait_for(timeout=30000)
                    assert len(received) == 1 and received[0]['kind'] == 'test', received
                    page.get_by_label('Переменная с URL наблюдателя', exact=True).fill('LANBRIDGE_OBSERVER_URL')
                    page.get_by_label('Включить внешний heartbeat', exact=True).check()
                    page.get_by_role('button', name='Сохранить наблюдателя', exact=True).click()
                    assert observer.wait(40), 'observer did not receive heartbeat'
                    page.reload()
                    page.get_by_text('Журнал доставки', exact=True).wait_for()
                    for width, device in ((1440, 'desktop'), (390, 'mobile')):
                        page.set_viewport_size({'width': width, 'height': 1000})
                        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
                        page.screenshot(path=str(ROOT / f'docs/previews/notifications-{device}.png'), full_page=True)
                    page.set_viewport_size({'width': 1440, 'height': 1000})
                    page.goto(base+'/#overview')
                    page.get_by_role('button', name='Добавить проверку', exact=True).click()
                    with socket.socket() as probe:
                        probe.bind(('127.0.0.1', 0))
                        closed_port = probe.getsockname()[1]
                    dialog = page.locator('dialog')
                    dialog.get_by_label('Название', exact=True).fill('Notes check')
                    dialog.get_by_label('Адрес host:port', exact=True).fill(f'127.0.0.1:{closed_port}')
                    dialog.get_by_label('Интервал, с', exact=True).fill('5')
                    dialog.get_by_label('Ошибок до сбоя', exact=True).fill('1')
                    page.get_by_role('button', name='Сохранить проверку', exact=True).click()
                    page.locator('.monitor-row').filter(has_text='Notes check').get_by_text('Сбой', exact=True).wait_for()
                    page.goto(base+'/#incidents')
                    page.get_by_role('button', name=re.compile('Notes check')).click()
                    page.get_by_label('Заметка', exact=True).fill('Проверка сохранения заметки')
                    page.get_by_role('button', name='Добавить заметку', exact=True).click()
                    page.locator('dialog').get_by_text('Проверка сохранения заметки', exact=True).wait_for()
                    page.get_by_role('button', name='Закрыть', exact=True).click()
                    with page.expect_download() as download:
                        page.get_by_role('link', name='Экспорт CSV', exact=True).click()
                    csv = root / 'incidents.csv'
                    download.value.save_as(str(csv))
                    assert 'Notes check' in csv.read_text(encoding='utf-8')
                    original_settings=page.evaluate("""async()=>{const s=await(await fetch('/api/status')).json();const old=(await(await fetch('/api/files/storage')).json()).settings;const r=await fetch('/api/files/storage',{method:'PUT',headers:{'Content-Type':'application/json','X-CSRF-Token':s.csrf},body:JSON.stringify({...old,critical_free:1000000000000000})});if(!r.ok)throw new Error(await r.text());return old}""")
                    process.terminate();process.wait(timeout=10);process=start();page.reload()
                    def await_kind(kind):
                        for _ in range(100):
                            if any(event['kind']==kind for event in received):return
                            time.sleep(.1)
                        raise AssertionError('No resource delivery: '+kind)
                    await_kind('resource_critical')
                    process.terminate();process.wait(timeout=10);process=start();page.reload()
                    queued=page.evaluate("async()=>await(await fetch('/api/notifications')).json()")
                    assert sum(d['kind']=='resource_critical' for d in queued['deliveries'])==1
                    page.evaluate("""async settings=>{const s=await(await fetch('/api/status')).json();const r=await fetch('/api/files/storage',{method:'PUT',headers:{'Content-Type':'application/json','X-CSRF-Token':s.csrf},body:JSON.stringify(settings)});if(!r.ok)throw new Error(await r.text())}""",original_settings)
                    process.terminate();process.wait(timeout=10);process=start();page.reload()
                    await_kind('resource_recovered')
                    assert not errors, errors
                    browser.close()
                process.terminate()
                process.wait(timeout=10)
                process = None
                logs = (root / 'server.log').read_text(encoding='utf-8')
                assert 'panic' not in logs.lower(), logs
                print(json.dumps({'webhook_retry_after_restart': 'PASS', 'observer': 'PASS', 'incident_notes_csv': 'PASS', 'resource_alert_restart_dedup_recovery': 'PASS', 'browser_errors': errors, 'screenshots': 2}))
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            process.wait(timeout=10)
        receiver.shutdown()
        receiver.server_close()


if __name__ == '__main__':
    main()

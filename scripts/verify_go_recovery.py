"""Corrupt a disposable database; verify recovery and the startup review gate."""
from pathlib import Path
import json, os, secrets, socket, sqlite3, ssl, subprocess, tempfile, time, urllib.request
import psutil
from playwright.sync_api import sync_playwright, expect

ROOT = Path(__file__).resolve().parents[1]

def main():
    with tempfile.TemporaryDirectory(prefix='lanbridge-recovery-') as folder:
        root = Path(folder)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
        config = root/'config.yaml'
        config.write_text(f'server:\n  host: "0.0.0.0"\n  port: {port}\n', encoding='utf-8')
        env = {k:v for k,v in os.environ.items() if not k.startswith('LANBRIDGE_')}
        command = [str(ROOT/'dist/lanbridge.exe'), '-config', str(config)]
        base = f'https://127.0.0.1:{port}'
        process = None
        with (root/'server.log').open('w', encoding='utf-8') as log:
            def start(*args):
                nonlocal process
                process = subprocess.Popen(command+list(args), env=env, stdout=log, stderr=log)
                for _ in range(100):
                    if process.poll() is not None: raise AssertionError('Unexpected exit; '+(root/'server.log').read_text(encoding='utf-8'))
                    try:
                        urllib.request.urlopen(base+'/healthz', context=ssl._create_unverified_context(), timeout=1).close(); return
                    except OSError: time.sleep(.1)
                raise AssertionError('Startup timeout')
            def stop():
                if process is not None and process.poll() is None:
                    process.terminate(); process.wait(timeout=10)
            try:
                start()
                with sync_playwright() as p:
                    browser = p.chromium.launch()
                    context = browser.new_context(ignore_https_errors=True)
                    page = context.new_page(); errors = []
                    page.on('pageerror', lambda e:errors.append(str(e)))
                    page.goto(base); page.locator('[name=username]').fill('recoveryadmin')
                    password = secrets.token_urlsafe(24)
                    page.locator('[name=password]').fill(password); page.locator('[name=confirm]').fill(password)
                    page.get_by_role('button', name='Создать администратора', exact=True).click()
                    page.locator('.workspace').wait_for(); stop()
                    dbpath = root/'runtime/lanbridge-go.sqlite3'
                    backup = root/'runtime/backups/99980000.sqlite3'
                    with sqlite3.connect(dbpath) as source:
                        target = sqlite3.connect(backup)
                        source.backup(target); target.close()
                        source.execute('PRAGMA wal_checkpoint(TRUNCATE)')
                    source.close()
                    (root/'runtime/backups/99990000.sqlite3').write_bytes(b'invalid latest backup')
                    original = dbpath.read_bytes(); damaged = b'CORRUPTED DATABASE'+original[18:]
                    dbpath.write_bytes(damaged)
                    result = subprocess.run(command, env=env, capture_output=True, timeout=20)
                    assert result.returncode == 1
                    marker = Path(str(dbpath)+'.recovery.json')
                    recovery = json.loads(marker.read_text(encoding='utf-8'))
                    assert recovery['ready'] and Path(recovery['backup']) == backup
                    assert (Path(recovery['archive'])/'original.sqlite3').read_bytes() == damaged
                    assert (Path(recovery['archive'])/'backup.sqlite3').read_bytes() == backup.read_bytes()
                    # Repeated normal startup stays closed until explicit review.
                    result = subprocess.run(command, env=env, capture_output=True, timeout=20)
                    assert result.returncode == 1 and marker.exists()
                    start('-accept-database-recovery')
                    listeners = [c for c in psutil.Process(process.pid).net_connections() if c.status == psutil.CONN_LISTEN]
                    assert listeners and all(c.laddr.ip == '127.0.0.1' for c in listeners)
                    status = context.request.get(base+'/api/status').json()
                    assert status['authenticated'] is False
                    page.reload(); page.locator('[name=username]').fill('recoveryadmin')
                    page.locator('[name=password]').fill(password)
                    page.get_by_role('button', name='Войти', exact=True).click()
                    page.locator('.workspace').wait_for()
                    assert not errors, errors
                    browser.close(); stop()
                    with sqlite3.connect(dbpath) as db:
                        assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
                        assert db.execute("SELECT access FROM file_acl WHERE path='.'").fetchone()[0] == 'admin'
                    db.close()
                    print(json.dumps({'result':'PASS','fallback_backup':True,'original_preserved':True,'review_gate':True,'loopback_only':True,'session_revoked':True,'browser_errors':errors}))
            finally: stop()

if __name__ == '__main__': main()

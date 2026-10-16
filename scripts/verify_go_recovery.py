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
                    page.goto(base+'/#settings')
                    page.get_by_role('button',name='Сменить мой пароль',exact=True).click()
                    dialog=page.get_by_role('dialog')
                    expect(dialog.locator('[name=role]')).to_have_value('admin')
                    dialog.get_by_label('Ваш текущий пароль',exact=True).fill(password)
                    changed=secrets.token_urlsafe(24)
                    dialog.locator('[name=password]').fill(changed)
                    dialog.get_by_label('Повторите новый пароль',exact=True).fill(changed)
                    page.set_viewport_size({'width':390,'height':900})
                    assert page.evaluate('()=>document.documentElement.scrollWidth<=innerWidth')
                    dialog.get_by_role('button',name='Сохранить учётную запись',exact=True).click()
                    page.get_by_role('button',name='Войти',exact=True).wait_for()
                    page.locator('[name=username]').fill('recoveryadmin')
                    page.locator('[name=password]').fill(changed)
                    page.get_by_role('button',name='Войти',exact=True).click()
                    page.locator('.workspace').wait_for()
                    # Bounded audit pages and escaped untrusted detail strings.
                    with sqlite3.connect(dbpath) as seed:
                        seed.executemany("INSERT INTO audit(created_at,user_id,action,detail) VALUES(1,1,'settings',?)", [(f'<img src=x onerror="window.auditInjected=true"> fixture {i}',) for i in range(120)])
                    seed.close()
                    page.goto(base+'/#settings')
                    page.get_by_text('Журнал аудита',exact=True).click()
                    audit=page.locator('details').filter(has=page.get_by_text('Журнал аудита',exact=True))
                    expect(audit.get_by_role('button',name='Применить фильтры',exact=True)).to_be_enabled()
                    audit.get_by_label('Событие аудита',exact=True).select_option('settings')
                    audit.get_by_role('button',name='Применить фильтры',exact=True).click()
                    expect(audit.locator('tbody tr')).to_have_count(50)
                    expect(audit.locator('.audit-detail').first).to_contain_text('fixture 119')
                    assert audit.locator('.audit-detail img').count()==0
                    assert page.evaluate('()=>window.auditInjected===undefined')
                    audit.get_by_role('button',name='Старее',exact=True).click()
                    expect(audit.locator('.audit-detail').first).to_contain_text('fixture 69')
                    audit.get_by_role('button',name='Старее',exact=True).click()
                    expect(audit.locator('tbody tr')).to_have_count(20)
                    expect(audit.get_by_role('button',name='Старее',exact=True)).to_be_disabled()
                    audit.get_by_role('button',name='Новее',exact=True).click()
                    expect(audit.locator('tbody tr')).to_have_count(50)
                    assert page.evaluate('()=>document.documentElement.scrollWidth<=innerWidth')
                    # The shared timezone and format update existing dates without reload.
                    fixture=audit.locator('tbody tr').filter(has_text='fixture 69').locator('time')
                    expect(fixture).to_have_text('01.01.1970, 03:00:01')
                    page.get_by_label('Часовой пояс',exact=True).select_option('UTC')
                    page.get_by_label('Формат даты',exact=True).select_option('iso')
                    page.get_by_label('Стартовая страница',exact=True).select_option('files')
                    page.get_by_role('button',name='Сохранить настройки',exact=True).click()
                    expect(page.get_by_text('Настройки сохранены',exact=True)).to_be_visible()
                    expect(fixture).to_have_text('1970-01-01, 00:00:01')
                    page.goto(base+'/')
                    expect(page.get_by_role('heading',name='Файлы',exact=True)).to_be_visible()
                    page.goto(base+'/#overview')
                    expect(page.get_by_role('heading',name='Обзор сети',exact=True)).to_be_visible()
                    summary=page.get_by_label('Устройства сети',exact=True)
                    expect(summary.locator('strong')).to_have_text('0 / 0')
                    with sqlite3.connect(dbpath) as seed:
                        seed.executescript("INSERT INTO devices(identity,ip,status,first_seen) VALUES('fixture1','192.168.1.1','online',1),('fixture2','192.168.1.2','offline',1),('fixture3','192.168.1.3','unknown',1); UPDATE discovery_config SET last_scan=1 WHERE id=1;")
                    seed.close()
                    refresh_devices="()=>dispatchEvent(new CustomEvent('lanbridge-event',{detail:{type:'devices'}}))"
                    page.evaluate(refresh_devices)
                    expect(summary.locator('strong')).to_have_text('1 / 3')
                    expect(summary).to_contain_text('Не отвечают: 1 · Не подтверждены: 1 · Незнакомые: 3')
                    expect(summary.locator('time')).to_have_text('1970-01-01, 00:00:01')
                    page.route('**/api/devices/summary',lambda route: route.fulfill(status=503,content_type='application/json',body='{"error":"test offline"}'))
                    page.evaluate(refresh_devices)
                    expect(summary.get_by_role('status')).to_contain_text('Сводка не обновлена')
                    expect(summary.locator('strong')).to_have_text('1 / 3')
                    page.unroute('**/api/devices/summary')
                    page.evaluate(refresh_devices)
                    expect(summary.get_by_role('status')).to_have_count(0)
                    assert page.evaluate('()=>document.documentElement.scrollWidth<=innerWidth')
                    page.goto(base+'/')
                    expect(page.get_by_role('heading',name='Файлы',exact=True)).to_be_visible()
                    assert not errors, errors
                    browser.close(); stop()
                    with sqlite3.connect(dbpath) as db:
                        assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
                        assert db.execute("SELECT access FROM file_acl WHERE path='.'").fetchone()[0] == 'admin'
                    db.close()
                    print(json.dumps({'result':'PASS','fallback_backup':True,'original_preserved':True,'review_gate':True,'loopback_only':True,'session_revoked':True,'password_changed_in_browser':True,'audit_paging_and_escaping':True,'home_page_and_explicit_routes':True,'browser_errors':errors}))
            finally: stop()

if __name__ == '__main__': main()

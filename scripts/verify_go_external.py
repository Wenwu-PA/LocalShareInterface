"""Exercise the real local gateway through an offline connector/browser fixture."""
from pathlib import Path
import json, os, secrets, socket, ssl, subprocess, tempfile, time, urllib.request
import psutil
from playwright.sync_api import sync_playwright, expect
ROOT=Path(__file__).resolve().parents[1]

def main():
 with tempfile.TemporaryDirectory(prefix='lanbridge-external-') as folder:
  temp=Path(folder);binary=temp/'fake-cloudflared.exe'
  go=os.environ.get('LANBRIDGE_GO_TOOLCHAIN') or str(Path.home()/'.cache/lanbridge-toolchain/go/bin/go.exe')
  subprocess.run([go,'build','-o',str(binary),str(ROOT/'scripts/fixtures/fake_cloudflared.go')],check=True,cwd=ROOT)
  storage=temp/'files';storage.mkdir();content=b'offline public gateway check';(storage/'demo.txt').write_bytes(content)
  with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
  cfg=temp/'config.yaml';cfg.write_text(f'server:\n  port: {port}\nstorage:\n  root: {json.dumps(str(storage))}\nexternal:\n  cloudflared: {json.dumps(str(binary))}\n',encoding='utf-8')
  base=f'https://127.0.0.1:{port}';public='https://lanbridge-test.trycloudflare.com'
  env={k:v for k,v in os.environ.items() if not k.startswith('LANBRIDGE_GO_')}
  with (temp/'server.log').open('w',encoding='utf-8') as log:
   process=subprocess.Popen([str(ROOT/'dist/lanbridge.exe'),'-config',str(cfg)],env=env,stdout=log,stderr=log)
   def wait_server():
    for _ in range(100):
     try:urllib.request.urlopen(base+'/healthz',context=ssl._create_unverified_context(),timeout=1).close();return
     except OSError:time.sleep(.1)
    raise AssertionError('server did not start')
   try:
    wait_server()
    with sync_playwright() as p:
     browser=p.chromium.launch();ctx=browser.new_context(ignore_https_errors=True,viewport={'width':1440,'height':1000});page=ctx.new_page();errors=[]
     page.on('pageerror',lambda e:errors.append(str(e)));page.goto(base)
     password=secrets.token_urlsafe(20);page.locator('[name=username]').fill('externaladmin');page.locator('[name=password]').fill(password);page.locator('[name=confirm]').fill(password);page.get_by_role('button',name='Создать администратора',exact=True).click();page.locator('.sidebar').wait_for()
     page.goto(base+'/#files');page.get_by_label('Действия: demo.txt',exact=True).click();page.get_by_role('button',name='Временная ссылка',exact=True).click();page.get_by_role('button',name='Создать ссылку',exact=True).click();link=page.get_by_label('Ссылка',exact=True);expect(link).to_be_visible();suffix=link.input_value()[len(base):];page.get_by_label('Закрыть',exact=True).click()
     page.goto(base+'/#external');start=page.get_by_role('button',name='Включить внешний доступ',exact=True);expect(start).to_be_disabled()
     page.get_by_label('Подтверждаю публикацию гостевых ссылок через Cloudflare',exact=True).check();expect(start).to_be_enabled();start.click()
     expect(page.get_by_label('Публичный адрес для гостевых ссылок',exact=True)).to_have_value(public,timeout=10000)
     info=json.loads(Path(str(binary)+'.json').read_text());origin=info['origin'];child=psutil.Process(info['pid'])
     guest=browser.new_context(ignore_https_errors=True,accept_downloads=True)
     def relay(route):
      request=route.request;headers=request.all_headers();headers['host']='lanbridge-test.trycloudflare.com';headers['x-forwarded-proto']='https'
      response=route.fetch(url=origin+request.url[len(public):],headers=headers)
      route.fulfill(response=response)
     guest.route(public+'/**',relay);g=guest.new_page();g.on('pageerror',lambda e:errors.append(str(e)))
     g.goto(public+suffix);g.get_by_role('button',name='Подготовить скачивание',exact=True).click()
     # Chromium's download attribute bypasses Playwright routing; use the
     # server's Content-Disposition so the offline relay handles navigation.
     g.get_by_role('link',name='Скачать файл или ZIP',exact=True).evaluate('(a)=>a.removeAttribute("download")')
     with g.expect_download() as transfer:g.get_by_role('link',name='Скачать файл или ZIP',exact=True).click()
     assert Path(transfer.value.path()).read_bytes()==content
     response=g.goto(public+'/api/files');assert response.status==404
     response=g.goto(public+'/api/external');assert response.status==404
     page.bring_to_front();expect(page.get_by_text('guest-download',exact=False)).to_be_visible(timeout=10000)
     output=ROOT/'docs/previews';output.mkdir(exist_ok=True)
     for width,name in [(1440,'desktop'),(390,'mobile')]:
      page.set_viewport_size({'width':width,'height':1000});assert page.evaluate('()=>document.documentElement.scrollWidth<=innerWidth');page.screenshot(path=str(output/f'external-{name}.png'),full_page=True)
     page.get_by_role('button',name='Отключить сейчас',exact=True).click();expect(page.get_by_label('Публичный адрес для гостевых ссылок',exact=True)).to_have_count(0)
     child.wait(timeout=5)
     # After restarting the app, no connector should be started automatically.
     process.terminate();process.wait(timeout=15);process=subprocess.Popen([str(ROOT/'dist/lanbridge.exe'),'-config',str(cfg)],env=env,stdout=log,stderr=log);wait_server();page.reload()
     expect(page.get_by_role('button',name='Отключить сейчас',exact=True)).to_be_disabled()
     # Abrupt parent termination must also kill the connector (Windows Job Object).
     page.get_by_label('Подтверждаю публикацию гостевых ссылок через Cloudflare',exact=True).check();start.click();expect(page.get_by_label('Публичный адрес для гостевых ссылок',exact=True)).to_have_value(public,timeout=10000)
     child=psutil.Process(json.loads(Path(str(binary)+'.json').read_text())['pid']);process.kill();process.wait(timeout=10);child.wait(timeout=5)
     assert not errors,errors;browser.close()
     print(json.dumps({'result':'PASS','offline_fixture':True,'guest_download':True,'admin_routes_blocked':True,'stop':True,'off_after_restart':True,'child_stops_on_parent_crash':True,'screenshots':2,'browser_errors':errors}))
   finally:
    if process.poll() is None:process.terminate();process.wait(timeout=15)
if __name__=='__main__':main()

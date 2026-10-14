"""Isolated browser checks for guest file/ZIP links, passwords, revocation and QR."""
from pathlib import Path
import io, json, os, secrets, socket, ssl, subprocess, tempfile, time, urllib.request, zipfile
from playwright.sync_api import sync_playwright, expect
ROOT=Path(__file__).resolve().parents[1]

def main():
 with tempfile.TemporaryDirectory(prefix='lanbridge-shares-') as folder:
  temporary=Path(folder);storage=temporary/'files';(storage/'Folder').mkdir(parents=True)
  content=b'guest file content\n';(storage/'Folder'/'hello.txt').write_bytes(content)
  with socket.socket() as sock:
   sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
  config=temporary/'config.yaml';config.write_text(f'server:\n  port: {port}\nstorage:\n  root: {json.dumps(str(storage))}\n',encoding='utf-8')
  env={k:v for k,v in os.environ.items() if not k.startswith('LANBRIDGE_GO_')};base=f'https://127.0.0.1:{port}'
  with (temporary/'server.log').open('w',encoding='utf-8') as log:
   process=subprocess.Popen([str(ROOT/'dist/lanbridge.exe'),'-config',str(config)],env=env,stdout=log,stderr=log)
   try:
    for _ in range(100):
     try:urllib.request.urlopen(base+'/healthz',context=ssl._create_unverified_context(),timeout=1).close();break
     except OSError:time.sleep(.1)
    with sync_playwright() as p:
     browser=p.chromium.launch();admin=browser.new_context(ignore_https_errors=True,viewport={'width':1440,'height':1000});page=admin.new_page();errors=[]
     page.on('pageerror',lambda e:errors.append(str(e)))
     page.goto(base);page.locator('[name=username]').fill('shareadmin');password=secrets.token_urlsafe(20)
     page.locator('[name=password]').fill(password);page.locator('[name=confirm]').fill(password);page.get_by_role('button',name='Создать администратора',exact=True).click()
     page.locator('.sidebar').wait_for();page.goto(base+'/#files')
     page.get_by_role('button',name='Подключить телефон',exact=True).click()
     expect(page.get_by_text('Сервер слушает только этот компьютер.',exact=False)).to_be_visible()
     expect(page.get_by_role('img',name='QR-код адреса для телефона')).to_be_visible()
     page.get_by_label('HTTPS-адрес сервера для получателя',exact=True).fill('https://192.168.1.20:8766')
     expect(page.get_by_label('Ссылка',exact=True)).to_have_value('https://192.168.1.20:8766/#files')
     page.get_by_label('Закрыть',exact=True).click()
     page.get_by_role('button',name='▱ Folder',exact=True).click()
     page.get_by_label('Действия: hello.txt',exact=True).click();page.get_by_role('button',name='Временная ссылка',exact=True).click()
     page.get_by_label('Пароль (необязательно)',exact=True).fill('guest-password')
     page.get_by_label('Лимит скачиваний (0 — без лимита)',exact=True).fill('1')
     page.get_by_role('button',name='Создать ссылку',exact=True).click()
     link=page.get_by_label('Ссылка',exact=True);expect(link).to_be_visible();url=link.input_value()
     assert url.startswith(base+'/s/')
     page.get_by_role('img',name='QR-код адреса для телефона').wait_for()
     output=ROOT/'docs/previews';output.mkdir(exist_ok=True)
     # Redact the temporary capability before saving a tracked screenshot.
     page.get_by_label('HTTPS-адрес сервера для получателя',exact=True).fill('https://192.168.1.20:8766')
     page.evaluate("()=>{document.querySelector('.share-link').value='https://192.168.1.20:8766/s/…';document.querySelector('.share-qr').style.visibility='hidden'}")
     page.screenshot(path=str(output/'share-create-desktop.png'))
     page.get_by_label('Закрыть',exact=True).click()
     guest=browser.new_context(ignore_https_errors=True,viewport={'width':390,'height':844},accept_downloads=True);g=guest.new_page();g.on('pageerror',lambda e:errors.append(str(e)))
     g.goto(url);expect(g.get_by_role('heading',name='Защищённый объект',exact=True)).to_be_visible()
     g.get_by_label('Пароль ссылки',exact=True).fill('wrong');g.get_by_role('button',name='Подготовить скачивание',exact=True).click()
     expect(g.get_by_role('alert')).to_have_text('Неверный пароль')
     g.get_by_label('Пароль ссылки',exact=True).fill('guest-password');g.get_by_role('button',name='Подготовить скачивание',exact=True).click()
     expect(g.get_by_role('link',name='Скачать файл или ZIP',exact=True)).to_be_visible()
     assert g.evaluate('()=>document.documentElement.scrollWidth<=innerWidth')
     g.screenshot(path=str(output/'share-guest-mobile.png'))
     with g.expect_download() as transfer:g.get_by_role('link',name='Скачать файл или ZIP',exact=True).click()
     assert Path(transfer.value.path()).read_bytes()==content
     g.reload();expect(g.get_by_role('alert')).to_contain_text('Ссылка недоступна')
     page.get_by_role('navigation',name='Путь к папке',exact=True).get_by_role('button',name='Общая папка',exact=True).click();page.get_by_label('Действия: Folder',exact=True).click();page.get_by_role('button',name='Временная ссылка',exact=True).click();page.get_by_role('button',name='Создать ссылку',exact=True).click()
     link=page.get_by_label('Ссылка',exact=True);expect(link).to_be_visible();folder_url=link.input_value();page.get_by_label('Закрыть',exact=True).click()
     # Links, quotas and sessions must survive a complete process restart.
     process.terminate();process.wait(timeout=15)
     process=subprocess.Popen([str(ROOT/'dist/lanbridge.exe'),'-config',str(config)],env=env,stdout=log,stderr=log)
     for _ in range(100):
      try:urllib.request.urlopen(base+'/healthz',context=ssl._create_unverified_context(),timeout=1).close();break
      except OSError:time.sleep(.1)
     page.reload();page.locator('.sidebar').wait_for()
     g.goto(url);expect(g.get_by_role('alert')).to_contain_text('Ссылка недоступна')
     g.goto(folder_url);g.get_by_role('button',name='Подготовить скачивание',exact=True).click()
     with g.expect_download() as transfer:g.get_by_role('link',name='Скачать файл или ZIP',exact=True).click()
     with zipfile.ZipFile(transfer.value.path()) as archive:assert archive.read('Folder/hello.txt')==content
     page.locator('summary',has_text='Временные ссылки').click();row=page.locator('.upload-history .upload-row').filter(has=page.get_by_text('Folder',exact=True));expect(row).to_be_visible()
     row.get_by_role('button',name='Отозвать',exact=True).click();expect(row).to_have_count(0)
     g.reload();expect(g.get_by_role('alert')).to_contain_text('Ссылка недоступна')
     page.get_by_label('Только активные',exact=True).uncheck();expect(page.get_by_text('Отозвана',exact=False)).to_be_visible()
     assert not errors,errors
     browser.close();print(json.dumps({'result':'PASS','guest_file':True,'guest_zip':True,'password':True,'quota':True,'revoke':True,'qr_rendered':True,'browser_errors':errors}))
   finally:
    process.terminate();process.wait(timeout=15)
if __name__=='__main__':main()

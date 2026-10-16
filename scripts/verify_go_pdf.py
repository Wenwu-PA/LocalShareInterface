"""Isolated PDF canvas, lazy loading, range and error checks in Chromium."""
from pathlib import Path
import json, os, secrets, socket, ssl, subprocess, tempfile, time, urllib.request
from playwright.sync_api import sync_playwright, expect
ROOT=Path(__file__).resolve().parents[1]

def pdf_fixture():
 objects=[
  b'<</Type /Catalog /Pages 2 0 R /OpenAction 7 0 R>>',
  b'<</Type /Pages /Kids [3 0 R 4 0 R] /Count 2>>',
  b'<</Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources <<>> /Contents 5 0 R>>',
  b'<</Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources <<>> /Contents 6 0 R>>',
 ]
 for color in (b'1 0 0',b'0 0 1'):
  stream=color+b' rg 0 0 200 200 re f'
  objects.append(b'<</Length '+str(len(stream)).encode()+b'>>\nstream\n'+stream+b'\nendstream')
 objects.append(b'<</S /JavaScript /JS (this.getURL("https://example.invalid/pdf-action"))>>')
 raw=bytearray(b'%PDF-1.4\n');offsets=[0]
 for i,obj in enumerate(objects,1):
  offsets.append(len(raw));raw.extend(f'{i} 0 obj\n'.encode()+obj+b'\nendobj\n')
 raw.extend(b'%'+b'A'*600000+b'\n');start=len(raw)
 raw.extend(f'xref\n0 {len(objects)+1}\n0000000000 65535 f \n'.encode())
 for offset in offsets[1:]:raw.extend(f'{offset:010d} 00000 n \n'.encode())
 raw.extend(f'trailer\n<</Size {len(objects)+1} /Root 1 0 R>>\nstartxref\n{start}\n%%EOF\n'.encode())
 return bytes(raw)

def main():
 with tempfile.TemporaryDirectory(prefix='lanbridge-pdf-') as folder:
  temporary=Path(folder)
  with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
  config=temporary/'config.yaml';config.write_text(f'server:\n  host: 127.0.0.1\n  port: {port}\n',encoding='utf-8')
  base=f'https://127.0.0.1:{port}'
  env={k:v for k,v in os.environ.items() if not k.startswith('LANBRIDGE_GO_')}
  with (temporary/'server.log').open('w',encoding='utf-8') as log:
   process=subprocess.Popen([str(ROOT/'dist/lanbridge.exe'),'-config',str(config)],env=env,stdout=log,stderr=log)
   try:
    for _ in range(100):
     try:urllib.request.urlopen(base+'/healthz',context=ssl._create_unverified_context(),timeout=1).close();break
     except OSError:time.sleep(.1)
    with sync_playwright() as p:
     browser=p.chromium.launch();context=browser.new_context(ignore_https_errors=True,viewport={'width':1200,'height':900})
     page=context.new_page();errors=[];requests=[];ranges=[]
     page.on('pageerror',lambda e:errors.append(str(e)))
     page.on('request',lambda r:requests.append(r.url))
     page.on('response',lambda r:ranges.append(r.status) if '/api/files/download?' in r.url else None)
     page.goto(base);password=secrets.token_urlsafe(20)
     page.locator('[name=username]').fill('pdfadmin');page.locator('[name=password]').fill(password);page.locator('[name=confirm]').fill(password)
     page.get_by_role('button',name='Создать администратора',exact=True).click();page.locator('.sidebar').wait_for()
     page.goto(base+'/#files')
     for name,content in [('pages.pdf',pdf_fixture()),('broken.pdf',b'not a pdf')]:
      page.locator('input[type=file][multiple]:not([webkitdirectory])').set_input_files({'name':name,'mimeType':'application/pdf','buffer':content})
      page.get_by_role('link',name='▤ '+name,exact=True).wait_for(timeout=30000)
     assert not any('pdf-preview-' in url or 'pdf.worker' in url for url in requests)
     def preview(name):
      page.get_by_label('Действия: '+name,exact=True).click();page.get_by_role('button',name='Предпросмотр',exact=True).click()
     preview('pages.pdf')
     page.get_by_text('Страница 1 из 2',exact=True).wait_for()
     page.locator('.pdf-page[aria-busy=false]').wait_for()
     assert page.locator('canvas').evaluate('(c)=>Array.from(c.getContext("2d").getImageData(10,10,1,1).data)')==[255,0,0,255]
     page.get_by_role('button',name='Следующая страница',exact=True).click()
     page.locator('.pdf-page[aria-busy=false]').wait_for()
     assert page.locator('canvas').evaluate('(c)=>Array.from(c.getContext("2d").getImageData(10,10,1,1).data)')==[0,0,255,255]
     page.get_by_label('Масштаб PDF',exact=True).select_option('2')
     page.locator('.pdf-page[aria-busy=false]').wait_for()
     assert page.locator('canvas').get_attribute('width')=='400'
     page.set_viewport_size({'width':390,'height':900})
     assert page.evaluate('()=>document.documentElement.scrollWidth<=innerWidth')
     page.get_by_role('button',name='Предыдущая страница',exact=True).click()
     page.get_by_label('Закрыть предпросмотр',exact=True).click()
     preview('broken.pdf')
     expect(page.get_by_role('alert')).to_contain_text('Не удалось открыть PDF')
     page.get_by_label('Закрыть предпросмотр',exact=True).click()
     assert 206 in ranges,ranges
     assert any('pdf.worker' in url for url in requests)
     assert not [url for url in requests if not url.startswith(base)],requests
     assert not errors,errors
     browser.close()
     print(json.dumps({'result':'PASS','two_canvas_pages':True,'range':True,'lazy_module':True,'corrupt_file':True,'external_requests':0,'browser_errors':errors}))
   finally:process.terminate();process.wait(timeout=10)
if __name__=='__main__':main()

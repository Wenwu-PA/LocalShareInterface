"""Isolated Chromium file workflow, resume, range/ZIP, and virtual list checks."""
from pathlib import Path
import json, os, secrets, socket, ssl, subprocess, tempfile, time, urllib.request
from playwright.sync_api import sync_playwright
ROOT=Path(__file__).resolve().parents[1]
def main():
 with tempfile.TemporaryDirectory(prefix="lanbridge-files-") as folder:
  temporary=Path(folder)
  with socket.socket() as sock:
   sock.bind(("127.0.0.1",0));port=sock.getsockname()[1]
  config=temporary/"config.yaml"
  config.write_text(f"server:\n  port: {port}\n",encoding="utf-8")
  env={k:v for k,v in os.environ.items() if not k.startswith("LANBRIDGE_GO_")}
  base=f"https://127.0.0.1:{port}"
  with (temporary/"server.log").open("w",encoding="utf-8") as log:
   process=subprocess.Popen([str(ROOT/"dist/lanbridge.exe"),"-config",str(config)],env=env,stdout=log,stderr=log)
   try:
    for _ in range(100):
     try:
      urllib.request.urlopen(base+"/healthz",context=ssl._create_unverified_context(),timeout=1).close();break
     except OSError:time.sleep(.1)
    with sync_playwright() as p:
     browser=p.chromium.launch();context=browser.new_context(ignore_https_errors=True,viewport={"width":1440,"height":1000},accept_downloads=True)
     page=context.new_page();errors=[];page.on("pageerror",lambda e:errors.append(str(e)))
     page.goto(base);page.locator('[name=username]').fill("fileadmin")
     password=secrets.token_urlsafe(20)
     page.locator('[name=password]').fill(password);page.locator('[name=confirm]').fill(password)
     page.get_by_role("button",name="Создать администратора",exact=True).click()
     page.locator('.sidebar').wait_for();page.goto(base+'/#files')
     page.get_by_label('Вид файлов',exact=True).select_option('grid')
     page.reload();page.get_by_label('Вид файлов',exact=True).wait_for()
     assert page.get_by_label('Вид файлов',exact=True).input_value()=='grid'
     page.get_by_label('Вид файлов',exact=True).select_option('list')
     page.reload();page.get_by_label('Вид файлов',exact=True).wait_for()
     assert page.get_by_label('Вид файлов',exact=True).input_value()=='list'
     page.get_by_role('button',name='Настройки хранилища',exact=True).click()
     page.get_by_label('Общая квота, МиБ',exact=True).fill('64.5')
     page.get_by_role('button',name='Сохранить настройки',exact=True).click()
     page.locator('dialog').wait_for(state='detached')
     page.reload()
     page.get_by_role('button',name='Настройки хранилища',exact=True).click()
     assert page.get_by_label('Общая квота, МиБ',exact=True).input_value()=='64.5'
     assert page.get_by_label('Резерв свободного места, МиБ',exact=True).input_value()=='64'
     page.get_by_label('Резерв свободного места, МиБ',exact=True).fill('15')
     page.get_by_role('button',name='Сохранить настройки',exact=True).click()
     assert page.locator('dialog').is_visible()
     assert not page.get_by_label('Резерв свободного места, МиБ',exact=True).evaluate('(e)=>e.validity.valid')
     page.get_by_label('Закрыть',exact=True).click()
     page.get_by_role("button",name="Новая папка",exact=True).click()
     page.locator('dialog input[name=name]').fill('Фото/Пустая')
     page.get_by_role("button",name="Применить",exact=True).click()
     page.get_by_role('button',name='▱ Фото',exact=True).wait_for()
     tree=page.get_by_role('complementary',name='Дерево папок')
     tree.get_by_role('button',name='Обновить дерево',exact=True).click()
     tree.get_by_role('button',name='Раскрыть Фото',exact=True).click()
     tree.get_by_role('button',name='Пустая',exact=True).click()
     page.get_by_role('heading',name='Папка пуста',exact=True).wait_for()
     tree.get_by_role('button',name='Общая папка',exact=True).click()
     page.get_by_role('button',name='▱ Фото',exact=True).wait_for()
     tree.get_by_role('button',name='Свернуть дерево папок',exact=True).click()
     assert tree.get_by_role('navigation').count()==0
     tree.get_by_role('button',name='Показать дерево папок',exact=True).click()

     content=b'hello file storage'*100000
     page.locator('input[type=file][multiple]:not([webkitdirectory])').set_input_files({"name":"sample.txt","mimeType":"text/plain","buffer":content})
     page.get_by_role('link',name='▤ sample.txt',exact=True).wait_for(timeout=30000)
     page.locator('summary',has_text='История передач').click()
     page.locator('.upload-history strong').get_by_text('sample.txt',exact=True).wait_for()
     assert 'Средняя скорость:' in page.locator('.transfer-timing').first.inner_text()
     page.get_by_label('Поиск в истории передач',exact=True).fill('несуществующий')
     page.locator('.upload-history').get_by_text('Ничего не найдено.',exact=True).wait_for()
     page.get_by_label('Поиск в истории передач',exact=True).fill('')
     page.locator('summary',has_text='История передач').click()

     page.get_by_label('Действия: sample.txt',exact=True).click()
     page.get_by_role('button',name='Предпросмотр',exact=True).click()
     page.locator('.file-preview-text').wait_for()
     assert page.locator('.file-preview-text').inner_text()==content[:65536].decode()
     page.get_by_text('UTF-8 · показаны первые 64 КиБ',exact=True).wait_for()
     page.get_by_label('Закрыть предпросмотр',exact=True).click()
     with page.expect_download() as downloading:page.get_by_role('link',name='▤ sample.txt',exact=True).click()
     downloaded=downloading.value;assert Path(downloaded.path()).read_bytes()==content
     page.locator('summary',has_text='История передач').click()
     page.locator('.upload-history .upload-row').filter(has_text='Скачивание').first.wait_for()
     page.locator('summary',has_text='История передач').click()

     viewport=page.get_by_role('list',name='Список файлов',exact=True)
     viewport.press('Control+a')
     assert page.get_by_label('Выбрать sample.txt',exact=True).is_checked()
     assert page.get_by_label('Выбрать Фото',exact=True).is_checked()
     viewport.press('Escape')
     assert not page.get_by_label('Выбрать sample.txt',exact=True).is_checked()
     assert page.get_by_role('button',name='Скачать ZIP',exact=True).is_disabled()
     page.get_by_label('Выбрать sample.txt',exact=True).check()
     viewport.press('F2')
     rename=page.locator('dialog input[name=name]')
     rename.wait_for(state='visible')
     assert rename.input_value()=='sample.txt'
     rename.press('Control+a')
     page.wait_for_function("()=>{const e=document.querySelector('dialog input[name=name]');return e.selectionEnd-e.selectionStart===e.value.length}")
     page.get_by_role('button',name='Отмена',exact=True).click()
     viewport.press('Shift+Delete')
     assert page.locator('dialog').count()==0
     viewport.press('Delete')
     page.get_by_role('heading',name='Выбранное в корзину',exact=True).wait_for()
     page.get_by_role('button',name='Отмена',exact=True).click()
     page.get_by_role('link',name='▤ sample.txt',exact=True).wait_for()
     viewport.press('Control+f')
     search=page.get_by_label('Поиск файлов',exact=True)
     assert search.evaluate('(e)=>e===document.activeElement')
     search.press('Control+a')
     assert not page.get_by_label('Выбрать Фото',exact=True).is_checked()
     with page.expect_download() as downloading:page.get_by_role('button',name='Скачать ZIP',exact=True).click()
     import zipfile
     with zipfile.ZipFile(downloading.value.path()) as archive:assert archive.read('sample.txt')==content
     page.locator('summary',has_text='История передач').click()
     page.locator('.upload-history .upload-row').filter(has_text='Скачивание ZIP').first.wait_for()
     page.get_by_label('Тип передачи',exact=True).select_option('download_zip')
     assert page.locator('.upload-history .upload-row').count()==1
     assert 'fileadmin' in page.locator('.upload-history .upload-row').inner_text()
     page.get_by_label('Поиск в истории передач',exact=True).fill('fileadmin')
     assert page.locator('.upload-history .upload-row').count()==1
     page.get_by_label('Тип передачи',exact=True).select_option('upload')
     assert page.locator('.upload-history .upload-row').count()==1
     assert 'Загрузка' in page.locator('.upload-history .upload-row').inner_text()
     page.get_by_label('Поиск в истории передач',exact=True).fill('')
     page.get_by_label('Тип передачи',exact=True).select_option('')

     page.locator('summary',has_text='История передач').click()

     page.get_by_label('Действия: sample.txt',exact=True).click();page.get_by_role('button',name='Копировать',exact=True).click()
     page.locator('dialog input[name=name]').fill('copy.txt');page.get_by_role('button',name='Применить',exact=True).click()
     page.get_by_role('link',name='▤ copy.txt',exact=True).wait_for()
     page.get_by_label('Действия: copy.txt',exact=True).click();page.get_by_role('button',name='В корзину',exact=True).click();page.get_by_role('button',name='Применить',exact=True).click()
     page.get_by_role('button',name='Корзина',exact=True).click();page.get_by_role('button',name='Восстановить',exact=True).click()
     page.locator('dialog').get_by_text('Корзина пуста.',exact=True).wait_for();page.get_by_label('Закрыть',exact=True).click()
     page.get_by_role('link',name='▤ copy.txt',exact=True).wait_for()
     # Bulk move two existing files, then restore both to keep later scenarios intact.
     for name in ('copy.txt','sample.txt'):page.get_by_label('Выбрать '+name,exact=True).check()
     viewport.press('Delete')
     page.get_by_role('button',name='Перенести в корзину',exact=True).click()
     page.get_by_text('Перенесено: 2. Ошибок: 0. Не обработано: 0.',exact=True).wait_for()
     page.get_by_role('button',name='Готово',exact=True).click()
     page.get_by_role('button',name='Корзина',exact=True).click()
     for remaining in (2,1):
      page.wait_for_function('(n)=>document.querySelectorAll("dialog .upload-row").length===n',arg=remaining)
      page.get_by_role('button',name='Восстановить',exact=True).first.click()
     page.locator('dialog').get_by_text('Корзина пуста.',exact=True).wait_for()
     page.get_by_label('Закрыть',exact=True).click()
     # Bulk copy retains originals, detects conflicts, then move preserves bytes.
     for expected in ('Скопировано: 2. Ошибок: 0. Не обработано: 0.', 'Скопировано: 0. Ошибок: 2. Не обработано: 0.'):
      for name in ('copy.txt','sample.txt'):page.get_by_label('Выбрать '+name,exact=True).check()
      page.get_by_role('button',name='Копировать выбранное',exact=True).click()
      page.get_by_label('Папка назначения',exact=True).fill('Фото/Пустая')
      page.get_by_role('button',name='Копировать',exact=True).click()
      page.get_by_text(expected,exact=True).wait_for()
      page.get_by_role('button',name='Готово',exact=True).click()
     page.get_by_role('button',name='▱ Фото',exact=True).click()
     page.get_by_role('button',name='▱ Пустая',exact=True).click()
     for name in ('copy.txt','sample.txt'):page.get_by_label('Выбрать '+name,exact=True).check()
     page.get_by_role('button',name='Переместить выбранное',exact=True).click()
     page.get_by_label('Папка назначения',exact=True).fill('Фото')
     page.get_by_role('button',name='Переместить',exact=True).click()
     page.get_by_text('Перенесено: 2. Ошибок: 0. Не обработано: 0.',exact=True).wait_for()
     page.get_by_role('button',name='Готово',exact=True).click()
     page.get_by_role('heading',name='Папка пуста',exact=True).wait_for()
     for name in ('copy.txt','sample.txt'):
      response=context.request.get(base+'/api/files/download',params={'path':'Фото/'+name})
      assert response.status==200 and response.body()==content
      page.evaluate("""async(path)=>{const s=await(await fetch('/api/status')).json();const r=await fetch('/api/files/operation',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':s.csrf},body:JSON.stringify({action:'trash',source:path})});if(!r.ok)throw new Error(await r.text())}""",'Фото/'+name)
     page.get_by_role('navigation',name='Путь к папке').get_by_role('button',name='Общая папка',exact=True).click()
     # A partially uploaded file survives a reload and requires the original hash.
     page.evaluate("""async()=>{const s=await(await fetch('/api/status')).json();const raw=new TextEncoder().encode('resume-content');const hash=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',raw)),n=>n.toString(16).padStart(2,'0')).join('');const u=await(await fetch('/api/uploads',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':s.csrf},body:JSON.stringify({path:'resume.txt',size:raw.length,sha256:hash,conflict:'skip'})})).json();const r=await fetch('/api/uploads/'+u.id,{method:'PATCH',headers:{'X-CSRF-Token':s.csrf,'Upload-Offset':'0'},body:raw.slice(0,4)});if(!r.ok)throw new Error(await r.text())}""")
     page.reload();page.get_by_role('button',name='Выбрать файл',exact=True).wait_for()
     with page.expect_file_chooser() as chooser:page.get_by_role('button',name='Выбрать файл',exact=True).click()
     chooser.value.set_files({"name":"resume.txt","mimeType":"text/plain","buffer":b'WRONG--CONTENT'})
     page.get_by_text('Выбран другой файл: SHA-256 не совпадает',exact=True).wait_for()
     # Reload discards the rejected File reference; select the correct original.
     page.reload();page.get_by_role('button',name='Выбрать файл',exact=True).wait_for()
     with page.expect_file_chooser() as chooser:page.get_by_role('button',name='Выбрать файл',exact=True).click()
     chooser.value.set_files({"name":"resume.txt","mimeType":"text/plain","buffer":b'resume-content'})
     page.get_by_role('link',name='▤ resume.txt',exact=True).wait_for(timeout=30000)
     for file_view in ('list','grid'):
      page.get_by_label('Вид файлов',exact=True).select_option(file_view)
      page.get_by_label('Выбрать copy.txt',exact=True).check()
      page.get_by_label('Выбрать sample.txt',exact=True).click(modifiers=['Shift'])
      for name in ('copy.txt','resume.txt','sample.txt'):
       assert page.get_by_label('Выбрать '+name,exact=True).is_checked()
      page.get_by_label('Выбрать copy.txt',exact=True).click(modifiers=['Shift'])
      for name in ('copy.txt','resume.txt','sample.txt'):
       assert not page.get_by_label('Выбрать '+name,exact=True).is_checked()
     page.get_by_label('Вид файлов',exact=True).select_option('list')
     output=ROOT/'docs/previews';output.mkdir(exist_ok=True)
     for theme in ('dark','light'):
      page.evaluate('(t)=>document.documentElement.dataset.theme=t',theme)
      for width,device in ((1440,'desktop'),(390,'mobile')):
       page.set_viewport_size({'width':width,'height':1000});page.evaluate('()=>{document.activeElement?.blur();window.scrollTo(0,0)}')
       page.screenshot(path=str(output/f'files-{device}-{theme}.png'),full_page=True)
       assert page.evaluate('()=>document.documentElement.scrollWidth<=innerWidth'), 'horizontal overflow'
     page.set_viewport_size({"width":1440,"height":1000})
     page.get_by_label('Сортировка файлов',exact=True).select_option('size')
     with page.expect_response(lambda r:'/api/files?' in r.url and 'order=desc' in r.url) as sorted_response:
      page.get_by_label('Порядок сортировки',exact=True).select_option('desc')
     entries=sorted_response.value.json()
     sizes=[item['size'] for item in entries if not item['directory']]
     assert sizes==sorted(sizes,reverse=True) and len(set(sizes))>1
     page.wait_for_function("()=>document.querySelector('.file-row a')?.textContent.includes('copy.txt')")
     with page.expect_response(lambda r:'/api/files?' in r.url and 'order=asc' in r.url) as sorted_response:
      page.get_by_label('Порядок сортировки',exact=True).select_option('asc')
     entries=sorted_response.value.json()
     sizes=[item['size'] for item in entries if not item['directory']]
     assert sizes==sorted(sizes)
     page.wait_for_function("()=>document.querySelector('.file-row a')?.textContent.includes('resume.txt')")
     page.get_by_label('Сортировка файлов',exact=True).select_option('name')
     # Generate 1,100 real empty folders through the API to exercise pagination.
     page.evaluate("""async()=>{const s=await(await fetch('/api/status')).json();for(let i=0;i<1100;i++){const r=await fetch('/api/files/mkdir',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':s.csrf},body:JSON.stringify({path:'load-'+String(i).padStart(4,'0')})});if(!r.ok)throw new Error(await r.text())}}""")
     page.get_by_role('button',name='Обновить',exact=True).click();page.get_by_role('button',name='Загрузить ещё',exact=True).wait_for()
     page.get_by_role('button',name='Загрузить ещё',exact=True).click();page.wait_for_function("()=>document.querySelector('.file-actions .muted')?.textContent.includes('1000 объектов')")
     page.get_by_role('button',name='Загрузить ещё',exact=True).click();page.wait_for_function("()=>document.querySelector('.file-actions .muted')?.textContent.includes('1104 объектов')")
     tree.get_by_role('button',name='Обновить дерево',exact=True).click()
     tree.get_by_role('button',name='load-0000',exact=True).wait_for()
     assert tree.locator('.folder-tree-row').count()==50
     tree.get_by_role('button',name='Далее',exact=True).click()
     tree.get_by_role('button',name='load-0050',exact=True).wait_for()
     assert tree.locator('.folder-tree-row').count()==50
     viewport.press('Control+a')
     page.wait_for_function("()=>document.querySelector('.file-actions .muted')?.textContent.includes('выбрано 1000')")
     page.get_by_role('button',name='Снять выбор',exact=True).click()
     assert page.locator('.file-row').count()<=18
     page.locator('.file-viewport').evaluate('(e)=>e.scrollTop=48000')
     page.wait_for_function("()=>document.querySelector('.file-row')?.style.top!=='0px'")
     assert page.locator('.file-row').count()<=18
     page.get_by_label('Вид файлов',exact=True).select_option('grid')
     page.locator('.file-tile').first.wait_for()
     assert page.locator('.file-tile').count()<=35
     page.locator('.file-viewport').evaluate('(e)=>e.scrollTop=e.scrollHeight')
     page.get_by_role('link',name='▤ sample.txt',exact=True).wait_for()
     page.set_viewport_size({'width':390,'height':1000})
     page.wait_for_function("()=>document.querySelector('.file-tile')?.style.top==='0px'")
     assert page.evaluate('()=>document.documentElement.scrollWidth<=innerWidth')
     assert page.locator('.file-tile').count()<=7
     page.get_by_label('Вид файлов',exact=True).select_option('list')
     page.set_viewport_size({'width':1440,'height':1000})
     page.get_by_label('Поиск файлов').fill('sample');page.get_by_role('link',name='▤ sample.txt',exact=True).wait_for()
     page.get_by_label('Поиск файлов').fill('');page.get_by_role('button',name='▱ load-0000',exact=True).wait_for()
     # Real resource probe with an artificial reserve, without filling the disk.
     resource_settings=page.evaluate("""async()=>{const s=await(await fetch('/api/status')).json();const old=(await(await fetch('/api/files/storage')).json()).settings;const r=await fetch('/api/files/storage',{method:'PUT',headers:{'Content-Type':'application/json','X-CSRF-Token':s.csrf},body:JSON.stringify({...old,critical_free:1000000000000000})});if(!r.ok)throw new Error(await r.text());return old}""")
     page.goto(base+'/#overview');page.reload()
     page.get_by_text('Мало места в хранилище.',exact=False).wait_for()
     page.get_by_role('heading',name='ПАМЯТЬ ПРОЦЕССА',exact=True).wait_for()
     for width,device in ((1440,'desktop'),(390,'mobile')):
      page.set_viewport_size({'width':width,'height':1000})
      assert page.evaluate('()=>document.documentElement.scrollWidth<=innerWidth')
      page.screenshot(path=str(output/f'resources-{device}.png'),full_page=True)
     page.evaluate("""async settings=>{const s=await(await fetch('/api/status')).json();const r=await fetch('/api/files/storage',{method:'PUT',headers:{'Content-Type':'application/json','X-CSRF-Token':s.csrf},body:JSON.stringify(settings)});if(!r.ok)throw new Error(await r.text())}""",resource_settings)
     page.reload();page.get_by_role('heading',name='ПАМЯТЬ ПРОЦЕССА',exact=True).wait_for()
     assert page.get_by_text('Мало места в хранилище.',exact=False).count()==0
     assert not errors,errors
     browser.close()
     print(json.dumps({'result':'PASS','upload_bytes':len(content),'virtual_items':1104,'max_rendered_rows':18,'screenshots':6,'resource_warning_and_recovery':True,'browser_errors':errors}))
   finally:
    process.terminate();process.wait(timeout=15)
if __name__=='__main__':main()

import {h} from 'preact';
import {useEffect, useRef, useState} from 'preact/hooks';
import htm from 'htm';
const html = htm.bind(h);
const limit = 65536;
export function FilePreview({item, onClose}) {
 const ref = useRef(), [text, setText] = useState(null), [error, setError] = useState('');
 const extension = item.name.split('.').pop().toLowerCase();
 const kind = ['txt','md','log','csv','json','yaml','yml','xml','html','css','js','py','go'].includes(extension) ? 'text'
  : ['png','jpg','jpeg','gif','webp','avif','bmp'].includes(extension) ? 'image'
  : ['mp4','webm','ogv','mov'].includes(extension) ? 'video' : 'unsupported';
 const url = '/api/files/download?' + new URLSearchParams({path:item.path});
 useEffect(() => {
  ref.current.showModal();
  const controller = new AbortController();
  if (kind === 'text') (async () => {
   try {
    if (!item.size) {setText('');return}
    const response = await fetch(url+'&inline=1', {signal:controller.signal, headers:{Range:'bytes=0-65535'}});
    if (!response.ok) throw new Error('Не удалось открыть файл (' + response.status + ')');
    const reader = response.body.getReader(), buffer = new Uint8Array(limit);
    let used = 0;
    try {
     while (used < limit) {
      const {done, value} = await reader.read();if (done) break;
      const piece = value.subarray(0, limit-used);buffer.set(piece, used);used += piece.length;
     }
    } finally {await reader.cancel()}
    setText(new TextDecoder('utf-8').decode(buffer.subarray(0,used)));
   } catch (e) {if (e.name !== 'AbortError') setError(e.message)}
  })();
  return () => controller.abort();
 }, []);
 return html`<dialog ref=${ref} class="monitor-dialog file-preview" onCancel=${onClose}>
  <div class="panel-heading"><h2>${item.name}</h2><button class="icon-button" aria-label="Закрыть предпросмотр" onClick=${onClose}>×</button></div>
  ${error ? html`<p class="form-error" role="alert">${error}</p>` : kind === 'text' ? html`
   <p class="muted">UTF-8${item.size>limit?' · показаны первые 64 КиБ':''}</p>
   ${text===null?html`<p role="status">Загрузка…</p>`:html`<pre class="file-preview-text">${text||'Пустой файл'}</pre>`}`
   : kind === 'image' ? html`<img src=${url+'&inline=1'} alt=${item.name} onError=${()=>setError('Браузер не смог открыть изображение.')}/>`
   : kind === 'video' ? html`<video src=${url+'&inline=1'} controls preload="metadata" onError=${()=>setError('Браузер не поддерживает этот видеоформат или файл недоступен.')}/>`
   : html`<p>Предпросмотр этого формата пока не поддерживается.</p>`}
  <div class="dialog-actions"><a class="button secondary" href=${url} download=${item.name}>Скачать файл</a></div>
 </dialog>`;
}

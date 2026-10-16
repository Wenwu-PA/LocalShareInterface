import {h} from 'preact';
import {useEffect,useRef,useState} from 'preact/hooks';
import htm from 'htm';
import {getDocument,GlobalWorkerOptions} from 'pdfjs-dist';
import workerURL from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
import {pdfScale} from './pdf-limits.js';
const html=htm.bind(h);
GlobalWorkerOptions.workerSrc=workerURL;
export function PDFPreview({url}) {
 const[doc,setDoc]=useState(null),[error,setError]=useState(''),[page,setPage]=useState(1),[zoom,setZoom]=useState(1);
 useEffect(()=>{
  let alive=true;
  const task=getDocument({url,disableStream:true,disableAutoFetch:true,rangeChunkSize:262144,disableFontFace:true,useSystemFonts:true,useWasm:false,enableXfa:false,maxImageSize:16*1024*1024,canvasMaxAreaInBytes:16*1024*1024,stopAtErrors:true});
  const timer=setTimeout(()=>{if(alive){setError('PDF не загрузился за 30 секунд. Скачайте файл для просмотра.');task.destroy().catch(()=>{})}},30000);
  task.promise.then(value=>{if(alive)setDoc(value)}).catch(e=>{if(alive)setError(e.name==='PasswordException'?'PDF защищён паролем. Скачайте файл и откройте в своём просмотрщике.':'Не удалось открыть PDF: файл повреждён, недоступен или формат не поддерживается.');}).finally(()=>clearTimeout(timer));
  return()=>{alive=false;clearTimeout(timer);task.destroy().catch(()=>{})};
 },[url]);
 return html`<div class="pdf-preview">
  <p class="muted"><a href="/licenses/pdfjs-NOTICE.txt" target="_blank" rel="noopener">PDF.js</a> · Постраничный просмотр. Ссылки, вложения и формы PDF неактивны.</p>
  ${error?html`<p role="alert" class="form-error">${error}</p>`:doc?html`<div class="dialog-actions pdf-toolbar"><button class="button secondary" disabled=${page===1} onClick=${()=>setPage(n=>n-1)}>Предыдущая страница</button><span role="status">Страница ${page} из ${doc.numPages}</span><button class="button secondary" disabled=${page===doc.numPages} onClick=${()=>setPage(n=>n+1)}>Следующая страница</button><label>Масштаб PDF <select aria-label="Масштаб PDF" value=${zoom} onChange=${e=>setZoom(Number(e.currentTarget.value))}>${[.5,1,1.5,2].map(n=>html`<option value=${n}>${n*100}%</option>`)}</select></label></div><${PDFPage} key=${page+'-'+zoom} doc=${doc} page=${page} zoom=${zoom} onTimeout=${()=>{setError('Страница слишком сложная для предпросмотра. Скачайте PDF.');doc.destroy().catch(()=>{})}}/>`:html`<p role="status">Загрузка PDF…</p>`}
 </div>`;
}
function PDFPage({doc,page,zoom,onTimeout}) {
 const canvas=useRef(),[busy,setBusy]=useState(true),[error,setError]=useState('');
 useEffect(()=>{
  let active=true,renderTask,pdfPage;
  const timer=setTimeout(()=>{if(active){renderTask?.cancel();onTimeout()}},30000);
  (async()=>{
   try{
    pdfPage=await doc.getPage(page);if(!active)return;
    const original=pdfPage.getViewport({scale:1});
    const viewport=pdfPage.getViewport({scale:pdfScale(original.width,original.height,zoom)});
    canvas.current.width=Math.max(1,Math.floor(viewport.width));canvas.current.height=Math.max(1,Math.floor(viewport.height));
    renderTask=pdfPage.render({canvas:canvas.current,viewport,annotationMode:0});
    await renderTask.promise;
   }catch(e){if(active&&e.name!=='RenderingCancelledException')setError('Не удалось отобразить страницу PDF.')}
   finally{clearTimeout(timer);if(active)setBusy(false);pdfPage?.cleanup()}
  })();
  return()=>{active=false;clearTimeout(timer);renderTask?.cancel()};
 },[]);
 return html`<div class="pdf-page" aria-busy=${busy}>${busy&&html`<p role="status">Отрисовка страницы…</p>`}${error&&html`<p class="form-error" role="alert">${error}</p>`}<canvas ref=${canvas} hidden=${!!error} role="img" aria-label=${'Страница PDF '+page}/></div>`;
}

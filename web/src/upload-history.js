import {dateStamp} from './date-display.js';
import {h} from 'preact';
import {useEffect,useState} from 'preact/hooks';
import htm from 'htm';
import {transferStats} from './transfer-stats.js';
const html=htm.bind(h);
export function UploadHistory({api,formatSize}) {
 const[open,setOpen]=useState(false),[revision,setRevision]=useState(0),[items,setItems]=useState([]),[busy,setBusy]=useState(false),[error,setError]=useState(''),[query,setQuery]=useState(''),[page,setPage]=useState(0);
 useEffect(()=>{
  if(!open)return;
  let active=true;setBusy(true);setError('');
  api('transfers').then(data=>{if(active){setItems(data);setPage(0)}}).catch(e=>{if(active)setError(e.message)}).finally(()=>{if(active)setBusy(false)});
  return()=>{active=false};
 },[open,revision]);
 const[direction,setDirection]=useState('');
 const filtered=items.filter(item=>(!direction||item.direction===direction)&&(item.path+' '+(item.username||'')+' '+(item.share_id||'')).toLocaleLowerCase().includes(query.toLocaleLowerCase()));
 return html`<details class="panel upload-history" onToggle=${e=>setOpen(e.currentTarget.open)}>
  <summary>История передач</summary>
  ${open&&html`<p class="muted">Последние 200 передач: свои, а для администратора — всех пользователей. Частичные скачивания отмечены отдельно. Предпросмотр не записывается. Длительность приблизительная, с учётом пауз и проверки файла; средняя скорость рассчитана за это время.</p>
   <div class="toolbar"><input aria-label="Поиск в истории передач" placeholder="Путь или пользователь" value=${query} onInput=${e=>{setQuery(e.currentTarget.value);setPage(0)}}/><select aria-label="Тип передачи" value=${direction} onChange=${e=>{setDirection(e.currentTarget.value);setPage(0)}}><option value="">Все передачи</option><option value="upload">Загрузки</option><option value="download">Скачивания файлов</option><option value="download_zip">ZIP-скачивания</option><option value="guest_download">Гостевые файлы</option><option value="guest_download_zip">Гостевые ZIP</option></select><button class="button secondary" disabled=${busy} onClick=${()=>setRevision(revision+1)}>Обновить историю</button></div>
   ${error&&html`<p class="form-error" role="alert">${error}</p>`}
   ${busy&&html`<p role="status">Загрузка истории…</p>`}
   <div aria-busy=${busy}>${filtered.slice(page*25,page*25+25).map(item=>html`<div class="upload-row" key=${item.id}><div><strong>${item.path}</strong><p class="muted">${{guest_download:'Гостевое скачивание',guest_download_zip:'Гостевой ZIP',download:'Скачивание',download_zip:'Скачивание ZIP',upload:'Загрузка'}[item.direction]||item.direction} · ${formatSize(item.size)} · ${item.share_id?'Гость · владелец ссылки: ':''}${item.username||'Пользователь #'+item.user_id}${item.share_id?' · ссылка '+item.share_id:''} · ${item.finished?dateStamp(item.finished*1000):'—'}</p><${TransferTiming} item=${item} formatSize=${formatSize}/><span>${{complete:'Завершено',partial:'Часть файла (Range)',interrupted:'Прервано'}[item.status]||item.status}</span>${item.error&&html`<p class="form-error">${item.error}</p>`}</div></div>`)}</div>
   ${!busy&&!error&&!filtered.length&&html`<p class="muted">${query||direction?'Ничего не найдено.':'История пока пуста.'}</p>`}
   ${filtered.length>25&&html`<div class="dialog-actions"><button class="button secondary" disabled=${page===0} onClick=${()=>setPage(page-1)}>Предыдущие записи</button><span>${page*25+1}–${Math.min((page+1)*25,filtered.length)} / ${filtered.length}</span><button class="button secondary" disabled=${(page+1)*25>=filtered.length} onClick=${()=>setPage(page+1)}>Следующие записи</button></div>`}`}
 </details>`;
}

function TransferTiming({item,formatSize}) {
 const {duration,speed}=transferStats(item);
 return html`<p class="muted transfer-timing">Время: ${duration} · Средняя скорость: ${speed===null?'—':speed>0&&speed<1?'менее 1 Б/с':'≈ '+formatSize(speed)+'/с'}</p>`;
}
